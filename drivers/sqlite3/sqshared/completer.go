package sqshared

import (
	"time"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"
)

// SQLite3 completion, layered on the generic completer:
//
//   - PRAGMA statements: the fixed pragma-name catalog is offered in the
//     "PRAGMA <cursor>" position, where the context engine would otherwise
//     fall back to generic SQL keywords (the SQLite-specific commands are
//     registered via WithStatementWords).
//   - ATTACH / DETACH: registered as statement verbs; "ATTACH <cursor>"
//     offers DATABASE, and "DETACH <cursor>" completes an attached schema
//     name from the schema cache (WithNamesVerbs).
//   - Table-valued pragma functions (pragma_table_info and friends) and the
//     JSON table functions are offered in FROM/JOIN object positions, where
//     only schemas and relations would otherwise appear.
//   - The mid-statement keyword fallback carries SQLite's expression
//     keywords (GLOB, REGEXP, ISNULL, ...) that the common list lacks.

// NewCompleter creates the completer for sqlite3-family databases,
// mirroring the generic drivers.NewCompleter fallback's reader budget.
// Caller-supplied opts are appended last, so they win — the handler passes
// its logger, connection strings, aliases and the lazy catalog cache there.
func NewCompleter(db drivers.DB, opts ...completer.Option) rline.Completer {
	readerOpts := []metadata.ReaderOption{
		// completion serves catalogs with thousands of objects: the limit
		// only guards runaway queries, it must not truncate the snapshot
		metadata.WithTimeout(10 * time.Second),
		metadata.WithLimit(100000),
	}
	opts = append([]completer.Option{
		completer.WithReader(NewMetadataReader(db, readerOpts...)),
		completer.WithDB(db),
		// ATTACH and DETACH are SQLite statement verbs the common list lacks
		completer.WithSQLStartCommands(append(completer.CommonSqlStartCommands, "ATTACH", "DETACH")),
		completer.WithStatementWords(map[string][]string{
			"PRAGMA": pragmas,
			"ATTACH": {"DATABASE"},
		}),
		completer.WithNamesVerbs("DETACH"),
		completer.WithTableFunctions(tableFunctions...),
		completer.WithExtraSQLCommands(sqliteKeywords...),
	}, opts...)
	return completer.NewDefaultCompleter(opts...)
}

// pragmas are SQLite's core pragma statement names, offered after PRAGMA.
// Statement pragmas accept optional arguments ("PRAGMA schema.name",
// "PRAGMA name = value"), so the candidates insert bare names.
// See: https://www.sqlite.org/pragma.html
var pragmas = []string{
	"application_id",
	"auto_vacuum",
	"automatic_index",
	"busy_timeout",
	"cache_size",
	"cache_spill",
	"cell_size_check",
	"checkpoint_fullfsync",
	"collation_list",
	"compile_options",
	"data_version",
	"database_list",
	"default_cache_size",
	"defer_foreign_keys",
	"encoding",
	"foreign_key_check",
	"foreign_key_list",
	"foreign_keys",
	"freelist_count",
	"fullfsync",
	"function_list",
	"hard_heap_limit",
	"ignore_check_constraints",
	"incremental_vacuum",
	"index_info",
	"index_list",
	"index_xinfo",
	"integrity_check",
	"journal_mode",
	"journal_size_limit",
	"legacy_alter_table",
	"locking_mode",
	"max_page_count",
	"mmap_size",
	"module_list",
	"optimize",
	"page_count",
	"page_size",
	"pragma_list",
	"query_only",
	"quick_check",
	"read_uncommitted",
	"recursive_triggers",
	"reverse_unordered_selects",
	"schema_version",
	"secure_delete",
	"synchronous",
	"table_info",
	"table_xinfo",
	"temp_store",
	"threads",
	"trusted_schema",
	"user_version",
	"wal_autocheckpoint",
	"wal_checkpoint",
	"writable_schema",
}

// tableFunctions are SQLite's set-returning functions — table-valued pragmas
// and the built-in JSON/series generators — valid in FROM/JOIN positions.
// See: https://www.sqlite.org/vtab.html#table-valued-functions
var tableFunctions = []completer.BuiltinFunc{
	{Name: "pragma_table_info", Args: "table"},
	{Name: "pragma_table_xinfo", Args: "table"},
	{Name: "pragma_index_info", Args: "index"},
	{Name: "pragma_index_xinfo", Args: "index"},
	{Name: "pragma_index_list", Args: "table"},
	{Name: "pragma_foreign_key_list", Args: "table"},
	{Name: "pragma_database_list"},
	{Name: "pragma_module_list"},
	{Name: "pragma_function_list"},
	{Name: "pragma_pragma_list"},
	{Name: "pragma_collation_list"},
	{Name: "pragma_compile_options"},
	{Name: "pragma_wal_checkpoint", Args: "schema"},
	{Name: "json_each", Args: "json [, path]"},
	{Name: "json_tree", Args: "json [, path]"},
	{Name: "generate_series", Args: "start, stop [, step]"},
}

// sqliteKeywords are SQLite expression and clause keywords the common
// mid-statement list lacks; they extend the keyword fallback only.
var sqliteKeywords = []string{
	"COLLATE",
	"ESCAPE",
	"GLOB",
	"IS NOT",
	"ISNULL",
	"NOTNULL",
	"ON CONFLICT",
	"REGEXP",
	"RETURNING",
}
