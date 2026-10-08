<p align="right">
<a href="./README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">OpsKat Extensions</h1>

<p align="center"><a href="https://github.com/opskat/opskat">OpsKat</a> 官方扩展的源码仓库。</p>

每个扩展都是基于 OpsKat 自带扩展 SDK（`github.com/opskat/opskat/pkg/extsdk`）构建的
WASM 客户端。如何编写扩展——manifest、资产类型、工具、策略、页面、测试——请看 OpsKat 仓库的
[`extensions/README.md`](https://github.com/opskat/opskat/blob/main/extensions/README.md)，本仓库不重复。

## 目录约定

```
extensions/<name>/
  go.mod          # 独立 Go 模块：github.com/opskat/extensions/<name>
  main.go ...     # 扩展本体，注册都写在 init() 里
  *_test.go       # 单元测试，经 opskat.TestHost 驱动
  manifest.json   # 名称、版本、hostABI、权限声明
  SKILL.md        # 给模型看的扩展说明（可选）
  locales/        # en.json、zh-CN.json
  frontend/       # 页面源码（可选）
  Makefile        # build 产出 dist/，test 跑测试
  dist/           # 构建产物，已被 git 忽略
```

目前的扩展：[`elasticsearch`](./extensions/elasticsearch)。

## 构建与测试

```bash
make build EXT=elasticsearch   # 产出 extensions/elasticsearch/dist/
make test EXT=elasticsearch
```

构建需要支持 `GOOS=wasip1` 的 Go；带 `frontend/` 的扩展还需要 Node.js 与 pnpm。`dist/`
是完整的扩展目录：main.wasm、manifest.json、SKILL.md（若有）、locales/，以及扩展带页面时
构建出的 frontend/。

## 本地安装

在应用的扩展设置里选"从目录安装"，选择 `extensions/<name>/dist`；或在终端执行：

```bash
opsctl ext dev "$PWD/extensions/elasticsearch/dist"
```

修改后重新 `make build` 并再执行一次 `opsctl ext dev` 即可热重载。

## 发布

扩展经 CI 进入 OpsKat 扩展商店，不需要手工上传。仓库根目录的 `index.json` 列出所有已发布版本，
`index.json.sig` 是它的 ed25519 签名，应用验签通过后才展示商店。

1. **在 PR 里改版本号。** 修改 `extensions/<name>/manifest.json` 的 `version`
   （`MAJOR.MINOR.PATCH`）。若版本低于 `index.json` 里该扩展已发布的最高版本，`index-check`
   任务会让 PR 检查失败；本地用 `make index-check` 可得到同样的结果。
2. **合并。** 推到 `main` 后，`package` 任务（`publish prepare`，不接触任何 Secret）对 manifest
   版本还不在 `index.json` 里的每个扩展，用 `make build` 构建 `dist/`，打成 zip（`manifest.json`
   位于 zip 根），经 OpsKat 自己的扩展加载流程载入该 zip，读出应用将展示的显示名、说明、图标、能力、
   `hostABI` 与 `minAppVersion`；随后 `publish` 任务（`publish release`，不运行任何扩展构建代码）
   把每个 zip 以单层 OCI 制品推到 `ghcr.io/opskat/extensions/<name>:<version>`，记录 sha256 与大小，
   签名 `index.json`，把 `index.json` 与 `index.json.sig` 提交回 `main`。这次提交带 `[skip ci]`，
   不会再次触发发布。

已在 `index.json` 里的版本不会重建、不会覆盖：要发布改动就升版本号。任一步失败则任务失败、什么都不提交，
因此 `index.json` 里的每个版本都一定已在 registry 中。

工具在 `tools/publish`，是独立的 Go 模块（`go -C tools/publish run . -h`）：`check`（PR 检查）、
`prepare` 与 `release`（CI 的两半）、`publish`（在同一台机器上两步都做，即 `make publish`）、`keygen`。
`make ci` 会跑它的测试。

### 维护者配置

只需做一次；合并后的首次发布即可看出是否配置正确。

1. **生成签名密钥对**：`go -C tools/publish run . keygen`，输出 `private:` 与 `public:` 两行，
   均为标准 base64。私钥绝不提交进仓库。
2. **把私钥加为仓库 Secret**，名为 `EXTENSION_INDEX_SIGNING_KEY`（Settings → Secrets and
   variables → Actions）。缺少时发布失败，报错会点名该 Secret。
3. **把公钥放进应用**：加入 OpsKat 内置的受信索引公钥列表并随版本发布。轮换时先把新公钥加进应用、
   旧公钥保留（应用接受任一受信公钥的签名），再替换 Secret。
4. **把包设为公开**：扩展首次发布后，把 `ghcr.io/opskat/extensions/<name>` 设为 public
   （组织 → Packages → 包设置 → Change visibility），应用才能免登录拉取；同一页面的
   "Manage Actions access" 需给本仓库写权限。
5. **允许 CI 推送 `main`**：Settings → Actions → General → Workflow permissions 选
   "Read and write permissions"；若 `main` 有保护规则，需允许 GitHub Actions 绕过，否则索引提交会被拒绝。
