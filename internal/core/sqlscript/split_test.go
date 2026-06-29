package sqlscript

import (
	"reflect"
	"strings"
	"testing"
)

var splitCases = []struct {
	name   string
	script string
	want   []string
}{
		{
			name:   "empty",
			script: "   \n\t ",
			want:   nil,
		},
		{
			name:   "single no trailing delimiter",
			script: "SELECT 1",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "single trailing delimiter",
			script: "SELECT 1;",
			want:   []string{"SELECT 1"},
		},
		{
			name:   "two statements",
			script: "SELECT 1; SELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "semicolon inside single-quoted string",
			script: "SELECT ';' AS a; SELECT 2",
			want:   []string{"SELECT ';' AS a", "SELECT 2"},
		},
		{
			name:   "semicolon inside double-quoted and backtick",
			script: "SELECT \"a;b\", `c;d`; SELECT 2",
			want:   []string{"SELECT \"a;b\", `c;d`", "SELECT 2"},
		},
		{
			name:   "escaped quote in string",
			script: `INSERT INTO t VALUES ('a\'; b'); SELECT 1`,
			want:   []string{`INSERT INTO t VALUES ('a\'; b')`, "SELECT 1"},
		},
		{
			name:   "doubled quote in string",
			script: "SELECT 'it''s; ok'; SELECT 2",
			want:   []string{"SELECT 'it''s; ok'", "SELECT 2"},
		},
		{
			name:   "line comment with semicolon",
			script: "SELECT 1; -- a; b\nSELECT 2",
			// A leading comment stays attached to the next statement; the
			// server skips it. Only a pure-comment span produces no statement.
			want: []string{"SELECT 1", "-- a; b\nSELECT 2"},
		},
		{
			name:   "hash comment with semicolon",
			script: "SELECT 1 # c; d\n; SELECT 2",
			want:   []string{"SELECT 1 # c; d", "SELECT 2"},
		},
		{
			name:   "block comment with semicolon",
			script: "SELECT /* x; y */ 1; SELECT 2",
			want:   []string{"SELECT /* x; y */ 1", "SELECT 2"},
		},
		{
			name:   "trailing comment only is dropped",
			script: "SELECT 1; -- trailing",
			want:   []string{"SELECT 1"},
		},
		{
			name: "delimiter directive with function body",
			script: `DELIMITER //
CREATE FUNCTION f(x DECIMAL(10,2))
RETURNS DECIMAL(10,2)
DETERMINISTIC
BEGIN
    -- comment
    RETURN x * 13;
END //
DELIMITER ;`,
			want: []string{`CREATE FUNCTION f(x DECIMAL(10,2))
RETURNS DECIMAL(10,2)
DETERMINISTIC
BEGIN
    -- comment
    RETURN x * 13;
END`},
		},
		{
			name: "delimiter then normal statements after reset",
			script: `DELIMITER //
CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET @a = 1; END //
DELIMITER ;
SELECT 1;`,
			want: []string{
				"CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN SET @a = 1; END",
				"SELECT 1",
			},
		},
		{
			name:   "delimiter is not matched inside string",
			script: "SELECT 'delimiter //' AS x",
			want:   []string{"SELECT 'delimiter //' AS x"},
		},
		{
			name:   "identifier starting with delimiter word is not a directive",
			script: "SELECT delimiter_col FROM t",
			want:   []string{"SELECT delimiter_col FROM t"},
		},
	{
		name:   "crlf line endings",
		script: "DELIMITER //\r\nSELECT 1//\r\n",
		want:   []string{"SELECT 1"},
	},
}

func TestSplit(t *testing.T) {
	for _, tt := range splitCases {
		t.Run(tt.name, func(t *testing.T) {
			got := Split(tt.script)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Split() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}

// TestSplitStreamParity feeds every Split case through the streaming reader and
// asserts identical output — the two share one state machine, this guards it.
func TestSplitStreamParity(t *testing.T) {
	for _, tt := range splitCases {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			err := SplitStream(strings.NewReader(tt.script), func(s string) error {
				got = append(got, s)
				return nil
			})
			if err != nil {
				t.Fatalf("SplitStream() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitStream() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}

// TestSplitStreamChunkBoundaries forces statements to span physical lines so the
// cross-line resume of strings and block comments is exercised.
func TestSplitStreamChunkBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "string spanning lines",
			script: "INSERT INTO t VALUES ('line one;\nline two')",
			want:   []string{"INSERT INTO t VALUES ('line one;\nline two')"},
		},
		{
			name:   "block comment spanning lines",
			script: "SELECT 1 /* a;\nb; c */; SELECT 2",
			want:   []string{"SELECT 1 /* a;\nb; c */", "SELECT 2"},
		},
		{
			name:   "backtick identifier spanning lines",
			script: "SELECT `weird;\ncol` FROM t; SELECT 2",
			want:   []string{"SELECT `weird;\ncol` FROM t", "SELECT 2"},
		},
		{
			name:   "escaped newline inside string",
			script: "SELECT 'a\\\nb;c'; SELECT 2",
			want:   []string{"SELECT 'a\\\nb;c'", "SELECT 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			err := SplitStream(strings.NewReader(tt.script), func(s string) error {
				got = append(got, s)
				return nil
			})
			if err != nil {
				t.Fatalf("SplitStream() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitStream() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}
