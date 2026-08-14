// app/electron/main/system-proxy.ts
/**
 * 一键系统代理（开关 / 备份 / 还原）。
 *
 * 将系统 HTTP/HTTPS 代理指向本机网关（默认 127.0.0.1:7892，实际端口以
 * proxy-core /api/status 返回的 httpProxyBind 为准），浏览器等系统应用
 * 无需逐个手动配置即可走代理。
 *
 * 平台实现：
 *   - Windows：注册表 HKCU\...\Internet Settings（ProxyEnable / ProxyServer / ProxyOverride）
 *   - macOS：  networksetup（web / secure proxy）
 *   - Linux：  GNOME gsettings（org.gnome.system.proxy）
 *
 * 行为约定：
 *   - 开启前备份当前系统代理设置，关闭或退出应用时完整还原；
 *   - 开启系统代理要求网关已运行（主进程从核心 API 获取实际绑定地址）；
 *   - 开关状态与备份持久化到 userData/settings.json（systemProxy 字段），
 *     应用重启后托盘/设置页仍能反映真实状态；
 *   - 退出应用（含更新重启）时自动关闭并还原，避免网关停止后系统代理指向失效端口。
 */
import { app, BrowserWindow, ipcMain } from 'electron'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { loadAppSettings, saveAppSettings } from './app-settings'

const execFileAsync = promisify(execFile) as (
  cmd: string,
  args: string[],
  options: { timeout?: number; windowsHide?: boolean },
) => Promise<{ stdout: string; stderr: string }>

export interface SystemProxyState {
  enabled: boolean
  /** 系统代理目标地址（网关 HTTP 入口），如 127.0.0.1:7892 */
  endpoint?: string
  error?: string
  changedAt?: number
}

// 开启前的原始系统代理设置，关闭/退出时按平台完整还原
interface WinProxyBackup {
  proxyEnable?: string
  proxyServer?: string
  proxyOverride?: string
  hadProxyEnable: boolean
  hadProxyServer: boolean
  hadProxyOverride: boolean
}

interface MacServiceProxy {
  enabled: boolean
  server: string
  port: string
}

interface MacProxyBackup {
  services: Array<{ name: string; web?: MacServiceProxy; secure?: MacServiceProxy }>
}

interface LinuxProxyBackup {
  mode: string
  http?: { enabled: string; host: string; port: string }
  https?: { enabled: string; host: string; port: string }
  ignoreHosts?: string
}

type ProxyBackup = WinProxyBackup | MacProxyBackup | LinuxProxyBackup

let state: SystemProxyState = {
  enabled: loadAppSettings().systemProxy?.enabled ?? false,
  endpoint: loadAppSettings().systemProxy?.endpoint,
}

// 状态变更监听器（托盘菜单需要重建以刷新勾选状态）
const stateListeners: Array<(state: SystemProxyState) => void> = []

// 读取核心状态所需的 token（由 index.ts 注入，避免模块间循环依赖）
let getToken: (() => string) | null = null

export function onSystemProxyStateChange(listener: (state: SystemProxyState) => void): void {
  stateListeners.push(listener)
}

function setState(patch: Partial<SystemProxyState>): void {
  state = { ...state, ...patch }
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send('system-proxy:event', state)
    }
  }
  for (const listener of stateListeners) {
    try {
      listener(state)
    } catch (e) {
      console.error(`[system-proxy] 状态监听器异常: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
}

export function getSystemProxyState(): SystemProxyState {
  return state
}

/** 托盘菜单使用：当前是否已开启系统代理 */
export function isSystemProxyEnabled(): boolean {
  return state.enabled
}

async function run(cmd: string, args: string[]): Promise<{ stdout: string; stderr: string }> {
  const { stdout, stderr } = await execFileAsync(cmd, args, { timeout: 30000, windowsHide: true })
  return { stdout, stderr }
}

// ---------- Windows（注册表） ----------

const WIN_KEY = 'HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings'

async function winQueryValue(name: string): Promise<string | undefined> {
  try {
    const { stdout } = await run('reg', ['query', WIN_KEY, '/v', name])
    // 输出末行形如 "    ProxyEnable    REG_DWORD    0x1"
    const line = stdout.trim().split(/\r?\n/).pop() ?? ''
    const parts = line.trim().split(/\s+/)
    return parts.length >= 2 ? parts[parts.length - 1] : undefined
  } catch {
    return undefined // 值不存在
  }
}

async function winSetValue(name: string, type: string, value: string): Promise<void> {
  await run('reg', ['add', WIN_KEY, '/v', name, '/t', type, '/d', value, '/f'])
}

async function winDeleteValue(name: string): Promise<void> {
  try {
    await run('reg', ['delete', WIN_KEY, '/v', name, '/f'])
  } catch {
    // 值不存在时忽略
  }
}

/** 通知系统代理设置已变更（部分运行中的应用需要刷新才生效），失败不影响设置本身 */
async function winRefresh(): Promise<void> {
  try {
    const script =
      "Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;public class WinInet{[DllImport(\"wininet.dll\",SetLastError=true)]public static extern bool InternetSetOption(IntPtr h,int o,IntPtr b,int l);}';[WinInet]::InternetSetOption([IntPtr]::Zero,39,[IntPtr]::Zero,0);[WinInet]::InternetSetOption([IntPtr]::Zero,37,[IntPtr]::Zero,0)"
    await run('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script])
  } catch {
    // 忽略
  }
}

async function winReadBackup(): Promise<WinProxyBackup> {
  const [proxyEnable, proxyServer, proxyOverride] = await Promise.all([
    winQueryValue('ProxyEnable'),
    winQueryValue('ProxyServer'),
    winQueryValue('ProxyOverride'),
  ])
  return {
    proxyEnable,
    proxyServer,
    proxyOverride,
    hadProxyEnable: proxyEnable !== undefined,
    hadProxyServer: proxyServer !== undefined,
    hadProxyOverride: proxyOverride !== undefined,
  }
}

async function winApply(host: string, port: number): Promise<void> {
  await winSetValue('ProxyEnable', 'REG_DWORD', '1')
  await winSetValue('ProxyServer', 'REG_SZ', `${host}:${port}`)
  // <local> 让本机地址不走代理，避免代理环路
  await winSetValue('ProxyOverride', 'REG_SZ', '<local>')
  await winRefresh()
}

async function winRestore(backup: WinProxyBackup): Promise<void> {
  const applyValue = async (name: string, type: string, value: string | undefined, had: boolean) => {
    if (had && value !== undefined) {
      await winSetValue(name, type, value)
    } else {
      await winDeleteValue(name)
    }
  }
  await applyValue('ProxyEnable', 'REG_DWORD', backup.proxyEnable, backup.hadProxyEnable)
  await applyValue('ProxyServer', 'REG_SZ', backup.proxyServer, backup.hadProxyServer)
  await applyValue('ProxyOverride', 'REG_SZ', backup.proxyOverride, backup.hadProxyOverride)
  await winRefresh()
}

/** 备份缺失时的兜底：直接关闭系统代理 */
async function winClear(): Promise<void> {
  await winSetValue('ProxyEnable', 'REG_DWORD', '0')
  await winRefresh()
}

// ---------- macOS（networksetup） ----------

async function macListServices(): Promise<string[]> {
  const { stdout } = await run('networksetup', ['-listallnetworkservices'])
  return stdout
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l && !l.startsWith('*') && !/^An asterisk/i.test(l))
}

async function macGetProxy(service: string, kind: 'web' | 'secure'): Promise<MacServiceProxy> {
  const flag = kind === 'web' ? '-getwebproxy' : '-getsecurewebproxy'
  const { stdout } = await run('networksetup', [flag, service])
  const get = (key: string): string => {
    const m = stdout.match(new RegExp(`^\\s*${key}\\s*:\\s*(.+)$`, 'im'))
    return m ? m[1].trim() : ''
  }
  return { enabled: /^yes$/i.test(get('Enabled')), server: get('Server'), port: get('Port') }
}

async function macReadBackup(): Promise<MacProxyBackup> {
  const services = await macListServices()
  const entries = await Promise.all(
    services.map(async (name) => {
      const [web, secure] = await Promise.all([macGetProxy(name, 'web'), macGetProxy(name, 'secure')])
      const entry: MacProxyBackup['services'][number] = { name }
      if (web.enabled) entry.web = web
      if (secure.enabled) entry.secure = secure
      return entry
    }),
  )
  return { services: entries }
}

async function macApply(host: string, port: number): Promise<void> {
  const services = await macListServices()
  const portStr = String(port)
  await Promise.all(
    services.map(async (name) => {
      await run('networksetup', ['-setwebproxy', name, host, portStr])
      await run('networksetup', ['-setsecurewebproxy', name, host, portStr])
      await run('networksetup', ['-setwebproxystate', name, 'on'])
      await run('networksetup', ['-setsecurewebproxystate', name, 'on'])
    }),
  )
}

async function macRestore(backup: MacProxyBackup): Promise<void> {
  await Promise.all(
    backup.services.map(async (entry) => {
      if (entry.web?.enabled) {
        await run('networksetup', ['-setwebproxy', entry.name, entry.web.server, entry.web.port])
        await run('networksetup', ['-setwebproxystate', entry.name, 'on'])
      } else {
        await run('networksetup', ['-setwebproxystate', entry.name, 'off'])
      }
      if (entry.secure?.enabled) {
        await run('networksetup', ['-setsecurewebproxy', entry.name, entry.secure.server, entry.secure.port])
        await run('networksetup', ['-setsecurewebproxystate', entry.name, 'on'])
      } else {
        await run('networksetup', ['-setsecurewebproxystate', entry.name, 'off'])
      }
    }),
  )
}

async function macClear(): Promise<void> {
  const services = await macListServices()
  await Promise.all(
    services.map(async (name) => {
      await run('networksetup', ['-setwebproxystate', name, 'off'])
      await run('networksetup', ['-setsecurewebproxystate', name, 'off'])
    }),
  )
}

// ---------- Linux（GNOME gsettings） ----------

const GNOME_PROXY = 'org.gnome.system.proxy'

async function gsGet(key: string): Promise<string> {
  const { stdout } = await run('gsettings', ['get', GNOME_PROXY, key])
  return stdout.trim()
}

async function gsSet(key: string, value: string): Promise<void> {
  await run('gsettings', ['set', GNOME_PROXY, key, value])
}

async function gsGetSub(schema: string, key: string): Promise<string> {
  const { stdout } = await run('gsettings', ['get', schema, key])
  return stdout.trim()
}

async function gsSetSub(schema: string, key: string, value: string): Promise<void> {
  await run('gsettings', ['set', schema, key, value])
}

async function linuxReadBackup(): Promise<LinuxProxyBackup> {
  const read = async (): Promise<LinuxProxyBackup> => {
    const [mode, httpHost, httpPort, httpEnabled, httpsHost, httpsPort, httpsEnabled, ignoreHosts] =
      await Promise.all([
        gsGet('mode'),
        gsGetSub(`${GNOME_PROXY}.http`, 'host'),
        gsGetSub(`${GNOME_PROXY}.http`, 'port'),
        gsGetSub(`${GNOME_PROXY}.http`, 'enabled'),
        gsGetSub(`${GNOME_PROXY}.https`, 'host'),
        gsGetSub(`${GNOME_PROXY}.https`, 'port'),
        gsGetSub(`${GNOME_PROXY}.https`, 'enabled'),
        gsGet('ignore-hosts'),
      ])
    return {
      mode,
      http: { enabled: httpEnabled, host: httpHost, port: httpPort },
      https: { enabled: httpsEnabled, host: httpsHost, port: httpsPort },
      ignoreHosts,
    }
  }
  try {
    return await read()
  } catch (e) {
    throw new Error(
      `当前桌面环境不支持系统代理（需要 GNOME gsettings）：${e instanceof Error ? e.message : String(e)}`,
    )
  }
}

async function linuxApply(host: string, port: number): Promise<void> {
  const portStr = String(port)
  await gsSet('mode', 'manual')
  await gsSetSub(`${GNOME_PROXY}.http`, 'enabled', 'true')
  await gsSetSub(`${GNOME_PROXY}.http`, 'host', `'${host}'`)
  await gsSetSub(`${GNOME_PROXY}.http`, 'port', portStr)
  await gsSetSub(`${GNOME_PROXY}.https`, 'enabled', 'true')
  await gsSetSub(`${GNOME_PROXY}.https`, 'host', `'${host}'`)
  await gsSetSub(`${GNOME_PROXY}.https`, 'port', portStr)
  // 本机地址不走代理，避免代理环路
  await gsSet('ignore-hosts', "['localhost', '127.0.0.1', '::1']")
}

async function linuxRestore(backup: LinuxProxyBackup): Promise<void> {
  await gsSet('mode', backup.mode)
  if (backup.http) {
    await gsSetSub(`${GNOME_PROXY}.http`, 'enabled', backup.http.enabled)
    await gsSetSub(`${GNOME_PROXY}.http`, 'host', backup.http.host)
    await gsSetSub(`${GNOME_PROXY}.http`, 'port', backup.http.port)
  }
  if (backup.https) {
    await gsSetSub(`${GNOME_PROXY}.https`, 'enabled', backup.https.enabled)
    await gsSetSub(`${GNOME_PROXY}.https`, 'host', backup.https.host)
    await gsSetSub(`${GNOME_PROXY}.https`, 'port', backup.https.port)
  }
  if (backup.ignoreHosts) {
    await gsSet('ignore-hosts', backup.ignoreHosts)
  }
}

async function linuxClear(): Promise<void> {
  await gsSet('mode', 'none')
}

// ---------- 平台分发 ----------

function readBackup(): Promise<ProxyBackup> {
  switch (process.platform) {
    case 'win32':
      return winReadBackup()
    case 'darwin':
      return macReadBackup()
    case 'linux':
      return linuxReadBackup()
    default:
      throw new Error(`暂不支持当前平台: ${process.platform}`)
  }
}

function applyProxy(host: string, port: number): Promise<void> {
  switch (process.platform) {
    case 'win32':
      return winApply(host, port)
    case 'darwin':
      return macApply(host, port)
    case 'linux':
      return linuxApply(host, port)
    default:
      throw new Error(`暂不支持当前平台: ${process.platform}`)
  }
}

function restoreProxy(backup?: ProxyBackup): Promise<void> {
  switch (process.platform) {
    case 'win32':
      return backup ? winRestore(backup as WinProxyBackup) : winClear()
    case 'darwin':
      return backup ? macRestore(backup as MacProxyBackup) : macClear()
    case 'linux':
      return backup ? linuxRestore(backup as LinuxProxyBackup) : linuxClear()
    default:
      throw new Error(`暂不支持当前平台: ${process.platform}`)
  }
}

function parseEndpoint(endpoint: string): { host: string; port: number } {
  const idx = endpoint.lastIndexOf(':')
  if (idx < 0) return { host: endpoint, port: 0 }
  return { host: endpoint.slice(0, idx), port: Number(endpoint.slice(idx + 1)) }
}

/** 从核心 API 获取网关当前实际绑定地址（端口可能自动顺延） */
async function resolveGatewayEndpoint(): Promise<string> {
  const token = getToken?.() ?? ''
  let res: Response
  try {
    res = await fetch('http://127.0.0.1:17890/api/status', {
      headers: token ? { 'X-Token': token } : {},
      signal: AbortSignal.timeout(5000),
    })
  } catch {
    throw new Error('无法连接核心服务，请确认 proxy-core 已启动')
  }
  if (!res.ok) throw new Error(`核心服务状态获取失败（HTTP ${res.status}）`)
  const body = (await res.json()) as {
    code: number
    msg: string
    data?: { running?: boolean; httpProxyBind?: string }
  }
  if (body.code !== 0 || !body.data) throw new Error(body.msg || '核心服务状态获取失败')
  if (!body.data.running) throw new Error('网关未运行，请先启动网关')
  if (!body.data.httpProxyBind) throw new Error('无法获取网关绑定地址')
  return body.data.httpProxyBind
}

function persist(systemProxy: { enabled: boolean; endpoint?: string; backup?: ProxyBackup }): void {
  const settings = loadAppSettings()
  saveAppSettings({ ...settings, systemProxy })
}

/**
 * 开启 / 关闭系统代理。
 * 开启：读取网关实际绑定地址 → 备份当前系统代理设置 → 写入代理 → 持久化。
 * 关闭：按备份完整还原 → 清除持久化的备份。
 * 任何失败都会写入 state.error 并广播，不抛出异常（调用方从返回值判断）。
 */
export async function setSystemProxy(enabled: boolean): Promise<SystemProxyState> {
  try {
    if (enabled === state.enabled) return state
    if (enabled) {
      const endpoint = await resolveGatewayEndpoint()
      const { host, port } = parseEndpoint(endpoint)
      if (!host || !Number.isInteger(port) || port <= 0) {
        throw new Error(`无效的网关地址: ${endpoint}`)
      }
      const backup = await readBackup()
      await applyProxy(host, port)
      persist({ enabled: true, endpoint, backup })
      setState({ enabled: true, endpoint, error: undefined, changedAt: Date.now() })
    } else {
      const backup = loadAppSettings().systemProxy?.backup
      await restoreProxy(backup as ProxyBackup | undefined)
      persist({ enabled: false, endpoint: undefined, backup: undefined })
      setState({ enabled: false, endpoint: undefined, error: undefined, changedAt: Date.now() })
    }
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    setState({ error: msg })
  }
  return state
}

/** 注册 IPC（在 app ready 后调用一次），tokenProvider 提供核心 API 鉴权 token */
export function initSystemProxy(tokenProvider: () => string): void {
  getToken = tokenProvider
  ipcMain.handle('system-proxy:get-state', () => getSystemProxyState())
  ipcMain.handle('system-proxy:set', async (_e, enabled: boolean) => {
    await setSystemProxy(Boolean(enabled))
    return state
  })
}

/** 退出应用时自动关闭并还原系统代理（防止网关停止后系统代理指向失效端口） */
export async function restoreSystemProxyOnQuit(): Promise<void> {
  try {
    if (loadAppSettings().systemProxy?.enabled && state.enabled) {
      await setSystemProxy(false)
    }
  } catch (e) {
    console.error(`[system-proxy] 退出时还原系统代理失败: ${e instanceof Error ? e.message : String(e)}`)
  }
}
