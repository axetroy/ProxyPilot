# AGENTS.md

ProxyPilot 是桌面端本地代理管理与代理网关系统：导入代理订阅 → 检测可用性 →
质量评分 → 维护代理池 → 为本机应用提供统一的 HTTP / HTTPS / SOCKS5 本地代理出口。

仓库由两个独立子项目组成（monorepo）：

- `proxy-core/` — Golang 核心引擎（API + 订阅导入/同步 + 检测 + 评分 + 本地代理网关）
- `app/` — Electron 桌面端 UI（React 19 + TypeScript + Vite + Mantine）

详细架构见 [DESIGN.md](./DESIGN.md)。

---

## 语言

- 必须使用简体中文回答用户，包括思考和推理过程。
- 所有解释、总结、代码说明、错误信息说明都使用简体中文。
- 代码中的变量名、函数名、类名使用英文。
- 如果用户明确要求其他语言，则遵循用户的要求。

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
- `build-app` job（win/linux/mac × x64/arm64 矩阵）：`npm run dist:<platform>[:arch]` 构建安装包
  （Windows NSIS x64+arm64 / Linux AppImage x64+arm64 / macOS dmg+zip x64+arm64），
  通过 `softprops/action-gh-release` 上传到同一 Release。
  同系统内架构交叉编译（如 linux/x64 编译 linux/arm64）：`dist:*:arm64` 脚本会先按
  `GOOS/GOARCH` 交叉编译 proxy-core，再以 `--arm64` 参数让 electron-builder 产出对应架构
  安装包；同一平台多架构必须分开构建（extraResources 里的 proxy-core 是单架构二进制）。
  架构完全由 CLI 的 `--x64`/`--arm64` 参数决定（`app/electron-builder.cjs` 的 target 只声明
  target 名称、不写死 arch：写死 arch 会覆盖 CLI 参数，且 CLI 指定了架构而该架构无对应
  target 时会回退到 Linux 默认 target `["snap","appimage"]`，在无 snapcraft 的 GitHub
  Actions runner 上报 "snapcraft process failed ENOENT"）；本地 `npm run dist` 未传
  flag 时默认构建当前主机架构，CI 各 `dist:<platform>[:arch]` 脚本通过 `--x64`/`--arm64`
  显式覆盖。
- 安装包命名遵循 electron-builder 升级检测（electron-updater）约定格式
  `${productName}-${version}-${os}-${arch}.${ext}`（如 `ProxyPilot-0.1.5-mac-arm64.dmg`、
  `ProxyPilot-0.1.5-win-x64.exe`、`ProxyPilot-0.1.5-linux-x64.AppImage`），
  由 `app/electron-builder.cjs` 的 `artifactName` 配置（注意必须用单引号字符串，
  反引号模板字符串会把 `${productName}` 等宏立即插值导致命名错误）
- **自动更新**：`electron-builder.cjs` 声明 `publish: { provider: 'github', owner: 'axetroy',
  repo: 'ProxyPilot' }` 以生成更新元数据（`latest*.yml`），CI 统一改名为「平台-架构」格式
  （`windows-x64.yml` / `windows-arm64.yml` / `linux-x64.yml` / `darwin-x64.yml` /
  `darwin-arm64.yml`）；electron-updater 已被 patch（`patches/electron-updater+6.8.9.patch`，
  由 patch-package 在 `npm install` 时自动应用）按本机平台+架构读取对应文件，避免同名
  yml 互相覆盖导致下载错架构安装包（v0.1.11 事故）。所有 dist 脚本都带 `--publish never`，
  禁止 electron-builder 在 tag 构建时自行上传（runner 代理下报 self-signed certificate
  错误），上传统一由 `softprops/action-gh-release` 完成。实现见
  `app/electron/main/updater.ts`，设计见 DESIGN.md「更新机制设计」。
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
  main.go              # 入口：装配各模块，spawn 后输出 PROXYPILOT_TOKEN 供前端鉴权；启动订阅 server
  api/                 # Gin REST 路由 + X-Token 鉴权中间件 + WebSocket 实时推送 + 对外订阅端点
  bus/                 # 日志/进度事件 pub/sub（回放历史，非阻塞发布）
  collector/           # 订阅抓取（fetcher.go）与定时调度（subscription.go）
  config/              # 配置：默认值 + PROXYPILOT_* 环境变量覆盖
  gateway/             # 本地 HTTP / HTTPS CONNECT / SOCKS5 代理出口
  rule/                # 智能分流规则：同步（Loyalsoldier/surge-rules）→ 域名白名单校验 → 匹配（代理名单/直连名单/.cn/geoip）
  model/               # 数据模型（ProxyNode / Subscription / CheckResult 等）
  parser/              # 订阅内容解析（Base64 / host:port / protocol://user:pass@host:port）与导出（proxy_export.go）
  pool/                # 节点池管理 + 质量评分（40% 成功率 + 30% 延迟 + 20% 稳定性 + 10% 连接安全度）
  scheduler/           # 出口选择：集中策略（fixed 固定 / best 最高评分 / random 随机 / weighted 智能加权 / round-robin 轮询 / chain 代理链路 / auto-chain 自动链路），失败惩罚窗口 30s，粘性绑定 10min，固定出口自动联动 fixed 策略
  geoip/               # 离线 IP 地区解析（ip2region xdb 数据 go:embed 内嵌，无外部 API）
  storage/             # SQLite（modernc.org/sqlite，无 CGO）：proxy_nodes/subscriptions/check_history/settings/proxy_chains
  validator/           # 节点连通性检测（HTTP 探测 + TCP 隧道）+ ConnectChain 逐跳链路连接（HTTP CONNECT / HTTPS TLS / SOCKS5 手写握手）

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
- 不要把 session token、订阅 URL、订阅密钥等敏感信息写进日志或提交到仓库。
- 代理网关默认出口 `127.0.0.1:7892`，HTTP 与 SOCKS5 共用同一端口（按连接首字节自动识别分流）。
  端口被占用会自动向后顺延，实际端口以 `/api/status` 返回值为准；仅绑定本机，不要修改为对外暴露。
- **对外订阅服务**独立监听 `127.0.0.1:17891`（`subscription_listen` 可配置）：把代理池中存活的节点
  作为订阅源对外提供，订阅 URL 为 `http://<host>:<port>/sub/<token>`（支持 `?format=plain|base64`，
  默认 base64）。订阅密钥独立于 session token，可随时重置；关闭时返回 404，密钥错误返回 401。
  默认仅本机监听，对外暴露（`0.0.0.0:17891`）需用户显式配置，避免把管理 API 一起暴露；
  监听地址修改后需重启 proxy-core 生效（无热更新）。
- 以下配置项可通过前端「设置」页或 `/api/settings` 修改，持久化在 SQLite `settings` 表：
  `proxy_port` / `check_target` / `check_safety_target` / `check_timeout` / `check_concurrency` /
  `refresh_interval` / `subscription_enabled` / `subscription_listen` / `history_retention_days`
  （检测历史保留天数）/ `chain_check_interval`（链路自动健康检测周期）；启动时 `config.LoadOverrides()`
  从 DB 覆盖默认值，环境变量优先级最高，其次 DB，最后默认值。`subscription_token` 不进
  `/api/settings` 通用表单（避免泄露），由 `/api/subscription` 专门接口管理（GET 返回、
  PUT `{resetToken:true}` 重置），首次启动随机生成并持久化。
- **智能分流规则**（`rule/`）：网关在建立上游连接前按 局域网 / `.cn` → 代理名单 → 直连名单 →
  geoip(CN) → 默认动作（whitelist 走代理 / blacklist 直连） 的顺序匹配目标域名（域名列表按后缀
  匹配，子域名命中父域条目），只改 proxy-core、不依赖系统 PAC。同步源默认固定为
  Loyalsoldier/surge-rules（HTTPS，`raw.githubusercontent.com` + `cdn.jsdelivr.net` 镜像，失败
  保留上次缓存 + go:embed 内置兜底列表）；解析只做域名白名单校验（小写、字符集受限、≤255），
  **绝不执行远程内容**。规则源 URL 允许用户配置 http(s) 源（如自建/内网列表，仅作文本拉取）。
  规则缓存写入与数据库同目录的 `pac_rules.json`。配置项
  （`pac_enabled` / `pac_mode` / `pac_direct_urls` / `pac_proxy_urls` / `pac_refresh_interval`）
  不进 `/api/settings` 通用表单，由 `/api/pac-config` 专门接口管理。设计见 DESIGN.md「智能分流设计」。
- 连接安全评分优先使用真实探测（`check_safety_target` 回显端点，默认 `https://httpbin.org/anything`）：
  对比直连/经代理的出口 IP 与目标收到的请求头，按 源 IP 隐藏 40% + 头泄漏 30% + 代理特征 30% 加权；
  在此基础上叠加调节项：两次经代理采样出口 IP 不同（轮换代理）+5，请求被代理改写（回显
  URL/Host 与目标不一致）每项 -10；探测失败时回退到按协议类型的启发式（SOCKS5=95 / HTTP(S)=80 / 带认证=50）。

---

## 运行流程备忘

1. `cd proxy-core && go build -buildmode=exe -o proxy-core.exe .`
2. `cd app && npm run dev`（内部会执行第 1 步）
3. Electron 主进程 spawn `proxy-core.exe`，解析 stdout 的
   `PROXYPILOT_TOKEN=<hex>` 与 `PROXYPILOT_API=http://127.0.0.1:17890`，
   之后的 API 请求都带该 token。
4. 退出时 Electron 通知 Go 停止，释放代理端口。
