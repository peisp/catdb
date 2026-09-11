// Package cli parses catdb's command line, in particular DBeaver's `-con`
// connection specification (docs/CLI_CON_DESIGN.md).
//
// The package is pure parsing: it produces a LaunchRequest describing what the
// user asked for and never touches the Wails application, the connection
// store, or the network. Acting on the request (lookup / create / connect) is
// the launch flow's job.
//
// Two rules shape the whole package:
//
//   - Wide open. A parameter catdb has no concept for produces a Warning and
//     is dropped; it must never fail a connection that would otherwise work.
//     Errors are reserved for a command line that cannot be read at all.
//   - No dialect knowledge. Foreign driver ids, JDBC URLs and JDBC property
//     names are translated by the driver itself through the optional
//     dbdriver.ExternalCompat extension (CLAUDE.md #12). What this package
//     knows are catdb's own keys and the SSH tunnel, which is a catdb-generic
//     capability.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"catdb/internal/services"
)

// LaunchRequest is everything the command line asked for. An empty one (no
// Specs) means "just start the GUI".
type LaunchRequest struct {
	// Specs holds one entry per -con occurrence, in command-line order.
	Specs []ConSpec
	// VarsFile is the -vars file path, as given (for logging).
	VarsFile string
	// WorkDir is the directory relative paths on this command line resolve
	// against: the process working directory on a cold start, the SECOND
	// instance's working directory when the command line was forwarded
	// (SecondInstanceData.WorkingDir) — the first instance's cwd would be
	// meaningless to the user who typed the path.
	WorkDir string
	// Locale is the -nl value ("zh", "zh_CN", "en", …), empty when absent.
	// It only picks the language of the launch-time authentication prompt
	// (docs/CLI_CON_DESIGN.md §6.3) — the application's own UI locale is a
	// user setting and is never touched by the command line.
	Locale string
	// Warnings are argument-level diagnostics (recognized-but-unimplemented
	// flags, unknown flags). Technical log text, deliberately English:
	// CLAUDE.md's i18n rule covers user-visible copy, not launch logs.
	Warnings []string
}

// IsEmpty reports whether the command line carried no connection request.
func (r LaunchRequest) IsEmpty() bool { return len(r.Specs) == 0 }

// ConSpec is one parsed `-con` specification.
type ConSpec struct {
	// Raw is the spec as it appeared on the command line, after ${…}
	// expansion. It can carry password=… verbatim — never log or display it;
	// use a derived label instead.
	Raw string

	// Shorthand marks the `-con "MyConn"` form: a bare token that is neither
	// key nor value but an existing connection's id or name. DBeaver looks it
	// up and fails if it is not there — it never creates from shorthand, which
	// is why Create defaults to false in this mode.
	Shorthand bool
	// Lookup is the shorthand token (id-or-name).
	Lookup string

	ID         string // id=    — exact profile id
	Name       string // name=  — profile name (lookup key, and the name to create with)
	FolderName string // folder= — connection group name

	// Draft is the translated connection, ready for ConnectionService.
	Draft services.ConnectionDraft

	// Behavior flags, defaults per DBeaver: create=true, save=false,
	// connect=true, openConsole=false, savePassword=true.
	Create       bool
	Save         bool
	Connect      bool
	OpenConsole  bool
	SavePassword bool

	// SetKeys records every catdb key the spec actually set, in dotted form
	// ("host", "params.timeout", "sshTunnel.user", …). DBeaver's patch
	// semantics (§2.4.2) overwrite exactly the fields the spec mentions on an
	// existing profile, so the launch flow must consult this set rather than
	// test the draft for zero values — `port=0` and "port not given" are
	// different things.
	SetKeys map[string]bool

	// FileRefs maps a dotted key to a LOCAL FILE PATH whose CONTENT belongs in
	// that field. Foreign tools name a file where catdb stores the bytes:
	// DBeaver's ssh `keyPath` versus catdb's sshTunnel.privateKey (PEM text —
	// internal/tunnel feeds it straight to ssh.ParsePrivateKey), pgJDBC's
	// sslrootcert/sslcert/sslkey versus catdb's PEM-valued ssl.* fields.
	// This package does no I/O, so it parks the path here and the launch flow
	// reads the file. The draft field itself holds the path until then.
	FileRefs map[string]string

	// Warnings are this spec's diagnostics (untranslatable properties,
	// ignored parameters). English, technical.
	Warnings []string
}

// SSHKeyPath is the private-key file the spec named (DBeaver's
// netHandler.ssh.keyPath), empty when it named none. See FileRefs.
func (s ConSpec) SSHKeyPath() string { return s.FileRefs["sshTunnel.privateKey"] }

// flagKind classifies one recognized command-line flag.
type flagKind int

const (
	flagCon flagKind = iota // takes a value, repeatable
	flagVars
	flagLocale       // -nl, takes a value: the authentication prompt's language
	flagIgnoredBare  // recognized, no value, not implemented
	flagIgnoredValue // recognized, consumes a value, not implemented
)

// knownFlags is keyed by the lowercased flag name with its leading dashes
// stripped. The ignored entries exist so a DBeaver command line pasted whole
// produces warnings instead of "unknown flag" noise or a parse failure — see
// docs/CLI_CON_DESIGN.md §2.1 and §3.4.
var knownFlags = map[string]flagKind{
	"con":                      flagCon,
	"connect":                  flagCon,
	"ds-spec":                  flagCon,
	"datasource-specification": flagCon,

	"vars":          flagVars,
	"variablesfile": flagVars,

	"newinstance":    flagIgnoredBare,
	"bringtofront":   flagIgnoredBare,
	"stop":           flagIgnoredBare,
	"quit":           flagIgnoredBare,
	"closetabs":      flagIgnoredBare,
	"disconnectall":  flagIgnoredBare,
	"reuseworkspace": flagIgnoredBare,
	"nosplash":       flagIgnoredBare,

	"nl": flagLocale,

	"f":    flagIgnoredValue,
	"file": flagIgnoredValue,
	"data": flagIgnoredValue,
}

// ParseArgs parses the process arguments (os.Args[1:]).
//
// builtin is the first variable layer — the `${home}` / `${workspace}` / …
// names, normally NewBuiltinResolver(env); nil skips it. ParseArgs then
// layers the -vars file and finally the process environment on top, which is
// DBeaver's resolution order (built-ins → vars file → environment).
//
// workDir is where relative paths on this command line resolve — see
// LaunchRequest.WorkDir; "" means the process working directory.
//
// An error means the command line itself is unreadable (a value-taking flag
// with no value). Everything softer is a Warning: the caller logs those and
// starts normally.
func ParseArgs(args []string, builtin VarResolver, workDir string) (LaunchRequest, error) {
	req := LaunchRequest{WorkDir: workDir}
	var rawSpecs []string

	warn := func(format string, a ...any) {
		req.Warnings = append(req.Warnings, fmt.Sprintf(format, a...))
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			warn("positional argument %q is not supported; ignored", arg)
			continue
		}

		name, inlineValue, hasInline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		kind, known := knownFlags[strings.ToLower(name)]
		if !known {
			warn("unknown flag %q ignored", arg)
			continue
		}

		// Value-taking flags accept both `-con value` and `-con=value`;
		// DBeaver's picocli command line uses the former.
		takesValue := kind == flagCon || kind == flagVars || kind == flagLocale || kind == flagIgnoredValue
		value := inlineValue
		if takesValue && !hasInline {
			if i+1 >= len(args) {
				if kind == flagIgnoredValue {
					warn("flag %q is recognized but not implemented; ignored", arg)
					continue
				}
				if kind == flagLocale {
					// Wide open: a dangling -nl only costs the prompt its
					// language, it must not abort the launch.
					warn("flag %q has no value; ignored", arg)
					continue
				}
				return LaunchRequest{}, fmt.Errorf("cli: flag %q requires a value", arg)
			}
			i++
			value = args[i]
		}

		switch kind {
		case flagCon:
			if strings.TrimSpace(value) == "" {
				warn("flag %q has an empty specification; ignored", arg)
				continue
			}
			rawSpecs = append(rawSpecs, value)
		case flagVars:
			req.VarsFile = value
		case flagLocale:
			req.Locale = value
		case flagIgnoredBare, flagIgnoredValue:
			warn("flag %q is recognized but not implemented; ignored", arg)
		}
	}

	vars := map[string]string{}
	if req.VarsFile != "" {
		data, err := os.ReadFile(AbsAgainst(workDir, req.VarsFile))
		if err != nil {
			// Wide open: a missing variables file must not abort the launch,
			// the unresolved ${…} stay literal and the failure is visible.
			warn("cannot read variables file %q: %v", req.VarsFile, err)
		} else {
			vars = parseProperties(string(data))
		}
	}
	resolve := chainResolvers(builtin, mapResolver(vars), envResolver())

	for _, raw := range rawSpecs {
		req.Specs = append(req.Specs, parseSpec(expandVars(raw, resolve)))
	}
	return req, nil
}

// AbsAgainst resolves a relative path against base ("" = the process working
// directory, i.e. the path is returned as is). Absolute paths pass through.
func AbsAgainst(base, path string) string {
	if base == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}
