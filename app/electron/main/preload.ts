import { contextBridge, ipcRenderer } from 'electron'
import type { AppSettings } from './app-settings'
import type { UpdaterState } from './updater'

contextBridge.exposeInMainWorld('proxypilot', {
  getToken: (): Promise<string> => ipcRenderer.invoke('get-token'),
  getApiBase: (): Promise<string> => ipcRenderer.invoke('get-api-base'),
  getPlatform: (): Promise<string> => ipcRenderer.invoke('get-platform'),
  getAppSettings: (): Promise<AppSettings> => ipcRenderer.invoke('get-app-settings'),
  setAppSettings: (settings: AppSettings): Promise<AppSettings> => ipcRenderer.invoke('set-app-settings', settings),

  // ---- 更新机制 ----
  getUpdaterState: (): Promise<UpdaterState> => ipcRenderer.invoke('updater:get-state'),
  checkForUpdates: (): Promise<UpdaterState> => ipcRenderer.invoke('updater:check'),
  setAutoUpdate: (enabled: boolean): Promise<UpdaterState> => ipcRenderer.invoke('updater:set-auto-update', enabled),
  installUpdate: (): Promise<void> => ipcRenderer.invoke('updater:install'),
  onUpdaterEvent: (cb: (state: UpdaterState) => void): (() => void) => {
    const listener = (_e: Electron.IpcRendererEvent, state: UpdaterState) => cb(state)
    ipcRenderer.on('updater:event', listener)
    return () => {
      ipcRenderer.removeListener('updater:event', listener)
    }
  },

  onCoreExit: (cb: () => void): void => {
    ipcRenderer.on('core:exit', () => cb())
  },
  onCoreError: (cb: (msg: string) => void): void => {
    ipcRenderer.on('core:error', (_e, msg: string) => cb(msg))
  },
})
