// Package postgres defines and registers usql's PostgreSQL driver.
//
// Alias: cockroachdb, CockroachDB
// Alias: redshift, Amazon Redshift
//
// See: https://github.com/lib/pq
// Group: base
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lib/pq" // DRIVER
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	pgmeta "github.com/xo/usql/drivers/metadata/postgres"
	"github.com/xo/usql/env"
	"github.com/xo/usql/rline"
	"github.com/xo/usql/text"
)

// pgExtraCommands are PostgreSQL keywords the common mid-statement fallback
// lacks: pattern matching, upsert and window clauses.
var pgExtraCommands = []string{
	"ILIKE",
	"ON CONFLICT",
	"RETURNING",
	"FILTER",
	"WINDOW",
	"LATERAL",
	"FOR UPDATE",
}

// pgBuiltinFunctions are PostgreSQL's everyday expression functions the
// dialect-neutral core list lacks. Static, so they serve at zero query cost.
var pgBuiltinFunctions = []completer.BuiltinFunc{
	{Name: "NOW"},
	{Name: "DATE_TRUNC", Args: "unit, ts"},
	{Name: "DATE_PART", Args: "field, ts"},
	{Name: "TO_CHAR", Args: "ts, fmt"},
	{Name: "TO_DATE", Args: "str, fmt"},
	{Name: "TO_NUMBER", Args: "str, fmt"},
	{Name: "TO_TIMESTAMP", Args: "str, fmt"},
	{Name: "STRING_AGG", Args: "expr, delim"},
	{Name: "ARRAY_AGG", Args: "expr"},
	{Name: "ARRAY_TO_STRING", Args: "arr, sep"},
	{Name: "GREATEST", Args: "expr, .."},
	{Name: "LEAST", Args: "expr, .."},
	{Name: "VERSION", Bare: true},
	{Name: "CURRENT_DATABASE", Bare: true},
	{Name: "CURRENT_SCHEMA", Bare: true},
}

// pgTableFunctions are PostgreSQL's set-returning functions, valid in
// FROM/JOIN positions.
var pgTableFunctions = []completer.BuiltinFunc{
	{Name: "GENERATE_SERIES", Args: "start, stop"},
	{Name: "UNNEST", Args: "array"},
	{Name: "JSONB_TO_RECORDSET", Args: "jsonb"},
}

func init() {
	openConn := func(stdout, stderr func() io.Writer, dsn string) (*sql.DB, error) {
		conn, err := pq.NewConnector(dsn)
		if err != nil {
			return nil, err
		}
		noticeConn := pq.ConnectorWithNoticeHandler(conn, func(notice *pq.Error) {
			out := stderr()
			fmt.Fprintln(out, notice.Severity+": ", notice.Message)
			if notice.Hint != "" {
				fmt.Fprintln(out, "HINT: ", notice.Hint)
			}
		})
		notificationConn := pq.ConnectorWithNotificationHandler(noticeConn, func(notification *pq.Notification) {
			var payload string
			if notification.Extra != "" {
				payload = fmt.Sprintf(text.NotificationPayload, notification.Extra)
			}
			fmt.Fprintln(stdout(), fmt.Sprintf(text.NotificationReceived, notification.Channel, payload, notification.BePid))
		})
		return sql.OpenDB(notificationConn), nil
	}
	drivers.Register("postgres", drivers.Driver{
		Name:                   "pq",
		AllowDollar:            true,
		AllowMultilineComments: true,
		LexerName:              "postgres",
		ForceParams: func(u *dburl.URL) {
			if u.Scheme == "cockroachdb" {
				drivers.ForceQueryParameters([]string{"sslmode", "disable"})(u)
			}
		},
		Open: func(ctx context.Context, u *dburl.URL, stdout, stderr func() io.Writer) (func(string, string) (*sql.DB, error), error) {
			return func(_, dsn string) (*sql.DB, error) {
				conn, err := openConn(stdout, stderr, dsn)
				if err != nil {
					return nil, err
				}
				// special retry handling case, since there's no lib/pq retry mode
				if env.Get("SSLMODE") == "retry" && !u.Query().Has("sslmode") {
					switch err = conn.PingContext(ctx); {
					case errors.Is(err, pq.ErrSSLNotSupported):
						s := "sslmode=disable " + dsn
						conn, err = openConn(stdout, stderr, s)
						if err != nil {
							return nil, err
						}
						u.DSN = s
					case err != nil:
						return nil, err
					}
				}
				return conn, nil
			}, nil
		},
		Version: func(ctx context.Context, db drivers.DB) (string, error) {
			// numeric version
			// SHOW server_version_num;
			var ver string
			err := db.QueryRowContext(ctx, `SHOW server_version`).Scan(&ver)
			if err != nil {
				return "", err
			}
			return "PostgreSQL " + ver, nil
		},
		ChangePassword: func(db drivers.DB, user, newpw, _ string) error {
			_, err := db.Exec(`ALTER USER ` + user + ` PASSWORD '` + newpw + `'`)
			return err
		},
		Err: func(err error) (string, string) {
			if e, ok := err.(*pq.Error); ok {
				return string(e.Code), e.Message
			}
			return "", err.Error()
		},
		IsPasswordErr: func(err error) bool {
			if e, ok := err.(*pq.Error); ok {
				return e.Code.Name() == "invalid_password"
			}
			return false
		},
		NewMetadataReader: pgmeta.NewReader(),
		NewCompleter: func(db drivers.DB, opts ...completer.Option) rline.Completer {
			reader := pgmeta.NewReader()(db,
				// completion is interactive; the same budget as the other
				// drivers with slow-catalog tolerance
				metadata.WithTimeout(20*time.Second),
				metadata.WithLimit(100000),
			)
			opts = append([]completer.Option{
				completer.WithReader(reader),
				completer.WithDB(db),
				completer.WithSchemaKind("schema"),
				completer.WithExtraSQLCommands(pgExtraCommands...),
				completer.WithBuiltinFunctions(pgBuiltinFunctions...),
				completer.WithTableFunctions(pgTableFunctions...),
			}, opts...)
			return completer.NewDefaultCompleter(opts...)
		},
		NewMetadataWriter: func(db drivers.DB, w io.Writer, opts ...metadata.ReaderOption) metadata.Writer {
			return metadata.NewDefaultWriter(pgmeta.NewReader()(db, opts...))(db, w)
		},
		Copy: func(ctx context.Context, db *sql.DB, rows *sql.Rows, table string) (int64, error) {
			columns, err := rows.Columns()
			if err != nil {
				return 0, fmt.Errorf("failed to fetch source rows columns: %w", err)
			}
			clen := len(columns)

			query := table
			if !strings.HasPrefix(strings.ToLower(query), "insert into") {
				leftParen := strings.IndexRune(table, '(')
				colQuery := "SELECT * FROM " + table + " WHERE 1=0"
				if leftParen != -1 {
					colQuery = "SELECT " + table[leftParen+1:len(table)-1] + " FROM " + table[:leftParen] + " WHERE 1=0"
					table = table[:leftParen]
				}
				colStmt, err := db.PrepareContext(ctx, colQuery)
				if err != nil {
					return 0, fmt.Errorf("failed to prepare query to determine target table columns: %w", err)
				}
				defer colStmt.Close()
				colRows, err := colStmt.QueryContext(ctx)
				if err != nil {
					return 0, fmt.Errorf("failed to execute query to determine target table columns: %w", err)
				}
				columns, err := colRows.Columns()
				if err != nil {
					return 0, fmt.Errorf("failed to fetch target table columns: %w", err)
				}
				if schemaSep := strings.Index(table, "."); schemaSep >= 0 {
					query = pq.CopyInSchema(table[:schemaSep], table[schemaSep+1:], columns...)
				} else {
					query = pq.CopyIn(table, columns...)
				}
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return 0, fmt.Errorf("failed to begin transaction: %w", err)
			}
			stmt, err := tx.PrepareContext(ctx, query)
			if err != nil {
				return 0, fmt.Errorf("failed to prepare insert query: %w", err)
			}
			defer stmt.Close()

			values := make([]interface{}, clen)
			for i := 0; i < clen; i++ {
				values[i] = new(interface{})
			}

			var n int64
			for rows.Next() {
				err = rows.Scan(values...)
				if err != nil {
					return n, fmt.Errorf("failed to scan row: %w", err)
				}
				_, err := stmt.ExecContext(ctx, values...)
				if err != nil {
					return n, fmt.Errorf("failed to exec copy: %w", err)
				}
			}
			res, err := stmt.ExecContext(ctx)
			if err != nil {
				return n, fmt.Errorf("failed to final exec copy: %w", err)
			}
			rn, err := res.RowsAffected()
			if err != nil {
				return n, fmt.Errorf("failed to check rows affected: %w", err)
			}
			n += rn

			err = tx.Commit()
			if err != nil {
				return n, fmt.Errorf("failed to commit transaction: %w", err)
			}

			return n, rows.Err()
		},
	}, "cockroachdb", "redshift")
}
