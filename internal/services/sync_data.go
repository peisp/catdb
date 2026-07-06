package services

// Data synchronization half of SyncService: primary-key ordered streaming
// merge comparison (internal/core/datasync) between two databases, then
// optional execution of the differences as parameterized statements on a
// dedicated target connection.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"catdb/internal/core/datasync"
	"catdb/internal/dbdriver"
	"catdb/wailsbridge"
)

// DataCompareRequest names the two databases and (optionally) the tables to
// compare. Empty Tables = every table present on both sides.
type DataCompareRequest struct {
	SourceConnID string   `json:"sourceConnId"`
	SourceDB     string   `json:"sourceDb"`
	SourceSchema string   `json:"sourceSchema"`
	TargetConnID string   `json:"targetConnId"`
	TargetDB     string   `json:"targetDb"`
	TargetSchema string   `json:"targetSchema"`
	Tables       []string `json:"tables"`
	BatchSize    int      `json:"batchSize"`
}

// DataTableDiff is one table's compare/execute outcome. Skipped carries a
// stable slug the front-end localizes; Samples is a bounded preview.
type DataTableDiff struct {
	Table         string                `json:"table"`
	Inserts       int64                 `json:"inserts"`
	Updates       int64                 `json:"updates"`
	Deletes       int64                 `json:"deletes"`
	ScannedSource int64                 `json:"scannedSource"`
	ScannedTarget int64                 `json:"scannedTarget"`
	Samples       []datasync.DiffSample `json:"samples,omitempty"`
	Skipped       string                `json:"skipped,omitempty"` // no-primary-key | pk-mismatch | missing-on-target | no-common-columns
	Error         string                `json:"error,omitempty"`
}

// DataCompareResult is the per-table outcome list, in name order.
type DataCompareResult struct {
	SyncID string          `json:"syncId"`
	Tables []DataTableDiff `json:"tables"`
}

// DataSyncExecRequest executes the differences for the listed tables.
// AllowDelete gates DELETE of target-only rows (default false = keep them).
type DataSyncExecRequest struct {
	SourceConnID string   `json:"sourceConnId"`
	SourceDB     string   `json:"sourceDb"`
	SourceSchema string   `json:"sourceSchema"`
	TargetConnID string   `json:"targetConnId"`
	TargetDB     string   `json:"targetDb"`
	TargetSchema string   `json:"targetSchema"`
	Tables       []string `json:"tables"`
	AllowDelete  bool     `json:"allowDelete"`
	BatchSize    int      `json:"batchSize"`
}

// DataSyncExecResult mirrors DataCompareResult for the write pass.
type DataSyncExecResult struct {
	SyncID string          `json:"syncId"`
	Tables []DataTableDiff `json:"tables"`
}

// emitDataProgress streams per-table lifecycle events so the UI can render a
// live row list during long compares/syncs. Phases: "table-start" (row begins),
// "progress" (running counts, ~every merge batch), "table-done" (final counts
// or skipped/error), "done" (whole run finished).
func emitDataProgress(syncID, table, phase string, st datasync.Stats, skipped, errMsg string) {
	wailsbridge.Emit("sync:data-progress", map[string]any{
		"syncId":        syncID,
		"table":         table,
		"phase":         phase,
		"inserts":       st.Inserts,
		"updates":       st.Updates,
		"deletes":       st.Deletes,
		"scannedSource": st.ScannedSource,
		"scannedTarget": st.ScannedTarget,
		"skipped":       skipped,
		"error":         errMsg,
	})
}

// tablePair carries everything the merge needs for one table.
type tablePair struct {
	table   string
	pk      []string
	columns []string
	skipped string
}

// resolveTablePairs validates PKs and computes the shared column list for
// every candidate table. Tables that cannot be synced come back with a
// Skipped slug instead of being silently dropped.
func resolveTablePairs(ctx context.Context, srcConn, tgtConn dbdriver.Connection, req DataCompareRequest) ([]tablePair, error) {
	srcMeta, tgtMeta := srcConn.Metadata(), tgtConn.Metadata()
	if srcMeta == nil || tgtMeta == nil {
		return nil, fmt.Errorf("SyncService: connection has no metadata adapter")
	}
	srcTables, err := srcMeta.ListTables(ctx, req.SourceDB, req.SourceSchema)
	if err != nil {
		return nil, fmt.Errorf("SyncService: list source tables: %w", err)
	}
	tgtTables, err := tgtMeta.ListTables(ctx, req.TargetDB, req.TargetSchema)
	if err != nil {
		return nil, fmt.Errorf("SyncService: list target tables: %w", err)
	}
	tgtSet := map[string]bool{}
	for _, t := range tgtTables {
		tgtSet[t.Name] = true
	}
	filter := map[string]bool{}
	for _, t := range req.Tables {
		filter[t] = true
	}

	var names []string
	for _, t := range srcTables {
		if len(filter) == 0 || filter[t.Name] {
			names = append(names, t.Name)
		}
	}
	sort.Strings(names)

	srcEd, tgtEd := srcConn.Editor(), tgtConn.Editor()
	if srcEd == nil || tgtEd == nil {
		return nil, fmt.Errorf("SyncService: connection has no editor adapter")
	}

	// Whole-schema column prefetch (optional BulkMetadata fast path) — cuts
	// two per-table round-trips from the validation pass.
	srcBulk := prefetchSchemaBulk(ctx, srcMeta, req.SourceDB, req.SourceSchema)
	tgtBulk := prefetchSchemaBulk(ctx, tgtMeta, req.TargetDB, req.TargetSchema)
	colsOf := func(b *schemaBulk, m dbdriver.Metadata, db, schema, name string) ([]dbdriver.ColumnMeta, error) {
		if b != nil {
			if c, ok := b.cols[name]; ok {
				return c, nil
			}
		}
		return m.ListColumns(ctx, db, schema, name)
	}
	// pkOf serves the primary key from the bulk index cache (PRIMARY entry is
	// in key-sequence order, same as Editor.PrimaryKeys). Tables without a
	// PRIMARY index fall back to the editor, which also probes for a unique
	// non-null index — that semantic stays per-table.
	pkOf := func(b *schemaBulk, ed dbdriver.Editor, db, schema, name string) ([]string, error) {
		if b != nil {
			for _, ix := range b.ixs[name] {
				if ix.Primary {
					cols := make([]string, len(ix.Columns))
					for i, c := range ix.Columns {
						cols[i] = c.Name
					}
					return cols, nil
				}
			}
		}
		return ed.PrimaryKeys(ctx, db, schema, name)
	}

	var out []tablePair
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := tablePair{table: name}
		if !tgtSet[name] {
			p.skipped = "missing-on-target"
			out = append(out, p)
			continue
		}
		srcPK, err := pkOf(srcBulk, srcEd, req.SourceDB, req.SourceSchema, name)
		if err != nil {
			return nil, fmt.Errorf("SyncService: source primary keys of %s: %w", name, err)
		}
		tgtPK, err := pkOf(tgtBulk, tgtEd, req.TargetDB, req.TargetSchema, name)
		if err != nil {
			return nil, fmt.Errorf("SyncService: target primary keys of %s: %w", name, err)
		}
		if len(srcPK) == 0 || len(tgtPK) == 0 {
			p.skipped = "no-primary-key"
			out = append(out, p)
			continue
		}
		if !sameStringSet(srcPK, tgtPK) {
			p.skipped = "pk-mismatch"
			out = append(out, p)
			continue
		}
		srcCols, err := colsOf(srcBulk, srcMeta, req.SourceDB, req.SourceSchema, name)
		if err != nil {
			return nil, fmt.Errorf("SyncService: source columns of %s: %w", name, err)
		}
		tgtCols, err := colsOf(tgtBulk, tgtMeta, req.TargetDB, req.TargetSchema, name)
		if err != nil {
			return nil, fmt.Errorf("SyncService: target columns of %s: %w", name, err)
		}
		tgtColSet := map[string]bool{}
		for _, c := range tgtCols {
			tgtColSet[c.Name] = true
		}
		var common []string
		for _, c := range srcCols {
			if tgtColSet[c.Name] {
				common = append(common, c.Name)
			}
		}
		if len(common) == 0 {
			p.skipped = "no-common-columns"
			out = append(out, p)
			continue
		}
		commonSet := map[string]bool{}
		for _, c := range common {
			commonSet[c] = true
		}
		pkOK := true
		for _, k := range srcPK {
			if !commonSet[k] {
				pkOK = false
				break
			}
		}
		if !pkOK {
			p.skipped = "pk-mismatch"
			out = append(out, p)
			continue
		}
		p.pk = srcPK
		p.columns = common
		out = append(out, p)
	}
	return out, nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

func newTableSync(p tablePair, srcConn, tgtConn dbdriver.Connection, srcDia, tgtDia dbdriver.Dialect, req DataCompareRequest) *datasync.TableSync {
	return &datasync.TableSync{
		Table:      p.table,
		SrcQuerier: srcConn.Querier(),
		SrcDialect: srcDia,
		SrcDB:      req.SourceDB,
		SrcSchema:  req.SourceSchema,
		TgtQuerier: tgtConn.Querier(),
		TgtDialect: tgtDia,
		TgtDB:      req.TargetDB,
		TgtSchema:  req.TargetSchema,
		PK:         p.pk,
		Columns:    p.columns,
		BatchSize:  req.BatchSize,
	}
}

// CompareData runs the dry-run merge for every resolvable table: counts +
// bounded samples, no writes.
func (s *SyncService) CompareData(ctx context.Context, req DataCompareRequest) (DataCompareResult, error) {
	var empty DataCompareResult
	srcConn, err := s.resolveConn(ctx, req.SourceConnID)
	if err != nil {
		return empty, err
	}
	tgtConn, err := s.resolveConn(ctx, req.TargetConnID)
	if err != nil {
		return empty, err
	}
	srcDrv, err := s.resolveDriver(ctx, req.SourceConnID)
	if err != nil {
		return empty, err
	}
	tgtDrv, err := s.resolveDriver(ctx, req.TargetConnID)
	if err != nil {
		return empty, err
	}
	return compareDataConns(ctx, srcConn, tgtConn, srcDrv, tgtDrv, req)
}

// compareDataConns is the connection-level body of CompareData.
func compareDataConns(ctx context.Context, srcConn, tgtConn dbdriver.Connection, srcDrv, tgtDrv dbdriver.Driver, req DataCompareRequest) (DataCompareResult, error) {
	var empty DataCompareResult
	pairs, err := resolveTablePairs(ctx, srcConn, tgtConn, req)
	if err != nil {
		return empty, err
	}

	const sampleLimit = 100
	res := DataCompareResult{SyncID: fmt.Sprintf("dc-%d", time.Now().UnixNano())}
	for _, p := range pairs {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		d := DataTableDiff{Table: p.table, Skipped: p.skipped}
		if p.skipped != "" {
			emitDataProgress(res.SyncID, p.table, "table-done", datasync.Stats{}, p.skipped, "")
			res.Tables = append(res.Tables, d)
			continue
		}
		emitDataProgress(res.SyncID, p.table, "table-start", datasync.Stats{}, "", "")
		ts := newTableSync(p, srcConn, tgtConn, srcDrv.Dialect(), tgtDrv.Dialect(), req)
		stats, samples, cerr := ts.Compare(ctx, sampleLimit, func(st datasync.Stats) {
			emitDataProgress(res.SyncID, p.table, "progress", st, "", "")
		})
		if cerr != nil {
			if ctx.Err() != nil {
				return empty, cerr
			}
			d.Error = cerr.Error()
		}
		d.Inserts, d.Updates, d.Deletes = stats.Inserts, stats.Updates, stats.Deletes
		d.ScannedSource, d.ScannedTarget = stats.ScannedSource, stats.ScannedTarget
		d.Samples = samples
		emitDataProgress(res.SyncID, p.table, "table-done", stats, "", d.Error)
		res.Tables = append(res.Tables, d)
	}
	emitDataProgress(res.SyncID, "", "done", datasync.Stats{}, "", "")
	return res, nil
}

// ExecuteDataSync applies the differences for the listed tables. Writes run
// on a dedicated target connection in batched transactions; the shared
// connection other windows use is never blocked.
func (s *SyncService) ExecuteDataSync(ctx context.Context, req DataSyncExecRequest) (DataSyncExecResult, error) {
	var empty DataSyncExecResult
	if len(req.Tables) == 0 {
		return empty, fmt.Errorf("SyncService: no tables selected")
	}
	srcConn, err := s.resolveConn(ctx, req.SourceConnID)
	if err != nil {
		return empty, err
	}
	tgtConn, err := s.resolveConn(ctx, req.TargetConnID)
	if err != nil {
		return empty, err
	}
	srcDrv, err := s.resolveDriver(ctx, req.SourceConnID)
	if err != nil {
		return empty, err
	}
	tgtDrv, err := s.resolveDriver(ctx, req.TargetConnID)
	if err != nil {
		return empty, err
	}

	// One dedicated write connection for the whole run (rule 9: transactions
	// never ride the shared pool connection).
	writeConn, err := s.mgr.OpenDedicated(ctx, req.TargetConnID)
	if err != nil {
		return empty, fmt.Errorf("SyncService: open dedicated target connection: %w", err)
	}
	defer func() { _ = writeConn.Close() }()
	return executeDataSyncConns(ctx, srcConn, tgtConn, writeConn, srcDrv, tgtDrv, req)
}

// executeDataSyncConns is the connection-level body of ExecuteDataSync.
// writeConn must be a dedicated connection owned by the caller.
func executeDataSyncConns(ctx context.Context, srcConn, tgtConn, writeConn dbdriver.Connection, srcDrv, tgtDrv dbdriver.Driver, req DataSyncExecRequest) (DataSyncExecResult, error) {
	var empty DataSyncExecResult
	compareReq := DataCompareRequest{
		SourceConnID: req.SourceConnID, SourceDB: req.SourceDB, SourceSchema: req.SourceSchema,
		TargetConnID: req.TargetConnID, TargetDB: req.TargetDB, TargetSchema: req.TargetSchema,
		Tables: req.Tables, BatchSize: req.BatchSize,
	}
	pairs, err := resolveTablePairs(ctx, srcConn, tgtConn, compareReq)
	if err != nil {
		return empty, err
	}

	res := DataSyncExecResult{SyncID: fmt.Sprintf("de-%d", time.Now().UnixNano())}
	for _, p := range pairs {
		if err := ctx.Err(); err != nil {
			emitDataProgress(res.SyncID, "", "done", datasync.Stats{}, "", "cancelled")
			return res, err
		}
		d := DataTableDiff{Table: p.table, Skipped: p.skipped}
		if p.skipped != "" {
			emitDataProgress(res.SyncID, p.table, "table-done", datasync.Stats{}, p.skipped, "")
			res.Tables = append(res.Tables, d)
			continue
		}
		emitDataProgress(res.SyncID, p.table, "table-start", datasync.Stats{}, "", "")
		ts := newTableSync(p, srcConn, tgtConn, srcDrv.Dialect(), tgtDrv.Dialect(), compareReq)
		stats, xerr := ts.Execute(ctx, writeConn, req.AllowDelete, func(st datasync.Stats) {
			emitDataProgress(res.SyncID, p.table, "progress", st, "", "")
		})
		if xerr != nil {
			if ctx.Err() != nil {
				emitDataProgress(res.SyncID, "", "done", datasync.Stats{}, "", "cancelled")
				res.Tables = append(res.Tables, d)
				return res, xerr
			}
			d.Error = xerr.Error()
		}
		d.Inserts, d.Updates, d.Deletes = stats.Inserts, stats.Updates, stats.Deletes
		d.ScannedSource, d.ScannedTarget = stats.ScannedSource, stats.ScannedTarget
		emitDataProgress(res.SyncID, p.table, "table-done", stats, "", d.Error)
		res.Tables = append(res.Tables, d)
	}
	emitDataProgress(res.SyncID, "", "done", datasync.Stats{}, "", "")
	return res, nil
}
