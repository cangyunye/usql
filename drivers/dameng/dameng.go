// Package dameng defines and registers usql's Dameng DM8 driver.
//
// Dameng (达梦 DM8) speaks a proprietary wire protocol served by the
// official pure-Go driver (gitee.com/chunanyong/dm, database/sql name
// "dm"). SQL and metadata are Oracle-style (DUAL, ALL_TABLES,
// SYS_CONTEXT, ...), so the Oracle metadata reader serves the catalog.
//
// See: https://gitee.com/chunanyong/dm
// Group: most
package dameng

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"regexp"
	"strings"

	// the dm package registers itself with database/sql as "dm" — the
	// Generator below returns that GoDriver name; the usql-side driver is
	// "dameng" (the scheme name)
	_ "gitee.com/chunanyong/dm" // DRIVER: dameng
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/metadata"
	orameta "github.com/xo/usql/drivers/metadata/oracle"
)

func init() {
	// go-dm speaks its own "dm://" URL scheme, which is exactly the parsed
	// dameng:// URL re-schemed: dm://user:password@host:port
	dburl.Register(dburl.Scheme{
		Driver: "dameng",
		Generator: func(u *dburl.URL) (string, string, error) {
			dsn, _, err := dburl.GenScheme("dm")(u)
			// go-dm registers with database/sql under the name "dm"
			return dsn, "dm", err
		},
		Transport: dburl.TransportTCP,
		Aliases:   []string{"dm"},
	})

	endRE := regexp.MustCompile(`;?\s*$`)
	endAnchorRE := regexp.MustCompile(`(?is)\send\s*;\s*$`)

	drivers.Register("dameng", drivers.Driver{
		AllowMultilineComments: true,
		AllowCComments:         true,
		LowerColumnNames:       true,
		Version: func(ctx context.Context, db drivers.DB) (string, error) {
			var ver string
			// DM8 publishes an Oracle-style V$VERSION; fall back through the
			// instance view for builds without it, and never fail the
			// connect over a cosmetic banner
			if err := db.QueryRowContext(ctx, `SELECT BANNER FROM V$VERSION WHERE ROWNUM = 1`).Scan(&ver); err != nil {
				if e2 := db.QueryRowContext(ctx, `SELECT SVR_VERSION FROM V$INSTANCE`).Scan(&ver); e2 != nil && !errors.Is(e2, sql.ErrNoRows) {
					return "Dameng DM8", nil
				}
			}
			if strings.TrimSpace(ver) == "" {
				return "Dameng DM8", nil
			}
			return ver, nil
		},
		User: func(ctx context.Context, db drivers.DB) (string, error) {
			var user string
			if err := db.QueryRowContext(ctx, `SELECT USER FROM DUAL`).Scan(&user); err != nil {
				return "", err
			}
			return user, nil
		},
		Process: func(_ *dburl.URL, prefix string, sqlstr string) (string, string, bool, error) {
			// trim the trailing ; like the oracle driver: DM rejects it over
			// the wire unless it terminates a PL/SQL END
			if !endAnchorRE.MatchString(sqlstr) {
				sqlstr = endRE.ReplaceAllString(sqlstr, "")
			}
			typ, q := drivers.QueryExecType(prefix, sqlstr)
			return typ, sqlstr, q, nil
		},
		NewMetadataReader: orameta.NewReaderQ(),
		NewMetadataWriter: func(db drivers.DB, w io.Writer, opts ...metadata.ReaderOption) metadata.Writer {
			return metadata.NewDefaultWriter(orameta.NewReaderQ()(db, opts...))(db, w)
		},
		Copy: drivers.CopyWithInsert(func(int) string {
			return "?"
		}),
	})
}
