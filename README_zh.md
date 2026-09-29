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

发布产物与扩展索引由扩展商店负责，不在本仓库内处理。
