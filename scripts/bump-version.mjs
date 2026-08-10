#!/usr/bin/env node
/**
 * 版本号升级脚本
 *
 * 用法：
 *   node scripts/bump-version.mjs patch          # 0.1.0 -> 0.1.1
 *   node scripts/bump-version.mjs minor          # 0.1.0 -> 0.2.0
 *   node scripts/bump-version.mjs major          # 0.1.0 -> 1.0.0
 *   node scripts/bump-version.mjs 1.2.3          # 直接指定版本号
 *   node scripts/bump-version.mjs patch --tag    # 更新版本 + git commit + 打 tag v0.1.1
 *   node scripts/bump-version.mjs patch --push   # 更新版本 + commit + tag + 推送远程（触发 CI 发布）
 *
 * 同步更新的文件（以 app/package.json 为版本源）：
 *   - app/package.json            Electron 应用版本（electron-builder 打包版本）
 *   - app/package-lock.json       根包版本（保持与 package.json 一致，否则 npm ci 报错）
 *   - proxy-core/config/config.go Go 本地开发默认版本（发布时由 goreleaser ldflags 注入覆盖）
 */
import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

const args = process.argv.slice(2)
const bumpArg = args.find((a) => !a.startsWith('--'))
const withTag = args.includes('--tag')
const withPush = args.includes('--push')

if (!bumpArg) {
  console.error('用法: node scripts/bump-version.mjs <major|minor|patch|X.Y.Z> [--tag] [--push]')
  process.exit(1)
}

// ---------- 读取当前版本 ----------
const appPkgPath = path.join(root, 'app', 'package.json')
const appPkg = JSON.parse(readFileSync(appPkgPath, 'utf8'))
const current = appPkg.version

// ---------- 计算新版本 ----------
let next
if (/^\d+\.\d+\.\d+$/.test(bumpArg)) {
  next = bumpArg
} else if (['major', 'minor', 'patch'].includes(bumpArg)) {
  const [major, minor, patch] = current.split('.').map(Number)
  if (bumpArg === 'major') next = `${major + 1}.0.0`
  else if (bumpArg === 'minor') next = `${major}.${minor + 1}.0`
  else next = `${major}.${minor}.${patch + 1}`
} else {
  console.error(`无效的版本参数: ${bumpArg}（应为 major|minor|patch 或 X.Y.Z）`)
  process.exit(1)
}

if (next === current) {
  console.error(`版本未变化: ${current}`)
  process.exit(1)
}

// ---------- --tag / --push 时要求工作区干净，避免把无关改动一起提交 ----------
if (withTag || withPush) {
  const status = execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim()
  if (status) {
    console.error('工作区有未提交的改动，请先提交或 stash：\n' + status)
    process.exit(1)
  }
}

// ---------- 更新 app/package.json ----------
appPkg.version = next
writeFileSync(appPkgPath, JSON.stringify(appPkg, null, 2) + '\n')

// ---------- 更新 app/package-lock.json（仅根包版本，依赖版本不动） ----------
const lockPath = path.join(root, 'app', 'package-lock.json')
const lock = JSON.parse(readFileSync(lockPath, 'utf8'))
lock.version = next
if (lock.packages?.['']) lock.packages[''].version = next
writeFileSync(lockPath, JSON.stringify(lock, null, 2) + '\n')

// ---------- 更新 proxy-core/config/config.go ----------
const goPath = path.join(root, 'proxy-core', 'config', 'config.go')
const goSrc = readFileSync(goPath, 'utf8')
const goNext = goSrc.replace(/var Version = "[^"]*"/, `var Version = "${next}"`)
if (goNext === goSrc) {
  console.error('config.go 中未找到 var Version 定义，请检查文件格式')
  process.exit(1)
}
writeFileSync(goPath, goNext)

console.log(`版本已更新: ${current} -> ${next}`)
console.log('  - app/package.json')
console.log('  - app/package-lock.json')
console.log('  - proxy-core/config/config.go')

// ---------- git commit + tag ----------
if (withTag || withPush) {
  const files = ['app/package.json', 'app/package-lock.json', 'proxy-core/config/config.go']
  execFileSync('git', ['add', ...files], { cwd: root, stdio: 'inherit' })
  execFileSync('git', ['commit', '-m', `chore: bump version to v${next}`], { cwd: root, stdio: 'inherit' })
  execFileSync('git', ['tag', `v${next}`], { cwd: root, stdio: 'inherit' })
  console.log(`已创建 tag: v${next}`)
}

// ---------- 推送远程（触发 CI 发布） ----------
if (withPush) {
  execFileSync('git', ['push'], { cwd: root, stdio: 'inherit' })
  execFileSync('git', ['push', 'origin', `v${next}`], { cwd: root, stdio: 'inherit' })
  console.log(`已推送，v${next} tag 将触发 CI 发布`)
}