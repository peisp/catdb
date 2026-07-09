package dmdrv

// Bulk (whole-schema) metadata reads — the dbdriver.BulkMetadata optional
// extension. Structure sync compares every table of a schema; the shared
// query constants in metadata.go drop their per-table filter here and the
// results are grouped by table, so the whole schema is served in three
// queries total.

import (
	"context"
	"fmt"

	"catdb/internal/dbdriver"
)

var _ dbdriver.BulkMetadata = metadata{}

func (m metadata) ListAllColumns(ctx context.Context, db, schema string) (map[string][]dbdriver.ColumnMeta, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListAllColumns requires a schema name")
	}
	rows, err := m.db.QueryContext(ctx, columnsQuery+` ORDER BY c.TABLE_NAME, c.COLUMN_ID`, s, s, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list all columns: %w", err)
	}
	defer rows.Close()
	out := map[string][]dbdriver.ColumnMeta{}
	for rows.Next() {
		table, c, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		out[table] = append(out[table], c)
	}
	return out, rows.Err()
}

func (m metadata) ListAllIndexes(ctx context.Context, db, schema string) (map[string][]dbdriver.IndexInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListAllIndexes requires a schema name")
	}
	rows, err := m.db.QueryContext(ctx, indexesQuery+` ORDER BY i.TABLE_NAME, i.INDEX_NAME, ic.COLUMN_POSITION`, s, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list all indexes: %w", err)
	}
	grouped, order, err := scanIndexes(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]dbdriver.IndexInfo{}
	for _, k := range order {
		out[k.table] = append(out[k.table], *grouped[k])
	}
	return out, nil
}

func (m metadata) ListAllForeignKeys(ctx context.Context, db, schema string) (map[string][]dbdriver.ForeignKeyInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListAllForeignKeys requires a schema name")
	}
	rows, err := m.db.QueryContext(ctx, foreignKeysQuery+` ORDER BY a.TABLE_NAME, a.CONSTRAINT_NAME, ac.POSITION`, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list all foreign keys: %w", err)
	}
	grouped, order, err := scanForeignKeys(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]dbdriver.ForeignKeyInfo{}
	for _, k := range order {
		out[k.table] = append(out[k.table], *grouped[k])
	}
	return out, nil
}
