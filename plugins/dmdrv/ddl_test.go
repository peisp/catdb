package dmdrv

import (
	"strings"
	"testing"

	"catdb/internal/dbdriver"
)

func strPtr(s string) *string { return &s }

func TestGenerateCreateTable(t *testing.T) {
	d := dialect{}
	ts := dbdriver.TableSchema{
		Name:   "orders",
		Schema: "SALES",
		Columns: []dbdriver.ColumnMeta{
			{Name: "id", NativeType: "BIGINT", IsPrimaryKey: true, IsAutoIncrement: true},
			{Name: "name", NativeType: "VARCHAR(64)", Nullable: false, Comment: "客户名"},
			{Name: "amount", NativeType: "NUMERIC(10,2)", Nullable: true, Default: strPtr("0")},
			{Name: "created_at", NativeType: "TIMESTAMP", Nullable: true, Default: strPtr("CURRENT_TIMESTAMP")},
		},
		Indexes: []dbdriver.IndexInfo{
			{Name: "ix_name", Columns: []dbdriver.IndexColumn{{Name: "name"}}, Type: "BTREE"},
			{Name: "ux_amount", Columns: []dbdriver.IndexColumn{{Name: "amount", Order: "DESC"}}, Unique: true},
		},
		ForeignKeys: []dbdriver.ForeignKeyInfo{
			{Name: "fk_cust", Columns: []string{"name"}, ReferencedSchema: "SALES",
				ReferencedTable: "customers", ReferencedColumns: []string{"name"}, OnDelete: "CASCADE"},
		},
		Comment: "订单表",
	}
	got, err := d.GenerateCreateTable(ts)
	if err != nil {
		t.Fatalf("GenerateCreateTable: %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE "SALES"."orders" (`,
		`"id" BIGINT IDENTITY(1,1) NOT NULL`,
		`"name" VARCHAR(64) NOT NULL`,
		`"amount" NUMERIC(10,2) DEFAULT 0 NULL`,
		`"created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP NULL`,
		`PRIMARY KEY ("id")`,
		`CONSTRAINT "fk_cust" FOREIGN KEY ("name") REFERENCES "SALES"."customers" ("name") ON DELETE CASCADE`,
		`CREATE INDEX "ix_name" ON "SALES"."orders" ("name");`,
		`CREATE UNIQUE INDEX "ux_amount" ON "SALES"."orders" ("amount" DESC);`,
		`COMMENT ON TABLE "SALES"."orders" IS '订单表';`,
		`COMMENT ON COLUMN "SALES"."orders"."name" IS '客户名';`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CREATE output missing %q:\n%s", want, got)
		}
	}
	// Identity columns must not get a DEFAULT.
	if strings.Contains(got, `"id" BIGINT IDENTITY(1,1) DEFAULT`) {
		t.Errorf("identity column must not carry DEFAULT:\n%s", got)
	}
}

func TestGenerateCreateTableErrors(t *testing.T) {
	d := dialect{}
	if _, err := d.GenerateCreateTable(dbdriver.TableSchema{Name: " "}); err == nil {
		t.Error("empty name should error")
	}
	if _, err := d.GenerateCreateTable(dbdriver.TableSchema{Name: "t"}); err == nil {
		t.Error("no columns should error")
	}
}

func TestGenerateAlterTableColumns(t *testing.T) {
	d := dialect{}
	cs := dbdriver.ChangeSet{
		Columns: []dbdriver.ColumnChange{
			{Kind: dbdriver.ColumnAdd, Column: &dbdriver.ColumnMeta{Name: "age", NativeType: "INT", Nullable: true}},
			{Kind: dbdriver.ColumnModify, Name: "name", Column: &dbdriver.ColumnMeta{Name: "name", NativeType: "VARCHAR(128)", Nullable: false}},
			{Kind: dbdriver.ColumnRename, Name: "note", Column: &dbdriver.ColumnMeta{Name: "remark", NativeType: "VARCHAR(255)", Nullable: true}},
			{Kind: dbdriver.ColumnDrop, Name: "legacy"},
		},
	}
	stmts, err := d.GenerateAlterTable("SALES", "", "orders", cs)
	if err != nil {
		t.Fatalf("GenerateAlterTable: %v", err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		`ALTER TABLE "SALES"."orders" ADD COLUMN "age" INT NULL;`,
		`ALTER TABLE "SALES"."orders" MODIFY "name" VARCHAR(128) NOT NULL;`,
		`ALTER TABLE "SALES"."orders" RENAME COLUMN "note" TO "remark";`,
		`ALTER TABLE "SALES"."orders" MODIFY "remark" VARCHAR(255) NULL;`,
		`ALTER TABLE "SALES"."orders" DROP COLUMN "legacy";`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ALTER output missing %q:\n%s", want, joined)
		}
	}
}

// Identity must never be emitted in MODIFY contexts — DM cannot turn an
// existing column into an identity column.
func TestGenerateAlterTableModifySkipsIdentity(t *testing.T) {
	d := dialect{}
	cs := dbdriver.ChangeSet{
		Columns: []dbdriver.ColumnChange{
			{Kind: dbdriver.ColumnModify, Name: "id",
				Column: &dbdriver.ColumnMeta{Name: "id", NativeType: "BIGINT", IsAutoIncrement: true}},
		},
	}
	stmts, err := d.GenerateAlterTable("SALES", "", "orders", cs)
	if err != nil {
		t.Fatalf("GenerateAlterTable: %v", err)
	}
	joined := strings.Join(stmts, "\n")
	if strings.Contains(joined, "IDENTITY") {
		t.Errorf("MODIFY must not emit IDENTITY:\n%s", joined)
	}
}

func TestGenerateAlterTablePKIndexFKOptions(t *testing.T) {
	d := dialect{}
	cs := dbdriver.ChangeSet{
		PrimaryKey: &dbdriver.PrimaryKeyChange{Drop: true, Columns: []string{"id", "sub"}},
		Indexes: []dbdriver.IndexChange{
			{Kind: "drop", Name: "ix_old"},
			{Kind: "add", Index: &dbdriver.IndexInfo{Name: "ix_new", Columns: []dbdriver.IndexColumn{{Name: "a"}, {Name: "b", Order: "DESC"}}, Unique: true}},
		},
		ForeignKeys: []dbdriver.ForeignKeyChange{
			{Kind: "drop", Name: "fk_old"},
			{Kind: "add", ForeignKey: &dbdriver.ForeignKeyInfo{Name: "fk_new", Columns: []string{"a"},
				ReferencedTable: "parent", ReferencedColumns: []string{"id"}}},
		},
		Options: []dbdriver.TableOptionChange{{Name: "comment", Value: "新注释"}},
	}
	stmts, err := d.GenerateAlterTable("SALES", "", "orders", cs)
	if err != nil {
		t.Fatalf("GenerateAlterTable: %v", err)
	}
	joined := strings.Join(stmts, "\n")
	for _, want := range []string{
		`ALTER TABLE "SALES"."orders" DROP PRIMARY KEY;`,
		`ALTER TABLE "SALES"."orders" ADD PRIMARY KEY ("id", "sub");`,
		`DROP INDEX "SALES"."ix_old";`,
		`CREATE UNIQUE INDEX "ix_new" ON "SALES"."orders" ("a", "b" DESC);`,
		`ALTER TABLE "SALES"."orders" DROP CONSTRAINT "fk_old";`,
		`ALTER TABLE "SALES"."orders" ADD CONSTRAINT "fk_new" FOREIGN KEY ("a") REFERENCES "parent" ("id");`,
		`COMMENT ON TABLE "SALES"."orders" IS '新注释';`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ALTER output missing %q:\n%s", want, joined)
		}
	}
}

func TestGenerateAlterTableEmpty(t *testing.T) {
	stmts, err := dialect{}.GenerateAlterTable("SALES", "", "orders", dbdriver.ChangeSet{})
	if err != nil {
		t.Fatalf("GenerateAlterTable(empty): %v", err)
	}
	if len(stmts) != 0 {
		t.Errorf("empty ChangeSet must yield no statements, got %v", stmts)
	}
	if _, err := (dialect{}).GenerateAlterTable("SALES", "", " ", dbdriver.ChangeSet{}); err == nil {
		t.Error("empty table name should error")
	}
}
