// Package services — TransferService streams large query results to disk
// (CSV / JSON / SQL dump / Excel) without holding everything in memory or
// crossing IPC with bulk row payloads.
//
// Why path-based, not response-based:
//   - 500k-row Excel exports easily exceed the IPC 2MB body limit (even with
//     v3 auto-chunking, base64 in/out is wasteful).
//   - The front-end picks the path with the native SaveFile dialog (see
//     wailsbridge); the Service opens that path and writes directly.
//
// Cancellation: every loop checks ctx.Err() between row batches so the front-
// end's promise cancel propagates to the file writer.

package services

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"catdb/internal/core/scanner"
	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/wailsbridge"
)

// TransferFormat enumerates the supported file types. Strings match
// front-end identifiers and the file extension we expect.
type TransferFormat string

const (
	FormatCSV  TransferFormat = "csv"
	FormatJSON TransferFormat = "json"
	FormatSQL  TransferFormat = "sql"
	FormatXLSX TransferFormat = "xlsx"
)

// ExportOptions is what the front-end sends to ExportQuery.
type ExportOptions struct {
	Format        TransferFormat `json:"format"`
	Path          string         `json:"path"`
	BatchSize     int            `json:"batchSize,omitempty"`
	IncludeHeader bool           `json:"includeHeader,omitempty"` // CSV: write column row
	IncludeDDL    bool           `json:"includeDDL,omitempty"`    // SQL: include CREATE TABLE (single-table exports only)
	TableName     string         `json:"tableName,omitempty"`     // SQL: target table name for INSERTs
	DB            string         `json:"db,omitempty"`            // database the SQL addresses (per-db routing)
}

// ExportResult is the synchronous return — progress events still fire during.
type ExportResult struct {
	TransferID string `json:"transferId"`
	Path       string `json:"path"`
	RowsTotal  int64  `json:"rowsTotal"`
	BytesTotal int64  `json:"bytesTotal"`
	ElapsedMs  int64  `json:"elapsedMs"`
}

// TransferService wraps export + import. It owns no streaming state itself —
// each call opens, writes, and closes — so multiple concurrent exports are
// safe as long as ctx for each is independent.
type TransferService struct {
	mgr *session.Manager
}

// NewTransferService wires the session manager.
func NewTransferService(mgr *session.Manager) *TransferService {
	return &TransferService{mgr: mgr}
}

func (s *TransferService) ServiceName() string { return "TransferService" }

func emitProgress(transferID string, rows int64, done bool, errMsg string) {
	wailsbridge.Emit("transfer:progress", map[string]any{
		"transferId": transferID,
		"rows":       rows,
		"done":       done,
		"error":      errMsg,
	})
}

// ExportQuery runs sqlText and streams every row through the chosen format
// into opts.Path. Returns when EOF (or ctx cancel / error).
//
// For Excel: uses excelize NewStreamWriter — rows are flushed to disk as we
// go; memory stays bounded.
// For CSV/JSON: encoding/csv + json.Encoder with newline framing (JSON Lines).
// For SQL: per-row INSERT (lightweight, easy to import anywhere).
func (s *TransferService) ExportQuery(ctx context.Context, connID, sqlText string, opts ExportOptions) (ExportResult, error) {
	return s.exportStreaming(ctx, connID, sqlText, opts, "")
}

func (s *TransferService) exportStreaming(ctx context.Context, connID, sqlText string, opts ExportOptions, ddlPrefix string) (ExportResult, error) {
	var empty ExportResult
	if connID == "" {
		return empty, fmt.Errorf("TransferService: connID is required")
	}
	if strings.TrimSpace(sqlText) == "" {
		return empty, fmt.Errorf("TransferService: sql is empty")
	}
	if opts.Path == "" {
		return empty, fmt.Errorf("TransferService: path is required")
	}
	if opts.Format == "" {
		return empty, fmt.Errorf("TransferService: format is required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}

	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return empty, err
		}
	}
	q, err := dbdriver.RouteQuerier(ctx, conn, opts.DB)
	if err != nil {
		return empty, err
	}

	transferID := "x-" + uuid.NewString()
	start := time.Now()

	rs, err := q.Query(ctx, sqlText)
	if err != nil {
		return empty, err
	}
	defer rs.Close()
	cols := rs.Columns()

	if err := os.MkdirAll(filepath.Dir(opts.Path), 0o755); err != nil {
		return empty, fmt.Errorf("TransferService: prepare dir: %w", err)
	}

	var writer rowWriter
	switch opts.Format {
	case FormatCSV:
		writer, err = newCSVWriter(opts.Path, cols, opts.IncludeHeader)
	case FormatJSON:
		writer, err = newJSONLWriter(opts.Path, cols)
	case FormatSQL:
		var dia dbdriver.Dialect
		dia, err = s.dialect(ctx, connID)
		if err != nil {
			return empty, err
		}
		writer, err = newSQLWriter(opts.Path, cols, opts, ddlPrefix, dia)
	case FormatXLSX:
		writer, err = newXLSXWriter(opts.Path, cols)
	default:
		return empty, fmt.Errorf("TransferService: unsupported format %q", opts.Format)
	}
	if err != nil {
		return empty, err
	}
	defer writer.Close()

	var rowsTotal int64
	for {
		if err := ctx.Err(); err != nil {
			emitProgress(transferID, rowsTotal, true, err.Error())
			return empty, err
		}
		batch, done, err := rs.Next(opts.BatchSize)
		if err != nil {
			emitProgress(transferID, rowsTotal, true, err.Error())
			return empty, err
		}
		for _, row := range batch {
			if err := writer.WriteRow(row); err != nil {
				emitProgress(transferID, rowsTotal, true, err.Error())
				return empty, err
			}
			rowsTotal++
		}
		if len(batch) > 0 {
			emitProgress(transferID, rowsTotal, false, "")
		}
		if done {
			break
		}
	}

	if err := writer.Close(); err != nil {
		emitProgress(transferID, rowsTotal, true, err.Error())
		return empty, err
	}

	var size int64
	if st, err := os.Stat(opts.Path); err == nil {
		size = st.Size()
	}

	emitProgress(transferID, rowsTotal, true, "")
	return ExportResult{
		TransferID: transferID,
		Path:       opts.Path,
		RowsTotal:  rowsTotal,
		BytesTotal: size,
		ElapsedMs:  time.Since(start).Milliseconds(),
	}, nil
}

// ExportTable is a convenience wrapper around ExportQuery for "dump this
// whole table". When IncludeDDL is true the resulting SQL file gets a
// CREATE TABLE prefix from the driver's metadata layer.
func (s *TransferService) ExportTable(ctx context.Context, connID, db, schema, table string, opts ExportOptions) (ExportResult, error) {
	if table == "" || (db == "" && schema == "") {
		return ExportResult{}, fmt.Errorf("TransferService: table and db (or schema) required")
	}
	dia, err := s.dialect(ctx, connID)
	if err != nil {
		return ExportResult{}, err
	}
	sqlText := fmt.Sprintf("SELECT * FROM %s", dbdriver.QualifyTable(dia, db, schema, table))
	// Carry the table name through so the SQL writer can produce real
	// INSERT INTO <table> statements rather than INSERT INTO query_results,
	// and the database so exportStreaming routes to the right pool.
	opts.TableName = table
	opts.DB = db

	// For SQL+IncludeDDL we prepend SHOW CREATE TABLE before the data dump.
	var ddlPrefix string
	if opts.Format == FormatSQL && opts.IncludeDDL {
		conn, err := s.mgr.Get(connID)
		if err != nil {
			conn, err = s.mgr.Open(ctx, connID)
			if err != nil {
				return ExportResult{}, err
			}
		}
		m := conn.Metadata()
		if m == nil {
			return ExportResult{}, fmt.Errorf("TransferService: metadata adapter missing")
		}
		ddl, err := m.GetCreateTable(ctx, db, schema, table)
		if err != nil {
			return ExportResult{}, fmt.Errorf("TransferService: get DDL: %w", err)
		}
		ddlPrefix = ddl + ";\n\n"
	}
	return s.exportStreaming(ctx, connID, sqlText, opts, ddlPrefix)
}

func (s *TransferService) dialect(ctx context.Context, connID string) (dbdriver.Dialect, error) {
	name, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	return d.Dialect(), nil
}

// rowWriter is the minimal contract every format implements.
type rowWriter interface {
	WriteRow(row []any) error
	Close() error
}

// --- CSV writer -----------------------------------------------------------

type csvWriter struct {
	f *os.File
	w *csv.Writer
}

func newCSVWriter(path string, cols []dbdriver.ColumnMeta, includeHeader bool) (*csvWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("TransferService: create %s: %w", path, err)
	}
	w := csv.NewWriter(f)
	if includeHeader {
		names := make([]string, len(cols))
		for i, c := range cols {
			names[i] = c.Name
		}
		if err := w.Write(names); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return &csvWriter{f: f, w: w}, nil
}

func (c *csvWriter) WriteRow(row []any) error {
	rec := make([]string, len(row))
	for i, v := range row {
		rec[i] = cellToString(v)
	}
	return c.w.Write(rec)
}

func (c *csvWriter) Close() error {
	if c.w != nil {
		c.w.Flush()
		if err := c.w.Error(); err != nil {
			_ = c.f.Close()
			c.f = nil
			return err
		}
	}
	if c.f != nil {
		err := c.f.Close()
		c.f = nil
		return err
	}
	return nil
}

// --- JSON Lines writer ----------------------------------------------------

type jsonlWriter struct {
	f       *os.File
	enc     *json.Encoder
	colKeys []string
}

func newJSONLWriter(path string, cols []dbdriver.ColumnMeta) (*jsonlWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("TransferService: create %s: %w", path, err)
	}
	keys := make([]string, len(cols))
	for i, c := range cols {
		keys[i] = c.Name
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &jsonlWriter{f: f, enc: enc, colKeys: keys}, nil
}

func (j *jsonlWriter) WriteRow(row []any) error {
	m := make(map[string]any, len(j.colKeys))
	for i, k := range j.colKeys {
		if i < len(row) {
			m[k] = row[i]
		}
	}
	return j.enc.Encode(m)
}

func (j *jsonlWriter) Close() error {
	if j.f == nil {
		return nil
	}
	err := j.f.Close()
	j.f = nil
	return err
}

// --- SQL dump writer ------------------------------------------------------

type sqlWriter struct {
	f      *os.File
	prefix string
	// insertPrefix is the constant "INSERT INTO <tbl> (<cols>) VALUES ("
	// head, rendered once with the source dialect's identifier quoting.
	insertPrefix string
	rules        dbdriver.ScriptRules
}

func newSQLWriter(path string, cols []dbdriver.ColumnMeta, opts ExportOptions, ddlPrefix string, dia dbdriver.Dialect) (*sqlWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("TransferService: create %s: %w", path, err)
	}
	tbl := opts.TableName
	if tbl == "" {
		tbl = "exported"
	}
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(dia.QuoteIdentifier(tbl))
	b.WriteString(" (")
	for i, c := range cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(dia.QuoteIdentifier(c.Name))
	}
	b.WriteString(") VALUES (")
	w := &sqlWriter{f: f, prefix: ddlPrefix, insertPrefix: b.String(), rules: dia.ScriptRules()}
	if w.prefix != "" {
		if _, err := f.WriteString(w.prefix); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return w, nil
}

func (w *sqlWriter) WriteRow(row []any) error {
	var b strings.Builder
	b.WriteString(w.insertPrefix)
	for i, v := range row {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(sqlLiteral(v, w.rules))
	}
	b.WriteString(");\n")
	_, err := w.f.WriteString(b.String())
	return err
}

func (w *sqlWriter) Close() error {
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// sqlLiteral renders v as a SQL literal for a dump file — dump output is
// inherently literal-encoded (a .sql file has no bind parameters). The
// dialect's ScriptRules select the escaping family: BackslashEscapes ⇒
// MySQL-style strings and X'…' byte literals, otherwise standard-conforming
// strings and Postgres '\x…' bytea literals.
func sqlLiteral(v any, rules dbdriver.ScriptRules) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "TRUE"
		}
		return "FALSE"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", x)
	case scanner.BigIntString:
		return x.Value
	case scanner.BytesValue:
		raw, err := base64.StdEncoding.DecodeString(x.Base64)
		if err != nil {
			return quoteSQLString(x.Base64, rules)
		}
		if rules.BackslashEscapes {
			return "X'" + hex.EncodeToString(raw) + "'"
		}
		return `'\x` + hex.EncodeToString(raw) + "'"
	case string:
		return quoteSQLString(x, rules)
	default:
		raw, _ := json.Marshal(x)
		return quoteSQLString(string(raw), rules)
	}
}

func quoteSQLString(s string, rules dbdriver.ScriptRules) string {
	if rules.BackslashEscapes {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// --- XLSX writer (streaming) ---------------------------------------------

type xlsxWriter struct {
	f      *excelize.File
	stream *excelize.StreamWriter
	path   string
	rowIdx int
}

func newXLSXWriter(path string, cols []dbdriver.ColumnMeta) (*xlsxWriter, error) {
	f := excelize.NewFile()
	stream, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("TransferService: NewStreamWriter: %w", err)
	}
	// Header row.
	header := make([]any, len(cols))
	for i, c := range cols {
		header[i] = c.Name
	}
	cell, _ := excelize.CoordinatesToCellName(1, 1)
	if err := stream.SetRow(cell, header); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &xlsxWriter{f: f, stream: stream, path: path, rowIdx: 1}, nil
}

func (x *xlsxWriter) WriteRow(row []any) error {
	x.rowIdx++
	cell, _ := excelize.CoordinatesToCellName(1, x.rowIdx)
	cells := make([]any, len(row))
	for i, v := range row {
		cells[i] = cellExcelValue(v)
	}
	return x.stream.SetRow(cell, cells)
}

func (x *xlsxWriter) Close() error {
	if x.stream == nil {
		return nil
	}
	if err := x.stream.Flush(); err != nil {
		_ = x.f.Close()
		x.stream = nil
		return err
	}
	x.stream = nil
	if err := x.f.SaveAs(x.path); err != nil {
		_ = x.f.Close()
		return err
	}
	return x.f.Close()
}

// --- cross-database data transfer -----------------------------------------

// DataTransferRequest is what the front-end sends to StartTransfer.
type DataTransferRequest struct {
	SourceConnID string   `json:"sourceConnId"`
	SourceDB     string   `json:"sourceDb"`
	SourceSchema string   `json:"sourceSchema,omitempty"`
	TargetConnID string   `json:"targetConnId"`
	TargetDB     string   `json:"targetDb"`
	TargetSchema string   `json:"targetSchema,omitempty"`
	Tables       []string `json:"tables"`
	CreateTable  bool     `json:"createTable"`
	TransferMode string   `json:"transferMode"` // "append" or "overwrite"
	BatchSize    int      `json:"batchSize"`
}

// DataTransferResult is returned once all tables have been processed (or on
// first fatal error — partial results are still returned so the UI can show
// what made it through).
type DataTransferResult struct {
	TransferID   string                          `json:"transferId"`
	TableResults map[string]*TableTransferResult `json:"tableResults"`
}

// TableTransferResult reports the outcome for one table.
type TableTransferResult struct {
	Rows  int64  `json:"rows"`
	Error string `json:"error,omitempty"`
}

// StartTransfer copies data from source-connection tables to target-connection
// tables. The method is synchronous — callers cancel via ctx; progress events
// fire on transfer:progress.
func (s *TransferService) StartTransfer(ctx context.Context, req DataTransferRequest) (DataTransferResult, error) {
	var empty DataTransferResult
	transferID := "t-" + uuid.NewString()
	result := DataTransferResult{
		TransferID:   transferID,
		TableResults: make(map[string]*TableTransferResult),
	}

	if req.BatchSize <= 0 {
		req.BatchSize = 500
	}

	// Resolve source connection.
	srcConn, err := s.mgr.Get(req.SourceConnID)
	if err != nil {
		srcConn, err = s.mgr.Open(ctx, req.SourceConnID)
		if err != nil {
			return empty, fmt.Errorf("TransferService: source: %w", err)
		}
	}
	srcQ, err := dbdriver.RouteQuerier(ctx, srcConn, req.SourceDB)
	if err != nil {
		return empty, fmt.Errorf("TransferService: source: %w", err)
	}

	// Resolve target connection.
	tgtConn, err := s.mgr.Get(req.TargetConnID)
	if err != nil {
		tgtConn, err = s.mgr.Open(ctx, req.TargetConnID)
		if err != nil {
			return empty, fmt.Errorf("TransferService: target: %w", err)
		}
	}
	tgtQ, err := dbdriver.RouteQuerier(ctx, tgtConn, req.TargetDB)
	if err != nil {
		return empty, fmt.Errorf("TransferService: target: %w", err)
	}

	// Drivers / dialects for identifier quoting and DDL rendering.
	srcName, tgtName, srcD, tgtD, err := s.dialects(ctx, req.SourceConnID, req.TargetConnID)
	if err != nil {
		return empty, err
	}
	sameDriver := srcName == tgtName

	// --- pre-check which target tables already exist ----------------------
	// Avoids "IF NOT EXISTS" string surgery on the DDL text — support for
	// that clause is not portable across dialects (DM). A nil map (probe
	// failed, e.g. no Metadata adapter) makes every lookup below false, so
	// CREATE runs unconditionally with the DDL as-is and any failure is
	// recorded per-table instead of blocking the whole transfer.
	var existingTables map[string]bool
	if req.CreateTable {
		if tgtMeta := tgtConn.Metadata(); tgtMeta != nil {
			if tbls, err := tgtMeta.ListTables(ctx, req.TargetDB, req.TargetSchema); err == nil {
				existingTables = make(map[string]bool, len(tbls))
				for _, t := range tbls {
					existingTables[t.Name] = true
				}
			}
		}
	}

	for _, tableName := range req.Tables {
		if err := ctx.Err(); err != nil {
			emitProgress(transferID, 0, true, err.Error())
			return result, err
		}

		tr := &TableTransferResult{}
		result.TableResults[tableName] = tr

		// --- create target table if needed ----------------------------------
		if req.CreateTable && !existingTables[tableName] {
			// Same driver → source-native DDL (full fidelity); cross-driver →
			// re-rendered through the target dialect. Cross-driver column types
			// are carried over verbatim for now (see docs/异构数据库同步与传输方案.md).
			ddl, err := createTableDDL(ctx, srcConn.Metadata(), tgtD, sameDriver,
				req.SourceDB, req.SourceSchema, req.TargetDB, req.TargetSchema, tableName)
			if err != nil {
				tr.Error = fmt.Sprintf("get DDL: %v", err)
				continue
			}
			if _, err := tgtQ.Exec(ctx, ddl); err != nil {
				tr.Error = fmt.Sprintf("create table: %v", err)
				continue
			}
		}

		// --- overwrite mode: truncate target table --------------------------
		if req.TransferMode == "overwrite" {
			tgtQual := dbdriver.QualifyTable(tgtD, req.TargetDB, req.TargetSchema, tableName)
			if _, err := tgtQ.Exec(ctx, tgtD.TruncateTableSQL(tgtQual)); err != nil {
				// May fail with FK refs (or the dialect already emitted DELETE
				// above); fall back to DELETE.
				if _, err := tgtQ.Exec(ctx, "DELETE FROM "+tgtQual); err != nil {
					tr.Error = fmt.Sprintf("truncate: %v", err)
					continue
				}
			}
		}

		// --- query source --------------------------------------------------
		srcQual := dbdriver.QualifyTable(srcD, req.SourceDB, req.SourceSchema, tableName)
		rs, err := srcQ.Query(ctx, "SELECT * FROM "+srcQual)
		if err != nil {
			tr.Error = fmt.Sprintf("query source: %v", err)
			continue
		}

		cols := rs.Columns()
		colNames := make([]string, len(cols))
		for i, c := range cols {
			colNames[i] = c.Name
		}
		tgtQual := dbdriver.QualifyTable(tgtD, req.TargetDB, req.TargetSchema, tableName)

		var rowsTotal int64
	loop:
		for {
			batch, done, err := rs.Next(req.BatchSize)
			if err != nil {
				tr.Error = fmt.Sprintf("read batch: %v", err)
				break
			}
			if len(batch) > 0 {
				if err := s.transferBatch(ctx, tgtQ, tgtQual, colNames, batch, tgtD); err != nil {
					tr.Error = err.Error()
					break
				}
				rowsTotal += int64(len(batch))
				emitProgress(transferID, rowsTotal, false, "")
			}
			if done {
				break loop
			}
		}
		_ = rs.Close()
		tr.Rows = rowsTotal
	}

	emitProgress(transferID, 0, true, "")
	return result, nil
}

// maxInsertParams caps placeholders per statement — MySQL prepared statements
// hard-limit at 65535, so wide tables get chunked into several INSERTs.
const maxInsertParams = 60000

// transferBatch inserts rows through multi-value parameterized INSERTs
// (CLAUDE.md #4): values cross drivers as bind parameters, never as literals,
// so each driver applies its own escaping and type coercion.
func (s *TransferService) transferBatch(ctx context.Context, q dbdriver.Querier, qualifiedTable string, colNames []string, rows [][]any, d dbdriver.Dialect) error {
	if len(rows) == 0 || len(colNames) == 0 {
		return nil
	}
	perStmt := maxInsertParams / len(colNames)
	if perStmt < 1 {
		perStmt = 1
	}
	for start := 0; start < len(rows); start += perStmt {
		end := min(start+perStmt, len(rows))
		sqlText, args := buildBatchInsert(d, qualifiedTable, colNames, rows[start:end])
		if _, err := q.Exec(ctx, sqlText, args...); err != nil {
			return fmt.Errorf("insert batch: %w", err)
		}
	}
	return nil
}

// buildBatchInsert renders one multi-value parameterized INSERT plus its
// flattened argument list.
func buildBatchInsert(d dbdriver.Dialect, qualifiedTable string, colNames []string, rows [][]any) (string, []any) {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString("INSERT INTO ")
	b.WriteString(qualifiedTable)
	b.WriteString(" (")
	for i, name := range colNames {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.QuoteIdentifier(name))
	}
	b.WriteString(") VALUES ")
	args := make([]any, 0, len(rows)*len(colNames))
	n := 0
	for ri, row := range rows {
		if ri > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(")
		for i, v := range row {
			if i > 0 {
				b.WriteString(", ")
			}
			n++
			b.WriteString(d.Placeholder(n))
			args = append(args, bindArg(v))
		}
		b.WriteString(")")
	}
	return b.String(), args
}

// bindArg unwraps the scanner's front-end marker types into values drivers
// can bind: BytesValue → raw []byte, BigIntString → digit string (both
// servers coerce it to the column's integer type).
func bindArg(v any) any {
	switch x := v.(type) {
	case scanner.BytesValue:
		raw, err := base64.StdEncoding.DecodeString(x.Base64)
		if err != nil {
			return x.Base64
		}
		return raw
	case scanner.BigIntString:
		return x.Value
	default:
		return v
	}
}

// dialects resolves both driver names and their Dialect.
func (s *TransferService) dialects(ctx context.Context, srcID, tgtID string) (srcName, tgtName string, src, tgt dbdriver.Dialect, err error) {
	srcName, err = s.mgr.DriverName(ctx, srcID)
	if err != nil {
		return "", "", nil, nil, err
	}
	d, err := registry.Get(srcName)
	if err != nil {
		return "", "", nil, nil, err
	}
	src = d.Dialect()

	tgtName, err = s.mgr.DriverName(ctx, tgtID)
	if err != nil {
		return "", "", nil, nil, err
	}
	d, err = registry.Get(tgtName)
	if err != nil {
		return "", "", nil, nil, err
	}
	tgt = d.Dialect()
	return
}

// --- helpers --------------------------------------------------------------

func cellToString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", x)
	case scanner.BigIntString:
		return x.Value
	case scanner.BytesValue:
		return x.Base64
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

// cellExcelValue keeps native numeric/bool types as-is so Excel formats them
// correctly; strings and complex objects become text.
func cellExcelValue(v any) any {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, string:
		return x
	case scanner.BigIntString:
		return x.Value
	case scanner.BytesValue:
		return x.Base64
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}
