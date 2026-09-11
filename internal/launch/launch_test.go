package launch

import (
	"path/filepath"
	"testing"

	"catdb/internal/cli"
	"catdb/internal/dbdriver"
	"catdb/internal/services"
	"catdb/internal/storage"

	// resolveFileDatabase consults the live registry for driver capabilities.
	_ "catdb/plugins"
)

func TestGenerateName(t *testing.T) {
	tests := []struct {
		name  string
		draft services.ConnectionDraft
		taken []string
		want  string
	}{
		{
			name:  "database wins over host",
			draft: services.ConnectionDraft{Driver: "mysql", Host: "db.example", Database: "shop"},
			want:  "Ext: mysql - shop",
		},
		{
			name:  "host is the fallback target",
			draft: services.ConnectionDraft{Driver: "postgres", Host: "db.example"},
			want:  "Ext: postgres - db.example",
		},
		{
			name:  "neither: driver only",
			draft: services.ConnectionDraft{Driver: "sqlite"},
			want:  "Ext: sqlite",
		},
		{
			name:  "collision appends (2)",
			draft: services.ConnectionDraft{Driver: "mysql", Database: "shop"},
			taken: []string{"Ext: mysql - shop"},
			want:  "Ext: mysql - shop (2)",
		},
		{
			name:  "collision walks up to a free suffix",
			draft: services.ConnectionDraft{Driver: "mysql", Database: "shop"},
			taken: []string{"Ext: mysql - shop", "Ext: mysql - shop (2)", "Ext: mysql - shop (3)"},
			want:  "Ext: mysql - shop (4)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GenerateName(tc.draft, tc.taken); got != tc.want {
				t.Errorf("GenerateName = %q, want %q", got, tc.want)
			}
		})
	}
}

// baseProfile is a fully populated existing connection — the patch tests check
// that every field the spec did NOT mention survives untouched.
func baseProfile() storage.ConnectionProfile {
	return storage.ConnectionProfile{
		ID:          "conn-1",
		Name:        "prod shop",
		Driver:      "mysql",
		GroupID:     "grp-1",
		Environment: "prod",
		Host:        "db.example",
		Port:        3306,
		User:        "app",
		Database:    "shop",
		Params:      map[string]string{"timeout": "5s", "collation": "utf8mb4_general_ci"},
		SSL:         &dbdriver.SSLConfig{Mode: "require", CACert: "OLD-CA"},
		SSHTunnel:   &dbdriver.SSHConfig{Host: "bastion", Port: 22, User: "jump"},
	}
}

func specWith(setKeys map[string]bool, draft services.ConnectionDraft) cli.ConSpec {
	return cli.ConSpec{SetKeys: setKeys, Draft: draft}
}

func TestBuildPatchDraftOnlyTouchesMentionedKeys(t *testing.T) {
	prof := baseProfile()
	spec := specWith(
		map[string]bool{"user": true, "database": true},
		services.ConnectionDraft{User: "readonly", Database: "shop_replica"},
	)

	d := BuildPatchDraft(prof, spec, prof.GroupID)

	if d.ID != "conn-1" || d.Driver != "mysql" || d.Environment != "prod" {
		t.Errorf("identity/driver/environment must be carried over: %+v", d)
	}
	if d.User != "readonly" || d.Database != "shop_replica" {
		t.Errorf("mentioned keys not applied: user=%q database=%q", d.User, d.Database)
	}
	if d.Host != "db.example" || d.Port != 3306 || d.Name != "prod shop" {
		t.Errorf("unmentioned keys must survive: %+v", d)
	}
	if d.Params["timeout"] != "5s" || d.SSL.Mode != "require" || d.SSHTunnel.User != "jump" {
		t.Errorf("nested structures must survive: %+v", d)
	}
}

func TestBuildPatchDraftZeroValuesAreRealValues(t *testing.T) {
	// `port=0` is a value the user set; it must overwrite. That is exactly why
	// the patch consults SetKeys instead of testing the draft for zero.
	prof := baseProfile()
	spec := specWith(map[string]bool{"port": true}, services.ConnectionDraft{Port: 0})

	if d := BuildPatchDraft(prof, spec, prof.GroupID); d.Port != 0 {
		t.Errorf("port = %d, want 0 (explicitly set)", d.Port)
	}
	// And the opposite: an unset port keeps the stored one.
	spec = specWith(map[string]bool{"user": true}, services.ConnectionDraft{Port: 0, User: "x"})
	if d := BuildPatchDraft(prof, spec, prof.GroupID); d.Port != 3306 {
		t.Errorf("port = %d, want the stored 3306", d.Port)
	}
}

func TestBuildPatchDraftSecrets(t *testing.T) {
	prof := baseProfile()

	// A password the spec carries is applied, and must NOT set the Clear flag —
	// SaveConnection would then wipe it instead of storing it.
	d := BuildPatchDraft(prof,
		specWith(map[string]bool{"password": true}, services.ConnectionDraft{Password: "s3cret"}),
		prof.GroupID)
	if d.Password != "s3cret" || d.ClearPassword {
		t.Errorf("password=%q clear=%v, want the value and no clear flag", d.Password, d.ClearPassword)
	}

	// An explicitly EMPTY password means "remove it". Without the Clear flag,
	// SaveConnection's merge would silently restore the keyring value.
	d = BuildPatchDraft(prof,
		specWith(map[string]bool{"password": true}, services.ConnectionDraft{Password: ""}),
		prof.GroupID)
	if d.Password != "" || !d.ClearPassword {
		t.Errorf("empty password should set ClearPassword, got %+v", d)
	}

	// A spec that never mentions the password leaves both alone, so the stored
	// secret is kept.
	d = BuildPatchDraft(prof, specWith(map[string]bool{"host": true}, services.ConnectionDraft{Host: "h"}), prof.GroupID)
	if d.Password != "" || d.ClearPassword {
		t.Errorf("untouched password must stay blank without a clear flag, got %+v", d)
	}

	// The SSH secrets take the same route (dedicated draft fields → keyring).
	d = BuildPatchDraft(prof,
		specWith(map[string]bool{"sshTunnel.password": true, "sshTunnel.privateKeyPass": true},
			services.ConnectionDraft{SSHPassword: "jump-pw", SSHKeyPassword: ""}),
		prof.GroupID)
	if d.SSHPassword != "jump-pw" || d.ClearSSHPassword {
		t.Errorf("ssh password not applied: %+v", d)
	}
	if !d.ClearSSHKeyPassword {
		t.Error("empty private-key passphrase should set ClearSSHKeyPassword")
	}
}

func TestBuildPatchDraftNestedKeys(t *testing.T) {
	prof := baseProfile()
	spec := specWith(
		map[string]bool{"params.timeout": true, "ssl.mode": true, "sshTunnel.port": true},
		services.ConnectionDraft{
			Params:    map[string]string{"timeout": "30s"},
			SSL:       &dbdriver.SSLConfig{Mode: "verify-full"},
			SSHTunnel: &dbdriver.SSHConfig{Port: 2222},
		},
	)

	d := BuildPatchDraft(prof, spec, prof.GroupID)

	if d.Params["timeout"] != "30s" {
		t.Errorf("params.timeout = %q, want 30s", d.Params["timeout"])
	}
	if d.Params["collation"] != "utf8mb4_general_ci" {
		t.Error("an unmentioned param must survive")
	}
	if d.SSL.Mode != "verify-full" || d.SSL.CACert != "OLD-CA" {
		t.Errorf("ssl patched wholesale instead of per field: %+v", d.SSL)
	}
	if d.SSHTunnel.Port != 2222 || d.SSHTunnel.Host != "bastion" || d.SSHTunnel.User != "jump" {
		t.Errorf("ssh patched wholesale instead of per field: %+v", d.SSHTunnel)
	}
	// The stored profile must not be aliased by the draft.
	if prof.Params["timeout"] != "5s" || prof.SSL.Mode != "require" || prof.SSHTunnel.Port != 22 {
		t.Error("BuildPatchDraft mutated the source profile")
	}
}

func TestBuildPatchDraftFolder(t *testing.T) {
	prof := baseProfile()
	d := BuildPatchDraft(prof, specWith(map[string]bool{}, services.ConnectionDraft{}), "grp-2")
	if d.GroupID != "grp-2" {
		t.Errorf("groupID = %q, want the resolved grp-2", d.GroupID)
	}
}

func TestSetDraftPEMReplacesPathWithContent(t *testing.T) {
	var d services.ConnectionDraft
	setDraftPEM(&d, "sshTunnel.privateKey", "-----BEGIN KEY-----")
	if d.SSHTunnel == nil || d.SSHTunnel.PrivateKey != "-----BEGIN KEY-----" {
		t.Errorf("ssh private key not set: %+v", d.SSHTunnel)
	}
	setDraftPEM(&d, "ssl.caCert", "-----BEGIN CERT-----")
	setDraftPEM(&d, "ssl.clientKey", "")
	if d.SSL == nil || d.SSL.CACert != "-----BEGIN CERT-----" || d.SSL.ClientKey != "" {
		t.Errorf("ssl fields not set: %+v", d.SSL)
	}
}

func TestSpecLabelPrefersTheMostRecognizableHandle(t *testing.T) {
	tests := []struct {
		spec cli.ConSpec
		want string
	}{
		{cli.ConSpec{Lookup: "MyConn", Name: "other"}, "MyConn"},
		{cli.ConSpec{Name: "prod shop", ID: "x"}, "prod shop"},
		{cli.ConSpec{ID: "conn-1"}, "conn-1"},
		{cli.ConSpec{Draft: services.ConnectionDraft{Database: "shop"}}, "shop"},
		{cli.ConSpec{Draft: services.ConnectionDraft{Host: "db.example"}}, "db.example"},
		{cli.ConSpec{Draft: services.ConnectionDraft{Driver: "mysql"}}, "mysql"},
		// Raw may carry password=… — specLabel must never fall back to it.
		{cli.ConSpec{Raw: "driver=mysql|password=secret"}, "(unnamed)"},
	}
	for _, tc := range tests {
		if got := specLabel(tc.spec); got != tc.want {
			t.Errorf("specLabel(%+v) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

func TestResolveFileDatabase(t *testing.T) {
	work := "/second/instance/cwd"
	mk := func(driver, db string) cli.ConSpec {
		return cli.ConSpec{
			Draft:   services.ConnectionDraft{Driver: driver, Database: db},
			SetKeys: map[string]bool{"database": true},
		}
	}

	// File-backed driver (sqlite): a relative path resolves against WorkDir.
	s := mk("sqlite", "data/app.db")
	resolveFileDatabase(&s, work)
	if want := filepath.Join(work, "data", "app.db"); s.Draft.Database != want {
		t.Errorf("sqlite relative path: got %q, want %q", s.Draft.Database, want)
	}

	// Absolute paths and special forms pass through.
	s = mk("sqlite", "/tmp/x.db")
	resolveFileDatabase(&s, work)
	if s.Draft.Database != "/tmp/x.db" {
		t.Errorf("absolute path must pass through, got %q", s.Draft.Database)
	}
	s = mk("sqlite", ":memory:")
	resolveFileDatabase(&s, work)
	if s.Draft.Database != ":memory:" {
		t.Errorf(":memory: must pass through, got %q", s.Draft.Database)
	}

	// A server database name is NOT a path — mysql must never be rewritten.
	s = mk("mysql", "shop")
	resolveFileDatabase(&s, work)
	if s.Draft.Database != "shop" {
		t.Errorf("server database name must pass through, got %q", s.Draft.Database)
	}

	// database not mentioned by the spec → untouched even for sqlite.
	s = cli.ConSpec{Draft: services.ConnectionDraft{Driver: "sqlite", Database: "leftover.db"}, SetKeys: map[string]bool{}}
	resolveFileDatabase(&s, work)
	if s.Draft.Database != "leftover.db" {
		t.Errorf("unset database must stay untouched, got %q", s.Draft.Database)
	}
}
