// electron-builder 配置（独立于 package.json）
// 通过环境变量 PP_CORE_BIN 指定 proxy-core 二进制文件名：
//   - dist:win    -> PP_CORE_BIN=proxy-core.exe
//   - dist:linux  -> PP_CORE_BIN=proxy-core
//   - dist:mac    -> PP_CORE_BIN=proxy-core
//   - dist        -> 未指定时按构建机平台推断（本机打包本机平台）
// 确保 Windows 打包包含 proxy-core.exe，Linux/macOS 打包包含 proxy-core。
const path = require('node:path')

module.exports = () => {
  const coreBin =
    process.env.PP_CORE_BIN ||
    (process.platform === 'win32' ? 'proxy-core.exe' : 'proxy-core')

  return {
    appId: 'com.axetroy.proxypilot',
    productName: 'ProxyPilot',
    directories: {
      output: 'release',
    },
    files: ['dist/**/*', 'dist-electron/**/*', 'package.json'],
    extraResources: [
      {
        from: path.join(__dirname, '..', 'proxy-core', coreBin),
        to: coreBin,
      },
    ],
    win: {
      target: [{ target: 'nsis', arch: ['x64'] }],
    },
    nsis: {
      oneClick: false,
      allowToChangeInstallationDirectory: true,
      createDesktopShortcut: true,
      createStartMenuShortcut: true,
    },
    linux: {
      target: [{ target: 'AppImage', arch: ['x64', 'arm64'] }],
      category: 'Network',
    },
    mac: {
      target: [{ target: 'dmg', arch: ['x64', 'arm64'] }],
      category: 'public.app-category.utilities',
    },
  }
}