# Third-Party Notices

ProxyPilot 包含或使用以下第三方组件。各自的版权归其作者所有，并依据各自的许可证条款授权使用。

## 内嵌组件（打包进安装包）

### ip2region v3（数据文件与 Go 库）

- 组件：IPv4 地区数据库（`proxy-core/geoip/data/ip2region_v4.xdb`）及其 Go 绑定库（`github.com/admpub/ip2region/v3`）
- 用途：离线 IP 地区解析（国家/省/市）
- 许可证：[Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0)
- 来源：<https://github.com/lionsoul2014/ip2region>、<https://github.com/admpub/ip2region>
- 版权声明：依据 Apache License 2.0 第 4 节要求保留上述来源与许可证归属

## 运行时同步数据（不随安装包分发）

### Loyalsoldier/surge-rules（智能分流规则列表）

- 组件：直连名单（direct.txt）与代理名单（gfw.txt）
- 用途：智能分流的默认规则源（运行时从 HTTPS 同步，本地缓存）
- 许可证：[MIT](https://github.com/Loyalsoldier/surge-rules/blob/master/LICENSE)
- 来源：<https://github.com/Loyalsoldier/surge-rules>

## 开发依赖

其余第三方依赖（Golang `go.mod`、前端 `package.json` 所列）均通过对应包管理器引入，
许可证信息见各依赖的 `LICENSE` 文件。本项目自身以 [MIT](./LICENSE) 许可证发布。
