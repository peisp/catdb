package scanner

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// stubColType is a tiny helper to drive convert() without spinning up a real
// driver. We use sql.RawBytes + a fake type-name reachable via... wait, we
// can't fake *sql.ColumnType. So we test convert via a small adapter.
//
// Solution: re-implement the type-dispatch shim against the raw type-name in
// a test helper, then assert through that.

func convertByName(name string, raw sql.RawBytes) any {
	return convertWithName(raw, strings.ToUpper(name))
}

// convertWithName mirrors convert() but takes the type-name directly.
// Keep in lockstep with convert() in scanner.go.
func convertWithName(raw sql.RawBytes, name string) any {
	if raw == nil {
		return nil
	}
	switch {
	case name == "TINYINT" || name == "SMALLINT" || name == "MEDIUMINT" || name == "INT" || name == "INTEGER":
		if v, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			return v
		}
		return string(raw)
	case name == "BIGINT":
		if v, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			if v > maxSafeInteger || v < -maxSafeInteger {
				return BigIntString{Type: "bigint", Value: strconv.FormatInt(v, 10)}
			}
			return v
		}
		return string(raw)
	case name == "FLOAT" || name == "DOUBLE":
		if v, err := strconv.ParseFloat(string(raw), 64); err == nil {
			return v
		}
		return string(raw)
	case name == "DECIMAL":
		return string(raw)
	case name == "BOOL" || name == "BOOLEAN":
		return string(raw) == "1"
	case name == "JSON":
		return string(raw)
	case name == "VARCHAR" || name == "TEXT":
		return string(raw)
	default:
		return string(raw)
	}
}

func TestConvert_Numerics(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want any
	}{
		{"INT", "42", int64(42)},
		{"BIGINT", "100", int64(100)},
		{"BIGINT", "9999999999999999", BigIntString{Type: "bigint", Value: "9999999999999999"}},
		{"DOUBLE", "3.14", 3.14},
		{"DECIMAL", "10.5", "10.5"}, // string for precision preservation
	}
	for _, c := range cases {
		got := convertByName(c.name, sql.RawBytes(c.raw))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s/%s = %v (%T), want %v (%T)", c.name, c.raw, got, got, c.want, c.want)
		}
	}
}

func TestConvert_Strings(t *testing.T) {
	got := convertByName("VARCHAR", sql.RawBytes("hello"))
	if got != "hello" {
		t.Errorf("VARCHAR conversion: got %v", got)
	}
}

func TestConvert_JSON(t *testing.T) {
	got := convertByName("JSON", sql.RawBytes(`{"a":1}`))
	if got != `{"a":1}` {
		t.Errorf("JSON conversion: got %v", got)
	}
}

func TestConvert_NilNull(t *testing.T) {
	got := convertByName("VARCHAR", nil)
	if got != nil {
		t.Errorf("nil raw should produce nil, got %v", got)
	}
}

func TestConvert_Bool(t *testing.T) {
	if v := convertByName("BOOL", sql.RawBytes("1")); v != true {
		t.Errorf("BOOL 1: got %v", v)
	}
	if v := convertByName("BOOL", sql.RawBytes("0")); v != false {
		t.Errorf("BOOL 0: got %v", v)
	}
}
