# 课题简报：补全元数据加载与单连接池的争用

> 状态：**课题完成**（2026-10-03）。Phase 0 测量 → Phase 1 决策（方案 A）→
> Phase 2 实现 → Phase 3 文档，全部完成；成功标准 1-4 达成（验证矩阵全绿）。
> 本文件自包含——新会话无需历史上下文即可接续（残余限制见文末）。
> 编写日期：2026-10-03。前置工作：本仓库已完成 Oracle/MySQL/PostgreSQL 补全体验修复
> （见 git log 与 drivers/completer/*_live_test.go）。

## 一句话问题

`drivers.Open` 把 CLI 连接池钉死为单连接（`drivers/drivers.go:191`，为保会话状态：
MySQL `USE` / PG `search_path` / Oracle `CURRENT_SCHEMA`），但补全器的懒加载缓存
（`drivers/completer/cache.go`）从后台 goroutine 发元数据查询——用户长语句与元数据
查询在唯一连接上互相排队。

## 证据（都已核实，不必重新怀疑）

- `drivers/drivers.go:189-192`：pinning 注释明说前提是"CLI 顺序执行语句"——
  本仓库后加的异步补全破坏了这个前提。
- `drivers/completer/cache.go`：加载在 goroutine 中执行，kick 重绘。
- `drivers/completer/bench_live_test.go` 记录了元数据查询的现实时长：OceanBase
  MySQL 冷目录单表列查询 ~10.5s（`drivers/metadata/mysql/metadata.go` 有注释）。
- 各 live 测试（bench/function/contract/pg_live）自行 `sql.Open`、未 pin，
  `drivers/completer/pg_live_test.go:35` 需要 `SetMaxOpenConns(1)` 才能稳定复现
  GUC 依赖行为。

## 复现/测量方法（Phase 0 要做的）

1. 写一个确定性基准：在飞行中的慢语句（`pg_sleep` / 大 SELECT）期间触发 L2/L3
   加载，测加载延迟；再测"加载飞行时用户语句的 Enter→首字节延迟"。
2. 真库：本地已有三库（连接用仓库命名连接 m1/o1/p 或本地环境变量，勿提交凭据），
   live 测试基建见 `USQL_BENCH_DSN` / `USQL_LIVE_MYSQL` / `USQL_LIVE_PG` /
   `USQL_LIVE_ORACLE`（`drivers/completer/*_live_test.go`）。

## 设计选项（Phase 1 spike 的决策对象，需给出对比矩阵）

**A. 元数据专用连接**：为 completer/metadata 读开第二条连接（同样 pin 单连接），
会话状态变化时重放。重放集合 = `scopeRE` 已识别的三类语句
（`drivers/completer/candidates.go:176`：`USE` / `SET search_path` /
`ALTER SESSION SET CURRENT_SCHEMA`）——handler 已有 `completerInvalidate` 钩子
（`handler/handler.go:159`）可复用为重放触发点。注意 lib/pq、go-sql-driver 无
connect 钩子，重放需在 scope 变化时惰性执行。

**B. 保持单连接，接受争用**：加载可取消/降级，语句延迟不解决（大概率不够）。

**C. 全量会话状态跟踪+重放（用户语句也不 pin）**——范围最大，本课题默认排除。

## 约束与非目标

- 不破坏异步补全契约：按键永不阻塞（`cache.go`/`live.go` 设计承诺）。
- 不破坏三库契约测试（`TestLiveCompletionContract`）与延迟水位（按键 <1ms）。
- 不做选项 C；不改用户语句的 pinning（会话一致性靠它）。
- 驱动中立：postgres/mysql/oracle(含 oboracle/godror)/duckdb/sqlite 全部可编译。

## 验证矩阵

- 契约：`USQL_LIVE_MYSQL` / `USQL_LIVE_PG` / `USQL_LIVE_ORACLE`
  （`TestLiveCompletionContract`）。
- 基准：`USQL_BENCH_DSN`（`TestBenchLiveCatalog`，新增"慢语句飞行"场景）。
- 泄漏/大小写：`TestLivePGLikeLeak`、`TestLivePGCasePreservation`。

## 成功标准

1. 长语句飞行期间补全加载仍能落地（或明确降级并记录）；
2. 元数据查询飞行时用户语句延迟不受影响（或 ≤ 明确阈值）；
3. 会话状态（USE/search_path/CURRENT_SCHEMA）对元数据读取保持一致；
4. 上述 live/契约/基准测试全绿。

## 工作方式

先读 `AGENTS.md`；按仓库既有 TDD 节奏（一个行为测试→一个实现）；spike 结论先落
成决策记录再动实现。计划生成的分阶段建议：

- **Phase 0（测量 spike）**：产出 Phase 0 的两个延迟数字 + 一页选项对比矩阵。
  不要跳过测量直接选方案实现。
- **Phase 1（决策门）**：按矩阵选 A/B（C 已排除），选择理由写成短决策记录。
- **Phase 2（TDD 实现）**：按选定方案逐循环推进，真库验证用上表 env 变量。
- **Phase 3（文档）**：把结论与残余限制补进本文件与 `AGENTS.md`。

---

## Phase 0 结果（2026-10-03，已实测）

### 基准

`TestBenchLiveContention`（`drivers/completer/bench_live_test.go`，`USQL_BENCH_DSN` 门控，
逐 DSN 子测试）模拟两个争用方向：

- **load**：补全加载落地延迟——空闲基线 / 2s 慢语句（`pg_sleep`/`SLEEP`/
  `dbms_session.sleep`）飞行 + 单连接 / 同样慢语句 + 第二条元数据专用连接
  （选项 A 的形态，同一 DSN 再开一个同样 pin 单连接的池）。
- **stmt**：用户语句 Enter→首行延迟——空闲基线 / 单个受控 L2 加载
  （qualified `FROM schema.`，占住连接 0.5s 的慢目录替身）/ 该加载跑在第二条连接上。

落地信号与契约测试同源：reader 的 Schemas 调用计数（关键词兜底层永远有候选，
候选计数不可用——这也是 `benchDSN` 冷启动循环的一个已知瑕疵，见下）。

### 两组延迟数字（本地 PostgreSQL 16 / MySQL 8.4 / Oracle 26ai Free，2026-10-03）

| 场景 | postgres | mysql | oracle |
|---|---|---|---|
| 加载：空闲基线 | 6ms | 6ms | 5ms |
| **加载：2s 慢语句飞行（单连接）** | **2.007s** | **2.035s** | **2.004s** |
| 加载：2s 慢语句飞行（双连接） | 6ms | 6ms | 6ms |
| 语句：空闲基线 | 0.6ms | 1.2ms | 1.4ms |
| **语句：0.5s 加载飞行（单连接）** | **502ms** | **508ms** | **504ms** |
| 语句：0.5s 加载飞行（双连接） | ~0 | ~0 | 1ms |

结论：两个方向都被 pinning 确定性阻塞——加载延迟 ≈ 慢语句剩余时长，用户语句延迟 ≈
加载剩余时长。对照 `bench_live_test.go` 已记录的 OceanBase 冷目录单表列查询 ~10.5s：
单连接下用户语句会被一次冷加载卡住 10s 量级；且慢语句飞行期间加载虽仍能落地
（成功标准 1 满足），但延迟随语句时长线性恶化。双连接对照下两个数字都回到基线——
争用是连接数问题，不是查询本身的问题。

### Phase 0 意外产出：informationschema 共享 reader bug（已修复）

写双连接对照时发现 `infos.New`（`drivers/metadata/informationschema/metadata.go`）
在包级初始化时构造**一个** `*InformationSchema`，返回的构造器每次调用都把
`LoggingReader` 重绑定到**最近一次**传入的 db 并返回同一实例——后建的 reader 会
静默劫持先建 reader 的查询目标。单 reader 的生产路径不可见，但任何持有两个 reader
的设计（选项 A 正是如此）都会读错库。回归测试
`TestNewReaderKeepsItsOwnDB`（`drivers/metadata/mysql/metadata_sqlite_test.go`）
先红后绿；修复为每次构造器调用创建全新实例。MySQL 家族（含 OceanBase）走该 reader。

（顺带记录：`benchDSN` 的冷启动循环以"候选非空"为落地信号，会被关键词兜底层
立即满足——其 cold 数字实为关键词回落延迟。本次新增的基准不受影响，用调用计数。）

## Phase 1 决策记录

**决策：采用方案 A——元数据专用连接（同样 pin 单连接）+ 会话状态惰性重放。**
方案 C 维持排除；方案 B 降级为 A 的失败降级路径，不作为主方案。

**理由（对照矩阵，延迟列为上表实测）：**

| 维度 | B. 保持单连接，接受争用 | A. 元数据专用连接 + 重放（选定） |
|---|---|---|
| 成功标准 1：慢语句飞行时加载 | 落地但排队（+2s/2s 语句，随语句时长线性恶化） | 立即落地（6ms ≈ 基线） |
| 成功标准 2：加载飞行时用户语句 | 排队等待（+0.5s/0.5s 加载；OceanBase 冷目录 ~10.5s） | 不受影响（≈基线） |
| 成功标准 3：会话状态一致 | 天然一致（同一连接） | 需重放：重放集合 = scopeRE 三类语句（`candidates.go:176`），触发点复用 handler 的 `completerInvalidate`（`handler/handler.go:159`）；lib/pq、go-sql-driver 无 connect 钩子 → 在 scope 变化时惰性重放原句 |
| 实现范围 | 小，但标准 2 无法达成（B 的"降级/可取消"救不了用户语句延迟） | 中：开第二连接 + 重放 + 失效联动 |
| 驱动中立 | 是 | 是：所有驱动 `sql.Open` 同一 DSN；重放用用户原句，无驱动特定协议 |
| 契约影响 | 若加载可取消，"加载必落地"契约被破坏 | 懒加载契约不变（查询次数与路径不变，只换连接） |
| 失败模式 | 争用即产品缺陷 | 重放失败/遗漏 → 元数据读到错误 scope（静默）→ 必须显式降级：重放失败即废弃元数据连接并回落共享池 |

**边界与残余风险（Phase 2 处理）：**

- 事务内 scope 语句：MySQL `USE` 隐式提交、PG `SET LOCAL search_path` 会被
  scopeRE 匹配但语义是事务级——重放到另一连接不在同一事务。Phase 2 需明确：
  检测事务内（跳过重放并失效）或精确排除 `SET LOCAL`。
- 重放幂等性：直接重放用户原句（不做语义解析）；`USE`/`SET` 均可重复执行。
- 元数据连接同样 pin 1，避免它自己的会话状态被内部并发打散。
- 元数据连接不可用时（断连、权限）必须降级回共享池并记录，绝不阻塞按键。

## Phase 2 实现（2026-10-03，已完成）

全部 TDD（一个行为测试→一个实现）：

- `completer.ScopeReplayable`（`drivers/completer/candidates.go`）：scope 语句中
  可重放的集合——scopeRE 匹配且非事务级（`SET LOCAL` 排除，正则不支持负向先行，
  用独立 `localSetRE` 组合判断）。测试：`TestScopeReplayable`（scope_test.go）。
- `handler.Open`（交互会话）：打开元数据专用池（`drivers.Open`，同 DSN、pin 1；
  物理连接懒建立——连接时仍零元数据查询），补全器经 `setCompleter` 建在其上；
  打开失败降级共享池并告警一行。`\connect` 重入关闭旧池。
- `handler.Execute`：scope 语句在用户池成功后，先在元数据池重放原句，再失效补全
  缓存（顺序保证失效后的加载看到新 scope）。重放失败 → `degradeCompleter`：
  废弃元数据池、补全器重建于语句连接（争用回归，scope 保持正确）。
- `handler.Close`：关闭元数据池。
- 测试：`TestLiveMetaPoolReplay`（`meta_pool_live_test.go`，三库门控）——双池 +
  重放 + 失效后补全解析新 scope，含"重放前 marker 不出现"的阴性对照；
  `TestPTYMetaPool`（pty_scenarios_test.go，PG 门控）——真实二进制下会话持有
  两条连接（application_name 区分），`SET search_path` 重放后补全菜单解析新表。

### 顺带修复（Phase 2 验证时发现的加载路径 bug）

`Columns`/`FunctionColumns`（`drivers/metadata/informationschema/metadata.go`）把
可空统计列（`character_octet_length`、`numeric_scale` 等）直接扫描进 int——引擎对
部分类型返回 NULL，整次加载失败；失败无缓存条目，补全**逐键重试且列候选永不落地**
（MySQL 契约测试的 columns=2、PG 泄漏测试的 Scan error 同根因）。修复为
`sql.NullInt64` 扫描（缺失读作零值）；回归测试
`TestColumnsToleratesNullStatistics`（sqlite 伪造 information_schema）先红后绿。

### 验证结果（2026-10-03，本地 PostgreSQL 16 / MySQL 8.4 / Oracle 26ai Free）

- 基准 `TestBenchLiveContention`：三库通过（上表数字）。
- 契约 `TestLiveCompletionContract`、重放 `TestLiveMetaPoolReplay`、
  泄漏 `TestLivePGLikeLeak`、大小写 `TestLivePGCasePreservation`：三库/PG 全通过。
- 单元套件（completer/metadata/mysql/handler/...）与全部 pty 回归场景通过；
  仅 dockertest（/var/run/docker.sock 缺失）与 odbc CGO 头文件等环境性失败，
  与本课题改动无关、改动前同样失败。

## 残余限制

- 显式事务内执行 session 级 `SET`/`USE` 后回滚：用户侧 scope 还原而元数据连接
  不还原，直到下一个 scope 语句（重放）或 `\refresh`。事务级 `SET LOCAL` 已被
  排除在重放之外。
- 重放失败（权限、连接问题）即降级到共享池直至重连/重开——保守正确，争用回归。
- 元数据池物理连接懒建立：首条连接出现在首次重放或首次补全加载时。
- 元数据池与用户池同 DSN：驱动级 `Open` 钩子（会话初始化）对两者同样生效，
  会话初始化产生的临时状态（如有）需驱动自洽。
