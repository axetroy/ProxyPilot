# 开发指南（DEVELOPMENT.md）

ProxyPilot 开发、构建与发布相关说明。产品特性与使用说明见 [README.md](./README.md)，
架构设计见 [DESIGN.md](./DESIGN.md)。

## 目录

- [开发指南（DEVELOPMENT.md）](#开发指南developmentmd)
  - [目录](#目录)
  - [架构总览](#架构总览)
  - [路线图](#路线图)
  - [环境要求](#环境要求)
  - [仓库结构](#仓库结构)
  - [快速开始](#快速开始)
    - [1. 编译并运行 Go 核心](#1-编译并运行-go-核心)
    - [2. 编译并运行 Electron + React](#2-编译并运行-electron--react)
    - [3. Electron 开发全链路](#3-electron-开发全链路)
  - [配置（proxy-core 环境变量）](#配置proxy-core-环境变量)
  - [API 参考](#api-参考)
    - [示例](#示例)
    - [WebSocket 事件](#websocket-事件)
  - [评分模型](#评分模型)
  - [数据存储](#数据存储)
  - [测试规范](#测试规范)
  - [打包与发布](#打包与发布)
    - [打包命令](#打包命令)
    - [macOS 签名与公证](#macos-签名与公证)
    - [CI 发布](#ci-发布)

## 架构总览

```
Electron UI (React) ──HTTP API + WebSocket──> Golang Core (proxy-core.exe)
                                              │ 订阅抓取 / 解析 / 检测 / 评分
                                              │ 代理池 / 出口选择 / 本地代理网关
                                              │ SQLite 持久化
                                              ▼
                                         外部代理节点池
```

Electron 启动时自动拉起 `proxy-core.exe`，通过 token 鉴权；退出时自动释放端口。
详细设计见 [DESIGN.md](./DESIGN.md)。

## 路线图

| 阶段    | 内容                                                         | 状态      |
| ------- | ------------------------------------------------------------ | --------- |
| Phase 1 | 框架 · Go 启动管理 · HTTP API · SQLite · 订阅抓取 · 节点检测 | ✅ 已完成 |
| Phase 2 | HTTP Proxy · HTTPS CONNECT · SOCKS5 · 自动选节点             | ✅ 已完成 |
| Phase 3 | 节点评分 · 自动淘汰 · 自动切换 · 实时监控                    | ✅ 已完成 |
| 打包    | electron-builder / NSIS / AppImage / dmg                     | ✅ 已完成 |
| Phase 4 | 云服务器：账号系统 · 云同步 · 团队管理 · 对外 API 服务       | ⏳ 未实现 |

> Phase 1–3 与打包为本地核心能力及发布链路，已完成；Phase 4 为云端能力，需部署云服务器提供支撑，当前尚未实现。

## 环境要求

| 依赖    | 版本  |
| ------- | ----- |
| Go      | 1.23+ |
| Node.js | 18+   |
| npm     | 9+    |

## 仓库结构

```
ProxyPilot/
├── DESIGN.md            # 架构设计白皮书
├── README.md            # 产品说明
├── DEVELOPMENT.md       # 本文件
├── FAQ.md               # 常见问题
├── proxy-core/          # Golang 核心引擎
│   ├── main.go
│   ├── api/             # Gin REST 路由 + WebSocket + 鉴权中间件 + 对外订阅端点
│   ├── bus/             # 日志/进度事件 pub/sub
│   ├── collector/       # 订阅抓取与定时调度
│   ├── parser/          # 订阅内容 → 节点（与导出）
│   ├── validator/       # 节点连通性检测 + CONNECT/SOCKS5 隧道 + 链路连接
│   ├── pool/            # 节点池 + 质量评分
│   ├── scheduler/       # 出口选择（fixed/best/random/weighted/round-robin/chain）
│   ├── gateway/         # HTTP / HTTPS CONNECT / SOCKS5 本地代理
│   ├── rule/            # 智能分流规则（同步/校验/匹配/geoip）
│   ├── geoip/           # 离线 IP 地区解析（ip2region，go:embed）
│   ├── storage/         # SQLite 持久化（modernc.org/sqlite，无 CGO）
│   └── config/          # 环境变量驱动配置 + session token
└── app/                 # Electron + React 渲染层
    ├── electron/        # 主进程：spawn proxy-core、注入 token
    ├── electron-builder.cjs
    ├── src/
    │   ├── api/         # Axios 客户端 + WS 日志流
    │   ├── stores/      # Zustand（status · pool · subscriptions · logs）
    │   ├── views/       # Dashboard / ProxyPool / Subscriptions / Logs / Settings
    │   └── components/ui/ # shadcn/ui 风格基础组件
    └── vite/tsconfig/…  # Vite + Tailwind + TypeScript
```

## 快速开始

### 1. 编译并运行 Go 核心

```bash
cd proxy-core
go mod tidy
go build -buildmode=exe -o proxy-core.exe .
./proxy-core.exe
```

启动后控制台输出：

```
PROXYPILOT_TOKEN=<随机 session token>
PROXYPILOT_API=http://127.0.0.1:17890
```

> token 由核心每次启动动态生成（可由环境变量 `PROXYPILOT_TOKEN` 覆盖），所有 API 请求须携带。

### 2. 编译并运行 Electron + React

```bash
cd app
npm install              # 若 Electron 二进制下载缓慢，见 FAQ.md「镜像加速」
npm run dev:renderer     # 仅启动 Vite dev server（已代理 /api /ws 到 Go）
npm run build            # 渲染层构建产物 → app/dist
npm run typecheck        # tsc --noEmit 类型检查
npm run electron:main    # 编译 electron main + 启动 Electron
```

### 3. Electron 开发全链路

```bash
cd app
npm run electron:dev     # 同时启动 Vite (5173) 与 Electron
```

Electron 会自动 spawn Go 核心（`npm run dev` 会先编译 proxy-core），并注入 token，加载界面后即可使用。
请勿在 Go 代码有编译错误时运行前端开发命令。

## 配置（proxy-core 环境变量）

| 变量                           | 默认值                                            | 说明                                                          |
| ------------------------------ | ------------------------------------------------- | ------------------------------------------------------------- |
| `PROXYPILOT_API_BIND`          | `127.0.0.1:17890`                                 | API / WebSocket 监听地址（仅本机）                            |
| `PROXYPILOT_DB_PATH`           | `proxypilot.db`                                   | SQLite 数据库文件路径                                         |
| `PROXYPILOT_PROXY_PORT`        | `7892`                                            | 代理监听端口（HTTP 与 SOCKS5 共用，仅本机，被占用时自动顺延） |
| `PROXYPILOT_TOKEN`             | 随机生成                                          | Session token（不设则每次启动生成）                           |
| `PROXYPILOT_CHECK_TARGET`      | `https://www.apple.com/library/test/success.html` | 节点检测目标 URL                                              |
| `PROXYPILOT_CHECK_TIMEOUT`     | `10s`                                             | 单节点检测超时                                                |
| `PROXYPILOT_CHECK_CONCURRENCY` | `32`                                              | 并发检测数                                                    |
| `PROXYPILOT_REFRESH_INTERVAL`  | `15m`                                             | 代理池自动检测周期                                            |

更多可配置项（检测目标、订阅、分流规则等）可通过「设置」页或 `/api/settings` / `/api/pac-config` 修改，
持久化于 SQLite `settings` 表；环境变量优先级最高，其次 DB，最后默认值。详见 DESIGN.md 与 AGENTS.md。

示例：

```bash
PROXYPILOT_DB_PATH=/opt/proxypilot/proxypilot.db PROXYPILOT_PROXY_PORT=8080 ./proxy-core.exe
```

## API 参考

- 基础地址：`http://127.0.0.1:17890`
- 鉴权：请求头 `X-Token: <session>`（WebSocket 可用 `?token=<session>`）
- 响应统一格式：

```json
{ "code": 0, "msg": "ok", "data": {} }
```

| Method | Path                            | 说明                                                                |
| ------ | ------------------------------- | ------------------------------------------------------------------- | ---- | --------- |
| GET    | `/api/status`                   | 系统状态（running / proxyCount / aliveCount / currentIP / version） |
| GET    | `/api/proxies`                  | 节点列表，支持 `?status=alive                                       | dead | new` 过滤 |
| DELETE | `/api/proxy/:id`                | 删除节点                                                            |
| POST   | `/api/proxy/check`              | 触发全量检测（空 body）或检测指定节点（`{"id": <n>}`）              |
| GET    | `/api/subscriptions`            | 订阅列表                                                            |
| POST   | `/api/subscription`             | 新增订阅（`{name, url, interval}`）                                 |
| DELETE | `/api/subscription/:id`         | 删除订阅                                                            |
| POST   | `/api/subscription/:id/refresh` | 立即刷新该订阅                                                      |
| POST   | `/api/gateway/start`            | 启动代理网关（默认 HTTP+SOCKS5 共用 7892，端口被占用自动顺延）      |
| POST   | `/api/gateway/stop`             | 停止网关                                                            |
| GET    | `/api/settings`                 | 获取可配置项                                                        |
| PUT    | `/api/settings`                 | 更新配置，保存并立即生效，重启后仍保留                              |
| GET    | `/ws`                           | WebSocket 实时事件流                                                |

> 智能分流规则配置由 `/api/pac-config` 专门接口管理（`pac_enabled` / `pac_mode` / 直连/代理名单 / 刷新周期等），
> 对外订阅服务由 `/api/subscription` 接口管理（密钥独立于 session token，可重置）。

### 示例

```bash
TOKEN=$(./proxy-core.exe | grep -oP 'PROXYPILOT_TOKEN=\K\S+')

# 状态
curl -H "X-Token: $TOKEN" http://127.0.0.1:17890/api/status

# 新增订阅
curl -X POST -H "X-Token: $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"source-a","url":"https://example.com/list","interval":3600}' \
  http://127.0.0.1:17890/api/subscription

# 全量检测
curl -X POST -H "X-Token: $TOKEN" http://127.0.0.1:17890/api/proxy/check

# 启动网关
curl -X POST -H "X-Token: $TOKEN" http://127.0.0.1:17890/api/gateway/start
```

### WebSocket 事件

`ws://127.0.0.1:17890/ws`，推送两类消息：

```jsonc
// 日志
{ "type": "log", "level": "info", "message": "proxy checked" }

// 检测进度
{ "type": "progress", "current": 50, "total": 500 }
```

## 评分模型

```
Score = 40% × 成功率 + 30% × 延迟得分 + 20% × 稳定性 + 10% × 连接安全
```

- 成功率：最近检测成功占比
- 延迟得分：按阈值分级（≤100ms=100、≤300ms=80、≤600ms=60、≤1000ms=40、≤2000ms=20）
- 稳定性：失败次数越多扣分越多
- 连接安全：SOCKS5 默认 95、HTTP(S) 默认 80；带用户名密码的节点降低；
  启用真实安全探测时按源 IP 隐藏 40% + 头泄漏 30% + 代理特征 30% 加权

## 数据存储

数据库文件默认 `proxypilot.db`，核心表：

| 表              | 说明                                                |
| --------------- | --------------------------------------------------- |
| `proxy_nodes`   | 节点（host, port, protocol 唯一去重，含评分与状态） |
| `subscriptions` | 订阅（name, url, interval, enabled, last_fetch）    |
| `check_history` | 检测历史（用于评分与稳定性计算）                    |
| `settings`      | 可配置项持久化                                      |
| `proxy_chains`  | 链式代理链路                                        |

## 测试规范

- Go 测试文件与源码同目录，包内测试；提交前运行 `go test ./... -count=1` 且全部通过。
- 测试**不得依赖真实网络**：外部 HTTP 用 `httptest`，节点检测用 `mockChecker`。
- 时间窗口/定时器测试要快速收敛，避免真实等待。
- Windows + CGO 未启用：**不要**使用 `go test -race`。
- 提交前运行 `go vet ./...` 与 `golangci-lint run ./...`，两者必须无输出/通过。
- 前端用 `npm run typecheck` 验证；修改功能后补充相应回归测试。

## 打包与发布

使用 [electron-builder](https://www.electron.build/) 打包桌面应用，proxy-core 二进制随包分发到
`resources/` 目录，Electron 启动时自动 spawn。

### 打包命令

```bash
cd app
npm run dist        # 编译 Go 核心 + 构建渲染层 + 打包当前平台
npm run dist:win    # 指定 Windows 平台（NSIS 安装包）
npm run dist:linux  # 指定 Linux 平台
npm run dist:mac    # 指定 macOS 平台
```

产物输出到 `app/release/`：

| 平台    | 产物                                | 说明                                                      |
| ------- | ----------------------------------- | --------------------------------------------------------- |
| Windows | `ProxyPilot Setup <version>.exe`    | NSIS 安装包（可选安装目录、桌面/开始菜单快捷方式）        |
| Windows | `win-unpacked/`                     | 解包目录（`ProxyPilot.exe` + `resources/proxy-core.exe`） |
| Linux   | `ProxyPilot-<version>.AppImage` 等  | 按 electron-builder 默认目标生成                          |
| macOS   | `ProxyPilot-<version>.dmg` / `.zip` | dmg 供手动安装，zip 供自动更新（Squirrel.Mac 只接受 zip） |

> 打包配置见 `app/electron-builder.cjs`：通过环境变量 `PP_CORE_BIN` 指定随包分发的 proxy-core
> 二进制（Windows 为 `proxy-core.exe`，Linux/macOS 为 `proxy-core`）；
> 安装包命名遵循 electron-updater 升级检测约定格式
> `${productName}-${version}-${os}-${arch}.${ext}`。

### macOS 签名与公证

当前未配置 Apple Developer 证书，macOS 产物为**未签名、未公证**的 dmg。macOS 的 Gatekeeper
会拦截未签名应用，首次打开时请：

1. 右键点击 `ProxyPilot.app` → 选择「打开」→ 再次确认「打开」；或
2. 终端执行 `xattr -dr com.apple.quarantine /Applications/ProxyPilot.app` 去除隔离属性

> 若已购买 Apple Developer Program（$99/年），可配置自动签名 + 公证，用户即可直接双击打开：
>
> 1. 在 Apple Developer 后台生成 **Developer ID Application** 证书，导出为 `.p12`
> 2. 在 GitHub 仓库 Settings → Secrets 配置：`CSC_LINK`（p12 的 base64）、`CSC_KEY_PASSWORD`、
>    `APPLE_ID`、`APPLE_APP_SPECIFIC_PASSWORD`（App 专用密码）、`APPLE_TEAM_ID`
> 3. 将 `app/electron-builder.cjs` 中 `mac.identity` 改为 `'Developer ID Application: <你的名字>'`，
>    并添加 `notarize: true`
> 4. CI 的 `build-app` job 中移除 `CSC_IDENTITY_AUTO_DISCOVERY: 'false'`，
>    electron-builder 会自动读取上述 secrets 完成签名与公证

### CI 发布

- 推送 `v*` tag 触发 CI：先跑测试矩阵（Go vet/test/lint + 前端 typecheck/build），
  再执行 `goreleaser release` 构建 proxy-core 六目标并创建 GitHub Release，
  最后按 平台 × 架构 矩阵构建安装包上传到同一 Release。
- 自动更新元数据由 electron-updater 按「平台-架构」分别读取，避免跨架构误下载。
- 发布需 `GITHUB_TOKEN`（仓库默认提供，`contents: write` 权限）。详见 AGENTS.md 与
  `.github/workflows/ci.yml`。
