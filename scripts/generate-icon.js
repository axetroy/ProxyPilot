// 生成应用图标：将 build/icon.svg 渲染为 build/icon.png（512x512）与 build/icon.ico
// 用法: npx electron scripts/generate-icon.js
// 依赖 Electron 离屏渲染（Chromium），无需额外图像库。
const { app, BrowserWindow } = require('electron')
const fs = require('node:fs')
const path = require('node:path')

const buildDir = path.join(__dirname, '..', 'app', 'build')
const svgPath = path.join(buildDir, 'icon.svg')
const pngPath = path.join(buildDir, 'icon.png')
const icoPath = path.join(buildDir, 'icon.ico')

// 将 PNG 打包为 ICO（PNG 内嵌格式，Windows Vista+ 支持，可容纳 512x512）
function createIco(pngBuffer) {
  const header = Buffer.alloc(6)
  header.writeUInt16LE(0, 0) // reserved
  header.writeUInt16LE(1, 2) // type: icon
  header.writeUInt16LE(1, 4) // count: 1

  const entry = Buffer.alloc(16)
  entry.writeUInt8(0, 0) // width（0 表示 256，Windows 会读取 PNG 实际尺寸）
  entry.writeUInt8(0, 1) // height
  entry.writeUInt8(0, 2) // color count
  entry.writeUInt8(0, 3) // reserved
  entry.writeUInt16LE(1, 4) // planes
  entry.writeUInt16LE(32, 6) // bit count
  entry.writeUInt32LE(pngBuffer.length, 8) // data size
  entry.writeUInt32LE(22, 12) // data offset（6 + 16 = 22）

  return Buffer.concat([header, entry, pngBuffer])
}

// 强制 1x 缩放，避免 Windows DPI 缩放导致输出尺寸不是 512x512
app.commandLine.appendSwitch('force-device-scale-factor', '1')

app.whenReady().then(async () => {
  try {
    const win = new BrowserWindow({
      width: 512,
      height: 512,
      useContentSize: true,
      show: false,
      webPreferences: { offscreen: true },
    })
    await win.loadFile(svgPath)
    // 等待 SVG 渲染完成
    await new Promise((r) => setTimeout(r, 800))
    const image = await win.webContents.capturePage()
    const png = image.toPNG()
    fs.writeFileSync(pngPath, png)
    fs.writeFileSync(icoPath, createIco(png))
    console.log(`已生成: ${pngPath} (${image.getSize().width}x${image.getSize().height})`)
    console.log(`已生成: ${icoPath}`)
    win.destroy()
  } catch (e) {
    console.error('生成图标失败:', e)
    process.exitCode = 1
  } finally {
    app.quit()
  }
})