// app/electron/main/updater.ts
/**
 * 应用更新机制（检查 / 下载 / 安装）。
 *
 * 基于 electron-updater，更新源为 GitHub Releases：
 *   https://github.com/axetroy/ProxyPilot/releases
 * electron-builder 打包时（见 electron-builder.cjs 的 publish 配置）会生成
 * latest.yml / latest-mac.yml / latest-linux.yml，随安装包一起发布到
 * GitHub Release；electron-updater 通过该文件检测新版本并增量下载。
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
import { autoUpdater } from 'electron-updater'
import { loadAppSettings, saveAppSettings } from './app-settings'

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
  autoUpdater.setFeedURL({
    provider: 'github',
    owner: 'axetroy',
    repo: 'ProxyPilot',
  })

  autoUpdater.on('checking-for-update', () => {
    setState({ status: 'checking', error: undefined })
  })
  autoUpdater.on('update-available', (info) => {
    setState({ status: 'available', latestVersion: info.version })
    notifyIfWindowHidden({ title: '发现新版本', body: `v${info.version} 已发布，正在自动下载…` })
  })
  autoUpdater.on('update-not-available', () => {
    setState({ status: 'not-available' })
    // 仅手动检查时提醒（自动检查静默），避免打扰
    if (state.source === 'manual') {
      notifyIfWindowHidden({ title: '已是最新版本', body: `当前 v${state.currentVersion} 已是最新` })
    }
  })
  autoUpdater.on('download-progress', (p) => {
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
  autoUpdater.on('update-downloaded', () => {
    // 下载期间用户可能已关闭自动更新（electron-updater 无法取消进行中的下载），
    // 此时不再提示安装，也不在退出时自动安装。
    if (!loadAppSettings().autoUpdate) {
      setState({ status: 'idle', progress: undefined })
      return
    }
    setState({ status: 'downloaded' })
    // 点击系统通知直接重启并安装，无需先打开主窗口
    notifyIfWindowHidden({
      title: '更新已就绪',
      body: `v${state.latestVersion} 已下载完成，点击即可重启安装`,
      onClick: () => autoUpdater.quitAndInstall(),
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
  ipcMain.handle('updater:install', () => {
    autoUpdater.quitAndInstall()
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
