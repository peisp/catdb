package schemadiff

import (
	"strings"

	"catdb/internal/dbdriver"
)

// ToTableSchema converts a desired Table into a dbdriver.TableSchema suitable
// for Dialect.GenerateCreateTable, skipping unfinished editor rows the same
// way the ALTER pipeline does: blank column names, indexes without a name or
// any filled column, incomplete foreign keys. The primary key is collected
// from per-column flags in order.
func (t Table) ToTableSchema(name, schema string) dbdriver.TableSchema {
	out := dbdriver.TableSchema{Name: strings.TrimSpace(name), Schema: schema, Comment: t.Comment}
	for _, c := range t.Columns {
		trimmed := strings.TrimSpace(c.Name)
		if trimmed == "" {
			continue
		}
		out.Columns = append(out.Columns, dbdriver.ColumnMeta{
			Name:            trimmed,
			NativeType:      c.NativeType,
			Nullable:        c.Nullable,
			Default:         c.Default,
			IsPrimaryKey:    c.IsPrimaryKey,
			IsAutoIncrement: c.IsAutoIncrement,
			Comment:         c.Comment,
		})
		if c.IsPrimaryKey {
			out.PrimaryKey = append(out.PrimaryKey, trimmed)
		}
	}
	for _, ix := range t.Indexes {
		if ix.Primary {
			continue
		}
		cols := filledIndexColumns(ix.Columns)
		if strings.TrimSpace(ix.Name) == "" || len(cols) == 0 {
			continue
		}
		out.Indexes = append(out.Indexes, *toIndexInfo(ix, cols))
	}
	for _, fk := range t.ForeignKeys {
		if !fkComplete(fk) {
			continue
		}
		out.ForeignKeys = append(out.ForeignKeys, *toForeignKeyInfo(fk))
	}
	return out
}
