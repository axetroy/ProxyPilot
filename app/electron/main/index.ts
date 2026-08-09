import { app, BrowserWindow, ipcMain, Menu } from 'electron'
import { spawn, ChildProcess } from 'node:child_process'
import { existsSync } from 'node:fs'
import * as path from 'node:path'

const API_BASE = 'http://127.0.0.1:17890'

let core: ChildProcess | null = null
let sessionToken = ''
let mainWindow: BrowserWindow | null = null
let tokenReadyPromise: Promise<void> | null = null
let resolveTokenReady: (() => void) | null = null
let tokenReady = false

function resolveCorePath(): string {
  // 打包后：<resources>/proxy-core.exe（extraResources 放置位置，与 app.asar 同级）
  const packaged = path.join(process.resourcesPath, 'proxy-core.exe')
  if (existsSync(packaged)) return packaged

  // 开发模式：<repo>/proxy-core/proxy-core.exe
  const dev = path.join(app.getAppPath(), '..', 'proxy-core', 'proxy-core.exe')
  if (existsSync(dev)) return dev

  // 兜底：app 目录下
  const fallback = path.join(app.getAppPath(), 'proxy-core.exe')
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
    console.error(`[core] proxy-core.exe not found at: ${exe}`)
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.webContents.send('core:error', `proxy-core.exe not found: ${exe}`)
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
      `且 proxy-core.exe 可正常运行。`,
  )
}

function createWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1200,
    height: 800,
    title: 'ProxyPilot',
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

app.whenReady().then(async () => {
  // 移除默认菜单栏（File/Edit/View 等），保留系统自带的窗口边框（最小化/最大化/关闭）
  Menu.setApplicationMenu(null)

  try {
    await startCore()
    await waitForCore()
  } catch (e) {
    console.error(e)
  }
  createWindow()

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

app.on('before-quit', stopCore)
