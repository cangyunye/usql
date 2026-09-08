<div align="center">
  <img src="https://raw.githubusercontent.com/xo/usql-logo/main/usql.png" height="120">
</div>

<div align="center">
  <a href="README.md">English</a> | 简体中文
</div>

<div align="center">
  <a href="#installing" title="Installing">安装</a> |
  <a href="#building" title="Building">构建</a> |
  <a href="#database-support" title="Database Support">数据库支持</a> |
  <a href="#using" title="Using">使用</a> |
  <a href="#features-and-compatibility" title="Features and Compatibility">功能与兼容性</a> |
  <a href="https://github.com/xo/usql/releases" title="Releases">发布版本</a> |
  <a href="#contributing" title="Contributing">参与贡献</a>
</div>

<br/>

`usql` 是一个通用 SQL 命令行界面，支持 PostgreSQL、MySQL、Oracle Database、
SQLite3、Microsoft SQL Server 以及[许多其他数据库][databases]，包括 NoSQL 和
非关系型数据库！

`usql` 提供了一种简洁的方式来通过命令行操作 [SQL 和 NoSQL 数据库][databases]，
其交互方式深受 PostgreSQL 的 `psql` 启发。`usql` 支持大部分 `psql` 核心功能，
例如[变量][variables]、[反引号][backticks]、[反斜杠命令][commands]，并且还具备
一些 `psql` 没有的特性，例如[多数据库支持][databases]、[数据库间复制][copying]、
[语法高亮][highlighting]、[上下文感知补全][completion]以及[终端图表][termgraphics]。

习惯使用 `psql` 管理 PostgreSQL 的数据库管理员和开发人员，在面对其他数据库时
会发现 `usql` 直观易用，是其他数据库自带命令行客户端的理想替代品。

[![Unit Tests][usql-ci-status]][usql-ci]
[![Go Reference][goref-usql-status]][goref-usql]
[![Releases][release-status]][Releases]
[![Discord Discussion][discord-status]][discord]

[usql-ci]: https://github.com/xo/usql/actions/workflows/test.yml "Test CI"
[usql-ci-status]: https://github.com/xo/usql/actions/workflows/test.yml/badge.svg "Test CI"
[goref-usql]: https://pkg.go.dev/github.com/xo/usql "Go Reference"
[goref-usql-status]: https://pkg.go.dev/badge/github.com/xo/usql.svg "Go Reference"
[release-status]: https://img.shields.io/github/v/release/xo/usql?display_name=tag&sort=semver "Latest Release"
[discord]: https://discord.gg/WDWAgXwJqN "Discord Discussion"
[discord-status]: https://img.shields.io/discord/829150509658013727.svg?label=Discord&logo=Discord&colorB=7289da&style=flat-square "Discord Discussion"
[installing]: #installing "Installing"
[databases]: #database-support "Database Support"
[releases]: https://github.com/xo/usql/releases "Releases"

<a id="installing"></a>

## 安装（Installing）

`usql` 可以[通过发布包安装][via Release]、[通过 Homebrew 安装][via Homebrew]、
[通过 AUR 安装][via AUR]、[通过 Scoop 安装][via Scoop]、[通过 Go 安装][via Go]，
或者[通过 Docker 安装][via Docker]：

[via Release]: #installing-via-release
[via Homebrew]: #installing-via-homebrew-macos-and-linux
[via AUR]: #installing-via-aur-arch-linux
[via Scoop]: #installing-via-scoop-windows
[via Go]: #installing-via-go
[via Docker]: #installing-via-docker

<a id="installing-via-release"></a>

### 通过发布包安装（Installing via Release）

1. [下载适合你平台的发布包][releases]
2. 从 `.tar.bz2` 或 `.zip` 文件中解压出 `usql` 或 `usql.exe`
3. 将解压出的可执行文件移动到 `$PATH`（Linux/macOS）或 `%PATH%`（Windows）
   中的某个目录下

<a id="installing-via-homebrew-macos-and-linux"></a>

### 通过 Homebrew 安装（macOS 和 Linux）

照常使用 [`brew` 命令][homebrew]从 [`xo/xo` tap][xo-tap] 安装 `usql`：

```sh
# 以 most 驱动集安装 usql
$ brew install xo/xo/usql
```

[ODBC 数据库][databases]的支持可以通过 `--with-odbc` 安装参数启用：

```sh
# 添加 xo tap
$ brew tap xo/xo

# 以 odbc 支持安装 usql
$ brew install --with-odbc usql
```

<a id="installing-via-aur-arch-linux"></a>

### 通过 AUR 安装（Arch Linux）

照常使用 [`yay` 命令][yay]从 [Arch Linux AUR][aur] 安装 `usql`：

```sh
# 以 most 驱动集安装 usql
$ yay -S usql
```

或者，构建并[使用 `makepkg` 安装][arch-makepkg]：

```sh
$ git clone https://aur.archlinux.org/usql.git && cd usql
$ makepkg -si
==> Making package: usql 0.12.10-1 (Fri 26 Aug 2022 05:56:09 AM WIB)
==> Checking runtime dependencies...
==> Checking buildtime dependencies...
==> Retrieving sources...
  -> Downloading usql-0.12.10.tar.gz...
...
```

<a id="installing-via-scoop-windows"></a>

### 通过 Scoop 安装（Windows）

使用 [Scoop](https://scoop.sh) 安装 `usql`：

```powershell
# 可选：首次运行远程脚本前需要执行
> Set-ExecutionPolicy RemoteSigned -Scope CurrentUser

# 如果尚未安装 scoop，先安装 scoop
> irm get.scoop.sh | iex

# 用 scoop 安装 usql
> scoop install usql
```

<a id="installing-via-go"></a>

### 通过 Go 安装（Installing via Go）

照常使用 Go 安装：

```sh
# 安装带 base 驱动集的最新版 usql
$ go install github.com/xo/usql@latest

# 或者，安装带 most 驱动集的 usql（构建标签的说明见下文）
$ go install -tags most github.com/xo/usql@latest
```

构建标签的详细信息[见下文](#building)。

<a id="installing-via-docker"></a>

### 通过 Docker 安装（Installing via Docker）

`usql` 团队维护了一个[官方容器镜像（`docker.io/usql/usql`）][docker-hub]，
可以配合 Docker、Podman 或其他容器运行时使用。

[docker-hub]: https://hub.docker.com/r/usql/usql

使用 Docker、Podman 或其他容器运行时安装 `usql`：

```sh
# 启动交互式 shell，并将 $PWD/data 目录挂载为容器内的卷
$ docker run --rm -it --volume $(pwd)/data:/data docker.io/usql/usql:latest sqlite3://data/test.db
Trying to pull docker.io/usql/usql:latest...
Getting image source signatures
Copying blob af48168d69d8 done   |
Copying blob efc2b5ad9eec skipped: already exists
Copying config 917ceb411d done   |
Writing manifest to image destination
Connected with driver sqlite3 (SQLite3 3.45.1)
Type "help" for help.

sq:data/test.db=> \q

# 在本地运行 postgres
$ docker run --detach --rm --name=postgres --publish=5432:5432 --env=POSTGRES_PASSWORD=P4ssw0rd docker.io/usql/postgres

# 连接到本地 postgres 实例
$ docker run --rm --network host -it docker.io/usql/usql:latest postgres://postgres:P4ssw0rd@localhost
Connected with driver postgres (PostgreSQL 16.3 (Debian 16.3-1.pgdg120+1))
Type "help" for help.

pg:postgres@localhost=> \q

# 运行指定版本的 usql
$ docker run --rm -it docker.io/usql/usql:0.19.3
```
<a id="building"></a>

## 构建（Building）

使用 `go build` 或 `go install` 开箱即用地构建 `usql` 时，默认只会包含
PostgreSQL、MySQL、SQLite3、Microsoft SQL Server、Oracle、CSVQ 这些
[`base` 驱动][databases]：

```sh
# 构建/安装 base 驱动集（PostgreSQL、MySQL、SQLite3、Microsoft SQL Server、
# Oracle、CSVQ）
$ go install github.com/xo/usql@main
```

其他数据库可以通过指定对应[数据库驱动][databases]的构建标签来启用：

```sh
# 构建/安装 base、Avatica 和 ODBC 驱动
$ go install -tags 'avatica odbc' github.com/xo/usql@main
```

每个构建标签 `<driver>` 都有一个对应的 `no_<driver>` 标签用于禁用该驱动：

```sh
# 构建/安装 most 驱动，但排除 Avatica、Couchbase 和 PostgreSQL
$ go install -tags 'most no_avatica no_couchbase no_postgres' github.com/xo/usql@main
```

本 fork 还提供了配合 [Task](https://github.com/go-task/task) 运行器使用的
[`Taskfile.yaml`](Taskfile.yaml)，封装了常用构建：`task build`（无 CGO 的测试
驱动集）、`task build:chart`（无 CGO 并包含 `\chart` 的 ECharts 引擎）、
`task build:cgo`（启用 CGO，支持终端图表输出）。

<a id="building-without-cgo"></a>

#### 无 CGO 构建（Building Without CGO）

无 CGO（`CGO_ENABLED=0`）构建会去掉所有对 C 库的绑定：默认的 SQLite3 驱动
（mattn/go-sqlite3）、DuckDB、ODBC、Oracle 的 `godror` 驱动，以及 resvg 终端
图表渲染器都会被移除。默认 SQLite3 驱动会由纯 Go 的 ModernC 转译版本
（`moderncsqlite`）替代；`sqlite3` 协议及其别名会自动路由到它：

```sh
# base 驱动的无 CGO 构建（SQLite3 使用 ModernC，无 resvg）
$ CGO_ENABLED=0 go build -tags 'moderncsqlite no_sqlite3' .

# most 驱动的无 CGO 构建（包含 OceanBase MySQL/oboracle、openGauss）
$ CGO_ENABLED=0 go build -tags 'most no_duckdb no_odbc no_godror no_sqlite3 moderncsqlite' .
```

无 CGO 构建的注意事项：

- 命名连接的密码始终保存在 AES-256-GCM 加密的 `secrets.enc` 文件中，与
  CGO 无关 —— 不涉及任何 OS 密钥环（见 [`\conns` 一节](#managing-named-connections-conns)）；
  只有从 OS 密钥环一次性迁移密码的 `\conns migrate` 需要 CGO 构建。
- `\chart` 需要 `chart` 构建标签（它会内嵌 goja JS 引擎和 echarts.min.js）。
  带 `-tags chart` 的无 CGO 构建支持 `\chart ... file=` SVG 导出；终端图片
  输出还需要 cgo 的 resvg 绑定。

<a id="database-support"></a>

## 数据库支持（Database Support）

`usql` 可以工作于 [`github.com/xo/dburl`][dburl] 支持的所有 Go 标准库兼容的
SQL 驱动。

`usql` 当前构建所包含的驱动可以用 [`\drivers` 命令][commands]查看：

```sh
$ cd $GOPATH/src/github.com/xo/usql

# 排除 base 驱动，加入 cassandra 和 moderncsqlite 构建
$ go build -tags 'no_postgres no_oracle no_sqlserver no_sqlite3 cassandra moderncsqlite'

# 显示构建包含的驱动
$ ./usql -c '\drivers'
Available Drivers:
  cql [ca, scy, scylla, datastax, cassandra]
  memsql (mysql) [me]
  moderncsqlite [mq, sq, file, sqlite, sqlite3, modernsqlite]
  mysql [my, maria, aurora, mariadb, percona]
  tidb (mysql) [ti]
  vitess (mysql) [vt]
```

上面的输出表示 `usql` 仅以 `mysql`、`cassandra`（即 `cql`）和
`moderncsqlite` 驱动构建。输出反映了 `usql` 可用驱动的信息，具体包括内部
驱动名、主 URL 协议（scheme）、驱动的可用协议别名（显示在 `[...]` 中），以及
线路兼容驱动所复用的真实底层驱动（显示在 `(...)` 中）。

<a id="supported-database-schemes-and-aliases"></a>

### 支持的数据库协议与别名（Supported Database Schemes and Aliases）

下表列出 `usql` 支持的 [Go SQL 驱动][go-sql]、对应的数据库名、协议/构建标签，
以及协议别名：

对于本 fork 新增的数据库 —— **OceanBase（MySQL 兼容）**、**OceanBase
（Oracle 兼容）** 和 **openGauss** —— 参见 `docs/CONNECTING.md` 中的
[连接示例](docs/CONNECTING.md)。

<!-- 驱动表由 gen.go 生成，与 README.md 保持同步 -->

| 数据库                       | 协议 / 标签     | 协议别名                                        | 驱动包 / 备注                                                               |
| ---------------------------- | --------------- | ----------------------------------------------- | --------------------------------------------------------------------------- |
| PostgreSQL                   | `postgres`      | `pg`, `pgsql`, `postgresql`                     | [github.com/lib/pq][d-postgres]                                             |
| MySQL                        | `mysql`         | `my`, `maria`, `aurora`, `mariadb`, `percona`   | [github.com/go-sql-driver/mysql][d-mysql]                                   |
| Microsoft SQL Server         | `sqlserver`     | `ms`, `mssql`, `azuresql`                       | [github.com/microsoft/go-mssqldb][d-sqlserver]                              |
| Oracle Database              | `oracle`        | `or`, `ora`, `oci`, `oci8`, `odpi`, `odpi-c`    | [github.com/sijms/go-ora/v2][d-oracle]                                      |
| SQLite3                      | `sqlite3`       | `sq`, `sqlite`, `file`                          | [github.com/mattn/go-sqlite3][d-sqlite3] <sup>[†][f-cgo]</sup>              |
| ClickHouse                   | `clickhouse`    | `ch`                                            | [github.com/ClickHouse/clickhouse-go/v2][d-clickhouse]                      |
| CSVQ                         | `csvq`          | `cs`, `csv`, `tsv`, `json`                      | [github.com/mithrandie/csvq-driver][d-csvq]                                 |
|                              |                 |                                                 |                                                                             |
| 阿里云 MaxCompute            | `maxcompute`    | `mc`                                            | [sqlflow.org/gomaxcompute][d-maxcompute]                                    |
| 阿里云 Tablestore            | `ots`           | `ot`, `tablestore`                              | [github.com/aliyun/aliyun-tablestore-go-sql-driver][d-ots]                  |
| Apache Avatica               | `avatica`       | `av`, `phoenix`                                 | [github.com/apache/calcite-avatica-go/v5][d-avatica]                        |
| Apache H2                    | `h2`            |                                                 | [github.com/jmrobles/h2go][d-h2]                                            |
| Apache Hive                  | `hive`          | `hi`, `hive2`                                   | [sqlflow.org/gohive][d-hive]                                                |
| Apache Ignite                | `ignite`        | `ig`, `gridgain`                                | [github.com/amsokol/ignite-go-client/sql][d-ignite]                         |
| Apache Impala                | `impala`        | `im`                                            | [github.com/sclgo/impala-go][d-impala]                                      |
| AWS Athena                   | `athena`        | `s3`, `aws`, `awsathena`                        | [github.com/uber/athenadriver/go][d-athena]                                 |
| Azure CosmosDB               | `cosmos`        | `cm`, `gocosmos`                                | [github.com/btnguyen2k/gocosmos][d-cosmos]                                  |
| Cassandra                    | `cassandra`     | `ca`, `scy`, `scylla`, `datastax`, `cql`        | [github.com/MichaelS11/go-cql-driver][d-cassandra]                          |
| ChaiSQL                      | `chai`          | `ci`, `genji`, `chaisql`                        | [github.com/chaisql/chai][d-chai]                                           |
| Couchbase                    | `couchbase`     | `n1`, `n1ql`                                    | [github.com/couchbase/go_n1ql][d-couchbase]                                 |
| Cznic QL                     | `ql`            | `cznic`, `cznicql`                              | [modernc.org/ql][d-ql]                                                      |
| Databend                     | `databend`      | `dd`, `bend`                                    | [github.com/datafuselabs/databend-go][d-databend]                           |
| Databricks                   | `databricks`    | `br`, `brick`, `bricks`, `databrick`            | [github.com/databricks/databricks-sql-go][d-databricks]                     |
| DuckDB                       | `duckdb`        | `dk`, `ddb`, `duck`, `file`                     | [github.com/duckdb/duckdb-go/v2][d-duckdb] <sup>[†][f-cgo]</sup>            |
| DynamoDb                     | `dynamodb`      | `dy`, `dyn`, `dynamo`, `dynamodb`               | [github.com/btnguyen2k/godynamo][d-dynamodb]                                |
| Exasol                       | `exasol`        | `ex`, `exa`                                     | [github.com/exasol/exasol-driver-go][d-exasol]                              |
| Firebird                     | `firebird`      | `fb`, `firebirdsql`                             | [github.com/nakagami/firebirdsql][d-firebird]                               |
| FlightSQL                    | `flightsql`     | `fl`, `flight`                                  | [github.com/apache/arrow/go/v17/arrow/flight/flightsql/driver][d-flightsql] |
| Google BigQuery              | `bigquery`      | `bq`                                            | [gorm.io/driver/bigquery/driver][d-bigquery]                                |
| Google Spanner               | `spanner`       | `sp`                                            | [github.com/googleapis/go-sql-spanner][d-spanner]                           |
| Microsoft ADODB              | `adodb`         | `ad`, `ado`                                     | [github.com/mattn/go-adodb][d-adodb]                                        |
| ModernC SQLite3              | `moderncsqlite` | `mq`, `modernsqlite`                            | [modernc.org/sqlite][d-moderncsqlite]                                       |
| MySQL MyMySQL                | `mymysql`       | `zm`, `mymy`                                    | [github.com/ziutek/mymysql/godrv][d-mymysql]                                |
| Netezza                      | `netezza`       | `nz`, `nzgo`                                    | [github.com/IBM/nzgo/v12][d-netezza]                                        |
| OceanBase Oracle             | `oboracle`      |                                                 | [github.com/helingjun/obconnector-go][d-oboracle]                           |
| openGauss                    | `opengauss`     |                                                 | [gitcode.com/opengauss/openGauss-connector-go-pq][d-opengauss]              |
| PostgreSQL PGX               | `pgx`           | `px`                                            | [github.com/jackc/pgx/v5/stdlib][d-pgx]                                     |
| Presto                       | `presto`        | `pr`, `prs`, `prestos`, `prestodb`, `prestodbs` | [github.com/prestodb/presto-go-client/v2][d-presto]                         |
| RamSQL                       | `ramsql`        | `rm`, `ram`                                     | [github.com/proullon/ramsql/driver][d-ramsql]                               |
| SAP ASE                      | `sapase`        | `ax`, `ase`, `tds`                              | [github.com/thda/tds][d-sapase]                                             |
| SAP HANA                     | `saphana`       | `sa`, `sap`, `hana`, `hdb`                      | [github.com/SAP/go-hdb/driver][d-saphana]                                   |
| Snowflake                    | `snowflake`     | `sf`                                            | [github.com/snowflakedb/gosnowflake/v2][d-snowflake]                        |
| Trino                        | `trino`         | `tr`, `trs`, `trinos`                           | [github.com/trinodb/trino-go-client/trino][d-trino]                         |
| Vertica                      | `vertica`       | `ve`                                            | [github.com/vertica/vertica-sql-go][d-vertica]                              |
| VoltDB                       | `voltdb`        | `vo`, `vdb`, `volt`                             | [github.com/VoltDB/voltdb-client-go/voltdbclient][d-voltdb]                 |
| YDB                          | `ydb`           | `yd`, `yds`, `ydbs`                             | [github.com/ydb-platform/ydb-go-sdk/v3][d-ydb]                              |
|                              |                 |                                                 |                                                                             |
| GO DRiver for ORacle         | `godror`        | `gr`                                            | [github.com/godror/godror][d-godror] <sup>[†][f-cgo]</sup>                  |
| ODBC                         | `odbc`          | `od`                                            | [github.com/alexbrainman/odbc][d-odbc] <sup>[†][f-cgo]</sup>                |
|                              |                 |                                                 |                                                                             |
| Amazon Redshift              | `postgres`      | `rs`, `redshift`                                | [github.com/lib/pq][d-postgres] <sup>[‡][f-wire]</sup>                      |
| CockroachDB                  | `postgres`      | `cr`, `cdb`, `crdb`, `cockroach`, `cockroachdb` | [github.com/lib/pq][d-postgres] <sup>[‡][f-wire]</sup>                      |
| OceanBase（MySQL 兼容）      | `mysql`         | `oceanbase`                                     | [github.com/go-sql-driver/mysql][d-mysql] <sup>[‡][f-wire]</sup>            |
| OLE ODBC                     | `adodb`         | `oo`, `ole`, `oleodbc`                          | [github.com/mattn/go-adodb][d-adodb] <sup>[‡][f-wire]</sup>                 |
| SingleStore MemSQL           | `mysql`         | `me`, `memsql`                                  | [github.com/go-sql-driver/mysql][d-mysql] <sup>[‡][f-wire]</sup>            |
| TiDB                         | `mysql`         | `ti`, `tidb`                                    | [github.com/go-sql-driver/mysql][d-mysql] <sup>[‡][f-wire]</sup>            |
| Vitess Database              | `mysql`         | `vt`, `vitess`                                  | [github.com/go-sql-driver/mysql][d-mysql] <sup>[‡][f-wire]</sup>            |
|                              |                 |                                                 |                                                                             |
|                              |                 |                                                 |                                                                             |
|                              |                 |                                                 |                                                                             |
| **无驱动（NO DRIVERS）**     | `no_base`       |                                                 | _不含任何 base 驱动（便于开发）_                                            |
| **多数驱动（MOST DRIVERS）** | `most`          |                                                 | _全部稳定驱动_                                                              |
| **全部驱动（ALL DRIVERS）**  | `all`           |                                                 | _全部驱动，不含 bad 驱动_                                                   |
| **BAD 驱动（BAD DRIVERS）**  | `bad`           |                                                 | _bad 驱动（损坏/不可用的驱动）_                                             |
| **NO &lt;TAG&gt;**           | `no_<tag>`      |                                                 | _排除带 `<tag>` 的驱动_                                                     |

[d-adodb]: https://github.com/mattn/go-adodb
[d-athena]: https://github.com/uber/athenadriver
[d-avatica]: https://github.com/apache/calcite-avatica-go
[d-bigquery]: https://github.com/go-gorm/bigquery
[d-cassandra]: https://github.com/MichaelS11/go-cql-driver
[d-chai]: https://github.com/chaisql/chai
[d-clickhouse]: https://github.com/ClickHouse/clickhouse-go
[d-cosmos]: https://github.com/btnguyen2k/gocosmos
[d-couchbase]: https://github.com/couchbase/go_n1ql
[d-csvq]: https://github.com/mithrandie/csvq-driver
[d-databend]: https://github.com/datafuselabs/databend-go
[d-databricks]: https://github.com/databricks/databricks-sql-go
[d-duckdb]: https://github.com/duckdb/duckdb-go
[d-dynamodb]: https://github.com/btnguyen2k/godynamo
[d-exasol]: https://github.com/exasol/exasol-driver-go
[d-firebird]: https://github.com/nakagami/firebirdsql
[d-flightsql]: https://github.com/apache/arrow/tree/main/go/arrow/flight/flightsql/driver
[d-godror]: https://github.com/godror/godror
[d-h2]: https://github.com/jmrobles/h2go
[d-hive]: https://github.com/sql-machine-learning/gohive
[d-ignite]: https://github.com/amsokol/ignite-go-client
[d-impala]: https://github.com/sclgo/impala-go
[d-maxcompute]: https://github.com/sql-machine-learning/gomaxcompute
[d-moderncsqlite]: https://gitlab.com/cznic/sqlite
[d-mymysql]: https://github.com/ziutek/mymysql
[d-mysql]: https://github.com/go-sql-driver/mysql
[d-netezza]: https://github.com/IBM/nzgo
[d-oboracle]: https://github.com/helingjun/obconnector-go
[d-odbc]: https://github.com/alexbrainman/odbc
[d-opengauss]: https://gitcode.com/opengauss/openGauss-connector-go-pq
[d-oracle]: https://github.com/sijms/go-ora
[d-ots]: https://github.com/aliyun/aliyun-tablestore-go-sql-driver
[d-pgx]: https://github.com/jackc/pgx
[d-postgres]: https://github.com/lib/pq
[d-presto]: https://github.com/prestodb/presto-go-client
[d-ql]: https://gitlab.com/cznic/ql
[d-ramsql]: https://github.com/proullon/ramsql
[d-sapase]: https://github.com/thda/tds
[d-saphana]: https://github.com/SAP/go-hdb
[d-snowflake]: https://github.com/snowflakedb/gosnowflake
[d-spanner]: https://github.com/googleapis/go-sql-spanner
[d-sqlite3]: https://github.com/mattn/go-sqlite3
[d-sqlserver]: https://github.com/microsoft/go-mssqldb
[d-trino]: https://github.com/trinodb/trino-go-client
[d-vertica]: https://github.com/vertica/vertica-sql-go
[d-voltdb]: https://github.com/VoltDB/voltdb-client-go
[d-ydb]: https://github.com/ydb-platform/ydb-go-sdk

<p>
  <i>
    <a id="f-cgo"><sup>†</sup> 需要 CGO</a><br>
    <a id="f-wire"><sup>‡</sup> 线路兼容（参见对应驱动）</a>
  </i>
</p>

上面的任意协议 scheme/别名都可以在通过命令行连接数据库时使用，也可以配合
[`\connect` 和 `\copy` 命令][commands]使用：

```sh
# 连接 vitess 数据库：
$ usql vt://user:pass@host:3306/mydatabase

$ usql
(not connected)=> \c vitess://user:pass@host:3306/mydatabase

$ usql
(not connected)=> \copy csvq://. pg://localhost/ 'select * ....' 'myTable'
```

关于构建 `usql` 使用的 DSN/URL 的更多细节，[见下文连接数据库一节][connecting]。
<a id="using"></a>

## 使用（Using）

[安装][installing]完成后，`usql` 的使用方式大致如下：

```sh
# 连接 postgres 数据库
$ usql postgres://booktest@localhost/booktest

# 连接 oracle 数据库
$ usql oracle://user:pass@host/oracle.sid

# 连接 postgres 数据库并执行 script.sql 中的命令
$ usql pg://localhost/ -f script.sql
```

<a id="command-line-options"></a>

### 命令行选项（Command-line Options）

支持的命令行选项：

```sh
$ usql --help
usql, the universal command-line interface for SQL databases

Usage:
  usql [flags]... [DSN]

Arguments:
  DSN   database url or connection name

Flags:
  -c, --command COMMAND                     run only single command (SQL or internal) and exit
  -f, --file FILE                           execute commands from file and exit
  -w, --no-password                         never prompt for password
  -X, --no-init                             do not execute initialization scripts (aliases: --no-rc --no-psqlrc --no-usqlrc)
  -o, --out FILE                            output file
  -W, --password                            force password prompt (should happen automatically)
      --name string                         save the connected DSN under NAME (default: <scheme>_<user>_<host>_<port>_<dbname>)
  -1, --single-transaction                  execute as a single transaction (if non-interactive)
  -e, --encoding string                     set client encoding for decoding database output (utf-8, gbk, gb2312, gb18030)
  -v, --set NAME=VALUE                      set variable NAME to VALUE (see \set command, aliases: --var --variable)
  -N, --cset NAME=DSN                       set named connection NAME to DSN (see \cset command)
  -P, --pset VAR=ARG                        set printing option VAR to ARG (see \pset command)
  -F, --field-separator FIELD-SEPARATOR     field separator for unaligned and CSV output (default "|" and ",")
  -R, --record-separator RECORD-SEPARATOR   record separator for unaligned and CSV output (default \n)
  -T, --table-attr TABLE-ATTR               set HTML table tag attributes (e.g., width, border)
  -A, --no-align                            unaligned table output mode (default true)
  -H, --html                                HTML table output mode (default true)
  -t, --tuples-only                         print rows only (default true)
  -x, --expanded                            turn on expanded table output (default true)
  -z, --field-separator-zero                set field separator for unaligned and CSV output to zero byte (default true)
  -0, --record-separator-zero               set record separator for unaligned and CSV output to zero byte (default true)
  -J, --json                                JSON output mode (default true)
  -C, --csv                                 CSV output mode (default true)
  -G, --vertical                            vertical output mode (default true)
  -q, --quiet                               run quietly (no messages, only query output) (default true)
      --config string                       config file
  -V, --version                             output version information, then exit
  -?, --help                                show this help, then exit
```

通过命令行传入 DSN 并成功连接后，该连接会被自动保存为命名连接（见
[`\conns` 一节](#managing-named-connections-conns)），默认名为
`<scheme>_<user>_<host>_<port>_<dbname>`，也可用 `--name NAME` 指定名称。
传入 `-e`/`--encoding` 时会把客户端编码一并记录到保存的连接中，之后按名
重连时会自动套用同样的编码。

<a id="connecting-to-databases"></a>

### 连接数据库（Connecting to Databases）

`usql` 通过[解析 URL][dburl] 打开数据库连接，并将得到的连接字符串传给
[对应的数据库驱动][databases]。数据库连接字符串（又称"数据源名称"，即
DSN）遵循与 URL 相同的解析规则，可以通过命令行传给 `usql`，也可以传给
[`\connect`、`\c` 和 `\copy` 命令][commands]。

<a id="managing-named-connections-conns"></a>

#### 管理命名连接（\conns）

[`\conns` 命令][commands]列出命名连接，并在终端上交互式地管理它们：

```sh
(not connected)=> \conns
```

一个带边框的表格会显示每个连接的名称、来源、驱动、用户、主机、端口、
数据库、配置的编码，以及是否存储了密码。菜单行接受以下输入：

| 输入           | 动作                                               |
| -------------- | -------------------------------------------------- |
| `a`            | 通过录入表单添加连接                               |
| `<行号>`       | 通过同一表单编辑该连接                             |
| `c <名称或#>`  | 连接到该命名连接                                   |
| `d <名称或#>`  | 删除该命名连接（需确认）                           |
| `q`            | 退出管理界面                                       |

`\conns` 还支持一步直达连接：`\conns <名称>` 或 `\conns <行号>` 直接打开
对应的存储连接，无需进入管理界面。

表单会依次询问名称（添加时）、驱动（已编译驱动的编号选择器或协议别名）、
用户、主机、端口、数据库、URL 参数和密码。密码输入为掩码显示；编辑时留空
表示保留已存储的密码。在非交互或管道执行时，`\conns` 仅打印表格。所有输入
都经过常规行编辑器，管理界面不定义任何全局快捷键，因此不会与 readline
绑定冲突。

在 [bubbletea 输入引擎][input-engine]（`USQL_INPUT=tui`）下，管理界面以
**全屏模态**方式运行（独占视图，退出后还原）：`<Up>`/`<Down>` 或
`<j>`/`<k>` 高亮行，`<Enter>`/`<行号>` 编辑，`<a>` 添加，`<c>` 连接高亮行，
`<d>` 在 `y`/`<n>` 确认后删除，`<q>`/`<Esc>` 退出。表单用 `<Tab>`/`<Up>`/
`<Down>` 导航（字段内有行内编辑器），在最后一个字段按 `<Enter>` 保存，
`<Esc>` 取消。连接成功会将会话切换到该连接；失败时重新打开管理界面。

已连接时，`\c <名称>` 中的 `<名称>` 若是一个不含 URL 标点符号的纯单词，
则表示重连到当前服务器上的另一个**数据库** —— 这是 psql 风格的数据库切换
方式，对 PostgreSQL 系服务器来说也是唯一的方式，因为线协议在建立连接时就要
求数据库。不带数据库名的 `opengauss://` URL 默认连接内置的 `postgres`
数据库，而不是登录用户自己的同名库（后者通常不存在）。`\conninfo` 在显示
连接字符串时会掩码存储的密码。

usql 管理的连接持久化到 `$HOME/.config/usql/connections.yaml`（或各平台的
对应位置），格式与 [`config.yaml` 的 `connections:`][config] 一致 —— 既支持
组件映射，也支持 DSN 字符串。**密码绝不会写入该文件。** 密码保存在旁边
AES-256-GCM 加密的 `secrets.enc` 文件中，以原子方式写入，权限为 `0600`。
加密密钥来自机器本地的随机密钥文件 `secret.key`（自动创建，同样 `0600`）；
若设置了 `USQL_SECRETS_PASSPHRASE`，则改用由该口令派生的密钥
（PBKDF2-SHA256，每文件独立盐值）—— 模式在文件首次创建时确定。
`USQL_SECRETS_KEYFILE` 可将密钥指向其他位置（如可移动介质）。usql 不再
使用 OS 密钥环，因此不会出现钥匙串授权弹窗；`\conns migrate` 可把早期
版本写入 OS 密钥环的密码迁移出来（并删除密钥环条目），旧的明文兜底文件
`secrets.json` 会被自动导入并改名为 `secrets.json.imported`。存储的密码
只在连接该命名连接时注入，且 `\cset` / `\conns` 的输出会掩码 URL 中内嵌
的密码。定义在 `config.yaml` 中的连接继续可用，并以来源 `config` 只读
列出；两处都定义的同一名称会在启动时报告。

连接也会自动保存：凡是在命令行传入 DSN 并成功建立的连接，都会以默认名
`<scheme>_<user>_<host>_<port>_<dbname>`（例如
`postgres_booktest_localhost_5432_booktest`）记录到 `connections.yaml`，
若指定了 `--name NAME` 则以该名称记录。`-e`/`--encoding` 的值会随连接一同
记录，因此 `usql NAME` 重连时会使用相同的客户端编码。与所有 usql 管理的
连接一样，密码（如有）进入密钥存储，绝不写入 `connections.yaml`。

<a id="database-connection-strings"></a>

#### 数据库连接字符串（Database Connection Strings）

数据库连接字符串形如：

```txt
  driver+transport://user:pass@host/dbname?opt1=a&opt2=b
  driver:/path/to/file
  /path/to/file
  name
```

各组成部分：

| 组成部分                        | 说明                                                                     |
| ------------------------------- | ------------------------------------------------------------------------ |
| `driver`                        | 驱动协议名或协议别名                                                     |
| `transport`                     | `tcp`、`udp`、`unix` 或驱动名 <i>（用于 ODBC 和 ADODB）</i>              |
| `user`                          | 用户名                                                                   |
| `pass`                          | 密码                                                                     |
| `host`                          | 主机名                                                                   |
| `dbname` <sup>[±][f-path]</sup> | 数据库名、实例名，或服务名/SID                                           |
| `?opt1=a&...`                   | 额外的数据库驱动选项（可用选项见对应 SQL 驱动的文档）                    |
| `/path/to/file`                 | 磁盘上的路径                                                             |
| `name`                          | 由 [`\cset`][connection-vars] 或 [`config.yaml`][config] 定义的连接名    |

[f-path]: #f-path "URL Paths for Databases"

<p>
  <i>
    <a id="f-path">
      <sup>±</sup> 某些数据库（如 Microsoft SQL Server 或 Oracle Database）
      支持形如 <code>/instance/dbname</code> 的路径组成部分，其中
      <code>/instance</code> 是可选的服务标识（即 "SID"）或数据库实例
    </a>
  </i>
</p>

<a id="driver-aliases"></a>

#### 驱动别名（Driver Aliases）

`usql` 支持与 [`dburl` 包][dburl]相同的驱动名和别名。每个数据库至少有一个
别名。完整的别名列表见 [`dburl` 的协议文档][dburl-schemes]。

<a id="short-aliases"></a>

##### 短别名（Short Aliases）

所有数据库驱动都有一个两字符短名，通常是驱动名的前两个字母。例如 `postgres`
的 `pg`、`mysql` 的 `my`、`sqlserver` 的 `ms`、`oracle` 的 `or`、`sqlite3`
的 `sq`。

<a id="passing-driver-options"></a>

#### 传递驱动选项（Passing Driver Options）

驱动选项以标准 URL 查询参数的形式指定，即 `?opt1=a&opt2=b`。可用选项请参考
[相关数据库驱动][databases]的文档。

<a id="paths-on-disk"></a>

#### 磁盘路径（Paths on Disk）

如果 URL 不带 `driver:` 协议前缀，`usql` 会检查它是否是磁盘上的路径。若路径
存在，`usql` 会尝试用合适的数据库驱动打开它。

当路径是 Unix Domain Socket 时，`usql` 会尝试用 MySQL 驱动打开。当路径是
目录时，`usql` 会尝试用 PostgreSQL 驱动打开。当路径是普通文件时，`usql` 会
尝试用 SQLite3 或 DuckDB 驱动打开。

<a id="driver-defaults"></a>

#### 驱动默认值（Driver Defaults）

与 URL 一样，URL 中的大多数组成部分都是可选的，很多部分可以省略。`usql`
会尽可能地使用默认值连接：

```sh
# 使用本地 $USER 和 /var/run/postgresql 下的 unix domain socket 连接 postgres
$ usql pg://
```

更多信息见[数据库驱动相关文档][databases]。

<a id="character-encoding"></a>

#### 字符编码（Character Encoding）

`usql` 内部一律以 UTF-8 处理文本。当数据库返回非 UTF-8 字符集的文本时
（例如以 `ENCODING 'GBK'` 创建的 openGauss 库、配置了
`character_set_server=gbk` 的 MySQL 服务器，或以 `NLS_LANG=ZHS16GBK` 访问
的 Oracle 库），需要设置客户端编码，`usql` 才能正确解码数据库输出，中文才能
正常显示、表格边框才能对齐。

可以通过三种方式设置编码：

```sh
# 1. 命令行
$ usql 'mysql://user:pass@host/db?charset=gbk' --encoding gbk

# 2. 会话内
=> \encoding gbk
Client encoding is gbk.

# 3. 命名连接配置（config.yaml 或 connections.yaml）
connections:
  og_gbk:
    protocol: opengauss
    username: gaussdb
    hostname: 127.0.0.1
    port: 5432
    database: postgres
    encoding: gbk   # 对数据库输出做客户端解码
```

命名连接的编码会在 `\c NAME` 连接时套用，也可以通过 [`\conns` 录入表单][commands] 交互式设置。有效编码为 `utf-8`（默认）、`gbk`、`gb2312` 和
`gb18030`。

编码必须与数据库实际发送的字节一致。对 MySQL 系服务器而言，DSN 的
`charset` 参数（控制 `SET NAMES`）必须与 `--encoding`/`\encoding` 一致。
无法按所配置编码解码的值（例如二进制数据）与 `utf-8` 一样以 `\xNN` 转义
显示。

在 Linux 上，控制台输出会转码以匹配终端的字符集，该字符集取自 `LC_ALL`、
`LC_CTYPE` 或 `LANG`（例如 `zh_CN.GBK`、`zh_CN.GB2312`、`zh_CN.GB18030`、
`zh_CN.UTF-8`）。在 `GBK`/`GB2312` locale 下，GBK 无法表示的字符会渲染为
`?`。重定向到文件或管道的输出始终为 UTF-8。

在 Windows 上，usql 启动时将所附着的控制台切换为 UTF-8（代码页 65001），
退出时恢复原始代码页。中文（zh-CN）默认控制台运行在 cp936（GBK），其字符集
没有制表符（box-drawing）字符，会破坏 UTF-8 输出 —— 切换后，表格边框和中文
都能正常渲染。若控制台无法切换（未附着控制台，或 usql 的输出被重定向），
则启用转码兜底：控制台输出转码为 `GetConsoleOutputCP` 隐含的编码
（cp936/GBK、cp54936/GB18030）或 `LC_ALL`、`LC_CTYPE`、`LANG` locale 所隐含
的编码，其中无法表示的字符渲染为 `?`，文件或管道始终接收 UTF-8。

控制台**输入**与输出一同切换为 UTF-8（代码页 65001），因此在经典 readline
引擎和 [bubbletea 输入引擎][input-engine]（`USQL_INPUT=tui`）下都可以直接
在查询、提示符和 [`\conns` 表单][commands]中输入中文，并以 UTF-8 保存到
历史记录。

<a id="connection-examples"></a>

### 连接示例（Connection Examples）

以下是示例连接字符串以及使用 `usql` 连接数据库的其他方式：

```sh
# 连接 postgres 数据库
$ usql pg://user:pass@host/dbname
$ usql pgsql://user:pass@host/dbname
$ usql postgres://user:pass@host:port/dbname
$ usql pg://
$ usql /var/run/postgresql
$ usql pg://user:pass@host/dbname?sslmode=disable # 不使用 SSL 连接

# 连接 mysql 数据库
$ usql my://user:pass@host/dbname
$ usql mysql://user:pass@host:port/dbname
$ usql my://
$ usql /var/run/mysqld/mysqld.sock

# 连接 sqlserver 数据库
$ usql sqlserver://user:pass@host/instancename/dbname
$ usql ms://user:pass@host/dbname
$ usql ms://user:pass@host/instancename/dbname
$ usql mssql://user:pass@host:port/dbname
$ usql ms://

# 使用 Windows 域认证连接 sqlserver 数据库
$ runas /user:ACME\wiley /netonly "usql mssql://host/dbname/"

# 连接 oracle 数据库
$ usql or://user:pass@host/sid
$ usql oracle://user:pass@host:port/sid
$ usql or://

# 连接 cassandra 数据库
$ usql ca://user:pass@host/keyspace
$ usql cassandra://host/keyspace
$ usql cql://host/
$ usql ca://

# 连接磁盘上已存在的 sqlite 数据库
$ usql dbname.sqlite3

# 注意：连接 SQLite 数据库时，如果省略了 "driver://" 或 "driver:" 协议/别名，
# 则该文件必须已经存在于磁盘上。
#
# 如果文件尚不存在，URL 必须带 file:, sq:, sqlite3: 或其他可识别的 sqlite3
# 驱动别名，以强制 usql 在指定路径创建新的空数据库：
$ usql sq://path/to/dbname.sqlite3
$ usql sqlite3://path/to/dbname.sqlite3
$ usql file:/path/to/dbname.sqlite3

# 连接 adodb ole 资源（仅限 windows）
$ usql adodb://Microsoft.Jet.OLEDB.4.0/myfile.mdb
$ usql "adodb://Microsoft.ACE.OLEDB.12.0/?Extended+Properties=\"Text;HDR=NO;FMT=Delimited\""

# 连接 $HOME/.config/usql/config.yaml 中的命名连接
$ cat $HOME/.config/usql/config.yaml
connections:
  my_named_connection: sqlserver://user:pass@localhost/
$ usql my_named_connection

# 使用 ODBC 驱动连接（需要以 odbc 标签构建）
$ cat /etc/odbcinst.ini
[DB2]
Description=DB2 driver
Driver=/opt/db2/clidriver/lib/libdb2.so
FileUsage = 1
DontDLClose = 1

[PostgreSQL ANSI]
Description=PostgreSQL ODBC driver (ANSI version)
Driver=psqlodbca.so
Setup=libodbcpsqlS.so
Debug=0
CommLog=1
UsageCount=1

# 通过上面的 odbc 配置连接 db2、postgres 数据库
$ usql odbc+DB2://user:pass@localhost/dbname
$ usql odbc+PostgreSQL+ANSI://user:pass@localhost/dbname?TraceFile=/path/to/trace.log
```

定义连接名的更多信息见[连接变量一节][connection-vars]。
<a id="executing-queries-and-commands"></a>

### 执行查询和命令（Executing Queries and Commands）

交互式解释器读取查询和[反斜杠元（`\`）命令][commands]，并将查询发送到已
连接的数据库：

```sh
$ usql sqlite://example.sqlite3
Connected with driver sqlite3 (SQLite3 3.17.0)
Type "help" for help.

sq:example.sqlite3=> create table test (test_id int, name string);
CREATE TABLE
sq:example.sqlite3=> insert into test (test_id, name) values (1, 'hello');
INSERT 1
sq:example.sqlite3=> select * from test;
  test_id | name
+---------+-------+
        1 | hello
(1 rows)

sq:example.sqlite3=> select * from test
sq:example.sqlite3-> \p
select * from test
sq:example.sqlite3-> \g
  test_id | name
+---------+-------+
        1 | hello
(1 rows)

sq:example.sqlite3=> \c postgres://booktest@localhost
error: pq: 28P01: password authentication failed for user "booktest"
Enter password:
Connected with driver postgres (PostgreSQL 9.6.6)
pg:booktest@localhost=> select * from authors;
  author_id |      name
+-----------+----------------+
          1 | Unknown Master
          2 | blah
          3 | foobar
(3 rows)

pg:booktest@localhost=>
```

命令可以接受一个或多个参数，参数可以用 `'` 或 `"` 引起来。命令参数[也可以
使用反引号][backticks]。

<a id="backslash-commands"></a>

### 反斜杠命令（Backslash Commands）

`usql` 支持交错使用的反斜杠（`\`）元命令，用于修改 `usql` 解释查询、格式化
输出的方式，并改变最终的交互流程。

```sh
(not connected)=> \c postgres://user:pass@localhost
pg:user@localhost=> select * from my_table \G
```

可用的反斜杠元命令可以通过 `\?` 查看：

```sh
$ usql
Type "help" for help.

(not connected)=> \?
General
  \q                                quit usql
  \quit                             alias for \q
  \copyright                        show usage and distribution terms for usql
  \drivers                          show database drivers available to usql

Help
  \? [commands]                     show help on usql's meta (backslash) commands
  \? options                        show help on usql command-line options
  \? variables                      show help on special usql variables

Connection
  \c DSN or \c NAME                 connect to dsn or named database connection
  \c DRIVER PARAMS...               connect to database with driver and parameters
  \connect                          alias for \c
  \conns                            show named connections, or manage (add/edit/delete/connect) interactively
  \conns NAME|N                     connect directly to a named connection
  \Z                                close (disconnect) database connection
  \disconnect                       alias for \Z
  \password [USER]                  change password for user
  \passwd                           alias for \password
  \conninfo                         display information about the current database connection
  \encoding [ENCODING]              show or set the client encoding used to decode database output
                                    (utf-8, gbk, gb2312, gb18030)

Query Execute
  \g [(OPTIONS)] [FILE] or ;        execute query (and send results to file or |pipe)
  \go                               alias for \g
  \G [(OPTIONS)] [FILE]             as \g, but forces vertical output mode
  \ego                              alias for \G
  \gx [(OPTIONS)] [FILE]            as \g, but forces expanded output mode
  \gexec                            execute query and execute each value of the result
  \gset [PREFIX]                    execute query and store results in usql variables
  \bind [PARAM]...                  set query parameters
  \timing [on|off]                  toggle timing of commands

Query View
  \crosstab [(OPTIONS)] [COLUMNS]   execute query and display results in crosstab
  \crosstabview                     alias for \crosstab
  \xtab                             alias for \crosstab
  \chart CHART [(OPTIONS)]          execute query and display results as a chart
  \watch [(OPTIONS)] [INTERVAL]     execute query every specified interval

Query Buffer
  \alias [NAME [ARG...]]            list SQL aliases, or expand NAME into the input line,
                                    replacing $placeholders with ARGs
  \e [-raw|-exec] [FILE] [LINE]     edit the query buffer, raw (non-interpolated) buffer, the
                                    exec buffer, or a file with external editor
  \edit                             alias for \e
  \p [-raw|-exec]                   show the contents of the query buffer, the raw
                                    (non-interpolated) buffer or the exec buffer
  \print                            alias for \p
  \raw                              alias for \p
  \exec                             alias for \p
  \w [-raw|-exec] FILE              write the contents of the query buffer, raw
                                    (non-interpolated) buffer, or exec buffer to file
  \write                            alias for \w
  \r                                reset (clear) the query buffer
  \reset                            alias for \r

Informational
  \d[S+] [NAME]                     list tables, views, and sequences or describe table, view,
                                    sequence, or index
  \da[S+] [PATTERN]                 list aggregates
  \df[S+] [PATTERN]                 list functions
  \di[S+] [PATTERN]                 list indexes
  \dm[S+] [PATTERN]                 list materialized views
  \dn[S+] [PATTERN]                 list schemas
  \dp[S] [PATTERN]                  list table, view, and sequence access privileges
  \ds[S+] [PATTERN]                 list sequences
  \dt[S+] [PATTERN]                 list tables
  \dv[S+] [PATTERN]                 list views
  \l[+]                             list databases
  \ss[+] [TABLE|QUERY] [k]          show stats for a table or a query

Variables
  \set [NAME [VALUE]]               set usql application variable, or show all usql application
                                    variables if no parameters
  \unset NAME                       unset (delete) usql application variable
  \pset [NAME [VALUE]]              set table print formatting option, or show all print
                                    formatting options if no parameters
  \a                                toggle between unaligned and aligned output mode
  \C [TITLE]                        set table title, or unset if none
  \f [SEPARATOR]                    show or set field separator for unaligned query output
  \H                                toggle HTML output mode
  \T [ATTRIBUTES]                   set HTML <table> tag attributes, or unset if none
  \t [on|off]                       show only rows
  \x [on|off|auto]                  toggle expanded output
  \cset [NAME [URL]]                set named connection, or show all named connections if no
                                    parameters
  \cset NAME DRIVER PARAMS...       set named connection for driver and parameters
  \prompt [-TYPE] VAR [PROMPT]      prompt user to set application variable

Input/Output
  \echo [-n] [MESSAGE]...           write message to standard output (-n for no newline)
  \qecho [-n] [MESSAGE]...          write message to \o output stream (-n for no newline)
  \warn [-n] [MESSAGE]...           write message to standard error (-n for no newline)
  \o [FILE]                         send all query results to file or |pipe
  \out                              alias for \o
  \copy SRC DST QUERY TABLE         copy results of query from source database into table on
                                    destination database
  \copy SRC DST QUERY TABLE(A,...)  copy results of query from source database into table's
                                    columns on destination database

Control/Conditional
  \i FILE                           execute commands from file
  \include                          alias for \i
  \ir FILE                          as \i, but relative to location of current script
  \include_relative                 alias for \ir
  \if EXPR                          begin conditional block
  \elif EXPR                        alternative within current conditional block
  \else                             final alternative within current conditional block
  \endif                            end conditional block

Transaction
  \begin [-read-only [ISOLATION]]   begin transaction, with optional isolation level
  \commit                           commit current transaction
  \rollback                         rollback (abort) current transaction
  \abort                            alias for \rollback

Operating System/Environment
  \! [COMMAND]                      execute command in shell or start interactive shell
  \cd [DIR]                         change the current working directory
  \getenv VARNAME ENVVAR            fetch environment variable
  \setenv NAME [VALUE]              set or unset environment variable
```

传给命令的参数[可以使用反引号][backticks]。

### SQL 别名（SQL Aliases）

本分支新增了 `\alias` 元命令：用户自定义的命名 SQL 片段，保存在
`<configdir>/usql/aliases.yaml`（macOS 为 `~/Library/Application
Support/usql/aliases.yaml`，Linux 为 `~/.config/usql/aliases.yaml`），可用
`USQL_ALIASES` 环境变量覆盖路径：

```yaml
common:
  s1: select * from $tablename limit 20;
postgres:
  sessions: select pid, usename, state, query from pg_stat_activity
            where datname = current_database();
  conns: select count(*) from pg_stat_activity;
mysql:
  sessions: show processlist;
  conns: show full processlist;
```

`common:` 段中的别名随处可用；按驱动名分的段（`postgres:`、`mysql:` 等）中的
别名仅在连接对应驱动时可见，且同名时覆盖 `common` 中的定义。

- `\alias` 列出当前会话可用的别名（含来源与 SQL）。
- `\alias s1` 将别名展开到输入行，并把光标定位到第一个 `$占位符` 处（即
  `$name` 记号；PostgreSQL 风格的数字参数如 `$1` 不视为占位符），改写后回车
  执行。多行模板会折叠到单行。
- `\alias s1 mytable` 还会按位置依次替换占位符；光标停在第一个剩余占位符处。
- 别名支持 Tab 补全；每次执行 `\alias` 都会重新加载别名文件，修改立即生效。

<a id="features-and-compatibility"></a>

## 功能与兼容性（Features and Compatibility）

`usql` 的功能、特性以及与 `psql` 的兼容性概览：

- [配置][config]
- [变量][variables]
- [反引号][backticks]
- [数据库间复制][copying]
- [语法高亮][highlighting]
- [时间格式化][timefmt]
- [上下文补全][completion]
- [主机连接信息](#host-connection-information)
- [密码][usqlpass]
- [运行时配置（RC）文件][usqlrc]

`usql` 项目的目标是支持尽可能多的 `psql` 核心特性，并尽可能与之兼容 ——
[欢迎贡献][contributing]！

<a id="configuration"></a>

#### 配置（Configuration）

在初始化阶段，`usql` 会读取一个标准的 [YAML 配置][yaml]文件
[`config.yaml`](contrib/config.yaml)。在 Windows 上是
`%AppData%/usql/config.yaml`，在 macOS 上是
`$HOME/Library/Application Support/usql/config.yaml`，在 Linux 及其他 Unix
系统上通常是 `$HOME/.config/usql/config.yaml`。

<a id="connections-config"></a>

##### `connections:`

[命名连接 DSN][connecting] 可以以字符串或映射的形式定义在 `connections:`
之下：

```yaml
connections:
  my_couchbase_conn: couchbase://Administrator:P4ssw0rd@localhost
  my_clickhouse_conn: clickhouse://clickhouse:P4ssw0rd@localhost
  my_godror_conn:
    protocol: godror
    username: system
    password: P4ssw0rd
    hostname: localhost
    port: 1521
    database: free
```

定义好的 `connections:` 可以在命令行配合 `\connect`、`\c`、`\copy` 以及
[其他命令][commands]使用：

```sh
$ usql my_godror_conn
Connected with driver godror (Oracle Database 23.0.0.0.0)
Type "help" for help.

gr:system@localhost/free=>
```

<a id="init-config"></a>

##### `init:`

初始化脚本可以以字符串形式定义为 `init:`：

```yaml
init: |
  \echo welcome to the jungle `date`
  \set SYNTAX_HL_STYLE paraiso-dark
  \set PROMPT1 '\033[32m%S%M%/%R%#\033[0m '
```

`init:` 脚本常用于设置[环境变量][variables]或其他配置，并可通过命令行的
`--no-init` / `-X` 标志禁用。脚本会在任何 `-c` / `--command` / `-f` /
`--file` 标志之前、交互式解释器启动之前执行。

<a id="other-options"></a>

##### 其他选项（Other Options）

可用配置项的总览请见 [`contrib/config.yaml`](contrib/config.yaml)。

<a id="variables"></a>

#### 变量（Variables）

`usql` 支持[运行时][runtime-vars]、[连接][connection-vars]和[显示格式化][print-vars]三类变量，分别用 `\set`、`\cset`、`\pset` 管理。

<a id="runtime-variables"></a>

##### 运行时变量（Runtime Variables）

运行时变量由 `\set` 和 `\unset` [命令][commands]管理：

```sh
(not connected)=> \unset FOO
(not connected)=> \set FOO bar
```

运行时变量可以用 `\set` 查看：

```sh
(not connected)=> \set
FOO = 'bar'
```

<a id="variable-interpolation"></a>

###### 变量插值（Variable Interpolation）

当运行时变量 `NAME` 被 `\set` 之后，`:NAME`、`:'NAME'` 和 `:"NAME"` 会被
插值进查询缓冲区：

```sh
pg:booktest@localhost=> \set FOO bar
pg:booktest@localhost=> select * from authors where name = :'FOO';
  author_id | name
+-----------+------+
          7 | bar
(1 rows)
```

当运行时变量以 `:'NAME'` 或 `:"NAME"` 的形式使用时，插值后的值会分别用 `'`
或 `"` 引起来：

```sh
pg:booktest@localhost=> \set TBLNAME authors
pg:booktest@localhost=> \set COLNAME name
pg:booktest@localhost=> \set FOO bar
pg:booktest@localhost=> select * from :TBLNAME where :"COLNAME" = :'FOO'
```

查询缓冲区和插值后的值可以用 `\p` 和 `\print` 查看，原始查询缓冲区可以用
`\raw` 查看：

```sh
pg:booktest@localhost-> \p
select * from authors where "name" = 'bar'
pg:booktest@localhost-> \raw
select * from :TBLNAME where :"COLNAME" = :'FOO'
```

<hr/>

> **注意**
>
> 位于其他字符串内部的变量<b><u>不会</b></u>被插值：

```sh
pg:booktest@localhost=> select ':FOO';
  ?column?
+----------+
  :FOO
(1 rows)

pg:booktest@localhost=> \p
select ':FOO';
```

<hr/>

<a id="connection-variables"></a>

##### 连接变量（Connection Variables）

连接变量与运行时变量类似，由 `\cset` 管理。连接变量可以配合 `\c`、
`\connect`、`\copy` 或[其他命令][commands]使用：

```sh
(not connected)=> \cset my_conn postgres://user:pass@localhost
(not connected)=> \c my_conn
Connected with driver postgres (PostgreSQL 16.2 (Debian 16.2-1.pgdg120+2))
pg:postgres@localhost=>
```

连接变量不会被插值进查询。定义持久连接变量的更多信息见[配置一节][config]。

连接变量可以用 `\cset` 查看：

```sh
(not connected)=> \cset
my_conn = 'postgres://user:pass@localhost'
```

<a id="display-formatting-print-variables"></a>

##### 显示格式化（打印）变量（Display Formatting (Print) Variables）

显示格式化变量可以用 `\pset` 和[其他命令][commands]设置：

```sh
(not connected)=> \pset time Kitchen
Time display is "Kitchen" ("3:04PM").
(not connected)=> \a
Output format is unaligned.
```

显示格式化变量可以用 `\pset` 查看：

```sh
(not connected)=> \pset
time                     Kitchen
```

<a id="other-variables"></a>

##### 其他变量（Other Variables）

运行时行为（例如[启用或禁用语法高亮][highlighting]）可以通过
[`SYNTAX_HL`][highlighting] 之类的特殊变量修改。

`THEME` 切换交互界面的配色主题，作用于表格、提示符、错误信息以及两个输入
引擎（`default`、`warm` 或 `plain`）：

```sh
pg:postgres@=> \set THEME warm
```

默认 `PROMPT1` 会用默认主题的强调色（粗体青色，通过 `%27` 转义机制）包裹
提示符；它不跟随 `THEME` 切换 —— 若使用自定义主题，请显式设置 `PROMPT1`。

使用 `\? variables` [命令][commands]可以查看变量帮助信息以及 `usql` 认识的
特殊变量列表：

```sh
(not connected)=> \? variables
```

<a id="backticks"></a>

#### 反引号（Backticks）

[反斜杠（`\`）元命令][commands]的参数支持反引号：

```sh
(not connected)=> \echo Welcome `echo $USER` -- 'currently:' "(" `date` ")"
Welcome ken -- currently: ( Wed Jun 13 12:10:27 WIB 2018 )
(not connected)=>
```

反引号包裹的参数会按原样传给用户的 `SHELL` 执行，并可以与 `\set` 结合：

```sh
pg:booktest@localhost=> \set MYVAR `date`
pg:booktest@localhost=> \set
MYVAR = 'Wed Jun 13 12:17:11 WIB 2018'
pg:booktest@localhost=> \echo :MYVAR
Wed Jun 13 12:17:11 WIB 2018
pg:booktest@localhost=>
```

<a id="copying-between-databases"></a>

#### 数据库间复制（Copying Between Databases）

`usql` 提供 `\copy` 命令，从源数据库 DSN 读取数据并写入目标数据库 DSN：

```sh
(not connected)=> \cset PGDSN postgres://user:pass@localhost
(not connected)=> \cset MYDSN mysql://user:pass@localhost
(not connected)=> \copy PGDSN MYDSN 'select book_id, author_id from books' 'books(id, author_id)'
```

如上所示，`\copy` 不要求已连接数据库，也不会修改或改变当前打开的数据库
连接或状态。

源数据库和目标数据库可以使用任意有效的 URL 或 DSN 名称：

```sh
(not connected)=> \cset MYDSN mysql://user:pass@localhost
(not connected)=> \copy postgres://user:pass@localhost MYDSN 'select book_id, author_id from books' 'books(id, author_id)'
```

<hr/>

> **注意**
>
> `usql` 的 `\copy` 与 `psql` 的 `\copy` 是两回事，<b><u>行为并不</u></b>
> 相同。

<hr/>

<a id="copy-parameters"></a>

##### 复制参数（Copy Parameters）

`\copy` 命令有两种参数形式：

```txt
\copy SRC DST QUERY TABLE
\copy SRC DST QUERY TABLE(COL1, COL2, ..., COLN)
```

其中：

- `SRC` —— [源数据库 URL][connecting]，`QUERY` 在其上执行
- `DST` —— [目标数据库 URL][connecting]，目标 `TABLE` 位于其上
- `QUERY` —— 在 `SRC` 连接上执行的查询，其结果将被复制到 `TABLE`
- `TABLE` —— 目标表名，后跟可选的形如 `(COL1, COL2, ..., COLN)` 的
  SQL 风格列列表
- `(COL1, COL2, ..., COLN)` —— 目标列名列表，与查询列一一对应

[变量、插值和引号][variables]的常规规则同样适用于 `\copy` 的参数。

<a id="quoting"></a>

###### 引号（Quoting）

`QUERY` 和 `TABLE` 含有空格时**必须**加引号：

```sh
$ usql
(not connected)=> echo :SOURCE_DSN :DESTINATION_DSN
pg://postgres:P4ssw0rd@localhost/ mysql://localhost
(not connected)=> \copy :SOURCE_DSN :DESTINATION_DSN 'select * from mySourceTable' 'myDestination(colA, colB)'
COPY 2
```

<a id="column-counts"></a>

###### 列数（Column Counts）

`QUERY` 返回的列数**必须**与 `TABLE` 表达式定义的列数一致：

```sh
$ usql
(not connected)=> \copy csvq:. sq:test.db 'select * from authors' authors
error: failed to prepare insert query: 2 values for 1 columns
(not connected)=> \copy csvq:. sq:test.db 'select name from authors' authors(name)
COPY 2
```

<a id="datatype-compatibility-and-casting"></a>

###### 数据类型兼容性与转换（Datatype Compatibility and Casting）

`\copy` 不会尝试做任何数据类型转换。

如果 `QUERY` 返回的列类型与 `TABLE` 列所期望的类型不同，可以利用源数据库的
转换/类型转换功能，把列转换成对 `TABLE` 列可用的类型：

```sh
$ usql
(not connected)=> \copy postgres://user:pass@localhost mysql://user:pass@localhost 'SELECT uuid_column::TEXT FROM myPgTable' myMyTable
COPY 1
```

<a id="importing-data-from-csv"></a>

###### 从 CSV 导入数据（Importing Data from CSV）

`\copy` 可以借助 `csvq` 驱动从 CSV（或任何其他数据库！）导入数据：

```sh
$ cat authors.csv
author_id,name
1,Isaac Asimov
2,Stephen King
$ cat books.csv
book_id,author_id,title
1,1,I Robot
2,2,Carrie
3,2,Cujo
$ usql
(not connected)=> -- 设置变量方便后续连接
(not connected)=> \set SOURCE_DSN csvq://.
(not connected)=> \set DESTINATION_DSN sqlite3:booktest.db
(not connected)=> -- 连接目标库并建表
(not connected)=> \c :DESTINATION_DSN
Connected with driver sqlite3 (SQLite3 3.38.5)
(sq:booktest.db)=> create table authors (author_id integer, name text);
CREATE TABLE
(sq:booktest.db)=> create table books (book_id integer not null primary key autoincrement, author_id integer, title text);
CREATE TABLE
(sq:booktest.db)=> -- 复制前先给 books 加一行
(sq:booktest.db)=> insert into books (author_id, title) values (1, 'Foundation');
INSERT 1
(sq:booktest.db)=> -- 断开连接，演示 \copy 会新建数据库连接
(sq:booktest.db)=> \disconnect
(not connected)=> -- 从 SOURCE 复制到 DESTINATION
(not connected)=> \copy :SOURCE_DSN :DESTINATION_DSN 'select * from authors' authors
COPY 2
(not connected)=> \copy :SOURCE_DSN :DESTINATION_DSN 'select author_id, title from books' 'books(author_id, title)'
COPY 3
(not connected)=> \c :DESTINATION_DSN
Connected with driver sqlite3 (SQLite3 3.38.5)
(sq:booktest.db)=> select * from authors;
 author_id |     name
-----------+--------------
         1 | Isaac Asimov
         2 | Stephen King
(2 rows)

sq:booktest.db=> select * from books;
 book_id | author_id |   title
---------+-----------+------------
       1 |         1 | Foundation
       2 |         1 | I Robot
       3 |         2 | Carrie
       4 |         2 | Cujo
(4 rows)
```

<hr/>

> **注意**
>
> 从一个数据库向另一个导入大数据集（> 1GiB）时，建议使用数据库自带的
> 原生客户端和工具。

<hr/>

<a id="reusing-connections-with-copy"></a>

###### 在复制中复用连接（Reusing Connections with Copy）

`\copy` 命令（以及所有 `usql` 命令）[支持变量][variables]。在编写脚本，或
需要在多个源/目标之间执行多次 `\copy` 时，最佳实践是在脚本或
[`$HOME/.usqlrc` RC 脚本][usqlrc]中 `\set` 连接变量。

类似地，可以将密码存入 [`$HOME/.usqlpass` 密码文件][usqlpass]，便于复用
（且避免写进脚本）。

例如：

```sh
$ cat $HOME/.usqlpass
postgres:*:*:*:postgres:P4ssw0rd
godror:*:*:*:system:P4ssw0rd
$ usql
Type "help" for help.

(not connected)=> \set pglocal postgres://postgres@localhost:49153?sslmode=disable
(not connected)=> \set orlocal godror://system@localhost:1521/orasid
(not connected)=> \copy :pglocal :orlocal 'select staff_id, first_name from staff' 'staff(staff_id, first_name)'
COPY 18
```
<a id="syntax-highlighting"></a>

#### 语法高亮（Syntax Highlighting）

交互式查询默认使用 [Chroma][chroma] 进行语法高亮。有若干[变量][variables]
控制语法高亮：

| 变量                    | 默认值                    | 取值              | 说明                                                       |
| ----------------------- | ------------------------- | ----------------- | ---------------------------------------------------------- |
| `SYNTAX_HL`             | `true`                    | `true` 或 `false` | 启用语法高亮                                               |
| `SYNTAX_HL_FORMAT`      | _取决于终端支持_          | formatter 名      | [Chroma formatter 名][chroma-formatter]                    |
| `SYNTAX_HL_OVERRIDE_BG` | `true`                    | `true` 或 `false` | 允许覆盖 chroma 样式的背景色                               |
| `SYNTAX_HL_STYLE`       | `monokai`                 | style 名          | [Chroma style 名][chroma-style]                            |

`SYNTAX_*` 变量是普通的 `usql` 变量，可以 `\set` 和 `\unset`：

```sh
$ usql
(not connected)=> \set SYNTAX_HL_STYLE dracula
(not connected)=> \unset SYNTAX_HL_OVERRIDE_BG
```

<a id="context-completion"></a>

#### 上下文补全（Context Completion）

在交互式 shell 中，按下 `<Tab>` 键即可使用 `usql` 的上下文补全。例如，在
PostgreSQL 数据库上按 `<Tab>` 可以补全 `SELECT` 查询的某些部分：

```sh
$ usql
Connected with driver postgres (PostgreSQL 14.4 (Debian 14.4-1.pgdg110+1))
Type "help" for help.

pg:postgres@=> select * f<Tab>
fetch            from             full outer join
```

又如，连接数据库后补全[反斜杠命令][commands]：

```sh
$ usql my://
Connected with driver mysql (10.8.3-MariaDB-1:10.8.3+maria~jammy)
Type "help" for help.

my:root@=> \g<Tab>
\g     \gexec \gset  \gx
```

并非所有命令、上下文或数据库都支持补全。如果你有兴趣帮助改进 `usql` 的
补全，请参见[下文贡献一节][contributing]。

按 `<Control-C>` 可以取消补全。

在交互式终端上，候选菜单还会**边输入边弹出** —— 类似输入法，提示符下方
会出现一个列表，每次按键都会刷新；它是纯展示的（在你用 `<Tab>`、方向键或
菜单内的 `<Enter>` 接受之前不会插入任何内容）。候选按前缀匹配优先排序，表
候选以 `schema.table` 全限定形式展示，便于区分名称相近的对象：

```sh
pg:postgres@=> select * from pu
   public.film   public.film_view   public
```

补全元数据按会话缓存一分钟；当语句改变了会话作用域时（MySQL 系服务器的
`USE`、PostgreSQL 系服务器的 `SET search_path`、Oracle 系服务器的
`ALTER SESSION SET CURRENT_SCHEMA`），缓存会被丢弃，因此候选列表始终反映
当前数据库/模式。缓慢的 catalog 来源（如 OceanBase）会在后台脱离输入循环
查询，输入永远不会被阻塞。

<a id="input-engine-bubbletea-tui"></a>

#### 输入引擎（Bubbletea TUI）

`usql` 内置两种交互输入引擎。默认是经典 readline 引擎；可选的
[bubbletea][bubbletea] 引擎提供输入法风格的纵向候选菜单和 fish/pgcli 风格
的幽灵文本（ghost text）建议：

```sh
# 按次启用
$ USQL_INPUT=tui usql pg://

# 或在 shell 配置中导出
$ export USQL_INPUT=tui
```

TUI 引擎依赖 raw 模式和 VT 渲染。在无法提供它们的终端上——cygwin pty
（管道 stdin）或 `TERM=dumb`——即使设置了 `USQL_INPUT=tui`，usql 也会自动
回退到 readline 引擎。

TUI 模式下，输入行以 **chroma 语法高亮**回显（与输出使用相同的 `SYNTAX_HL`
样式，包括多行语句上下文），并随输入实时重新高亮。补全候选显示为提示符
下方的**单列菜单**（类似中日韩输入法），完整单词展示、高亮当前选项、可用
`<PgUp>`/`<PgDn>` 滚动、`<Alt>`+`<1-9>` 直接选择；当输入行下方没有空间时，
菜单会像输入法候选窗一样**弹出在上方**：

```sh
pg:postgres@=> select * from pu<Tab>
  1 public.users
▸ 2 public.user_settings
  3 public.ufilters
```

在行尾输入时，最佳匹配（来自历史记录，以及连接后的数据库元数据）会在光标
后显示一条**暗色幽灵建议**，类似 fish 或 pgcli：

```sh
pg:postgres@=> select * from us█ers where ...
```

- `<Right>` 接受整条建议；`<Ctrl>`+`<Right>` 接受一个词
- `<Ctrl>`+`<R>` / `<Ctrl>`+`<S>` 增量历史搜索（反向/正向）；`<Enter>`
  接受匹配，`<Ctrl>`+`<G>` 取消
- 菜单打开时，`<Enter>`/`<Tab>` 接受所选候选，绝不会执行当前行；
  `<Esc>` 关闭菜单
- emacs 编辑：带 kill 环的剪切/粘贴（`<Ctrl>`+`<W>`、`<Ctrl>`+`<K>`、
  `<Ctrl>`+`<U>`、`<Ctrl>`+`<Y>`、`<Alt>`+`<Y>` 循环）、对调
  （`<Ctrl>`+`<T>`、`<Alt>`+`<T>`）以及数字前缀参数
  （`<Alt>`+`<3>` 将下一条命令重复三次）
- `<Ctrl>`+`<L>` 清屏并原位重绘当前行
- `<Ctrl>`+`<C>` 取消当前行，空行上的 `<Ctrl>`+`<D>` 退出

注意：`<Ctrl>`+`<S>` 只在读取一行时被捕获（终端处于 raw 模式）；行间仍保留
经典的流控制含义。

非交互运行（`-c`、`-f`、管道输入、`-o`）始终使用逐行普通输入，不受
`USQL_INPUT` 影响。

<a id="colored-tables"></a>

#### 彩色表格（Colored Tables）

在支持颜色的交互式终端上，以 `\pset linestyle unicode` 渲染的对齐表格会
着色：主题色边框、加粗表头、斑马纹行。着色只插入转义序列，绝不改变表格的
字符布局，因此中日韩文字与 ASCII 混排仍能对齐。着色由以下变量控制：

```sh
pg:postgres@=> \pset table_color auto   # on、off 或 auto（默认）
```

`auto` 时，终端支持即着色；重定向输出（`\o`、`\g file`、`\g |pipe`）永不
着色。

<a id="time-formatting"></a>

#### 时间格式化（Time Formatting）

一些数据库的时间/日期列[支持格式化][go-time]。默认情况下，`usql` 将
时间/日期列格式化为 [RFC3339Nano][go-time]，可以用 `\pset time FORMAT`
修改：

```sh
$ usql pg://
Connected with driver postgres (PostgreSQL 13.2 (Debian 13.2-1.pgdg100+1))
Type "help" for help.

pg:postgres@=> \pset
time                     RFC3339Nano
pg:postgres@=> select now();
             now
-----------------------------
 2021-05-01T22:21:44.710385Z
(1 row)

pg:postgres@=> \pset time Kitchen
Time display is "Kitchen" ("3:04PM").
pg:postgres@=> select now();
   now
---------
 10:22PM
(1 row)

pg:postgres@=>
```

`usql` 的时间格式支持任何 [Go 支持的时间格式][go-time]，也可以是任何标准
Go 常量名，例如上面的 `Kitchen`。可用时间常量[见下文](#time-constants)。

<a id="time-constants"></a>

##### 时间常量（Time Constants）

以下是 `usql` 中可用的时间常量名、对应的时间格式值和示例输出：

| 常量        |                                格式 |                             显示 <sup>[↓][f-ts]</sup> |
| ----------- | ------------------------------------: | ----------------------------------: |
| ANSIC       |            `Mon Jan _2 15:04:05 2006` |          `Wed Aug  3 20:12:48 2022` |
| UnixDate    |        `Mon Jan _2 15:04:05 MST 2006` |      `Wed Aug  3 20:12:48 UTC 2022` |
| RubyDate    |      `Mon Jan 02 15:04:05 -0700 2006` |    `Wed Aug 03 20:12:48 +0000 2022` |
| RFC822      |                 `02 Jan 06 15:04 MST` |               `03 Aug 22 20:12 UTC` |
| RFC822Z     |               `02 Jan 06 15:04 -0700` |             `03 Aug 22 20:12 +0000` |
| RFC850      |      `Monday, 02-Jan-06 15:04:05 MST` | `Wednesday, 03-Aug-22 20:12:48 UTC` |
| RFC1123     |       `Mon, 02 Jan 2006 15:04:05 MST` |     `Wed, 03 Aug 2022 20:12:48 UTC` |
| RFC1123Z    |     `Mon, 02 Jan 2006 15:04:05 -0700` |     `Wed, 03 Aug 2022 20:12:48 +0000` |
| RFC3339     |           `2006-01-02T15:04:05Z07:00` |              `2022-08-03T20:12:48Z` |
| RFC3339Nano | `2006-01-02T15:04:05.999999999Z07:00` |       `2022-08-03T20:12:48.693257Z` |
| Kitchen     |                              `3:04PM` |                            `8:12PM` |
| Stamp       |                     `Jan _2 15:04:05` |                   `Aug  3 20:12:48` |
| StampMilli  |                 `Jan _2 15:04:05.000` |               `Aug  3 20:12:48.693` |
| StampMicro  |              `Jan _2 15:04:05.000000` |             `Aug  3 20:12:48.693257` |
| StampNano   |           `Jan _2 15:04:05.000000000` |           `Aug  3 20:12:48.693257000` |

[f-ts]: #f-ts "Timestamp Value"

<p>
  <i>
    <a id="f-ts"><sup>↓</sup> 由时间戳 <code>2022-08-03T20:12:48.693257Z</code> 生成</a><br>
  </i>
</p>

<a id="host-connection-information"></a>

#### 主机连接信息（Host Connection Information）

默认情况下，`usql` 在连接数据库时显示连接信息。这可能会对某些数据库或
连接造成问题。可以通过将系统环境变量 `USQL_SHOW_HOST_INFORMATION` 设为
`false` 来禁用：

```sh
$ export USQL_SHOW_HOST_INFORMATION=false
$ usql pg://booktest@localhost
Type "help" for help.

pg:booktest@=>
```

`SHOW_HOST_INFORMATION` 是标准的 [`usql` 变量][variables]，可以 `\set` 或
`\unset`，也可以通过命令行的 `-v` 或 `--set` 传入：

```sh
$ usql --set SHOW_HOST_INFORMATION=false pg://
Type "help" for help.

pg:booktest@=> \set SHOW_HOST_INFORMATION true
pg:booktest@=> \connect pg://
Connected with driver postgres (PostgreSQL 9.6.9)
pg:booktest@=>
```

<a id="row-limiting"></a>

#### 行数限制（Row Limiting）

默认情况下，`usql` 将交互式的、无过滤条件的 `SELECT` 查询限制在 100 行，
避免误操作的全表查询刷屏。该限制以所连数据库自身的语法追加 —— `LIMIT n`
（PostgreSQL、MySQL、SQLite、openGauss、MySQL 模式的 OceanBase、
ClickHouse 等）、`FETCH FIRST n ROWS ONLY`（Oracle 12c+、Oracle 模式的
OceanBase）或 `SELECT TOP (n)`（SQL Server、SAP ASE）—— 且仅当语句没有
`WHERE` 子句、没有既有的行数限制（`LIMIT`、`OFFSET`、`FETCH`、`TOP`、
`ROWNUM`）、没有集合运算（`UNION` 等）、也没有 `FOR UPDATE` 锁子句时才
生效。

非交互执行的查询（`-c`、`-f`、管道输入）永远不会被改写。

限制由 `ROWLIMIT` [变量][variables]控制（默认 `100`；`0` 表示禁用），可通过
`USQL_ROWLIMIT` 环境变量、`-v`/`--set` 或 `\set` 设置：

```sh
$ USQL_ROWLIMIT=25 usql pg://
pg:=> \set ROWLIMIT 0        # 禁用
pg:=> \set ROWLIMIT 500      # 提高到 500
```

<a id="terminal-graphics"></a>

#### 终端图形（Terminal Graphics）

`usql` 借助 [`github.com/kenshaw/rasterm` 包][rasterm]，支持
[Kitty][kitty-graphics]、[iTerm][iterm-graphics] 和 [Sixel][sixel-graphics]
协议的终端图形。终端图形仅在交互式 shell 中可用。

<a id="detection-and-support"></a>

##### 检测与支持（Detection and Support）

`usql` 会利用 `USQL_TERM_GRAPHICS`、`TERM_GRAPHICS` 以及各终端特有的环境
变量来检测终端图形支持是否可用。

支持可用时，交互会话开始时会显示 logo：

<div style="padding-left: 20px;">
  <img src="https://raw.githubusercontent.com/xo/usql-logo/main/usql-interactive.png" height="120">
</div>

<a id="charts-and-graphs"></a>

##### 图表（Charts and Graphs）

[`\chart` 命令][chart-command]可以在终端中直接显示图表：

<div style="padding-left: 20px;">
  <img src="https://raw.githubusercontent.com/xo/usql-logo/main/chart-example.png" height="120">
</div>

详情见 [`\chart` 元命令一节][chart-command]。

<a id="enablingdisabling-terminal-graphics"></a>

##### 启用/禁用终端图形（Enabling/Disabling Terminal Graphics）

终端图形可以通过设置 `USQL_TERM_GRAPHICS` 或 `TERM_GRAPHICS` 环境变量来
强制启用或禁用：

```sh
# 禁用
$ USQL_TERM_GRAPHICS=none usql

# 强制 iterm 图形
$ TERM_GRAPHICS=iterm usql
```

| 变量            | 默认值  | 取值                                  | 说明               |
| --------------- | ------- | ------------------------------------- | ------------------ |
| `TERM_GRAPHICS` | ``      | ``, `kitty`, `iterm`, `sixel`, `none` | 启用/禁用终端图形  |

<a id="terminals-with-graphics-support"></a>

##### 支持图形的终端（Terminals with Graphics Support）

以下终端已使用 `usql` 测试：

- [WezTerm][wezterm] 是一款跨平台终端（Windows、macOS、Linux 等），支持
  [iTerm][iterm-graphics] 图形

- [iTerm2][iterm2] 是 macOS 终端，支持 [iTerm][iterm-graphics] 图形

- [kitty][kitty] 是 Linux、macOS 及多种 BSD 上的终端，支持
  [Kitty][kitty-graphics] 图形

- [foot][foot] 是 Linux（及其他 Wayland 宿主）上的 Wayland 终端，支持
  [Sixel][sixel-graphics] 图形

更多支持 [Sixel][sixel-graphics] 图形的终端参见
[Are We Sixel Yet?][arewesixelyet] 网站的汇总。

<a id="the-chart-command"></a>

#### `\chart` 命令（The `\chart` Command）

[`\chart` 命令][chart-command]将 SQL 查询的结果渲染为图表，底层使用运行在
内嵌 JavaScript 引擎（[goja][goja]）上的 [Apache ECharts][echarts]：

```sh
pg:postgres@=> \chart title="Films per rating" type=bar \
  select rating, count(*) from film group by rating;
```

结果的第一个文本列用作 X 轴（类别）标签，其余每个数值列各成为一个数据
系列。可用选项：

| 选项       | 说明                                                                  |
| ---------- | ---------------------------------------------------------------------- |
| `title`    | 图表标题                                                              |
| `subtitle` | 图表副标题                                                            |
| `size`     | 图表尺寸 `NxN`（宽 x 高，默认 `800x600`）                             |
| `bg`       | 图表背景色                                                            |
| `type`     | 图表类型 —— `bar`（存在类别列时的默认值）或 `line`                    |
| `prec`     | 数值数据的小数精度                                                    |
| `file`     | 将渲染出的 SVG 写入文件，而不是输出到终端                             |
| `help`     | 显示选项摘要                                                          |

<a id="chart-build-requirements"></a>

##### 图表的构建要求（Chart Build Requirements）

`\chart` 由 `chart` 构建标签门控，因为它内嵌了 ECharts JS 包和 JS 引擎
（会给二进制增加约 7MB）。发布构建不含该标签，因此这些二进制中的 `\chart`
会提示如何启用：

```sh
# 以图表支持构建（终端图片输出，需要 CGO 以使用 resvg）
$ go build -tags 'most chart' .

# 无 CGO 构建，仅支持 SVG 文件导出
$ CGO_ENABLED=0 go build -tags 'most chart no_duckdb no_odbc no_godror no_sqlite3 moderncsqlite' .
```

- **CGO 构建**会将渲染出的 SVG 栅格化，并通过 [Kitty][kitty-graphics]、
  [iTerm][iterm-graphics] 或 [Sixel][sixel-graphics] 图形协议直接显示在
  终端中（经 `resvg` 绑定）。
- 带 `chart` 标签的**无 CGO 构建**无法栅格化图片；请使用
  `\chart ... file=chart.svg` 将 SVG 写入文件。
- **不带 `chart` 标签**时，`\chart` 会报错并说明构建标签要求。

<a id="passwords"></a>

#### 密码（Passwords）

`usql` 支持在启动时从用户 `HOME` 目录下的 `.usqlpass` 文件读取数据库密码：

```sh
$ cat $HOME/.usqlpass
# 格式为：
# protocol:host:port:dbname:user:pass
postgres:*:*:*:booktest:booktest
$ usql pg://
Connected with driver postgres (PostgreSQL 9.6.9)
Type "help" for help.

pg:booktest@=>
```

`.usqlpass` 功能不会被移除，但更推荐通过 [`config.yaml` 文件][config]
[定义命名连接][connection-vars]。

<hr/>

> **注意**
>
> `.usqlpass` 文件不能对其他用户可读，权限应相应设置：

```sh
chmod 0600 ~/.usqlpass
```

<hr/>

<a id="runtime-configuration-rc-file"></a>

#### 运行时配置（RC）文件（Runtime Configuration (RC) File）

`usql` 支持执行用户 `HOME` 目录下的 `.usqlrc` 运行时配置（RC）文件：

```sh
$ cat $HOME/.usqlrc
\echo WELCOME TO THE JUNGLE `date`
\set SYNTAX_HL_STYLE paraiso-dark

-- 设置彩色提示符（默认为 "%S%m%/%R%#" ）
\set PROMPT1 "\033[32m%S%m%/%R%#\033[0m"
$ usql
WELCOME TO THE JUNGLE Thu Jun 14 02:36:53 WIB 2018
Type "help" for help.

(not connected)=> \set
SYNTAX_HL_STYLE = 'paraiso-dark'
(not connected)=>
```

`.usqlrc` 在启动时读取，方式与命令行 `-f` / `--file` 传入的文件相同。它常
用于设置启动环境变量和配置。

启动时传入 `-X` 或 `--no-init` 可以临时禁用 RC 文件执行：

```sh
$ usql --no-init pg://
```

`.usqlrc` 功能不会被移除，但更推荐在 [`config.yaml` 文件][config]中设置
`init` 脚本。

<a id="additional-notes"></a>

## 补充说明（Additional Notes）

以下是与 `usql` 相关的补充说明和杂项：

<a id="release-builds"></a>

### 发布构建（Release Builds）

[发布构建][releases]使用 `most` 构建标签以及额外的 [SQLite3 构建标签
（见：`build.sh`）](build.sh)。它们不带 `chart` 构建标签构建（见
[`\chart` 命令](#the-chart-command)），因此使用 `\chart` 时会提示如何启用。

<a id="macos"></a>

### macOS（macOS）

macOS 上推荐的安装方式是[通过 `brew`][via Homebrew]，这与 `sqlite3` 驱动在
macOS 上处理库依赖的方式有关。如果在运行 `usql` 时遇到如下（或类似）错误：

```sh
$ usql
dyld: Library not loaded: /usr/local/opt/icu4c/lib/libicuuc.68.dylib
  Referenced from: /Users/user/.local/bin/usql
  Reason: image not found
Abort trap: 6
```

可以用 `brew` 安装 [`icu4c`](http://site.icu-project.org) 来补齐缺失的库
依赖：

```sh
$ brew install icu4c
Running `brew update --auto-update`...
==> Downloading ...
...

$ usql
(not connected)=>
```

<a id="contributing"></a>

## 参与贡献（Contributing）

`usql` 目前仍处于 WIP 阶段，正朝着 1.0 版本迈进。欢迎高质量的 PR ——
GitHub issue 跟踪器上有一批标记了 `help wanted` 的明确待办！贡献的技术
细节参见 [CONTRIBUTING.md](CONTRIBUTING.md)。

[_今天认领一个 issue，明天提交一个 PR！_][help-wanted]

<a id="related-projects"></a>

## 相关项目（Related Projects）

- [dburl][dburl] —— Go 包，为解析和打开数据库连接 URL 提供标准的 URL 风格
  机制
- [xo][xo] —— Go 命令行工具，从数据库 schema 生成 Go 代码

[dburl]: https://github.com/xo/dburl
[dburl-schemes]: https://github.com/xo/dburl#protocol-schemes-and-aliases
[go-time]: https://pkg.go.dev/time#pkg-constants
[go-sql]: https://pkg.go.dev/database/sql
[homebrew]: https://brew.sh/
[xo]: https://github.com/xo/xo
[xo-tap]: https://github.com/xo/homebrew-xo
[chroma]: https://github.com/alecthomas/chroma
[chroma-formatter]: https://github.com/alecthomas/chroma#formatters
[chroma-style]: https://xyproto.github.io/splash/docs/all.html
[bubbletea]: https://github.com/charmbracelet/bubbletea
[input-engine]: #input-engine-bubbletea-tui "输入引擎（Bubbletea TUI）"
[help-wanted]: https://github.com/xo/usql/issues?q=is:open+is:issue+label:%22help+wanted%22
[aur]: https://aur.archlinux.org/packages/usql
[yay]: https://github.com/Jguer/yay
[arch-makepkg]: https://wiki.archlinux.org/title/makepkg
[backticks]: #backticks "Backticks"
[config]: #configuration "Configuration"
[commands]: #backslash-commands "Backslash Commands"
[completion]: #context-completion "Context Completion"
[connecting]: #connecting-to-databases "Connecting to Databases"
[contributing]: #contributing "Contributing"
[copying]: #copying-between-databases "Copying Between Databases"
[highlighting]: #syntax-highlighting "Syntax Highlighting"
[termgraphics]: #terminal-graphics "Terminal Graphics"
[timefmt]: #time-formatting "Time Formatting"
[usqlpass]: #passwords "Passwords"
[usqlrc]: #runtime-configuration-rc-file "Runtime Configuration File"
[variables]: #variables "Variables"
[runtime-vars]: #runtime-variables "Runtime Variables"
[connection-vars]: #connection-variables "Connection Variables"
[print-vars]: #display-formatting-print-variables "Display Formatting (print) Variables"
[kitty-graphics]: https://sw.kovidgoyal.net/kitty/graphics-protocol.html
[iterm-graphics]: https://iterm2.com/documentation-images.html
[sixel-graphics]: https://saitoha.github.io/libsixel/
[rasterm]: https://github.com/kenshaw/rasterm
[wezterm]: https://wezfurlong.org/wezterm/
[iterm2]: https://iterm2.com
[foot]: https://codeberg.org/dnkl/foot
[kitty]: https://sw.kovidgoyal.net/kitty/
[arewesixelyet]: https://www.arewesixelyet.com
[chart-command]: #the-chart-command "\\chart 元命令"
[echarts]: https://echarts.apache.org "Apache ECharts"
[goja]: https://github.com/dop251/goja "goja ECMAScript 5.1+ implementation in Go"
[yaml]: https://yaml.org
