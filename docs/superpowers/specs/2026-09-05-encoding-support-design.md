# 客户端字符编码支持（GBK / GB2312 / GB18030 / UTF-8）设计

日期：2026-09-05
状态：待评审

## 背景与问题

usql 全链路假设 UTF-8（`drivers.ConvertBytes` 直接 `string(buf)`，`tblfmt` 按 UTF-8 解码）。
数据库端使用 GBK 系编码时（如 openGauss `database_encoding=GBK`、MySQL DSN `charset=gbk`、
Oracle `NLS_LANG=ZHS16GBK`），结果集字节流是 GBK，客户端表现为：

1. 中文数据显示为 `\xNN` 十六进制转义或偶发 mojibake（`tblfmt.FormatBytes` 的 invalid-UTF-8 分支）；
2. 终端为 GBK locale（`LANG=zh_CN.GBK` 等）时，即便数据正确，UTF-8 字节输出也乱码。

已确认的事实：
- `tblfmt` 用 `runewidth` 算列宽，CJK 恒为 2 列，解码成 UTF-8 后边框对齐无需额外处理；
- `runewidth` 的 locale 表认识 `gbk`/`gb2312`/`cp936`，**不认识 `gb18030`**（歧义宽度字符会按 1 算）；
- `tblfmt` 对 `ResultSet` 有 `interface{ ColumnTypes() ([]*sql.ColumnType, error) }` 断言
  （mysql/sqlserver 列类型扫描依赖），包装 ResultSet 必须透传该方法；
- `dburl.BuildURL` 只读取已知键，未知键（如 `encoding`）被忽略，可安全存入连接配置。

## 目标

- 数据库返回 GBK/GB2312/GB18030/UTF-8 字节流，usql 均能正确解码并显示；
- 用户**显式指定**解码方式，不做自动检测（用户已知数据库编码）；
- Linux 控制台输出编码按 `LANG`/`LC_ALL`（`zh_CN.GBK`、`zh_CN.GB2312`、`zh_CN.GB18030`、
  `zh_CN.UTF-8`）自动匹配；
- 中文数据表格边框保持对齐。

## 非目标（明确不做）

- 自动检测编码（utf8.Valid 试探）——误判不可靠，用户已否决；
- SQL 脚本文件（`-f`/`\i`）输入转码——给 GBK 库发 GBK 字面量是正确行为；
- 补全候选（completer）、驱动错误消息文本的转码——不经结果集路径，记为后续项；
- Windows/macOS 控制台代码页检测——仅按需求覆盖 Linux locale。

## 设计

数据流：`DB bytes (GBK/UTF-8) --[输入解码 h.enc]--> UTF-8 内部串 --[tblfmt/runewidth 对齐]--> UTF-8 --[输出编码 rline 包装]--> 终端字符集`

### 1. 新包 `charset/`

```go
// charset/charset.go
// ParseEncoding 将名字解析为解码器；nil 表示 UTF-8 直通。
//   utf-8 | utf8 | ""      -> nil
//   gbk | cp936            -> simplifiedchinese.GBK
//   gb2312 | euccn         -> simplifiedchinese.GBK   (GB2312 原生 EUC-CN 是 GBK 子集)
//   gb18030                -> simplifiedchinese.GB18030
//   其他                    -> error（列出合法名字）
func ParseEncoding(name string) (encoding.Encoding, error)

// ToUTF8 用 enc 解码；enc 为 nil 时原样返回。
// 解码出错（非法 GBK 序列）时返回原串，维持 tblfmt 的 \xNN 转义兜底。
func ToUTF8(s string, enc encoding.Encoding) string

// OutputEncoding 从 LC_ALL > LC_CTYPE > LANG 的 '.' 后缀解析终端编码。
//   UTF-8/UTF8/空/C/POSIX  -> nil（不转码）
//   GBK/GB2312/EUC-CN/CP936 -> simplifiedchinese.GBK
//   GB18030                 -> simplifiedchinese.GB18030
//   其他                    -> nil
func OutputEncoding() encoding.Encoding
```

```go
// charset/resultset.go
// NewResultSet 包装 tblfmt.ResultSet：
//   Columns(): 逐个解码列名
//   Scan(dst...): 透传底层 Scan 后解码 dst（*any、*string、*[]byte、
//                 *sql.RawBytes、*sql.NullString 五种形态；RawBytes 重建切片）
//   ColumnTypes(): 透传底层（保住 tblfmt 的接口断言路径）

```

包 `init()`：locale 字符集为 `gb18030` 时设
`runewidth.DefaultCondition.EastAsianWidth = true`（补 runewidth 表缺失）。

### 2. CLI 标志

`run.go` 增加 `--encoding NAME`（短标志 `-e`）：

```sh
usql 'mysql://...?charset=gbk' --encoding gbk
```

启动时应用到 handler；非法名字报错退出。

### 3. Handler 接入（`handler/handler.go`）

- `Handler` 增加 `enc encoding.Encoding`、`encName string` 字段；
  `SetEncoding(name string) error`（解析并存名）、`EncodingName() string`。
- `scan()`：构建 `row` 后逐格 `charset.ToUTF8(x, h.enc)` —— 覆盖 `\gset`/`\gexec`/chart 取数路径。
- `doQuery()`：`resultSet := charset.NewResultSet(tblfmt.ResultSet(rows), h.enc)` —— 覆盖主查询与 `\d` 元数据（列名+数据）。
- `doExecChart()`：`cols` 逐个解码（图表系列名）。
- `Open()`：单个参数命中命名连接时（`env.Vars().GetConn`），若该连接配置了编码则应用（空则保持当前）。

### 4. 命名连接配置

连接条目（component map 形态）增加 `encoding` 键：

```yaml
connections:
  og_gbk:
    protocol: opengauss
    username: gaussdb
    hostname: 127.0.0.1
    port: 5432
    database: postgres
    encoding: gbk        # 服务端返回编码
```

- `env.LoadConns()`：map 条目含非空 `encoding` 时，先取出（从 map 删除，避免进 URL），
  经 `ParseEncoding` 校验后存入会话：新增 `Variables` 字段 + `SetConnEncoding/GetConnEncoding`。
- `\conns` 交互表单（`metacmd/conns.go connsForm`）增加 `encoding` 字段：
  编辑时预填 `env.ConnEncoding(name)`，空=不设；随 components 一并 `SaveConn` 落盘
  （BuildURL 忽略该键，YAML round-trip 保留）。
- `\c NAME`（`Connect` 经 `handler.Open`）自动应用所配编码。

### 5. `\encoding [NAME]` 元命令（对齐 psql）

`metacmd/cmds.go` 新增 `Encoding` 命令，`go generate` 重新生成 `descs.go`：

- 无参：显示当前输入解码方式与控制台输出编码（如 `client encoding is gbk; console encoding is GBK`）；
- 带参：`\encoding gbk` 会话级覆盖 `h.SetEncoding`，下次查询生效。

### 6. 控制台输出转码（`rline/rline.go New()`）

仅当写**终端**（`out == ""`）且 `charset.OutputEncoding() != nil` 时，将 stdout/stderr 包为：

```go
transform.NewWriter(w, encoding.ReplaceUnsupported(enc.NewEncoder()))
```

- `ReplaceUnsupported`：GBK 编不出的字符（emoji 等）替换为 `?` 而非写错误；GB18030 全 Unicode 可编码，等于直通。
- 转码只在终端路径；`-o` 文件、`\o`、`| pipe` 保持 UTF-8。
- readline 提示符经同一 writer，ASCII 原样透传。

### 7. 文档

- README 增加 "Character Encoding" 小节：三种指定方式（`--encoding`、`\encoding`、连接配置）；
  输出按 locale 自动匹配；**MySQL 需同时设 DSN `charset=gbk`**（驱动协商决定线上字节，
  `--encoding` 只负责解码 usql 收到的字节，两者必须一致）。
- `contrib/config.yaml` 示例加 `encoding` 注释行。

## 已知边界

- 极端情况：线上字节实际是 UTF-8 却配了 `--encoding gbk` → 数据被错误解码（用户配置责任，文档写明）。
- 二进制 BLOB（非 UTF-8 字节）配了 GBK 解码会变汉字垃圾；需要看十六进制时 `\encoding utf-8` 即恢复 `\xNN` 显示。
- `\encoding` 是会话级，不写回连接配置。

## 验证计划

1. 单测（`charset/`）：`ParseEncoding` 表驱动；`ToUTF8`（UTF-8 直通、GBK→汉字、非法序列原样）；
   `OutputEncoding` 各 locale；`NewResultSet` 对五种 dst 形态的解码与 `ColumnTypes` 透传。
2. 端到端（真实二进制）：sqlite3 库存 GBK 字节 ——
   - `LANG=zh_CN.UTF-8 usql --encoding gbk sqlite3://...`：输出 `张三` 且边框对齐；
   - `LANG=zh_CN.GBK usql --encoding gbk ...`：输出经 `iconv -f gbk -t utf-8` 还原为 `张三`（证明字节真是 GBK）；
   - 命名连接配置 `encoding: gbk` + `\c og_gbk` 同样生效；`\encoding` 显示正确。
3. 回归：`go build -tags most .`、`go test ./...`、`go generate` 后 `git diff` 为空。
