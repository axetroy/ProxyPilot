import { app, BrowserWindow, dialog, ipcMain, Menu, Notification, Tray, nativeImage } from 'electron'
import { spawn, ChildProcess } from 'node:child_process'
import { existsSync, mkdirSync } from 'node:fs'
import * as path from 'node:path'
import { pathToFileURL } from 'node:url'
import { isProxyPilotStatus } from './core-process'
import { loadAppSettings, saveAppSettings, type AppSettings } from './app-settings'
import {
  initUpdater,
  scheduleStartupCheck,
  checkForUpdates,
  onUpdaterStateChange,
  updaterTooltip,
  setBeforeInstallCleanup,
} from './updater'
import {
  initSystemProxy,
  isSystemProxyEnabled,
  setSystemProxy,
  onSystemProxyStateChange,
  restoreSystemProxyOnQuit,
} from './system-proxy'

// 禁用硬件加速以防止 GPU 崩溃（Windows 常见问题）
app.disableHardwareAcceleration()

// Windows 系统通知（更新提醒等）需要显式设置 AppUserModelID，
// 否则 toast 不会显示（与 electron-builder 的 appId 保持一致）
app.setAppUserModelId('com.axetroy.proxypilot')

// 单例锁：只允许一个实例运行，重复启动时聚焦已有窗口
const gotTheLock = app.requestSingleInstanceLock()
if (!gotTheLock) {
  app.quit()
} else {
  app.on('second-instance', () => {
    showMainWindow()
  })
}

// 默认 API 地址；core 启动后按 stdout 的 PROXYPILOT_API 更新为实际端口
// （API 端口被占用时会向后顺延，实际地址由 core 告知）。
const DEFAULT_API_BASE = 'http://127.0.0.1:17890'
let apiBase = DEFAULT_API_BASE
let apiBaseReadyPromise: Promise<void> | null = null
let resolveApiBaseReady: (() => void) | null = null

let core: ChildProcess | null = null
let sessionToken = ''
let mainWindow: BrowserWindow | null = null
let tray: Tray | null = null
let isQuitting = false
let shutdownStarted = false
let tokenReadyPromise: Promise<void> | null = null
let resolveTokenReady: (() => void) | null = null
let tokenReady = false

function resolveCorePath(): string {
  // Windows 下 Go 构建产物为 proxy-core.exe，其他平台为 proxy-core
  const coreBin = process.platform === 'win32' ? 'proxy-core.exe' : 'proxy-core'

  // 打包后：<resources>/proxy-core（extraResources 放置位置，与 app.asar 同级）
  const packaged = path.join(process.resourcesPath, coreBin)
  if (existsSync(packaged)) return packaged

  // 开发模式：<repo>/proxy-core/proxy-core
  const dev = path.join(app.getAppPath(), '..', 'proxy-core', coreBin)
  if (existsSync(dev)) return dev

  // 兜底：app 目录下
  const fallback = path.join(app.getAppPath(), coreBin)
  if (existsSync(fallback)) return fallback

  return packaged
}

async function startCore(): Promise<void> {
  if (core) return
  // 每次启动都重新初始化就绪信号，core 重启后按新输出值更新
  apiBase = DEFAULT_API_BASE
  apiBaseReadyPromise = new Promise<void>((resolve) => {
    resolveApiBaseReady = resolve
  })
  tokenReady = false
  tokenReadyPromise = new Promise<void>((resolve) => {
    resolveTokenReady = resolve
  })
  const exe = resolveCorePath()
  if (!existsSync(exe)) {
    console.error(`[core] proxy-core not found at: ${exe}`)
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('core:error', `proxy-core not found: ${exe}`)
    }
    return
  } else {
    console.log(`[core] starting proxy-core: ${exe}`)
  }
  // 数据库放在 Electron 用户数据目录（appdata）下，由应用创建专属目录：
  // 打包后 install 目录 / resources 可能只读或随版本更替被清空，数据不应写在那里。
  // 目录确保存在后再启动 core（storage 侧也会做同样兜底）。
  const dataDir = app.getPath('userData')
  mkdirSync(dataDir, { recursive: true })
  const dbPath = path.join(dataDir, 'proxypilot.db')

  core = spawn(exe, [], {
    cwd: path.dirname(exe),
    env: {
      ...process.env,
      PROXYPILOT_TOKEN: sessionToken,
      PROXYPILOT_DB_PATH: dbPath,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: false,
  })

  core.on('error', (err) => {
    console.error(`[core] failed to spawn proxy-core: ${err.message}`)
    core = null
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('core:error', `failed to start proxy-core: ${err.message}`)
    }
  })

  core.stdout?.on('data', (chunk: Buffer) => {
    const text = chunk.toString()
    const m = text.match(/PROXYPILOT_TOKEN=(\S+)/)
    if (m) {
      sessionToken = m[1]
      tokenReady = true
      resolveTokenReady?.()
      resolveTokenReady = null
    }
    const mApi = text.match(/PROXYPILOT_API=(http:\/\/\S+)/)
    if (mApi) {
      apiBase = mApi[1]
      resolveApiBaseReady?.()
      resolveApiBaseReady = null
    }
    process.stdout.write(text)
  })
  core.stderr?.on('data', (chunk: Buffer) => {
    process.stderr.write(chunk.toString())
  })
  core.on('exit', () => {
    core = null
    sessionToken = ''
    tokenReady = false
    tokenReadyPromise = null
    resolveTokenReady = null
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('core:exit')
    }
  })
}

function stopCore(): void {
  if (core) {
    core.kill()
    core = null
  }
}

async function waitForCore(timeoutMs = 20000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastErr = ''
  // core 启动后会在 stdout 输出 PROXYPILOT_TOKEN 与 PROXYPILOT_API（实际端口）。
  // 先等两者就绪，避免 API 端口顺延后仍拿默认端口探测而误判。
  // 用带超时的等待：core 崩溃/输出缺失时不会永久挂起。
  if (tokenReadyPromise) {
    await withTimeout(tokenReadyPromise, timeoutMs, 'proxy-core 未在超时时间内输出会话 token')
  }
  if (apiBaseReadyPromise) {
    await withTimeout(apiBaseReadyPromise, timeoutMs, 'proxy-core 未在超时时间内输出 API 地址')
  }
  while (Date.now() < deadline) {
    if (!core) {
      throw new Error('proxy-core 进程已退出，无法就绪')
    }
    try {
      const headers: Record<string, string> = sessionToken ? { 'X-Token': sessionToken } : {}
      const res = await fetch(`${apiBase}/api/status`, { headers })
      if (res.ok) {
        // 校验响应确实来自 ProxyPilot core：若端口被其他进程占用，立即失败，
        // 避免 UI 连到错误服务
        if (isProxyPilotStatus(await res.json())) {
          return
        }
        throw new Error(`端口 ${apiBase.replace('http://', '')} 已被其他进程占用（非 ProxyPilot 服务）`)
      }
      lastErr = `HTTP ${res.status}`
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e)
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error(
    `proxy-core 未在 ${timeoutMs}ms 内就绪（最后错误: ${lastErr}）。` +
      `请确认端口 ${apiBase.replace('http://', '')} 未被其他进程占用，` +
      `且 proxy-core 可正常运行。`,
  )
}

// withTimeout 给 Promise 加超时，避免等待永不落地的信号（如 core 崩溃后
// tokenReadyPromise 永远不会 resolve）导致启动流程永久挂起。
function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), ms)
  })
  return Promise.race([promise, timeout]).finally(() => {
    if (timer) clearTimeout(timer)
  })
}

function resolveIconPath(): string {
  // 打包后：<resources>/icon.png（extraResources 复制到 resources/）
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'icon.png')
  }
  // 开发模式：<app>/build/icon.png（__dirname = app/dist-electron/main）
  return path.join(__dirname, '..', '..', 'build', 'icon.png')
}

// macOS 菜单栏托盘模板图标路径（打包后 <resources>/trayTemplate.png）。
function resolveTrayIconPath(): string {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'trayTemplate.png')
  }
  return path.join(__dirname, '..', '..', 'build', 'trayTemplate.png')
}

// macOS Dock 可见性：仅在主窗口可见时显示 Dock 图标。
// 隐藏到托盘后从 Dock 移除，只保留菜单栏托盘作为入口；
// 窗口 show/hide 事件触发同步。
function syncDockVisibility(): void {
  if (process.platform !== 'darwin' || !app.dock) return
  if (mainWindow && !mainWindow.isDestroyed() && mainWindow.isVisible()) {
    app.dock.show()
  } else {
    app.dock.hide()
  }
}

function showMainWindow(): void {
  if (!mainWindow || mainWindow.isDestroyed()) {
    createWindow()
    return
  }
  // 必须先恢复 Dock 图标再显示窗口：Dock 隐藏时 app 处于 accessory 模式，
  // 直接 show 窗口无法正常获得键盘焦点（macOS 限制）。
  // 注意这里不能调 syncDockVisibility()：窗口此刻还处于隐藏状态，
  // 它会按 isVisible()=false 把 Dock 隐藏（正好相反），需要显式 show。
  if (process.platform === 'darwin' && app.dock) {
    app.dock.show()
  }
  if (mainWindow.isMinimized()) mainWindow.restore()
  mainWindow.show()
  mainWindow.focus()
}

function createTray(): void {
  let icon: Electron.NativeImage
  if (process.platform === 'darwin') {
    // macOS 菜单栏使用模板图标：16x16（自动加载 @2x），系统按深浅色菜单栏自动渲染单色
    icon = nativeImage.createFromPath(resolveTrayIconPath())
    icon.setTemplateImage(true)
  } else {
    // Windows/Linux 托盘可以显示彩色大图（系统会自动缩放）
    icon = nativeImage.createFromPath(resolveIconPath())
  }
  tray = new Tray(icon)
  tray.setToolTip('ProxyPilot')
  // 托盘 tooltip 随更新状态变化：窗口隐藏到托盘时也能看到下载进度
  onUpdaterStateChange((s) => {
    tray?.setToolTip(updaterTooltip(s))
  })
  // 系统代理开关状态变化时重建菜单，刷新复选框勾选状态
  onSystemProxyStateChange(buildTrayMenu)
  buildTrayMenu()
}

async function toggleSystemProxy(enabled: boolean): Promise<void> {
  const next = await setSystemProxy(enabled)
  if (next.error) {
    console.error(`[system-proxy] ${next.error}`)
    // 窗口可能已隐藏到托盘，用系统原生通知提示失败原因
    if (Notification.isSupported()) {
      new Notification({ title: '系统代理', body: next.error }).show()
    }
  }
}

function buildTrayMenu(): void {
  if (!tray) return
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: '显示主窗口', click: showMainWindow },
      { type: 'separator' },
      {
        label: '系统代理',
        type: 'checkbox',
        checked: isSystemProxyEnabled(),
        click: (item) => void toggleSystemProxy(item.checked),
      },
      { label: '检查更新', click: () => void checkForUpdates(true) },
      { type: 'separator' },
      { label: '退出程序', click: () => app.quit() },
    ]),
  )
  // 点击托盘图标只弹出菜单，不显示窗口；只有点击菜单项「显示主窗口」才显示窗口。
  // macOS：setContextMenu 后点击托盘图标由系统自动弹出菜单，
  // 无需再注册 click 处理（重复注册会导致菜单弹出两次）。
  // Windows/Linux：左键点击不会自动弹菜单，需要手动弹出。
  // macOS：点击托盘图标后隐藏 Dock 图标，仅通过托盘作为入口
  if (process.platform === 'darwin') {
    app.dock?.hide()
  } else {
    tray.on('click', () => {
      tray?.popUpContextMenu()
    })
  }
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    title: 'ProxyPilot',
    icon: resolveIconPath(),
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })

  const devServerUrl = process.env.VITE_DEV_SERVER_URL || 'http://127.0.0.1:5173'

  if (!app.isPackaged) {
    mainWindow.loadURL(devServerUrl).catch(() => {
      mainWindow?.loadFile(path.join(app.getAppPath(), 'dist', 'index.html'))
    })
  } else {
    mainWindow.loadFile(path.join(app.getAppPath(), 'dist', 'index.html'))
  }

  // 开发模式：不自动打开开发者工具，改用 F12 / Ctrl+Shift+I 手动切换。
  // 应用菜单被移除（Menu.setApplicationMenu(null)），默认快捷键失效，
  // 这里手动监听键盘事件，让开发者工具随时可切换（再次按下即关闭）
  if (!app.isPackaged) {
    mainWindow.webContents.on('before-input-event', (event, input) => {
      if (input.type !== 'keyDown') return
      const isF12 = input.key === 'F12' && !input.alt && !input.control && !input.shift && !input.meta
      const isCtrlShiftI =
        input.key.toLowerCase() === 'i' && input.control && input.shift && !input.alt && !input.meta
      if (isF12 || isCtrlShiftI) {
        event.preventDefault()
        mainWindow?.webContents.toggleDevTools()
      }
    })
  }

  mainWindow.on('close', (e) => {
    // 默认最小化到托盘；仅当用户选择「退出程序」或应用真正退出时才关闭窗口
    if (!isQuitting && loadAppSettings().closeBehavior === 'minimize') {
      e.preventDefault()
      mainWindow?.hide()
    }
  })

  // 窗口可见性变化时同步 Dock 图标（隐藏到托盘 → 移除 Dock，重新显示 → 恢复）
  mainWindow.on('show', syncDockVisibility)
  mainWindow.on('hide', syncDockVisibility)

  mainWindow.on('closed', () => {
    mainWindow = null
    syncDockVisibility()
  })

  // Electron 43：query-session-end 是窗口事件（Windows 关机 / 注销 / 重启前触发）
  mainWindow.on('query-session-end', () => gracefulShutdown())

  // 初次创建时同步一次，兜底 'show' 事件竞态
  syncDockVisibility()
}

ipcMain.handle('get-token', async () => {
  if (tokenReady && sessionToken) return sessionToken
  if (tokenReadyPromise) {
    await tokenReadyPromise
  }
  return sessionToken
})
ipcMain.handle('get-api-base', () => apiBase)
ipcMain.handle('get-platform', () => process.platform)
// 选择本地订阅文件：返回 file:// 形式的 URL（供订阅 URL 字段使用）。
ipcMain.handle('pick-subscription-file', async (): Promise<string | null> => {
  const win = BrowserWindow.getFocusedWindow() ?? mainWindow
  if (!win) return null
  const result = await dialog.showOpenDialog(win, {
    title: '选择订阅文件',
    properties: ['openFile'],
    filters: [
      { name: '订阅文件', extensions: ['txt', 'list', 'sub', 'conf', 'json', 'yaml', 'yml'] },
      { name: '所有文件', extensions: ['*'] },
    ],
  })
  if (result.canceled || result.filePaths.length === 0) return null
  // file:// 形式：用 pathToFileURL 统一编码，正确处理空格、#、?、中文等字符，
  // 避免手动拼接导致 URL 解析把路径截断。
  return pathToFileURL(result.filePaths[0]).href
})
// 开机自启：通过系统登录项实现（Windows 注册表 Run 键 / macOS LaunchAgents / Linux autostart）。
// 仅在保存设置变化时写入，避免每次启动都重置用户手动在系统层的改动。
function applyAutoLaunch(enabled: boolean): void {
  try {
    app.setLoginItemSettings({ openAtLogin: enabled })
  } catch (e) {
    console.error(`[app] 设置开机自启失败: ${e instanceof Error ? e.message : String(e)}`)
  }
}

ipcMain.handle('get-app-settings', () => loadAppSettings())
ipcMain.handle('set-app-settings', (_e, settings: AppSettings) => {
  const prev = loadAppSettings()
  saveAppSettings(settings)
  // 开机自启状态发生变化时同步系统登录项
  if (prev.autoLaunch !== settings.autoLaunch) {
    applyAutoLaunch(settings.autoLaunch)
  }
  return loadAppSettings()
})

app.whenReady().then(async () => {
  // 移除默认菜单栏（File/Edit/View 等），保留系统自带的窗口边框（最小化/最大化/关闭）
  Menu.setApplicationMenu(null)
  createTray()

  try {
    await startCore()
    await waitForCore()
  } catch (e) {
    console.error(e)
  }
  createWindow()

  // 更新机制：注册 IPC + 启动自动检查（默认开启，延迟数秒后检查）
  initUpdater()
  scheduleStartupCheck()
  // 系统代理：注册 IPC，token 由主进程持有的 sessionToken 提供
  initSystemProxy(() => sessionToken, () => apiBase)

  // 更新安装前先清理：停核心 + 还原系统代理（若开启），保证安装器 spawn 时
  // 应用已干净退出（proxy-core 无文件锁、无需安装器强杀进程、系统代理不残留）
  setBeforeInstallCleanup(async () => {
    await restoreSystemProxyOnQuit()
    stopCore()
  })

  // macOS 惯例：点击 Dock 图标重新激活应用时恢复/显示主窗口
  // （Dock 图标仅在窗口可见时存在，此路径也覆盖窗口被销毁后的重建）
  app.on('activate', () => {
    showMainWindow()
  })
})

app.on('window-all-closed', () => {
  // macOS 惯例是关闭所有窗口后应用常驻 Dock；但用户选择「退出程序」行为时，
  // 关闭主窗口即应直接退出应用（不受平台惯例限制）
  if (process.platform !== 'darwin' || loadAppSettings().closeBehavior === 'quit') {
    app.quit()
  }
})

/**
 * 统一的退出收尾（幂等，所有退出路径都汇到这里）：
 * 还原系统代理（防止网关停止后系统代理指向失效端口）→ 停核心 → 退出。
 * 看门狗兜底：即使清理挂起（如系统命令无响应），也在 15s 内强制退出，
 * 避免 Windows 关机 / 系统信号场景下因清理卡住而被系统强杀。
 *
 * 收尾完成后用 app.quit() 而非 app.exit(0)：app.exit 会跳过 before-quit/will-quit/quit
 * 事件链，导致 electron-updater 的 autoInstallOnAppQuit（退出时自动安装已下载的更新）
 * 永远不执行。app.quit() 第二次触发 before-quit 时 shutdownStarted 已为 true，
 * 直接放行走到 'quit' 事件，electron-updater 的 onQuit 监听器即可 spawn 安装器。
 */
function gracefulShutdown(): void {
  if (shutdownStarted) return
  shutdownStarted = true
  // 兜底看门狗：清理挂起时强制退出（此路径跳过 'quit'，自动安装不生效，仅作最后保障）
  setTimeout(() => app.exit(0), 15000).unref()
  void (async () => {
    try {
      await restoreSystemProxyOnQuit()
    } catch (err) {
      console.error(err)
    }
    stopCore()
    app.quit()
  })()
}

app.on('before-quit', (e) => {
  isQuitting = true
  // 正常退出路径（托盘「退出程序」/ Cmd+Q / 关闭窗口且行为为退出 / 更新重启 quitAndInstall
  // / Linux Ctrl+C 被 Electron 劫持转成的 app.quit）都会先到这里。
  // 首次退出：preventDefault，由 gracefulShutdown 完成清理后再次 app.quit()；
  // 第二次（shutdownStarted=true）：放行，让事件链走到 'quit' 触发退出自动安装。
  if (shutdownStarted) return
  e.preventDefault()
  gracefulShutdown()
})

// 系统退出信号（POSIX）：SIGTERM（systemd/docker 停止、kill）、SIGINT（Ctrl+C）、
// SIGHUP（终端挂断）。这些信号不会触发 before-quit，必须显式监听；
// 与 gracefulShutdown 的幂等保护配合，不会与 Electron 的 SIGINT 劫持重复清理。
if (process.platform !== 'win32') {
  for (const sig of ['SIGTERM', 'SIGINT', 'SIGHUP'] as const) {
    process.on(sig, () => gracefulShutdown())
  }
}

// Windows 系统关机 / 注销 / 重启：不经过 before-quit，由主窗口的
// query-session-end 事件单独监听（尽力而为，系统会限时强杀）。
// 不 preventDefault，尊重用户的关机选择，只做快速清理后退出。
