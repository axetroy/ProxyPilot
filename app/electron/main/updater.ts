// app/electron/main/updater.ts
/**
 * 应用更新机制（检查 / 下载 / 安装）。
 *
 * 基于 electron-updater，更新源为 GitHub Releases：
 *   https://github.com/axetroy/ProxyPilot/releases
 * electron-builder 打包时会生成更新元数据（latest*.yml），CI 统一改名为
 * 「平台-架构」格式（windows-x64.yml / windows-arm64.yml / linux-x64.yml /
 * darwin-x64.yml / darwin-arm64.yml，见 .github/workflows/ci.yml）；
 * electron-updater 已被 patch（见 patches/electron-updater+6.8.9.patch，由
 * patch-package 在 npm install 时自动应用），按本机平台+架构读取对应文件。
 *
 * 行为约定：
 *   - 自动更新默认开启（userData/settings.json 的 autoUpdate 字段，缺省 true）；
 *   - 启动后延迟数秒自动检查一次，发现新版本立即自动下载；
 *   - 下载进度通过 `updater:event` 实时推送到所有窗口；
 *   - 下载完成后等待用户确认，点击「立即重启」才 quitAndInstall；
 *   - 设置页可关闭自动更新：关闭后不再自动检查/下载，手动检查仍可用；
 *   - 开发模式（未打包）不做更新检查，避免 electron-updater 误读 dev 配置。
 */
import { app, BrowserWindow, ipcMain, Notification } from 'electron'
import { execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { promisify } from 'node:util'
import { autoUpdater, type UpdateInfo } from 'electron-updater'
import { loadAppSettings, saveAppSettings } from './app-settings'

const execFileAsync = promisify(execFile) as (
  cmd: string,
  args: string[],
  options: { timeout?: number; windowsHide?: boolean },
) => Promise<{ stdout: string; stderr: string }>

// electron-builder 写入的卸载注册表键（perUser 在 HKCU，perMachine 在 HKLM）
const WIN_UNINSTALL_KEY = 'Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\com.axetroy.proxypilot'

// electron-builder NSIS 把安装位置 InstallLocation 写在 INSTALL_REGISTRY_KEY =
// Software\<APP_GUID> 下（见 app-builder-lib 的 multiUser.nsh / NsisTarget.js），
// 并不在卸载键里。APP_GUID = UUID.v5(appId, ELECTRON_BUILDER_NS_UUID)，需在
// 运行时用同样算法算出，才能从注册表读到旧版安装目录。
const APP_ID = 'com.axetroy.proxypilot'
// 与 app-builder-lib 的 NsisTarget.js 保持一致：
//   ELECTRON_BUILDER_NS_UUID = UUID.parse("50e065bc-3134-11e6-9bab-38c9862bdaf3")
//   APP_GUID = UUID.v5(appId, ELECTRON_BUILDER_NS_UUID)
// 本机实测 APP_GUID = 56f7d2e6-797d-5a32-ae0a-87aeb0a3440d（卸载键里可见）
const ELECTRON_BUILDER_NS_UUID = '50e065bc-3134-11e6-9bab-38c9862bdaf3'
const INSTALL_REGISTRY_KEY = `Software\\${uuidV5(APP_ID, ELECTRON_BUILDER_NS_UUID)}`

/** 与 app-builder-lib 保持一致的 UUID v5 实现（决定 INSTALL_REGISTRY_KEY） */
function uuidV5(name: string, namespace: string): string {
  const ns = Buffer.from(namespace.replace(/-/g, ''), 'hex')
  const hash = createHash('sha1').update(ns).update(name, 'utf8').digest()
  hash[6] = (hash[6] & 0x0f) | 0x50
  hash[8] = (hash[8] & 0x3f) | 0x80
  const hex = hash.toString('hex')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`
}

export type UpdaterStatus =
  | 'idle'
  | 'checking'
  | 'available'
  | 'not-available'
  | 'downloading'
  | 'downloaded'
  | 'error'
  | 'dev'

export interface UpdaterProgress {
  percent: number
  transferred: number
  total: number
  bytesPerSecond: number
}

export interface UpdaterState {
  /** 自动更新开关（默认开启） */
  enabled: boolean
  status: UpdaterStatus
  /** 触发来源：auto=启动/重新开启自动更新，manual=设置页手动检查 */
  source: 'auto' | 'manual'
  currentVersion: string
  latestVersion?: string
  progress?: UpdaterProgress
  error?: string
  checkedAt?: number
}

let state: UpdaterState = {
  enabled: loadAppSettings().autoUpdate,
  status: 'idle',
  source: 'auto',
  currentVersion: app.getVersion(),
}

// 防止自动检查与手动检查并发触发
let checkInFlight = false

// 下载到了与当前机器架构不匹配的安装包（例如 CI 的 latest.yml 被另一架构覆盖，
// v0.1.11 事故：x64 机器下载到 win-arm64 安装包）。置位后拒绝安装，并禁用
// 退出时自动安装，防止 quitAndInstall 先卸载旧版、又装不上任何文件导致应用被删。
let wrongArchInstallerPending = false

// 安装前清理钩子（由 index.ts 注入：停核心 + 还原系统代理），
// 保证安装器 spawn 时应用已干净退出，无文件锁、无需安装器杀进程。
let beforeInstallCleanup: (() => Promise<void>) | null = null

export function setBeforeInstallCleanup(fn: () => Promise<void>): void {
  beforeInstallCleanup = fn
}

/**
 * 读取旧版本的安装目录（Windows 注册表 InstallLocation）。
 *
 * 背景：electron-updater 6.8.9 的 NsisUpdater 从不给 installDirectory 赋值，
 * 导致 doInstall 不会传 /D= 参数；assisted 安装器（nsis.oneClick:false +
 * allowToChangeInstallationDirectory:true）静默更新时会用 NSIS 默认目录
 * （$LOCALAPPDATA\Programs\ProxyPilot），而 uninstallOldVersion 又会从注册表
 * 卸载旧目录，结果「旧应用被删、新版装到默认目录找不到」。这里手动读取
 * 旧安装位置注入，让 /D= 指向旧目录，更新才能原地覆盖。
 */
async function readInstallLocation(): Promise<string | undefined> {
  if (process.platform !== 'win32') return undefined
  for (const root of ['HKCU', 'HKLM'] as const) {
    // 主键：electron-builder 把 InstallLocation 写在 Software\<APP_GUID> 下；
    // 兜底：个别旧版 electron-builder 也会把它写在卸载键里。
    for (const sub of [INSTALL_REGISTRY_KEY, WIN_UNINSTALL_KEY]) {
      try {
        const { stdout } = await execFileAsync('reg', ['query', `${root}\\${sub}`, '/v', 'InstallLocation'], {
          timeout: 10000,
          windowsHide: true,
        })
        const line = stdout.trim().split(/\r?\n/).pop() ?? ''
        // reg query 输出形如：`    InstallLocation    REG_SZ    C:\...\ProxyPilot`
        // 路径可能包含空格，必须按 REG_SZ 后的整段取值，不能按空白切分
        const m = line.match(/REG_SZ\s+(.+)$/)
        let value = m ? m[1].trim() : undefined
        if (value) {
          // assisted 安装器静默更新时会把 $INSTDIR 规范化：若 /D= 目录名不含
          // APP_FILENAME（"ProxyPilot"），会追加子目录，保证与卸载目录一致
          if (!value.toLowerCase().includes('proxypilot')) {
            value = `${value}\\ProxyPilot`
          }
          return value
        }
      } catch {
        // 该根键/子键不存在，尝试下一个
      }
    }
  }
  return undefined
}

/**
 * 判断待安装的更新包架构是否与当前机器匹配。
 *
 * 背景：CI 为 Windows 并行构建 x64 / arm64，两者各自生成的 latest.yml 上传到同一
 * Release 会互相覆盖（v0.1.11 事故：latest.yml 指向 win-arm64）。arm64 NSIS 安装器
 * 在 x64 机器上不会解压任何应用文件（identify_package 找不到匹配架构），但仍会先
 * 卸载旧版并写注册表/快捷方式，最终「旧应用被删、新版没装上」。安装前必须拦截。
 *
 * x64 机器拒绝 arm64 包；arm64 机器两种架构都放行（x64 包可经 Windows 仿真安装运行）。
 */
function isWrongArchInstaller(info: UpdateInfo): boolean {
  if (process.platform !== 'win32') return false
  const fileName = info.files?.[0]?.url ?? info.path ?? ''
  const isArm64Package = /\barm64\b/i.test(fileName)
  return process.arch === 'x64' && isArm64Package
}

/** 安装前先做清理（停核心、还原系统代理），再触发 quitAndInstall */
async function installUpdate(): Promise<void> {
  // 兜底：架构不匹配时拒绝安装（正常情况下 update-downloaded 已拦截并禁用自动安装）
  if (wrongArchInstallerPending) {
    setState({
      status: 'error',
      error: '更新包架构与本机不匹配，已取消安装。请从 GitHub Releases 手动下载对应版本。',
      progress: undefined,
    })
    return
  }
  if (beforeInstallCleanup) {
    // 10s 超时兜底：清理挂起时不再等待，直接进入安装流程
    await Promise.race([beforeInstallCleanup(), new Promise<void>((resolve) => setTimeout(resolve, 10000))])
  }
  autoUpdater.quitAndInstall()
}

// 状态变更监听器（如托盘 tooltip 更新），在每次 setState 后触发
const stateListeners: Array<(state: UpdaterState) => void> = []

export function onUpdaterStateChange(listener: (state: UpdaterState) => void): void {
  stateListeners.push(listener)
}

function isAnyWindowVisible(): boolean {
  return BrowserWindow.getAllWindows().some(
    (w) => !w.isDestroyed() && w.isVisible() && !w.isMinimized(),
  )
}

/**
 * 主窗口可见时由渲染进程用 Mantine 通知提醒；窗口隐藏（最小化到托盘）时
 * 用系统原生通知兜底，保证「检查更新」等入口的提醒不会漏掉。
 * 传入 onClick 时（如「更新已就绪」），点击通知可直接执行操作，
 * 无需先打开主窗口（Windows / macOS 支持点击，Linux 因系统限制仅展示）。
 */
function notifyIfWindowHidden(options: { title: string; body: string; onClick?: () => void }): void {
  if (!Notification.isSupported()) return
  if (isAnyWindowVisible()) return
  const notification = new Notification({ title: options.title, body: options.body })
  if (options.onClick) {
    notification.on('click', options.onClick)
  }
  notification.show()
}

function setState(patch: Partial<UpdaterState>): void {
  state = { ...state, ...patch }
  for (const win of BrowserWindow.getAllWindows()) {
    if (!win.isDestroyed()) {
      win.webContents.send('updater:event', state)
    }
  }
  for (const listener of stateListeners) {
    try {
      listener(state)
    } catch (e) {
      console.error(`[updater] 状态监听器异常: ${e instanceof Error ? e.message : String(e)}`)
    }
  }
}

/** 根据更新状态生成托盘 tooltip 文案（窗口隐藏时也能直观看到下载进度） */
export function updaterTooltip(updaterState: UpdaterState): string {
  switch (updaterState.status) {
    case 'available':
      return `ProxyPilot · 发现新版本 v${updaterState.latestVersion}，正在下载…`
    case 'downloading': {
      const pct = updaterState.progress ? Math.round(updaterState.progress.percent) : 0
      return `ProxyPilot · 正在下载更新 ${pct}%`
    }
    case 'downloaded':
      return 'ProxyPilot · 更新已就绪，打开应用重启安装'
    case 'error':
      return 'ProxyPilot · 检查更新失败'
    default:
      return 'ProxyPilot'
  }
}

/** 注册 IPC 与 autoUpdater 事件（在 app ready 后调用一次） */
export function initUpdater(): void {
  autoUpdater.autoDownload = true
  autoUpdater.autoInstallOnAppQuit = true
  autoUpdater.allowPrerelease = false
  // 显式指定更新源（与 electron-builder.cjs 的 publish 配置保持一致），
  // 即使打包时未生成 app-update.yml 也能正常工作。
  //
  // 更新元数据文件名：electron-updater 已被 patch（patches/electron-updater+6.8.9.patch，
  // 由 patch-package 在 npm install 时自动应用），默认 channel 即「平台-架构」命名：
  //   windows-x64.yml / windows-arm64.yml / linux-x64.yml / darwin-x64.yml / darwin-arm64.yml
  // 应用启动时读取与本机平台+架构对应的文件，从源头避免下载到不匹配架构的安装包
  // （v0.1.11 事故：latest.yml 被 win-arm64 job 覆盖，x64 用户下载 arm64 安装器 →
  // 不解压任何文件却先卸载旧版 → 应用被删除）。CI 侧产物改名见 .github/workflows/ci.yml。
  autoUpdater.setFeedURL({
    provider: 'github',
    owner: 'axetroy',
    repo: 'ProxyPilot',
  })

  autoUpdater.on('checking-for-update', () => {
    wrongArchInstallerPending = false
    setState({ status: 'checking', error: undefined })
  })
  autoUpdater.on('update-available', (info) => {
    setState({ status: 'available', latestVersion: info.version })
    if (isWrongArchInstaller(info)) {
      // 提前预警：latest.yml 指向了与本机架构不匹配的安装包（CI 覆盖事故）。
      // 下载仍会继续，但 update-downloaded / installUpdate 会最终拦截安装。
      console.warn(`[updater] 更新包架构与本机不匹配: ${info.files?.[0]?.url ?? info.path}`)
    }
    notifyIfWindowHidden({ title: '发现新版本', body: `v${info.version} 已发布，正在自动下载…` })
  })
  autoUpdater.on('update-not-available', () => {
    setState({ status: 'not-available' })
    // 仅手动检查时提醒（自动检查静默），避免打扰
    if (state.source === 'manual') {
      notifyIfWindowHidden({ title: '已是最新版本', body: `当前 v${state.currentVersion} 已是最新` })
    }
  })
  let lastProgressBroadcastAt = 0
  autoUpdater.on('download-progress', (p) => {
    // 进度事件按数据块触发非常频繁（每秒数十次），节流到 300ms 广播一次，
    // 保证渲染进程进度条流畅更新且不被事件洪泛拖慢
    const now = Date.now()
    if (now - lastProgressBroadcastAt < 300) return
    lastProgressBroadcastAt = now
    setState({
      status: 'downloading',
      progress: {
        percent: p.percent,
        transferred: p.transferred,
        total: p.total,
        bytesPerSecond: p.bytesPerSecond,
      },
    })
  })
  autoUpdater.on('update-downloaded', (info) => {
    // 下载期间用户可能已关闭自动更新（electron-updater 无法取消进行中的下载），
    // 此时不再提示安装，也不在退出时自动安装。
    if (!loadAppSettings().autoUpdate) {
      setState({ status: 'idle', progress: undefined })
      return
    }
    // 架构不匹配：拒绝安装。arm64 安装器在 x64 机器上会先卸载旧版、但不会
    // 解压任何应用文件（identify_package 找不到匹配架构），导致应用被删除。
    if (isWrongArchInstaller(info)) {
      wrongArchInstallerPending = true
      // 禁用退出时自动安装，防止用户退出应用时 electron-updater 自行触发安装
      autoUpdater.autoDownload = false
      autoUpdater.autoInstallOnAppQuit = false
      const fileName = info.files?.[0]?.url ?? info.path ?? '未知'
      const msg = `下载的更新包架构（${fileName}）与本机（${process.arch}）不匹配，已取消安装。请从 GitHub Releases 手动下载 ${process.arch} 版本。`
      setState({ status: 'error', error: msg, progress: undefined })
      notifyIfWindowHidden({ title: '更新安装已取消', body: msg })
      return
    }
    wrongArchInstallerPending = false
    setState({ status: 'downloaded' })
    // 点击系统通知直接重启并安装，无需先打开主窗口；走 installUpdate 以便
    // 先停核心/还原系统代理再安装（裸 quitAndInstall 会跳过清理）
    notifyIfWindowHidden({
      title: '更新已就绪',
      body: `v${state.latestVersion} 已下载完成，点击即可重启安装`,
      onClick: () => void installUpdate(),
    })
  })
  autoUpdater.on('error', (err) => {
    const msg = err?.message ?? String(err)
    setState({ status: 'error', error: msg })
    if (state.source === 'manual') {
      notifyIfWindowHidden({ title: '检查更新失败', body: msg })
    }
  })

  ipcMain.handle('updater:get-state', () => getUpdaterState())
  ipcMain.handle('updater:check', () => checkForUpdates(true))
  ipcMain.handle('updater:set-auto-update', (_e, enabled: boolean) => setAutoUpdate(enabled))
  ipcMain.handle('updater:install', () => installUpdate())

  // 修复 electron-updater 不读取安装目录的缺陷：从注册表读出旧安装位置
  // 注入 NsisUpdater.installDirectory，doInstall 才能带上 /D= 参数（见 readInstallLocation）
  void readInstallLocation().then((dir) => {
    if (dir) {
      ;(autoUpdater as unknown as { installDirectory?: string }).installDirectory = dir
      console.log(`[updater] 已定位旧安装目录: ${dir}`)
    }
  })
}

export function getUpdaterState(): UpdaterState {
  return state
}

export async function checkForUpdates(manual: boolean): Promise<UpdaterState> {
  if (!app.isPackaged) {
    setState({ status: 'dev' })
    return state
  }
  if (checkInFlight) return state
  checkInFlight = true
  try {
    setState({
      status: 'checking',
      source: manual ? 'manual' : 'auto',
      error: undefined,
      checkedAt: Date.now(),
    })
    // autoDownload=true，检查到新版本后会自动开始下载
    await autoUpdater.checkForUpdates()
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    setState({ status: 'error', error: msg })
  } finally {
    checkInFlight = false
  }
  return state
}

/** 启动后自动检查一次（自动更新关闭时跳过）；延迟数秒等待网络与核心就绪 */
export function scheduleStartupCheck(): void {
  if (!loadAppSettings().autoUpdate) return
  setTimeout(() => {
    void checkForUpdates(false)
  }, 5000)
}

function setAutoUpdate(enabled: boolean): UpdaterState {
  const settings = loadAppSettings()
  saveAppSettings({ ...settings, autoUpdate: enabled })
  autoUpdater.autoDownload = enabled
  autoUpdater.autoInstallOnAppQuit = enabled
  setState(
    enabled
      ? { enabled: true }
      : { enabled: false, status: 'idle', progress: undefined, latestVersion: undefined, error: undefined },
  )
  // 重新开启自动更新后立即检查一次
  if (enabled && app.isPackaged) {
    setTimeout(() => {
      void checkForUpdates(false)
    }, 1000)
  }
  return state
}
