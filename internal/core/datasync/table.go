package datasync

import (
	"context"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// TableSync binds the merge engine to one concrete table pair. The caller
// (SyncService) resolves the common column list and the shared primary key
// up front; TableSync builds the ordered streaming SELECTs and drives Merge.
type TableSync struct {
	Table string

	SrcQuerier dbdriver.Querier
	SrcDialect dbdriver.Dialect
	SrcDB      string
	SrcSchema  string

	TgtQuerier dbdriver.Querier
	TgtDialect dbdriver.Dialect
	TgtDB      string
	TgtSchema  string

	// PK is the shared primary key; Columns the shared column list (PK
	// included), selected in identical order on both sides.
	PK      []string
	Columns []string

	BatchSize int
}

func (t *TableSync) batch() int {
	if t.BatchSize <= 0 {
		return 500
	}
	return t.BatchSize
}

// pkIndexes maps PK names to positions inside Columns.
func (t *TableSync) pkIndexes() ([]int, error) {
	var out []int
	for _, k := range t.PK {
		found := -1
		for i, c := range t.Columns {
			if c == k {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("datasync: pk column %q not in column list", k)
		}
		out = append(out, found)
	}
	return out, nil
}

func orderedSelect(dia dbdriver.Dialect, db, schema, table string, cols, pk []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = dia.QuoteIdentifier(c)
	}
	order := make([]string, len(pk))
	for i, c := range pk {
		order[i] = dia.QuoteIdentifier(c)
	}
	return fmt.Sprintf("SELECT %s FROM %s ORDER BY %s",
		strings.Join(quoted, ", "),
		dbdriver.QualifyTable(dia, db, schema, table),
		strings.Join(order, ", "))
}

// openStreams starts both ordered SELECTs. The returned close func must run
// even on error paths.
func (t *TableSync) openStreams(ctx context.Context) (src, tgt RowSource, closeAll func(), err error) {
	srcRS, err := t.SrcQuerier.Query(ctx, orderedSelect(t.SrcDialect, t.SrcDB, t.SrcSchema, t.Table, t.Columns, t.PK))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("datasync: read source %s: %w", t.Table, err)
	}
	tgtRS, err := t.TgtQuerier.Query(ctx, orderedSelect(t.TgtDialect, t.TgtDB, t.TgtSchema, t.Table, t.Columns, t.PK))
	if err != nil {
		_ = srcRS.Close()
		return nil, nil, nil, fmt.Errorf("datasync: read target %s: %w", t.Table, err)
	}
	closeAll = func() {
		_ = srcRS.Close()
		_ = tgtRS.Close()
	}
	return NewResultSetSource(srcRS, t.batch()), NewResultSetSource(tgtRS, t.batch()), closeAll, nil
}

// Compare runs the merge in dry-run mode: counts + up to sampleLimit samples.
func (t *TableSync) Compare(ctx context.Context, sampleLimit int, progress func(Stats)) (Stats, []DiffSample, error) {
	pkIdx, err := t.pkIndexes()
	if err != nil {
		return Stats{}, nil, err
	}
	src, tgt, closeAll, err := t.openStreams(ctx)
	if err != nil {
		return Stats{}, nil, err
	}
	defer closeAll()

	var samples []DiffSample
	take := func(kind string, row []any, changed []int) {
		if sampleLimit > 0 && len(samples) >= sampleLimit {
			return
		}
		s := DiffSample{Kind: kind, Key: keyValues(row, pkIdx)}
		for _, ci := range changed {
			s.Columns = append(s.Columns, t.Columns[ci])
		}
		samples = append(samples, s)
	}
	stats, err := Merge(ctx, src, tgt, pkIdx, Handlers{
		Insert:   func(srcRow []any) error { take("insert", srcRow, nil); return nil },
		Update:   func(srcRow, tgtRow []any, changed []int) error { take("update", srcRow, changed); return nil },
		Delete:   func(tgtRow []any) error { take("delete", tgtRow, nil); return nil },
		Progress: progress,
	})
	return stats, samples, err
}

func keyValues(row []any, pkIdx []int) []any {
	out := make([]any, len(pkIdx))
	for i, idx := range pkIdx {
		out[i] = normalize(row[idx])
	}
	return out
}

// Execute runs the merge in write mode: every difference becomes a
// parameterized Editor statement executed on writeConn inside batched
// transactions (one commit per BatchSize statements). writeConn should be a
// dedicated connection (session.Manager.OpenDedicated) so the long write
// transaction never blocks the shared connection.
func (t *TableSync) Execute(ctx context.Context, writeConn dbdriver.Connection, allowDelete bool, progress func(Stats)) (Stats, error) {
	pkIdx, err := t.pkIndexes()
	if err != nil {
		return Stats{}, err
	}
	ed := writeConn.Editor()
	if ed == nil {
		return Stats{}, fmt.Errorf("datasync: target connection has no editor")
	}
	src, tgt, closeAll, err := t.openStreams(ctx)
	if err != nil {
		return Stats{}, err
	}
	defer closeAll()

	ap := &txApplier{conn: writeConn, batch: t.batch()}
	defer ap.rollback()

	rowMap := func(row []any, only []int) map[string]any {
		m := map[string]any{}
		if only == nil {
			for i, c := range t.Columns {
				m[c] = row[i]
			}
			return m
		}
		for _, i := range only {
			m[t.Columns[i]] = row[i]
		}
		return m
	}
	pkMap := func(row []any) map[string]any {
		m := map[string]any{}
		for _, i := range pkIdx {
			m[t.Columns[i]] = row[i]
		}
		return m
	}

	h := Handlers{
		Insert: func(srcRow []any) error {
			sqlText, args, err := ed.BuildInsert(t.TgtDB, t.TgtSchema, t.Table, rowMap(srcRow, nil))
			if err != nil {
				return err
			}
			return ap.exec(ctx, sqlText, args)
		},
		Update: func(srcRow, tgtRow []any, changed []int) error {
			sqlText, args, err := ed.BuildUpdate(t.TgtDB, t.TgtSchema, t.Table, pkMap(tgtRow), rowMap(srcRow, changed))
			if err != nil {
				return err
			}
			return ap.exec(ctx, sqlText, args)
		},
		Progress: progress,
	}
	if allowDelete {
		h.Delete = func(tgtRow []any) error {
			sqlText, args, err := ed.BuildDelete(t.TgtDB, t.TgtSchema, t.Table, pkMap(tgtRow))
			if err != nil {
				return err
			}
			return ap.exec(ctx, sqlText, args)
		}
	}

	stats, err := Merge(ctx, src, tgt, pkIdx, h)
	if err != nil {
		return stats, err
	}
	if err := ap.flush(); err != nil {
		return stats, err
	}
	// Note: with allowDelete=false, stats.Deletes still counts the extra
	// target rows found — they just weren't touched.
	return stats, nil
}

// txApplier executes statements on the write connection in batched
// transactions: commit every `batch` statements, keep going.
type txApplier struct {
	conn    dbdriver.Connection
	batch   int
	tx      dbdriver.Tx
	pending int
}

func (a *txApplier) exec(ctx context.Context, sqlText string, args []any) error {
	if a.tx == nil {
		tx, err := a.conn.Begin(ctx, nil)
		if err != nil {
			return fmt.Errorf("datasync: begin: %w", err)
		}
		a.tx = tx
	}
	if _, err := a.tx.Exec(ctx, sqlText, args...); err != nil {
		return err
	}
	a.pending++
	if a.pending >= a.batch {
		if err := a.tx.Commit(); err != nil {
			return fmt.Errorf("datasync: commit: %w", err)
		}
		a.tx = nil
		a.pending = 0
	}
	return nil
}

func (a *txApplier) flush() error {
	if a.tx == nil {
		return nil
	}
	err := a.tx.Commit()
	a.tx = nil
	a.pending = 0
	if err != nil {
		return fmt.Errorf("datasync: commit: %w", err)
	}
	return nil
}

// rollback discards the in-flight transaction (deferred safety net — no-op
// after a clean flush).
func (a *txApplier) rollback() {
	if a.tx != nil {
		_ = a.tx.Rollback()
		a.tx = nil
		a.pending = 0
	}
}
