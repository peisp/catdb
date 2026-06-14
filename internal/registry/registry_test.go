package registry

import (
	"context"
	"database/sql"
	"testing"

	"catdb/internal/dbdriver"
)

type fakeDriver struct{ name string }

func (f fakeDriver) Name() string                               { return f.name }
func (f fakeDriver) Version() string                            { return "test" }
func (f fakeDriver) ConnectionSchema() []dbdriver.ConnParamField { return nil }
func (f fakeDriver) Capabilities() dbdriver.Capabilities         { return dbdriver.Capabilities{} }
func (f fakeDriver) Dialect() dbdriver.Dialect                   { return nil }
func (f fakeDriver) Open(_ context.Context, _ dbdriver.ConnConfig) (dbdriver.Connection, error) {
	return nil, nil
}

var _ dbdriver.Driver = fakeDriver{}
var _ = sql.TxOptions{}

func TestRegisterAndGet(t *testing.T) {
	reset()
	Register(fakeDriver{name: "mysql"})

	d, err := Get("mysql")
	if err != nil {
		t.Fatalf("Get(mysql) returned error: %v", err)
	}
	if d.Name() != "mysql" {
		t.Fatalf("expected mysql, got %s", d.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	reset()
	if _, err := Get("nope"); err == nil {
		t.Fatal("expected error for unknown driver")
	}
}

func TestListIsSorted(t *testing.T) {
	reset()
	Register(fakeDriver{name: "postgres"})
	Register(fakeDriver{name: "mysql"})
	Register(fakeDriver{name: "sqlite"})

	got := Names()
	want := []string{"mysql", "postgres", "sqlite"}
	if len(got) != len(want) {
		t.Fatalf("expected %d names, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("at %d: want %s, got %s", i, want[i], got[i])
		}
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	reset()
	Register(fakeDriver{name: "mysql"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	Register(fakeDriver{name: "mysql"})
}

func TestNilRegistrationPanics(t *testing.T) {
	reset()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil driver")
		}
	}()
	Register(nil)
}

func TestEmptyNamePanics(t *testing.T) {
	reset()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty driver name")
		}
	}()
	Register(fakeDriver{name: ""})
}
