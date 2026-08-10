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
    // 禁用 electron-builder 自动发布：检测到 GITHUB_TOKEN 时它会在 tag 构建
    // 尝试自行上传到 GitHub（runner 代理下报 self-signed certificate 错误）。
    // 上传统一由 CI 的 softprops/action-gh-release 负责。
    publish: null,
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
      // 未配置 Apple Developer 证书：明确不签名（identity: null），
      // 否则 electron-builder 会尝试自动发现证书导致构建失败。
      // 有证书后改为 identity: 'Developer ID Application: <你的名字>' 并配置公证。
      identity: null,
    },
  }
}