# ProxyPilot

# Golang + Electron 架构设计白皮书

**版本：V1.0**
**架构定位：桌面端智能代理管理与代理网关系统**

---

# 1. 项目定位

## 1.1 产品定义

ProxyPilot 是一款桌面端代理管理软件。

核心能力：

1. 代理订阅管理；
2. 代理节点采集；
3. 代理可用性检测；
4. 代理质量评分；
5. 自动维护代理池；
6. 提供本地 HTTP / HTTPS / SOCKS5 代理服务；
7. 为本机应用提供统一代理出口。

产品形态：

```
用户应用

Chrome
Python
Node.js
IDE
其他客户端

        |
        |
        v

localhost Proxy

        |
        |
        v

ProxyPilot Client

        |
        |
        v

Internet Proxy Pool
```

---

# 2. 总体架构

采用：

> Electron + Golang Core 分离架构

```
┌──────────────────────────────────────┐
│             Electron UI               │
│                                      │
│ React + TypeScript                   │
│                                      │
│ - Dashboard                          │
│ - Proxy Pool                         │
│ - Subscription                       │
│ - Logs                               │
│ - Settings                           │
└──────────────────┬───────────────────┘
                   │
                   │ HTTP API
                   │ WebSocket
                   │
┌──────────────────▼───────────────────┐
│           Golang Core Engine          │
│                                      │
│  API Server                          │
│  Proxy Collector                     │
│  Proxy Validator                     │
│  Proxy Pool Manager                  │
│  Proxy Scheduler                     │
│  Proxy Gateway                       │
│                                      │
└───────────────┬──────────────────────┘
                │
        ┌───────▼────────┐
        │ SQLite Database │
        └────────────────┘

                |
                |
        External Proxy Nodes

```

---

# 3. 技术选型

## 3.1 Electron 层

职责：

负责用户交互。

技术：

```
Electron

+
React 18

+
TypeScript

+
Vite

+
Zustand

+
Tailwind CSS

+
Radix UI

```

---

功能：

* 软件窗口；
* 配置管理；
* 节点展示；
* 数据可视化；
* 日志展示；
* 用户操作。

---

## 3.2 Golang Core

职责：

负责所有网络能力。

技术：

```
Go 1.26+

Gin

Gorilla WebSocket

SQLite

go-socks5

net/http

context

sync

```

---

负责：

* 网络连接；
* 代理检测；
* TCP代理；
* SOCKS5；
* 调度算法；
* 数据管理。

---

# 4. 进程架构

最终运行：

```
ProxyPilot.exe

 |
 |
 +-- Electron Process
 |
 |
 +-- proxy-core.exe

```

---

启动流程：

```
用户打开软件

        |
        v

Electron启动

        |
        v

启动proxy-core

        |
        v

Go监听:

127.0.0.1:17890

        |
        v

Electron连接

        |
        v

加载界面

```

---

关闭流程：

```
Electron退出

        |

通知Go停止

        |

释放代理端口

        |

退出core

```

---

# 5. Electron 与 Golang 通信设计

采用：

## HTTP API + WebSocket

通信：

```
Electron

     |
     |
 HTTP REST API

     |

Golang


     |

 WebSocket

     |

实时事件

```

---

# 6. API设计

## 6.1 系统状态

GET

```
/api/status
```

返回：

```json
{
 "running":true,
 "proxyCount":500,
 "aliveCount":120,
 "currentIP":"1.2.3.4"
}

```

---

# 6.2 订阅管理

获取：

```
GET /api/subscriptions
```

新增：

```
POST /api/subscription

```

数据：

```json
{
"name":"proxy-source",
"url":"https://xxx.com/list",
"interval":3600
}

```

---

# 6.3 节点列表

GET

```
/api/proxies
```

返回：

```json
[
 {
 "host":"1.1.1.1",
 "port":8080,
 "protocol":"http",
 "latency":120,
 "score":95,
 "status":"alive"
 }
]

```

---

# 6.4 手动检测

POST

```
/api/proxy/check
```

---

# 6.5 启动代理服务

POST

```
/api/gateway/start

```

参数：

```json
{
"http":"127.0.0.1:7892",
"socks5":"127.0.0.1:7892"
}

```

---

# 6.6 WebSocket

地址：

```
ws://127.0.0.1:17890/ws

```

推送：

## 日志

```json
{
"type":"log",
"level":"info",
"message":"proxy checked"
}

```

## 检测进度

```json
{
"type":"progress",
"current":50,
"total":500
}

```

---

# 6.7 对外订阅服务

将代理池中**存活的节点**作为订阅源对外提供，其他设备 / 客户端
（Clash、v2ray 等）通过订阅 URL 拉取节点列表。

## 独立监听端口

订阅服务与主 API 分离，默认：

```
127.0.0.1:17891
```

- 默认仅监听本机；如需局域网设备订阅，在前端「设置 → 订阅服务」中把监听地址
  改为 `0.0.0.0:17891`（修改后需重启应用生效）。
- 独立端口避免把带鉴权中间件的管理 API（17890）一起暴露出去。
- 订阅开关关闭时返回 404；密钥错误时返回 401。

## 订阅 URL

```
GET /sub/<subscription_token>?format=base64|plain
```

- token 位于 path（v2ray 订阅风格），独立于管理 API 的 session token，
  首次启动随机生成并持久化，可在设置页一键重置。
- 默认返回 base64 编码的节点列表（与解析路径互逆）；`format=plain` 返回明文，
  每行一个 `protocol://user:pass@host:port`。
- 内容仅包含当前存活的节点，按 分数 → 延迟 → ID → host 排序。

## 配置接口

```
GET /api/subscription   # 返回 {enabled, listen, token, url}
PUT /api/subscription   # body: {enabled?, listen?, resetToken?}
```

持久化到 SQLite settings 表：`subscription_enabled`、`subscription_listen`、
`subscription_token`。

---

# 7. Golang Core模块设计

目录：

```
proxy-core


├── main.go


├── api

│   ├── http.go

│   └── websocket.go


├── collector

│   ├── subscription.go

│   └── fetcher.go


├── parser

│   └── proxy_parser.go


├── validator

│   └── checker.go


├── pool

│   ├── manager.go

│   └── score.go


├── scheduler

│   └── selector.go


├── gateway

│   ├── http.go

│   ├── https.go

│   └── socks5.go


├── storage

│   └── sqlite.go


└── config


```

---

# 8. Proxy Collector设计

## 功能

负责代理来源。

支持：

```
URL订阅

API

文件

手工输入

```

---

流程：

```
Subscription

      |

Fetcher

      |

Parser

      |

Proxy Pool

```

---

# 9. Proxy Validator设计

目标：

判断代理是否可用。

检测流程：

```
代理节点

 |

TCP连接

 |

协议握手

 |

HTTP请求

 |

出口IP检测

 |

评分

 |

入池

```

---

检测指标：

| 指标     | 说明  |
| ------ | --- |
| 连接成功率  | 稳定性 |
| 响应时间   | 速度  |
| 匿名等级   | 安全性 |
| 连续可用时间 | 寿命  |
| 失败次数   | 健康度 |

---

评分模型：

```
Score=

40% 成功率

30% 延迟

20% 稳定性

10% 匿名等级

```

---

# 10. Proxy Pool设计

代理状态：

```
NEW

 |

CHECKING

 |

AVAILABLE

 |

DEAD

```

数据：

```json
{
"id":10001,
"host":"1.1.1.1",
"port":8080,
"type":"http",
"score":90,
"latency":100,
"lastCheck":"2026-08-08"
}

```

---

# 11. Proxy Scheduler设计

负责：

选择出口代理。

策略：

## 最优节点

公式：

```
weight=

score / latency

```

---

## 失败切换

流程：

```
Node A

失败

 |

Node B

失败

 |

Node C

```

---

# 12. Proxy Gateway设计

提供：

## HTTP Proxy

默认：

```
127.0.0.1:7892
```

## 端口占用自动顺延

HTTP 与 SOCKS5 共用同一端口：两种协议通过连接首字节自动识别（SOCKS5 握手以
`0x05` 开头，HTTP 请求以方法名开头），在单一端口上同时提供服务。端口被占用时
整体向后顺延，保证网关总能启动成功。实际绑定的端口通过 `/api/status` 的
`httpProxyBind` / `socks5ProxyBind` 字段返回（两者相同），前端界面据此显示真实地址。

---

## HTTPS CONNECT

支持：

```
CONNECT host:443

```

建立 TCP Tunnel。

---

## SOCKS5

默认与 HTTP 共用同一端口（端口相同即为混合模式）：

```
127.0.0.1:7892

```

支持：

* TCP
* DNS代理

---

# 13. 数据库设计

SQLite：

```
proxypilot.db

```

---

## proxy_nodes

字段：

```
id

host

port

protocol

username

password

latency

score

status

created_at

updated_at

```

---

## subscriptions

```
id

name

url

interval

enabled

last_fetch

```

---

## check_history

```
id

proxy_id

success

latency

error

created_at

```

---

# 14. 安全设计

## API保护

Go启动生成：

```
session token

```

请求：

```
X-Token: xxxx

```

---

## 网络限制

默认：

只监听：

```
127.0.0.1

```

禁止：

```
0.0.0.0

```

避免变成开放代理。

## 订阅服务保护

- 订阅服务独立监听 `127.0.0.1:17891`，默认不对外；对外监听必须由用户显式配置
  `0.0.0.0`，不会随管理 API 一起暴露。
- 订阅密钥独立于 session token，通过 URL 中的 path 传递，
  校验使用常量时间比较，避免时序攻击。
- 订阅开关默认开启，但关闭后立即返回 404，密钥泄露可随时重置。

---

# 15. 日志系统

统一事件：

```
Logger

 |

WebSocket

 |

Electron Console

```

等级：

```
DEBUG

INFO

WARN

ERROR

```

---

# 16. 打包结构

Windows：

```
ProxyPilot.exe

resources/

    proxy-core.exe

    proxypilot.db

```

---

Mac：

```
ProxyPilot.app

Contents/

    Electron

    proxy-core

```

---

Linux：

```
ProxyPilot.AppImage

```

---

# 17. 开发阶段规划

# Phase 1：基础版本（4周）

完成：

✅ Electron框架
✅ Go Core启动管理
✅ HTTP API通信
✅ SQLite
✅ 代理订阅
✅ 节点检测

---

# Phase 2：代理网关（4周）

完成：

✅ HTTP Proxy
✅ HTTPS CONNECT
✅ SOCKS5
✅ 自动选择节点

---

# Phase 3：智能化（6周）

完成：

✅ 节点评分
✅ 自动淘汰
✅ 自动切换
✅ 实时监控

---

# Phase 4：商业化能力

增加：

```
账号系统

云同步

代理市场

团队管理

API服务

```

---

# 18. 更新机制设计（自动更新 / 手动检查 / 下载进度）

## 更新源

GitHub Releases：

```
https://github.com/axetroy/ProxyPilot/releases
```

基于 `electron-updater`（主进程 `app/electron/main/updater.ts`）。
`electron-builder.cjs` 声明 `publish: { provider: 'github', owner, repo }` 后，
构建时会生成更新元数据文件（Windows `latest.yml` / macOS `latest-mac.yml` /
Linux `latest-linux.yml`），随安装包一起被 CI 上传到 Release；
dist 脚本均带 `--publish never`，上传统一由 `softprops/action-gh-release` 完成。

## 流程

```
启动 / 手动检查
     │
     ▼
electron-updater 拉取 latest*.yml
     │
     ├─ 无新版本 ──► 提示「已是最新」（仅手动检查时）
     │
     └─ 有新版本 ──► 自动下载（autoDownload=true）
           │
           ▼
   download-progress 事件
           │  （updater:event 推送到渲染进程，展示进度条与网速）
           ▼
   update-downloaded
           │
           ▼
   通知「更新已就绪」→ 用户点击「立即重启」→ quitAndInstall()
```

## 关键约定

- **自动更新默认开启**：设置持久化在 Electron 主进程 `userData/settings.json`
  （`AppSettings.autoUpdate`，缺省 `true`，与 closeBehavior 同文件）。
  启动后延迟 5s 自动检查一次，发现新版本自动下载。
- **可关闭自动更新**：设置页「软件更新」开关。关闭后不再自动检查/下载，
  但手动检查仍可用；重新开启后立即检查一次。
  下载进行中关闭时无法取消 electron-updater 的下载，但不会再提示安装、
  退出时也不会自动安装。
- **手动检查**：设置页「检查更新」按钮（`updater:check`），以及系统托盘右键菜单的
  「检查更新」入口（窗口隐藏时也会触发）。
- **提醒渠道**：窗口可见时用 Mantine 通知；窗口隐藏（最小化到托盘）时由主进程发
  系统原生通知兜底（Windows 需 `app.setAppUserModelId` 与 appId 一致才能弹 toast）。
  「更新已就绪」的原生通知支持点击，直接 `quitAndInstall()` 重启安装，无需先打开
  主窗口（Windows / macOS 支持点击，Linux 仅展示）。
  托盘图标 tooltip 也会随状态变化（如「正在下载更新 45%」），窗口隐藏时仍可直观
  看到下载进度（`updaterTooltip()`，状态变更通过 `onUpdaterStateChange` 通知主进程）。
- **下载进度**：`download-progress` 事件实时推送（百分比 / 已下载 / 总量 / 网速），
  设置页内嵌进度条，任意页面弹出全局通知（Mantine Notifications）。
  主进程对 download-progress 做 300ms 节流后广播，避免每秒数十次的进度事件洪泛渲染进程。
  全局进度通知用固定 id：首次 `notifications.show` 创建、后续 `notifications.update` 刷新——
  **Mantine v9 的 show 对相同 id 会直接忽略而非更新**，若全部用 show，进度条会停在初始值。
- **安装目录（Windows 关键修复）**：electron-updater 6.8.9 的 NsisUpdater 从不给
  `installDirectory` 赋值，`doInstall` 因而不会传 `/D=` 参数。assisted 安装器
  （`nsis.oneClick:false` + `allowToChangeInstallationDirectory:true`）静默更新时
  会把新版装到 NSIS 默认目录，同时 `uninstallOldVersion` 从注册表卸载旧目录，
  导致「旧应用被删、新版装到别处找不到」。主进程从注册表
  `Uninstall\com.axetroy.proxypilot` 的 `InstallLocation`（HKCU/HKLM）读出旧安装
  目录，注入 `autoUpdater.installDirectory` 使 `/D=` 指向旧目录，实现原地覆盖更新。
- **安装前清理**：`updater:install` 先执行注入的清理钩子（停核心 + 还原系统代理，
  10s 超时兜底）再 `quitAndInstall()`，保证安装器 spawn 时应用已干净退出——
  proxy-core 无文件锁、安装器无需强杀进程、系统代理不残留指向旧网关。
- **开发模式**：`app.isPackaged === false` 时不发起更新检查（状态置为 `dev`），
  避免 electron-updater 误读开发环境配置。
- **IPC 通道**：`updater:get-state` / `updater:check` / `updater:set-auto-update` /
  `updater:install`（invoke），事件推送 `updater:event`；
  preload 通过 `window.proxypilot` 暴露给渲染进程。

---

# 19. 系统代理（一键开关）

将系统 HTTP / HTTPS 代理指向本机网关，浏览器等系统应用无需逐个手动配置。
由 Electron 主进程实现（`app/electron/main/system-proxy.ts`），不依赖 proxy-core。

**平台实现**：

```
Windows 注册表 HKCU\...\Internet Settings（ProxyEnable / ProxyServer / ProxyOverride）
        写入后通过 PowerShell InternetSetOption 刷新，使运行中的应用生效
macOS   networksetup（-setwebproxy / -setsecurewebproxy，应用到所有启用的网络服务）
Linux   GNOME gsettings（org.gnome.system.proxy mode=manual + http/https 代理）
```

**行为约定**：

- **地址来源**：开启时主进程调用核心 `GET /api/status`，取 `httpProxyBind` 作为代理
  目标（端口占用自动顺延后仍是实际绑定值），要求网关已运行。
- **备份 / 还原**：开启前完整备份当前系统代理设置（含原值是否存在）；关闭时按备份
  逐项还原（Windows 还原 ProxyEnable/ProxyServer/ProxyOverride，macOS 还原各服务
  web/secure 代理状态，Linux 还原 mode 与各子键）。备份缺失时兜底为直接关闭代理。
- **持久化**：开关状态、目标地址与备份存入 `userData/settings.json` 的 `systemProxy`
  字段，应用重启后托盘/设置页状态保持一致。
- **退出还原**：应用退出（含更新重启 `quitAndInstall`）时自动关闭并还原系统代理，
  避免网关停止后系统代理指向失效端口导致断网。所有退出路径统一汇入
  `gracefulShutdown()`（幂等）：还原代理 → 停核心 → `app.exit(0)`，并带 15s
  看门狗兜底（清理挂起时强制退出）。

**退出信号覆盖矩阵**（均能触发还原清理）：

```
app.quit()（托盘退出 / Cmd+Q / 关窗且行为=退出 / quitAndInstall） → before-quit ✅
Linux Ctrl+C（SIGINT，被 Electron 劫持转 app.quit）              → before-quit ✅
SIGTERM / SIGHUP（systemd·docker stop / kill / 终端挂断）        → 显式监听 ✅
Windows 关机 / 注销 / 重启（query-session-end）                  → 显式监听 ✅
SIGKILL / 任务管理器强制结束                                    → 无法捕捉（OS 直接杀死）❌
```
- **入口**：设置页「系统代理」卡片（开关 + 状态 + 错误提示，网关未运行时禁用）与
  系统托盘右键菜单「系统代理」复选框（状态变化时重建菜单刷新勾选，失败弹原生通知）。
- **IPC 通道**：`system-proxy:get-state` / `system-proxy:set`（invoke），事件推送
  `system-proxy:event`；preload 通过 `window.proxypilot` 暴露给渲染进程。

---

# 20. 最终架构总结

最终技术栈：

```
              Electron

        React + TypeScript


                 |

        HTTP API + WebSocket


                 |

              Golang


      +----------+----------+

      |                     |

 Proxy Engine          SQLite


      |

 HTTP / HTTPS / SOCKS5


      |

 External Proxy Pool

```
