<p align="right">
<a href="./README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">OpsKat Extensions</h1>

<p align="center">A WebAssembly-based extension framework for the <a href="https://github.com/opskat/opskat">OpsKat</a> platform. Extensions are compiled to WASM and executed in a secure sandbox, combining Go backends with React frontends through a well-defined host interface.</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.23-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  &nbsp;
  <img src="https://img.shields.io/badge/WASM-WASIP1-654FF0?style=for-the-badge&logo=webassembly&logoColor=white" alt="WASM">
  &nbsp;
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=white" alt="React">
</p>

## Architecture

```
┌─────────────────────────────────────────┐
│         OpsKat Host Application         │
│  ┌───────────────┐  ┌────────────────┐  │
│  │ WASM Runtime  │  │ Asset Config   │  │
│  │ (tools/actions│  │ KV Store       │  │
│  │  /policies)   │  │ File & HTTP IO │  │
│  └───────────────┘  └────────────────┘  │
│       WASM Host Imports (ABI)           │
└─────────────────────────────────────────┘
             ↕  WASM ABI
┌─────────────────────────────────────────┐
│       Extension (WASM Module)           │
│  ┌───────────────┐  ┌────────────────┐  │
│  │ Go SDK        │  │ User Code      │  │
│  │ (opskat pkg)  │  │ (tools/actions)│  │
│  └───────────────┘  └────────────────┘  │
└─────────────────────────────────────────┘
```

## Project Structure

```
.
├── Makefile                    # Root build orchestrator
├── sdk/go/opskat/              # Go SDK for building extensions
├── extensions/
│   └── oss/                    # S3-compatible object storage extension
│       ├── backend/            # Go → WASM
│       ├── frontend/           # React + Vite
│       ├── locales/            # i18n (en, zh-CN)
│       ├── manifest.json       # Extension declaration
│       └── Makefile
└── examples/
    └── echo/                   # Minimal reference extension
        ├── backend/
        ├── frontend/
        └── manifest.json
```

## Supported Extensions

### OSS Extension (`extensions/oss`)

A full-featured S3-compatible object storage manager. Works with AWS S3, MinIO, Aliyun OSS, and any S3-compatible service.

**Tools** (for AI invocation):

| Tool | Description |
|------|-------------|
| `list_buckets` | List all buckets |
| `list_objects` | List objects with pagination |
| `get_object_info` | Get object metadata |
| `download_object` | Download an object |
| `upload_object` | Upload an object |
| `copy_object` | Server-side copy |
| `move_object` | Move (copy + delete) |
| `delete_object` | Delete a single object |
| `delete_objects` | Batch delete |
| `presign_url` | Generate pre-signed URL |

**Actions** (for frontend UI):

| Action | Description |
|--------|-------------|
| `test_connection` | Validate asset configuration |
| `browse` | Navigate directories |
| `upload` / `download` | Transfer with progress events |
| `batch_delete` / `batch_copy` | Batch operations with progress |
| `get_presigned_url` | Generate share links |
| `search` | Prefix-based search |
| `preview` | Preview file content |

**Policy groups**: Read-only, Read-Write, Full Access, with fine-grained action control (`list`, `read`, `write`, `delete`, `admin`).

### Echo Example (`examples/echo`)

A minimal reference extension demonstrating core SDK patterns: tool handling, action streaming, KV storage, logging, and policy checking.

## Key Concepts

| Concept | Description |
|---------|-------------|
| **Tool** | Stateless, synchronous operation invoked by AI |
| **Action** | Stateful, streaming operation invoked by frontend UI |
| **EventWriter** | Real-time event streaming from actions to frontend |
| **PolicyChecker** | Maps (tool, args) to (action, resource) for authorization |
| **IOHandle** | Abstracted file/HTTP I/O through the host |
| **Manifest** | JSON declaration of tools, actions, UI pages, and policies |

## Building

**Prerequisites**: [Go 1.23+](https://go.dev/), [Node.js 18+](https://nodejs.org/) with [pnpm](https://pnpm.io/)

```bash
# Build the OSS extension (backend WASM + frontend)
make build EXT=oss

# Or build manually
cd extensions/oss
make build
```

Build steps:
1. Compile Go to WASM: `GOOS=wasip1 GOARCH=wasm go build -o dist/main.wasm .`
2. Build frontend with Vite: `pnpm install && pnpm build`
3. Copy `manifest.json` and `locales/` to `dist/`

Output is in `extensions/oss/dist/`.

## Running the Dev Server

```bash
make devserver EXT=oss
```

This starts a local dev server that simulates the OpsKat host, loading your extension for interactive testing.

## Testing

Run unit tests (no WASM compilation required):

```bash
# Test OSS extension
cd extensions/oss/backend && go test -v ./...

# Test echo example
cd examples/echo/backend && go test -v ./...

# Test SDK
cd sdk/go/opskat && go test -v ./...
```

The SDK provides `TestHost` for simulating the host environment in tests:

```go
func TestMyTool(t *testing.T) {
    th := opskat.NewTestHost(
        opskat.WithAssetConfig("my-asset", myConfig),
        opskat.WithMockHTTP(mockHandler),
    )
    defer th.Close()

    result, err := th.CallTool("my_tool", map[string]any{"key": "value"})
    // assert result
}
```

## Developing a New Extension

1. **Create the directory structure**:
   ```
   extensions/my-ext/
   ├── backend/
   │   ├── main.go
   │   └── go.mod
   ├── frontend/
   │   └── index.js (or use Vite for TypeScript)
   ├── manifest.json
   └── Makefile
   ```

2. **Register handlers** in `main.go`:
   ```go
   package main

   import "github.com/opskat/extensions/sdk/go/opskat"

   func main() {
       opskat.RegisterTool("my_tool", handleMyTool)
       opskat.RegisterAction("my_action", handleMyAction)
       opskat.RegisterPolicyChecker(checkPolicy)
       opskat.Run()
   }
   ```

3. **Write frontend** using host-provided React:
   ```js
   const { React } = window.__OPSKAT_EXT__;

   export function MyPage({ assetId }) {
       const [data, setData] = React.useState(null);

       async function callBackend() {
           const res = await window.__OPSKAT_EXT__.api.callTool("my-ext", "my_tool", { key: "value" });
           setData(res);
       }

       return React.createElement("div", {}, /* UI */);
   }
   ```

4. **Define `manifest.json`** with tools, actions, policies, and frontend pages.

5. **Build and test**:
   ```bash
   cd extensions/my-ext && make build
   go test -v ./backend/...
   ```

## Debugging

- **Logging**: Use `opskat.Log("info", "message")` — output is captured by the host.
- **Events**: In tests, use `th.Events()` to inspect all emitted action events.
- **Host functions**: Only available in WASM builds. Use `opskat.TestHost` for local testing.
- **Frontend**: Check `vite.config.ts` hostExternals plugin if imports fail — React and UI components come from `window.__OPSKAT_EXT__`.

---

## License

This project is open-sourced under the [GPLv3](./LICENSE) license.
