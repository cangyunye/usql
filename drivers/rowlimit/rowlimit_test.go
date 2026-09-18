package rowlimit

import (
	"testing"
)

func TestRowLimit(t *testing.T) {
	cases := []struct {
		name    string
		driver  string
		n       int
		sql     string
		want    string
		changed bool
	}{
		// LIMIT family (default strategy)
		{"postgres unfiltered", "postgres", 100, "SELECT * FROM film", "SELECT * FROM film\nLIMIT 100", true},
		{"mysql with order by", "mysql", 5, "SELECT id FROM t ORDER BY id DESC", "SELECT id FROM t ORDER BY id DESC\nLIMIT 5", true},
		{"sqlite trailing semicolon", "sqlite3", 10, "SELECT * FROM t;", "SELECT * FROM t\nLIMIT 10", true},
		{"opengauss cte", "opengauss", 50, "WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x\nLIMIT 50", true},
		{"clickhouse table shortcut", "clickhouse", 3, "TABLE t", "TABLE t\nLIMIT 3", true},
		{"ob mysql mode", "mysql", 100, "select * from t", "select * from t\nLIMIT 100", true},
		// FETCH FIRST family
		{"oracle", "oracle", 100, "SELECT * FROM film ORDER BY id", "SELECT * FROM film ORDER BY id\nFETCH FIRST 100 ROWS ONLY", true},
		{"godror lower case", "godror", 1, "select * from dual", "select * from dual\nFETCH FIRST 1 ROWS ONLY", true},
		{"ob oracle mode", "oboracle", 10, "SELECT id FROM t", "SELECT id FROM t\nFETCH FIRST 10 ROWS ONLY", true},
		// TOP family
		{"sqlserver", "sqlserver", 100, "SELECT * FROM film", "SELECT TOP (100) * FROM film", true},
		{"sqlserver distinct", "sqlserver", 25, "SELECT DISTINCT a, b FROM t ORDER BY a", "SELECT DISTINCT TOP (25) a, b FROM t ORDER BY a", true},
		{"sqlserver all", "sqlserver", 7, "SELECT ALL * FROM t", "SELECT ALL TOP (7) * FROM t", true},
		// never rewritten
		{"where filter", "postgres", 100, "SELECT * FROM film WHERE id = 1", "SELECT * FROM film WHERE id = 1", false},
		{"literal where is not a filter", "postgres", 100, "SELECT 'where' FROM t", "SELECT 'where' FROM t\nLIMIT 100", true},
		{"existing limit", "postgres", 100, "SELECT * FROM t LIMIT 5", "SELECT * FROM t LIMIT 5", false},
		{"existing fetch", "oracle", 100, "SELECT * FROM t FETCH FIRST 5 ROWS ONLY", "SELECT * FROM t FETCH FIRST 5 ROWS ONLY", false},
		{"existing offset", "mysql", 100, "SELECT * FROM t ORDER BY id OFFSET 5", "SELECT * FROM t ORDER BY id OFFSET 5", false},
		{"quoted identifier top", "postgres", 100, `SELECT "top" FROM t`, "SELECT \"top\" FROM t\nLIMIT 100", true},
		{"union set op", "postgres", 100, "SELECT 1 UNION SELECT 2", "SELECT 1 UNION SELECT 2", false},
		{"oracle minus", "oracle", 100, "SELECT 1 FROM dual MINUS SELECT 2 FROM dual", "SELECT 1 FROM dual MINUS SELECT 2 FROM dual", false},
		{"for update", "postgres", 100, "SELECT * FROM t FOR UPDATE", "SELECT * FROM t FOR UPDATE", false},
		{"rownum present", "oracle", 100, "SELECT * FROM t WHERE ROWNUM <= 5", "SELECT * FROM t WHERE ROWNUM <= 5", false},
		{"trailing line comment keeps clause visible", "postgres", 100, "SELECT * FROM t -- WHERE\n", "SELECT * FROM t -- WHERE\nLIMIT 100", true},
		{"insert not a query head", "postgres", 100, "INSERT INTO t VALUES (1)", "INSERT INTO t VALUES (1)", false},
		{"sqlserver with cte skipped", "sqlserver", 100, "WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x", false},
		{"none strategy firebird", "firebird", 100, "SELECT * FROM t", "SELECT * FROM t", false},
		{"none strategy odbc", "odbc", 100, "SELECT * FROM t", "SELECT * FROM t", false},
		{"zero limit disables", "postgres", 0, "SELECT * FROM t", "SELECT * FROM t", false},
		{"negative limit disables", "mysql", -5, "SELECT * FROM t", "SELECT * FROM t", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := Apply(c.driver, c.sql, c.n)
			if changed != c.changed {
				t.Fatalf("Apply(%q, %q, %d) changed = %v, want %v", c.driver, c.sql, c.n, changed, c.changed)
			}
			if got != c.want {
				t.Errorf("Apply(%q, %q, %d) = %q, want %q", c.driver, c.sql, c.n, got, c.want)
			}
		})
	}
}

func TestPage(t *testing.T) {
	cases := []struct {
		name          string
		driver        string
		limit, offset int
		sql           string
		want          string
		changed       bool
	}{
		// LIMIT family
		{"postgres next page", "postgres", 100, 100, "SELECT * FROM film", "SELECT * FROM film\nLIMIT 100 OFFSET 100", true},
		{"mysql first page", "mysql", 100, 0, "SELECT * FROM t", "SELECT * FROM t\nLIMIT 100 OFFSET 0", true},
		{"sqlite trailing semicolon", "sqlite3", 50, 50, "SELECT * FROM t;", "SELECT * FROM t\nLIMIT 50 OFFSET 50", true},
		// FETCH FIRST family
		{"oracle offset fetch", "oracle", 100, 200, "SELECT * FROM film", "SELECT * FROM film\nOFFSET 200 ROWS FETCH FIRST 100 ROWS ONLY", true},
		{"oboracle offset fetch", "oboracle", 100, 100, "SELECT * FROM t", "SELECT * FROM t\nOFFSET 100 ROWS FETCH FIRST 100 ROWS ONLY", true},
		// TOP family
		{"sqlserver wrapped offset fetch", "sqlserver", 100, 100, "SELECT * FROM film", "SELECT * FROM (SELECT * FROM film) AS _usql_page ORDER BY (SELECT NULL) OFFSET 100 ROWS FETCH NEXT 100 ROWS ONLY", true},
		{"sqlserver wraps cte fine", "sqlserver", 100, 100, "WITH x AS (SELECT 1) SELECT * FROM x", "SELECT * FROM (WITH x AS (SELECT 1) SELECT * FROM x) AS _usql_page ORDER BY (SELECT NULL) OFFSET 100 ROWS FETCH NEXT 100 ROWS ONLY", true},
		{"sapase top start at", "sapase", 100, 200, "SELECT * FROM t", "SELECT TOP (100) START AT 201 * FROM t", true},
		{"sapase distinct start at", "sapase", 10, 10, "SELECT DISTINCT a FROM t", "SELECT DISTINCT TOP (10) START AT 11 a FROM t", true},
		{"sapase cte skipped", "sapase", 100, 100, "WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x", false},
		// never rewritten
		{"where filter", "postgres", 100, 100, "SELECT * FROM t WHERE id = 1", "SELECT * FROM t WHERE id = 1", false},
		{"existing offset", "mysql", 100, 100, "SELECT * FROM t OFFSET 5", "SELECT * FROM t OFFSET 5", false},
		{"none strategy firebird", "firebird", 100, 100, "SELECT * FROM t", "SELECT * FROM t", false},
		{"none strategy odbc", "odbc", 100, 100, "SELECT * FROM t", "SELECT * FROM t", false},
		{"zero limit", "postgres", 0, 100, "SELECT * FROM t", "SELECT * FROM t", false},
		{"negative offset", "postgres", 100, -1, "SELECT * FROM t", "SELECT * FROM t", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := Page(c.driver, c.sql, c.limit, c.offset)
			if changed != c.changed {
				t.Fatalf("Page(%q, %q, %d, %d) changed = %v, want %v", c.driver, c.sql, c.limit, c.offset, changed, c.changed)
			}
			if got != c.want {
				t.Errorf("Page(%q, %q, %d, %d) = %q, want %q", c.driver, c.sql, c.limit, c.offset, got, c.want)
			}
		})
	}
}
