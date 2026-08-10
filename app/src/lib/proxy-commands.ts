import type { ProxyNode } from '@/types'
import type { Platform } from '@/api'

export interface ProxyCommandSet {
  platform: Platform
  label: string
  /** 设置代理环境变量的命令 */
  env: string
  /** 使用代理访问示例站点的 curl 命令 */
  curl: string
}

export interface GatewayCommandSet {
  platform: Platform
  label: string
  /** 设置网关环境变量的命令（http_proxy/https_proxy 走 HTTP 出口，all_proxy 走 SOCKS5 出口） */
  env: string
  /** 使用 HTTP 出口访问示例站点的 curl 命令 */
  curlHttp: string
  /** 使用 SOCKS5 出口访问示例站点的 curl 命令 */
  curlSocks5: string
}

/** 生成代理 URL，如 http://user:pass@host:port */
export function proxyUrl(n: ProxyNode): string {
  const auth = n.username ? `${encodeURIComponent(n.username)}:${encodeURIComponent(n.password ?? '')}@` : ''
  return `${n.protocol}://${auth}${n.host}:${n.port}`
}

/** 生成 curl 的代理参数，如 -x http://user:pass@host:port */
function curlProxyArg(url: string): string {
  if (url.startsWith('socks5://')) {
    // --socks5-hostname 不接受 user:pass@ 内联认证，需用 -x socks5h:// 形式
    // （socks5h:// 等价于 --socks5-hostname，且 -x 支持内嵌用户名密码）
    return `-x ${url.replace(/^socks5:\/\//, 'socks5h://')}`
  }
  return `-x ${url}`
}

/** 根据平台生成对应的代理使用命令 */
export function buildCommands(n: ProxyNode): ProxyCommandSet[] {
  const url = proxyUrl(n)
  const isSocks = n.protocol === 'socks5'
  const curl = `curl ${curlProxyArg(url)} https://example.com`

  // SOCKS5 是传输层代理，需同时设置 http_proxy/https_proxy/all_proxy，
  // 否则部分工具（git、npm 等）只认 http_proxy/https_proxy 而不认 all_proxy
  const envVars = isSocks ? ['http_proxy', 'https_proxy', 'all_proxy'] : ['http_proxy']

  return [
    {
      platform: 'win32',
      label: 'Windows PowerShell',
      env: envVars.map((v) => `$env:${v} = "${url}"`).join('; '),
      curl,
    },
    {
      platform: 'win32',
      label: 'Windows CMD',
      env: envVars.map((v) => `set ${v}=${url}`).join(' && '),
      curl,
    },
    {
      platform: 'darwin',
      label: 'macOS / Linux (bash/zsh)',
      env: `export ${envVars.map((v) => `${v}=${url}`).join(' ')}`,
      curl,
    },
  ]
}

/**
 * 根据平台生成对应的网关使用命令。
 * 网关同时提供 HTTP 与 SOCKS5 两个出口：
 * - http_proxy/https_proxy 指向 HTTP 出口（如 http://127.0.0.1:7890）
 * - all_proxy 指向 SOCKS5 出口（如 socks5://127.0.0.1:7891）
 */
export function buildGatewayCommands(httpUrl: string, socks5Url: string): GatewayCommandSet[] {
  const httpEnv = ['http_proxy', 'https_proxy']
  const socksEnv = ['all_proxy']
  const curlHttp = `curl ${curlProxyArg(httpUrl)} https://example.com`
  const curlSocks5 = `curl ${curlProxyArg(socks5Url)} https://example.com`

  return [
    {
      platform: 'win32',
      label: 'Windows PowerShell',
      env: [...httpEnv.map((v) => `$env:${v} = "${httpUrl}"`), ...socksEnv.map((v) => `$env:${v} = "${socks5Url}"`)].join('; '),
      curlHttp,
      curlSocks5,
    },
    {
      platform: 'win32',
      label: 'Windows CMD',
      env: [...httpEnv.map((v) => `set ${v}=${httpUrl}`), ...socksEnv.map((v) => `set ${v}=${socks5Url}`)].join(' && '),
      curlHttp,
      curlSocks5,
    },
    {
      platform: 'darwin',
      label: 'macOS / Linux (bash/zsh)',
      env: `export ${[...httpEnv.map((v) => `${v}=${httpUrl}`), ...socksEnv.map((v) => `${v}=${socks5Url}`)].join(' ')}`,
      curlHttp,
      curlSocks5,
    },
  ]
}