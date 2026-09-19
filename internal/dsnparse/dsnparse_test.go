package dsnparse

import (
	"testing"

	"github.com/xo/dburl"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		scheme  string
		user    string
		pass    string
		hasPass bool
		noUser  bool // User == nil (no "@" in the DSN)
		host    string
		port    string
		path    string
		query   string
		frag    string
	}{
		// plaintext passwords, the whole point of the lenient parser
		{
			name: "password with @ and #", dsn: "postgres://superme:AA@1122#@172.20.208.1:5432/test?sslmode=disable",
			user: "superme", pass: "AA@1122#", hasPass: true, host: "172.20.208.1", port: "5432", path: "/test", query: "sslmode=disable",
		},
		{name: "password with colon", dsn: "postgres://u:p@ss:word@h/db", user: "u", pass: "p@ss:word", hasPass: true, host: "h", path: "/db"},
		{name: "password with slash", dsn: "postgres://u:pa/ss@h/db", user: "u", pass: "pa/ss", hasPass: true, host: "h", path: "/db"},
		{name: "password with question mark", dsn: "postgres://u:p?ss@h/db", user: "u", pass: "p?ss", hasPass: true, host: "h", path: "/db"},
		{
			name: "password with question mark and query", dsn: "postgres://u:p?ss@h/db?sslmode=disable",
			user: "u", pass: "p?ss", hasPass: true, host: "h", path: "/db", query: "sslmode=disable",
		},
		{name: "password with hash", dsn: "postgres://u:p#ss@h/db", user: "u", pass: "p#ss", hasPass: true, host: "h", path: "/db"},
		{name: "password with space", dsn: "postgres://u:p ss@h/db", user: "u", pass: "p ss", hasPass: true, host: "h", path: "/db"},
		{name: "at in password and dbname", dsn: "postgres://u:p@ss@h/d@b", user: "u", pass: "p@ss", hasPass: true, host: "h", path: "/d@b"},

		// quoted passwords
		{name: "double-quoted password", dsn: `postgres://u:"p@ss:w#rd"@h:1/db`, user: "u", pass: "p@ss:w#rd", hasPass: true, host: "h", port: "1", path: "/db"},
		{name: "single-quoted password with space", dsn: "postgres://u:'p@ss w#rd'@h/db", user: "u", pass: "p@ss w#rd", hasPass: true, host: "h", path: "/db"},
		{name: "double quotes inside single-quoted password", dsn: `postgres://u:'say "hi"'@h/db`, user: "u", pass: `say "hi"`, hasPass: true, host: "h", path: "/db"},
		{name: "single quote inside double-quoted password", dsn: `postgres://u:"it's"@h/db`, user: "u", pass: "it's", hasPass: true, host: "h", path: "/db"},
		{name: "quoted password keeps percent literal", dsn: `postgres://u:"p%40ss"@h/db`, user: "u", pass: "p%40ss", hasPass: true, host: "h", path: "/db"},

		// quoted usernames
		{name: "quoted username without colon", dsn: `postgres://"p@ss"@h/db`, user: "p@ss", host: "h", path: "/db"},
		{name: "quoted username with colon", dsn: `postgres://"us:er":p@ss@h/db`, user: "us:er", pass: "p@ss", hasPass: true, host: "h", path: "/db"},

		// percent-encoded forms keep working
		{name: "encoded password", dsn: "postgres://superme:AA%401122%23@h/db", user: "superme", pass: "AA@1122#", hasPass: true, host: "h", path: "/db"},
		{name: "encoded percent", dsn: "postgres://u:p%25ss@h/db", user: "u", pass: "p%ss", hasPass: true, host: "h", path: "/db"},
		{name: "invalid escape kept literal", dsn: "postgres://u:p%zz@h/db", user: "u", pass: "p%zz", hasPass: true, host: "h", path: "/db"},

		// known-good DSNs from the test suite must be unaffected
		{name: "sqlserver password with at", dsn: "sqlserver://sa:Adm1nP@ssw0rd@localhost/", user: "sa", pass: "Adm1nP@ssw0rd", hasPass: true, host: "localhost", path: "/"},
		{name: "mysql standard", dsn: "mysql://root:abc@127.0.0.1:3306/db", user: "root", pass: "abc", hasPass: true, host: "127.0.0.1", port: "3306", path: "/db"},
		{name: "uppercase scheme", dsn: "POSTGRES://u:p@h/db", scheme: "postgres", user: "u", pass: "p", hasPass: true, host: "h", path: "/db"},

		// structural edges
		{name: "no password", dsn: "postgres://u@h/db", user: "u", host: "h", path: "/db"},
		{name: "empty username", dsn: "postgres://:pw@h/db", user: "", pass: "pw", hasPass: true, host: "h", path: "/db"},
		{name: "empty password", dsn: "postgres://u:@h/db", user: "u", pass: "", hasPass: true, host: "h", path: "/db"},
		{name: "at with empty credentials", dsn: "postgres://@h/db", user: "", host: "h", path: "/db"},
		{name: "no credentials host port db", dsn: "postgres://h:5432/db", noUser: true, host: "h", port: "5432", path: "/db"},
		{name: "no credentials host db", dsn: "postgres://h/db", noUser: true, host: "h", path: "/db"},
		{name: "no path", dsn: "postgres://h", noUser: true, host: "h"},
		{name: "empty dbname", dsn: "postgres://h/", noUser: true, host: "h", path: "/"},
		{name: "empty host", dsn: "postgres:///db", noUser: true, path: "/db"},
		{name: "empty everything", dsn: "postgres://", noUser: true},
		{name: "ipv6 with port", dsn: "postgres://u:p@[::1]:5432/db", user: "u", pass: "p", hasPass: true, host: "::1", port: "5432", path: "/db"},
		{name: "ipv6 without port", dsn: "postgres://u@[::1]/db", user: "u", host: "::1", path: "/db"},
		{name: "multi-segment path", dsn: "postgres://h/a/b/c", noUser: true, host: "h", path: "/a/b/c"},
		{name: "at in dbname", dsn: "postgres://h/d@b", noUser: true, host: "h", path: "/d@b"},
		{name: "at in query value", dsn: "postgres://u:p@h/db?user=admin@x", user: "u", pass: "p", hasPass: true, host: "h", path: "/db", query: "user=admin@x"},
		{name: "fragment in dbname", dsn: "postgres://h/db#frag", noUser: true, host: "h", path: "/db", frag: "frag"},
		{name: "unix transport", dsn: "postgres+unix://user@/var/run/postgresql/db", user: "user", host: "", path: "/var/run/postgresql/db"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, err := Parse(tc.dsn)
			if err != nil {
				t.Fatalf("Parse(%q) failed: %v", tc.dsn, err)
			}
			if scheme := u.Scheme; tc.scheme != "" && scheme != tc.scheme {
				t.Errorf("scheme = %q, want %q", scheme, tc.scheme)
			}
			switch {
			case tc.noUser:
				if u.User != nil {
					t.Errorf("User = %v, want nil", u.User)
				}
			default:
				if u.User == nil {
					t.Fatalf("User = nil, want non-nil")
				}
				if got := u.User.Username(); got != tc.user {
					t.Errorf("username = %q, want %q", got, tc.user)
				}
				pass, has := u.User.Password()
				if has != tc.hasPass {
					t.Errorf("has password = %v, want %v", has, tc.hasPass)
				}
				if has && pass != tc.pass {
					t.Errorf("password = %q, want %q", pass, tc.pass)
				}
			}
			if got := u.Hostname(); got != tc.host {
				t.Errorf("host = %q, want %q", got, tc.host)
			}
			if got := u.Port(); got != tc.port {
				t.Errorf("port = %q, want %q", got, tc.port)
			}
			if got := u.Path; got != tc.path {
				t.Errorf("path = %q, want %q", got, tc.path)
			}
			if got := u.RawQuery; got != tc.query {
				t.Errorf("query = %q, want %q", got, tc.query)
			}
			if got := u.Fragment; got != tc.frag {
				t.Errorf("fragment = %q, want %q", got, tc.frag)
			}
		})
	}
}

// TestRebuildPassthrough verifies non-scheme:// DSNs are passed through
// unchanged, and clean scheme:// DSNs round-trip byte-for-byte.
func TestRebuildPassthrough(t *testing.T) {
	passthrough := []string{
		"",
		"./plain.db",
		"/abs/path.db",
		"booktest",
		"sqlite:./x.db",
		"file:test.db?cache=shared",
	}
	for _, dsn := range passthrough {
		if got, ok := rebuild(dsn); ok || got != dsn {
			t.Errorf("rebuild(%q) = (%q, %v), want unchanged passthrough", dsn, got, ok)
		}
	}
	identity := []string{
		"postgres://h/db",
		"postgres://h:5432/db?sslmode=disable",
		"postgres://u:p@h/db",
		"postgres:///db",
		"postgres://",
		"postgres://?x=1",
		"postgres://#f",
		"sqlite3://./testdata/x.db",
		"sqlite3:///path/to/db.sqlite",
	}
	for _, dsn := range identity {
		if got, ok := rebuild(dsn); !ok || got != dsn {
			t.Errorf("rebuild(%q) = (%q, %v), want identity", dsn, got, ok)
		}
	}
}

// TestRebuildEncoded verifies the rebuilt URL for lenient DSNs.
func TestRebuildEncoded(t *testing.T) {
	tests := []struct{ raw, want string }{
		{
			"postgres://superme:AA@1122#@172.20.208.1:5432/test?sslmode=disable",
			"postgres://superme:AA%401122%23@172.20.208.1:5432/test?sslmode=disable",
		},
		{"postgres://u:p@ss@h/db", "postgres://u:p%40ss@h/db"},
		{"postgres://u:p ss@h/db", "postgres://u:p%20ss@h/db"},
		{`postgres://u:"p@ss"@h/db`, "postgres://u:p%40ss@h/db"},
	}
	for _, tc := range tests {
		got, ok := rebuild(tc.raw)
		if !ok {
			t.Errorf("rebuild(%q) not rebuilt", tc.raw)
			continue
		}
		if got != tc.want {
			t.Errorf("rebuild(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// TestParseMatchesDBURL verifies lenient parsing yields identical components
// to dburl.Parse for DSNs net/url can already handle.
func TestParseMatchesDBURL(t *testing.T) {
	tests := []string{
		"postgres://superme:AA%401122%23@172.20.208.1:5432/test?sslmode=disable",
		"postgres://u:p%25ss@h/db",
		"sqlserver://sa:Adm1nP@ssw0rd@localhost/",
		"mysql://root:abc@127.0.0.1:3306/db",
		"POSTGRES://u:p@h/db",
		"postgres://u@h/db",
		"postgres://:pw@h/db",
		"postgres://u:@h/db",
		"postgres://@h/db",
		"postgres://h:5432/db",
		"postgres://h/db",
		"postgres://h",
		"postgres://h/",
		"postgres://u:p@[::1]:5432/db",
		"postgres://u@[::1]/db",
		"postgres://h/a/b/c",
		"postgres://h/d@b",
		"postgres://u:p@h/db?user=admin@x",
		"postgres://h/db#frag",
	}
	for _, dsn := range tests {
		a, err := Parse(dsn)
		if err != nil {
			t.Errorf("Parse(%q) failed: %v", dsn, err)
			continue
		}
		b, err := dburl.Parse(dsn)
		if err != nil {
			t.Errorf("dburl.Parse(%q) failed: %v", dsn, err)
			continue
		}
		if a.Scheme != b.Scheme || a.Host != b.Host || a.Path != b.Path || a.RawQuery != b.RawQuery {
			t.Errorf("Parse(%q) = {%s %s %s %s}, dburl.Parse = {%s %s %s %s}",
				dsn, a.Scheme, a.Host, a.Path, a.RawQuery, b.Scheme, b.Host, b.Path, b.RawQuery)
		}
		au, ap := "", ""
		if a.User != nil {
			au = a.User.Username()
			ap, _ = a.User.Password()
		}
		bu, bp := "", ""
		if b.User != nil {
			bu = b.User.Username()
			bp, _ = b.User.Password()
		}
		if au != bu || ap != bp {
			t.Errorf("Parse(%q) user/pass = (%q, %q), dburl.Parse = (%q, %q)", dsn, au, ap, bu, bp)
		}
	}
}
