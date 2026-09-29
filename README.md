<p align="right">
<a href="./README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">OpsKat Extensions</h1>

<p align="center">Source of the official <a href="https://github.com/opskat/opskat">OpsKat</a> extensions.</p>

Each extension is a WASM guest built on the extension SDK that ships with OpsKat
(`github.com/opskat/opskat/pkg/extsdk`). For how to write one — manifest, asset
types, tools, policies, pages, testing — read
[`extensions/README.md`](https://github.com/opskat/opskat/blob/main/extensions/README.md)
in the OpsKat repository; it is not repeated here.

## Layout

```
extensions/<name>/
  go.mod          # its own Go module: github.com/opskat/extensions/<name>
  main.go ...     # the guest; registrations live in init()
  *_test.go       # unit tests, driven through opskat.TestHost
  manifest.json   # name, version, hostABI, capability grants
  SKILL.md        # what the model reads about the extension (optional)
  locales/        # en.json, zh-CN.json
  frontend/       # page sources (optional)
  Makefile        # `build` produces dist/, `test` runs the tests
  dist/           # build output, git-ignored
```

Current extensions: [`elasticsearch`](./extensions/elasticsearch).

## Build and test

```bash
make build EXT=elasticsearch   # -> extensions/elasticsearch/dist/
make test EXT=elasticsearch
```

Building needs Go with `GOOS=wasip1` support. `dist/` is a complete extension
directory: main.wasm, manifest.json, SKILL.md when present, and locales/.

## Install locally

Either use "Install from directory" in the app's extension settings and pick
`extensions/<name>/dist`, or from a terminal:

```bash
opsctl ext dev "$PWD/extensions/elasticsearch/dist"
```

Re-running `make build` and `opsctl ext dev` again hot-reloads the extension.

## Releases

Released artifacts and the extension index are handled by the extension store,
not by this repository.
