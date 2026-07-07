package postgresdrv

// Bulk (whole-schema) metadata reads — the dbdriver.BulkMetadata optional
// extension used by structure sync to avoid ~3N per-table round-trips. Each
// variant reuses the shared query constants from metadata.go without the
// relname filter and groups results by table.

import (
	"context"
	"fmt"

	"catdb/internal/dbdriver"
)

var _ dbdriver.BulkMetadata = metadata{}

func (m metadata) ListAllColumns(ctx context.Context, db, schema string) (map[string][]dbdriver.ColumnMeta, error) {
	ns := resolveSchema(db, schema)
	rows, err := m.pool.Query(ctx, columnsQuery+` ORDER BY c.relname, a.attnum`, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list all columns: %w", err)
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
	ns := resolveSchema(db, schema)
	rows, err := m.pool.Query(ctx, indexesQuery+` ORDER BY c.relname, i.relname, k.n`, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list all indexes: %w", err)
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
	ns := resolveSchema(db, schema)
	rows, err := m.pool.Query(ctx, foreignKeysQuery+` ORDER BY c.relname, con.conname, u.ord`, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list all foreign keys: %w", err)
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
