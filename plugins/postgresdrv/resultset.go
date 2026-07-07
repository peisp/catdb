package postgresdrv

import (
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"catdb/internal/core/scanner"
	"catdb/internal/dbdriver"
)

// maxSafeInteger mirrors the scanner package: int8 values outside the JS
// safe-integer range ride as BigIntString to avoid front-end precision loss.
const maxSafeInteger int64 = 1<<53 - 1

// resultSet adapts pgx.Rows to dbdriver.ResultSet.
//
// The pool runs in simple-protocol mode, so every cell arrives in Postgres
// text format; convertText parses it per type name — the pgx-native analogue
// of core/scanner's RawBytes path, producing the same IPC value shapes
// (int64, float64, string, scanner.BytesValue, scanner.BigIntString).
//
// Construction eagerly steps to the first row: pgx surfaces execution errors
// (including ctx cancellation) on the first Next(), and in simple protocol
// the field descriptions only exist after it — while dbdriver.Querier.Query
// must report errors immediately and Columns() must be ready up front.
type resultSet struct {
	ctx   context.Context
	rows  pgx.Rows
	cols  []dbdriver.ColumnMeta
	types []string // lower-case type name per column

	mu      sync.Mutex
	pending [][]any // the eagerly-read first row, if any
	closed  bool
	doneEOF bool
}

func newResultSet(ctx context.Context, rows pgx.Rows) (*resultSet, error) {
	rs := &resultSet{ctx: ctx, rows: rows}
	if rows.Next() {
		rs.initColumns()
		rs.pending = [][]any{rs.convertRow(rows.RawValues())}
	} else {
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rs.initColumns()
		rs.doneEOF = true
	}
	return rs, nil
}

func (r *resultSet) initColumns() {
	fds := r.rows.FieldDescriptions()
	r.cols = make([]dbdriver.ColumnMeta, len(fds))
	r.types = make([]string, len(fds))
	dia := dialect{}
	for i, fd := range fds {
		name := typeNameForOID(r.rows, fd.DataTypeOID)
		r.types[i] = name
		r.cols[i] = dbdriver.ColumnMeta{
			Name:        fd.Name,
			NativeType:  strings.ToUpper(name),
			LogicalType: dia.MapType(name),
			// Field descriptions carry no nullability; default to true rather
			// than fabricate NOT NULL (same policy as core/scanner).
			Nullable: true,
		}
	}
}

func typeNameForOID(rows pgx.Rows, oid uint32) string {
	if conn := rows.Conn(); conn != nil {
		if t, ok := conn.TypeMap().TypeForOID(oid); ok {
			return t.Name
		}
	}
	return ""
}

func (r *resultSet) Columns() []dbdriver.ColumnMeta { return r.cols }

// Next fetches the next batch. After done=true is returned once, subsequent
// calls return (nil, true, nil) until Close is invoked.
func (r *resultSet) Next(batch int) ([][]any, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, true, scanner.ErrAlreadyClosed
	}
	if batch <= 0 {
		batch = 500
	}
	data := make([][]any, 0, batch)
	if len(r.pending) > 0 {
		data = append(data, r.pending...)
		r.pending = nil
	}
	if r.doneEOF {
		return data, true, nil
	}
	for len(data) < batch {
		if err := r.ctx.Err(); err != nil {
			r.doneEOF = true
			return data, true, err
		}
		if !r.rows.Next() {
			r.doneEOF = true
			return data, true, r.rows.Err()
		}
		data = append(data, r.convertRow(r.rows.RawValues()))
	}
	return data, false, nil
}

// Close releases the underlying pgx.Rows (returning its connection to the
// pool) and marks the stream finished.
func (r *resultSet) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	r.rows.Close()
	return nil
}

func (r *resultSet) convertRow(raw [][]byte) []any {
	row := make([]any, len(raw))
	for i, cell := range raw {
		row[i] = convertText(cell, r.types[i])
	}
	return row
}

// convertText parses one Postgres text-format cell into the IPC-ready value.
// Text is copied out — raw slices are only valid until the next rows.Next().
func convertText(raw []byte, typeName string) any {
	if raw == nil {
		return nil
	}
	s := string(raw)
	switch typeName {
	case "int2", "int4":
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
		return s

	case "int8":
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			if v > maxSafeInteger || v < -maxSafeInteger {
				return scanner.BigIntString{Type: "bigint", Value: s}
			}
			return v
		}
		return s

	case "float4", "float8":
		if v, err := strconv.ParseFloat(s, 64); err == nil {
			return v
		}
		return s

	case "numeric":
		// Preserve precision — NUMERIC rides as a string.
		return s

	case "bool":
		return s == "t" || s == "true"

	case "bytea":
		// Text format is "\x<hex>" (bytea_output=hex, the server default).
		if strings.HasPrefix(s, `\x`) {
			if b, err := hex.DecodeString(s[2:]); err == nil {
				return scanner.BytesValue{
					Type:   "bytes",
					Base64: base64Encode(b),
					Length: len(b),
				}
			}
		}
		return s

	default:
		// date/time/timestamp/timestamptz/json/uuid/text/arrays/enums/… —
		// the Postgres text rendering is already what the grid should show.
		return s
	}
}
