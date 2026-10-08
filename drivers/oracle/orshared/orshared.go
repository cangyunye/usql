// Package orshared contains shared a shared driver implementation for the
// Oracle Database. Used by Oracle and Godror drivers.
package orshared

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/env"
	"github.com/xo/usql/rline"
)

// oracleStartCommands extends the common statement starters with Oracle's
// own verbs, MERGE above all.
var oracleStartCommands = append(completer.CommonSqlStartCommands,
	"MERGE",
	"AUDIT",
	"NOAUDIT",
	"RENAME",
	"FLASHBACK",
	"PURGE",
)

// oracleExtraCommands are Oracle keywords the common mid-statement fallback
// lacks: row pseudo-columns, hierarchical queries and set difference.
var oracleExtraCommands = []string{
	"ROWNUM",
	"MINUS",
	"CONNECT BY",
	"START WITH",
	"PRIOR",
}

// oracleBuiltinFunctions are Oracle's everyday expression functions the
// dialect-neutral core list lacks. Static, so they serve at zero query cost.
var oracleBuiltinFunctions = []completer.BuiltinFunc{
	{Name: "NVL", Args: "expr, value"},
	{Name: "NVL2", Args: "expr, v1, v2"},
	{Name: "DECODE", Args: "expr, search, result, .."},
	{Name: "TO_CHAR", Args: "expr, fmt"},
	{Name: "TO_DATE", Args: "str, fmt"},
	{Name: "TO_NUMBER", Args: "str, fmt"},
	{Name: "TO_TIMESTAMP", Args: "str, fmt"},
	{Name: "SYSDATE", Bare: true},
	{Name: "SYSTIMESTAMP", Bare: true},
	{Name: "SUBSTR", Args: "str, pos, len"},
	{Name: "INSTR", Args: "str, sub"},
	{Name: "LPAD", Args: "str, n, pad"},
	{Name: "RPAD", Args: "str, n, pad"},
	{Name: "LTRIM", Args: "str, set"},
	{Name: "RTRIM", Args: "str, set"},
	{Name: "TRUNC", Args: "n, digits"},
	{Name: "ADD_MONTHS", Args: "date, n"},
	{Name: "MONTHS_BETWEEN", Args: "d1, d2"},
	{Name: "LAST_DAY", Args: "date"},
	{Name: "LISTAGG", Args: "expr, delim"},
	{Name: "USER", Bare: true},
}

// Register registers an oracle driver. newReader supplies the metadata reader
// constructor (e.g. orameta.NewReader for the ":N" placeholder style used by
// go-ora, or orameta.NewReaderQ for the "?" style used over the MySQL wire).
func Register(name string, err func(error) (string, string), isPasswordErr func(error) bool, newReader func(db drivers.DB, opts ...metadata.ReaderOption) metadata.Reader) {
	endRE := regexp.MustCompile(`;?\s*$`)
	endAnchorRE := regexp.MustCompile(`(?i)\send\s*;\s*$`)
	drivers.Register(name, drivers.Driver{
		AllowMultilineComments: true,
		LowerColumnNames:       true,
		ForceParams: func(u *dburl.URL) {
			// if the service name is not specified, use the environment
			// variable if present
			if strings.TrimPrefix(u.Path, "/") == "" {
				if n, ok := env.Getenv("ORACLE_SID", "ORASID"); ok && n != "" {
					u.Path = "/" + n
					if u.Host == "" {
						u.Host = "localhost"
					}
				}
			}
		},
		Version: func(ctx context.Context, db drivers.DB) (string, error) {
			var ver string
			if err := db.QueryRowContext(ctx, `SELECT banner FROM v$version WHERE ROWNUM = 1`).Scan(&ver); err != nil {
				return "", err
			}
			return ver, nil
		},
		User: func(ctx context.Context, db drivers.DB) (string, error) {
			var user string
			if err := db.QueryRowContext(ctx, `SELECT user FROM dual`).Scan(&user); err != nil {
				return "", err
			}
			return user, nil
		},
		ChangePassword: func(db drivers.DB, user, newpw, _ string) error {
			_, err := db.Exec(`ALTER USER ` + user + ` IDENTIFIED BY ` + newpw)
			return err
		},
		Err:           err,
		IsPasswordErr: isPasswordErr,
		Process: func(_ *dburl.URL, prefix string, sqlstr string) (string, string, bool, error) {
			if !endAnchorRE.MatchString(sqlstr) {
				// trim last ; but only when not END;
				sqlstr = endRE.ReplaceAllString(sqlstr, "")
			}
			typ, q := drivers.QueryExecType(prefix, sqlstr)
			return typ, sqlstr, q, nil
		},
		NewMetadataReader: newReader,
		NewCompleter: func(db drivers.DB, opts ...completer.Option) rline.Completer {
			reader := newReader(db,
				// completion is interactive but serves cold catalogs that can
				// be slow (OceanBase Oracle tenants) — the same budget as the
				// MySQL family, and generous enough to not truncate snapshots
				metadata.WithTimeout(20*time.Second),
				metadata.WithLimit(100000),
			)
			opts = append([]completer.Option{
				completer.WithReader(reader),
				completer.WithDB(db),
				// Oracle namespaces its objects by user; the menu badge says so
				completer.WithSchemaKind("user"),
				// Oracle's own statement starters and mid-statement keywords
				completer.WithSQLStartCommands(oracleStartCommands),
				completer.WithExtraSQLCommands(oracleExtraCommands...),
				// Oracle's everyday expression functions
				completer.WithBuiltinFunctions(oracleBuiltinFunctions...),
			}, opts...)
			return completer.NewDefaultCompleter(opts...)
		},
		NewMetadataWriter: func(db drivers.DB, w io.Writer, opts ...metadata.ReaderOption) metadata.Writer {
			return metadata.NewDefaultWriter(newReader(db, opts...))(db, w)
		},
		Copy: drivers.CopyWithInsert(func(n int) string {
			return fmt.Sprintf(":%d", n)
		}),
	})
}
