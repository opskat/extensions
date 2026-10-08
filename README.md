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

Building needs Go with `GOOS=wasip1` support, and Node.js with pnpm for an extension
that has a `frontend/`. `dist/` is a complete extension directory: main.wasm,
manifest.json, SKILL.md when present, locales/, and the built page under frontend/
when the extension has one.

## Install locally

Either use "Install from directory" in the app's extension settings and pick
`extensions/<name>/dist`, or from a terminal:

```bash
opsctl ext dev "$PWD/extensions/elasticsearch/dist"
```

Re-running `make build` and `opsctl ext dev` again hot-reloads the extension.

## Releases

Extensions reach the OpsKat extension store through CI; nobody uploads anything
by hand. `index.json` at the repository root lists every published version, and
`index.json.sig` is its ed25519 signature, which the app checks before showing
the store.

1. **Bump the version in a pull request.** Change `version` in
   `extensions/<name>/manifest.json` (`MAJOR.MINOR.PATCH`). The `index-check` job
   fails the pull request if the version is below the highest version
   `index.json` already publishes for that extension; run `make index-check` to
   see the same result locally.
2. **Merge.** On the push to `main`, the `package` job (`publish prepare`, no
   secrets) builds `dist/` with `make build` for every extension whose manifest
   version is not in `index.json` yet, zips it with `manifest.json` at the zip
   root, and loads the zip through OpsKat's own extension loader to read the
   display name, description, icon, capabilities, `hostABI` and `minAppVersion`
   the app will show. The `publish` job (`publish release`), which runs no
   extension build code, then pushes each zip as a single-layer OCI artifact to
   `ghcr.io/opskat/extensions/<name>:<version>`, records its sha256 and size,
   signs `index.json` and commits `index.json` and `index.json.sig` to `main`.
   That commit carries `[skip ci]`, so it does not publish again.

A version already in `index.json` is never rebuilt or overwritten: to ship a
change, bump the version. If any step fails, the job fails and nothing is
committed, so every version in `index.json` is in the registry.

The tool is `tools/publish`, its own Go module (`go -C tools/publish run . -h`):
`check` (the pull-request gate), `prepare` and `release` (the two CI halves),
`publish` (both, on one machine — `make publish`) and `keygen`. `make ci` runs its
tests.

### Maintainer setup

Done once; the first publish after merging shows whether it worked.

1. **Generate the signing key pair**: `go -C tools/publish run . keygen`. It
   prints a `private:` and a `public:` line, both standard base64. Never commit
   the private key.
2. **Add the private key as a repository secret** named
   `EXTENSION_INDEX_SIGNING_KEY` (Settings → Secrets and variables → Actions).
   Without it, publishing fails with an error naming the secret.
3. **Put the public key into the app**: add it to OpsKat's built-in list of
   trusted index keys and ship that release. To rotate, add the new key to the
   app first and keep the old one listed — the app accepts a signature from any
   trusted key — then replace the secret.
4. **Make the packages public**: after an extension's first publish, set
   `ghcr.io/opskat/extensions/<name>` to public (organization → Packages →
   package settings → Change visibility), so the app can pull it without logging
   in. In the same settings, "Manage Actions access" must give this repository
   write access.
5. **Let CI push to `main`**: Settings → Actions → General → Workflow
   permissions → "Read and write permissions". If `main` is protected, allow
   GitHub Actions to bypass the rule, or the index commit is rejected.
