package dbdriver

import (
	"reflect"
	"testing"
)

func TestParseJDBCURL(t *testing.T) {
	def := ExternalURLParts{Host: "127.0.0.1", Port: "3306", Database: "def"}
	cases := []struct {
		name           string
		url            string
		host, port, db string
		props          map[string]string
		wantErr        bool
	}{
		{name: "defaults applied", url: "jdbc:x://", host: "127.0.0.1", port: "3306", db: "def"},
		{name: "host only", url: "jdbc:x://h1", host: "h1", port: "3306", db: "def"},
		{name: "host and port", url: "jdbc:x://h1:1234", host: "h1", port: "1234", db: "def"},
		{name: "database", url: "jdbc:x://h1/mydb", host: "h1", port: "3306", db: "mydb"},
		{name: "empty path keeps default", url: "jdbc:x://h1/", host: "h1", port: "3306", db: "def"},
		{name: "empty port keeps default", url: "jdbc:x://h1:", host: "h1", port: "3306", db: "def"},
		{name: "userinfo dropped", url: "jdbc:x://u:p@h1/mydb", host: "h1", port: "3306", db: "mydb"},
		{name: "userinfo with @ in password", url: "jdbc:x://u:p@w@h1/mydb", host: "h1", port: "3306", db: "mydb"},
		{name: "ipv6 with port", url: "jdbc:x://[::1]:9/mydb", host: "::1", port: "9", db: "mydb"},
		{name: "ipv6 without port", url: "jdbc:x://[::1]/mydb", host: "::1", port: "3306", db: "mydb"},
		{name: "failover list", url: "jdbc:x://h1:1,h2:2/mydb", host: "h1", port: "1", db: "mydb"},
		{name: "surrounding space", url: "  jdbc:x://h1/mydb  ", host: "h1", port: "3306", db: "mydb"},
		{
			name: "query", url: "jdbc:x://h1/mydb?a=1&b=two&flag&c=a%20b&d=a+b",
			host: "h1", port: "3306", db: "mydb",
			props: map[string]string{"a": "1", "b": "two", "flag": "", "c": "a b", "d": "a b"},
		},
		{
			name: "query without path", url: "jdbc:x://h1?a=1",
			host: "h1", port: "3306", db: "def",
			props: map[string]string{"a": "1"},
		},
		{name: "wrong prefix", url: "jdbc:y://h1", wantErr: true},
		{name: "no prefix", url: "h1:3306", wantErr: true},
		{name: "truncated prefix", url: "jdbc:x:/", wantErr: true},
		{name: "bad port", url: "jdbc:x://h1:12a/mydb", wantErr: true},
		{name: "unclosed ipv6", url: "jdbc:x://[::1/mydb", wantErr: true},
		{name: "junk after ipv6", url: "jdbc:x://[::1]x/mydb", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseJDBCURL(c.url, "jdbc:x://", def)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseJDBCURL: %v", err)
			}
			want := ExternalURLParts{Host: c.host, Port: c.port, Database: c.db, Props: c.props}
			if want.Props == nil {
				want.Props = map[string]string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v want %+v", got, want)
			}
		})
	}
}
