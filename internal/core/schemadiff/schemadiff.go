// Package schemadiff computes the structured difference between a table's
// current shape and a desired shape, emitting a dialect-agnostic
// dbdriver.ChangeSet. Dialects render the ChangeSet into DDL via
// GenerateAlterTable.
//
// The comparison semantics are a direct port of the front-end alterPlan.ts
// engine (the structure editor's original diff): match-by-OrigName rename
// tracking for columns, drop+add pairs for changed indexes/FKs, positional
// AFTER/FIRST clauses only for genuinely moved columns, and PRIMARY KEY
// handled as its own drop/add pair.
//
// Two callers, one algorithm:
//   - structure editor: desired = user-edited draft (OrigName tracks renames)
//   - structure sync:   desired = source table, matched to target by name
//     (see FromTableSchema, which fills OrigName for name matches)
package schemadiff

import (
	"strings"

	"catdb/internal/dbdriver"
)

// Column is one desired column. OrigName links it back to the current table:
// "" means newly added; a non-empty OrigName different from Name means rename.
type Column struct {
	OrigName string `json:"origName,omitempty"`

	Name            string  `json:"name"`
	NativeType      string  `json:"nativeType"`
	Nullable        bool    `json:"nullable"`
	Default         *string `json:"default,omitempty"` // nil = no DEFAULT clause
	IsPrimaryKey    bool    `json:"isPrimaryKey,omitempty"`
	IsAutoIncrement bool    `json:"isAutoIncrement,omitempty"`
	Comment         string  `json:"comment,omitempty"`
}

// Index is one desired secondary index (or the PRIMARY entry, which the diff
// ignores — the PK pipeline owns it).
type Index struct {
	OrigName string `json:"origName,omitempty"`

	Name    string                 `json:"name"`
	Columns []dbdriver.IndexColumn `json:"columns"`
	Unique  bool                   `json:"unique,omitempty"`
	Primary bool                   `json:"primary,omitempty"`
	Type    string                 `json:"type,omitempty"`
	Comment string                 `json:"comment,omitempty"`
}

// ForeignKey is one desired FK constraint.
type ForeignKey struct {
	OrigName string `json:"origName,omitempty"`

	Name              string   `json:"name"`
	Columns           []string `json:"columns"`
	ReferencedSchema  string   `json:"referencedSchema,omitempty"`
	ReferencedTable   string   `json:"referencedTable"`
	ReferencedColumns []string `json:"referencedColumns"`
	OnUpdate          string   `json:"onUpdate,omitempty"`
	OnDelete          string   `json:"onDelete,omitempty"`
}

// Table is the desired shape handed to Diff.
type Table struct {
	Columns     []Column     `json:"columns"`
	Indexes     []Index      `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
	Comment     string       `json:"comment"`
}

// Options tweaks comparison behavior.
type Options struct {
	// NormalizeType canonicalizes a native type string before equality
	// comparison. Nil falls back to NormalizeNativeType (MySQL-flavored).
	NormalizeType func(string) string
}

// FromTableSchema converts a live table (e.g. the sync source) into a desired
// Table, filling OrigName wherever target has a column/index/FK of the same
// name so the diff treats them as candidates for in-place change rather than
// drop+add.
func FromTableSchema(src dbdriver.TableSchema, target dbdriver.TableSchema) Table {
	targetCols := make(map[string]bool, len(target.Columns))
	for _, c := range target.Columns {
		targetCols[c.Name] = true
	}
	targetIx := make(map[string]bool, len(target.Indexes))
	for _, ix := range target.Indexes {
		targetIx[ix.Name] = true
	}
	targetFK := make(map[string]bool, len(target.ForeignKeys))
	for _, fk := range target.ForeignKeys {
		targetFK[fk.Name] = true
	}

	out := Table{Comment: src.Comment}
	for _, c := range src.Columns {
		col := Column{
			Name:            c.Name,
			NativeType:      c.NativeType,
			Nullable:        c.Nullable,
			Default:         c.Default,
			IsPrimaryKey:    c.IsPrimaryKey,
			IsAutoIncrement: c.IsAutoIncrement,
			Comment:         c.Comment,
		}
		if targetCols[c.Name] {
			col.OrigName = c.Name
		}
		out.Columns = append(out.Columns, col)
	}
	for _, ix := range src.Indexes {
		d := Index{Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique, Primary: ix.Primary, Type: ix.Type, Comment: ix.Comment}
		if targetIx[ix.Name] {
			d.OrigName = ix.Name
		}
		out.Indexes = append(out.Indexes, d)
	}
	for _, fk := range src.ForeignKeys {
		d := ForeignKey{
			Name: fk.Name, Columns: fk.Columns,
			ReferencedSchema: fk.ReferencedSchema, ReferencedTable: fk.ReferencedTable,
			ReferencedColumns: fk.ReferencedColumns, OnUpdate: fk.OnUpdate, OnDelete: fk.OnDelete,
		}
		if targetFK[fk.Name] {
			d.OrigName = fk.Name
		}
		out.ForeignKeys = append(out.ForeignKeys, d)
	}
	return out
}

// Diff computes the ChangeSet that reconciles current into desired.
func Diff(current dbdriver.TableSchema, desired Table, opts Options) dbdriver.ChangeSet {
	norm := opts.NormalizeType
	if norm == nil {
		norm = NormalizeNativeType
	}
	var cs dbdriver.ChangeSet
	cs.Columns, cs.PrimaryKey = diffColumns(current.Columns, desired.Columns, norm)
	cs.Indexes = diffIndexes(current.Indexes, desired.Indexes)
	cs.ForeignKeys = diffForeignKeys(current.ForeignKeys, desired.ForeignKeys)
	if current.Comment != desired.Comment {
		cs.Options = append(cs.Options, dbdriver.TableOptionChange{Name: "comment", Value: desired.Comment})
	}
	return cs
}

// ---- columns ---------------------------------------------------------------

func toColumnMeta(c Column) *dbdriver.ColumnMeta {
	return &dbdriver.ColumnMeta{
		Name:            strings.TrimSpace(c.Name),
		NativeType:      c.NativeType,
		Nullable:        c.Nullable,
		Default:         c.Default,
		IsPrimaryKey:    c.IsPrimaryKey,
		IsAutoIncrement: c.IsAutoIncrement,
		Comment:         c.Comment,
	}
}

func columnBodiesEqual(d Column, orig dbdriver.ColumnMeta, norm func(string) string) bool {
	if norm(d.NativeType) != norm(orig.NativeType) {
		return false
	}
	if d.Nullable != orig.Nullable {
		return false
	}
	if !ptrStrEqual(d.Default, orig.Default) {
		return false
	}
	if d.IsAutoIncrement != orig.IsAutoIncrement {
		return false
	}
	if d.Comment != orig.Comment {
		return false
	}
	return true
}

func ptrStrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func diffColumns(orig []dbdriver.ColumnMeta, desired []Column, norm func(string) string) ([]dbdriver.ColumnChange, *dbdriver.PrimaryKeyChange) {
	origByName := make(map[string]dbdriver.ColumnMeta, len(orig))
	for _, c := range orig {
		origByName[c.Name] = c
	}
	claimed := make(map[string]bool, len(desired))
	for _, d := range desired {
		if d.OrigName != "" {
			claimed[d.OrigName] = true
		}
	}

	var changes []dbdriver.ColumnChange

	// DROP: original columns no draft row claims.
	for _, c := range orig {
		if !claimed[c.Name] {
			changes = append(changes, dbdriver.ColumnChange{Kind: dbdriver.ColumnDrop, Name: c.Name})
		}
	}

	// Surviving orders (post-drop) for accurate moved-detection.
	var survivingDesiredOrig []string // OrigName of surviving desired rows, in final order
	for _, d := range desired {
		if d.OrigName != "" {
			if _, ok := origByName[d.OrigName]; ok {
				survivingDesiredOrig = append(survivingDesiredOrig, d.OrigName)
			}
		}
	}
	var survivingOrigOrder []string
	for _, c := range orig {
		if claimed[c.Name] {
			survivingOrigOrder = append(survivingOrigOrder, c.Name)
		}
	}

	// ADD / RENAME / MODIFY, walking desired in final order.
	var prevName *string
	for _, d := range desired {
		trimmed := strings.TrimSpace(d.Name)
		if trimmed == "" {
			// Unfinished row — skip without advancing prevName so a later
			// row's AFTER clause doesn't latch onto a blank identifier.
			continue
		}
		pos := positionalClause(prevName)

		if d.OrigName == "" {
			changes = append(changes, dbdriver.ColumnChange{Kind: dbdriver.ColumnAdd, Column: toColumnMeta(d), Position: pos})
		} else if origCol, ok := origByName[d.OrigName]; !ok {
			// OrigName points at nothing (defensive) — treat as new.
			changes = append(changes, dbdriver.ColumnChange{Kind: dbdriver.ColumnAdd, Column: toColumnMeta(d), Position: pos})
		} else {
			renamed := d.OrigName != trimmed
			bodyChanged := !columnBodiesEqual(d, origCol, norm)
			moved := positionChanged(d.OrigName, survivingDesiredOrig, survivingOrigOrder)
			var movedPos *dbdriver.ColumnPosition
			if moved {
				movedPos = pos
			}
			if renamed {
				changes = append(changes, dbdriver.ColumnChange{Kind: dbdriver.ColumnRename, Name: d.OrigName, Column: toColumnMeta(d), Position: movedPos})
			} else if bodyChanged || moved {
				changes = append(changes, dbdriver.ColumnChange{Kind: dbdriver.ColumnModify, Name: d.OrigName, Column: toColumnMeta(d), Position: movedPos})
			}
		}
		p := trimmed
		prevName = &p
	}

	// PRIMARY KEY as its own drop/add pair, from per-column flags in order.
	var origPK, desiredPK []string
	for _, c := range orig {
		if c.IsPrimaryKey {
			origPK = append(origPK, c.Name)
		}
	}
	for _, d := range desired {
		if d.IsPrimaryKey && strings.TrimSpace(d.Name) != "" {
			desiredPK = append(desiredPK, strings.TrimSpace(d.Name))
		}
	}
	var pk *dbdriver.PrimaryKeyChange
	if !slicesEqual(origPK, desiredPK) {
		pk = &dbdriver.PrimaryKeyChange{Drop: len(origPK) > 0, Columns: desiredPK}
	}
	return changes, pk
}

func positionalClause(prevName *string) *dbdriver.ColumnPosition {
	if prevName == nil {
		return &dbdriver.ColumnPosition{First: true}
	}
	return &dbdriver.ColumnPosition{After: *prevName}
}

// positionChanged reports whether origName's previous-column differs between
// the surviving-only original order and the surviving-only desired order.
func positionChanged(origName string, survivingDesiredOrig, survivingOrigOrder []string) bool {
	finalIdx, origIdx := -1, -1
	for i, n := range survivingDesiredOrig {
		if n == origName {
			finalIdx = i
			break
		}
	}
	for i, n := range survivingOrigOrder {
		if n == origName {
			origIdx = i
			break
		}
	}
	if finalIdx < 0 || origIdx < 0 {
		return false
	}
	var prevFinal, prevOrig string
	if finalIdx > 0 {
		prevFinal = survivingDesiredOrig[finalIdx-1]
	}
	if origIdx > 0 {
		prevOrig = survivingOrigOrder[origIdx-1]
	}
	return prevFinal != prevOrig
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- indexes ---------------------------------------------------------------

func filledIndexColumns(cols []dbdriver.IndexColumn) []dbdriver.IndexColumn {
	var out []dbdriver.IndexColumn
	for _, c := range cols {
		if strings.TrimSpace(c.Name) != "" {
			out = append(out, dbdriver.IndexColumn{Name: strings.TrimSpace(c.Name), Order: strings.ToUpper(c.Order)})
		}
	}
	return out
}

// normIndexOrder treats an omitted sort direction and ASC as the same thing —
// databases report the default direction as "ASC" even when the DDL omitted it.
func normIndexOrder(s string) string {
	u := strings.ToUpper(s)
	if u == "" {
		return "ASC"
	}
	return u
}

func indexColumnsEqual(a []dbdriver.IndexColumn, b []dbdriver.IndexColumn) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if normIndexOrder(a[i].Order) != normIndexOrder(b[i].Order) {
			return false
		}
	}
	return true
}

// normIndexType treats an omitted index type and BTREE as the same thing —
// databases report the default access method as "BTREE" even when the DDL
// omitted a USING clause.
func normIndexType(s string) string {
	u := strings.ToUpper(s)
	if u == "" {
		return "BTREE"
	}
	return u
}

func indexesEqual(d Index, orig dbdriver.IndexInfo) bool {
	if d.Name != orig.Name {
		return false
	}
	if d.Unique != orig.Unique {
		return false
	}
	if normIndexType(d.Type) != normIndexType(orig.Type) {
		return false
	}
	if d.Comment != orig.Comment {
		return false
	}
	return indexColumnsEqual(d.Columns, orig.Columns)
}

func toIndexInfo(d Index, cols []dbdriver.IndexColumn) *dbdriver.IndexInfo {
	return &dbdriver.IndexInfo{
		Name:    strings.TrimSpace(d.Name),
		Columns: cols,
		Unique:  d.Unique,
		Type:    strings.ToUpper(d.Type),
		Comment: d.Comment,
	}
}

func diffIndexes(orig []dbdriver.IndexInfo, desired []Index) []dbdriver.IndexChange {
	// PRIMARY lives in the PK pipeline.
	var origNonPK []dbdriver.IndexInfo
	for _, ix := range orig {
		if !ix.Primary {
			origNonPK = append(origNonPK, ix)
		}
	}
	origByName := make(map[string]dbdriver.IndexInfo, len(origNonPK))
	for _, ix := range origNonPK {
		origByName[ix.Name] = ix
	}
	claimed := make(map[string]bool, len(desired))
	for _, d := range desired {
		if !d.Primary && d.OrigName != "" {
			claimed[d.OrigName] = true
		}
	}

	var drops, adds []dbdriver.IndexChange
	for _, ix := range origNonPK {
		if !claimed[ix.Name] {
			drops = append(drops, dbdriver.IndexChange{Kind: "drop", Name: ix.Name})
		}
	}
	for _, d := range desired {
		if d.Primary {
			continue
		}
		cols := filledIndexColumns(d.Columns)
		if strings.TrimSpace(d.Name) == "" || len(cols) == 0 {
			continue
		}
		if d.OrigName == "" {
			adds = append(adds, dbdriver.IndexChange{Kind: "add", Index: toIndexInfo(d, cols)})
			continue
		}
		origIx, ok := origByName[d.OrigName]
		if !ok {
			continue
		}
		if !indexesEqual(d, origIx) {
			drops = append(drops, dbdriver.IndexChange{Kind: "drop", Name: d.OrigName})
			adds = append(adds, dbdriver.IndexChange{Kind: "add", Index: toIndexInfo(d, cols)})
		}
	}
	return append(drops, adds...)
}

// ---- foreign keys ----------------------------------------------------------

// normRefAction treats an absent ON UPDATE/DELETE clause and RESTRICT as the
// same thing — MySQL reports RESTRICT as absence.
func normRefAction(s string) string {
	u := strings.ToUpper(s)
	if u == "" {
		return "RESTRICT"
	}
	return u
}

func fkEqual(d ForeignKey, orig dbdriver.ForeignKeyInfo) bool {
	if d.Name != orig.Name {
		return false
	}
	if !slicesEqual(d.Columns, orig.Columns) {
		return false
	}
	if d.ReferencedSchema != orig.ReferencedSchema {
		return false
	}
	if d.ReferencedTable != orig.ReferencedTable {
		return false
	}
	if !slicesEqual(d.ReferencedColumns, orig.ReferencedColumns) {
		return false
	}
	if normRefAction(d.OnUpdate) != normRefAction(orig.OnUpdate) {
		return false
	}
	if normRefAction(d.OnDelete) != normRefAction(orig.OnDelete) {
		return false
	}
	return true
}

func fkComplete(d ForeignKey) bool {
	return strings.TrimSpace(d.Name) != "" && len(d.Columns) > 0 &&
		strings.TrimSpace(d.ReferencedTable) != "" && len(d.ReferencedColumns) > 0
}

func toForeignKeyInfo(d ForeignKey) *dbdriver.ForeignKeyInfo {
	return &dbdriver.ForeignKeyInfo{
		Name:              strings.TrimSpace(d.Name),
		Columns:           d.Columns,
		ReferencedSchema:  d.ReferencedSchema,
		ReferencedTable:   d.ReferencedTable,
		ReferencedColumns: d.ReferencedColumns,
		OnUpdate:          d.OnUpdate,
		OnDelete:          d.OnDelete,
	}
}

func diffForeignKeys(orig []dbdriver.ForeignKeyInfo, desired []ForeignKey) []dbdriver.ForeignKeyChange {
	origByName := make(map[string]dbdriver.ForeignKeyInfo, len(orig))
	for _, fk := range orig {
		origByName[fk.Name] = fk
	}
	claimed := make(map[string]bool, len(desired))
	for _, d := range desired {
		if d.OrigName != "" {
			claimed[d.OrigName] = true
		}
	}

	var drops, adds []dbdriver.ForeignKeyChange
	for _, fk := range orig {
		if !claimed[fk.Name] {
			drops = append(drops, dbdriver.ForeignKeyChange{Kind: "drop", Name: fk.Name})
		}
	}
	for _, d := range desired {
		if strings.TrimSpace(d.Name) == "" {
			continue
		}
		if d.OrigName == "" {
			if fkComplete(d) {
				adds = append(adds, dbdriver.ForeignKeyChange{Kind: "add", ForeignKey: toForeignKeyInfo(d)})
			}
			continue
		}
		origFK, ok := origByName[d.OrigName]
		if !ok {
			continue
		}
		if !fkEqual(d, origFK) {
			drops = append(drops, dbdriver.ForeignKeyChange{Kind: "drop", Name: d.OrigName})
			if fkComplete(d) {
				adds = append(adds, dbdriver.ForeignKeyChange{Kind: "add", ForeignKey: toForeignKeyInfo(d)})
			}
		}
	}
	return append(drops, adds...)
}
