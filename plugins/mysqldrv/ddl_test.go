package mysqldrv

import (
	"reflect"
	"testing"

	"catdb/internal/dbdriver"
)

func strp(s string) *string { return &s }

func TestFormatDefaultExpr(t *testing.T) {
	cases := map[string]string{
		"":                    "''",
		"NULL":                "NULL",
		"null":                "NULL",
		"CURRENT_TIMESTAMP":   "CURRENT_TIMESTAMP",
		"0":                   "0",
		"-1.5":                "-1.5",
		"1e-3":                "1e-3",
		"(uuid())":            "(uuid())",
		"hello":               "'hello'",
		"it's":                "'it''s'",
		`back\slash`:          `'back\\slash'`,
		"1970-01-01 00:00:00": "'1970-01-01 00:00:00'",
	}
	for in, want := range cases {
		if got := formatDefaultExpr(in); got != want {
			t.Errorf("formatDefaultExpr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateCreateTable(t *testing.T) {
	d := dialect{}
	ts := dbdriver.TableSchema{
		Name:   "users",
		Schema: "app",
		Columns: []dbdriver.ColumnMeta{
			{Name: "id", NativeType: "BIGINT UNSIGNED", IsPrimaryKey: true, IsAutoIncrement: true},
			{Name: "name", NativeType: "VARCHAR(64)", Comment: "display name"},
			{Name: "email", NativeType: "VARCHAR(255)", Nullable: true, Default: strp("NULL")},
			{Name: "created", NativeType: "DATETIME(6)", Default: strp("CURRENT_TIMESTAMP")},
		},
		Indexes: []dbdriver.IndexInfo{
			{Name: "PRIMARY", Primary: true, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "idx_email", Unique: true, Columns: []dbdriver.IndexColumn{{Name: "email"}}, Type: "BTREE"},
			{Name: "ft_name", Columns: []dbdriver.IndexColumn{{Name: "name"}}, Type: "FULLTEXT"},
		},
		ForeignKeys: []dbdriver.ForeignKeyInfo{
			{Name: "fk_org", Columns: []string{"org_id"}, ReferencedSchema: "app", ReferencedTable: "orgs", ReferencedColumns: []string{"id"}, OnDelete: "CASCADE"},
		},
		Options: map[string]string{"engine": "InnoDB", "charset": "utf8mb4"},
		Comment: "user accounts",
	}
	got, err := d.GenerateCreateTable(ts)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE `app`.`users` (\n" +
		"  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,\n" +
		"  `name` VARCHAR(64) NOT NULL COMMENT 'display name',\n" +
		"  `email` VARCHAR(255) NULL DEFAULT NULL,\n" +
		"  `created` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,\n" +
		"  PRIMARY KEY (`id`),\n" +
		"  UNIQUE INDEX `idx_email` (`email`),\n" +
		"  FULLTEXT INDEX `ft_name` (`name`),\n" +
		"  CONSTRAINT `fk_org` FOREIGN KEY (`org_id`) REFERENCES `app`.`orgs` (`id`) ON DELETE CASCADE\n" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='user accounts';"
	if got != want {
		t.Errorf("GenerateCreateTable mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerateCreateTableErrors(t *testing.T) {
	d := dialect{}
	if _, err := d.GenerateCreateTable(dbdriver.TableSchema{}); err == nil {
		t.Error("empty name must error")
	}
	if _, err := d.GenerateCreateTable(dbdriver.TableSchema{Name: "t"}); err == nil {
		t.Error("no columns must error")
	}
}

func TestGenerateAlterTableColumns(t *testing.T) {
	d := dialect{}
	cs := dbdriver.ChangeSet{
		Columns: []dbdriver.ColumnChange{
			{Kind: dbdriver.ColumnDrop, Name: "legacy"},
			{Kind: dbdriver.ColumnAdd,
				Column:   &dbdriver.ColumnMeta{Name: "age", NativeType: "INT", Nullable: true},
				Position: &dbdriver.ColumnPosition{After: "email"}},
			{Kind: dbdriver.ColumnModify,
				Column:   &dbdriver.ColumnMeta{Name: "name", NativeType: "VARCHAR(128)"},
				Position: nil},
			{Kind: dbdriver.ColumnRename, Name: "email",
				Column:   &dbdriver.ColumnMeta{Name: "email_address", NativeType: "VARCHAR(255)", Nullable: true},
				Position: &dbdriver.ColumnPosition{First: true}},
		},
		PrimaryKey: &dbdriver.PrimaryKeyChange{Drop: true, Columns: []string{"id", "org_id"}},
	}
	got, err := d.GenerateAlterTable("app", "", "users", cs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER TABLE `app`.`users` DROP COLUMN `legacy`;",
		"ALTER TABLE `app`.`users` ADD COLUMN `age` INT NULL AFTER `email`;",
		"ALTER TABLE `app`.`users` MODIFY COLUMN `name` VARCHAR(128) NOT NULL;",
		"ALTER TABLE `app`.`users` CHANGE COLUMN `email` `email_address` VARCHAR(255) NULL FIRST;",
		"ALTER TABLE `app`.`users` DROP PRIMARY KEY;",
		"ALTER TABLE `app`.`users` ADD PRIMARY KEY (`id`, `org_id`);",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestGenerateAlterTableIndexesFKsOptions(t *testing.T) {
	d := dialect{}
	cs := dbdriver.ChangeSet{
		Indexes: []dbdriver.IndexChange{
			{Kind: "drop", Name: "idx_old"},
			{Kind: "add", Index: &dbdriver.IndexInfo{Name: "idx_new", Unique: true,
				Columns: []dbdriver.IndexColumn{{Name: "a"}, {Name: "b", Order: "DESC"}}, Type: "BTREE", Comment: "covering"}},
			{Kind: "add", Index: &dbdriver.IndexInfo{Name: "idx_hash",
				Columns: []dbdriver.IndexColumn{{Name: "c"}}, Type: "HASH"}},
		},
		ForeignKeys: []dbdriver.ForeignKeyChange{
			{Kind: "drop", Name: "fk_old"},
			{Kind: "add", ForeignKey: &dbdriver.ForeignKeyInfo{Name: "fk_new", Columns: []string{"user_id"},
				ReferencedTable: "users", ReferencedColumns: []string{"id"}, OnUpdate: "CASCADE", OnDelete: "RESTRICT"}},
		},
		Options: []dbdriver.TableOptionChange{{Name: "comment", Value: "it's new"}},
	}
	got, err := d.GenerateAlterTable("app", "", "orders", cs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ALTER TABLE `app`.`orders` DROP INDEX `idx_old`;",
		"ALTER TABLE `app`.`orders` ADD UNIQUE INDEX `idx_new` (`a`, `b` DESC) COMMENT 'covering';",
		"ALTER TABLE `app`.`orders` ADD INDEX `idx_hash` (`c`) USING HASH;",
		"ALTER TABLE `app`.`orders` DROP FOREIGN KEY `fk_old`;",
		"ALTER TABLE `app`.`orders` ADD CONSTRAINT `fk_new` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE CASCADE;",
		"ALTER TABLE `app`.`orders` COMMENT = 'it''s new';",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestGenerateAlterTableEmpty(t *testing.T) {
	d := dialect{}
	got, err := d.GenerateAlterTable("app", "", "t", dbdriver.ChangeSet{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty ChangeSet must yield no statements, got %v", got)
	}
}
