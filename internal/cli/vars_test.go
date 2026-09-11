package cli

import (
	"reflect"
	"testing"
)

func TestParseProperties(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{name: "empty", in: "", want: map[string]string{}},
		{
			name: "separators",
			in:   "a=1\nb:2\nc 3\nd = 4\ne\t:\t5\n",
			want: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"},
		},
		{
			name: "comments and blank lines",
			in:   "# a comment\n\n  ! another\nkey=value\n  # not a key\n",
			want: map[string]string{"key": "value"},
		},
		{
			name: "leading whitespace stripped, trailing value whitespace kept",
			in:   "   key   =   value  \n",
			want: map[string]string{"key": "value  "},
		},
		{
			name: "line continuation drops the continuation indent",
			in:   "url=jdbc:mysql://\\\n    host:3306/db\n",
			want: map[string]string{"url": "jdbc:mysql://host:3306/db"},
		},
		{
			name: "even backslashes are not a continuation",
			in:   "path=C:\\\\share\\\\\nnext=1\n",
			want: map[string]string{"path": `C:\share\`, "next": "1"},
		},
		{
			name: "escapes in values",
			in:   "tabbed=a\\tb\nnewline=a\\nb\ncr=a\\rb\nbackslash=a\\\\b\nquote=a\\\"b\nunicode=caf\\u00e9\n",
			want: map[string]string{
				"tabbed": "a\tb", "newline": "a\nb", "cr": "a\rb",
				"backslash": `a\b`, "quote": `a"b`, "unicode": "café",
			},
		},
		{
			name: "escaped separators inside keys",
			in:   "a\\=b=1\nc\\:d=2\ne\\ f=3\n",
			want: map[string]string{"a=b": "1", "c:d": "2", "e f": "3"},
		},
		{
			name: "key with no value",
			in:   "lonely\nalso=\n",
			want: map[string]string{"lonely": "", "also": ""},
		},
		{
			name: "value keeps later separators",
			in:   "url=jdbc:mysql://h:3306/db?a=b\n",
			want: map[string]string{"url": "jdbc:mysql://h:3306/db?a=b"},
		},
		{
			name: "windows line endings",
			in:   "a=1\r\nb=2\r\n",
			want: map[string]string{"a": "1", "b": "2"},
		},
		{
			name: "unknown escape drops the backslash",
			in:   `path=C:\Temp` + "\n",
			want: map[string]string{"path": "C:Temp"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseProperties(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %#v want %#v", got, c.want)
			}
		})
	}
}

func TestExpandVars(t *testing.T) {
	resolve := mapResolver(map[string]string{"a": "A", "empty": "", "nested": "${a}"})
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"${a}", "A"},
		{"x${a}y", "xAy"},
		{"${a}${a}", "AA"},
		{"${missing}", "${missing}"},
		{"${a}-${missing}-${a}", "A-${missing}-A"},
		{"${empty}", ""},
		// One pass only: an expansion is not re-scanned, same as DBeaver.
		{"${nested}", "${a}"},
		// An unterminated reference is left alone.
		{"${a", "${a"},
		{"$a{}", "$a{}"},
	}
	for _, c := range cases {
		if got := expandVars(c.in, resolve); got != c.want {
			t.Errorf("expandVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := expandVars("${a}", nil); got != "${a}" {
		t.Errorf("a nil resolver must be a no-op, got %q", got)
	}
}

func TestNewBuiltinResolver(t *testing.T) {
	r := NewBuiltinResolver(Environment{
		Home:       "/home/me",
		Workspace:  "/home/me/.catdb",
		AppVersion: "2.0.0",
		AppPath:    "/opt/catdb",
		LocalIP:    "10.0.0.5",
	})
	want := map[string]string{
		"home":                "/home/me",
		"workspace":           "/home/me/.catdb",
		"application.name":    "catdb",
		"application.version": "2.0.0",
		"application.path":    "/opt/catdb",
		"dbeaver_home":        "/opt/catdb",
		"local.ip":            "10.0.0.5",
	}
	for name, w := range want {
		got, ok := r.Resolve(name)
		if !ok || got != w {
			t.Errorf("%s: got %q ok=%v, want %q", name, got, ok, w)
		}
	}
	if _, ok := r.Resolve("nope"); ok {
		t.Error("an unknown built-in must not resolve")
	}
	// A built-in with no value falls through to the next layer instead of
	// expanding to an empty string.
	bare := NewBuiltinResolver(Environment{})
	if _, ok := bare.Resolve("workspace"); ok {
		t.Error("an uninjected workspace must not resolve")
	}
}
