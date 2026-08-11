import { contextBridge, ipcRenderer } from 'electron'

interface AppSettings {
  closeBehavior: 'minimize' | 'quit'
}

contextBridge.exposeInMainWorld('proxypilot', {
  getToken: (): Promise<string> => ipcRenderer.invoke('get-token'),
  getApiBase: (): Promise<string> => ipcRenderer.invoke('get-api-base'),
  getPlatform: (): Promise<string> => ipcRenderer.invoke('get-platform'),
  getAppSettings: (): Promise<AppSettings> => ipcRenderer.invoke('get-app-settings'),
  setAppSettings: (settings: AppSettings): Promise<AppSettings> => ipcRenderer.invoke('set-app-settings', settings),
  onCoreExit: (cb: () => void): void => {
    ipcRenderer.on('core:exit', () => cb())
  },
  onCoreError: (cb: (msg: string) => void): void => {
    ipcRenderer.on('core:error', (_e, msg: string) => cb(msg))
  },
})