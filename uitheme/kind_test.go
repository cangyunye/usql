package uitheme

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestKindOf(t *testing.T) {
	cases := []struct {
		scan any // nil for no scan type
		db   string
		want Kind
	}{
		// database type names
		{nil, "INT4", KindInt},
		{nil, "INTEGER", KindInt},
		{nil, "BIGINT", KindInt},
		{nil, "BIGINT UNSIGNED", KindInt},
		{nil, "SMALLSERIAL", KindInt},
		{nil, "DECIMAL(10,2)", KindFloat},
		{nil, "NUMERIC", KindFloat},
		{nil, "MONEY", KindFloat},
		{nil, "DOUBLE PRECISION", KindFloat},
		{nil, "FLOAT8", KindFloat},
		{nil, "REAL", KindFloat},
		{nil, "TIMESTAMPTZ", KindTime},
		{nil, "DATETIME", KindTime},
		{nil, "DATE", KindTime},
		{nil, "INTERVAL", KindTime},
		{nil, "TIME WITH TIME ZONE", KindTime},
		{nil, "BOOLEAN", KindBool},
		{nil, "BLOB", KindBinary},
		{nil, "VARBINARY(16)", KindBinary},
		{nil, "BYTEA", KindBinary},
		{nil, "IMAGE", KindBinary},
		{nil, "LONG RAW", KindBinary},
		{nil, "VARCHAR(255)", KindString},
		{nil, "CHARACTER VARYING", KindString},
		{nil, "TEXT", KindString},
		{nil, "JSONB", KindString},
		{nil, "UUID", KindString},
		{nil, "UNIQUEIDENTIFIER", KindString},
		{nil, "XML", KindString},
		{nil, "ENUM", KindString},
		{nil, "", KindUnknown},
		{nil, "WEIRD", KindUnknown},
		// scan types
		{time.Time{}, "", KindTime},
		{int64(0), "", KindInt},
		{uint32(0), "", KindInt},
		{float64(0), "", KindFloat},
		{"", "", KindString},
		{true, "", KindBool},
		{[]byte{}, "", KindBinary},
		{sql.NullTime{}, "", KindTime},
		{sql.NullBool{}, "", KindBool},
		{sql.NullInt64{}, "", KindInt},
		{sql.NullFloat64{}, "", KindFloat},
		{sql.NullString{}, "", KindString},
		{sql.RawBytes{}, "", KindBinary},
		{&time.Time{}, "", KindTime}, // pointers are unwrapped
		// the database type wins where drivers scan typed values as strings
		// or bytes: exact decimals, temporals, and booleans must not degrade
		// to the string kind
		{"", "NUMERIC", KindFloat},
		{"", "TIMESTAMP", KindTime},
		{[]byte{}, "DATETIME", KindTime},
		{int64(0), "BOOLEAN", KindBool},
		// unmatched type names fall back to the scan type
		{int64(0), "WEIRD", KindInt},
		{time.Time{}, "WEIRD", KindTime},
	}
	for _, c := range cases {
		var st reflect.Type
		if c.scan != nil {
			st = reflect.TypeOf(c.scan)
		}
		if got := kindOf(st, c.db); got != c.want {
			t.Errorf("kindOf(%v, %q) = %v, want %v", st, c.db, got, c.want)
		}
	}
}
