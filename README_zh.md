<p align="right">
<a href="./README.md">English</a> | <a href="./README_zh.md">中文</a>
</p>

<h1 align="center">OpsKat Extensions</h1>

<p align="center">基于 WebAssembly 的 <a href="https://github.com/opskat/opskat">OpsKat</a> 平台扩展框架。扩展被编译为 WASM 并在安全沙箱中运行，通过定义良好的宿主接口将 Go 后端与 React 前端结合在一起。</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.23-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go">
  &nbsp;
  <img src="https://img.shields.io/badge/WASM-WASIP1-654FF0?style=for-the-badge&logo=webassembly&logoColor=white" alt="WASM">
  &nbsp;
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=white" alt="React">
</p>

## 架构

```
┌─────────────────────────────────────────┐
│            OpsKat 宿主应用               │
│  ┌───────────────┐  ┌────────────────┐  │
│  │ WASM 运行时    │  │ 资产配置       │  │
│  │ (工具/动作     │  │ KV 存储        │  │
│  │  /策略)        │  │ 文件 & HTTP IO │  │
│  └───────────────┘  └────────────────┘  │
│        WASM 宿主导入 (ABI)               │
└─────────────────────────────────────────┘
              ↕  WASM ABI
┌─────────────────────────────────────────┐
│          扩展 (WASM 模块)                │
│  ┌───────────────┐  ┌────────────────┐  │
│  │ Go SDK        │  │ 用户代码       │  │
│  │ (opskat 包)   │  │ (工具/动作)    │  │
│  └───────────────┘  └────────────────┘  │
└─────────────────────────────────────────┘
```

## 项目结构

```
.
├── Makefile                    # 根构建编排
├── sdk/go/opskat/              # 用于构建扩展的 Go SDK
├── extensions/
│   └── oss/                    # S3 兼容对象存储扩展
│       ├── backend/            # Go → WASM
│       ├── frontend/           # React + Vite
│       ├── locales/            # 国际化 (en, zh-CN)
│       ├── manifest.json       # 扩展声明
│       └── Makefile
└── examples/
    └── echo/                   # 最小参考扩展
        ├── backend/
        ├── frontend/
        └── manifest.json
```

## 支持的扩展

### OSS 扩展 (`extensions/oss`)

功能完整的 S3 兼容对象存储管理器，支持 AWS S3、MinIO、阿里云 OSS 及任何 S3 兼容服务。

**工具**（供 AI 调用）：

| 工具 | 说明 |
|------|------|
| `list_buckets` | 列出所有存储桶 |
| `list_objects` | 分页列出对象 |
| `get_object_info` | 获取对象元数据 |
| `download_object` | 下载对象 |
| `upload_object` | 上传对象 |
| `copy_object` | 服务端复制 |
| `move_object` | 移动（复制+删除） |
| `delete_object` | 删除单个对象 |
| `delete_objects` | 批量删除 |
| `presign_url` | 生成预签名 URL |

**动作**（供前端 UI 调用）：

| 动作 | 说明 |
|------|------|
| `test_connection` | 验证资产配置 |
| `browse` | 浏览目录结构 |
| `upload` / `download` | 带进度事件的传输 |
| `batch_delete` / `batch_copy` | 带进度的批量操作 |
| `get_presigned_url` | 生成分享链接 |
| `search` | 基于前缀搜索 |
| `preview` | 预览文件内容 |

**策略组**：只读、读写、完全访问，支持细粒度动作控制（`list`、`read`、`write`、`delete`、`admin`）。

### Echo 示例 (`examples/echo`)

最小参考扩展，演示 SDK 核心模式：工具处理、动作流式传输、KV 存储、日志记录和策略检查。

## 核心概念

| 概念 | 说明 |
|------|------|
| **Tool（工具）** | 无状态、同步操作，由 AI 调用 |
| **Action（动作）** | 有状态、流式操作，由前端 UI 调用 |
| **EventWriter** | 从动作实时流式传输事件到前端 |
| **PolicyChecker** | 将 (工具, 参数) 映射为 (动作, 资源) 用于授权 |
| **IOHandle** | 通过宿主抽象的文件/HTTP I/O |
| **Manifest** | 声明工具、动作、UI 页面和策略的 JSON 文件 |

## 构建

**前置依赖**：[Go 1.23+](https://go.dev/)、[Node.js 18+](https://nodejs.org/) + [pnpm](https://pnpm.io/)

```bash
# 构建 OSS 扩展（后端 WASM + 前端）
make build EXT=oss

# 或手动构建
cd extensions/oss
make build
```

构建步骤：
1. 编译 Go 为 WASM：`GOOS=wasip1 GOARCH=wasm go build -o dist/main.wasm .`
2. 使用 Vite 构建前端：`pnpm install && pnpm build`
3. 将 `manifest.json` 和 `locales/` 复制到 `dist/`

产出位于 `extensions/oss/dist/`。

## 运行开发服务器

```bash
make devserver EXT=oss
```

这会启动一个模拟 OpsKat 宿主的本地开发服务器，加载扩展进行交互式测试。

## 测试

运行单元测试（无需 WASM 编译）：

```bash
# 测试 OSS 扩展
cd extensions/oss/backend && go test -v ./...

# 测试 echo 示例
cd examples/echo/backend && go test -v ./...

# 测试 SDK
cd sdk/go/opskat && go test -v ./...
```

SDK 提供 `TestHost` 用于在测试中模拟宿主环境：

```go
func TestMyTool(t *testing.T) {
    th := opskat.NewTestHost(
        opskat.WithAssetConfig("my-asset", myConfig),
        opskat.WithMockHTTP(mockHandler),
    )
    defer th.Close()

    result, err := th.CallTool("my_tool", map[string]any{"key": "value"})
    // 断言 result
}
```

## 开发新扩展

1. **创建目录结构**：
   ```
   extensions/my-ext/
   ├── backend/
   │   ├── main.go
   │   └── go.mod
   ├── frontend/
   │   └── index.js（或使用 Vite 支持 TypeScript）
   ├── manifest.json
   └── Makefile
   ```

2. **注册处理器**（`main.go`）：
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

3. **编写前端**，使用宿主提供的 React：
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

4. **定义 `manifest.json`**，声明工具、动作、策略和前端页面。

5. **构建和测试**：
   ```bash
   cd extensions/my-ext && make build
   go test -v ./backend/...
   ```

## 调试

- **日志**：使用 `opskat.Log("info", "message")` — 输出由宿主捕获。
- **事件**：在测试中使用 `th.Events()` 检查所有已发出的动作事件。
- **宿主函数**：仅在 WASM 构建中可用，本地测试请使用 `opskat.TestHost`。
- **前端**：如果导入失败，检查 `vite.config.ts` 的 hostExternals 插件 — React 和 UI 组件来自 `window.__OPSKAT_EXT__`。

---

## 开源许可

本项目基于 [GPLv3](./LICENSE) 协议开源。
