package schemadiff

import (
	"reflect"
	"testing"

	"catdb/internal/dbdriver"
)

func strp(s string) *string { return &s }

func col(name, typ string, opts ...func(*dbdriver.ColumnMeta)) dbdriver.ColumnMeta {
	c := dbdriver.ColumnMeta{Name: name, NativeType: typ, Nullable: true}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func pk() func(*dbdriver.ColumnMeta)      { return func(c *dbdriver.ColumnMeta) { c.IsPrimaryKey = true } }
func notNull() func(*dbdriver.ColumnMeta) { return func(c *dbdriver.ColumnMeta) { c.Nullable = false } }

func desiredFrom(cols ...dbdriver.ColumnMeta) []Column {
	out := make([]Column, len(cols))
	for i, c := range cols {
		out[i] = Column{
			OrigName: c.Name, Name: c.Name, NativeType: c.NativeType, Nullable: c.Nullable,
			Default: c.Default, IsPrimaryKey: c.IsPrimaryKey, IsAutoIncrement: c.IsAutoIncrement, Comment: c.Comment,
		}
	}
	return out
}

func usersTable() dbdriver.TableSchema {
	return dbdriver.TableSchema{
		Name: "users",
		Columns: []dbdriver.ColumnMeta{
			col("id", "bigint", pk(), notNull()),
			col("name", "varchar(64)", notNull()),
			col("email", "varchar(255)"),
		},
	}
}

func TestDiffNoChanges(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: desiredFrom(cur.Columns...)}
	cs := Diff(cur, des, Options{})
	if !cs.Empty() {
		t.Fatalf("expected empty ChangeSet, got %+v", cs)
	}
}

func TestDiffCosmeticTypeDifferenceIgnored(t *testing.T) {
	cur := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("p", "decimal(10, 2) unsigned")}}
	des := Table{Columns: []Column{{OrigName: "p", Name: "p", NativeType: "DECIMAL(10,2) UNSIGNED", Nullable: true}}}
	cs := Diff(cur, des, Options{})
	if !cs.Empty() {
		t.Fatalf("cosmetic type diff must not emit changes, got %+v", cs.Columns)
	}
}

func TestDiffAddColumn(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: append(desiredFrom(cur.Columns...),
		Column{Name: "age", NativeType: "INT", Nullable: true})}
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 {
		t.Fatalf("want 1 column change, got %+v", cs.Columns)
	}
	ch := cs.Columns[0]
	if ch.Kind != dbdriver.ColumnAdd || ch.Column.Name != "age" {
		t.Fatalf("want add age, got %+v", ch)
	}
	if ch.Position == nil || ch.Position.After != "email" {
		t.Fatalf("want AFTER email, got %+v", ch.Position)
	}
}

func TestDiffAddColumnFirst(t *testing.T) {
	cur := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("a", "int")}}
	des := Table{Columns: append([]Column{{Name: "z", NativeType: "INT", Nullable: true}},
		desiredFrom(cur.Columns...)...)}
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 || cs.Columns[0].Position == nil || !cs.Columns[0].Position.First {
		t.Fatalf("want add FIRST, got %+v", cs.Columns)
	}
}

func TestDiffDropColumn(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: desiredFrom(cur.Columns[0], cur.Columns[1])} // email dropped
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 || cs.Columns[0].Kind != dbdriver.ColumnDrop || cs.Columns[0].Name != "email" {
		t.Fatalf("want drop email, got %+v", cs.Columns)
	}
}

func TestDiffRenameColumn(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: desiredFrom(cur.Columns...)}
	des.Columns[2].Name = "email_address"
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 {
		t.Fatalf("want 1 change, got %+v", cs.Columns)
	}
	ch := cs.Columns[0]
	if ch.Kind != dbdriver.ColumnRename || ch.Name != "email" || ch.Column.Name != "email_address" {
		t.Fatalf("want rename email→email_address, got %+v", ch)
	}
	if ch.Position != nil {
		t.Fatalf("unmoved rename must not carry a position, got %+v", ch.Position)
	}
}

func TestDiffModifyColumn(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: desiredFrom(cur.Columns...)}
	des.Columns[1].NativeType = "VARCHAR(128)"
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 || cs.Columns[0].Kind != dbdriver.ColumnModify || cs.Columns[0].Column.NativeType != "VARCHAR(128)" {
		t.Fatalf("want modify name→varchar(128), got %+v", cs.Columns)
	}
	if cs.Columns[0].Position != nil {
		t.Fatalf("unmoved modify must not carry a position")
	}
}

func TestDiffMoveColumn(t *testing.T) {
	cur := usersTable()                                                                // id, name, email
	des := Table{Columns: desiredFrom(cur.Columns[0], cur.Columns[2], cur.Columns[1])} // id, email, name
	cs := Diff(cur, des, Options{})
	// Both email and name have new previous-columns → two modifies with positions.
	if len(cs.Columns) != 2 {
		t.Fatalf("want 2 moves, got %+v", cs.Columns)
	}
	if cs.Columns[0].Column.Name != "email" || cs.Columns[0].Position.After != "id" {
		t.Fatalf("want email AFTER id, got %+v", cs.Columns[0])
	}
	if cs.Columns[1].Column.Name != "name" || cs.Columns[1].Position.After != "email" {
		t.Fatalf("want name AFTER email, got %+v", cs.Columns[1])
	}
}

func TestDiffBlankNameRowsSkipped(t *testing.T) {
	cur := usersTable()
	des := Table{Columns: desiredFrom(cur.Columns...)}
	des.Columns = append(des.Columns, Column{Name: "   ", NativeType: "INT"})
	cs := Diff(cur, des, Options{})
	if !cs.Empty() {
		t.Fatalf("blank-name draft rows must be ignored, got %+v", cs)
	}
}

func TestDiffDefaultComparison(t *testing.T) {
	cur := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("a", "int")}}
	// nil vs "" differ
	des := Table{Columns: []Column{{OrigName: "a", Name: "a", NativeType: "int", Nullable: true, Default: strp("")}}}
	cs := Diff(cur, des, Options{})
	if len(cs.Columns) != 1 || cs.Columns[0].Kind != dbdriver.ColumnModify {
		t.Fatalf("nil vs empty default must differ, got %+v", cs.Columns)
	}
	// equal defaults
	cur.Columns[0].Default = strp("0")
	des.Columns[0].Default = strp("0")
	if cs := Diff(cur, des, Options{}); !cs.Empty() {
		t.Fatalf("equal defaults must not diff, got %+v", cs.Columns)
	}
}

func TestDiffPrimaryKeyChanges(t *testing.T) {
	// add PK to table without one
	cur := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("a", "int", notNull())}}
	des := Table{Columns: desiredFrom(cur.Columns...)}
	des.Columns[0].IsPrimaryKey = true
	cs := Diff(cur, des, Options{})
	if cs.PrimaryKey == nil || cs.PrimaryKey.Drop || !reflect.DeepEqual(cs.PrimaryKey.Columns, []string{"a"}) {
		t.Fatalf("want add PK(a) without drop, got %+v", cs.PrimaryKey)
	}

	// drop PK entirely
	cur2 := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("a", "int", pk(), notNull())}}
	des2 := Table{Columns: desiredFrom(cur2.Columns...)}
	des2.Columns[0].IsPrimaryKey = false
	cs2 := Diff(cur2, des2, Options{})
	if cs2.PrimaryKey == nil || !cs2.PrimaryKey.Drop || len(cs2.PrimaryKey.Columns) != 0 {
		t.Fatalf("want drop PK only, got %+v", cs2.PrimaryKey)
	}

	// change PK columns
	cur3 := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{col("a", "int", pk(), notNull()), col("b", "int", notNull())}}
	des3 := Table{Columns: desiredFrom(cur3.Columns...)}
	des3.Columns[0].IsPrimaryKey = false
	des3.Columns[1].IsPrimaryKey = true
	cs3 := Diff(cur3, des3, Options{})
	if cs3.PrimaryKey == nil || !cs3.PrimaryKey.Drop || !reflect.DeepEqual(cs3.PrimaryKey.Columns, []string{"b"}) {
		t.Fatalf("want drop+add PK(b), got %+v", cs3.PrimaryKey)
	}
}

func ixInfo(name string, unique bool, cols ...string) dbdriver.IndexInfo {
	ic := make([]dbdriver.IndexColumn, len(cols))
	for i, c := range cols {
		ic[i] = dbdriver.IndexColumn{Name: c}
	}
	return dbdriver.IndexInfo{Name: name, Columns: ic, Unique: unique, Type: "BTREE"}
}

func ixDesired(ix dbdriver.IndexInfo) Index {
	return Index{OrigName: ix.Name, Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique, Primary: ix.Primary, Type: ix.Type, Comment: ix.Comment}
}

func TestDiffIndexes(t *testing.T) {
	cur := usersTable()
	cur.Indexes = []dbdriver.IndexInfo{
		{Name: "PRIMARY", Primary: true, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
		ixInfo("idx_name", false, "name"),
		ixInfo("idx_email", true, "email"),
	}
	des := Table{Columns: desiredFrom(cur.Columns...)}
	// keep idx_name but make it unique (changed → drop+add), drop idx_email, add idx_new
	changed := ixDesired(cur.Indexes[1])
	changed.Unique = true
	des.Indexes = []Index{
		{OrigName: "PRIMARY", Name: "PRIMARY", Primary: true, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
		changed,
		{Name: "idx_new", Columns: []dbdriver.IndexColumn{{Name: "email"}, {Name: "name", Order: "desc"}}},
	}
	cs := Diff(cur, des, Options{})
	want := []dbdriver.IndexChange{
		{Kind: "drop", Name: "idx_email"},
		{Kind: "drop", Name: "idx_name"},
		{Kind: "add", Index: &dbdriver.IndexInfo{Name: "idx_name", Columns: []dbdriver.IndexColumn{{Name: "name"}}, Unique: true, Type: "BTREE"}},
		{Kind: "add", Index: &dbdriver.IndexInfo{Name: "idx_new", Columns: []dbdriver.IndexColumn{{Name: "email"}, {Name: "name", Order: "DESC"}}}},
	}
	if !reflect.DeepEqual(cs.Indexes, want) {
		t.Fatalf("index changes mismatch:\ngot  %+v\nwant %+v", cs.Indexes, want)
	}
}

func TestDiffIndexDefaultNormalization(t *testing.T) {
	// Read-back metadata says Order=ASC / Type=BTREE where the desired side
	// omitted both — semantically identical, must not diff.
	cur := usersTable()
	cur.Indexes = []dbdriver.IndexInfo{
		{Name: "idx_name", Columns: []dbdriver.IndexColumn{{Name: "name", Order: "ASC"}}, Type: "BTREE"},
	}
	des := Table{Columns: desiredFrom(cur.Columns...)}
	des.Indexes = []Index{
		{OrigName: "idx_name", Name: "idx_name", Columns: []dbdriver.IndexColumn{{Name: "name"}}},
	}
	if cs := Diff(cur, des, Options{}); len(cs.Indexes) != 0 {
		t.Fatalf("ASC/BTREE defaults must compare equal, got %+v", cs.Indexes)
	}
}

func fkInfo(name string) dbdriver.ForeignKeyInfo {
	return dbdriver.ForeignKeyInfo{
		Name: name, Columns: []string{"user_id"},
		ReferencedTable: "users", ReferencedColumns: []string{"id"},
	}
}

func TestDiffForeignKeys(t *testing.T) {
	cur := dbdriver.TableSchema{
		Columns:     []dbdriver.ColumnMeta{col("user_id", "bigint")},
		ForeignKeys: []dbdriver.ForeignKeyInfo{fkInfo("fk_user")},
	}
	des := Table{Columns: desiredFrom(cur.Columns...)}

	// RESTRICT vs empty is equal — no change.
	same := ForeignKey{OrigName: "fk_user", Name: "fk_user", Columns: []string{"user_id"},
		ReferencedTable: "users", ReferencedColumns: []string{"id"}, OnDelete: "RESTRICT"}
	des.ForeignKeys = []ForeignKey{same}
	if cs := Diff(cur, des, Options{}); len(cs.ForeignKeys) != 0 {
		t.Fatalf("RESTRICT vs empty must be equal, got %+v", cs.ForeignKeys)
	}

	// change ON DELETE → drop+add
	changed := same
	changed.OnDelete = "CASCADE"
	des.ForeignKeys = []ForeignKey{changed}
	cs := Diff(cur, des, Options{})
	if len(cs.ForeignKeys) != 2 || cs.ForeignKeys[0].Kind != "drop" || cs.ForeignKeys[1].Kind != "add" ||
		cs.ForeignKeys[1].ForeignKey.OnDelete != "CASCADE" {
		t.Fatalf("want drop+add pair, got %+v", cs.ForeignKeys)
	}

	// drop FK
	des.ForeignKeys = nil
	cs = Diff(cur, des, Options{})
	if len(cs.ForeignKeys) != 1 || cs.ForeignKeys[0].Kind != "drop" || cs.ForeignKeys[0].Name != "fk_user" {
		t.Fatalf("want drop fk_user, got %+v", cs.ForeignKeys)
	}
}

func TestDiffTableComment(t *testing.T) {
	cur := usersTable()
	cur.Comment = "old"
	des := Table{Columns: desiredFrom(cur.Columns...), Comment: "new"}
	cs := Diff(cur, des, Options{})
	if len(cs.Options) != 1 || cs.Options[0].Name != "comment" || cs.Options[0].Value != "new" {
		t.Fatalf("want comment option change, got %+v", cs.Options)
	}
}

func TestFromTableSchemaOrigNameFilling(t *testing.T) {
	src := usersTable() // id, name, email
	target := dbdriver.TableSchema{Columns: []dbdriver.ColumnMeta{
		col("id", "bigint", pk(), notNull()),
		col("legacy", "text"),
	}}
	des := FromTableSchema(src, target)
	if des.Columns[0].OrigName != "id" {
		t.Fatalf("id exists in target — OrigName must link, got %q", des.Columns[0].OrigName)
	}
	if des.Columns[1].OrigName != "" || des.Columns[2].OrigName != "" {
		t.Fatalf("name/email absent in target — OrigName must stay empty")
	}
	// Full sync diff: legacy dropped, name+email added.
	cs := Diff(target, des, Options{})
	kinds := map[dbdriver.ColumnChangeKind]int{}
	for _, ch := range cs.Columns {
		kinds[ch.Kind]++
	}
	if kinds[dbdriver.ColumnDrop] != 1 || kinds[dbdriver.ColumnAdd] != 2 {
		t.Fatalf("want 1 drop + 2 adds, got %+v", cs.Columns)
	}
}

func TestNormalizeNativeType(t *testing.T) {
	cases := map[string]string{
		"varchar(255)":           "VARCHAR(255)",
		"decimal(10, 2)":         "DECIMAL(10,2)",
		"int(10) unsigned":       "INT(10) UNSIGNED",
		"int unsigned zerofill":  "INT UNSIGNED",
		"enum('a','b')":          "ENUM('a','b')",
		"datetime(6)":            "DATETIME(6)",
		"text":                   "TEXT",
		"":                       "",
		"DECIMAL(10,2) UNSIGNED": "DECIMAL(10,2) UNSIGNED",
	}
	for in, want := range cases {
		if got := NormalizeNativeType(in); got != want {
			t.Errorf("NormalizeNativeType(%q) = %q, want %q", in, got, want)
		}
	}
}
