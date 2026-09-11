package cli

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VarResolver resolves one `${name}` reference. ok=false means "this layer
// does not define the name" and hands the lookup to the next layer.
type VarResolver interface {
	Resolve(name string) (string, bool)
}

// ResolverFunc adapts a plain function to VarResolver.
type ResolverFunc func(name string) (string, bool)

// Resolve implements VarResolver.
func (f ResolverFunc) Resolve(name string) (string, bool) { return f(name) }

// Environment carries the runtime-dependent values behind the built-in
// variables. The fields this package cannot compute on its own (the data
// directory, the build version) are injected by the caller — internal/cli
// must not reach into internal/storage or the build metadata itself.
type Environment struct {
	Home       string // ${home}          — defaults to the OS user home
	Workspace  string // ${workspace}     — catdb's data directory (injected)
	AppName    string // ${application.name}    — defaults to "catdb"
	AppVersion string // ${application.version} — build version (injected)
	AppPath    string // ${application.path} / ${dbeaver_home} — defaults to the executable's directory
	LocalIP    string // ${local.ip}      — defaults to the first non-loopback IPv4
}

// NewBuiltinResolver is the built-in variable layer, mirroring DBeaver's
// SystemVariablesResolver (docs/CLI_CON_DESIGN.md §2.5/§3.5). Fields left
// empty in env are filled in from the OS where that is possible; names that
// still resolve to an empty string are reported as unresolved so the lookup
// falls through to the -vars file and the environment.
func NewBuiltinResolver(env Environment) VarResolver {
	if env.Home == "" {
		env.Home, _ = os.UserHomeDir()
	}
	if env.AppName == "" {
		env.AppName = "catdb"
	}
	if env.AppPath == "" {
		if exe, err := os.Executable(); err == nil {
			env.AppPath = filepath.Dir(exe)
		}
	}
	if env.LocalIP == "" {
		env.LocalIP = localIP()
	}
	vars := map[string]string{
		"home":                env.Home,
		"workspace":           env.Workspace,
		"application.name":    env.AppName,
		"application.version": env.AppVersion,
		"application.path":    env.AppPath,
		"dbeaver_home":        env.AppPath,
		"local.ip":            env.LocalIP,
	}
	return ResolverFunc(func(name string) (string, bool) {
		v, ok := vars[name]
		if !ok || v == "" {
			return "", false
		}
		return v, true
	})
}

// chainResolvers consults the layers in order and returns the first hit.
func chainResolvers(layers ...VarResolver) VarResolver {
	return ResolverFunc(func(name string) (string, bool) {
		for _, l := range layers {
			if l == nil {
				continue
			}
			if v, ok := l.Resolve(name); ok {
				return v, true
			}
		}
		return "", false
	})
}

func mapResolver(m map[string]string) VarResolver {
	return ResolverFunc(func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	})
}

func envResolver() VarResolver {
	return ResolverFunc(os.LookupEnv)
}

// expandVars replaces every `${name}` with its resolved value. An unresolved
// name is left in the text verbatim — that is what DBeaver's
// GeneralUtils.replaceVariables does, and it keeps a typo visible instead of
// silently producing an empty host.
func expandVars(s string, resolve VarResolver) string {
	if resolve == nil || !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		start := strings.Index(s[i:], "${")
		if start < 0 {
			b.WriteString(s[i:])
			break
		}
		start += i
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		end += start
		b.WriteString(s[i:start])
		name := s[start+2 : end]
		if v, ok := resolve.Resolve(name); ok {
			b.WriteString(v)
		} else {
			b.WriteString(s[start : end+1])
		}
		i = end + 1
	}
	return b.String()
}

// localIP returns the first non-loopback IPv4 address, or "" when there is
// none (offline machine).
func localIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// parseProperties parses a java.util.Properties text file — the format
// DBeaver's -vars flag takes. Implemented here rather than pulled from an ini
// library because the two formats differ in ways that silently corrupt values
// (':' as a separator, backslash escapes, line continuations).
//
// Supported, matching java.util.Properties#load:
//   - `key=value`, `key:value`, and `key value` (whitespace separator)
//   - `#` and `!` line comments (leading whitespace allowed)
//   - a trailing odd backslash continues the logical line; the continuation's
//     leading whitespace is dropped
//   - escapes \t \n \r \f \\ \" \' \uXXXX, and \<other> → <other>, which is how
//     an escaped separator (\= \: \<space>) stays inside a key
//   - leading whitespace and the whitespace around the separator are dropped;
//     trailing whitespace in a value is KEPT (Java does too)
func parseProperties(text string) map[string]string {
	out := map[string]string{}
	lines := splitPropertyLines(text)
	for i := 0; i < len(lines); {
		line := strings.TrimLeft(lines[i], " \t\f")
		i++
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		for hasContinuation(line) {
			line = line[:len(line)-1]
			if i >= len(lines) {
				break
			}
			line += strings.TrimLeft(lines[i], " \t\f")
			i++
		}
		key, value := splitProperty(line)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func splitPropertyLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// hasContinuation reports whether the line ends with an ODD number of
// backslashes — an even count is an escaped backslash, not a continuation.
func hasContinuation(line string) bool {
	n := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// splitProperty locates the key/value separator, honoring backslash escapes,
// and unescapes both halves.
func splitProperty(line string) (string, string) {
	sep, valStart := -1, len(line)
	for i := 0; i < len(line); {
		c := line[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c == ' ' || c == '\t' || c == '\f' || c == '=' || c == ':' {
			sep = i
			j := i
			if c == '=' || c == ':' {
				j++
			} else {
				for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\f') {
					j++
				}
				if j < len(line) && (line[j] == '=' || line[j] == ':') {
					j++
				}
			}
			for j < len(line) && (line[j] == ' ' || line[j] == '\t' || line[j] == '\f') {
				j++
			}
			valStart = j
			break
		}
		i++
	}
	if sep < 0 {
		return unescapeProperty(line), ""
	}
	return unescapeProperty(line[:sep]), unescapeProperty(line[valStart:])
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			break // dangling backslash — Java drops it too
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 < len(s) {
				if n, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(n))
					i += 4
					continue
				}
			}
			b.WriteByte('u')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
