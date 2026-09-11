// Package launch executes a parsed command line (docs/CLI_CON_DESIGN.md §4.4):
// it locates or creates the connections a `-con` specification asked for,
// patches existing ones the way DBeaver does, opens them, and reports back in
// services.LaunchOutcome.
//
// Why its own package: internal/cli imports internal/services (ConSpec carries
// a services.ConnectionDraft), so services cannot import cli back. This is the
// one place that sees both sides.
//
// Split of responsibilities, which the whole file follows:
//   - internal/cli parses and translates, does no I/O and never fails softly;
//   - this package does the I/O (files, store, keyring, network) and turns
//     failures into user-visible slugs;
//   - everything the user cannot act on (unsupported DBeaver parameters,
//     untranslatable JDBC properties) is technical English in the log.
package launch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"catdb/internal/cli"
	"catdb/internal/dbdriver"
	"catdb/internal/services"
	"catdb/internal/storage"
)

// Launcher runs launch requests. One per process; main() builds it after the
// services and hands it to SystemService as the pending-launch hook.
type Launcher struct {
	conns   *services.ConnectionService
	store   *storage.Store
	secrets *storage.Secrets

	mu      sync.Mutex
	pending *cli.LaunchRequest
}

// New wires the launcher. All three dependencies are required.
func New(conns *services.ConnectionService, store *storage.Store, secrets *storage.Secrets) *Launcher {
	return &Launcher{conns: conns, store: store, secrets: secrets}
}

// SetPending parks a request parsed during startup. Wails discards events
// emitted before a window is ready, so the cold-start path waits for the
// front-end to pull instead of pushing (docs/CLI_CON_DESIGN.md §4.5). An empty
// request is ignored.
func (l *Launcher) SetPending(req cli.LaunchRequest) {
	if req.IsEmpty() {
		// Argument-level warnings still deserve the log even with no spec.
		logWarnings("cli", req.Warnings)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending = &req
}

// ConsumePendingLaunch executes the parked request and clears it. Returns the
// zero outcome when there is nothing pending, which is the normal case.
// Implements services.PendingLaunch.
func (l *Launcher) ConsumePendingLaunch(ctx context.Context) services.LaunchOutcome {
	l.mu.Lock()
	req := l.pending
	l.pending = nil
	l.mu.Unlock()
	if req == nil {
		return services.LaunchOutcome{}
	}
	return l.ExecuteLaunch(ctx, *req)
}

// ExecuteLaunch processes every specification in order and returns what the
// front-end should do. It never returns an error: a spec that fails contributes
// a LaunchError and the remaining specs are still processed.
func (l *Launcher) ExecuteLaunch(ctx context.Context, req cli.LaunchRequest) services.LaunchOutcome {
	logWarnings("cli", req.Warnings)
	var out services.LaunchOutcome
	for _, spec := range req.Specs {
		l.runSpec(ctx, spec, &out)
	}
	return out
}

// CleanupEphemeral drops the profiles created with `save=false` plus their
// keyring entries. Called at startup to sweep leftovers from an abnormal exit;
// the shutdown pass lives in ConnectionService.ServiceShutdown.
func (l *Launcher) CleanupEphemeral(ctx context.Context) {
	if n := services.CleanupEphemeralConnections(ctx, l.store, l.secrets); n > 0 {
		log.Printf("launch: swept %d leftover ephemeral connection(s)", n)
	}
}

// --- one specification ------------------------------------------------------

func (l *Launcher) runSpec(ctx context.Context, spec cli.ConSpec, out *services.LaunchOutcome) {
	// Never log spec.Raw: it can carry password=… verbatim. DBeaver removed
	// exactly this log line for the same reason (dbeaver#3788).
	logWarnings("cli: -con "+specLabel(spec), spec.Warnings)

	l.loadFileRefs(&spec, out)

	prof, found, err := l.lookup(ctx, spec)
	if err != nil {
		out.Errors = append(out.Errors, services.LaunchError{
			Slug: "lookup-failed", Name: specLabel(spec), Detail: err.Error(),
		})
		return
	}

	var (
		id          string
		dropSecrets bool
	)
	if found {
		id, err = l.patch(ctx, prof, spec)
		if err != nil {
			out.Errors = append(out.Errors, services.LaunchError{
				Slug: "save-failed", Name: prof.Name, Detail: err.Error(),
			})
		}
	} else {
		id, dropSecrets, err = l.create(ctx, spec, out)
	}
	if err != nil || id == "" {
		return
	}
	// savePassword=false: the credentials were needed for the connect below —
	// session.Manager reads them from the keyring and catdb has no in-memory
	// credential cache — so they are dropped again as soon as this spec is
	// done. A later reconnect finds no password, which is what "don't remember
	// it" means. For an ephemeral profile it is belt and braces: the whole
	// entry goes at shutdown anyway.
	if dropSecrets {
		defer func() {
			if err := l.secrets.Save(id, storage.Secret{}); err != nil {
				log.Printf("launch: drop unsaved credentials for %s: %v", id, err)
			}
		}()
	}

	out.ConnectionIDs = append(out.ConnectionIDs, id)
	if spec.OpenConsole {
		out.ConsoleIDs = append(out.ConsoleIDs, id)
	}

	// openConsole implies a connection — DBeaver opens the console on a live
	// datasource (docs/CLI_CON_DESIGN.md §2.4.4).
	if spec.Connect || spec.OpenConsole {
		if err := l.conns.Connect(ctx, id); err != nil {
			out.Errors = append(out.Errors, services.LaunchError{
				Slug: "connect-failed", Name: specLabel(spec), Detail: err.Error(),
			})
		}
	}
}

// loadFileRefs turns the paths internal/cli parked in FileRefs into the file
// CONTENT catdb stores: DBeaver names an SSH key file and pgJDBC names
// certificate files, catdb keeps PEM text. A file it cannot read leaves the
// field empty (an unreadable path would fail far more confusingly later) and
// surfaces one error.
func (l *Launcher) loadFileRefs(spec *cli.ConSpec, out *services.LaunchOutcome) {
	for key, path := range spec.FileRefs {
		data, err := os.ReadFile(expandHome(path))
		if err != nil {
			setDraftPEM(&spec.Draft, key, "")
			out.Errors = append(out.Errors, services.LaunchError{
				Slug: "file-unreadable", Name: path, Detail: err.Error(),
			})
			continue
		}
		setDraftPEM(&spec.Draft, key, string(data))
	}
}

// lookup resolves the spec to an existing profile. Shorthand (`-con "MyConn"`)
// tries the token as an id first and as a name second, which is what DBeaver
// does; the keyed form honours whichever of id/name the spec carried.
func (l *Launcher) lookup(ctx context.Context, spec cli.ConSpec) (storage.ConnectionProfile, bool, error) {
	byID := func(id string) (storage.ConnectionProfile, bool, error) {
		p, err := l.store.GetConnection(ctx, id)
		if errors.Is(err, storage.ErrNotFound) {
			return storage.ConnectionProfile{}, false, nil
		}
		if err != nil {
			return storage.ConnectionProfile{}, false, err
		}
		return p, true, nil
	}

	if spec.Shorthand {
		if spec.Lookup == "" {
			return storage.ConnectionProfile{}, false, nil
		}
		if p, ok, err := byID(spec.Lookup); err != nil || ok {
			return p, ok, err
		}
		return l.byName(ctx, spec.Lookup)
	}

	if spec.ID != "" {
		if p, ok, err := byID(spec.ID); err != nil || ok {
			return p, ok, err
		}
		// A DBeaver workspace id can never match ours (§3.4) — fall through to
		// the name, then to create.
	}
	if spec.Name != "" {
		return l.byName(ctx, spec.Name)
	}
	return storage.ConnectionProfile{}, false, nil
}

// byName finds a profile by name: exact match first, then case-insensitive,
// keeping with the "wide open" rule.
func (l *Launcher) byName(ctx context.Context, name string) (storage.ConnectionProfile, bool, error) {
	all, err := l.store.ListConnections(ctx)
	if err != nil {
		return storage.ConnectionProfile{}, false, err
	}
	for _, p := range all {
		if p.Name == name {
			return p, true, nil
		}
	}
	for _, p := range all {
		if strings.EqualFold(p.Name, name) {
			return p, true, nil
		}
	}
	return storage.ConnectionProfile{}, false, nil
}

// patch applies DBeaver's overwrite semantics (§2.4.2) to an existing profile:
// exactly the keys the specification mentioned are replaced, everything else
// is carried over untouched.
func (l *Launcher) patch(ctx context.Context, prof storage.ConnectionProfile, spec cli.ConSpec) (string, error) {
	groupID := prof.GroupID
	if spec.SetKeys["folder"] && spec.FolderName != "" {
		id, err := l.resolveGroup(ctx, spec.FolderName)
		if err != nil {
			log.Printf("launch: resolve folder %q: %v", spec.FolderName, err)
		} else {
			groupID = id
		}
	}
	if d := spec.Draft.Driver; d != "" && d != prof.Driver {
		log.Printf("launch: connection %q is a %s connection; ignoring driver=%s from the command line",
			prof.Name, prof.Driver, d)
	}
	if spec.SetKeys["savePassword"] && !spec.SavePassword {
		log.Printf("launch: savePassword=false has no effect on the existing connection %q; "+
			"its stored credentials are left alone", prof.Name)
	}

	draft := BuildPatchDraft(prof, spec, groupID)
	saved, err := l.conns.SaveConnection(ctx, draft)
	if err != nil {
		return "", fmt.Errorf("save %q: %w", prof.Name, err)
	}
	return saved.ID, nil
}

// create builds a brand-new profile. Refuses without a driver — that is the
// one hard requirement DBeaver has too (§2.4.3). The bool reports whether the
// stored credentials must be dropped once the caller is done with the profile
// (savePassword=false).
func (l *Launcher) create(ctx context.Context, spec cli.ConSpec, out *services.LaunchOutcome) (string, bool, error) {
	if !spec.Create {
		out.Errors = append(out.Errors, services.LaunchError{Slug: "not-found", Name: specLabel(spec)})
		return "", false, errNotCreated
	}
	if spec.Draft.Driver == "" {
		out.Errors = append(out.Errors, services.LaunchError{Slug: "driver-missing", Name: specLabel(spec)})
		return "", false, errNotCreated
	}

	draft := spec.Draft
	draft.ID = ""
	if draft.Name == "" {
		existing, err := l.store.ListConnections(ctx)
		if err != nil {
			out.Errors = append(out.Errors, services.LaunchError{
				Slug: "save-failed", Name: specLabel(spec), Detail: err.Error(),
			})
			return "", false, err
		}
		draft.Name = GenerateName(draft, namesOf(existing))
	}
	if spec.FolderName != "" {
		id, err := l.resolveGroup(ctx, spec.FolderName)
		if err != nil {
			log.Printf("launch: resolve folder %q: %v", spec.FolderName, err)
		} else {
			draft.GroupID = id
		}
	}
	// save=false → the profile is stored like any other (so the sidebar, the
	// session manager and the query layer need no special case) but flagged for
	// deletion at exit.
	draft.Ephemeral = !spec.Save

	saved, err := l.conns.SaveConnection(ctx, draft)
	if err != nil {
		out.Errors = append(out.Errors, services.LaunchError{
			Slug: "save-failed", Name: draft.Name, Detail: err.Error(),
		})
		return "", false, err
	}
	return saved.ID, !spec.SavePassword, nil
}

// errNotCreated marks "nothing to do, the error is already reported".
var errNotCreated = errors.New("launch: connection not created")

// resolveGroup maps a folder name onto a connection group, creating it when
// absent (§3.1: folder → connection group, look up by name / create).
func (l *Launcher) resolveGroup(ctx context.Context, name string) (string, error) {
	groups, err := l.store.ListGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		if strings.EqualFold(g.Name, name) {
			return g.ID, nil
		}
	}
	g, err := l.store.SaveGroup(ctx, storage.Group{Name: name})
	if err != nil {
		return "", err
	}
	return g.ID, nil
}

// --- pure helpers (unit-tested) --------------------------------------------

// BuildPatchDraft composes the draft that patches prof with the keys spec
// actually set. Membership in SetKeys is what decides, never a zero-value test:
// `port=0` and "no port given" must stay distinguishable.
//
// Secrets follow SaveConnection's rules — a blank password keeps the stored
// one, so an explicitly empty `password=` sets the matching Clear flag instead.
func BuildPatchDraft(prof storage.ConnectionProfile, spec cli.ConSpec, groupID string) services.ConnectionDraft {
	d := services.ConnectionDraft{
		ID:          prof.ID,
		Name:        prof.Name,
		Driver:      prof.Driver,
		GroupID:     groupID,
		Environment: prof.Environment,
		Host:        prof.Host,
		Port:        prof.Port,
		User:        prof.User,
		Database:    prof.Database,
		Params:      cloneParams(prof.Params),
		SSL:         cloneSSL(prof.SSL),
		SSHTunnel:   cloneSSH(prof.SSHTunnel),
	}
	src := spec.Draft

	for key := range spec.SetKeys {
		switch key {
		case "name":
			d.Name = src.Name
		case "host":
			d.Host = src.Host
		case "port":
			d.Port = src.Port
		case "user":
			d.User = src.User
		case "database":
			d.Database = src.Database
		case "password":
			d.Password = src.Password
			d.ClearPassword = src.Password == ""
		case "sshTunnel.password":
			d.SSHPassword = src.SSHPassword
			d.ClearSSHPassword = src.SSHPassword == ""
		case "sshTunnel.privateKeyPass":
			d.SSHKeyPassword = src.SSHKeyPassword
			d.ClearSSHKeyPassword = src.SSHKeyPassword == ""
		default:
			switch {
			case strings.HasPrefix(key, "params."):
				if d.Params == nil {
					d.Params = map[string]string{}
				}
				d.Params[strings.TrimPrefix(key, "params.")] = src.Params[strings.TrimPrefix(key, "params.")]
			case strings.HasPrefix(key, "ssl."):
				patchSSL(&d, src.SSL, strings.TrimPrefix(key, "ssl."))
			case strings.HasPrefix(key, "sshTunnel."):
				patchSSH(&d, src.SSHTunnel, strings.TrimPrefix(key, "sshTunnel."))
			}
		}
	}
	return d
}

func patchSSL(d *services.ConnectionDraft, src *dbdriver.SSLConfig, field string) {
	if src == nil {
		return
	}
	if d.SSL == nil {
		d.SSL = &dbdriver.SSLConfig{}
	}
	switch field {
	case "mode":
		d.SSL.Mode = src.Mode
	case "caCert":
		d.SSL.CACert = src.CACert
	case "clientCert":
		d.SSL.ClientCert = src.ClientCert
	case "clientKey":
		d.SSL.ClientKey = src.ClientKey
	case "serverName":
		d.SSL.ServerName = src.ServerName
	}
}

func patchSSH(d *services.ConnectionDraft, src *dbdriver.SSHConfig, field string) {
	if src == nil {
		return
	}
	if d.SSHTunnel == nil {
		d.SSHTunnel = &dbdriver.SSHConfig{}
	}
	switch field {
	case "host":
		d.SSHTunnel.Host = src.Host
	case "port":
		d.SSHTunnel.Port = src.Port
	case "user":
		d.SSHTunnel.User = src.User
	case "privateKey":
		d.SSHTunnel.PrivateKey = src.PrivateKey
	case "useAgent":
		d.SSHTunnel.UseAgent = src.UseAgent
	case "knownHostsPath":
		d.SSHTunnel.KnownHostsPath = src.KnownHostsPath
	}
}

// GenerateName is DBeaver's fallback connection name: "Ext: <driver> - <target>"
// where target is the database, or the host when there is none. A collision
// appends " (2)", " (3)", … exactly as DBeaver does (§2.4.3).
func GenerateName(d services.ConnectionDraft, taken []string) string {
	target := d.Database
	if target == "" {
		target = d.Host
	}
	base := "Ext: " + d.Driver
	if target != "" {
		base += " - " + target
	}
	used := make(map[string]bool, len(taken))
	for _, n := range taken {
		used[n] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if !used[candidate] {
			return candidate
		}
	}
}

// setDraftPEM writes the content of a referenced file into the draft field the
// dotted key names. Only the PEM-valued fields can be file references.
func setDraftPEM(d *services.ConnectionDraft, dotKey, content string) {
	switch dotKey {
	case "ssl.caCert", "ssl.clientCert", "ssl.clientKey":
		if d.SSL == nil {
			d.SSL = &dbdriver.SSLConfig{}
		}
		switch dotKey {
		case "ssl.caCert":
			d.SSL.CACert = content
		case "ssl.clientCert":
			d.SSL.ClientCert = content
		case "ssl.clientKey":
			d.SSL.ClientKey = content
		}
	case "sshTunnel.privateKey":
		if d.SSHTunnel == nil {
			d.SSHTunnel = &dbdriver.SSHConfig{}
		}
		d.SSHTunnel.PrivateKey = content
	}
}

// expandHome resolves a leading "~" — shells expand it, but a path that
// arrived quoted or through a -vars file did not go through one.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// specLabel is the best human handle for a specification, used as {name} in
// the front-end's error message and as the log prefix. It must never fall back
// to spec.Raw — that can carry password=… verbatim.
func specLabel(spec cli.ConSpec) string {
	switch {
	case spec.Lookup != "":
		return spec.Lookup
	case spec.Name != "":
		return spec.Name
	case spec.ID != "":
		return spec.ID
	case spec.Draft.Database != "":
		return spec.Draft.Database
	case spec.Draft.Host != "":
		return spec.Draft.Host
	case spec.Draft.Driver != "":
		return spec.Draft.Driver
	default:
		return "(unnamed)"
	}
}

func namesOf(profiles []storage.ConnectionProfile) []string {
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p.Name)
	}
	return out
}

func cloneParams(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneSSL(in *dbdriver.SSLConfig) *dbdriver.SSLConfig {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneSSH(in *dbdriver.SSHConfig) *dbdriver.SSHConfig {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func logWarnings(prefix string, warnings []string) {
	for _, w := range warnings {
		log.Printf("%s: %s", prefix, w)
	}
}
