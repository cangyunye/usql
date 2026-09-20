package mysql

import (
	"time"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	infos "github.com/xo/usql/drivers/metadata/informationschema"
	"github.com/xo/usql/rline"
)

var (
	// NewReader for MySQL databases
	NewReader = infos.New(
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
		infos.WithSystemSchemas([]string{"mysql", "information_schema", "performance_schema", "sys"}),
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
