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

url 支持 http(s) 订阅地址与 `file://` 本地文件（如 `file:///C:/path/list.txt`），

由 Fetcher 按 scheme 分发：http/https 走 HTTP 下载，file 读取本地文件。

手动抓取（立即测试）：

```
POST /api/subscription/:id/refresh
```

同步抓取该订阅并返回本次结果摘要：

```json
{ "id": 1, "total": 120, "added": 3 }
```

`total` 为本次解析出的节点总数，`added` 为相对池中已有的新增节点数；
`total` 为 0 表示订阅内容解析为空（可能无效）。前端「抓取」按钮与
新增订阅后的自动抓取均以此摘要反馈结果。

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

参数（三选一）：

```json
{ "id": 1 }              // 检测单个节点（同步返回结果）
{ "ids": [1, 2, 3] }     // 批量检测选中节点（异步，202 返回 started/total）
{}                       // 检测全部节点（异步）
```

---

# 6.4.1 批量删除节点

POST

```
/api/proxy/batch-delete
```

参数：

```json
{ "ids": [1, 2, 3] }
```

批量删除节点池中的多个节点（订阅导入大量节点后的清理场景）；
若删除的包含固定出口节点，自动取消指定，策略处理与单个删除一致。
返回 `{ "deleted": n }`。

---

# 6.4.2 节点检测历史（延迟趋势曲线）

GET

```
/api/proxy/:id/history?limit=60
```

返回单个节点最近的检测历史，按时间正序（旧→新），供前端绘制延迟趋势曲线：
`check_history` 表每条记录含 success / latency / createdAt；检测失败记录
latency 为 0。limit 默认 60、上限 500。节点详情弹窗用纯 SVG 手绘 Sparkline：
成功点连线、失败点断开并在底部以红点标注。

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
"country":"美国",
"province":"",
"city":"",
"lastCheck":"2026-08-08"
}

```

--- 

## 节点地区解析（离线 GeoIP）

- 数据源使用 ip2region（Apache-2.0）IPv4 xdb 数据，经 `go:embed` 随 proxy-core
  二进制内嵌（约 11MB），**完全离线、不依赖任何外部 API**；全内存加载（10 微秒级
  查询）、单例并发安全，天然适配无 CGO 的 goreleaser 六目标交叉编译。IPv6 数据文件
  过大（36MB+）而代理节点几乎全为 IPv4，暂不内置，IPv6 节点地区留空。
- 解析时机：节点检测完成时（`pool.evalOne`）解析并写入 `proxy_nodes`
  的 `country/province/city` 三列（已有库启动时自动 `ALTER TABLE` 迁移补列）。
  优先级：匿名性探测成功时的**节点出口 IP**（`ProxiedIP`，最准确，代表真实代理
  所在地）→ 节点 host（IP 直接查，域名做本地 DNS 解析后查）。
- 数据口径：国内数据精确到城市（中文），海外数据通常只有国家（英文，
  部分运营商/机构出现在城市场位），保留 IP 段统一标记为 `Reserved`。
- UI：代理池表格新增「地区」列展示 `国家 · 省份 · 城市`（连续同名段自动合并），
  搜索框可按主机名/协议/地区关键字过滤。

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

## 出口路由策略

网关出口采用集中策略管理（「出口路由」页面），持久化在 settings 表 `egress_strategy`，
由 `GET/PUT /api/egress` 专门接口管理（固定节点沿用 `pinned_proxy_id` 与 `PUT/DELETE /api/proxy/pin`）。

六种策略（选择器 `scheduler.Selector.NextForHost` 按策略分发，切换即时生效并持久化，重启自动恢复）：

1. **fixed 固定出口**：使用用户指定的节点，节点存活时全部流量（HTTP/HTTPS/SOCKS5 不区分）固定走它，
   不再按评分自动选择。指定节点死亡时临时回退智能加权，复活后自动恢复固定；
   节点被删除/自动淘汰后自动取消指定，避免悬空引用。指定节点跨协议使用是安全的：
   `validator.ConnectTCP` 会按节点自身协议选择握手方式（SOCKS5 握手 / HTTP CONNECT）。
2. **best 最高评分**：每次从存活节点中选择评分最高者（叠加失败惩罚）。
3. **random 随机可用**：从存活节点中随机挑选，同一域名在粘性窗口（10min）内保持稳定，避免频繁更换出口 IP。
4. **weighted 智能加权（默认）**：`weight = score/latency`，叠加失败惩罚窗口 30s（`0.5^failures`），
   域名粘性绑定 10min，是默认最均衡的策略。
5. **round-robin 轮询**：存活节点按 ID 顺序轮流使用（协议族内过滤 + 回退），均衡负载，不做粘性。
6. **chain 代理链路**：客户端依次经过多个代理节点到达目标（客户端 → n0 → n1 → … → target），
   提升匿名性与绕过能力。链路由用户配置（`proxy_chains` 表，有序节点 ID 列表，可按需启用多条）。
7. **auto-chain 自动链路**：无需手动指定节点，按配置的层数 N 与每层选择策略
   （weighted / random / best），每次连接自动从存活节点中挑选 N 个互不相同的节点组成链路
   （客户端 → nodes[0] → … → target），节点池变化即时生效、节点失效自动换新。

### 代理链路（chain）

- 链路连接由 `validator.ConnectChain` 实现：逐跳在上层隧道之上按该跳节点协议握手
  （HTTP CONNECT / HTTPS TLS+CONNECT / SOCKS5 手写 RFC1928/1929），握手目标是下一跳节点地址，
  最后一跳握手到目标地址，返回直达 target 的隧道。链路中各跳节点协议可混合；
  SOCKS5 握手在已有连接上完成（x/net/proxy 的 Dialer 无法复用已有 conn，故手写）。
- chain 策略下网关 `upstreamViaChain` 从**已启用**的链路中随机挑一条「全部节点存活」的链建立隧道，
  失败（任一跳不可达）则尝试下一条；链上任一节点缺失或非存活则该链不可用。
  链列表带 30s TTL 缓存（避免每次请求查询 SQLite），增删/启停最多延迟 30s 生效。
- 链 CRUD 由 `/api/chain`（`GET /api/chains`、`POST /api/chain`、`PUT /api/chain/:id`、`DELETE /api/chain/:id`）管理，
  节点校验：全部存在于节点池、去重保序；链默认停用，需在界面显式启用。
- 限制：SOCKS5 UDP 中继不支持链路承载，chain 策略下 UDP 流量回退到单跳 SOCKS5 节点。

### 自动链路（auto-chain）

- 与手动 chain 不同，auto-chain **不需要用户指定节点**：按配置的层数 N 与每层选择策略，
  由 `scheduler.Selector.SelectChain(N, strategy)` 从存活节点中挑选 N 个**互不相同**的节点
  （每层按 weighted 加权 / random 随机 / best 最高分挑选，叠加失败惩罚；存活节点不足 N 时
  按实际存活数建链，无存活节点时报错）。
- 链路连接同样走 `validator.ConnectChain`（逐跳握手：HTTP CONNECT / HTTPS TLS+CONNECT / SOCKS5），
  返回的节点顺序即链路跳顺序。每层不限制协议，各跳节点协议可混合。
- 配置项：`chain_hops`（层数，1-8，默认 2）与 `chain_selection`（每层策略，
  weighted / random / best，默认 weighted），由 `/api/egress` 接口管理（PUT 时可携带
  `chainHops` / `chainSelection`，持久化在 settings 表，重启自动恢复）。
- 一键测试：`POST /api/egress/auto-chain/test` 按当前配置从存活节点中挑选节点，
  复用 `validator.TestChain` 逐跳测试，返回与手动链路测试相同的 `ChainTestResult` 结构
  （每跳 key/latency/error），供前端展示整链连通性与每跳延迟。
- 网关每次请求现选节点（无缓存），节点池增删/状态变化即时生效；链路建立失败时整体报错，
  下一次请求重新挑选节点。适配场景：不想维护具体链路、希望节点失效自动换新。
- 限制：同 chain 策略，UDP 流量回退到单跳 SOCKS5 节点；auto-chain 为动态现选，
  无法像手动链路那样预先固定并测试整条链（测试仅反映当前一次挑选）。

固定出口（Pin）与策略联动：`Pin` 自动切到 fixed 策略，`Unpin` 恢复智能加权；
指定节点被删除时选择器自动清除（内存），重启时清理持久化。
`Pinned()` 仅在策略为 fixed 时返回固定节点（非 fixed 策略下界面不显示"固定出口已指定"），
但 `PinnedID()` 保留指定，切回 fixed 策略时自动恢复。
`/api/status` 返回 `pinnedNode` 供前端展示当前固定出口。

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

* TCP（CONNECT）
* UDP（UDP ASSOCIATE）
* DNS代理

UDP ASSOCIATE：本地为每个会话分配一个仅绑定 127.0.0.1 的 UDP 中继端口，
客户端经该端口收发 SOCKS5 UDP 数据报（RSV|FRAG|ATYP|DST.ADDR|DST.PORT|DATA，
不支持分片）。数据报必须经代理池中的 **SOCKS5 节点**转发（HTTP/HTTPS 节点只
支持 CONNECT 隧道，无法承载 UDP，此时返回 `0x07` 命令不支持）；转发依赖上游
SOCKS5 节点同样支持 UDP ASSOCIATE。会话随 TCP 控制连接关闭而结束。

---

## 流量统计

网关在建立每条出站连接时包装连接对象（`countedConn`），对 `Read`/`Write`
按字节实时累计上传/下载，并按出口维度分桶：

* **节点**：单跳策略（fixed/best/random/weighted/round-robin）命中的节点。
* **链路**：chain 按链路名分桶；auto-chain 每次现选节点、无固定名称，统一记
  为 `auto-chain`。
* **直连**：智能分流命中「直连」的目标（不经节点池）。

统计仅存于内存（本次启动累计，进程重启清零，不落盘）。通过
`GET /api/traffic` 读取快照（`total` / `byNode` / `byChain` / `direct`，
`byNode` 只含节点 ID，前端用节点池映射为地址）。

限制：

* UDP 中继按数据报转发，不走连接对象，暂不统计。
* 普通 HTTP 转发的请求体上行与响应体下行都经包装连接计数（走 `http.Transport`
  的 `DialContext`），无需在各转发路径单独埋点。

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

## 数据库瘦身（手动维护）

检测历史（`check_history`）是唯一持续增长的写日志：每轮检测每个节点插入一条，
长时间运行后数据库文件会缓慢膨胀。代理池提供手动瘦身能力（设置页「数据管理」）：

- 配置项 `history_retention_days`（默认 7 天）：早于该天数的检测历史视为过期。
- `GET /api/db/status`：返回数据库文件大小、检测历史总条数、当前可清理条数。
- `POST /api/db/compact`：删除早于保留天数的检测历史，然后
  `PRAGMA wal_checkpoint(TRUNCATE)` + `VACUUM` 收缩物理文件，返回删除条数与瘦身前/后大小。
- 仅影响检测历史；节点、订阅、设置均不受影响。

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
构建时会生成更新元数据文件（`latest*.yml`），CI 统一改名为「平台-架构」格式
（`windows-x64.yml` / `windows-arm64.yml` / `linux-x64.yml` / `darwin-x64.yml` /
`darwin-arm64.yml`）；electron-updater 已被 patch（`patches/electron-updater+6.8.9.patch`，
由 patch-package 在 `npm install` 时自动应用）按本机平台+架构读取对应文件，
避免同名 yml 互相覆盖导致下载错架构安装包（v0.1.11 事故）。
dist 脚本均带 `--publish never`，上传统一由 `softprops/action-gh-release` 完成。

macOS 的更新包是 **zip**（`MacUpdater` 依赖 Squirrel.Mac，只消费 `.zip`：
`findFile(files, "zip", ["pkg", "dmg"])` 找不到 zip 会抛 `ERR_UPDATER_ZIP_FILE_NOT_FOUND`，
dmg 被显式排除、不能作为更新源，仅供手动安装），因此 `electron-builder.cjs` 的
mac target 为 `['dmg', 'zip']`——两者都随 Release 上传，`latest-mac.yml` 同时列出
两个文件，自动更新只取 zip。

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
  导致「旧应用被删、新版装到别处找不到」。主进程从注册表 `Software\<APP_GUID>`
  （APP_GUID = UUID.v5(appId, electron-builder 固定命名空间)，兜底查卸载键）的
  `InstallLocation`（HKCU/HKLM）读出旧安装目录，注入 `autoUpdater.installDirectory`
  使 `/D=` 指向旧目录，实现原地覆盖更新。
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
  `gracefulShutdown()`（幂等）：还原代理 → 停核心 → 再次 `app.quit()`。
  **不能用 `app.exit(0)`**：它会跳过 before-quit/will-quit/quit 事件链，导致
  electron-updater 的 `autoInstallOnAppQuit`（下载完成后退出应用自动安装）失效；
  二次 `app.quit()` 时 `before-quit` 因清理已完成而放行，事件链走到 `quit` 事件，
  electron-updater 的 onQuit 监听器随即 spawn 安装器。另带 15s 看门狗兜底
  （清理挂起时强制退出，该路径跳过自动安装，仅作最后保障）。

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

# 20. 智能分流设计（网关内规则匹配）

## 定位

在网关入口（HTTP CONNECT / SOCKS5 隧道）解析出目标 host 后，先做规则匹配，
决定该连接是「直连」还是「经节点池代理」。这是 Clash / Surge 等主流客户端的
「规则分流」思路，**不是**浏览器 PAC 协议（`FindProxyForURL` 脚本交给系统代理）。

为什么不用系统级 PAC：

| 维度 | 网关内分流（本方案） | 系统级 PAC |
| --- | --- | --- |
| 生效范围 | 所有走网关的应用（HTTP + SOCKS5 统一入口） | 仅遵守系统代理的应用 |
| IP 级判断 | 可用 geoip 离线库（纯 IP 目标也能判） | 只能域名规则，IP 需 dnsResolve（受本地污染影响） |
| 匹配性能 | Go map / 哈希，毫秒级 | 浏览器 JS 解释执行 |
| 改动范围 | 只改 proxy-core | proxy-core + Windows/macOS/Linux 三平台 system-proxy |
| 网关崩溃时 | 直连也失效（本机进程，可接受） | 直连仍通 |

## 分流决策

连接建立时按以下优先级匹配目标 host（首条命中即生效）：

1. **本机 / 局域网**（`127.0.0.0/8`、`10.0.0.0/8`、`172.16.0.0/12`、`192.168.0.0/16`、
   `169.254.0.0/16`、`::1`、`*.local` 及无点单标签 host）→ 直连
2. **手动代理名单**（用户自定义域名，强制走代理）→ 走节点池
3. **手动直连名单**（用户自定义域名，强制直连）→ 直连
4. **`.cn` 顶级域**（host 以 `.cn` 结尾）→ 直连
5. **纯 IP 目标**（客户端未给域名）→ 白名单按 geoip 判定 CN → 直连、否则走代理；
   黑名单默认直连（纯 IP 无法命中域名代理名单，直接放行更符合「默认直连」语义）
6. **命中代理名单**（gfw 列表，同步自上游）→ 走节点池
7. **命中直连名单**（direct 列表，同步自上游）→ 直连
8. **默认动作** → 白名单走节点池 / 黑名单直连

优先级设计说明：代理名单置于直连名单之前，保证「该走代理的绝不被误判直连」——
直连被墙域名会因 DNS 污染解析到假 IP 而失败；反之即使代理名单误伤（把可直连域名
放进代理），也只是多消耗节点流量，不会打不开。**手动规则**置于自动匹配之前，让
用户能明确覆盖同步名单与 `.cn` / geoip 判断（如把 `.cn` 域名强制走代理、把误入
代理名单的域名强制直连）；手动代理优先于手动直连，与自动名单语义一致。域名名单
按**后缀匹配**（子域名命中父域条目，如 `app.baidu.com` 命中 `baidu.com`），与
Clash/Surge 的 DOMAIN-SUFFIX 语义一致。

## DNS 语义

- **直连路径**：网关用本机 DNS 解析。命中直连的均为大陆 / 局域网 / 未被墙域名，
  本机 DNS 干净，无污染问题。
- **代理路径**：目标域名原样交给上游节点，由节点侧解析（远程解析，等价 socks5h），
  避开本地 DNS 污染。

## 规则来源与同步

默认同步 Loyalsoldier/surge-rules（每日自动更新，每行一个域名的纯文本）：

- 直连名单：`release/direct.txt`
- 代理名单：`release/gfw.txt`

主源 `raw.githubusercontent.com`，备用镜像 `cdn.jsdelivr.net`（约 12h 延迟），
按顺序尝试直到成功。同步失败保留上次缓存，并用内置兜底列表（go:embed 最小集，
覆盖常见国内域名与常见被墙域名）保证离线可用。

同步周期默认 24h（`pac_refresh_interval`），启动时异步同步一次，之后按周期刷新。
规则解析只做**域名白名单校验**（小写、字符集受限、长度 ≤ 255），绝不允许把远程
内容作为代码执行。规则源 URL 允许用户配置 http(s) 源（如自建/内网列表，仅作文本
拉取），默认源固定为 Loyalsoldier/surge-rules（HTTPS）。

## 配置项

持久化在 SQLite `settings` 表，经 `/api/pac-config` 专门接口校验与持久化（不
进 `/api/settings` 通用表单，避免与通用设置混在一起）：

| key | 默认 | 说明 |
| --- | --- | --- |
| `pac_enabled` | `1` | 分流开关（关闭时全部走节点池） |
| `pac_mode` | `whitelist` | `whitelist`（默认走代理）/ `blacklist`（默认直连，命中代理名单才走代理） |
| `pac_direct_urls` | direct.txt 主源+镜像 | 逗号分隔，按序尝试 |
| `pac_proxy_urls` | gfw.txt 主源+镜像 | 逗号分隔，按序尝试 |
| `pac_refresh_interval` | `24h` | 规则自动刷新周期 |
| `pac_custom_direct` | 空 | 手动直连名单（域名，逗号分隔，优先级最高） |
| `pac_custom_proxy` | 空 | 手动代理名单（域名，逗号分隔，优先级最高） |

## 接口

- `GET /api/pac-config`：返回分流配置与规则同步状态（直连/代理规则数、最近同步
  时间、最近错误、是否同步中、手动规则名单）
- `PUT /api/pac-config`：更新分流配置（`enabled` / `mode` / `urls` / `refresh`，
  以及 `customDirect` / `customProxy` 手动名单**整表覆盖**，提交 `[]` 清空）；
  规则源（urls）变化后自动异步触发一次同步，失败/进行中通过后续 GET 的
  `syncError` / `syncing` 暴露
- `POST /api/pac/sync`：立即同步规则

## 模块结构

```
proxy-core/rule/       # 规则管理（RuleManager）
  rule.go              # 内存规则集（direct / proxy 两个域名集合）+ Match(host)
  sync.go              # 拉取（主源→镜像）→ 解析 → 白名单校验 → 写缓存
  builtin.go           # go:embed 内置兜底列表
gateway/               # 分流接入点：gateway.go 在建立上游连接前调用 RuleManager.Shunt
```

规则缓存文件与数据库同目录（`pac_rules.json`），格式为 JSON（域名数组 + 同步时间），
启动时加载，避免每次启动重新拉取。

---

# 21. 最终架构总结

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
