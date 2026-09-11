package launch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"

	"catdb/internal/cli"
	"catdb/internal/services"
	"catdb/internal/storage"
)

func TestNeedsPasswordPrompt(t *testing.T) {
	tests := []struct {
		name   string
		spec   cli.ConSpec
		stored string
		want   bool
	}{
		{
			name: "connect with nothing stored asks",
			spec: cli.ConSpec{Connect: true, SetKeys: map[string]bool{}},
			want: true,
		},
		{
			name: "openConsole implies a connection, so it asks too",
			spec: cli.ConSpec{OpenConsole: true, SetKeys: map[string]bool{}},
			want: true,
		},
		{
			name: "no connection wanted: never asks",
			spec: cli.ConSpec{SetKeys: map[string]bool{}},
			want: false,
		},
		{
			name: "the command line gave a password",
			spec: cli.ConSpec{Connect: true, SetKeys: map[string]bool{"password": true}},
			want: false,
		},
		{
			name: "an explicitly empty password= is still a deliberate answer",
			spec: cli.ConSpec{Connect: true, SetKeys: map[string]bool{"password": true}},
			want: false,
		},
		{
			name:   "the keyring already has one",
			spec:   cli.ConSpec{Connect: true, SetKeys: map[string]bool{}},
			stored: "s3cret",
			want:   false,
		},
		{
			name: "savePassword=false does not suppress the prompt — it is the flow it governs",
			spec: cli.ConSpec{Connect: true, SavePassword: false, SetKeys: map[string]bool{"savePassword": true}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsPasswordPrompt(tc.spec, tc.stored); got != tc.want {
				t.Errorf("needsPasswordPrompt = %v, want %v", got, tc.want)
			}
		})
	}
}

// promptLauncher builds a Launcher with just the store + keyring halves the
// prompt path touches (no ConnectionService: ensurePassword never connects),
// on a mocked keyring and a temp database.
func promptLauncher(t *testing.T) (*Launcher, string) {
	t.Helper()
	keyring.MockInit()
	store, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	prof, err := store.SaveConnection(context.Background(), storage.ConnectionProfile{
		Name: "prod shop", Driver: "mysql", Host: "db.example", Port: 3306, User: "app",
	})
	if err != nil {
		t.Fatalf("SaveConnection: %v", err)
	}
	return &Launcher{store: store, secrets: storage.NewSecrets("catdb-test")}, prof.ID
}

func connectSpec(savePassword bool) cli.ConSpec {
	return cli.ConSpec{Connect: true, SavePassword: savePassword, SetKeys: map[string]bool{}}
}

func TestEnsurePasswordStoresOnlyThePasswordField(t *testing.T) {
	l, id := promptLauncher(t)
	// The connection already has SSH credentials the user configured by hand.
	if err := l.secrets.Save(id, storage.Secret{SSHPassword: "jump-pw", SSHKeyPassword: "key-pw"}); err != nil {
		t.Fatalf("seed secrets: %v", err)
	}

	var got PromptRequest
	l.SetPrompter(PrompterFunc(func(_ context.Context, r PromptRequest) (string, bool) {
		got = r
		return "typed", true
	}))

	var out services.LaunchOutcome
	drop, proceed := l.ensurePassword(context.Background(), connectSpec(true), id, "zh_CN", &out)
	if !proceed || drop {
		t.Fatalf("savePassword=true: drop=%v proceed=%v, want false/true", drop, proceed)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", out.Errors)
	}
	// The dialog is told what to show, from the SAVED profile.
	if got.ConnectionName != "prod shop" || got.User != "app" || got.Locale != "zh_CN" {
		t.Errorf("prompt request = %+v", got)
	}
	secret, err := l.secrets.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if secret.Password != "typed" {
		t.Errorf("password = %q, want the typed one", secret.Password)
	}
	if secret.SSHPassword != "jump-pw" || secret.SSHKeyPassword != "key-pw" {
		t.Errorf("the SSH credentials must survive: %+v", secret)
	}
}

func TestEnsurePasswordDropsWhenSavePasswordIsFalse(t *testing.T) {
	l, id := promptLauncher(t)
	if err := l.secrets.Save(id, storage.Secret{SSHPassword: "jump-pw"}); err != nil {
		t.Fatalf("seed secrets: %v", err)
	}
	l.SetPrompter(PrompterFunc(func(context.Context, PromptRequest) (string, bool) { return "typed", true }))

	var out services.LaunchOutcome
	drop, proceed := l.ensurePassword(context.Background(), connectSpec(false), id, "", &out)
	if !drop || !proceed {
		t.Fatalf("savePassword=false: drop=%v proceed=%v, want true/true", drop, proceed)
	}
	// The connect in between reads it from the keyring; the caller drops it
	// afterwards — field-precisely.
	l.clearStoredPassword(id)
	secret, err := l.secrets.Load(id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if secret.Password != "" {
		t.Errorf("password = %q, want it dropped", secret.Password)
	}
	if secret.SSHPassword != "jump-pw" {
		t.Errorf("dropping the password must not touch the SSH credentials: %+v", secret)
	}
}

func TestEnsurePasswordCancelledStopsTheConnect(t *testing.T) {
	l, id := promptLauncher(t)
	l.SetPrompter(PrompterFunc(func(context.Context, PromptRequest) (string, bool) { return "", false }))

	var out services.LaunchOutcome
	drop, proceed := l.ensurePassword(context.Background(), connectSpec(true), id, "", &out)
	if proceed || drop {
		t.Fatalf("cancelled: drop=%v proceed=%v, want false/false", drop, proceed)
	}
	if len(out.Errors) != 1 || out.Errors[0].Slug != "auth-cancelled" || out.Errors[0].Name != "prod shop" {
		t.Fatalf("errors = %+v, want one auth-cancelled for the connection", out.Errors)
	}
	if secret, err := l.secrets.Load(id); err == nil && secret.Password != "" {
		t.Errorf("a cancelled prompt must store nothing, got %+v", secret)
	}
}

func TestEnsurePasswordSkippedWhenNotNeeded(t *testing.T) {
	l, id := promptLauncher(t)
	l.SetPrompter(PrompterFunc(func(context.Context, PromptRequest) (string, bool) {
		t.Error("prompted although a password was already stored")
		return "", false
	}))
	if err := l.secrets.Save(id, storage.Secret{Password: "stored"}); err != nil {
		t.Fatalf("seed secrets: %v", err)
	}

	var out services.LaunchOutcome
	if drop, proceed := l.ensurePassword(context.Background(), connectSpec(false), id, "", &out); drop || !proceed {
		t.Errorf("stored password: drop=%v proceed=%v, want false/true", drop, proceed)
	}

	// No prompter wired at all → the old behaviour, connect straight away.
	l.SetPrompter(nil)
	if err := l.secrets.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if drop, proceed := l.ensurePassword(context.Background(), connectSpec(false), id, "", &out); drop || !proceed {
		t.Errorf("no prompter: drop=%v proceed=%v, want false/true", drop, proceed)
	}
}

// willPrompt is what decides whether patch() still logs "savePassword=false has
// no effect": with a prompt ahead, it does have one.
func TestWillPrompt(t *testing.T) {
	l, id := promptLauncher(t)
	spec := connectSpec(false)
	if l.willPrompt(spec, id) {
		t.Error("no prompter wired: willPrompt must be false")
	}
	l.SetPrompter(PrompterFunc(func(context.Context, PromptRequest) (string, bool) { return "", false }))
	if !l.willPrompt(spec, id) {
		t.Error("prompter wired and nothing stored: willPrompt must be true")
	}
	if err := l.secrets.Save(id, storage.Secret{Password: "stored"}); err != nil {
		t.Fatalf("seed secrets: %v", err)
	}
	if l.willPrompt(spec, id) {
		t.Error("password already stored: willPrompt must be false")
	}
}
