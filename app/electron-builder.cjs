// @ts-check
/**
 * electron-builder 打包配置（独立于 package.json）。
 *
 * 通过环境变量 PP_CORE_BIN 指定 proxy-core 二进制文件名：
 *   - dist:win    -> PP_CORE_BIN=proxy-core.exe
 *   - dist:linux  -> PP_CORE_BIN=proxy-core
 *   - dist:mac    -> PP_CORE_BIN=proxy-core
 *   - dist        -> 未指定时按构建机平台推断（本机打包本机平台）
 * 确保 Windows 打包包含 proxy-core.exe，Linux/macOS 打包包含 proxy-core。
 *
 * @file electron-builder 配置
 * @see https://www.electron.build/configuration
 */
const path = require('node:path')

/**
 * 返回 electron-builder 配置对象。
 *
 * 通过 JSDoc 类型标注（`() => import('electron-builder').Configuration`）
 * 让编辑器识别返回值的类型，编辑下方 return 对象时即可获得完整的
 * 属性补全、字段说明与类型检查。
 *
 * @type {() => import('electron-builder').Configuration}
 */
module.exports = () => {
  const coreBin =
    process.env.PP_CORE_BIN ||
    (process.platform === 'win32' ? 'proxy-core.exe' : 'proxy-core')

  // 默认构建当前主机架构：本地 `npm run dist` 只产出本机架构的安装包。
  // CI 通过 dist:*:arm64 / dist:mac:x64 等脚本的 --x64/--arm64 参数显式覆盖。
  const hostArch = process.arch === 'arm64' ? 'arm64' : 'x64'

  return {
    appId: 'com.axetroy.proxypilot',
    productName: 'ProxyPilot',
    // 安装包命名遵循 electron-builder 升级检测（electron-updater）约定的格式：
    // ${productName}-${version}-${os}-${arch}.${ext}
    // 例如：ProxyPilot-0.1.5-mac-arm64.dmg、ProxyPilot-0.1.5-win-x64.exe、
    // ProxyPilot-0.1.5-linux-x64.AppImage（包名明确包含平台与架构）。
    // 注意：必须用单引号字符串；若用 JS 模板字符串（反引号），
    // ${productName} 等宏会被立即插值，导致产物命名错误。
    artifactName: '${productName}-${version}-${os}-${arch}.${ext}',
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
      // 应用图标，供主进程 BrowserWindow 使用（打包后位于 resources/icon.png）
      {
        from: path.join(__dirname, 'build', 'icon.png'),
        to: 'icon.png',
      },
    ],
    win: {
      icon: 'build/icon.ico',
      // 默认跟随当前主机架构；其他架构由 dist:win:arm64（--win --arm64）覆盖构建
      target: [{ target: 'nsis', arch: [hostArch] }],
    },
    nsis: {
      oneClick: false,
      allowToChangeInstallationDirectory: true,
      createDesktopShortcut: true,
      createStartMenuShortcut: true,
    },
    linux: {
      icon: 'build/icon.png',
      // 默认跟随当前主机架构；其他架构由 dist:linux:arm64（--linux --arm64）覆盖构建。
      // 注意同一平台多架构需分开构建：extraResources 里的 proxy-core
      // 是单个二进制，一次构建只能对应一个目标架构。
      target: [{ target: 'AppImage', arch: [hostArch] }],
      category: 'Network',
    },
    mac: {
      icon: 'build/icon.png',
      // 默认跟随当前主机架构（Apple Silicon 上产 arm64）；x64 由
      // dist:mac:x64（--mac --x64）覆盖构建，同一平台多架构分开构建以保证
      // proxy-core 架构匹配。dmg 只能在 macOS 上构建。
      target: [{ target: 'dmg', arch: [hostArch] }],
      category: 'public.app-category.utilities',
      // 未配置 Apple Developer 证书：明确不签名（identity: null），
      // 否则 electron-builder 会尝试自动发现证书导致构建失败。
      // 有证书后改为 identity: 'Developer ID Application: <你的名字>' 并配置公证。
      identity: null,
    },
  }
}