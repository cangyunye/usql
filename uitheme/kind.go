package uitheme

import (
	"database/sql"
	"reflect"
	"strings"
	"time"
)

// Kind is the display class of a result column's values, driving the value
// colors painted into aligned tables (see Theme.Values).
type Kind int

// Value kinds. KindUnknown and KindBinary leave values in the terminal's
// default color.
const (
	KindUnknown Kind = iota
	KindInt
	KindFloat
	KindString
	KindTime
	KindBool
	KindBinary
	numKind
)

// KindOf classifies a result column for value coloring.
func KindOf(ct *sql.ColumnType) Kind {
	if ct == nil {
		return KindUnknown
	}
	return kindOf(ct.ScanType(), ct.DatabaseTypeName())
}

// kindOf classifies a column by its scan type and database type name. The
// database type name is matched first: its unambiguous families (temporal,
// exact-decimal, boolean, binary) must win over the scan type, because
// drivers commonly scan those as plain strings or []byte. The scan type then
// covers drivers that report no useful type name (expressions, NULL-typed
// columns), and the loose string/int families come last.
func kindOf(scanType reflect.Type, dbType string) Kind {
	if k := dbTypeKind(dbType); k != KindUnknown {
		return k
	}
	return scanTypeKind(scanType)
}

// dbTypeKind matches a database type name (as reported by
// sql.ColumnType.DatabaseTypeName, possibly with size or attributes) to a
// Kind. Matching is case-insensitive substring matching, so qualified sizes
// ("DECIMAL(10,2)") and vendor spellings ("BIGINT UNSIGNED", "CHARACTER
// VARYING") match too. Order matters: the temporal family must precede the
// int family (INTERVAL contains INT), and the exact-decimal family must
// precede both.
func dbTypeKind(dbType string) Kind {
	s := strings.ToUpper(dbType)
	switch {
	case containsAny(s, "TIME", "DATE", "INTERVAL"):
		return KindTime
	case containsAny(s, "DECIMAL", "NUMERIC", "MONEY"):
		return KindFloat
	case containsAny(s, "DOUBLE", "FLOAT", "REAL"):
		return KindFloat
	case containsAny(s, "BOOL"):
		return KindBool
	case containsAny(s, "BLOB", "BINARY", "BYTEA", "IMAGE", "RAW"):
		return KindBinary
	case containsAny(s, "INT", "SERIAL"):
		return KindInt
	case containsAny(s, "CHAR", "TEXT", "CLOB", "ENUM", "UUID", "JSON", "XML", "IDENTIFIER", "GUID"):
		return KindString
	}
	return KindUnknown
}

// scanTypeKind classifies a column by the Go type the driver scans values
// into.
func scanTypeKind(scanType reflect.Type) Kind {
	for t := scanType; t != nil && t.Kind() == reflect.Pointer; t = t.Elem() {
		scanType = t.Elem()
	}
	switch scanType {
	case nil:
		return KindUnknown
	case reflect.TypeOf(time.Time{}):
		return KindTime
	case reflect.TypeOf(sql.RawBytes{}):
		return KindBinary
	case reflect.TypeOf(sql.NullTime{}):
		return KindTime
	case reflect.TypeOf(sql.NullBool{}):
		return KindBool
	case reflect.TypeOf(sql.NullInt64{}):
		return KindInt
	case reflect.TypeOf(sql.NullFloat64{}):
		return KindFloat
	case reflect.TypeOf(sql.NullString{}):
		return KindString
	}
	switch scanType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return KindInt
	case reflect.Float32, reflect.Float64:
		return KindFloat
	case reflect.String:
		return KindString
	case reflect.Bool:
		return KindBool
	case reflect.Slice:
		if scanType.Elem().Kind() == reflect.Uint8 {
			return KindBinary
		}
	}
	return KindUnknown
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
