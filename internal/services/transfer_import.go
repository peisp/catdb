package services

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"

	"catdb/internal/core/sqlscript"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
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
//   - The file is streamed through the shared statement splitter
//     (internal/core/sqlscript): statements are separated on the active
//     delimiter, with strings/comments respected and the DELIMITER directive
//     honored, so dumps containing routines/triggers import correctly.
type ImportOptions struct {
	Format    ImportFormat `json:"format"`
	Path      string       `json:"path"`
	DB        string       `json:"db,omitempty"`
	Schema    string       `json:"schema,omitempty"`
	Table     string       `json:"table,omitempty"` // CSV only
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
		name, err := s.mgr.DriverName(ctx, connID)
		if err != nil {
			return empty, fmt.Errorf("TransferService: resolve driver: %w", err)
		}
		d, err := registry.Get(name)
		if err != nil {
			return empty, fmt.Errorf("TransferService: resolve driver: %w", err)
		}
		return s.importSQL(ctx, q, d.Dialect().ScriptRules(), opts, transferID, start)
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
		sqlText, args, err := ed.BuildInsert(opts.DB, opts.Schema, opts.Table, valMap)
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
	rules dbdriver.ScriptRules,
	opts ImportOptions,
	transferID string,
	start time.Time,
) (ImportResult, error) {
	f, err := os.Open(opts.Path)
	if err != nil {
		return ImportResult{}, fmt.Errorf("TransferService: open %s: %w", opts.Path, err)
	}
	defer f.Close()

	var statementsRun int64
	var rowsAffected int64

	// Stream the file through the shared splitter: it honors quotes, comments,
	// and the DELIMITER directive (so routine/trigger dumps import correctly),
	// while holding at most one statement in memory at a time.
	err = sqlscript.SplitStream(f, rules, func(stmt string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := q.Exec(ctx, stmt)
		if err != nil {
			return err
		}
		statementsRun++
		rowsAffected += res.RowsAffected
		if statementsRun%50 == 0 {
			emitProgress(transferID, rowsAffected, false, "")
		}
		return nil
	})
	if err != nil {
		emitProgress(transferID, rowsAffected, true, err.Error())
		return ImportResult{}, fmt.Errorf("TransferService: sql import: %w", err)
	}

	emitProgress(transferID, rowsAffected, true, "")
	return ImportResult{
		TransferID:    transferID,
		StatementsRun: statementsRun,
		RowsAffected:  rowsAffected,
		ElapsedMs:     time.Since(start).Milliseconds(),
	}, nil
}
