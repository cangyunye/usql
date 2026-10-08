# 课题简报：补全的权限可见性（OnlyAccessible）

> 状态：**课题完成**（2026-10-04）。Oracle / MySQL / PostgreSQL 三家族 live
> 契约测试全绿（`TestLivePrivVisibility`），单元测试（sqlite fake + 录制驱动）
> 全绿。本文件自包含——新会话无需历史上下文即可接续（边界与残余限制见文末）。
> 编写日期：2026-10-04。前置工作：补全上下文引擎与元数据池
> （见 `docs/topics/completion-conn-contention.md` 与 `drivers/completer/*_live_test.go`）。

## 一句话问题

补全的 schema 层（L1）与对象层（L2）数据源不一致，会把登录用户**无权访问**
的命名空间/对象端上菜单：Oracle 的 L1 来自 `all_users`（列出容器内一切账户，
不管有无授权），于是出现"能补全 `scott.`、里面却一张表都没有"的割裂；
PostgreSQL 的 `pg_catalog` 对所有登录可读，`pg_class` 上的对象查询不做 ACL
过滤；只有 MySQL 的 `information_schema` 天然按授权过滤。

## 语义决策（为什么不是硬编码 owner=登录用户+公共用户）

- 硬编码会把**已授权的 schema**（如 `GRANT SELECT ON scott.emp TO appuser`
  之后的 scott）藏掉，而这恰恰是授权的意义；还要为每个家族枚举"公共角色"。
- 改为从各家族的权限目录视图**推导**，语义自动正确，且保证
  **L1 ⊆ L2 可达**：L1 出现的命名空间在 L2 必然有对象，"空菜单命名空间"这一
  类问题从结构上消失。
- `metadata.Filter` 新增 `OnlyAccessible bool`（`drivers/metadata/metadata.go`）：
  只由补全器路径设置；`\dt`/`\dn`/`\d` 等 listing 元命令不设置，保持 psql 习惯
  （可发现性优先）。

## 各家族实现与实测依据（2026-10-04，本地 PG 15 容器 / MySQL 8.4 / Oracle 26ai Free）

| 家族 | L1 schema 层 | L2 对象层 | 实测证据 |
|---|---|---|---|
| Oracle | **改**：`Schemas(OnlyAccessible)` 改查 `SELECT DISTINCT owner FROM all_objects`（`drivers/metadata/oracle/metadata.go`），并排除 `PUBLIC` | `all_objects`/`all_sequences`/`all_arguments` 本就是权限视图，零改动 | `all_users` 对 appuser 列 33 个账户、`all_objects` DISTINCT owner 只有 5 个可达 owner；`all_objects` 中 owner='PUBLIC' 7819 行 |
| PostgreSQL | infos 的 `information_schema.schemata` **已按权限过滤，无需改**（实测 probe_limited 对仅授 USAGE 的 schema 可见、未授权 schema 隐藏——推翻了"按 owner 成员身份过滤、需要 override"的预期） | **改**：pg `metaReader.Tables`（查 `pg_class`）加 `has_schema_privilege(n.oid,'USAGE') AND (has_table_privilege(...,'SELECT') OR 'INSERT' OR 'UPDATE' OR 'DELETE')` | 未授权 schema 的表对受限用户 `has_table_privilege=false`；授权后 true |
| MySQL | `information_schema.schemata` 服务端已按授权过滤，零改动 | 同左，零改动 | `TestLivePrivVisibility/mysql` 建库不授权 → 不可补全；`GRANT SELECT ON db.*` 后出现 |

- **PUBLIC（Oracle）排除的原因**：`SELECT * FROM public.dual` 实测 ORA-00903
  ——`PUBLIC.x` 限定引用不是合法 Oracle SQL，PUBLIC 不能作为命名空间补全；
  且公共同义词 7819 行会灌满菜单。非限定公共同义词补全维持现状（不提供）。
- PG 的 `information_schema.routines` 对函数默认 PUBLIC EXECUTE 故全员可见
  （函数层在手动输入未授权 schema 名时仍会列出）——psql 同样不过滤，记为边界。

## 补全路径打标（哪些调用设 OnlyAccessible）

设：`loadSchemas`（L1）、`loadSchemaObjects` 的 tables/functions/sequences（L2）、
`loadListTables/Functions/Sequences/Indexes`（`\d` 参数补全）、`querySchemaCands`
（无缓存回退）。
不设：`loadCurrentSchema`（当前 schema 必须总能解析，OceanBase SYS 场景）、
`loadColumns`（L3 的父表已被 L2 审过）。

## 验证

```sh
# 单元（无服务器）
go test ./drivers/metadata/oracle/ ./drivers/metadata/postgres/ ./drivers/postgres/ ./drivers/oracle/... ./drivers/completer/
# live 契约（三家族；gate 账号需可建 schema/user，Oracle 用 system 而非 appuser）
USQL_LIVE_PG='...' USQL_LIVE_MYSQL='...' USQL_LIVE_ORACLE='oracle://system:***@host:port/XEPDB1' \
  go test ./drivers/completer/ -run 'TestLivePrivVisibility|TestLiveCompletionContract|TestLiveMetaPoolReplay' -v
```

`TestLivePrivVisibility` 的三段式契约：建锁定 schema + 表并收回授权 → 以受限
登录建补全器，断言命名空间与限定对象均不可见（菜单稳定化轮询，排除异步加载
假阴性）→ 授权 + `Invalidate()` 后必须出现。

## 残余限制

- Oracle L1 变为 `all_objects` DISTINCT 扫描，大系统上比 `all_users` 重——
  异步加载 + 10 分钟 L1 缓存吸收；不新增视图依赖。
- Oracle 的非限定公共同义词不参与补全（见上）。
- PG 的 routines（函数）层在手动键入未授权 schema 时仍会列出（默认 PUBLIC
  EXECUTE 所致）；sequences 层 `information_schema.sequences` 已按权限过滤。
- L3 列层不重复过滤：能出现在候选里的表，其列总是可见的；手动键入未授权表名
  时列会列出（与 psql 相同）。
- `OnlyAccessible` 对不支持权限检查的读者（sqlite、csvq 等）为 no-op。
