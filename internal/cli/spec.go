package cli

import (
	"fmt"
	"strconv"
	"strings"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
)

// specPair is one raw key=value token of a specification.
type specPair struct{ key, value string }

// splitSpec splits a `-con` specification on unescaped `|`, mirroring
// DBeaver's splitConnectionSpec: only `\|` and `\\` are escapes, every other
// backslash is a literal one. That is what lets a Windows path ride through
// unquoted — `database=C:\Temp\test.db` keeps its separators because `\T` is
// not an escape sequence.
func splitSpec(spec string) []string {
	var out []string
	var b strings.Builder
	for i := 0; i < len(spec); i++ {
		c := spec[i]
		if c == '\\' && i+1 < len(spec) && (spec[i+1] == '|' || spec[i+1] == '\\') {
			b.WriteByte(spec[i+1])
			i++
			continue
		}
		if c == '|' {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	out = append(out, b.String())
	return out
}

// parseSpec turns one (already variable-expanded) specification into a
// ConSpec. It never fails: anything it cannot use becomes a Warning.
func parseSpec(raw string) ConSpec {
	s := ConSpec{
		Raw:          raw,
		Create:       true,
		Save:         false,
		Connect:      true,
		OpenConsole:  false,
		SavePassword: true,
		SetKeys:      map[string]bool{},
		FileRefs:     map[string]string{},
	}

	tokens := splitSpec(raw)
	if len(tokens) == 1 && !strings.Contains(tokens[0], "=") {
		// Shorthand: an id-or-name to look up. DBeaver never creates from it.
		s.Shorthand = true
		s.Lookup = strings.TrimSpace(tokens[0])
		s.Create = false
		return s
	}

	var pairs []specPair
	for _, tok := range tokens {
		if strings.TrimSpace(tok) == "" {
			continue
		}
		key, value, found := strings.Cut(tok, "=")
		if !found {
			s.warnf("spec token %q is not key=value; ignored", tok)
			continue
		}
		pairs = append(pairs, specPair{strings.TrimSpace(key), value})
	}

	// Pass 1 — resolve the driver before anything else. `driver=` may appear
	// after `url=` or after `prop.*`, and both need the driver to translate,
	// so collecting first and translating second is the only correct order.
	rawDriver := ""
	for _, p := range pairs {
		if strings.EqualFold(p.key, "driver") {
			rawDriver = strings.TrimSpace(p.value)
		}
	}
	name, compat := s.resolveDriver(rawDriver, pairs)
	s.Draft.Driver = name

	// Pass 2 — the URL, before the explicit address keys so that an explicit
	// host/port/database always wins over what the URL carried.
	for _, p := range pairs {
		if !strings.EqualFold(p.key, "url") || strings.TrimSpace(p.value) == "" {
			continue
		}
		s.applyURL(strings.TrimSpace(p.value), compat)
	}

	// Pass 3 — everything else, in spec order (a later key wins).
	for _, p := range pairs {
		s.applyKey(p.key, p.value, compat)
	}
	return s
}

func (s *ConSpec) warnf(format string, a ...any) {
	s.Warnings = append(s.Warnings, fmt.Sprintf(format, a...))
}

// resolveDriver maps DBeaver's driver id onto a registered catdb driver:
// exact registry name first, then the ExternalCompat alias tables
// (case-insensitive). When the spec names no driver — or an unknown one — but
// carries a URL, the driver is inferred from the URL scheme: whichever
// ExternalCompat driver can parse it is the one that owns it.
//
// The returned name is empty when nothing matched; the launch flow decides
// what to do with that (create needs a driver, patching an existing profile
// does not).
func (s *ConSpec) resolveDriver(rawDriver string, pairs []specPair) (string, dbdriver.ExternalCompat) {
	if rawDriver != "" {
		if d, err := registry.Get(rawDriver); err == nil {
			compat, _ := d.(dbdriver.ExternalCompat)
			return d.Name(), compat
		}
		needle := strings.ToLower(rawDriver)
		for _, d := range registry.List() {
			if strings.EqualFold(d.Name(), needle) {
				compat, _ := d.(dbdriver.ExternalCompat)
				return d.Name(), compat
			}
			compat, ok := d.(dbdriver.ExternalCompat)
			if !ok {
				continue
			}
			for _, alias := range compat.ExternalDriverAliases() {
				if strings.EqualFold(alias, needle) {
					return d.Name(), compat
				}
			}
		}
	}

	for _, p := range pairs {
		if !strings.EqualFold(p.key, "url") {
			continue
		}
		url := strings.TrimSpace(p.value)
		if url == "" {
			continue
		}
		for _, d := range registry.List() {
			compat, ok := d.(dbdriver.ExternalCompat)
			if !ok {
				continue
			}
			if _, err := compat.ParseExternalURL(url); err == nil {
				if rawDriver != "" {
					s.warnf("driver %q is not registered; inferred %q from the connection URL", rawDriver, d.Name())
				}
				return d.Name(), compat
			}
		}
	}

	if rawDriver != "" {
		s.warnf("driver %q is not registered; driver-specific parameters cannot be translated", rawDriver)
	}
	return "", nil
}

// applyURL asks the driver to split a foreign connection URL and feeds the
// pieces (plus its query properties) into the draft.
func (s *ConSpec) applyURL(url string, compat dbdriver.ExternalCompat) {
	if compat == nil {
		s.warnf("cannot parse url %q: driver %q has no external-URL support", url, s.Draft.Driver)
		return
	}
	parts, err := compat.ParseExternalURL(url)
	if err != nil {
		s.warnf("cannot parse url %q: %v", url, err)
		return
	}
	if parts.Host != "" {
		s.set("host", parts.Host)
	}
	if parts.Port != "" {
		s.set("port", parts.Port)
	}
	if parts.Database != "" {
		s.set("database", parts.Database)
	}
	for k, v := range parts.Props {
		s.applyForeignProp(k, v, compat, "url")
	}
}

// applyKey dispatches one spec parameter. The order of the checks is the
// dictionary order of docs/CLI_CON_DESIGN.md §2.3: core keys, then the
// prefixed families, then catdb's own dotted keys.
func (s *ConSpec) applyKey(key, value string, compat dbdriver.ExternalCompat) {
	switch strings.ToLower(key) {
	case "driver", "url":
		return // handled in passes 1 and 2
	case "id":
		s.ID = strings.TrimSpace(value)
		s.SetKeys["id"] = true
		return
	case "name":
		s.Name = value
		s.Draft.Name = value
		s.SetKeys["name"] = true
		return
	case "folder":
		s.FolderName = value
		s.SetKeys["folder"] = true
		return
	case "host", "port", "database", "user", "password":
		s.set(strings.ToLower(key), value)
		return
	case "savepassword":
		s.setBool("savePassword", value, &s.SavePassword)
		return
	case "create":
		s.setBool("create", value, &s.Create)
		return
	case "save":
		s.setBool("save", value, &s.Save)
		return
	case "connect":
		s.setBool("connect", value, &s.Connect)
		return
	case "openconsole":
		s.setBool("openConsole", value, &s.OpenConsole)
		return

	// Recognized, deliberately without effect (docs/CLI_CON_DESIGN.md §3.3).
	case "server", "auth", "autocommit",
		"showsystemobjects", "showutilityobjects", "showonlyentities",
		"hidefolders", "hideschemas", "mergeentities":
		s.warnf("parameter %q has no catdb equivalent; ignored", key)
		return
	}

	switch {
	case hasPrefixFold(key, "prop."):
		s.applyForeignProp(key[len("prop."):], value, compat, "prop")
	case hasPrefixFold(key, "advprop."), hasPrefixFold(key, "authprop."):
		s.warnf("parameter %q belongs to DBeaver's provider/auth model; ignored", key)
	case hasPrefixFold(key, "nethandler."):
		s.applyHandler(key, key[len("netHandler."):], value)
	case hasPrefixFold(key, "handler."):
		s.applyHandler(key, key[len("handler."):], value)
	case hasPrefixFold(key, "params."), hasPrefixFold(key, "ssl."), hasPrefixFold(key, "sshtunnel."):
		// catdb's own vocabulary passes straight through.
		s.set(key, value)
	default:
		s.warnf("unknown parameter %q; ignored", key)
	}
}

// applyForeignProp routes one foreign connection property through the driver's
// translation table. origin is only used to make the warning readable.
func (s *ConSpec) applyForeignProp(key, value string, compat dbdriver.ExternalCompat, origin string) {
	if compat == nil {
		s.warnf("cannot translate %s property %q: no driver resolved", origin, key)
		return
	}
	dotKey, val, ok := compat.TranslateExternalProp(key, value)
	if !ok {
		s.warnf("%s property %q has no catdb equivalent for driver %q; ignored", origin, key, s.Draft.Driver)
		return
	}
	s.set(dotKey, val)
	// pgJDBC names certificate FILES where catdb stores PEM content.
	switch dotKey {
	case "ssl.caCert", "ssl.clientCert", "ssl.clientKey":
		s.FileRefs[dotKey] = val
	}
}

// sshHandlerKeys maps DBeaver's SSH tunnel handler properties onto catdb's
// sshTunnel.* keys. The SSH tunnel is a catdb-generic capability (every driver
// gets the same sshTunnel.* schema block from internal/tunnel), so unlike the
// JDBC vocabulary this table belongs in the generic layer — see
// docs/CLI_CON_DESIGN.md §3.2.
var sshHandlerKeys = map[string]string{
	"host":     "sshTunnel.host",
	"port":     "sshTunnel.port",
	"user":     "sshTunnel.user",
	"username": "sshTunnel.user",
	"password": "sshTunnel.password",
}

// applyHandler handles `netHandler.<id>.<k>` and `handler.<id>.<k>`. Only the
// SSH tunnel has a catdb counterpart; naming it is enough to enable it, the
// same way filling in sshTunnel.host does in the connection form.
func (s *ConSpec) applyHandler(fullKey, rest, value string) {
	id, prop, found := strings.Cut(rest, ".")
	if !found || prop == "" {
		s.warnf("malformed handler parameter %q; ignored", fullKey)
		return
	}
	if !strings.Contains(strings.ToLower(id), "ssh") {
		s.warnf("network handler %q has no catdb equivalent; ignored", id)
		return
	}

	lower := strings.ToLower(prop)
	if dotKey, ok := sshHandlerKeys[lower]; ok {
		s.set(dotKey, value)
		return
	}
	switch lower {
	case "keypath":
		// catdb's sshTunnel.privateKey holds the PEM TEXT (internal/tunnel
		// passes it to ssh.ParsePrivateKey), DBeaver names the key FILE. This
		// package does no I/O: park the path and let the launch flow load it.
		s.set("sshTunnel.privateKey", value)
		s.FileRefs["sshTunnel.privateKey"] = value
	case "authtype":
		switch strings.ToUpper(strings.TrimSpace(value)) {
		case "PASSWORD", "PUBLIC_KEY":
			// No action: the password / key path carries the information.
		case "AGENT":
			s.set("sshTunnel.useAgent", "true")
		default:
			s.warnf("unknown ssh authType %q; ignored", value)
		}
	case "enabled":
		// Implied: configuring the handler enables the tunnel.
	default:
		s.warnf("ssh handler property %q has no catdb equivalent; ignored", prop)
	}
}

// setBool parses a DBeaver boolean into dst, warning on anything unreadable
// (the default stays in force). key is the canonical parameter name recorded
// in SetKeys.
func (s *ConSpec) setBool(key, value string, dst *bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		*dst = true
	case "false", "0", "no", "off":
		*dst = false
	default:
		s.warnf("parameter %q has a non-boolean value %q; ignored", key, value)
		return
	}
	s.SetKeys[key] = true
}

// set writes one catdb connection key (dotted form) into the draft and records
// its CANONICAL spelling in SetKeys, so the launch flow can test membership
// without worrying how the user cased the key. An unknown key is warned about,
// not written.
func (s *ConSpec) set(dotKey, value string) {
	canon := strings.ToLower(dotKey)
	switch canon {
	case "host":
		s.Draft.Host = value
	case "port":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			s.warnf("port %q is not a number; ignored", value)
			return
		}
		s.Draft.Port = n
	case "user":
		s.Draft.User = value
	case "password":
		s.Draft.Password = value
	case "database":
		s.Draft.Database = value
	default:
		if k, ok := cutPrefixFold(dotKey, "params."); ok {
			// Driver param names are case-sensitive — keep them verbatim.
			if s.Draft.Params == nil {
				s.Draft.Params = map[string]string{}
			}
			s.Draft.Params[k] = value
			canon = "params." + k
			break
		}
		if k, ok := cutPrefixFold(dotKey, "ssl."); ok {
			field, ok := s.setSSL(k, value)
			if !ok {
				s.warnf("unknown ssl option %q; ignored", k)
				return
			}
			canon = "ssl." + field
			break
		}
		if k, ok := cutPrefixFold(dotKey, "sshTunnel."); ok {
			field, ok := s.setSSH(k, value)
			if !ok {
				s.warnf("unknown ssh option %q; ignored", k)
				return
			}
			if field == "" {
				return // recognized key, unusable value — already warned
			}
			canon = "sshTunnel." + field
			break
		}
		s.warnf("unknown connection key %q; ignored", dotKey)
		return
	}
	s.SetKeys[canon] = true
}

// setSSL writes one ssl.* field and returns its canonical name.
func (s *ConSpec) setSSL(key, value string) (string, bool) {
	if s.Draft.SSL == nil {
		s.Draft.SSL = &dbdriver.SSLConfig{}
	}
	switch strings.ToLower(key) {
	case "mode":
		s.Draft.SSL.Mode = value
		return "mode", true
	case "cacert":
		s.Draft.SSL.CACert = value
		return "caCert", true
	case "clientcert":
		s.Draft.SSL.ClientCert = value
		return "clientCert", true
	case "clientkey":
		s.Draft.SSL.ClientKey = value
		return "clientKey", true
	case "servername":
		s.Draft.SSL.ServerName = value
		return "serverName", true
	}
	return "", false
}

// setSSH writes one sshTunnel.* field. The two secrets do NOT live on the
// tunnel struct in a draft: ConnectionService keeps them in the dedicated
// SSHPassword / SSHKeyPassword fields so they end up in the keyring instead of
// the SQLite blob (see cloneSSHWithoutSecrets).
func (s *ConSpec) setSSH(key, value string) (string, bool) {
	lower := strings.ToLower(key)
	if lower == "password" {
		s.Draft.SSHPassword = value
		return "password", true
	}
	if lower == "privatekeypass" {
		s.Draft.SSHKeyPassword = value
		return "privateKeyPass", true
	}
	if s.Draft.SSHTunnel == nil {
		s.Draft.SSHTunnel = &dbdriver.SSHConfig{}
	}
	switch lower {
	case "host":
		s.Draft.SSHTunnel.Host = value
		return "host", true
	case "port":
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			s.warnf("ssh port %q is not a number; ignored", value)
			return "", true // recognized key, unusable value
		}
		s.Draft.SSHTunnel.Port = n
		return "port", true
	case "user":
		s.Draft.SSHTunnel.User = value
		return "user", true
	case "privatekey":
		s.Draft.SSHTunnel.PrivateKey = value
		return "privateKey", true
	case "useagent":
		s.Draft.SSHTunnel.UseAgent = strings.EqualFold(strings.TrimSpace(value), "true") ||
			strings.TrimSpace(value) == "1"
		return "useAgent", true
	case "knownhostspath":
		s.Draft.SSHTunnel.KnownHostsPath = value
		return "knownHostsPath", true
	}
	return "", false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if !hasPrefixFold(s, prefix) {
		return "", false
	}
	return s[len(prefix):], true
}
