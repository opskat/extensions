# Oracle 扩展：转储 SQL 文件与运行 SQL 文件

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-11-19（修订：sqldb 按行写语句的钩子；日期类取值统一显式格式；整库转储的视图取自 ListObjects；修正轮 1–3：国家字符集 UNISTR、标识列提前、SDK 调用结束回收、LOB 单值 16 MB 与宿主内存上限 512 MB）

**Objective:** Oracle 扩展 0.3.0 在 OpsKat 里提供与内置驱动相同的「转储 SQL 文件」与「运行 SQL 文件」：把 Oracle 用户（schema）里的表、视图、序列、函数、过程、包、触发器与数据转储成一个能还原的 `.sql` 文件，并能在任意 Oracle 用户下运行这样的文件（以及 SQL Developer 导出的文件）。

**Hard invariant:** 0.2.0 已有的连接、浏览、查询、编辑、建改删表、导入导出、语句分类行为不变，只有两处有意的变化：日期、时间戳、间隔值在「复制为 INSERT」「导出为 SQL」与表格编辑生成的语句里改为显式格式的转换函数（决定 11）；放不进一条字面量的 BLOB 行在这些地方写成 PL/SQL 块（决定 10）。**[用户决定]** 这套取值写法是 Oracle 共用的一套规则，同样用于筛选（LIKE 之外）与导入写出的 SQL；表设计器的默认值与注释照旧用宿主的通用写法（数字不加引号、国家字符集类型带 N 或写成 `UNISTR`），在 OpsKat 里的结果不变；放不进一条字面量的 CLOB / NCLOB 行同样写成 PL/SQL 块（0.2.0 在这些地方报 ORA-01704）。**[用户决定]** 主仓库只改编进扩展 WASM 的共用 SQL 库 `pkg/dbdriver/sqldb`：新增一个可选的按行写语句钩子（决定 10），加在 opskat#382 的分支上；另有宿主修正（决定 12、13）：整库转储的视图取自 `ListObjects` 的视图列表，不再只认 `ListTables` 里列出的。**[用户决定]** 以及扩展 SDK（`pkg/extsdk`，编进每个 Go 扩展）的一处修正（修正轮 2）：每次调用结束时堆占用较大就回收一次，避免大 LOB 转储之后的垃圾把实例的线性内存撑过宿主的 64 MB 上限、令运行 SQL 文件偶发崩溃。内置驱动行为不变、宿主 ABI 不变。其余只改 extensions 仓库的 `extensions/oracle`、发布工具的宿主依赖与发布元数据。

## 所处位置

opskat issue #368 的后续。主仓库 PR opskat/opskat#382（分支 `feat/sql-dump-and-run`）实现了两项功能的宿主部分，并把扩展驱动的宿主 ABI 升到 1.1：宿主负责读写文件、排序、进度与取消，扩展驱动只提供各自数据库的写法与切分（opskat `docs/specs/2026-11-16-sql-dump-and-run.md` 决定 16 与「扩展驱动」一节）。本轮把 Oracle 扩展接上。

## Problem

标注：**[已核实]** = 对照 extensions `origin/main`（`9afb94c`）与 opskat `feat/sql-dump-and-run`（`ab0117ba`）源码确认；**[用户决定]** = 本轮对话中用户选定。

1. **Oracle 资产上没有这两项功能。** [已核实] Oracle 扩展 0.2.0 声明 `hostABI: "1.0"`（`extensions/oracle/manifest.json`），描述里没有 `dump_sql` / `run_sql_file`；宿主 1.1 对 1.0 扩展不显示这两项。
2. **建表语句带着用户名。** [已核实] 建表语句来自 `DBMS_METADATA.GET_DDL`（`extensions/oracle/ddl.go:43`），带 `"OWNER"."表"` 与存储、表空间子句，原样写进文件只能还原回同一个用户、同一套表空间。
3. **现有切分只认整段文本。** [已核实] `splitOracle`（`extensions/oracle/split.go`）对整段 SQL 切分并分类，运行文件需要按块流式切分、在块之间带状态。
4. **Oracle 的写法差异。** [已核实] 23ai 之前没有 `DROP … IF EXISTS`；不能在会话里关闭外键检查；`GENERATED ALWAYS` 的标识列不能直接插入取值，也没有 `OVERRIDING SYSTEM VALUE`；SQL 里单个字符串字面量最多 4000 字节、`HEXTORAW` 最多 2000 字节；PL/SQL 单元以单独一行的 `/` 结束。

## Actors and user stories

1. 作为**运维用户**，我想把一个 Oracle 用户下的表与程序转储成文件，在另一个库或另一个用户下运行它来还原，包括带大 CLOB / BLOB 的表（单个值不超过 16 MB）。
2. 作为**运维用户**，我想在 Oracle 资产上运行 SQL Developer 导出的 `.sql` 文件，不必再开 SQL*Plus。
3. 作为**维护者**，我想等宿主发布了 ABI 1.1 再上架 0.3.0，旧版宿主的用户继续用 0.2.0。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** 整个 schema 转储包含表、视图、序列、函数、过程、包（包头与包体）、触发器 | Oracle 应用最常见的一套；包已在 0.2.0 里归在「过程」下浏览（`catalog.go:140-163`）。Rejected: 再加同义词与类型——依赖排序更复杂；只做表、视图、序列——不能还原程序 |
| 2 | 转储文件不带用户名，可在任意用户下运行；不写存储、表空间、段属性 | 与宿主决定 17「INSERT 表名不带库名」一致；带表空间的文件在没有同名表空间的库上建不出表。Rejected: 原样写 GET_DDL——只能还原回原用户与原表空间 |
| 3 | 「写入删除语句」勾上时，每条删除写成忽略「对象不存在」的 PL/SQL 块，19c 到 23ai 同一写法 | 19c、21c 没有 `IF EXISTS`；一种写法便于测试。Rejected: 23ai 用 `IF EXISTS`、旧版用 PL/SQL——两套写法 |
| 4 | 不声明能关闭外键检查；外键在全部数据之后用 `ALTER TABLE … ADD CONSTRAINT` 写出 | Oracle 没有会话级开关；按约束逐个 DISABLE 会改变用户的约束状态。Rejected: 导数据前 DISABLE、之后 ENABLE |
| 5 | `GENERATED ALWAYS` 的标识列：建表照原样，导数据前临时改为 `BY DEFAULT`，导完改回 `ALWAYS` 并把下一个值设到不小于已有最大值 | Oracle 没有 OVERRIDING；这样还原后的标识列定义与原库相同。Rejected: 把标识列建成 BY DEFAULT——还原后定义与原库不同 |
| 6 | **[用户决定]** 单个值不超过 16 MB 的 CLOB / NCLOB / BLOB 都能还原（修正轮 3 由「任意大小」改定，见决定 13）：放得进一条字面量的仍是普通 INSERT；放不进的那一行写成 PL/SQL 块，先插入空 LOB 再分段追加。超过 16 MB 的值在读它之前停下，给出指明表、列与大小的错误，不再让扩展崩溃 | Oracle 字面量上限；[已核实] 整值都在扩展实例内存里处理（修正轮 2 规格轴实测：1.5 MB 的 BLOB 行在 64 MB 上限下已崩溃）。Rejected: 跨调用流式传 LOB——要改宿主 ABI、改写转储与导出路径；把上限定在约 1 MB——真实库里的文档、图片常常超过 |
| 7 | **[用户决定]** 运行文件时 SQL*Plus / SQL Developer 的客户端指令：只影响客户端显示的跳过并在日志里记一笔；改变执行的停下 | SQL Developer 导出的文件开头就有 `SET DEFINE OFF`，应当能直接运行。Rejected: 一律停下 |
| 8 | `EXEC` / `EXECUTE` 包成匿名块执行 | 脚本里常见、语义明确。Rejected: 当作不支持停下 |
| 9 | **[用户决定]** 0.3.0 声明 `hostABI: "1.1"`、`minAppVersion` 为第一个含 opskat#382 的宿主版本；开发期依赖钉在 `feat/sql-dump-and-run` 的伪版本、用本地宿主验证；宿主发版后换成正式版本，再合并触发发布 | 旧版宿主装不上 1.1 的扩展，商店里在宿主发版前不能出现 0.3.0。Rejected: 做完就合并上架 |
| 10 | **[用户决定]** 在 `pkg/dbdriver/sqldb` 加一个可选钩子：方言可以为某一行写出整条语句（包括 PL/SQL 块）代替默认的 INSERT；Oracle 用它把放不进字面量的 LOB 行写成「插入空 LOB 再分段 `DBMS_LOB.APPEND`」的块，以 `/` 结束 | [已核实] 行语句的形状由 sqldb 固定为 `INSERT … VALUES …;`（`pkg/dbdriver/sqldb/rows.go:131-135`），方言只能决定单个值的写法；CLOB 可用 `TO_CLOB` 拼接，BLOB 在 SQL 里不能拼接。sqldb 编进扩展的 WASM、不经宿主 ABI。Rejected: CLOB 拼接、大 BLOB 停下报错——不能还原 |
| 11 | **[用户决定]** 日期、时间戳（含时区）、间隔值统一写成显式格式的 `TO_DATE` / `TO_TIMESTAMP` / `TO_TIMESTAMP_TZ` / `TO_DSINTERVAL` / `TO_YMINTERVAL`，转储、复制为 INSERT、导出为 SQL、表格编辑一致 | 文件在 SQL*Plus 等外部工具里导入也不依赖 NLS；同一种写法，不分叉。[已核实] 现在写成依赖会话 NLS 的字符串（`extensions/oracle/write.go:68-92`，NLS 在连接时设定 `connect.go:40`）。Rejected: 保持现有写法——文件离开 OpsKat 可能因 NLS 不同而导入失败 |
| 12 | **[用户决定]** 整库转储的视图由宿主取自 `ListObjects` 的视图列表（opskat#382 上的宿主修正），Oracle 的 `ListTables` 仍只列表 | [已核实] 宿主 `planDump` 只把 `ListTables` 里同时出现在 `ListObjects.Views` 中的名字当作视图（opskat `pkg/dbdriver/dump.go:311-322`），Oracle 的 `ListTables` 只列表（`extensions/oracle/catalog.go:36-44`），整库转储因此没有视图。Rejected: Oracle 的 `ListTables` 也列出视图——树的「表」下会出现视图，违反浏览行为不变 |
| 13 | **[用户决定]** 宿主为扩展驱动（不只 Oracle）：扩展实例的内存上限由 64 MB 提到 512 MB（修正轮 3 实测 16 MB 的值峰值 211–245 MB，256 MB 余量不足，用户改定）；按行读取的游标每页按字节封顶（约 4 MB，至少一行），不再固定 256 行；扩展 SDK 的调用结束回收（修正轮 2）保留。宿主 ABI 的形状不变 | [已核实] 宿主上限 `WithMemoryLimitPages(1024)`（opskat `pkg/extension/runtime.go:152`），Oracle 模块静态映像占 19.6 MB；每页固定 `driverStreamRows = 256` 行（`internal/extreg/extdriver/session.go:57`），30 行 × 256 KB 的一页已让 0.2.0 的整表导出崩溃。Rejected: 只调 Oracle——问题在宿主与 SDK 的共用路径；不设单值上限——Go 的内存不足不可恢复，只能在读值之前拦下 |

## 能力声明

扩展描述新增 `dump_sql` 与 `run_sql_file`，不声明「转储时能关闭外键检查」。在 OpsKat 里的表现与内置驱动相同：入口、对话框、导出选项、进度、取消、出错时目标保持原样、UTF-8 预检、停下与继续、停止、树刷新（见 opskat spec「入口」「转储」「运行 SQL 文件」）；「关闭外键检查」选项在 Oracle 资产上置灰并提示外键放在数据之后。Oracle 的「库」节点是用户（schema），转储整个库即转储这个用户下的对象。

## 转储

**文件内容**（按宿主规定的段落，Oracle 的写法如下）：

- 注释头由宿主写出。不需要会话设置；日期、时间戳、间隔的值用显式格式的转换函数写出，不依赖导入端的 NLS 设置。
- 删除段（勾了「写入删除语句」时）：先删要转储的表上的外键，再按依赖倒序删除触发器、视图、包体、包头、函数、过程、表、序列；每条都是忽略「不存在」错误（ORA-00942、ORA-02289、ORA-04043、ORA-04080 等）的 PL/SQL 块，以 `/` 结束。
- 序列：用户自建的序列（不含标识列自带的序列）写在表之前，起始值不小于源库的当前值，保留增量、上下限、循环与缓存设置。
- 表：建表语句不带用户名、存储、表空间、段属性；含列类型、默认值、非空、主键、唯一约束、CHECK、标识列定义、表与列的注释；不由约束建出的索引单独写出。勾了「导出数据」时每行一条 INSERT（不含虚拟列），值的写法见下；超过字面量上限的 LOB 那一行写成 PL/SQL 块。
- 外键：全部数据之后写出。
- 视图、函数、过程、包：按依赖排序（`ALL_DEPENDENCIES`），被依赖的在前；包头在包体之前；视图写成 `CREATE OR REPLACE` 的完整定义；PL/SQL 单元以 `/` 结束。
- 触发器：全部数据之后写出，保留启用 / 停用状态。
- 标识列：每张表的数据之后立即改回 `ALWAYS` 并对齐下一个值（决定 5），在视图、程序与触发器建出之前，还原后不留下失效（INVALID）的对象（修正轮 1，用户批准）。

**值的写法**：NUMBER 原样；CHAR / VARCHAR2 / NCHAR / NVARCHAR2 用字符串字面量；国家字符集类型含非 ASCII 字符时写成 `UNISTR` 转义（纯 ASCII 仍用 N 前缀），不经数据库字符集转换，非 Unicode 库（如 GBK）上也不丢字符（修正轮 1，用户批准），空串与 NULL 同为 NULL（Oracle 语义）；DATE / TIMESTAMP / TIMESTAMP WITH (LOCAL) TIME ZONE / INTERVAL 用显式格式的转换函数，保留小数秒与时区；RAW 用 `HEXTORAW`；CLOB / NCLOB / BLOB 见决定 6；BINARY_FLOAT / BINARY_DOUBLE 保留精度与特殊值。

**只转储选中的表时**：写这些表本身，以及这些表的列默认值引用的序列（否则建表失败）；没有视图、程序与触发器（与宿主一致）。

## 运行 SQL 文件

- 切分：普通语句以 `;` 结束（发给数据库时去掉末尾的 `;`）；PL/SQL 块与存储程序（`BEGIN`、`DECLARE`、`CREATE [OR REPLACE] FUNCTION / PROCEDURE / PACKAGE [BODY] / TRIGGER / TYPE`）以单独一行的 `/` 或文件末尾结束；认得单行与多行注释、普通引号与 `q'[…]'` 引号；语句跨读取块时正确切分。
- 客户端指令（决定 7）：跳过 `SET`、`PROMPT`、`REM` / `REMARK`、`SPOOL`、`CLEAR`、`COLUMN`、`TTITLE` / `BTITLE`、`SHOW`、单独的 `/`（无语句时）并在日志里记一行；停下（按「不支持的写法」，提示改用 SQL*Plus）的有 `@` / `@@` / `START`、`WHENEVER`、`CONNECT` / `CONN`、`EXIT` / `QUIT`、`HOST` / `!`、`DEFINE` / `UNDEFINE` / `ACCEPT` / `VARIABLE`、带 `&` 替换变量且未 `SET DEFINE OFF` 的语句。
- `EXEC` / `EXECUTE`：包成 `BEGIN … END;` 执行（决定 8）。
- 本扩展转储出来的文件、SQL Developer 的「导出 → 插入语句」文件都要能运行。

## 发布

`manifest.json`：`version` 0.3.0、`hostABI` 1.1、`minAppVersion` 为第一个含 opskat#382 的宿主版本。开发与验证期间 `extensions/oracle/go.mod` 与发布工具 `tools/publish/go.mod` 都依赖 `github.com/opskat/opskat` 的 `feat/sql-dump-and-run` 伪版本（[已核实] 发布工具钉的 v1.15.0 只认 hostABI 1.0，`make index-check` 与发布会拒绝 0.3.0）；opskat 发版后换成正式版本、更新 `minAppVersion`，再合并本轮 PR，由仓库现有的发布流程上架。旧版宿主（ABI 1.0）的用户在商店里仍得到 0.2.0。

## Out of scope

- 同义词、对象类型（TYPE）、物化视图（及其容器表）、数据库链接、权限、作业、分区表的分区定义以外的存储设置。
- 19c 之前的版本（0.2.0 本来就不支持）。
- 把 `&` 替换变量交互式代入。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 转储写法（Go 单元测试，金样文件） | 不带用户名与存储子句的建表、序列、删除段 PL/SQL 块、外键在后、依赖顺序、包头在包体前、`/` 终止、标识列前后处理、各类型的值字面量、大 LOB 的 PL/SQL 块 | `extensions/oracle/ddl_test.go`、`write.go` 的测试 |
| 流式切分（Go 单元测试） | `;` 与 `/`、`q'[…]'`、注释、跨块边界（每种读取大小）、客户端指令的跳过与停下、`EXEC` | `extensions/oracle/split_test.go` |
| 真实库往返（集成测试） | 在 19c（GBK）、21c、23ai 上建一个包含各类对象与大 LOB、各种日期类型、标识列的用户，转储后运行到另一个用户，比对结构与数据；勾删除后覆盖还原到原用户 | `extensions/oracle/integration_*_test.go` |
| 运行时验收（沙箱） | 用 `feat/sql-dump-and-run` 构建的宿主加载本扩展，走右键菜单 → 转储 → 运行导入 → 读回 | opskat `e2e/scratch/2026-11-16-sql-dump-and-run/` 的脚本 |

## Open questions

（无）
