package rule

import _ "embed"

// 内置兜底列表：离线 / 首次同步失败时兜底使用（go:embed 随二进制编译）。
// 仅覆盖最常用的国内直连域名与常见需代理域名，完整列表由外部同步提供。
//go:embed builtin_direct.txt
var builtinDirect string

//go:embed builtin_proxy.txt
var builtinProxy string