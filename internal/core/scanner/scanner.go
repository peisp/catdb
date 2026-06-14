// Package scanner converts a *sql.Rows stream into the framework-agnostic
// row format dbdriver.ResultSet promises: [][]any rows + []ColumnMeta header.
//
// Design notes (ARCHITECTURE.md §6.1):
//   - We scan into sql.RawBytes once per call to avoid the cost of letting
//     the driver allocate Go values per cell, then we do our own type-switch
//     using sql.ColumnType.DatabaseTypeName(). This gives precise, stable
//     conversions across MySQL types instead of leaving them as []byte.
//   - Row data is []any (NOT map[string]any) — column names ship once via
//     ColumnMeta and rows ride in dense arrays for small IPC payloads.
//   - BIGINT values that fall outside the JS safe-integer range are emitted
//     as strings to avoid precision loss in the front-end.
//   - Bytes go out base64-encoded with a "__bytes__" wrapper so the front-end
//     can render them differently (hex preview, "blob" badge).
package scanner

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"catdb/internal/dbdriver"
)

// maxSafeInteger is the largest integer JavaScript Number can represent
// exactly (2^53 - 1). BIGINT outside ±this range is sent as a string.
const maxSafeInteger int64 = 1<<53 - 1

// ColumnMetasFromTypes builds the column header the front-end consumes. It
// uses dialect.MapType (driver-specific) to project the native type onto our
// shared logical-type enum.
func ColumnMetasFromTypes(types []*sql.ColumnType, d dbdriver.Dialect) []dbdriver.ColumnMeta {
	out := make([]dbdriver.ColumnMeta, len(types))
	for i, t := range types {
		nt := t.DatabaseTypeName()
		nullable, hasNullable := t.Nullable()
		length, _ := t.Length()
		precision, scale, _ := t.DecimalSize()

		cm := dbdriver.ColumnMeta{
			Name:        t.Name(),
			NativeType:  nt,
			LogicalType: d.MapType(nt),
			Length:      length,
			Precision:   precision,
			Scale:       scale,
		}
		// When the driver doesn't know, default to true rather than fabricate
		// false (NOT NULL would surprise the editor).
		cm.Nullable = !hasNullable || nullable
		out[i] = cm
	}
	return out
}

// BytesValue is the marker the front-end uses to detect a binary cell.
// JSON-encodable, stable shape, small.
type BytesValue struct {
	Type   string `json:"__type__"` // always "bytes"
	Base64 string `json:"base64"`
	Length int    `json:"length"`
}

// BigIntString is used for BIGINTs outside the JS safe-int range.
type BigIntString struct {
	Type  string `json:"__type__"` // always "bigint"
	Value string `json:"value"`
}

// ScanBatch reads at most `batch` rows from rows, converting each cell using
// the column type metadata.
//
// Returns (rows, done, err). done=true means rows is exhausted; the final
// slice may still contain rows. Callers should treat any error as terminal
// and close the underlying *sql.Rows.
//
// ctx is checked between rows so a slow upstream (e.g. server side filtering)
// doesn't hold the worker hostage.
func ScanBatch(ctx context.Context, rows *sql.Rows, colTypes []*sql.ColumnType, batch int) ([][]any, bool, error) {
	if batch <= 0 {
		batch = 500
	}
	n := len(colTypes)
	data := make([][]any, 0, batch)
	raw := make([]sql.RawBytes, n)
	holders := make([]any, n)
	for i := range holders {
		holders[i] = &raw[i]
	}

	for i := 0; i < batch; i++ {
		if err := ctx.Err(); err != nil {
			return data, true, err
		}
		if !rows.Next() {
			if err := rows.Err(); err != nil {
				return data, true, err
			}
			return data, true, nil
		}
		if err := rows.Scan(holders...); err != nil {
			return data, true, fmt.Errorf("scanner: scan: %w", err)
		}
		row := make([]any, n)
		for j := range raw {
			row[j] = convert(raw[j], colTypes[j])
			// RawBytes is only valid until the next Scan, so we MUST copy
			// strings and bytes out (convert() already does that).
		}
		data = append(data, row)
	}
	// We hit the batch cap, more rows may follow.
	return data, false, nil
}

// convert maps a RawBytes cell to the IPC-ready value.
//
// We prefer to short-circuit on the native type name; for unknown types we
// fall back to a UTF-8 string so the cell at least round-trips.
func convert(raw sql.RawBytes, t *sql.ColumnType) any {
	if raw == nil {
		return nil
	}
	name := strings.ToUpper(t.DatabaseTypeName())
	switch {
	case name == "TINYINT" || name == "SMALLINT" || name == "MEDIUMINT" || name == "INT" || name == "INTEGER":
		if v, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			return v
		}
		// Fallback: unsigned 32+ bit values parse fine, but watch for "1"
		// boolean-style TINYINT(1) — left as int.
		if v, err := strconv.ParseUint(string(raw), 10, 64); err == nil && v <= math.MaxInt64 {
			return int64(v)
		}
		return string(raw)

	case name == "BIGINT":
		if v, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			if v > maxSafeInteger || v < -maxSafeInteger {
				return BigIntString{Type: "bigint", Value: strconv.FormatInt(v, 10)}
			}
			return v
		}
		if v, err := strconv.ParseUint(string(raw), 10, 64); err == nil {
			return BigIntString{Type: "bigint", Value: strconv.FormatUint(v, 10)}
		}
		return string(raw)

	case name == "FLOAT" || name == "DOUBLE" || name == "REAL":
		if v, err := strconv.ParseFloat(string(raw), 64); err == nil {
			return v
		}
		return string(raw)

	case name == "DECIMAL" || name == "NUMERIC":
		// Preserve precision — DECIMAL is a string in transit. Front-end can
		// render or convert to BigNumber as needed.
		return string(raw)

	case name == "BOOL" || name == "BOOLEAN":
		return string(raw) == "1" || strings.EqualFold(string(raw), "true")

	case name == "BIT":
		// MySQL ships BIT as binary; non-zero → true.
		for _, b := range raw {
			if b != 0 {
				return true
			}
		}
		return false

	case name == "DATE":
		if t, ok := tryParseTime(string(raw), "2006-01-02"); ok {
			return t.Format("2006-01-02")
		}
		return string(raw)

	case name == "TIME":
		return string(raw)

	case name == "DATETIME" || strings.HasPrefix(name, "DATETIME"):
		if t, ok := tryParseTime(string(raw), "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"); ok {
			return t.Format(time.RFC3339Nano)
		}
		return string(raw)

	case name == "TIMESTAMP" || strings.HasPrefix(name, "TIMESTAMP"):
		if t, ok := tryParseTime(string(raw), "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"); ok {
			return t.Format(time.RFC3339Nano)
		}
		return string(raw)

	case name == "YEAR":
		if v, err := strconv.Atoi(string(raw)); err == nil {
			return v
		}
		return string(raw)

	case name == "JSON":
		// Send as string; front-end can JSON.parse if it wants.
		return string(raw)

	case name == "CHAR" || name == "VARCHAR" || name == "TEXT" ||
		name == "TINYTEXT" || name == "MEDIUMTEXT" || name == "LONGTEXT" ||
		name == "ENUM" || name == "SET" || name == "":
		return string(raw)

	case name == "BINARY" || name == "VARBINARY" || name == "BLOB" ||
		name == "TINYBLOB" || name == "MEDIUMBLOB" || name == "LONGBLOB":
		return BytesValue{
			Type:   "bytes",
			Base64: base64.StdEncoding.EncodeToString(raw),
			Length: len(raw),
		}

	default:
		// Unknown type: return string verbatim.
		return string(raw)
	}
}

func tryParseTime(s string, layouts ...string) (time.Time, bool) {
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ErrAlreadyClosed is returned by ResultSet wrappers when Next is called on
// an already-closed stream.
var ErrAlreadyClosed = errors.New("scanner: result set already closed")
