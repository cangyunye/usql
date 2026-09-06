# Connecting to OceanBase and openGauss

This document describes how to connect `usql` to the databases this fork adds:
**OceanBase (MySQL-compatible)**, **OceanBase (Oracle-compatible)**, and
**openGauss**. Connection strings use the same URL rules as any other
database (see the [Database Connection Strings][dburl] section of the
README).

## Driver/build tags

| Database                | scheme        | SQL driver                        | build tag    |
| ----------------------- | ------------- | --------------------------------- | ------------ |
| OceanBase MySQL         | `oceanbase://`| `go-sql-driver/mysql`             | `mysql`      |
| OceanBase Oracle        | `oboracle://` | `helingjun/obconnector-go`        | `oboracle`   |
| openGauss               | `opengauss://`| `openGauss-connector-go-pq`       | `opengauss`  |

`oceanbase://` is a wire-compatible alias of the `mysql` driver; `opengauss://`
is wire-compatible with PostgreSQL (`lib/pq` fork). `oboracle` is a separate
driver because OceanBase **Oracle-compatible tenants reject standard MySQL
clients** (error 1235) and need the OceanBase-aware `obconnector-go`.

`oboracle` and `opengauss` are in the `most` build-tag group, so a default
build does not include them. Build a binary with all three (plus the usual
PostgreSQL/MySQL/Oracle) using:

```sh
# via task (see Taskfile.yaml)
task build

# equivalent go build
CGO_ENABLED=0 go build -tags 'postgres mysql oracle opengauss oboracle moderncsqlite' -o usql .
```

> `oboracle` and `opengauss` are pure Go and work in a `CGO_ENABLED=0`
> (CGO-free) build; `mysql`/`postgres`/`oracle` are pure Go too.

## OceanBase (MySQL-compatible)

Connect to a MySQL-compatible tenant directly on the observer's MySQL port
(default `2881`):

```sh
# user@tenant, no #cluster
usql 'oceanbase://root@obmysql:P4ssw0rd@127.0.0.1:2881/test'
```

- The username is **`user@tenant`** (the `@tenant` suffix selects the
  OceanBase tenant). The database is the tenant's database/schema.
- Do **not** add a `#cluster` suffix when connecting directly to an
  **observer** (see [The `#cluster` suffix](#the-cluster-suffix)).
- Verify:

```sh
usql 'oceanbase://root@obmysql:P4ssw0rd@127.0.0.1:2881/' -c 'select version();'
# 5.7.25-OceanBase-v4.4.2.2
```

## OceanBase (Oracle-compatible)

OceanBase Oracle-compatible tenants must be reached through `obconnector-go`:

```sh
# user@tenant; the Oracle tenant's privileged user is often SYS
usql 'oboracle://sys@oratest:P4ssw0rd@127.0.0.1:2881/'
```

- SQL is Oracle-style: `SELECT ... FROM dual`, `user_tables`, `v$version`, etc.
- The username is **`user@tenant`**. Use the tenant's actual user (for a
  freshly created Oracle tenant this is often `sys`).
- Do **not** add a `#cluster` suffix for direct observer connections.
- Switch schema (not tenant) with `ALTER SESSION SET CURRENT_SCHEMA = <schema>`;
  completion follows the session's current schema. Note that a login user's
  own objects are excluded from `\d`-style listings when connected as a
  system user (`SYS`), since system schemas are filtered out.
- Verify:

```sh
usql 'oboracle://sys@oratest:P4ssw0rd@127.0.0.1:2881/' -c 'select user from dual;'
# SYS
```

## openGauss

openGauss speaks the PostgreSQL wire protocol, so the DSN looks like
PostgreSQL's:

```sh
usql 'opengauss://user:P4ssw0rd@127.0.0.1:5432/postgres'
```

- Supported parameters match `lib/pq`: `host`, `port`, `user`, `password`,
  `dbname`, `sslmode`, etc. (either URL form or `key=value` DSN). In a URL,
  escape `#` as `%23` and `$` as `%24` when they appear in the password.
- **Database defaults to `postgres`.** The PostgreSQL wire protocol requires
  a database at connect time, and libpq would default to the login user's
  own database — which usually does not exist (`database "ogadmin" does not
  exist`). Omitting the database connects to the built-in `postgres`
  database instead:

  ```sh
  usql 'opengauss://user:P4ssw0rd@127.0.0.1:5432'
  ```

- **Switch databases with `\c <dbname>`** after connecting (the psql-style
  shortcut; it reconnects to the named database on the same server, reusing
  user/host/port/password). This is the only way to change databases on
  PostgreSQL-family servers, including the compat-mode databases:

  ```sh
  og:ogadmin@127.0.0.1/postgres=> \c og_ora
  og:ogadmin@127.0.0.1/og_ora=>
  ```

- Verify:

```sh
usql 'opengauss://user:P4ssw0rd@127.0.0.1:5432/postgres' -c 'select version();'
```

## The `#cluster` suffix

The OceanBase username format is `user@tenant#cluster`. The `#cluster` part is
**only meaningful when going through OBProxy** (the proxy uses it to route to a
cluster). When connecting **directly to an observer** (the default, port
`2881`), the observer does **not** interpret `#cluster`, so omit it and use the
two-part `user@tenant` form:

| Target                | Username          |
| --------------------- | ----------------- |
| observer (direct)     | `user@tenant`     |
| OBProxy               | `user@tenant#cluster` |

The `obconnector-go` driver forwards the username verbatim (it does not parse
`#`), so whether a `#cluster` suffix works is decided by the server. If you do
include `#cluster` in a URL, **escape the `#` as `%23`** (a raw `#` would be
treated as the URL fragment):

```sh
# only for OBProxy
usql 'oboracle://sys%40oratest%23obcluster:P4ssw0rd@127.0.0.1:2883/'
```

## Storing connections with `\conns`

Use the `\conns` meta command to store named connections (passwords are kept
out of the config file):

```sh
(not connected)=> \conns
\conns> [a]dd  [1..n] edit  [c <name|#>]onnect  [d <name|#>]elete  [q]uit: a
```

In the form, fill:

- **name** — an identifier (letters, digits, `_`), e.g. `ob_oracle`
- **driver** — pick the scheme (`oboracle`, `oceanbase`, `opengauss`, ...)
- **username** — `user@tenant` (e.g. `sys@oratest` / `root@obmysql`); it may
  contain `@` and is stored and injected correctly
- **hostname / port / database / parameters / password** — as usual

Then connect by name — either `\c ob_oracle`, or the one-step
`\conns ob_oracle` / `\conns <row number>` (opens the stored connection
without entering the manager). Inside the manager, `c <name|#>` does the
same.

Stored connections live in `connections.yaml` (no passwords) with passwords in
the OS keyring, or in a `0600`-permission `secrets.json` fallback file. Both
are in the usql configuration directory:

- **Linux/Unix**: `$HOME/.config/usql/` (or `$XDG_CONFIG_HOME/usql/`)
- **macOS**: `$HOME/Library/Application Support/usql/`
- **Windows**: `%AppData%/usql/`

So the two files are `connections.yaml` and `secrets.json` in that directory.

Connecting from a command-line DSN also **saves** it automatically: pass
`--name <name>` to choose the stored name, otherwise a default identifier is
derived from `scheme_user_host_port_dbname` (database optional). The URL's
password is kept in the secret store, never in `connections.yaml`.

## Completion notes for these databases

- The candidate menu opens **while typing** (input-method style,
  display-only; `<Tab>` or `<Enter>` accepts). Table candidates are shown
  fully qualified as `schema.table`, so similarly named objects are easy to
  tell apart; column candidates follow aliases (`f.` lists that table's
  columns).
- The candidate list respects the session scope: after `USE db` (OceanBase
  MySQL tenants), `SET search_path` (openGauss/PostgreSQL) or `ALTER SESSION
  SET CURRENT_SCHEMA` (OceanBase Oracle tenants) the scope cache is dropped
  automatically.
- Catalog metadata is cached for one minute per session; OceanBase
  dictionary queries are slow on a cold cache (up to ~10s), but run in the
  background — typing never blocks, and every later keystroke is instant.
- `\conninfo` masks the stored password when displaying the connection
  string.

[conns]: https://github.com/xo/usql#managing-named-connections-conns
