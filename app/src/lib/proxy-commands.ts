import type { ProxyNode } from '@/types'
import type { Platform } from '@/api'

export interface ProxyCommandSet {
  platform: Platform
  label: string
  /** 设置代理环境变量的命令 */
  env: string
}

export interface GatewayCommandSet {
  platform: Platform
  label: string
  /** 设置网关环境变量的命令（HTTP_PROXY/HTTPS_PROXY 走 HTTP 出口，ALL_PROXY 走 SOCKS5 出口） */
  env: string
}

/** 生成代理 URL，如 http://user:pass@host:port */
export function proxyUrl(n: ProxyNode): string {
  const auth = n.username ? `${encodeURIComponent(n.username)}:${encodeURIComponent(n.password ?? '')}@` : ''
  return `${n.protocol}://${auth}${n.host}:${n.port}`
}

/** 所有平台统一设置这三个大写环境变量，兼容 git、npm、curl 等各类工具 */
const ENV_VARS = ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY']

/** 根据平台生成对应的代理使用命令 */
export function buildCommands(n: ProxyNode): ProxyCommandSet[] {
  const url = proxyUrl(n)

  return [
    {
      platform: 'win32',
      label: 'Windows PowerShell',
      env: ENV_VARS.map((v) => `$env:${v} = "${url}"`).join('; '),
    },
    {
      platform: 'win32',
      label: 'Windows CMD',
      env: ENV_VARS.map((v) => `set ${v}=${url}`).join(' && '),
    },
    {
      platform: 'darwin',
      label: 'macOS / Linux (bash/zsh)',
      env: `export ${ENV_VARS.map((v) => `${v}=${url}`).join(' ')}`,
    },
  ]
}

/**
 * 根据平台生成对应的网关使用命令。
 * 网关同时提供 HTTP 与 SOCKS5 两个出口：
 * - HTTP_PROXY/HTTPS_PROXY 指向 HTTP 出口（如 http://127.0.0.1:7892）
 * - ALL_PROXY 指向 SOCKS5 出口（如 socks5://127.0.0.1:7893）
 */
export function buildGatewayCommands(httpUrl: string, socks5Url: string): GatewayCommandSet[] {
  const httpEnv = ['HTTP_PROXY', 'HTTPS_PROXY']
  const socksEnv = ['ALL_PROXY']

  return [
    {
      platform: 'win32',
      label: 'Windows PowerShell',
      env: [...httpEnv.map((v) => `$env:${v} = "${httpUrl}"`), ...socksEnv.map((v) => `$env:${v} = "${socks5Url}"`)].join('; '),
    },
    {
      platform: 'win32',
      label: 'Windows CMD',
      env: [...httpEnv.map((v) => `set ${v}=${httpUrl}`), ...socksEnv.map((v) => `set ${v}=${socks5Url}`)].join(' && '),
    },
    {
      platform: 'darwin',
      label: 'macOS / Linux (bash/zsh)',
      env: `export ${[...httpEnv.map((v) => `${v}=${httpUrl}`), ...socksEnv.map((v) => `${v}=${socks5Url}`)].join(' ')}`,
    },
  ]
}