import { app, BrowserWindow, ipcMain, Menu, Tray, nativeImage } from 'electron'
import { spawn, ChildProcess } from 'node:child_process'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import * as path from 'node:path'

// 禁用硬件加速以防止 GPU 崩溃（Windows 常见问题）
app.disableHardwareAcceleration()

// 单例锁：只允许一个实例运行，重复启动时聚焦已有窗口
const gotTheLock = app.requestSingleInstanceLock()
if (!gotTheLock) {
  app.quit()
} else {
  app.on('second-instance', () => {
    showMainWindow()
  })
}

const API_BASE = 'http://127.0.0.1:17890'

let core: ChildProcess | null = null
let sessionToken = ''
let mainWindow: BrowserWindow | null = null
let tray: Tray | null = null
let isQuitting = false
let tokenReadyPromise: Promise<void> | null = null
let resolveTokenReady: (() => void) | null = null
let tokenReady = false

// ---- 应用级设置（持久化到 userData/settings.json）----

interface AppSettings {
  closeBehavior: 'minimize' | 'quit'
}

function settingsFilePath(): string {
  return path.join(app.getPath('userData'), 'settings.json')
}

function loadAppSettings(): AppSettings {
  try {
    const raw = JSON.parse(readFileSync(settingsFilePath(), 'utf-8')) as Partial<AppSettings>
    return { closeBehavior: raw.closeBehavior === 'quit' ? 'quit' : 'minimize' }
  } catch {
    return { closeBehavior: 'minimize' }
  }
}

function saveAppSettings(settings: AppSettings): void {
  try {
    writeFileSync(settingsFilePath(), JSON.stringify(settings, null, 2), 'utf-8')
  } catch (e) {
    console.error(`[app] 保存设置失败: ${e instanceof Error ? e.message : String(e)}`)
  }
}

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

async function killExistingCore(): Promise<void> {
  if (process.platform !== 'win32') return
  await new Promise<void>((resolve) => {
    const taskkill = spawn('taskkill', ['/F', '/IM', 'proxy-core.exe', '/T'], {
      stdio: 'ignore',
    })
    taskkill.on('error', () => resolve())
    taskkill.on('exit', () => resolve())
  })
  await new Promise((resolve) => setTimeout(resolve, 500))
}

async function startCore(): Promise<void> {
  if (core) return
  await killExistingCore()
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
  core = spawn(exe, [], {
    cwd: path.dirname(exe),
    env: { ...process.env, PROXYPILOT_TOKEN: sessionToken },
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
  if (tokenReadyPromise) {
    await tokenReadyPromise
  }
  while (Date.now() < deadline) {
    if (!core) {
      throw new Error('proxy-core 进程已退出，无法就绪')
    }
    try {
      const headers: Record<string, string> = sessionToken ? { 'X-Token': sessionToken } : {}
      const res = await fetch(`${API_BASE}/api/status`, { headers })
      if (res.ok) {
        return
      }
      lastErr = `HTTP ${res.status}`
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e)
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error(
    `proxy-core 未在 ${timeoutMs}ms 内就绪（最后错误: ${lastErr}）。` +
      `请确认端口 ${API_BASE.replace('http://', '')} 未被其他进程占用，` +
      `且 proxy-core 可正常运行。`,
  )
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

function showMainWindow(): void {
  if (!mainWindow || mainWindow.isDestroyed()) {
    createWindow()
    return
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
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: '显示主窗口', click: showMainWindow },
      { type: 'separator' },
      { label: '退出程序', click: () => app.quit() },
    ]),
  )
  // Windows/Linux：单击托盘图标显示主窗口（macOS 单击默认弹出菜单）
  tray.on('click', showMainWindow)
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

  mainWindow.on('close', (e) => {
    // 默认最小化到托盘；仅当用户选择「退出程序」或应用真正退出时才关闭窗口
    if (!isQuitting && loadAppSettings().closeBehavior === 'minimize') {
      e.preventDefault()
      mainWindow?.hide()
    }
  })

  mainWindow.on('closed', () => {
    mainWindow = null
  })
}

ipcMain.handle('get-token', async () => {
  if (tokenReady && sessionToken) return sessionToken
  if (tokenReadyPromise) {
    await tokenReadyPromise
  }
  return sessionToken
})
ipcMain.handle('get-api-base', () => API_BASE)
ipcMain.handle('get-platform', () => process.platform)
ipcMain.handle('get-app-settings', () => loadAppSettings())
ipcMain.handle('set-app-settings', (_e, settings: AppSettings) => {
  saveAppSettings(settings)
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

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()

    if (mainWindow && mainWindow.isMinimized()) mainWindow.restore()

    mainWindow?.show()
    mainWindow?.focus()
  })
})

app.on('window-all-closed', () => {
  // macOS 惯例是关闭所有窗口后应用常驻 Dock；但用户选择「退出程序」行为时，
  // 关闭主窗口即应直接退出应用（不受平台惯例限制）
  if (process.platform !== 'darwin' || loadAppSettings().closeBehavior === 'quit') {
    app.quit()
  }
})

app.on('before-quit', () => {
  isQuitting = true
  stopCore()
})
