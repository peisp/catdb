package dbdriver

// ChangeSet is the structured, dialect-agnostic description of "how to turn
// table A into table B". It is produced by internal/core/schemadiff and
// rendered into DDL by Dialect.GenerateAlterTable. Statement text never
// appears here — only in the Dialect output — so the diff logic stays
// database-neutral.

// ColumnChangeKind enumerates the column mutations a ChangeSet can carry.
type ColumnChangeKind string

const (
	ColumnAdd    ColumnChangeKind = "add"
	ColumnDrop   ColumnChangeKind = "drop"
	ColumnModify ColumnChangeKind = "modify"
	// ColumnRename renames (and possibly redefines) a column in one step —
	// MySQL CHANGE COLUMN. Name holds the old name, Column.Name the new one.
	ColumnRename ColumnChangeKind = "rename"
)

// ColumnPosition places a column explicitly: first in the table, or right
// after a named column. A nil *ColumnPosition means "no positional clause".
type ColumnPosition struct {
	First bool   `json:"first,omitempty"`
	After string `json:"after,omitempty"`
}

// ColumnChange is one column mutation.
type ColumnChange struct {
	Kind ColumnChangeKind `json:"kind"`
	// Name is the existing column name (drop/modify) or the old name (rename).
	// Empty for add.
	Name string `json:"name,omitempty"`
	// Column is the desired definition (nil for drop). For rename its Name
	// field carries the new column name.
	Column   *ColumnMeta     `json:"column,omitempty"`
	Position *ColumnPosition `json:"position,omitempty"`
}

// PrimaryKeyChange replaces the table's primary key. Drop=true drops the
// existing PK first; a non-empty Columns then adds the new one.
type PrimaryKeyChange struct {
	Drop    bool     `json:"drop,omitempty"`
	Columns []string `json:"columns,omitempty"`
}

// IndexChange adds or drops a secondary index. Changed indexes appear as a
// drop + add pair (databases generally have no in-place index alter).
type IndexChange struct {
	Kind string `json:"kind"` // "add" | "drop"
	// Name is the index to drop; empty for add.
	Name  string     `json:"name,omitempty"`
	Index *IndexInfo `json:"index,omitempty"`
}

// ForeignKeyChange adds or drops a foreign-key constraint. Changed FKs appear
// as a drop + add pair.
type ForeignKeyChange struct {
	Kind string `json:"kind"` // "add" | "drop"
	// Name is the constraint to drop; empty for add.
	Name       string          `json:"name,omitempty"`
	ForeignKey *ForeignKeyInfo `json:"foreignKey,omitempty"`
}

// TableOptionChange updates one table-level option (currently "comment").
type TableOptionChange struct {
	Name  string `json:"name"` // "comment"
	Value string `json:"value"`
}

// ChangeSet groups every mutation needed to reconcile one table. Slices are
// already in safe execution order within their group; the canonical
// cross-group order is Columns → PrimaryKey → Indexes → ForeignKeys → Options.
type ChangeSet struct {
	Columns     []ColumnChange      `json:"columns,omitempty"`
	PrimaryKey  *PrimaryKeyChange   `json:"primaryKey,omitempty"`
	Indexes     []IndexChange       `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeyChange  `json:"foreignKeys,omitempty"`
	Options     []TableOptionChange `json:"options,omitempty"`
}

// Empty reports whether the ChangeSet carries no mutations at all.
func (c ChangeSet) Empty() bool {
	return len(c.Columns) == 0 && c.PrimaryKey == nil &&
		len(c.Indexes) == 0 && len(c.ForeignKeys) == 0 && len(c.Options) == 0
}
