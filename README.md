# ProxyPilot

桌面端智能代理管理与代理网关系统。

自动收集代理订阅 → 检测可用性 → 质量评分 → 维护代理池 → 为本机应用提供统一的
HTTP / HTTPS / SOCKS5 代理出口。

- **架构定位**：Electron 桌面端 + Golang 核心引擎
- **核心出口**：`127.0.0.1:7892`（HTTP 代理）与 `127.0.0.1:7893`（SOCKS5）
- **设计文档**：见 [DESIGN.md](./DESIGN.md)

---

## 核心能力

| 能力 | 说明 |
| --- | --- |
| 代理订阅管理 | 通过 URL 订阅自动采集节点，支持 interval 定时刷新与手动触发 |
| 订阅内容解析 | 自动识别 Base64 编码列表 / 裸 `host:port` / `protocol://user:pass@host:port` |
| 可用性检测 | 通过节点建立真实 HTTP 请求，测量延迟与连通性 |
| 质量评分 | `40% 成功率 + 30% 延迟 + 20% 稳定性 + 10% 匿名度` |
| 代理池维护 | 状态机 `NEW → CHECKING → AVAILABLE → DEAD`，失效节点自动降级，连续失败 ≥3 次自动淘汰 |
| 本地代理网关 | HTTP 代理、HTTPS CONNECT 隧道、SOCKS5 服务，按 `score/latency` 权重选出口，失败自动切换（30s 惩罚窗口） |
| 实时日志 | 检查进度与日志通过 WebSocket 实时推送到界面 |

---

## 总体架构

```
┌───────────────────────────────────────────────────────────┐
│                     Electron UI (React)                    │
│                                                           │
│   app/  Electron进程 · React18 + TS + Vite + Zustand +    │
│   Tailwind + shadcn/ui风格的本地组件库                      │
│   Dashboard · Proxy Pool · Subscriptions · Logs · Settings│
└───────────────────────────┬───────────────────────────────┘
                            │  HTTP API + WebSocket
                            │  127.0.0.1:17890  (仅本机)
┌───────────────────────────▼───────────────────────────────┐
│                   Golang Core（proxy-core.exe）            │
│                                                           │
│   api/        Gin REST + Token 鉴权 + WebSocket            │
│   collector/  订阅抓取调度 │ parser/ 解析订阅内容           │
│   validator/  节点连通性检测                                │
│   pool/       节点池 + 评分                                 │
│   scheduler/  出口选择（weight = score / latency）          │
│   gateway/    HTTP / CONNECT / SOCKS5 本地代理             │
│                                                           │
│   storage/    SQLite（proxy_nodes · subscriptions ·       │
│               check_history）                              │
└───────────────────────────┬───────────────────────────────┘
                            ▼
                     外部代理节点池
```

**进程模型**（最终打包形态）：

```
ProxyPilot.exe
  ├── Electron 进程（UI）
  └── proxy-core.exe（Go 核心，随 Electron 启停）
```

Electron 启动时 spawn `proxy-core.exe`，读取其输出的 `PROXYPILOT_TOKEN` 用于后续 API 鉴权；
退出时通知 Go 停止、释放代理端口（DESIGN.md §4 关闭流程）。

---

## 目录结构

```
ProxyPilot/
├── DESIGN.md            # 架构设计白皮书（V1.0）
├── README.md
├── proxy-core/           # Golang 核心引擎
│   ├── main.go
│   ├── api/              # Gin REST 路由 + WebSocket + 鉴权中间件
│   ├── bus/              # 日志/进度事件 pub/sub
│   ├── collector/        # 订阅抓取（fetcher.go）与调度（subscription.go）
│   ├── parser/           # 订阅内容 → 节点（proxy_parser.go）
│   ├── validator/        # 节点连通性检测 + CONNECT/SOCKS5 隧道
│   ├── pool/             # 节点池（manager.go）+ 评分（score.go）
│   ├── scheduler/        # 出口选择（selector.go）
│   ├── gateway/          # HTTP/HTTPS/SOCKS5 本地代理
│   ├── storage/          # SQLite 持久化（scan.go / sqlite.go）
│   └── config/           # 环境变量驱动配置 + session token
└── app/                  # Electron + React 渲染层
    ├── electron/         # main 进程：spawn core、注入 token
    ├── src/
    │   ├── api/          # Axios 客户端 + WS 日志流
    │   ├── stores/       # Zustand（status · pool · subscriptions · logs）
    │   ├── views/        # Dashboard / ProxyPool / Subscriptions / Logs / Settings
    │   ├── components/ui/# shadcn/ui 风格基础组件
    │   └── lib/          # cn() 工具
    └── vite/tsconfig/…   # Vite + Tailwind + TypeScript
```

---

## 快速开始

### 环境要求

| 依赖 | 版本 |
| --- | --- |
| Go | 1.23+ |
| Node.js | 18+ |
| npm | 9+ |

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
npm install              # 若 Electron 二进制下载缓慢，见下方「镜像加速」
npm run dev              # 仅启动 Vite dev server（已代理 /api /ws 到 Go）
npm run build            # 渲染层构建产物 → app/dist
npm run typecheck        # tsc --noEmit 类型检查
npm run electron:main    # 编译 electron main + 启动 Electron
```

### 3. Electron 开发全链路

```bash
cd app
npm run electron:dev # 同时启动 Vite (5173) 与 Electron
```

Electron 会自动 spawn Go 核心，并注入 token，加载界面后即可使用。

---

## 配置（proxy-core 环境变量）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PROXYPILOT_API_BIND` | `127.0.0.1:17890` | API / WebSocket 监听地址（仅本机） |
| `PROXYPILOT_DB_PATH` | `proxypilot.db` | SQLite 数据库文件路径 |
| `PROXYPILOT_HTTP_BIND` | `127.0.0.1:7892` | HTTP 代理监听地址（被占用时自动向后顺延） |
| `PROXYPILOT_SOCKS5_BIND` | `127.0.0.1:7893` | SOCKS5 代理监听地址（被占用时自动向后顺延） |
| `PROXYPILOT_TOKEN` | 随机生成 | Session token（不设则每次启动生成） |
| `PROXYPILOT_CHECK_TARGET` | `https://www.apple.com/library/test/success.html` | 节点检测目标 URL |
| `PROXYPILOT_CHECK_TIMEOUT` | `10s` | 单节点检测超时 |
| `PROXYPILOT_CHECK_CONCURRENCY` | `32` | 并发检测数 |
| `PROXYPILOT_REFRESH_INTERVAL` | `15m` | 代理池自动检测周期 |

示例：

```bash
PROXYPILOT_DB_PATH=/opt/proxypilot/proxypilot.db PROXYPILOT_HTTP_BIND=127.0.0.1:8080 ./proxy-core.exe
```

---

## API 参考（对应 DESIGN.md §6）

- 基础地址：`http://127.0.0.1:17890`
- 鉴权：请求头 `X-Token: <session>`（WebSocket 可用 `?token=<session>`）
- 响应统一格式：

```json
{ "code": 0, "msg": "ok", "data": { } }
```

| Method | Path | 说明 |
| --- | --- | --- |
| GET | `/api/status` | 系统状态（running / proxyCount / aliveCount / currentIP / version） |
| GET | `/api/proxies` | 节点列表，支持 `?status=alive|dead|new` 过滤 |
| DELETE | `/api/proxy/:id` | 删除节点 |
| POST | `/api/proxy/check` | 触发全量检测（空 body）或检测指定节点（`{"id": <n>}`） |
| GET | `/api/subscriptions` | 订阅列表 |
| POST | `/api/subscription` | 新增订阅（`{name, url, interval}`） |
| DELETE | `/api/subscription/:id` | 删除订阅 |
| POST | `/api/subscription/:id/refresh` | 立即刷新该订阅 |
| POST | `/api/gateway/start` | 启动代理网关（HTTP 7892 / SOCKS5 7893） |
| POST | `/api/gateway/stop` | 停止网关 |
| GET | `/api/settings` | 获取可配置项（HTTP/SOCKS5 端口、检测目标、超时、并发、刷新周期） |
| PUT | `/api/settings` | 更新配置（`{"check_timeout": "5s"}`），保存并立即生效，重启后仍保留 |
| GET | `/ws` | WebSocket 实时事件流 |

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

---

## 评分模型（DESIGN.md §9）

```
Score = 40% × 成功率 + 30% × 延迟得分 + 20% × 稳定性 + 10% × 匿名性
```

- 成功率：最近检测成功占比
- 延迟得分：按阈值分级（≤100ms=100、≤300ms=80、≤600ms=60、≤1000ms=40、≤2000ms=20）
- 稳定性：失败次数越多扣分越多
- 匿名性：SOCKS5 默认 95、HTTP(S) 默认 80；带用户名密码的节点降低

---

## 数据存储（SQLite）

数据库文件默认 `proxypilot.db`，含三张表（DESIGN.md §13）：

| 表 | 字段 |
| --- | --- |
| `proxy_nodes` | id, host, port, protocol, username, password, latency, score, status, success_count, fail_count, last_check, created_at, updated_at |
| `subscriptions` | id, name, url, interval, enabled, last_fetch, created_at |
| `check_history` | id, proxy_id, success, latency, error, created_at |

> 节点以 `(host, port, protocol)` 唯一去重，重复抓取自动更新；检测历史被用于评分与稳定性计算。

---

## 本地代理使用

网关启动后，本机应用只需将代理指向本地端口。默认端口为 `7892`（HTTP）/ `7893`（SOCKS5），
若默认端口被其他程序占用，网关会自动向后顺延寻找一对空闲端口（HTTP 与 SOCKS5 端口保持相邻），
实际绑定的端口以界面「Dashboard」或 `/api/status` 返回的 `httpProxyBind` / `socks5ProxyBind` 为准：

```
HTTP  →  127.0.0.1:7892
SOCKS5 → 127.0.0.1:7893
```

- 浏览器/系统代理：指向 `127.0.0.1:7892`
- 命令行：

```bash
curl -x http://127.0.0.1:7892 https://ifconfig.me
curl --socks5 127.0.0.1:7893 https://ifconfig.me
```

---

## 打包与发布

使用 [electron-builder](https://www.electron.build/) 打包桌面应用，proxy-core 二进制会随包分发到 `resources/` 目录，Electron 启动时自动 spawn。

### 打包命令

```bash
cd app
npm run dist        # 编译 Go 核心 + 构建渲染层 + 打包当前平台
npm run dist:win    # 指定 Windows 平台（NSIS 安装包）
npm run dist:linux  # 指定 Linux 平台
npm run dist:mac    # 指定 macOS 平台
```

产物输出到 `app/release/`：

| 平台 | 产物 | 说明 |
| --- | --- | --- |
| Windows | `ProxyPilot Setup <version>.exe` | NSIS 安装包（可选安装目录、桌面/开始菜单快捷方式） |
| Windows | `win-unpacked/` | 解包目录（`ProxyPilot.exe` + `resources/proxy-core.exe`） |
| Linux | `ProxyPilot-<version>.AppImage` 等 | 按 electron-builder 默认目标生成 |
| macOS | `ProxyPilot-<version>.dmg` 等 | 按 electron-builder 默认目标生成 |

> 打包配置见 `app/electron-builder.cjs`（通过环境变量 `PP_CORE_BIN` 指定随包分发的 proxy-core 二进制：Windows 为 `proxy-core.exe`，Linux/macOS 为 `proxy-core`）。

### macOS 签名与公证

当前未配置 Apple Developer 证书，macOS 产物为**未签名、未公证**的 dmg。macOS 的 Gatekeeper 会拦截未签名应用，首次打开时请：

1. 右键点击 `ProxyPilot.app` → 选择「打开」→ 再次确认「打开」；或
2. 终端执行 `xattr -dr com.apple.quarantine /Applications/ProxyPilot.app` 去除隔离属性

> 若已购买 Apple Developer Program（$99/年），可配置自动签名 + 公证，用户即可直接双击打开：
>
> 1. 在 Apple Developer 后台生成 **Developer ID Application** 证书，导出为 `.p12`
> 2. 在 GitHub 仓库 Settings → Secrets 配置：`CSC_LINK`（p12 的 base64）、`CSC_KEY_PASSWORD`、`APPLE_ID`、`APPLE_APP_SPECIFIC_PASSWORD`（App 专用密码）、`APPLE_TEAM_ID`
> 3. 将 `app/electron-builder.cjs` 中 `mac.identity` 改为 `'Developer ID Application: <你的名字>'`，并添加 `notarize: true`
> 4. CI 的 `build-app` job 中移除 `CSC_IDENTITY_AUTO_DISCOVERY: 'false'`，electron-builder 会自动读取上述 secrets 完成签名与公证

---

## 路线图（DESIGN.md §17）

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| Phase 1 | Electron 框架 · Go 启动管理 · HTTP API · SQLite · 订阅抓取 · 节点检测 | ✅ 已完成 |
| Phase 2 | HTTP Proxy · HTTPS CONNECT · SOCKS5 · 自动选节点 | ✅ 已完成 |
| Phase 3 | 节点评分 · 自动淘汰 · 自动切换 · 实时监控 | ✅ 已完成 |
| Phase 4 | 账号系统 · 云同步 · 代理市场 · 团队管理 · API 服务 | ⏳ 待办 |
| 打包 | electron-builder / NSIS / portable | ✅ 已完成 |

---

## 常见问题

**Electron 二进制下载失败 / 过慢？**

项目已内置中国区镜像配置（`app/.npmrc`），若仍失败可手动设置环境变量：

```bash
# Windows PowerShell
$env:ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"
$env:ELECTRON_BUILDER_BINARIES_MIRROR="https://npmmirror.com/mirrors/electron-builder-binaries/"
cd app
npm install
```

> `ELECTRON_MIRROR` 加速 electron 二进制下载；`ELECTRON_BUILDER_BINARIES_MIRROR` 加速 electron-builder 打包工具（NSIS / winCodeSign 等）下载。

**Go 模块拉取失败？**

```bash
go env -w GOPROXY=https://goproxy.cn,direct
```

**安全说明**

`proxy-core.exe` 默认只绑定 `127.0.0.1`，请勿改为 `0.0.0.0`，以避免成为开放代理。

---

## License

[MIT](./LICENSE)