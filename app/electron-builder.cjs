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

  // 注意：target 里【不要】写死 arch。electron-builder 中 target 配置里的
  // `arch` 优先级高于 CLI 的 --x64/--arm64 参数，且当 CLI 指定了架构而某个
  // 架构条目没有对应 target 时，会回退到该平台的 defaultTarget
  // （Linux 默认是 ["snap", "appimage"]），导致：
  //   1. CI 的 dist:*:arm64 实际仍产出 x64 产物；
  //   2. linux-arm64 构建意外触发 snap target，在无 snapcraft 的
  //      GitHub Actions runner 上报 "snapcraft process failed ENOENT"。
  // 因此这里只声明 target 名称，架构完全由 CLI 的 --x64/--arm64 决定；
  // 未传 flag 时（本地 `npm run dist`）electron-builder 默认构建当前主机架构。

  return {
    appId: 'com.axetroy.proxypilot',
    productName: 'ProxyPilot',
    // 安装包压缩级别：maximum 以更慢的打包时间为代价换取更小的安装包
    // （NSIS 的 7z 压缩 / AppImage 的 squashfs 压缩级别均提升）。
    compression: 'maximum',
    // 安装包命名遵循 electron-builder 升级检测（electron-updater）约定的格式：
    // ${productName}-${version}-${os}-${arch}.${ext}
    // 例如：ProxyPilot-0.1.5-mac-arm64.dmg、ProxyPilot-0.1.5-win-x64.exe、
    // ProxyPilot-0.1.5-linux-x64.AppImage（包名明确包含平台与架构）。
    // 注意：必须用单引号字符串；若用 JS 模板字符串（反引号），
    // ${productName} 等宏会被立即插值，导致产物命名错误。
    artifactName: '${productName}-${version}-${os}-${arch}.${ext}',
    // 更新源：GitHub Releases（electron-updater 运行时从这里下载）。
    // 声明 publish 配置后 electron-builder 才会生成更新元数据（latest*.yml）。
    // CI 会把它们统一改名为「平台-架构」格式（windows-x64.yml / darwin-arm64.yml 等，
    // 见 .github/workflows/ci.yml），electron-updater 已被 patch（patches/
    // electron-updater+6.8.9.patch，patch-package 在 npm install 时自动应用）按本机
    // 平台+架构读取对应文件——避免同一平台多架构 job 生成同名 yml 上传互相覆盖
    // （v0.1.11 事故：x64 用户下载到 arm64 安装包，应用被卸载后未重装）。
    // 上传仍统一由 CI 的 softprops/action-gh-release 负责：dist 脚本都带
    // `--publish never`，禁止 electron-builder 在 tag 构建时自行上传
    // （runner 代理下会报 self-signed certificate 错误）。
    publish: {
      provider: 'github',
      owner: 'axetroy',
      repo: 'ProxyPilot',
    },
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
      // macOS 菜单栏托盘模板图标（16x16 + @2x，打包后位于 resources/trayTemplate*.png）
      {
        from: path.join(__dirname, 'build', 'trayTemplate.png'),
        to: 'trayTemplate.png',
      },
      {
        from: path.join(__dirname, 'build', 'trayTemplate@2x.png'),
        to: 'trayTemplate@2x.png',
      },
      // 第三方许可证声明（Apache-2.0 等要求保留归属），打包后位于 resources/THIRD_PARTY_NOTICES.md
      {
        from: path.join(__dirname, '..', 'THIRD_PARTY_NOTICES.md'),
        to: 'THIRD_PARTY_NOTICES.md',
      },
    ],
    win: {
      icon: 'build/icon.ico',
      // 架构由 CLI --x64/--arm64 决定（dist:win / dist:win:arm64）；
      // 未指定时默认构建当前主机架构。NSIS 支持 arm64。
      target: 'nsis',
    },
    nsis: {
      oneClick: false,
      allowToChangeInstallationDirectory: true,
      createDesktopShortcut: true,
      createStartMenuShortcut: true,
    },
    linux: {
      icon: 'build/icon.png',
      // 只构建 AppImage，架构由 CLI --x64/--arm64 决定（dist:linux / dist:linux:arm64）。
      // 同平台多架构需分开构建：extraResources 里的 proxy-core 是单个二进制，
      // 一次构建只能对应一个目标架构。不要在这里声明 arch，否则会覆盖 CLI 参数
      // 并可能意外触发 snap target（见文件头部注释）。
      target: 'AppImage',
      category: 'Network',
    },
    mac: {
      icon: 'build/icon.png',
      // 架构由 CLI --arm64/--x64 决定（dist:mac / dist:mac:x64），
      // 同平台多架构分开构建以保证 proxy-core 架构匹配。dmg 只能在 macOS 上构建。
      // 同时产出 zip：electron-updater 的 MacUpdater 依赖 Squirrel.Mac，只能消费
      // .zip 更新包（findFile(files, "zip", ["pkg", "dmg"]) 找不到 zip 会直接抛
      // ERR_UPDATER_ZIP_FILE_NOT_FOUND，dmg 不能作为更新源），dmg 仅供手动安装。
      target: ['dmg', 'zip'],
      category: 'public.app-category.utilities',
      // 未配置 Apple Developer 证书：明确不签名（identity: null），
      // 否则 electron-builder 会尝试自动发现证书导致构建失败。
      // 有证书后改为 identity: 'Developer ID Application: <你的名字>' 并配置公证。
      identity: null,
    },
  }
}