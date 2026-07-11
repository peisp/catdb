package mariadbdrv

import (
	"testing"

	"catdb/internal/dbdriver/contract"
)

// TestUIDialectDescriptor runs the shared static validation (no live DB) so
// descriptor mistakes surface in plain unit tests, not just integration runs.
func TestUIDialectDescriptor(t *testing.T) {
	contract.TestUIDialect(t, driver{})
}

// TestOverrides pins the two things mariadbdrv changes on top of the embedded
// mysqldrv.Driver.
func TestOverrides(t *testing.T) {
	d := driver{}
	if got := d.Name(); got != "mariadb" {
		t.Errorf("Name() = %q, want %q", got, "mariadb")
	}
	if got := d.UIDialect().EditorDialect; got != "mariadb" {
		t.Errorf("UIDialect().EditorDialect = %q, want %q", got, "mariadb")
	}
}
