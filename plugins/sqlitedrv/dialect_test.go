package sqlitedrv

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

func TestNormalizeTypeAffinity(t *testing.T) {
	d := dialect{}
	cases := map[string]string{
		"VARCHAR(64)":      "TEXT",
		"varchar(128)":     "TEXT",
		"TEXT":             "TEXT",
		"CLOB":             "TEXT",
		"INT":              "INTEGER",
		"INTEGER":          "INTEGER",
		"BIGINT":           "INTEGER",
		"UNSIGNED BIG INT": "INTEGER",
		"REAL":             "REAL",
		"DOUBLE":           "REAL",
		"FLOAT":            "REAL",
		"NUMERIC":          "NUMERIC",
		"DECIMAL(10,2)":    "NUMERIC",
		"BOOLEAN":          "NUMERIC",
		"DATETIME":         "NUMERIC",
		"BLOB":             "BLOB",
		"":                 "BLOB",
	}
	for in, want := range cases {
		got := d.NormalizeType(in)
		if got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
		if again := d.NormalizeType(got); again != got {
			t.Errorf("NormalizeType not idempotent for %q: %q → %q", in, got, again)
		}
	}
}

func TestMapType(t *testing.T) {
	d := dialect{}
	cases := map[string]dbdriver.LogicalType{
		"INTEGER":     dbdriver.TypeInt,
		"BIGINT":      dbdriver.TypeBigInt,
		"VARCHAR(64)": dbdriver.TypeString,
		"TEXT":        dbdriver.TypeText,
		"REAL":        dbdriver.TypeFloat,
		"NUMERIC":     dbdriver.TypeDecimal,
		"BOOLEAN":     dbdriver.TypeBool,
		"DATETIME":    dbdriver.TypeDateTime,
		"TIMESTAMP":   dbdriver.TypeTimestamp,
		"DATE":        dbdriver.TypeDate,
		"BLOB":        dbdriver.TypeBytes,
		"JSON":        dbdriver.TypeJSON,
		"POINT":       dbdriver.TypeInt, // SQLite affinity quirk: contains "INT"
	}
	for in, want := range cases {
		if got := d.MapType(in); got != want {
			t.Errorf("MapType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateCreateTableAutoincrementAndIndexes(t *testing.T) {
	d := dialect{}
	def := "active"
	ddl, err := d.GenerateCreateTable(dbdriver.TableSchema{
		Name:   "t",
		Schema: "main",
		Columns: []dbdriver.ColumnMeta{
			{Name: "id", NativeType: "INTEGER", IsPrimaryKey: true, IsAutoIncrement: true},
			{Name: "name", NativeType: "VARCHAR(64)", Nullable: false},
			{Name: "status", NativeType: "TEXT", Nullable: true, Default: &def},
		},
		Indexes: []dbdriver.IndexInfo{
			{Name: "ix_name", Columns: []dbdriver.IndexColumn{{Name: "name"}}, Unique: true},
		},
	})
	if err != nil {
		t.Fatalf("GenerateCreateTable: %v", err)
	}
	for _, want := range []string{
		`"id" INTEGER PRIMARY KEY AUTOINCREMENT`,
		`"name" VARCHAR(64) NOT NULL`,
		`"status" TEXT DEFAULT 'active'`,
		`CREATE UNIQUE INDEX "main"."ix_name" ON "t" ("name");`,
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL missing %q:\n%s", want, ddl)
		}
	}

	// The multi-statement string must execute in one Exec — the sync path
	// runs each Statements entry through a single Querier.Exec.
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "ddl.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("exec generated DDL: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('t','ix_name')`).Scan(&n); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected table + index created, got %d objects", n)
	}
}

func TestTruncateTableSQL(t *testing.T) {
	d := dialect{}
	if got := d.TruncateTableSQL(`"t"`); got != `DELETE FROM "t"` {
		t.Errorf("TruncateTableSQL = %q", got)
	}
}

func TestReplaceViewSQL(t *testing.T) {
	d := dialect{}
	got := d.ReplaceViewSQL(`"v"`, "SELECT 1")
	want := []string{`DROP VIEW IF EXISTS "v";`, `CREATE VIEW "v" AS SELECT 1;`}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ReplaceViewSQL = %v, want %v", got, want)
	}
}

func TestGenerateAlterTableUnsupportedChanges(t *testing.T) {
	d := dialect{}
	col := dbdriver.ColumnMeta{Name: "name", NativeType: "TEXT"}
	if _, err := d.GenerateAlterTable("main", "", "t", dbdriver.ChangeSet{
		Columns: []dbdriver.ColumnChange{{Kind: dbdriver.ColumnModify, Name: "name", Column: &col}},
	}); err == nil {
		t.Fatal("expected error for column modify")
	}
	if _, err := d.GenerateAlterTable("main", "", "t", dbdriver.ChangeSet{
		PrimaryKey: &dbdriver.PrimaryKeyChange{Drop: true, Columns: []string{"id"}},
	}); err == nil {
		t.Fatal("expected error for primary-key change")
	}
	if _, err := d.GenerateAlterTable("main", "", "t", dbdriver.ChangeSet{
		ForeignKeys: []dbdriver.ForeignKeyChange{{Kind: "drop", Name: "fk_t_0"}},
	}); err == nil {
		t.Fatal("expected error for foreign-key change")
	}

	// Comment options are dropped, not an error (SQLite has no comments).
	stmts, err := d.GenerateAlterTable("main", "", "t", dbdriver.ChangeSet{
		Options: []dbdriver.TableOptionChange{{Name: "comment", Value: "x"}},
	})
	if err != nil {
		t.Fatalf("comment option: %v", err)
	}
	if len(stmts) != 0 {
		t.Fatalf("comment option should yield no statements, got %v", stmts)
	}
}
