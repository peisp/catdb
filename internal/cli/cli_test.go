package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	// The alias tables live in the drivers, so the registry has to be
	// populated for driver=/url= resolution to work.
	_ "catdb/plugins"
)

func parseOne(t *testing.T, spec string) ConSpec {
	t.Helper()
	req, err := ParseArgs([]string{"-con", spec}, nil)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if len(req.Specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(req.Specs))
	}
	return req.Specs[0]
}

func hasWarning(s []string, substr string) bool {
	for _, w := range s {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestParseArgsNoConIsEmpty(t *testing.T) {
	req, err := ParseArgs([]string{"-nosplash", "somefile.sql"}, nil)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !req.IsEmpty() {
		t.Fatalf("expected an empty launch request, got %+v", req.Specs)
	}
	if len(req.Warnings) != 2 {
		t.Errorf("expected a warning per ignored argument, got %v", req.Warnings)
	}
}

func TestParseArgsFlagForms(t *testing.T) {
	req, err := ParseArgs([]string{
		"-con", "driver=mysql|host=h1",
		"-connect=driver=mysql|host=h2",
		"--datasource-specification", "driver=mysql|host=h3",
		"-ds-spec", "driver=mysql|host=h4",
	}, nil)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	want := []string{"h1", "h2", "h3", "h4"}
	if len(req.Specs) != len(want) {
		t.Fatalf("got %d specs, want %d", len(req.Specs), len(want))
	}
	for i, h := range want {
		if req.Specs[i].Draft.Host != h {
			t.Errorf("spec %d host: got %q want %q", i, req.Specs[i].Draft.Host, h)
		}
	}
}

func TestParseArgsIgnoredAndUnknownFlags(t *testing.T) {
	req, err := ParseArgs([]string{
		"-newInstance", "-bringToFront", "-stop", "-quit", "-closeTabs",
		"-disconnectAll", "-reuseWorkspace", "-nl", "en_US", "-data", "/tmp/ws",
		"-f", "script.sql", "-madeUpFlag",
		"-con", "driver=mysql|host=h1",
	}, nil)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if len(req.Specs) != 1 {
		t.Fatalf("the -con spec must survive the ignored flags, got %d specs", len(req.Specs))
	}
	if !hasWarning(req.Warnings, "-newInstance") || !hasWarning(req.Warnings, "-madeUpFlag") {
		t.Errorf("warnings: %v", req.Warnings)
	}
	// -nl/-data/-f must have swallowed their values rather than leaving them
	// as positional arguments.
	if hasWarning(req.Warnings, "positional") {
		t.Errorf("value-taking ignored flags leaked their value: %v", req.Warnings)
	}
}

func TestParseArgsMissingValueIsAnError(t *testing.T) {
	if _, err := ParseArgs([]string{"-con"}, nil); err == nil {
		t.Fatal("expected an error for -con without a value")
	}
	if _, err := ParseArgs([]string{"-vars"}, nil); err == nil {
		t.Fatal("expected an error for -vars without a value")
	}
}

func TestSpecDefaults(t *testing.T) {
	s := parseOne(t, "driver=mysql|host=h1|user=root")
	if !s.Create || s.Save || !s.Connect || s.OpenConsole || !s.SavePassword {
		t.Errorf("defaults: create=%v save=%v connect=%v openConsole=%v savePassword=%v",
			s.Create, s.Save, s.Connect, s.OpenConsole, s.SavePassword)
	}
	if s.Draft.Driver != "mysql" {
		t.Errorf("driver: got %q", s.Draft.Driver)
	}
}

func TestSpecBehaviorFlags(t *testing.T) {
	s := parseOne(t, "driver=mysql|host=h1|create=false|save=true|connect=false|openConsole=true|savePassword=false")
	if s.Create || !s.Save || s.Connect || !s.OpenConsole || s.SavePassword {
		t.Errorf("flags: %+v", s)
	}
	for _, k := range []string{"create", "save", "connect", "openConsole", "savePassword"} {
		if !s.SetKeys[k] {
			t.Errorf("SetKeys missing %q", k)
		}
	}

	bad := parseOne(t, "driver=mysql|create=maybe")
	if !bad.Create {
		t.Error("an unreadable boolean must leave the default in force")
	}
	if !hasWarning(bad.Warnings, "non-boolean") {
		t.Errorf("warnings: %v", bad.Warnings)
	}
}

func TestSpecShorthand(t *testing.T) {
	s := parseOne(t, "My Connection")
	if !s.Shorthand || s.Lookup != "My Connection" {
		t.Fatalf("shorthand: %+v", s)
	}
	if s.Create {
		t.Error("shorthand must not create")
	}
	if !s.Connect {
		t.Error("shorthand still connects")
	}
}

func TestSpecEscaping(t *testing.T) {
	// \| is a literal pipe, \\ is a literal backslash, every other backslash
	// stays put — which is what keeps Windows paths intact.
	s := parseOne(t, `driver=sqlite|database=C:\Temp\test.db`)
	if s.Draft.Database != `C:\Temp\test.db` {
		t.Errorf("windows path: got %q", s.Draft.Database)
	}
	s = parseOne(t, `driver=sqlite|database=C:\\Temp\\test.db`)
	if s.Draft.Database != `C:\Temp\test.db` {
		t.Errorf("escaped windows path: got %q", s.Draft.Database)
	}
	s = parseOne(t, `driver=mysql|password=a\|b\\c`)
	if s.Draft.Password != `a|b\c` {
		t.Errorf("escaped value: got %q", s.Draft.Password)
	}
}

func TestSpecDriverAliases(t *testing.T) {
	cases := map[string]string{
		"mysql":         "mysql",
		"mysql8":        "mysql",
		"MySQL5":        "mysql",
		"mariadb":       "mariadb",
		"postgresql":    "postgres",
		"postgres-jdbc": "postgres",
		"sqlite_jdbc":   "sqlite",
		"DAMENG":        "dm",
	}
	for alias, want := range cases {
		s := parseOne(t, "driver="+alias+"|host=h1")
		if s.Draft.Driver != want {
			t.Errorf("driver %q: got %q want %q (%v)", alias, s.Draft.Driver, want, s.Warnings)
		}
	}

	s := parseOne(t, "driver=oracle|host=h1")
	if s.Draft.Driver != "" {
		t.Errorf("unknown driver: got %q", s.Draft.Driver)
	}
	if !hasWarning(s.Warnings, "not registered") {
		t.Errorf("warnings: %v", s.Warnings)
	}
}

func TestSpecURL(t *testing.T) {
	s := parseOne(t, "driver=mysql|url=jdbc:mysql://db.example.com:3307/shop|user=root")
	if s.Draft.Host != "db.example.com" || s.Draft.Port != 3307 || s.Draft.Database != "shop" {
		t.Fatalf("url: %+v", s.Draft)
	}
	for _, k := range []string{"host", "port", "database"} {
		if !s.SetKeys[k] {
			t.Errorf("SetKeys missing %q", k)
		}
	}

	// An explicit host/port/database wins over the URL, no matter the order.
	s = parseOne(t, "host=other|port=3310|database=other_db|driver=mysql|url=jdbc:mysql://db.example.com:3307/shop")
	if s.Draft.Host != "other" || s.Draft.Port != 3310 || s.Draft.Database != "other_db" {
		t.Fatalf("explicit keys must win: %+v", s.Draft)
	}

	// driver= after url= must still be picked up (two-pass parsing).
	s = parseOne(t, "url=jdbc:postgresql://pg.example.com/shop|driver=postgresql")
	if s.Draft.Driver != "postgres" || s.Draft.Host != "pg.example.com" || s.Draft.Port != 5432 {
		t.Fatalf("driver after url: %+v", s.Draft)
	}

	// No driver at all: inferred from the URL scheme.
	s = parseOne(t, "url=jdbc:mariadb://mdb.example.com/shop")
	if s.Draft.Driver != "mariadb" || s.Draft.Host != "mdb.example.com" {
		t.Fatalf("inferred driver: %+v (%v)", s.Draft, s.Warnings)
	}

	s = parseOne(t, "driver=mysql|url=not-a-jdbc-url")
	if !hasWarning(s.Warnings, "cannot parse url") {
		t.Errorf("warnings: %v", s.Warnings)
	}
}

func TestSpecURLQueryPropsAreTranslated(t *testing.T) {
	s := parseOne(t, "driver=mysql|url=jdbc:mysql://h1/shop?connectTimeout=30000&sslMode=REQUIRED&allowMultiQueries=true")
	if s.Draft.Params["timeout"] != "30s" {
		t.Errorf("timeout: got %q", s.Draft.Params["timeout"])
	}
	if s.Draft.SSL == nil || s.Draft.SSL.Mode != "require" {
		t.Errorf("ssl: %+v", s.Draft.SSL)
	}
	if !hasWarning(s.Warnings, "allowMultiQueries") {
		t.Errorf("an untranslatable property must warn: %v", s.Warnings)
	}
}

func TestSpecPropPrefix(t *testing.T) {
	s := parseOne(t, "driver=mysql|host=h1|prop.connectTimeout=5000|prop.connectionCollation=utf8mb4_bin|prop.useSSL=false")
	if s.Draft.Params["timeout"] != "5s" || s.Draft.Params["collation"] != "utf8mb4_bin" {
		t.Errorf("params: %v", s.Draft.Params)
	}
	if s.Draft.SSL == nil || s.Draft.SSL.Mode != "disable" {
		t.Errorf("ssl: %+v", s.Draft.SSL)
	}
	if !s.SetKeys["params.timeout"] || !s.SetKeys["ssl.mode"] {
		t.Errorf("SetKeys: %v", s.SetKeys)
	}

	// prop.* overrides the same property coming from the URL query.
	s = parseOne(t, "driver=mysql|url=jdbc:mysql://h1/shop?connectTimeout=30000|prop.connectTimeout=1000")
	if s.Draft.Params["timeout"] != "1s" {
		t.Errorf("prop.* must win over the url query: %q", s.Draft.Params["timeout"])
	}
}

func TestSpecPostgresSSLFilesAreFileRefs(t *testing.T) {
	s := parseOne(t, "driver=postgresql|host=h1|prop.sslrootcert=/etc/ssl/root.crt|prop.sslmode=verify-ca")
	if s.Draft.SSL == nil || s.Draft.SSL.CACert != "/etc/ssl/root.crt" || s.Draft.SSL.Mode != "verify-ca" {
		t.Fatalf("ssl: %+v", s.Draft.SSL)
	}
	if s.FileRefs["ssl.caCert"] != "/etc/ssl/root.crt" {
		t.Errorf("FileRefs must park the certificate path: %v", s.FileRefs)
	}
}

func TestSpecNativeKeysPassThrough(t *testing.T) {
	s := parseOne(t, "driver=mysql|host=h1|params.readTimeout=10s|ssl.mode=verify-full|ssl.serverName=db|sshTunnel.host=jump|sshTunnel.port=2222")
	if s.Draft.Params["readTimeout"] != "10s" {
		t.Errorf("params: %v", s.Draft.Params)
	}
	if s.Draft.SSL.Mode != "verify-full" || s.Draft.SSL.ServerName != "db" {
		t.Errorf("ssl: %+v", s.Draft.SSL)
	}
	if s.Draft.SSHTunnel.Host != "jump" || s.Draft.SSHTunnel.Port != 2222 {
		t.Errorf("ssh: %+v", s.Draft.SSHTunnel)
	}
}

func TestSpecSSHHandler(t *testing.T) {
	s := parseOne(t, strings.Join([]string{
		"driver=mysql", "host=127.0.0.1", "database=shop", "user=root", "password=pw",
		"netHandler.ssh.host=jump.example.com",
		"netHandler.ssh.port=2222",
		"netHandler.ssh.userName=deploy",
		"netHandler.ssh.password=sshpw",
		"netHandler.ssh.authType=PASSWORD",
	}, "|"))
	ssh := s.Draft.SSHTunnel
	if ssh == nil || ssh.Host != "jump.example.com" || ssh.Port != 2222 || ssh.User != "deploy" {
		t.Fatalf("ssh: %+v", ssh)
	}
	if s.Draft.SSHPassword != "sshpw" {
		t.Errorf("the ssh password belongs in the draft's keyring field, got %+v", s.Draft)
	}
	if ssh.Password != "" {
		t.Errorf("the ssh password must not be written onto the tunnel struct: %+v", ssh)
	}
	if ssh.UseAgent {
		t.Error("authType=PASSWORD must not enable the agent")
	}

	// handler.<id>.<k> is the same table.
	s = parseOne(t, "driver=mysql|handler.ssh_tunnel.host=jump|handler.ssh_tunnel.user=deploy")
	if s.Draft.SSHTunnel == nil || s.Draft.SSHTunnel.Host != "jump" || s.Draft.SSHTunnel.User != "deploy" {
		t.Fatalf("handler.*: %+v", s.Draft.SSHTunnel)
	}

	// AGENT switches to ssh-agent auth.
	s = parseOne(t, "driver=mysql|netHandler.ssh.host=jump|netHandler.ssh.authType=AGENT")
	if !s.Draft.SSHTunnel.UseAgent {
		t.Error("authType=AGENT must set useAgent")
	}

	// keyPath names a FILE; the draft holds the path and FileRefs marks it for
	// the launch flow to load (catdb's privateKey is PEM content).
	s = parseOne(t, "driver=mysql|netHandler.ssh.host=jump|netHandler.ssh.authType=PUBLIC_KEY|netHandler.ssh.keyPath=/home/me/.ssh/id_rsa")
	if s.SSHKeyPath() != "/home/me/.ssh/id_rsa" {
		t.Errorf("SSHKeyPath: got %q", s.SSHKeyPath())
	}
	if s.Draft.SSHTunnel.PrivateKey != "/home/me/.ssh/id_rsa" {
		t.Errorf("the path stays in the draft until the launch flow loads it: %+v", s.Draft.SSHTunnel)
	}

	// A handler catdb has no concept of is warned about, not fatal.
	s = parseOne(t, "driver=mysql|host=h1|netHandler.proxy.host=proxy.example.com")
	if s.Draft.Host != "h1" {
		t.Error("an unknown handler must not disturb the rest of the spec")
	}
	if !hasWarning(s.Warnings, "proxy") {
		t.Errorf("warnings: %v", s.Warnings)
	}
}

func TestSpecIgnoredParameters(t *testing.T) {
	s := parseOne(t, strings.Join([]string{
		"driver=mysql", "host=h1",
		"server=prod", "auth=native", "authProp.user=root", "advProp.showViews=true",
		"autoCommit=false", "showSystemObjects=true", "hideFolders=true", "mergeEntities=true",
		"whatIsThis=1",
	}, "|"))
	if s.Draft.Host != "h1" || s.Draft.Driver != "mysql" {
		t.Fatalf("ignored parameters must not disturb the rest: %+v", s.Draft)
	}
	for _, w := range []string{"server", "auth", "authProp.user", "advProp.showViews", "autoCommit", "showSystemObjects", "whatIsThis"} {
		if !hasWarning(s.Warnings, w) {
			t.Errorf("missing warning for %q: %v", w, s.Warnings)
		}
	}
}

func TestSpecIdentityKeys(t *testing.T) {
	s := parseOne(t, "id=abc-123|name=Shop DB|folder=Production|driver=mysql|host=h1")
	if s.ID != "abc-123" || s.Name != "Shop DB" || s.FolderName != "Production" {
		t.Fatalf("identity: %+v", s)
	}
	if s.Draft.Name != "Shop DB" {
		t.Errorf("the draft carries the name too: %q", s.Draft.Name)
	}
}

func TestSpecSetKeysDistinguishZeroFromUnset(t *testing.T) {
	s := parseOne(t, "driver=mysql|host=h1")
	if s.SetKeys["port"] {
		t.Error("port was not given")
	}
	if !s.SetKeys["host"] {
		t.Errorf("SetKeys: %v", s.SetKeys)
	}
	s = parseOne(t, "driver=mysql|database=")
	if !s.SetKeys["database"] {
		t.Error("an explicitly emptied database must be recorded as set")
	}
}

// The real-world samples from the DBeaver command-line documentation.
func TestSpecDBeaverDocumentationSamples(t *testing.T) {
	t.Run("sqlite windows path", func(t *testing.T) {
		s := parseOne(t, `driver=sqlite|database=C:\Temp\test.db|name=SQLite Test|create=true|save=true|connect=true`)
		if s.Draft.Driver != "sqlite" || s.Draft.Database != `C:\Temp\test.db` {
			t.Fatalf("draft: %+v", s.Draft)
		}
		if s.Name != "SQLite Test" || !s.Create || !s.Save || !s.Connect {
			t.Fatalf("spec: %+v", s)
		}
	})

	t.Run("mysql host port user", func(t *testing.T) {
		s := parseOne(t, "driver=mysql|host=192.168.0.10|port=3306|database=test|user=root|password=1234|savePassword=true|openConsole=true")
		if s.Draft.Host != "192.168.0.10" || s.Draft.Port != 3306 || s.Draft.Database != "test" ||
			s.Draft.User != "root" || s.Draft.Password != "1234" {
			t.Fatalf("draft: %+v", s.Draft)
		}
		if !s.SavePassword || !s.OpenConsole {
			t.Fatalf("spec: %+v", s)
		}
	})

	t.Run("url form", func(t *testing.T) {
		s := parseOne(t, "driver=postgres-jdbc|url=jdbc:postgresql://localhost:5432/postgres|user=postgres|password=pgpass|name=PG Local")
		if s.Draft.Driver != "postgres" || s.Draft.Host != "localhost" || s.Draft.Port != 5432 ||
			s.Draft.Database != "postgres" || s.Draft.User != "postgres" {
			t.Fatalf("draft: %+v", s.Draft)
		}
	})
}

func TestParseArgsVariableExpansion(t *testing.T) {
	dir := t.TempDir()
	varsPath := filepath.Join(dir, "vars.properties")
	content := "# catdb test vars\ndb.host = db.example.com\ndb.pass\\:word=s3cr3t\n"
	if err := os.WriteFile(varsPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write vars: %v", err)
	}
	t.Setenv("CATDB_TEST_DB", "shop")

	builtin := NewBuiltinResolver(Environment{Workspace: "/data/catdb", AppVersion: "1.2.3"})
	req, err := ParseArgs([]string{
		"-vars", varsPath,
		"-con", "driver=mysql|host=${db.host}|database=${CATDB_TEST_DB}|password=${db.pass:word}|user=${nope}",
		"-con", "driver=sqlite|database=${workspace}/local.db|name=${application.name} ${application.version}",
	}, builtin)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if req.VarsFile != varsPath {
		t.Errorf("VarsFile: got %q", req.VarsFile)
	}
	if len(req.Specs) != 2 {
		t.Fatalf("got %d specs", len(req.Specs))
	}

	first := req.Specs[0].Draft
	if first.Host != "db.example.com" {
		t.Errorf("vars file: host=%q", first.Host)
	}
	if first.Database != "shop" {
		t.Errorf("environment: database=%q", first.Database)
	}
	if first.Password != "s3cr3t" {
		t.Errorf("escaped properties key: password=%q", first.Password)
	}
	// An unresolved variable stays literal, the way DBeaver leaves it.
	if first.User != "${nope}" {
		t.Errorf("unresolved variable: user=%q", first.User)
	}

	second := req.Specs[1]
	if second.Draft.Database != "/data/catdb/local.db" {
		t.Errorf("builtin workspace: %q", second.Draft.Database)
	}
	if second.Name != "catdb 1.2.3" {
		t.Errorf("builtin app name/version: %q", second.Name)
	}
}

func TestParseArgsBuiltinsWinOverVarsFile(t *testing.T) {
	dir := t.TempDir()
	varsPath := filepath.Join(dir, "vars.properties")
	if err := os.WriteFile(varsPath, []byte("application.name=hijacked\nonly.here=fromfile\n"), 0o600); err != nil {
		t.Fatalf("write vars: %v", err)
	}
	t.Setenv("only.here", "fromenv")

	builtin := NewBuiltinResolver(Environment{})
	req, err := ParseArgs([]string{
		"-variablesFile", varsPath,
		"-con", "driver=mysql|host=h1|name=${application.name}|database=${only.here}",
	}, builtin)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	s := req.Specs[0]
	if s.Name != "catdb" {
		t.Errorf("a built-in must win over the vars file: %q", s.Name)
	}
	if s.Draft.Database != "fromfile" {
		t.Errorf("the vars file must win over the environment: %q", s.Draft.Database)
	}
}

func TestParseArgsMissingVarsFileWarnsOnly(t *testing.T) {
	req, err := ParseArgs([]string{"-vars", filepath.Join(t.TempDir(), "nope.properties"), "-con", "driver=mysql|host=h1"}, nil)
	if err != nil {
		t.Fatalf("a missing variables file must not fail the launch: %v", err)
	}
	if len(req.Specs) != 1 {
		t.Fatalf("got %d specs", len(req.Specs))
	}
	if !hasWarning(req.Warnings, "cannot read variables file") {
		t.Errorf("warnings: %v", req.Warnings)
	}
}
