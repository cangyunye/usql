package charset

import (
	"database/sql"

	"github.com/xo/tblfmt"
	"golang.org/x/text/encoding"
)

// NewResultSet wraps a tblfmt.ResultSet, decoding column names and scanned
// values from enc to UTF-8. When enc is nil the result set is returned
// unwrapped.
//
// The wrapper implements ColumnTypes when the wrapped result set does, so
// tblfmt's interface assertion (used by drivers that scan by column type)
// keeps working.
func NewResultSet(rs tblfmt.ResultSet, enc encoding.Encoding) tblfmt.ResultSet {
	if enc == nil {
		return rs
	}
	return &resultSet{rs: rs, enc: enc}
}

type resultSet struct {
	rs  tblfmt.ResultSet
	enc encoding.Encoding
}

// Columns returns the column names decoded to UTF-8.
func (r *resultSet) Columns() ([]string, error) {
	cols, err := r.rs.Columns()
	if err != nil {
		return nil, err
	}
	for i := range cols {
		cols[i] = ToUTF8(cols[i], r.enc)
	}
	return cols, nil
}

// Scan passes dst to the wrapped result set, then decodes values in place.
// All pointer shapes tblfmt passes are handled: *any (holding string, []byte,
// or driver types), *string, *[]byte, *sql.RawBytes, and *sql.NullString.
func (r *resultSet) Scan(dst ...any) error {
	if err := r.rs.Scan(dst...); err != nil {
		return err
	}
	for _, d := range dst {
		switch x := d.(type) {
		case *any:
			switch v := (*x).(type) {
			case []byte:
				*x = []byte(ToUTF8(string(v), r.enc))
			case string:
				*x = ToUTF8(v, r.enc)
			}
		case *string:
			*x = ToUTF8(*x, r.enc)
		case *[]byte:
			*x = []byte(ToUTF8(string(*x), r.enc))
		case *sql.RawBytes:
			*x = []byte(ToUTF8(string(*x), r.enc))
		case *sql.NullString:
			x.String = ToUTF8(x.String, r.enc)
		}
	}
	return nil
}

// ColumnTypes passes through to the wrapped result set when it provides
// column types, mimicking tblfmt's ErrResultSetHasNoColumnTypes otherwise.
func (r *resultSet) ColumnTypes() ([]*sql.ColumnType, error) {
	if ct, ok := r.rs.(interface {
		ColumnTypes() ([]*sql.ColumnType, error)
	}); ok {
		return ct.ColumnTypes()
	}
	return nil, tblfmt.ErrResultSetHasNoColumnTypes
}

// Next satisfies the tblfmt.ResultSet interface.
func (r *resultSet) Next() bool {
	return r.rs.Next()
}

// NextResultSet satisfies the tblfmt.ResultSet interface.
func (r *resultSet) NextResultSet() bool {
	return r.rs.NextResultSet()
}

// Close satisfies the tblfmt.ResultSet interface.
func (r *resultSet) Close() error {
	return r.rs.Close()
}

// Err satisfies the tblfmt.ResultSet interface.
func (r *resultSet) Err() error {
	return r.rs.Err()
}
