package mysql

import (
	"database/sql"
	"strings"
	"time"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	infos "github.com/xo/usql/drivers/metadata/informationschema"
	"github.com/xo/usql/rline"
)

// mysqlStartCommands extends the common statement starters with MySQL's own
// verbs, REPLACE above all.
var mysqlStartCommands = append(completer.CommonSqlStartCommands,
	"USE",
	"REPLACE",
	"OPTIMIZE",
	"REPAIR",
	"FLUSH",
	"RENAME",
	"KILL",
)

// mysqlExtraCommands are MySQL keywords the common mid-statement fallback
// lacks: pattern matching, collation, arithmetic and upsert clauses.
var mysqlExtraCommands = []string{
	"REGEXP",
	"RLIKE",
	"COLLATE",
	"ESCAPE",
	"INTERVAL",
	"DIV",
	"IGNORE",
	"ON DUPLICATE KEY",
	"FOR UPDATE",
}

// mysqlBuiltinFunctions are MySQL's everyday expression functions the
// dialect-neutral core list lacks. Static, so they serve at zero query cost.
var mysqlBuiltinFunctions = []completer.BuiltinFunc{
	{Name: "NOW"},
	{Name: "CURDATE"},
	{Name: "CURTIME"},
	{Name: "IFNULL", Args: "expr, value"},
	{Name: "GROUP_CONCAT", Args: "expr, .."},
	{Name: "CONCAT_WS", Args: "sep, str, .."},
	{Name: "DATE_FORMAT", Args: "date, fmt"},
	{Name: "STR_TO_DATE", Args: "str, fmt"},
	{Name: "UNIX_TIMESTAMP"},
	{Name: "DATE_ADD", Args: "date, INTERVAL expr unit"},
	{Name: "DATE_SUB", Args: "date, INTERVAL expr unit"},
	{Name: "DATEDIFF", Args: "d1, d2"},
	{Name: "TIMESTAMPDIFF", Args: "unit, d1, d2"},
	{Name: "SUBSTR", Args: "str, pos, len"},
	{Name: "LOCATE", Args: "sub, str"},
	{Name: "LEFT", Args: "str, n"},
	{Name: "RIGHT", Args: "str, n"},
	{Name: "JSON_EXTRACT", Args: "json, path, .."},
	{Name: "MD5", Args: "str"},
	{Name: "SHA2", Args: "str, bits"},
	{Name: "UUID", Bare: true},
	{Name: "LAST_INSERT_ID", Bare: true},
	{Name: "DATABASE", Bare: true},
	{Name: "VERSION", Bare: true},
	{Name: "ROW_COUNT", Bare: true},
}

// DialectOptions returns the MySQL-specific completer options — statement
// starters, mid-statement keywords and expression functions beyond the
// dialect-neutral core. NewCompleter itself stays dialect-neutral because
// DuckDB wires it too; the MySQL-family drivers layer these on top.
func DialectOptions() []completer.Option {
	return []completer.Option{
		completer.WithSQLStartCommands(mysqlStartCommands),
		completer.WithExtraSQLCommands(mysqlExtraCommands...),
		completer.WithBuiltinFunctions(mysqlBuiltinFunctions...),
	}
}

// catalogReader serves \l for MySQL: databases double as catalogs, so the
// schemata view backs both the \l listing and its argument completion.
type catalogReader struct {
	metadata.LoggingReader
}

var _ metadata.CatalogReader = &catalogReader{}

// Catalogs from the schemata view, matching names
func (r catalogReader) Catalogs(f metadata.Filter) (*metadata.CatalogSet, error) {
	qstr := `SELECT
  schema_name
FROM information_schema.schemata
`
	conds, vals := []string{}, []interface{}{}
	if f.Name != "" {
		conds = append(conds, "schema_name LIKE ?")
		vals = append(vals, f.Name)
	}
	if len(conds) != 0 {
		qstr += " WHERE " + strings.Join(conds, " AND ")
	}
	qstr += " ORDER BY schema_name"
	rows, closeRows, err := r.Query(qstr, vals...)
	if err != nil {
		if err == sql.ErrNoRows {
			return metadata.NewCatalogSet([]metadata.Catalog{}), nil
		}
		return nil, err
	}
	defer closeRows()

	results := []metadata.Catalog{}
	for rows.Next() {
		rec := metadata.Catalog{}
		if err := rows.Scan(&rec.Catalog); err != nil {
			return nil, err
		}
		results = append(results, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metadata.NewCatalogSet(results), nil
}

var (
	// NewReader for MySQL databases: the information-schema reader, plus
	// the catalogs the schemata view serves
	NewReader = func(db drivers.DB, opts ...metadata.ReaderOption) metadata.Reader {
		return metadata.NewPluginReader(
			newInfoSchemaReader(db, opts...),
			&catalogReader{LoggingReader: metadata.NewLoggingReader(db, opts...)},
		)
	}
	// newInfoSchemaReader is the information-schema reader the completer and
	// writer serve from
	newInfoSchemaReader = infos.New(
		infos.WithPlaceholder(func(int) string { return "?" }),
		infos.WithSequences(false),
		infos.WithCheckConstraints(false),
		infos.WithCustomClauses(map[infos.ClauseName]string{
			infos.ColumnsDataType:                 "column_type",
			infos.ColumnsNumericPrecRadix:         "10",
			infos.FunctionColumnsNumericPrecRadix: "10",
			infos.ConstraintIsDeferrable:          "''",
			infos.ConstraintInitiallyDeferred:     "''",
			infos.PrivilegesGrantor:               "''",
			infos.ConstraintJoinCond:              "AND r.referenced_table_name = f.table_name",
		}),
		infos.WithSystemSchemas([]string{"mysql", "information_schema", "performance_schema", "sys", "oceanbase"}),
		infos.WithCurrentSchema("COALESCE(DATABASE(), '%')"),
		infos.WithUsagePrivileges(false),
	)
	// NewCompleter for MySQL databases
	NewCompleter = func(db drivers.DB, opts ...completer.Option) rline.Completer {
		readerOpts := []metadata.ReaderOption{
			// this needs to be relatively low, since autocomplete is very
			// interactive — but low enough timeouts break column completion
			// on wire-compatible databases with slow catalogs: OceanBase
			// MySQL tenants measure ~10.5s for a cold 200-column table's
			// columns query (and ~8s for ordinary ones), so 10s timed out
			metadata.WithTimeout(20 * time.Second),
			// completion serves catalogs with thousands of objects
			metadata.WithLimit(100000),
		}
		reader := NewReader(db, readerOpts...)
		opts = append([]completer.Option{
			completer.WithReader(reader),
			completer.WithDB(db),
			// MySQL calls its namespaces databases; the menu badge says so
			completer.WithSchemaKind("database"),
			// USE registers as a statement command: the core completer serves
			// "USE <name>" from the L1 schema cache (non-blocking)
			completer.WithSQLStartCommands(append(completer.CommonSqlStartCommands, "USE")),
		}, opts...)
		return completer.NewDefaultCompleter(opts...)
	}
)
