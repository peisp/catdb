package services

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"catdb/internal/dbdriver"
)

// ImportFormat enumerates the supported import file types. Same string values
// the front-end uses (matched by lower-case).
type ImportFormat string

const (
	ImportCSV ImportFormat = "csv"
	ImportSQL ImportFormat = "sql"
)

// ImportOptions describes how to ingest a local file into a connection.
//
// For CSV:
//   - HasHeader=true uses the first row as column names; otherwise Columns
//     must be provided.
//   - Each non-empty row becomes one INSERT.
//
// For SQL:
//   - The file is read as a series of statements separated by ';'.
//   - Pure -- line comments are stripped; block comments / inline -- are left
//     to the server (which handles them correctly).
type ImportOptions struct {
	Format    ImportFormat `json:"format"`
	Path      string       `json:"path"`
	DB        string       `json:"db,omitempty"`
	Table     string       `json:"table,omitempty"`   // CSV only
	HasHeader bool         `json:"hasHeader,omitempty"`
	Columns   []string     `json:"columns,omitempty"` // CSV only when HasHeader=false
	Delimiter string       `json:"delimiter,omitempty"`
	BatchSize int          `json:"batchSize,omitempty"`
}

// ImportResult mirrors ExportResult.
type ImportResult struct {
	TransferID    string `json:"transferId"`
	RowsAffected  int64  `json:"rowsAffected"`
	StatementsRun int64  `json:"statementsRun"`
	ElapsedMs     int64  `json:"elapsedMs"`
}

// ImportFile streams Path into the target DB. Path comes from the native
// OpenFile dialog (so the back-end has filesystem access).
func (s *TransferService) ImportFile(ctx context.Context, connID string, opts ImportOptions) (ImportResult, error) {
	var empty ImportResult
	if connID == "" {
		return empty, fmt.Errorf("TransferService: connID is required")
	}
	if opts.Path == "" {
		return empty, fmt.Errorf("TransferService: path is required")
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
	q := conn.Querier()
	if q == nil {
		return empty, fmt.Errorf("TransferService: connection has no querier")
	}

	transferID := "i-" + uuid.NewString()
	start := time.Now()

	switch opts.Format {
	case ImportCSV:
		return s.importCSV(ctx, conn, q, opts, transferID, start)
	case ImportSQL:
		return s.importSQL(ctx, q, opts, transferID, start)
	default:
		return empty, fmt.Errorf("TransferService: unsupported import format %q", opts.Format)
	}
}

func (s *TransferService) importCSV(
	ctx context.Context,
	conn dbdriver.Connection,
	q dbdriver.Querier,
	opts ImportOptions,
	transferID string,
	start time.Time,
) (ImportResult, error) {
	if opts.Table == "" {
		return ImportResult{}, fmt.Errorf("TransferService: CSV import requires table")
	}
	f, err := os.Open(opts.Path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("TransferService: open %s: %w", opts.Path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.LazyQuotes = true
	if opts.Delimiter != "" {
		r.Comma = rune(opts.Delimiter[0])
	}

	columns := opts.Columns
	if opts.HasHeader {
		row, err := r.Read()
		if err != nil {
			return ImportResult{}, fmt.Errorf("TransferService: read header: %w", err)
		}
		columns = row
	}
	if len(columns) == 0 {
		return ImportResult{}, fmt.Errorf("TransferService: column list missing — set HasHeader or Columns")
	}

	ed := conn.Editor()
	if ed == nil {
		return ImportResult{}, fmt.Errorf("TransferService: connection has no editor")
	}

	table := opts.Table
	if opts.DB != "" {
		table = opts.DB + "." + opts.Table
	}

	var rowsAffected int64
	var rowsRead int64
	for {
		if err := ctx.Err(); err != nil {
			emitProgress(transferID, rowsAffected, true, err.Error())
			return ImportResult{}, err
		}
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			emitProgress(transferID, rowsAffected, true, err.Error())
			return ImportResult{}, fmt.Errorf("TransferService: csv read: %w", err)
		}
		rowsRead++
		valMap := make(map[string]any, len(columns))
		for i, c := range columns {
			if i < len(row) {
				valMap[c] = row[i]
			}
		}
		sqlText, args, err := ed.BuildInsert(table, valMap)
		if err != nil {
			emitProgress(transferID, rowsAffected, true, err.Error())
			return ImportResult{}, err
		}
		res, err := q.Exec(ctx, sqlText, args...)
		if err != nil {
			emitProgress(transferID, rowsAffected, true, err.Error())
			return ImportResult{}, err
		}
		rowsAffected += res.RowsAffected
		if rowsRead%int64(opts.BatchSize) == 0 {
			emitProgress(transferID, rowsAffected, false, "")
		}
	}

	emitProgress(transferID, rowsAffected, true, "")
	return ImportResult{
		TransferID:   transferID,
		RowsAffected: rowsAffected,
		ElapsedMs:    time.Since(start).Milliseconds(),
	}, nil
}

func (s *TransferService) importSQL(
	ctx context.Context,
	q dbdriver.Querier,
	opts ImportOptions,
	transferID string,
	start time.Time,
) (ImportResult, error) {
	f, err := os.Open(opts.Path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("TransferService: open %s: %w", opts.Path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// SQL files frequently exceed 64KB on a single line (large INSERT batches).
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var stmt strings.Builder
	var statementsRun int64
	var rowsAffected int64

	exec := func() error {
		s := strings.TrimSpace(stmt.String())
		stmt.Reset()
		if s == "" {
			return nil
		}
		res, err := q.Exec(ctx, s)
		if err != nil {
			return err
		}
		statementsRun++
		rowsAffected += res.RowsAffected
		if statementsRun%50 == 0 {
			emitProgress(transferID, rowsAffected, false, "")
		}
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			emitProgress(transferID, rowsAffected, true, err.Error())
			return ImportResult{}, err
		}
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		stmt.WriteString(line)
		stmt.WriteByte('\n')
		if strings.HasSuffix(strings.TrimRight(trimmed, " \t"), ";") {
			if err := exec(); err != nil {
				emitProgress(transferID, rowsAffected, true, err.Error())
				return ImportResult{}, fmt.Errorf("TransferService: sql exec: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		emitProgress(transferID, rowsAffected, true, err.Error())
		return ImportResult{}, fmt.Errorf("TransferService: scan: %w", err)
	}
	if err := exec(); err != nil {
		emitProgress(transferID, rowsAffected, true, err.Error())
		return ImportResult{}, err
	}

	emitProgress(transferID, rowsAffected, true, "")
	return ImportResult{
		TransferID:    transferID,
		StatementsRun: statementsRun,
		RowsAffected:  rowsAffected,
		ElapsedMs:     time.Since(start).Milliseconds(),
	}, nil
}
