package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"catdb/internal/core/schemadiff"
	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/wailsbridge"
)

// SyncService drives structure synchronization (and, in S4, data
// synchronization): compare two databases, render the DDL that reconciles the
// target with the source, and execute the user-approved subset.
//
// Compare is read-only; Execute only ever runs the statements the front-end
// passes back, so the user always sees exactly what will run.
type SyncService struct {
	mgr *session.Manager
}

// NewSyncService wires the session manager dependency.
func NewSyncService(mgr *session.Manager) *SyncService {
	return &SyncService{mgr: mgr}
}

func (s *SyncService) ServiceName() string { return "SyncService" }

func (s *SyncService) resolveConn(ctx context.Context, connID string) (dbdriver.Connection, error) {
	if connID == "" {
		return nil, fmt.Errorf("SyncService: connID is required")
	}
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return nil, err
		}
	}
	return conn, nil
}

func (s *SyncService) resolveDriver(ctx context.Context, connID string) (dbdriver.Driver, error) {
	name, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, err
	}
	return registry.Get(name)
}

// ---- Compare ----------------------------------------------------------------

// SchemaCompareRequest names the two databases to reconcile. Tables narrows
// the comparison to the listed tables; empty means every table.
type SchemaCompareRequest struct {
	SourceConnID string   `json:"sourceConnId"`
	SourceDB     string   `json:"sourceDb"`
	SourceSchema string   `json:"sourceSchema"`
	TargetConnID string   `json:"targetConnId"`
	TargetDB     string   `json:"targetDb"`
	TargetSchema string   `json:"targetSchema"`
	Tables       []string `json:"tables"`
}

// SchemaObjectDiff is one table/view in the compare result. Statements is the
// DDL (already rendered for the target) that reconciles the object;
// Destructive marks statements that can lose data or objects (DROP TABLE /
// DROP VIEW / column drops) so the UI can default them to unchecked.
type SchemaObjectDiff struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`   // "table" | "view"
	Status      string   `json:"status"` // "create" | "drop" | "alter" | "same"
	Statements  []string `json:"statements"`
	Destructive bool     `json:"destructive,omitempty"`
	// Error carries a per-object failure slug + detail; the object is shown
	// but not executable. Other objects still compare.
	Error string `json:"error,omitempty"`
}

// SchemaCompareResult is the ordered object list (tables first, then views).
type SchemaCompareResult struct {
	SyncID  string             `json:"syncId"`
	Objects []SchemaObjectDiff `json:"objects"`
}

// emitSchemaCompareProgress streams per-object compare progress so the UI can
// fill its list live instead of freezing on large databases. Phases:
// "object-start" (name/kind), "object-done" (full diff), "done" (all objects).
func emitSchemaCompareProgress(syncID, phase, name, kind string, obj *SchemaObjectDiff) {
	payload := map[string]any{
		"syncId": syncID,
		"phase":  phase,
		"name":   name,
		"kind":   kind,
	}
	if obj != nil {
		payload["object"] = obj
	}
	wailsbridge.Emit("sync:schema-progress", payload)
}

// CompareSchemas diffs every (selected) table and view of source vs target
// and renders reconciliation DDL through the TARGET's dialect — that's the
// server the statements will run on.
func (s *SyncService) CompareSchemas(ctx context.Context, req SchemaCompareRequest) (SchemaCompareResult, error) {
	var empty SchemaCompareResult
	if req.SourceDB == "" && req.SourceSchema == "" || req.TargetDB == "" && req.TargetSchema == "" {
		return empty, fmt.Errorf("SyncService: source and target database are required")
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
	return compareSchemasConns(ctx, srcConn, tgtConn, srcDrv, tgtDrv, req)
}

// compareSchemasConns is the connection-level body of CompareSchemas —
// separated so integration tests can drive it with directly-opened driver
// connections (no session.Manager / keyring involved).
func compareSchemasConns(ctx context.Context, srcConn, tgtConn dbdriver.Connection, srcDrv, tgtDrv dbdriver.Driver, req SchemaCompareRequest) (SchemaCompareResult, error) {
	var empty SchemaCompareResult
	srcMeta, tgtMeta := srcConn.Metadata(), tgtConn.Metadata()
	if srcMeta == nil || tgtMeta == nil {
		return empty, fmt.Errorf("SyncService: connection has no metadata adapter")
	}
	dia := tgtDrv.Dialect()
	sameDriver := srcDrv.Name() == tgtDrv.Name()

	srcTables, err := srcMeta.ListTables(ctx, req.SourceDB, req.SourceSchema)
	if err != nil {
		return empty, fmt.Errorf("SyncService: list source tables: %w", err)
	}
	tgtTables, err := tgtMeta.ListTables(ctx, req.TargetDB, req.TargetSchema)
	if err != nil {
		return empty, fmt.Errorf("SyncService: list target tables: %w", err)
	}

	filter := map[string]bool{}
	for _, t := range req.Tables {
		filter[t] = true
	}
	included := func(name string) bool { return len(filter) == 0 || filter[name] }

	srcByName := map[string]dbdriver.TableInfo{}
	for _, t := range srcTables {
		if included(t.Name) {
			srcByName[t.Name] = t
		}
	}
	tgtByName := map[string]dbdriver.TableInfo{}
	for _, t := range tgtTables {
		if included(t.Name) {
			tgtByName[t.Name] = t
		}
	}

	out := SchemaCompareResult{SyncID: fmt.Sprintf("sc-%d", time.Now().UnixNano())}

	// Source-only tables need one native CREATE TABLE read each with no bulk
	// form — fetched concurrently (the query is latency-bound, not
	// server-bound). Their diffs land in a map the sorted loop below drains.
	var createOnly []string
	createSet := map[string]bool{}
	for name := range srcByName {
		if _, ok := tgtByName[name]; !ok {
			createOnly = append(createOnly, name)
			createSet[name] = true
		}
	}
	sort.Strings(createOnly)
	names := sortedKeys(srcByName, tgtByName)

	// Fill the UI's live list immediately: one object-start per table BEFORE
	// the (potentially slow) metadata prefetch below, so the compare never
	// looks frozen. Create-only tables get theirs from the DDL prefetch
	// workers as each one starts.
	for _, name := range names {
		if !createSet[name] {
			emitSchemaCompareProgress(out.SyncID, "object-start", name, "table", nil)
		}
	}

	// The three network-bound prefetches are independent — run them
	// concurrently so the silent window before per-object results is
	// max(src, tgt, ddl) instead of their sum:
	//   - whole-schema bulk prefetch per side via the optional BulkMetadata
	//     extension (3 queries per side instead of 3 per table — the
	//     difference between seconds and minutes on remote connections;
	//     nil = extension absent, per-table reads)
	//   - CREATE TABLE DDL of source-only tables (streams its object-done
	//     events while the bulk prefetches are still loading)
	var (
		srcBulk, tgtBulk *schemaBulk
		createDiffs      map[string]SchemaObjectDiff
		pf               sync.WaitGroup
	)
	pf.Add(3)
	go func() { defer pf.Done(); srcBulk = prefetchSchemaBulk(ctx, srcMeta, req.SourceDB, req.SourceSchema) }()
	go func() { defer pf.Done(); tgtBulk = prefetchSchemaBulk(ctx, tgtMeta, req.TargetDB, req.TargetSchema) }()
	go func() {
		defer pf.Done()
		createDiffs = prefetchCreateDDLs(ctx, srcMeta, dia, sameDriver, req, createOnly, out.SyncID)
	}()
	pf.Wait()
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		if d, ok := createDiffs[name]; ok {
			// Progress for these was already emitted by the prefetch workers.
			out.Objects = append(out.Objects, d)
			continue
		}
		emitSchemaCompareProgress(out.SyncID, "object-start", name, "table", nil)
		_, inSrc := srcByName[name]
		_, inTgt := tgtByName[name]
		diff := SchemaObjectDiff{Name: name, Kind: "table"}
		switch {
		case inSrc && !inTgt:
			diff.Status = "create"
			stmt, cerr := createTableDDL(ctx, srcMeta, dia, sameDriver, req, name)
			if cerr != nil {
				diff.Error = cerr.Error()
			} else {
				diff.Statements = []string{stmt}
			}
		case !inSrc && inTgt:
			diff.Status = "drop"
			diff.Destructive = true
			diff.Statements = []string{
				"DROP TABLE " + dbdriver.QualifyTable(dia, req.TargetDB, req.TargetSchema, name) + ";",
			}
		default:
			src, rerr := readTableSchemaBulk(ctx, srcMeta, srcBulk, req.SourceDB, req.SourceSchema, name, srcByName[name].Comment)
			if rerr != nil {
				diff.Error = rerr.Error()
				break
			}
			tgt, rerr := readTableSchemaBulk(ctx, tgtMeta, tgtBulk, req.TargetDB, req.TargetSchema, name, tgtByName[name].Comment)
			if rerr != nil {
				diff.Error = rerr.Error()
				break
			}
			cs := schemadiff.Diff(tgt, schemadiff.FromTableSchema(src, tgt), schemadiff.Options{NormalizeType: dia.NormalizeType})
			if cs.Empty() {
				diff.Status = "same"
				break
			}
			diff.Status = "alter"
			diff.Destructive = hasDestructiveChange(cs)
			stmts, gerr := dia.GenerateAlterTable(req.TargetDB, req.TargetSchema, name, cs)
			if gerr != nil {
				diff.Error = gerr.Error()
				break
			}
			diff.Statements = stmts
		}
		out.Objects = append(out.Objects, diff)
		emitSchemaCompareProgress(out.SyncID, "object-done", name, "table", &diff)
	}

	// Views: presence + normalized-definition comparison. Best-effort — a
	// driver whose view definitions can't be read just reports the error on
	// the affected object.
	viewDiffs := compareViews(ctx, srcConn, tgtConn, dia, req, included, out.SyncID)
	out.Objects = append(out.Objects, viewDiffs...)
	emitSchemaCompareProgress(out.SyncID, "done", "", "", nil)
	return out, nil
}

// prefetchCreateDDLs resolves the CREATE diff for every source-only table
// with bounded concurrency: SHOW CREATE TABLE is a cheap, latency-bound
// round-trip, so 8 in flight cut a 1000-table cold-target compare by ~8×
// while keeping the source's native DDL fidelity. Per-table failures land in
// the object's Error field; a cancelled ctx just leaves tables unfetched (the
// caller aborts on ctx anyway).
func prefetchCreateDDLs(ctx context.Context, srcMeta dbdriver.Metadata, dia dbdriver.Dialect, sameDriver bool, req SchemaCompareRequest, names []string, syncID string) map[string]SchemaObjectDiff {
	out := make(map[string]SchemaObjectDiff, len(names))
	if len(names) == 0 {
		return out
	}
	const workers = 8
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, workers)
	)
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			emitSchemaCompareProgress(syncID, "object-start", name, "table", nil)
			diff := SchemaObjectDiff{Name: name, Kind: "table", Status: "create"}
			stmt, err := createTableDDL(ctx, srcMeta, dia, sameDriver, req, name)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				diff.Error = err.Error()
			} else {
				diff.Statements = []string{stmt}
			}
			emitSchemaCompareProgress(syncID, "object-done", name, "table", &diff)
			mu.Lock()
			out[name] = diff
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	return out
}

// createTableDDL renders the CREATE statement for a source-only table. Same
// driver → the source's native DDL text (full fidelity: collation,
// auto-increment, …). Cross-driver → regenerate from the read schema through
// the target dialect.
func createTableDDL(ctx context.Context, srcMeta dbdriver.Metadata, dia dbdriver.Dialect, sameDriver bool, req SchemaCompareRequest, name string) (string, error) {
	if sameDriver {
		ddl, err := srcMeta.GetCreateTable(ctx, req.SourceDB, req.SourceSchema, name)
		if err != nil {
			return "", err
		}
		// The native DDL text names the table unqualified; qualify it so the
		// statement lands in the target database regardless of the target
		// connection's default database.
		qualified := dbdriver.QualifyTable(dia, req.TargetDB, req.TargetSchema, name)
		ddl = strings.Replace(ddl, "CREATE TABLE "+dia.QuoteIdentifier(name), "CREATE TABLE "+qualified, 1)
		if !strings.HasSuffix(strings.TrimSpace(ddl), ";") {
			ddl += ";"
		}
		return ddl, nil
	}
	src, err := readTableSchema(ctx, srcMeta, req.SourceDB, req.SourceSchema, name, "")
	if err != nil {
		return "", err
	}
	src.Name = name
	src.Schema = firstNonEmptyStr(req.TargetSchema, req.TargetDB)
	return dia.GenerateCreateTable(src)
}

// schemaBulk caches whole-schema metadata fetched through the optional
// dbdriver.BulkMetadata extension.
type schemaBulk struct {
	cols map[string][]dbdriver.ColumnMeta
	ixs  map[string][]dbdriver.IndexInfo
	fks  map[string][]dbdriver.ForeignKeyInfo
}

// prefetchSchemaBulk loads the whole schema in three queries when the driver
// supports it. Any failure degrades to nil — callers fall back to per-table
// reads, never fail the compare over an optional fast path.
func prefetchSchemaBulk(ctx context.Context, m dbdriver.Metadata, db, schema string) *schemaBulk {
	bm, ok := m.(dbdriver.BulkMetadata)
	if !ok {
		return nil
	}
	cols, err := bm.ListAllColumns(ctx, db, schema)
	if err != nil {
		return nil
	}
	ixs, err := bm.ListAllIndexes(ctx, db, schema)
	if err != nil {
		return nil
	}
	fks, err := bm.ListAllForeignKeys(ctx, db, schema)
	if err != nil {
		return nil
	}
	return &schemaBulk{cols: cols, ixs: ixs, fks: fks}
}

// readTableSchemaBulk serves a table from the prefetched cache; absent cache
// or a table missing from it (e.g. created after the prefetch) falls back to
// the per-table reads.
func readTableSchemaBulk(ctx context.Context, m dbdriver.Metadata, b *schemaBulk, db, schema, table, comment string) (dbdriver.TableSchema, error) {
	if b != nil {
		if cols, ok := b.cols[table]; ok {
			return dbdriver.TableSchema{
				Name: table, Schema: schema, Comment: comment,
				Columns: cols, Indexes: b.ixs[table], ForeignKeys: b.fks[table],
			}, nil
		}
	}
	return readTableSchema(ctx, m, db, schema, table, comment)
}

// readTableSchema assembles a TableSchema from the driver's metadata reads.
func readTableSchema(ctx context.Context, m dbdriver.Metadata, db, schema, table, comment string) (dbdriver.TableSchema, error) {
	cols, err := m.ListColumns(ctx, db, schema, table)
	if err != nil {
		return dbdriver.TableSchema{}, fmt.Errorf("list columns of %s: %w", table, err)
	}
	ix, err := m.ListIndexes(ctx, db, schema, table)
	if err != nil {
		return dbdriver.TableSchema{}, fmt.Errorf("list indexes of %s: %w", table, err)
	}
	fks, err := m.ListForeignKeys(ctx, db, schema, table)
	if err != nil {
		return dbdriver.TableSchema{}, fmt.Errorf("list foreign keys of %s: %w", table, err)
	}
	return dbdriver.TableSchema{Name: table, Schema: schema, Columns: cols, Indexes: ix, ForeignKeys: fks, Comment: comment}, nil
}

// hasDestructiveChange reports whether applying cs can lose data.
func hasDestructiveChange(cs dbdriver.ChangeSet) bool {
	for _, ch := range cs.Columns {
		if ch.Kind == dbdriver.ColumnDrop {
			return true
		}
	}
	return false
}

// ---- views -------------------------------------------------------------------

// compareViews diffs view presence + normalized definitions via the drivers'
// Metadata.ListViewDefinitions.
func compareViews(ctx context.Context, srcConn, tgtConn dbdriver.Connection, dia dbdriver.Dialect, req SchemaCompareRequest, included func(string) bool, syncID string) []SchemaObjectDiff {
	srcMeta, tgtMeta := srcConn.Metadata(), tgtConn.Metadata()
	srcViews, err := srcMeta.ListViews(ctx, req.SourceDB, req.SourceSchema)
	if err != nil {
		return nil
	}
	tgtViews, err := tgtMeta.ListViews(ctx, req.TargetDB, req.TargetSchema)
	if err != nil {
		return nil
	}
	srcSet := map[string]bool{}
	for _, v := range srcViews {
		if included(v.Name) {
			srcSet[v.Name] = true
		}
	}
	tgtSet := map[string]bool{}
	for _, v := range tgtViews {
		if included(v.Name) {
			tgtSet[v.Name] = true
		}
	}
	if len(srcSet) == 0 && len(tgtSet) == 0 {
		return nil
	}

	srcDefs, srcErr := srcMeta.ListViewDefinitions(ctx, req.SourceDB, req.SourceSchema)
	tgtDefs, tgtErr := tgtMeta.ListViewDefinitions(ctx, req.TargetDB, req.TargetSchema)

	var out []SchemaObjectDiff
	for _, name := range sortedKeys(srcSet, tgtSet) {
		emitSchemaCompareProgress(syncID, "object-start", name, "view", nil)
		diff := SchemaObjectDiff{Name: name, Kind: "view"}
		inSrc, inTgt := srcSet[name], tgtSet[name]
		fq := dbdriver.QualifyTable(dia, req.TargetDB, req.TargetSchema, name)
		switch {
		case inSrc && !inTgt:
			diff.Status = "create"
			if srcErr != nil {
				diff.Error = "fetch-view-definition: " + srcErr.Error()
				break
			}
			diff.Statements = []string{createViewStatement(fq, srcDefs[name], req, dia)}
		case !inSrc && inTgt:
			diff.Status = "drop"
			diff.Destructive = true
			diff.Statements = []string{"DROP VIEW " + fq + ";"}
		default:
			if srcErr != nil || tgtErr != nil {
				diff.Error = "fetch-view-definition"
				break
			}
			if normalizeViewDef(srcDefs[name], req.SourceDB) == normalizeViewDef(tgtDefs[name], req.TargetDB) {
				diff.Status = "same"
				break
			}
			diff.Status = "alter"
			diff.Statements = []string{createViewStatement(fq, srcDefs[name], req, dia)}
		}
		out = append(out, diff)
		emitSchemaCompareProgress(syncID, "object-done", name, "view", &diff)
	}
	return out
}

// normalizeViewDef canonicalizes a VIEW_DEFINITION for equality comparison:
// strip the schema qualifier of its own database and collapse whitespace, so
// the "same" view living in two differently-named databases compares equal.
func normalizeViewDef(def, ownDB string) string {
	d := strings.ReplaceAll(def, "`"+ownDB+"`.", "")
	return strings.Join(strings.Fields(d), " ")
}

// retargetViewDef rewrites source-database qualifiers to the target database
// so the definition resolves on the target server.
func retargetViewDef(def string, req SchemaCompareRequest, dia dbdriver.Dialect) string {
	srcDB := firstNonEmptyStr(req.SourceSchema, req.SourceDB)
	tgtDB := firstNonEmptyStr(req.TargetSchema, req.TargetDB)
	if srcDB == "" || srcDB == tgtDB {
		return def
	}
	return strings.ReplaceAll(def, dia.QuoteIdentifier(srcDB)+".", dia.QuoteIdentifier(tgtDB)+".")
}

func createViewStatement(fq, def string, req SchemaCompareRequest, dia dbdriver.Dialect) string {
	return "CREATE OR REPLACE VIEW " + fq + " AS " + retargetViewDef(def, req, dia) + ";"
}

// ---- Execute ------------------------------------------------------------------

// SchemaSyncExecRequest carries the user-approved statements to run on the
// target, in order. StopOnError halts at the first failure; otherwise the
// remaining statements still run and failures are reported per statement.
type SchemaSyncExecRequest struct {
	TargetConnID string `json:"targetConnId"`
	// TargetDB routes the statements to the addressed database on drivers
	// whose databases are isolation boundaries (Postgres).
	TargetDB    string   `json:"targetDb,omitempty"`
	Statements  []string `json:"statements"`
	StopOnError bool     `json:"stopOnError"`
}

// SchemaSyncStatementResult reports one executed statement.
type SchemaSyncStatementResult struct {
	Statement string `json:"statement"`
	Error     string `json:"error,omitempty"`
}

// SchemaSyncExecResult summarizes an Execute run.
type SchemaSyncExecResult struct {
	SyncID   string                      `json:"syncId"`
	Executed int                         `json:"executed"`
	Failed   int                         `json:"failed"`
	Results  []SchemaSyncStatementResult `json:"results"`
}

func emitSyncProgress(syncID string, index, total int, errMsg string, done bool) {
	wailsbridge.Emit("sync:progress", map[string]any{
		"syncId": syncID,
		"index":  index,
		"total":  total,
		"error":  errMsg,
		"done":   done,
	})
}

// ExecuteSchemaSync runs the approved DDL statements sequentially on the
// target connection, emitting sync:progress after each statement.
func (s *SyncService) ExecuteSchemaSync(ctx context.Context, req SchemaSyncExecRequest) (SchemaSyncExecResult, error) {
	var empty SchemaSyncExecResult
	if len(req.Statements) == 0 {
		return empty, fmt.Errorf("SyncService: no statements to execute")
	}
	conn, err := s.resolveConn(ctx, req.TargetConnID)
	if err != nil {
		return empty, err
	}
	return executeSchemaStatements(ctx, conn, req)
}

// executeSchemaStatements is the connection-level body of ExecuteSchemaSync.
func executeSchemaStatements(ctx context.Context, conn dbdriver.Connection, req SchemaSyncExecRequest) (SchemaSyncExecResult, error) {
	var empty SchemaSyncExecResult
	q, err := dbdriver.RouteQuerier(ctx, conn, req.TargetDB)
	if err != nil {
		return empty, err
	}

	res := SchemaSyncExecResult{SyncID: fmt.Sprintf("ss-%d", time.Now().UnixNano())}
	total := len(req.Statements)
	for i, raw := range req.Statements {
		if err := ctx.Err(); err != nil {
			emitSyncProgress(res.SyncID, i, total, "cancelled", true)
			return res, err
		}
		stmt := strings.TrimSuffix(strings.TrimSpace(raw), ";")
		if stmt == "" {
			continue
		}
		item := SchemaSyncStatementResult{Statement: raw}
		if _, err := q.Exec(ctx, stmt); err != nil {
			item.Error = err.Error()
			res.Failed++
			res.Results = append(res.Results, item)
			emitSyncProgress(res.SyncID, i+1, total, item.Error, false)
			if req.StopOnError {
				emitSyncProgress(res.SyncID, i+1, total, "", true)
				return res, nil
			}
			continue
		}
		res.Executed++
		res.Results = append(res.Results, item)
		emitSyncProgress(res.SyncID, i+1, total, "", false)
	}
	emitSyncProgress(res.SyncID, total, total, "", true)
	return res, nil
}

// ---- helpers ------------------------------------------------------------------

// sortedKeys merges the keys of two same-key-type maps into a sorted slice.
func sortedKeys[V1, V2 any](a map[string]V1, b map[string]V2) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
