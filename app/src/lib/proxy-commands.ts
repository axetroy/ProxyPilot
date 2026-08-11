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
  /** 设置 HTTP 代理环境变量的命令（HTTP_PROXY/HTTPS_PROXY 走 HTTP 出口） */
  httpEnv: string
  /** 设置 SOCKS5 代理环境变量的命令（ALL_PROXY 走 SOCKS5 出口） */
  socks5Env: string
}

/** 生成代理 URL，如 http://user:pass@host:port */
export function proxyUrl(n: ProxyNode): string {
  const auth = n.username ? `${encodeURIComponent(n.username)}:${encodeURIComponent(n.password ?? '')}@` : ''
  // SOCKS5 统一使用 socks5h 协议：让代理服务器（节点）负责解析目标域名，
  // 避免客户端本地解析出 IPv6 地址后，节点因不支持 IPv6 目标而连接失败。
  const scheme = n.protocol === 'socks5' ? 'socks5h' : n.protocol
  return `${scheme}://${auth}${n.host}:${n.port}`
}

/** 所有平台统一设置这三个大写环境变量，兼容 git、npm、curl 等各类工具 */
const ENV_VARS = ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY']

/**
 * 根据平台生成对应的代理使用命令。
 * SOCKS5 节点生成的地址使用 socks5h 协议（见 proxyUrl），
 * 让节点负责解析目标域名，避免客户端本地解析出 IPv6 地址导致连接失败。
 */
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
 * 网关同时提供 HTTP 与 SOCKS5 两个出口，分别生成独立命令：
 * - HTTP 命令：HTTP_PROXY/HTTPS_PROXY 指向 HTTP 出口（如 http://127.0.0.1:7892）
 * - SOCKS5 命令：HTTP_PROXY/HTTPS_PROXY/ALL_PROXY 全部指向 SOCKS5 出口
 *   （如 socks5h://127.0.0.1:7892，与 HTTP 共用端口；SOCKS5 同样能处理 HTTP 请求，
 *   因此 HTTP_PROXY/HTTPS_PROXY 也一并设置，兼容只认 HTTP 环境变量的工具）
 *
 * 注意 SOCKS5 地址使用 socks5h 协议：让代理服务器（网关）负责解析目标域名，
 * 避免客户端本地解析出 IPv6 地址后，上游节点因不支持 IPv6 目标而连接失败。
 */
export function buildGatewayCommands(httpUrl: string, socks5Url: string): GatewayCommandSet[] {
  const httpEnvVars = ['HTTP_PROXY', 'HTTPS_PROXY']
  const socksEnvVars = ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY']

  return [
    {
      platform: 'win32',
      label: 'Windows PowerShell',
      httpEnv: httpEnvVars.map((v) => `$env:${v} = "${httpUrl}"`).join('; '),
      socks5Env: socksEnvVars.map((v) => `$env:${v} = "${socks5Url}"`).join('; '),
    },
    {
      platform: 'win32',
      label: 'Windows CMD',
      httpEnv: httpEnvVars.map((v) => `set ${v}=${httpUrl}`).join(' && '),
      socks5Env: socksEnvVars.map((v) => `set ${v}=${socks5Url}`).join(' && '),
    },
    {
      platform: 'darwin',
      label: 'macOS / Linux (bash/zsh)',
      httpEnv: `export ${httpEnvVars.map((v) => `${v}=${httpUrl}`).join(' ')}`,
      socks5Env: `export ${socksEnvVars.map((v) => `${v}=${socks5Url}`).join(' ')}`,
    },
  ]
}