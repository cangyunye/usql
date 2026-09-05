# Repository Guidelines

## Project Overview

`usql` is a universal SQL CLI written in Go (`module github.com/xo/usql`, Go 1.26.1). It connects to dozens of databases (PostgreSQL, MySQL, SQLite3, SQL Server, Oracle, ClickHouse, DuckDB, etc.) through a single `database/sql`-compatible driver registry, normalizing connection-string parsing, query execution, meta-commands (`\d`, `\drivers`, `\?`), result formatting, and schema introspection.

The single source of truth is the **driver registry**: every database is registered at runtime by calling `drivers.Register(name, drivers.Driver{...})` from an `init()` in a per-database package. Almost everything else — the README "Supported Database Schemes" table, the generated `internal/` build-tag files, the `\drivers` help output, and interactive connection-string tab-completion — is auto-generated or auto-derived from that registry.

## Architecture & Data Flow

Two-layer driver model:

1. **URL scheme layer (`xo/dburl`, external)** — maps a URL scheme like `postgres://` to a SQL driver name and a DSN generator. Schemes live in dburl's `BaseSchemes()`; wire-compatible DBs reuse an existing driver via the `Override` field (e.g. `cockroachdb`/`redshift` → `postgres`, `tidb`/`vitess`/`memsql` → `mysql`). usql can also manipulate this registry at runtime via `dburl.Register`, `dburl.RegisterAlias`, `dburl.Unregister` (see `internal/z.go`).
2. **usql driver layer (`drivers`)** — `drivers.Register(name, drivers.Driver{...})` inserts a `drivers.Driver` struct into the `drivers` map (`drivers.Available()`). This map drives `\drivers`, completion, connection handling, metadata introspection, and `Copy` support.

Data flow: `usql <scheme>://user@host/db` → `run.go`/`handler` parse the DSN with `dburl.Parse` → resolve driver name → look up `drivers[driver]` → call `Driver.Open` → get `*sql.DB` → execute via `stmt`/`handler`.

Generation flow: `go generate` (i.e. `go run gen.go`, wired by `//go:generate go run gen.go` in `main.go`) walks `drivers/`, parses each package's doc comment + `// DRIVER`-tagged import, and regenerates:
- `internal/internal.go` (`KnownBuildTags()` map),
- `internal/<tag>.go` (blank imports, one per driver, under build tags),
- the README driver table (between `<!-- DRIVER DETAILS START -->`/`<!-- DRIVER DETAILS END -->`),
- `metacmd/descs.go` (from `metacmd/cmds.go` doc comments),
- `text/license.go`.

> Note: `CONTRIBUTING.md` says to run `internal/gen.sh`, but that file does **not** exist. The real generator is `go run gen.go` (equivalently `go generate`).

## Key Directories

- `drivers/` — per-database driver packages. Each `drivers/<tag>/<tag>.go` registers itself via `init()` + `drivers.Register`. (`drivers.go` holds the `Driver` struct and registry; `qtype.go` holds query/exec-prefix maps.)
- `internal/` — **generated** blank-import files + `KnownBuildTags()`. One file per driver, gated by build tags. DO NOT hand-edit; regenerate via `go generate`.
- `handler/` — connection opening, query execution, completion, buffering (`handler.go`, ~44 KB).
- `metacmd/` — `\` meta-commands. `cmds.go` (hand-written), `descs.go` (generated from `cmds.go` doc comments), `charts/`.
- `stmt/` — statement parsing, parameter handling, query buffering.
- `env/` — `USQL_*` env vars, config/RC/history file handling, shell helpers.
- `text/` — banners, usage text, license, sentinel errors (`errors.go`).
- `rline/` — readline wrapper (`readline` library).
- `styles/` — chroma syntax styles.
- `drivers/metadata/` — schema-introspection readers/writers for `\d*` commands (e.g. `informationschema`, `postgres`, `sqlite3`).
- `drivers/completer/` — tab-completion.
- `drivers/testdata/` — golden files + `gen-golden.sh` for reader tests.
- `testdata/` — SQL fixtures (`copy.sql`, `numbers.sql`, `quotes.sql`, `inc_test.sql`, `booktest/`).
- `contrib/` — Docker/podman test provisioning (`podman-run.sh`, `podman-stop.sh`, `config.yaml`, `usql-test.sh`, per-DB `podman-config`).
- `.github/workflows/` — CI (`test.yml`, `release.yml`, `announce.yml`).

## Development Commands

```sh
# Build with base drivers (default: postgres, mysql, sqlite3, sqlserver, oracle, clickhouse, csvq)
go build .

# Build with most / all drivers
go build -tags most .
go build -tags all .

# Build specific driver(s) or exclude some
go build -tags 'postgres duckdb' .
go build -tags 'most no_duckdb' .

# Run
go run . 'postgres://user:pass@host/db'

# Unit tests
go test ./...

# Driver integration tests (spins up real DB containers via dockertest; needs Docker)
go test github.com/xo/usql/drivers
# keep containers between runs:
go test -cleanup=false github.com/xo/usql/drivers

# Regenerate internal/, README table, metacmd/descs.go, text/license.go
go generate            # == go run gen.go

# Multi-platform release build
./build.sh             # see --help via build.sh -h for flags (e.g. -a arch, -v version)
```

No `Makefile`/`Dockerfile` exists. `build.sh` is the release build script (cross-compile, tar/zip packaging, checksums). `update-deps.sh` bumps Go dependencies. `go mod tidy` after adding a dependency.

## Code Conventions & Common Patterns

**Driver package doc comment format** — `gen.go`'s `parseDriverInfo` parses these from the package comment and errors if any are missing/malformed:

```go
// Package duckdb defines and registers usql's DuckDB driver.
//
// See: https://github.com/duckdb/duckdb-go/v2
// Group: most
// Alias: dk,DuckDB
```

- Package comment MUST start with `Package <tag> defines and registers usql's ` and contain `<Desc> driver.` and a `See: <URL>` line.
- Optional `Group: base|most|all|bad` (default `most`). Optional `Alias: <name>,<desc>` (repeatable). `Requires CGO.` in the comment marks a CGO driver.
- The import of the actual SQL driver is tagged `// DRIVER` (and can override the registered name): `"github.com/jackc/pgx/v5/stdlib" // DRIVER`. Without it, `gen.go` assumes the driver package path/name.

**Driver registration** — each driver calls `drivers.Register(name, drivers.Driver{...})` in `init()` (see `drivers/pgx/pgx.go`, `drivers/duckdb/duckdb.go`). `drivers.Driver` (in `drivers/drivers.go`) supports optional hooks: `Open`, `Version`, `User`, `ChangePassword`, `IsPasswordErr`, `Err`, `NewMetadataReader`, `NewMetadataWriter`, `NewCompleter`, `Copy`, `ForceParams`, plus flags like `AllowDollar`, `AllowMultilineComments`, `LexerName`, `LowerColumnNames`. Minimal drivers (e.g. `drivers/mysql/mysql.go`) can set only a few.

**Build-tag gating** — all gating lives in the generated `internal/<tag>.go` files (driver packages themselves carry no build constraints). `gen.go`'s `writeInternal` produces:
- `base` → `//go:build (!no_base || <tag>) && !no_<tag>`
- `most` → `//go:build (all || most || <tag>) && !no_<tag>`
- `all` → `//go:build (all || <tag>) && !no_<tag>`
- `bad` → `//go:build (bad || <tag>) && !no_<tag>`

**Generated files** — `internal/*.go`, `metacmd/descs.go`, `text/license.go` carry `// Code generated by gen.go. DO NOT EDIT.` — never hand-edit.

**Meta-commands** — defined in `metacmd/cmds.go` as `func (p *Params) <Name>(...) error` with a doc comment the generator parses (`<Func> is a <section> meta command (...)` + a `Descs:` block). `descs.go` is regenerated from them.

**Error handling** — sentinel errors in `text/errors.go` (`ErrDriverNotAvailable`, `ErrMissingDSN`, ...). Driver-specific error mapping via `Driver.Err` / `Driver.IsPasswordErr`.

**Async/context** — DB operations thread `context.Context` (`ExecContext`, `QueryContext`, etc.). `Driver.Open` and `Driver.Copy` take a `context.Context`.

**Naming** — driver tag/directory is lowercase; the "Database" column in the README table and the `Desc` come from the doc comment. Wire-compatible DBs reuse an existing scheme and don't get their own driver package (they appear as `(postgres)`/`(mysql)` in `\drivers`).

## Important Files

- `main.go` — entry point; carries `//go:generate go run gen.go`.
- `run.go` — `New()` builds the cobra/viper CLI; flag/DSN parsing, command wiring.
- `gen.go` — code generator (`//go:build ignore`); the driver-source-of-truth scanner.
- `drivers/drivers.go` — `drivers.Driver` struct, `Register`, `Available`, `Registered`.
- `drivers/qtype.go` — query/exec prefix maps (`QueryExecType`).
- `internal/internal.go` — generated `KnownBuildTags()`.
- `internal/z.go` — hand-written `init()` doing special-case `dburl.RegisterAlias`/`Unregister` (oleodbc, moderncsqlite).
- `go.mod` / `go.sum` — module deps; `go 1.26.1`.
- `build.sh` — release/cross-build script.
- `CONTRIBUTING.md` — contribution workflow (contains the stale `internal/gen.sh` reference; use `go generate`).
- `README.md` — the driver table is generated between `<!-- DRIVER DETAILS START -->`/`<!-- DRIVER DETAILS END -->`.

## Runtime/Tooling Preferences

- **Runtime**: Go 1.26.1; no Node/other runtime required.
- **Package manager**: standard Go modules (`go get`, `go mod tidy`). No npm/yarn/pnpm.
- **Codegen**: `go generate` / `go run gen.go` (mandatory after adding a driver or changing a driver doc comment).
- **Build**: `go build` with build tags, or `build.sh` for release. No Makefile.
- **External deps**: `spf13/cobra`, `spf13/viper`, `xo/dburl`, `alecthomas/chroma/v2`, `gohxs/readline`, `ory/dockertest/v3` (tests), `google/goexpect` (tests).
- **Container runtime**: Docker or Podman needed for driver integration tests and `contrib/` provisioning.

## Testing & QA

- **Unit tests**: `go test ./...`. Covers `stmt/` parsing, `drivers/` helpers, `drivers/metadata/` readers, `drivers/completer/`.
- **Integration tests**: `go test github.com/xo/usql/drivers` (`drivers/drivers_test.go`) uses `ory/dockertest/v3` to spin up real DB containers (Postgres, MySQL, etc.) and exercise `\d*` metadata + queries. Requires Docker. Pass `-cleanup=false` during development to keep containers running. `contrib/usql-test.sh` and `contrib/podman-run.sh` provision test databases.
- **Golden/snapshot tests**: `drivers/testdata/gen-golden.sh` regenerates golden files consumed by `drivers/*/sqshared/reader_test.go` and `drivers/metadata/*/metadata_test.go`.
- **CLI test helper**: `testcli.go`; `main_test.go` imports `google/goexpect`.
- **Fixtures**: root `testdata/` (`copy.sql`, `numbers.sql`, `quotes.sql`, `inc_test.sql`, `booktest/`).
- **CI** (`.github/workflows/test.yml`): runs build + `go test` across OSes. No separate lint step beyond `go vet`; keep `gofmt` clean.

## Adding a New Database Driver (e.g. OceanBase, openGauss)

This repo's fork will add OceanBase and openGauss. Both are wire-compatible (OceanBase ↔ MySQL, openGauss ↔ PostgreSQL). Recipe:

1. **Register the URL scheme** in `xo/dburl` (external). Either add to dburl's `BaseSchemes()` (fork it) or call `dburl.Register(dburl.Scheme{Driver: "<scheme>", Generator: ..., Override: "mysql"/"postgres"})` / `dburl.RegisterAlias` at runtime (pattern in `internal/z.go`). For wire-compatible DBs, set `Override` to the existing driver name.
2. **Create `drivers/<tag>/<tag>.go`** with the required doc comment (`Package <tag> defines and registers usql's <Desc> driver.`, `See: <URL>`, `Group: ...`) and an `init()` calling `drivers.Register("<name>", drivers.Driver{...})`.
3. **Add the dependency** to `go.mod` (`go get <pkg>` then `go mod tidy`).
4. **Regenerate** via `go generate` (== `go run gen.go`) — this creates `internal/<tag>.go`, updates `internal/internal.go`'s `KnownBuildTags()`, and adds a row to the README table.
5. **Build and verify**: `go build -tags '<tag>' .`, then run `\drivers` and connect to confirm.
6. **Update hand-written docs**: `README.md` prose (Building section), `CONTRIBUTING.md` narrative.
7. **Optional**: provide `NewMetadataReader`/`NewMetadataWriter` for `\d*` support (see `drivers/metadata/informationschema` or plugin readers), and `NewCompleter` for tab-completion.

For wire-compatible DBs you may skip a distinct driver package entirely and simply connect with the existing `mysql://` / `postgres://` scheme — but a distinct scheme/driver entry is needed to expose `oceanbase://` / `opengauss://` as first-class schemes.
