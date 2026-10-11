package drivers

import "testing"

// TestDriverQuoteIdent locks the fold-direction rules: Oracle family folds
// unquoted identifiers to upper, PostgreSQL family (openGauss) to lower —
// catalog names stored under any other case must complete quoted.
func TestDriverQuoteIdent(t *testing.T) {
	for _, drv := range []string{"oracle", "godror", "oboracle"} {
		q := driverQuoteIdent[drv]
		if q == nil {
			t.Fatalf("%s: no quote hook", drv)
		}
		if got := q("dept"); got != `"dept"` {
			t.Errorf("%s: quote(dept) = %q, want %q", drv, got, `"dept"`)
		}
		if got := q("MIG_MYSQL"); got != "MIG_MYSQL" {
			t.Errorf("%s: quote(MIG_MYSQL) = %q, want unquoted", drv, got)
		}
	}
	q := driverQuoteIdent["opengauss"]
	if q == nil {
		t.Fatal("opengauss: no quote hook")
	}
	if got := q("AUDIT_SYNC"); got != `"AUDIT_SYNC"` {
		t.Errorf("opengauss: quote(AUDIT_SYNC) = %q, want %q", got, `"AUDIT_SYNC"`)
	}
	if got := q("dept"); got != "dept" {
		t.Errorf("opengauss: quote(dept) = %q, want unquoted", got)
	}
	if _, ok := driverQuoteIdent["sqlite3"]; ok {
		t.Error("sqlite3 should have no identifier quoting")
	}
}
