# AGENTS.md

ProxyPilot 是桌面端智能代理管理与代理网关系统：自动收集代理订阅 → 检测可用性 →
质量评分 → 维护代理池 → 为本机应用提供统一的 HTTP / HTTPS / SOCKS5 代理出口。

仓库由两个独立子项目组成（monorepo）：

- `proxy-core/` — Golang 核心引擎（API + 订阅采集 + 检测 + 评分 + 本地代理网关）
- `app/` — Electron 桌面端 UI（React 19 + TypeScript + Vite + Mantine）

详细架构见 [DESIGN.md](./DESIGN.md)。

---

## 常用命令

### proxy-core（Go 后端）

```bash
cd proxy-core

go build ./...        # 编译
go run .              # 本地运行（默认监听 127.0.0.1:17890）
go test ./... -count=1      # 运行全部测试
go vet ./...          # 静态检查
golangci-lint run ./...     # Lint（需安装 golangci-lint）
goreleaser build --snapshot --clean   # 跨平台构建验证（win/linux/macOS × amd64/arm64）
goreleaser release --clean           # 发布（需 GITHUB_TOKEN，配置见 .goreleaser.yml）
```

跨平台构建使用 [goreleaser](https://goreleaser.com)（配置 `proxy-core/.goreleaser.yml`）：
- 支持 windows / linux / darwin，架构 amd64 / arm64
- 纯 Go 无 CGO（`modernc.org/sqlite`），可交叉编译
- 版本号通过 ldflags 注入 `config.Version`（`-X ...config.Version={{.Version}}`），
  本地开发时使用默认值 `0.1.0`

### CI 发布流程（`.github/workflows/ci.yml`）

- 推送 `v*` tag（如 `v0.1.0`）触发发布，先跑 `test` 矩阵（Go vet/test/lint + 前端 typecheck/build）
- `release` job（ubuntu）：`goreleaser release` 构建 proxy-core 六目标并创建 GitHub Release
- `build-app` job（win/linux/mac 矩阵）：`npm run dist:<platform>` 构建安装包
  （Windows NSIS x64 / Linux AppImage x64+arm64 / macOS dmg x64+arm64），
  通过 `softprops/action-gh-release` 上传到同一 Release
- 发布需 `GITHUB_TOKEN`（仓库默认提供，`contents: write` 权限）

### app（Electron 前端）

```bash
cd app

npm install           # 安装依赖
npm run dev           # 开发模式（自动编译 proxy-core + 启动 Vite + Electron）
npm run build         # 构建（vite build + tsc 编译 electron）
npm run typecheck     # TypeScript 类型检查
npm run dist:win      # 打包 Windows 安装包（electron-builder，含 proxy-core.exe）
npm run dist:linux    # 打包 Linux 包（含 proxy-core）
npm run dist:mac      # 打包 macOS 包（含 proxy-core）
```

`npm run dev` 会先执行 `build:core` 编译 proxy-core（Windows 下 Go 自动生成
`proxy-core.exe`，其他平台生成 `proxy-core`），请勿在 Go 代码有编译错误时运行
前端开发命令。

electron-builder 配置在 `app/electron-builder.cjs`（函数形式，electron-builder
自动查找该文件名）：通过环境变量 `PP_CORE_BIN` 指定打包进 extraResources 的
proxy-core 二进制（`dist:win` 为 `proxy-core.exe`，`dist:linux`/`dist:mac` 为
`proxy-core`，`dist` 未指定时按构建机平台推断）。Electron 主进程
`resolveCorePath()` 按 `process.platform` 查找对应二进制。

---

## 项目结构

```
proxy-core/            # Golang 核心引擎（模块 github.com/axetroy/ProxyPilot/proxy-core）
  main.go              # 入口：装配各模块，spawn 后输出 PROXYPILOT_TOKEN 供前端鉴权
  api/                 # Gin REST 路由 + X-Token 鉴权中间件 + WebSocket 实时推送
  bus/                 # 日志/进度事件 pub/sub（回放历史，非阻塞发布）
  collector/           # 订阅抓取（fetcher.go）与定时调度（subscription.go）
  config/              # 配置：默认值 + PROXYPILOT_* 环境变量覆盖
  gateway/             # 本地 HTTP / HTTPS CONNECT / SOCKS5 代理出口
  model/               # 数据模型（ProxyNode / Subscription / CheckResult 等）
  parser/              # 订阅内容解析（Base64 / host:port / protocol://user:pass@host:port）
  pool/                # 节点池管理 + 质量评分（40% 成功率 + 30% 延迟 + 20% 稳定性 + 10% 匿名度）
  scheduler/           # 出口选择：weight = score/latency，失败惩罚窗口 30s，粘性绑定 10min
  storage/             # SQLite（modernc.org/sqlite，无 CGO）
  validator/           # 节点连通性检测（HTTP 探测 + TCP 隧道）

app/                   # Electron UI
  electron/main/       # 主进程：spawn proxy-core.exe、读取 token、窗口管理
  electron/preload.ts  # 预加载脚本
  src/                 # React 渲染进程
    api/               # 后端 HTTP + WebSocket 客户端封装
    stores/            # Zustand 状态（pool / logs / status / subscriptions）
    views/             # 页面：Dashboard / ProxyPool / Subscriptions / Logs / Settings
```

---

## 测试规范

- Go 测试文件与源码同目录（如 `pool/manager_test.go`），包内测试（`package pool`）。
- 提交代码前必须运行 `go test ./... -count=1` 且全部通过。
- 测试**不得依赖真实网络**：外部 HTTP 用 `httptest`，节点检测用 `mockChecker` 代替
  `validator.NewChecker`（否则会真实联网，测试不稳定）。
- 涉及时间窗口/定时器的测试要快速收敛，避免真实等待。
- Windows + CGO 未启用：**不要**使用 `go test -race`（会报 "requires cgo"）。
- 修改功能后补充相应回归测试，例如存储层的关键 bug 已有专门回归测试
  （`TestUpsertSubscriptionUpdatesWithoutNewRow` 等）。

## 代码规范

### Go（proxy-core）

- 标准库优先，模块职责单一；新模块放入 `proxy-core/<name>/` 独立包。
- 注释与日志使用中文，与现有代码保持一致。
- 错误必须显式处理：lint 开启 errcheck，`io.Copy`、`Shutdown` 等返回值
  未使用时应写 `_, _ = ...` 或 `_ = ...`。
- 未使用的导出函数/字段会被 `unused` 检查报告，删除或补上真实调用。
- 提交前运行 `go vet ./...` 与 `golangci-lint run ./...`，两者都必须无输出/通过。

### TypeScript / React（app）

- TypeScript 严格模式；用 `npm run typecheck` 验证。
- 组件优先使用 `src/components/ui/` 下的 shadcn/ui 风格基础组件，而非重复造轮子。
- 状态管理用 Zustand（`src/stores/`），API 调用走 `src/api/` 统一封装。
- 支持暗黑模式：颜色必须用 Mantine 主题变量（如 `var(--mantine-color-default-hover)`），
  不要写死灰度值（如 `#f1f3f5`），否则深色模式下不随主题变化。
- 路由使用 react-router-dom HashRouter。

---

## 安全注意事项

- API 仅监听 `127.0.0.1:17890`，所有请求必须带 `X-Token` header
  （token 由 proxy-core 启动时随机生成并通过 stdout 输出给 Electron）。
- 不要把 session token、订阅 URL 等敏感信息写进日志或提交到仓库。
- 代理网关默认出口 `127.0.0.1:7892`，HTTP 与 SOCKS5 共用同一端口（按连接首字节自动识别分流）。
  端口被占用会自动向后顺延，实际端口以 `/api/status` 返回值为准；仅绑定本机，不要修改为对外暴露。
- 以下配置项可通过前端「设置」页或 `/api/settings` 修改，持久化在 SQLite `settings` 表：
  `proxy_port` / `check_target` / `check_timeout` / `check_concurrency` /
  `refresh_interval`；启动时 `config.LoadOverrides()` 从 DB 覆盖默认值，环境变量优先级最高，其次 DB，最后默认值。

---

## 运行流程备忘

1. `cd proxy-core && go build -buildmode=exe -o proxy-core.exe .`
2. `cd app && npm run dev`（内部会执行第 1 步）
3. Electron 主进程 spawn `proxy-core.exe`，解析 stdout 的
   `PROXYPILOT_TOKEN=<hex>` 与 `PROXYPILOT_API=http://127.0.0.1:17890`，
   之后的 API 请求都带该 token。
4. 退出时 Electron 通知 Go 停止，释放代理端口。
