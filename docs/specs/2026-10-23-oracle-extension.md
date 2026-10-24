# Oracle 数据库扩展

> Status: Approved
> Owner: OpsKat 维护者
> Last updated: 2026-10-23（修订：服务名改为驱动专属字段、发布工具升级、minAppVersion；建表不含主键与注释；共用 SQL 库五处修正）

**Objective:** 发布官方扩展 `oracle`，在 OpsKat 内置的「SQL 数据库」资产下提供 Oracle 驱动：用口令或 Kerberos、经直连 / SSH 隧道 / 代理连接 Oracle 19c 及以上（含 TCPS 与监听器重定向），在查询面板与 AI / opsctl 里执行 Oracle SQL 与 PL/SQL，并像内置驱动一样浏览、查看、编辑表与对象、建改删表、导入导出。

**Hard invariant:** 不改 OpsKat 桌面端；**[用户决定]** opskat 的共用 SQL 库 `pkg/dbdriver/sqldb`（编进扩展的 WASM、不在桌面端运行）为 Oracle 修五处——建表列定义把 DEFAULT 写在 NOT NULL 之前、筛选的「为空 / 不为空」可由方言声明空串即 NULL、按原值定位行时可由方言给出按列类型的比较写法（Oracle 的 LOB 列）、写值时可由方言声明按列类型经函数转换的字面量（Oracle 带时区的时间戳，**[用户决定 2026-10-24]**）、表数据筛选值可由方言声明按列类型写字面量（按显示的文本筛选 Oracle 带本地时区的时间戳，**[用户决定 2026-10-24]**）——内置驱动除建表 DDL 文本的这一处顺序外行为不变，扩展升级依赖取用；其余只改 extensions 仓库；仓库里已有的扩展与发布流程不变（**[用户决定]** 发布工具 `tools/publish` 只升级其依赖的宿主版本，使它认得 ABI 2.5 的契约声明；Elasticsearch 的索引条目不变）；扩展只经宿主给的途径联网，不接触资产口令以外不属于它的凭据（Kerberos 只拿票据）。

## 所处位置

issue #329（Oracle 数据库支持）三步路线的第三步，也是最后一步。前两步已在 opskat main 合并：① 宿主驱动接口（读路径 #342、写路径 #345）；② 扩展平台能力（opsctl 独立运行扩展 #346、扩展提供数据库驱动 #347、重定向拨号与证书 TLS #348、Kerberos 与语句切分分类 #349，main `dbb46b7a`，宿主 ABI 2.5）。**[用户决定]** 本轮一次做完读写全部能力。

## Problem

标注：**[已核实]** = 对照 opskat `origin/main`（`dbb46b7a`）、extensions `origin/main`（`eea4a69`）或 go-ora v2.9.0 源码确认；**[实测]** = 2026-10-10 / 2026-10-21 / 2026-10-23 在 docker.internal 的 Oracle 容器上用验证用扩展观察到；**[用户决定]** = 本轮对话中用户选定；**[未知]** = 只能在真机上确认。

1. **OpsKat 连不了 Oracle。** [已核实] 四个内置驱动是 MySQL、PostgreSQL、SQL Server、SQLite；宿主已能加载扩展提供的数据库驱动（ABI 2.5，`pkg/extension/manifest.go:55`），但 extensions 仓库里没有任何扩展声明 `databaseDrivers`（唯一的扩展是 Elasticsearch，`extensions/elasticsearch/manifest.json`）。
2. **go-ora 不能直接编成扩展。** [已核实] 纯 Go 的 go-ora v2.9.0 在 `GOOS=wasip1` 下编译失败（`network/net_others.go` 用了 wasip1 没有的 `syscall.Sendmsg`），Kerberos 握手里取本机地址的 `net.InterfaceAddrs` 在 WASM 中不可用（`advanced_nego` 的 `getHostIPAddress`）。[实测] 打上这两处补丁后连接、查询、Kerberos 登录都正常（验证证据 opskat `e2e/scratch/2026-10-22-ext-driver-kerberos-sql-classify/`）。
3. **Oracle 的 SQL 与内置驱动的通用做法不兼容。** [实测] 11g~21c 拒绝普通 SQL 末尾的分号而 PL/SQL 块必须以分号结尾；一次调用只能执行一条语句；绑定变量只能写 `:1` / `:name`；把查询包进子查询分页会在重复列名（ORA-00918）、`FOR UPDATE`、末尾 `--` 注释上出错；`CURRENT_SCHEMA` 会粘在池化连接上；XMLType 在 19c 直接读回 NULL；NUMBER 以字符串读回、`''` 读回为 NULL、CHAR 补空格。
4. **Oracle 的对象模型与宿主的树不完全对应。** [已核实] 宿主对象浏览只有视图、存储过程、函数、触发器四类（`pkg/dbdriver/browse.go:7`、`pkg/dbdriver/descriptor.go:368-390`）；Oracle 一个资产连一个服务，库里按用户（schema）分对象，另有包、物化视图、序列、同义词、类型。

## Actors and user stories

1. 作为**运维用户**，我想在 SQL 数据库资产里选 Oracle、填主机端口与服务名（或 SID）、用口令或 Kerberos、必要时以 SYSDBA 登录，经 SSH 跳板或 TCPS 也能连上。
2. 作为**运维用户**，我想在资产树里按 schema 看到表、视图、存储过程（含包）、函数、触发器及其源码与 DDL，并像 MySQL 一样查看、筛选、编辑表数据，建表改表，导入导出。
3. 作为**运维用户**，我想在查询面板里执行 Oracle 语句与 PL/SQL 块，危险语句照旧先确认。
4. 作为**运维用户**，我想让 AI 与 opsctl 按我配置的语句类型策略执行 Oracle SQL。
5. 作为**维护者**，我想让这个扩展像 Elasticsearch 一样由 extensions 仓库的 CI 构建、测试并发布到扩展商店。

## Design decisions

| # | Decision | Basis and rejected option |
|---|---|---|
| 1 | **[用户决定]** 一轮做完读写全部能力（连接、执行、浏览、表数据、表格编辑、建改删清表、导入导出），手动事务不做 | 用户选定。Rejected: 先读后写分两轮——用户不要；手动事务——宿主尚未提供跨语句事务的会话能力 |
| 2 | **[用户决定]** go-ora 用 opskat 组织下的 fork（v2.9.0 + 两处 wasip1 补丁），不向上游提 PR | 用户选定；fork 让 CI 直接拉取、可复现。Rejected: 补丁源码放进扩展目录——仓库里多一份第三方源码、升级靠手工；只提上游等合并——发布时间不受控 |
| 3 | **[用户决定]** 只支持 Oracle 19c 及以上；登录后发现低于 19 即拒绝并说明 | 用户选定；19c 起的语法（`OFFSET … FETCH`、128 字节标识符、`ORACLE_MAINTAINED`）可统一使用，行为可预期。Rejected: 11.2 起全部——11g 要另一套分页与元数据写法；允许低版本尽力而为——会半工作 |
| 4 | **[用户决定]** 资产树第一层就是 schema：资产 → schema → 表 / 视图 / … | 用户选定；一个资产只连一个服务，再加一层永远只有一个节点。Rejected: 服务 → schema → 表——多点一下；只显示登录用户的 schema——看不到被授权的其他 schema |
| 5 | **[用户决定]** 包归入「存储过程」、物化视图归入「视图」；序列、同义词、类型本轮不在树里显示 | 用户选定；只改扩展仓库。Rejected: 先给宿主加对象分类——第三步推迟一轮；连包也不显示——包是 Oracle 最常用的代码单元 |
| 6 | **[用户决定]** 服务名、「按 SID 连接」开关与「连接角色」（普通 / SYSDBA / SYSOPER，默认普通）都是 Oracle 的驱动专属字段，表单不显示通用的「数据库」字段；TLS 只接受 PEM（沿用宿主证书字段） | [已核实] 宿主浏览、查询面板与 AI 执行时把资产配置的「数据库」换成树节点 / 标签页 / scope 选的库名再开会话（opskat `internal/app/query/objects.go:75-77`、`internal/ai/helper/database_helper.go:70-72`），扩展驱动按库各开会话（`internal/extreg/dbdriver.go:77`），而驱动专属字段不被替换；服务名放在「数据库」里就会在打开某个 schema 时丢失。宿主交给驱动的是证书内容而不是文件，扩展读不到钱包文件。Rejected: 「数据库」填服务名并放弃 schema 当「库」——推翻决定 4；改宿主——违反只改扩展仓库；只支持服务名、不支持角色——老库与 DBA 场景用不了 |
| 7 | 不声明连接级只读、不提供新建数据库 | [已核实] go-ora 不支持只读事务选项，Oracle 没有会话级只读；Oracle 的「新建数据库」不是普通用户能做的操作。宿主对没有声明只读的驱动拒绝只读配置，并且不显示建库入口 |
| 8 | 没有主键的表用 ROWID 精确定位要改 / 删的行 | Oracle 每行有稳定的 ROWID，与 PostgreSQL ctid、SQLite rowid 同理（宿主 `LocatesRowWithoutPrimaryKey`，`pkg/dbdriver/descriptor.go:39`）。Rejected: 按原值匹配——重复行时只能改到其中一行 |
| 9 | **[用户决定]** 扩展的 `minAppVersion` 为 1.14.1：约定下一次从 opskat main 发布的版本 1.14.1 包含宿主 ABI 2.5 | [已核实] 最新发布 v1.14.0 不含 ABI 2.5（没有任何 tag 包含 `dbb46b7a`）；Elasticsearch 已用尚未发布的 1.14.1；合并进 extensions main 会自动发布到商店，1.14.1 发布前用户装不上。Rejected: 1.15.0——用户选 1.14.1；按当前发布版本填——旧版宿主装上后加载失败 |
| 10 | **[用户决定]** 本轮一并把 `tools/publish` 依赖的宿主升级到 `dbb46b7a` 或之后 | [已核实] 它现在依赖的宿主（`859f2b3c`）只接受 hostABI 2.0–2.2，`make index-check` 与发布会拒绝 Oracle 的契约，索引也写不出数据库驱动 / Kerberos / 重定向三项授权。Rejected: 另开 PR 先升级——本轮要等它合并才能过 CI |

## 发布与安装

- 扩展名与驱动 ID 都是 `oracle`，首个版本 0.1.0，宿主 ABI 2.5；按 extensions 仓库的约定放在 `extensions/oracle/`，由仓库 CI 构建、测试，合并进 main 后发布到扩展商店。
- 安全契约声明：提供数据库驱动 `oracle`、使用资产的 Kerberos 凭据取票、跟随数据库重定向。安装 / 升级确认因此列出这三项及其含义（宿主已有的展示）。
- 在低于 `minAppVersion` 的 OpsKat 上，商店把它标为需要更新 OpsKat、不能安装；以本地包安装时宿主拒绝并点名需要的版本（[已核实] 宿主现有的兼容检查，`pkg/extension/compat.go:22`）。
- 驱动在「SQL 数据库」资产的驱动下拉中显示为 Oracle，默认端口 1521。

## 连接

**前提：** 资产选 Oracle 驱动，填主机、端口、用户名与「服务名」（必填）。

- **服务名 / SID：** 默认按服务名连接；打开「按 SID 连接」时把「服务名」字段的值当 SID。opsctl / AI 用驱动专属字段的键填这三项，未填服务名时保存失败并点名该字段。
- **连接角色：** 普通（默认）、SYSDBA、SYSOPER。选 SYSDBA / SYSOPER 时以该角色登录，登录失败的报错原样给出。
- **认证：** 口令；或 Kerberos（宿主的 Kerberos 字段组：主体、口令或 keytab、KDC、可选服务主体），驱动在握手中按 Oracle 报出的服务主体向宿主要票，不经手 Kerberos 凭据。Kerberos 时用户名可不填。
- **TCPS：** 用宿主的证书字段（启用、跳过校验、服务器名、CA / 客户端证书 / 私钥，路径或粘贴，PEM）。服务器名为空时按资产主机名校验；校验失败、证书读不到时报错且不回落到不加密。钱包（`cwallet.sso` / `ewallet.p12`）需用户先用 orapki 导出成 PEM，扩展说明里写明。
- **途径与重定向：** 直连、SSH 隧道、代理都可用；监听器把连接重定向到别的地址（共享服务器分派器、SCAN / RAC 本地监听）时，经资产的同一途径连到该地址。
- **版本：** 登录后读取数据库版本，低于 19 时断开并报错，报错点名读到的版本并说明需要 19c 及以上；测试连接同样报这条。
- **字符集：** 数据库字符集不是 UTF-8（如 ZHS16GBK）时，中文的读写正确。[已知限制] 在非 UTF-8 字符集的库上，含非 ASCII 字符的口令无法登录（go-ora 的限制），扩展说明里写明。
- **超时：** 资产的查询超时到期时取消正在执行的语句并报超时。
- **失败：** 主机连不上、服务名 / SID 不存在、口令错、账号锁定等，原样给出 Oracle 的报错（带 ORA 编号）。

## SQL 执行与语句切分分类

**前提：** 查询面板、AI 或 opsctl 对 Oracle 资产执行一段 SQL 文本。

- **切分：** 顶层 `;` 结束一条普通语句；以 `BEGIN`、`DECLARE`、`CREATE [OR REPLACE] [EDITIONABLE|NONEDITIONABLE] PROCEDURE / FUNCTION / PACKAGE [BODY] / TRIGGER / TYPE [BODY]` 开头的 PL/SQL 块在独占一行的 `/` 或文本末尾结束。单引号字符串、`q'[…]'` 一类替代引号、双引号标识符、`--` 与 `/* */` 注释里的分号和斜杠不切分。
- **执行：** 普通语句去掉末尾分号后执行；PL/SQL 块保留结尾的 `;`。多条语句按查询面板 / AI / opsctl 现有方式逐条执行。
- **分类：** 每条语句给出类型——SELECT（含 WITH）、INSERT、UPDATE、DELETE、MERGE、TRUNCATE、`CREATE <对象>`、`ALTER <对象>`、`DROP <对象>`、GRANT、REVOKE、PLSQL BLOCK、CALL（含 EXEC / EXECUTE）、COMMIT、ROLLBACK 等，按首个关键字（及对象类型）给出，其余为首个关键字。
- **危险标记：** 不带 WHERE 的 UPDATE / DELETE、CALL、含不带 WHERE 的 UPDATE / DELETE 的 PL/SQL 块标为危险（原因分别为宿主定义的 `no_where_update` / `no_where_delete` / `call`）。查询面板对危险语句与 DELETE / DROP / TRUNCATE / ALTER 照旧先弹确认；AI / opsctl 按语句类型策略放行、确认或拒绝。
- **切不开的文本：** 未闭合的引号或注释报「无法切分」并指出位置，不执行。
- **分页：** 查询面板里 SELECT / WITH 的结果分页并显示总行数。以下语句都能正常出结果：结果有重复列名、带 `FOR UPDATE`、末尾有 `--` 注释、本身已带 `FETCH FIRST` / `OFFSET`、带 `ORDER BY`。
- **绑定：** 查询面板与 AI 执行的是完整 SQL 文本，不涉及绑定变量；扩展自己生成的语句（表数据、编辑、导入）用 Oracle 的绑定写法。

## 浏览

**前提：** 资产已连上，展开资产树。

- **schema 列表：** 列出拥有当前用户可见的表、视图或代码对象的 schema；Oracle 自带的系统 schema（`ORACLE_MAINTAINED = 'Y'`）不列出；登录用户自己的 schema 排第一，其余按名字排序。宿主里的「库」（树节点、查询面板的「库」下拉、AI / opsctl 的 scope）即 schema：选了 schema 时，语句里不带 schema 前缀的对象按该 schema 解析，且不影响同一资产其他标签页或其他 schema 的会话；没有选时按登录用户自己的 schema 解析。
- **表：** schema 下列出表（含分区表）；回收站里的表（`BIN$…`）不列出。
- **视图：** 视图与物化视图。
- **存储过程：** 独立的存储过程与包（名字标明是包）。
- **函数、触发器：** 独立的函数；表上的触发器。
- **源码：** 视图、物化视图显示定义；存储过程、函数、触发器显示源码；包显示规格与包体（有包体时）。
- **表结构：** 列（类型、长度 / 精度、可空、默认值、注释）、主键、索引（唯一与否、列）、外键。
- **DDL：** 表与视图可「查看 DDL」，内容是数据库生成的建表 / 建视图语句（`DBMS_METADATA`）。
- **起始查询：** 从表打开 SQL 编辑器时的起始语句是 `SELECT * FROM "SCHEMA"."TABLE" FETCH FIRST n ROWS ONLY`。
- **失败：** 没有权限看某个对象时，该对象不出现在列表里；看源码 / DDL 时没有权限报 Oracle 的错误。

## 表数据、编辑、建改删表与导入导出

**前提：** 在资产树里打开一张表。

- **查看：** 首屏给出列、主键、总行数与第一页；筛选、排序、翻页按宿主的表数据界面工作。
- **值的显示：** NUMBER / FLOAT 按数据库里的精确值；DATE 显示为 `YYYY-MM-DD HH:MM:SS`；TIMESTAMP 带小数秒（按列的精度）；TIMESTAMP WITH (LOCAL) TIME ZONE 带时区；INTERVAL 按 Oracle 的文本形式；CHAR 不去掉补齐的空格（照数据库原值）；VARCHAR2 / NVARCHAR2 / CLOB / NCLOB / LONG 为文本；BLOB / RAW / LONG RAW 为二进制；XMLType 序列化为文本（表数据界面；查询面板里直接选 XMLType 列时，19c 上读回 NULL 是 go-ora 的已知限制，扩展说明里提示改用 `XMLSERIALIZE`）；23ai 的 BOOLEAN 显示 true / false（表数据界面；查询面板里直接选 BOOLEAN 列时显示 1 / 0 是 go-ora 的已知限制——它把 BOOLEAN 报成普通 NUMBER，扩展说明里写明）[用户决定 2026-10-24]，JSON、VECTOR 显示为文本；NULL 显示为 NULL。
- **表格编辑：** 新增、修改、删除行，提交前预览生成的语句并确认（宿主现有流程）。有主键的表按主键定位；没有主键的表按 ROWID 定位，不显示「无主键」提示。值按列类型写回（DATE / TIMESTAMP 用对应的字面量或转换，二进制按十六进制）。视图不可编辑。
- **建表：** 宿主建表对话框给出的表名与各列的名字、类型、可空、默认值生成 Oracle 的 `CREATE TABLE`。[已核实] 宿主的建表对话框与请求里没有主键与注释（opskat `pkg/dbdriver/session.go:343-358`），表注释、列注释可建表后用「改表」设置。
- **改表：** 改表名、列改名、新增 / 删除列，以及改列类型、可空、默认值、列注释、表注释。
- **删表、清空表：** `DROP TABLE`（是否进回收站按数据库的回收站设置）与 `TRUNCATE TABLE`，执行前宿主照旧确认。
- **导入：** 宿主支持的导入格式按列类型写入；导入失败的行报 Oracle 错误。不提供「忽略外键」。
- **导出 / 复制为 SQL：** 整表导出为 Oracle 的 INSERT 语句（流式，不把整表读进内存）；选中行复制为 INSERT / UPDATE 语句。
- **标识符：** 生成的语句一律用带双引号的 `"SCHEMA"."TABLE"` 与 `"COLUMN"`，大小写照数据库字典原样。

## Out of scope

- 手动事务（开始 / 提交 / 回滚跨多次执行）：宿主尚未提供。
- Oracle 18c 及以下版本。
- 序列、同义词、类型、数据库链接在树里的显示；给宿主增加对象分类。
- 执行计划（EXPLAIN PLAN）与 Oracle 专属的编辑器补全。
- 读取 Oracle 钱包文件、TNS 别名 / `tnsnames.ora`、LDAP 命名。
- 连接级只读、新建数据库 / 用户。
- 向 go-ora 上游提交补丁。

## Testing decisions

| Seam | What it verifies | Prior art |
|---|---|---|
| 发布工具检查（`make index-check`，CI 运行） | 含 Oracle 扩展时版本检查通过；`tools/publish` 自身的测试通过；打包出的索引条目列出数据库驱动、Kerberos、重定向三项授权，Elasticsearch 条目不变 | `tools/publish/*_test.go` |
| 扩展单元测试（宿主的 `opskat.TestHost`，CI 运行） | 语句切分分类（引号、注释、PL/SQL 块、`/`、危险标记、切不开的位置）；末尾分号处理；版本检查的报错；生成的分页、筛选、编辑、建改删表、导入导出语句；驱动描述与契约声明 | `extensions/elasticsearch/*_test.go`；opskat `pkg/extension/testdata/fixture-dbdriver` |
| 真库集成测试（设置环境变量指向 Oracle 时运行，CI 不跑） | 在 19c / 21c / 23ai 上：连接、执行、分页各种语句形态、浏览与源码、表数据值的显示、编辑（含无主键表）、建改删表、导入导出 | 无 |
| 运行时验证（wrap-up 后主会话驱动） | 沙箱桌面端与 opsctl 对 docker.internal 的 19c（GBK、TCPS、Kerberos，重建临时 KDC）、21c、23ai 容器：全部要求；经 SSH 隧道与监听器重定向；12c / 11g 容器上的版本拒绝；商店 / 本地包安装确认 | opskat `e2e/scratch/2026-10-22-ext-driver-kerberos-sql-classify/` 的工具与验证环境 |

不能自动化的部分：真实 Oracle 与 KDC 不在 CI；Kerberos、TCPS、重定向、GBK 字符集、桌面端界面只在运行时验证中观察。

## Open questions

