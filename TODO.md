# TODO — 终端层改造遗留点（第二轮已完成，剩余项见下）

> 上下文：bubbletea+lipgloss 终端层改造 Phase 1-3 及第二轮收尾均已完成（uitheme 主题/
> 表格着色、rline.Completer 解耦、`USQL_INPUT=tui` 输入引擎、GBK 双向、对齐测试矩阵；
> 第二轮补齐了 P1 全部功能项与 P2/P3 大部分项）。默认引擎仍为 readline，默认输出无色
> ——行为零破坏。以下为剩余项，按优先级排列。

## 已完成（第二轮，待提交）

- [x] **TUI 引擎接入 chroma 语法高亮回显** — `rline/tui_hl.go`：`lineView()` 经引擎的
  `outFn`（handler 的 `outputHighlighter`，跨行上下文语义与 readline `Config.Output`
  一致）高亮当前行；按内容缓存 + 80ms debounce（尾随 tick 重算）；`splitStyled`/`lastSGR`
  做 ANSI 感知的光标块覆盖（光标单元重置后重发 SGR 状态，token 颜色不丢）；内容不匹配
  （陈旧缓存/Tab 展开）回退纯文本。PTY 冒烟已验证逐键上色。
- [x] **元数据快照缓存** — `drivers/completer/snapshot.go`：连接建立时后台一次性加载
  可见 tables/functions/sequences/schemas（沿用 reader 的 3s 超时/1000 行限制），
  按键纯内存过滤（仅服务 `OnlyVisible` 且无 Name/Parent 模式的查询，其余回落
  cachedReader→DB）；USE/`search_path` 变更经 `Invalidate()` 重建，`\c` 重连随
  completer 重建。`WithContextCompletion()` 接入确认无误（handler.go 建复器处已启用，
  驱动自有 completer 经 opts 前置组合）。
- [x] **Ctrl-S 正向历史搜索** — `searchFwd` 方向标记 + `tuiHistory.fwdSearch`；顺带修复
  重复 Ctrl-R 不步进的 bug（查询编辑从起点重扫、方向键越过当前匹配）、failed 标记、
  query 多字节退格。bubbletea 读行期间 raw mode 已关 IXON，Ctrl-S 可用；行间仍为流控
  语义（README 已注明）。
- [x] **菜单贴底时向上弹** — 读行启动时 DSR 探测光标行（`cursorRow`，raw 模式 + 重开
  /dev/stdin 拿可设 deadline 的 fd）；下方空间不足时菜单纯内存渲染在输入行上方（终端
  滚动让位），探测失败回退旧的收缩行为。
- [x] **emacs 补齐** — yank-pop（Alt+Y 循环 kill ring）、Ctrl-T/Alt-T 转置、数字前缀
  参数（Alt+数字，作用于移动/删字/按词 kill/转置/yankN）。
- [x] **PROMPT1 主题色** — 默认值经 `%27`（ESC）机制包一层主题 Accent 色（粗体青色），
  无色环境保持原样；`PROMPT1` 显式设置仍可覆盖。
- [x] **`\set THEME` 主题系统** — uitheme 多主题（default/warm/plain）+ `Use()` 运行时
  切换；env.Set 校验并列出可用名。
- [x] **`\conns` bubbletea 模态化** — `metacmd/conns_tui.go`：altscreen 全屏模态
  （列表/删除确认/表单三态、字段编辑器、驱动编号选择），连接动作退出程序后执行（密码
  提示走原生终端），失败重开管理器；readline 模式行为不变。顺带修复文件库连接的 DSN：
  `env/conns.go buildConnURL`（path 无 hostname 时重建 `scheme:///path`）+
  `SaveConnFromURL` 补采 `u.Opaque`（此前自动保存的 sqlite 连接丢失路径不可连）。
- [x] **并发写防护** — `rline/tui.go guardedWriter`：读行会话期间经 IO Stdout/Stderr
  的外部写进入缓冲、会话结束冲刷；bubbletea 渲染走原始终端。completer 的诊断日志改经
  `WithLogger` 走 IO stderr（默认 logger 亦改 os.Stderr）。
- [x] **PTY 冒烟自动化** — `ptysmoke.go`（//go:build ignore）恢复：应答 OSC10/11、
  DSR、DA1、XTVERSION 查询（带轻量光标跟踪，DSR 应答真实行列），按键脚本支持 `\xHH`
  转义与原子转义序列。**必须预置终端能力查询应答**，否则 bubbletea/termenv 吞按键/挂起
  （已内建）。
- [x] **Windows cp936** — `charset/console_windows.go`：`GetConsoleOutputCP`/
  `GetConsoleCP` 检测 cp936/cp54936，输出转码 + 输入解码（`ConsoleInputEncoding`）
  双向；重定向时代码页返回 0 回退 UTF-8。charset 与全程序 GOOS=windows 交叉编译通过。
- [x] **x/text 归位 direct** — go mod tidy 核实无变化（本就为 direct）。

## 已完成（第三轮：用户报障修复）

- [x] **`\conns` 缺失于补全候选**：根治方式是让 `gen.go`（本就解析 cmds.go 生成
  descs.go）顺带生成 `drivers/completer/cmds_gen.go` 的 `backslashCommands`（含
  `dt[S+]` 的全组合变体与别名），completer 不再手维护命令表；新增
  `metacmd/completer_drift_test.go` 防漂移测试（遍历全部注册命令验证可补全）。
  该测试同时揪出约 30 个历史缺失命令（\d、\o、\if/\endif、\quit、\chart 等），
  一并随生成修复。
- [x] **OceanBase Oracle 租户 `V$PARAMETER` 不存在导致 selectables 报错**：
  `orameta.Catalogs` 拆成 v$parameter / dba_db_links 两条尽力而为查询，任一失败
  （如 OBE-00942）降级为返回已获得的部分，不再让补全的 namespaces 查询整体报错；
  新增 `drivers/metadata/oracle/metadata_test.go`（modernc sqlite 内存库模拟缺表
  场景，无需 Docker）覆盖两个降级路径。

## 已完成（第四轮：逐步覆盖 readline）

- [x] **TUI 引擎安全网（非 VT 环境自动回退）** — `rline/tui.go selectEngine`：
  `inputMode()` 的决策核心提为纯函数（env 注入，可测）；cygwin（管道 stdin 无法
  raw mode）与 `TERM=dumb` 下即使显式 `USQL_INPUT=tui` 也强制回退 readline；
  `USQL_INPUT=readline` 显式回退已识别（为默认翻转铺路）。
- [x] **cursorRow 生产化** — DSR 应答解析提为纯函数 `parseCursorRow`（引擎测试覆盖），
  清理探测路径上的 4 处 `PROBE:` 调试残留 stderr 输出。
- [x] **TUI 键位对齐 readline** — 逐键位审计 `operation.go` 键表：补上缺失的
  **Ctrl-L 清屏**（`tea.ClearScreen` 原位重绘，缓冲区/ghost/菜单不动，行为与
  readline 一致）；其余 emacs 键位（Ctrl-B/P/N/A/E/K/U/W/Y/T、Alt-B/D/F/T/Y、
  数字前缀、Ctrl-R/S）此前已覆盖。vi 模式为 readline 死代码（从未接线），见剩余项。
- [x] **非交互输入脱离 readline** — `rline/plain.go plainReader`：管道/`-o`/`-c`/`-f`
  场景不再构建 readline 实例（此前 `rline.New` 无条件 `readline.NewEx`）。逐行读
  （支持超长行、`\r\n`、末行无换行刷新）；`-c/-f` 保持 Next=EOF + 密码不可用语义；
  密码复用 `readPassword`（文件 stdin 走 x/term 关回显）；`-o` 输出文件 closer 链
  保留（`plainReader.closers`）。
- [x] **readline 包裁剪** — 删除 `rline/readline/remote.go`（gohxs 的网络 readline
  特性，474 行，usql 与包内均无引用）。

## 剩余项

- [ ] **默认引擎翻转**：`rline/tui.go` 的 `inputMode()` 目前仅在 `USQL_INPUT=tui` 时启用
  TUI。泡够一个版本后翻转为默认 `tui`（`selectEngine` 已识别 `USQL_INPUT=readline`
  显式回退），并同步 README「Input Engine」章节。
- [ ] **Windows conhost VT 模式冒烟**：cp936 检测/转码已实现并交叉编译通过，但 TUI
  引擎在 conhost 的 VT 模式下的实机冒烟需要 Windows 环境（可参照 ptysmoke 场景）。
- [ ] **Cygwin 路径评估**：cygwin 下已由 `selectEngine` 强制回退 readline（管道 stdin
  无法 raw mode）；仅当有用户反馈时再评估 TUI-on-cygwin 可行性。
- [ ] **vi 模式**（可选）：readline 引擎的 `VimMode` 在 usql 从未接线（死代码）；如需
  vi 编辑需在 TUI 引擎首次实现。
- [ ] **tblfmt 上游问题跟进**（本仓库只做了规避）：
  - CJK locale（EastAsianWidth=true）下 `linestyle=unicode` 直接报
    `invalid line style`（tblfmt encode.go 校验要求边框字形宽度恒 1）——table_color
    因此仅显式 unicode 时生效。
  - border=1 时短行不 pad 到列宽（上游行为，列不齐观感的真正来源）。

## 验证基线（回归用）

- `go build -tags most .` 必须通过（注意 `-buildvcs=false`，沙箱内 git 报 dubious
  ownership；已 `git config --global --add safe.directory` 处理）；`go test ./...`
  仅允许 `drivers`（需 Docker）与 odbc（缺 sql.h）两个既有环境失败。
- 对齐矩阵：`go test ./uitheme/ ./charset/`（覆盖二义宽度字符、emoji GBK 收缩、
  round-trip、流式 painter 一致性）。
- 并发：`go test -race ./rline/ ./drivers/completer/`。
- PTY 冒烟（ptysmoke，记得带 `USQL_INPUT=tui`，注意 keys 脚本里 `:N` 停顿必须独立成步、
  转义写法 `\xHH`/`\e`、箭头键 `\e[A..D`）：
  - 高亮/ghost/菜单：`go run ptysmoke.go -rows 8 -cols 80 -keys 'sel\t,:300,\t' -- ./usql sqlite:///tmp/t.db`
  - Ctrl-R/S 双向：`-keys '\x12,:200,elect,:300,\x12,:200,\x13,:300,\r,...'`
  - `\conns` 模态：添加→选中→连接（见 git log 与 cm8/cm9 场景）。
- 关键文件入口：`rline/tui.go`（引擎+guardedWriter）、`rline/tui_model.go`（交互模型）、
  `rline/tui_hl.go`（高亮缓存/debounce/ANSI 切分）、`uitheme/theme.go`（多主题）、
  `uitheme/table.go`（CellWidth/painter）、`charset/charset.go`（ConsoleEncoder/
  ConsolePreview/ConsoleDecoder）、`charset/console_windows.go`（代码页检测）、
  `drivers/completer/snapshot.go`（快照）、`metacmd/conns_tui.go`（模态）、
  `env/conns.go`（buildConnURL）、`handler/handler.go` doQuery（table_color 门控）。
