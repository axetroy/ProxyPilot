# 常见问题（FAQ）

ProxyPilot 开发、构建与使用中的常见问题。产品说明见 [README.md](./README.md)，
开发指南见 [DEVELOPMENT.md](./DEVELOPMENT.md)。

## 目录

- [常见问题（FAQ）](#常见问题faq)
  - [目录](#目录)
  - [Electron 二进制下载失败 / 过慢？](#electron-二进制下载失败--过慢)
  - [Go 模块拉取失败？](#go-模块拉取失败)
  - [npm install 失败？](#npm-install-失败)
  - [macOS 打开应用提示已损坏 / 无法验证开发者？](#macos-打开应用提示已损坏--无法验证开发者)
  - [代理端口被占用怎么办？](#代理端口被占用怎么办)
  - [安全说明](#安全说明)

## Electron 二进制下载失败 / 过慢？

项目已内置中国区镜像配置（`app/.npmrc`），若仍失败可手动设置环境变量：

```bash
# Windows PowerShell
$env:ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"
$env:ELECTRON_BUILDER_BINARIES_MIRROR="https://npmmirror.com/mirrors/electron-builder-binaries/"
cd app
npm install
```

> `ELECTRON_MIRROR` 加速 electron 二进制下载；`ELECTRON_BUILDER_BINARIES_MIRROR` 加速
> electron-builder 打包工具（NSIS / winCodeSign 等）下载。

## Go 模块拉取失败？

```bash
go env -w GOPROXY=https://goproxy.cn,direct
```

## npm install 失败？

- 确认使用 Node.js 18+ 与 npm 9+；
- `npm install` 会触发 `postinstall` 的 patch-package（对 electron-updater 应用自动更新补丁），
  请勿跳过 postinstall 脚本；
- 若残留旧依赖，可删除 `app/node_modules` 与 `app/package-lock.json` 后重新安装。

## macOS 打开应用提示已损坏 / 无法验证开发者？

未签名/未公证的 macOS 产物会被 Gatekeeper 拦截，按下列方式之一打开：

1. 右键点击 `ProxyPilot.app` → 选择「打开」→ 再次确认「打开」；或
2. 终端执行 `xattr -dr com.apple.quarantine /Applications/ProxyPilot.app` 去除隔离属性。

> 若已配置 Apple Developer 证书签名 + 公证，用户可直接双击打开，无需上述操作。
> 详见 [DEVELOPMENT.md](./DEVELOPMENT.md#macos-签名与公证)。

## 代理端口被占用怎么办？

网关会自动向后顺延端口，实际绑定端口以界面 Dashboard 或 `/api/status` 返回的
`httpProxyBind` / `socks5ProxyBind` 为准。也可在「设置」页修改 `proxy_port` 后重启网关。

## 安全说明

- `proxy-core.exe` 默认只绑定 `127.0.0.1`，请勿改为 `0.0.0.0`，以避免成为开放代理。
- 管理 API 需要 `X-Token` 鉴权（token 每次启动随机生成），请勿泄露。
- 对外订阅服务常驻监听但默认关闭（关闭时返回 404），且仅本机监听；对外暴露需显式配置，请勿将管理 API 一同暴露。
- 代理订阅仅用于你拥有合法授权或明确许可的目的；请勿以任何形式向第三方转售、共享订阅或节点。
- 智能分流规则源允许配置 http(s) 来源，仅作文本拉取与域名白名单校验，绝不执行远程内容。
- 若使用软件遇到与本地法律冲突的场景，请停止使用，并遵守所在司法辖区的法律法规。
