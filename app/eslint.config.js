// @ts-check
// ESLint flat config（ESLint 9）
// React Compiler 健康检查：
//  - eslint-plugin-react-compiler：检测编译器无法安全处理的代码（hooks 规则违规、可变值等）
//  - eslint-plugin-react-hooks（recommended-latest）：React 19 + Compiler 时代的 hooks 规则
// 解析器用 @babel/eslint-parser（支持项目无分隔符换行的 TS interface 风格）
const babelParser = require('@babel/eslint-parser')
const reactCompiler = require('eslint-plugin-react-compiler')
const reactHooks = require('eslint-plugin-react-hooks')

module.exports = [
  {
    ignores: ['dist/**', 'dist-electron/**', 'node_modules/**', 'build/**'],
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: {
      parser: babelParser,
      parserOptions: {
        requireConfigFile: false,
        sourceType: 'module',
        babelOptions: {
          parserOpts: {
            plugins: ['typescript', 'jsx'],
          },
        },
      },
    },
    plugins: {
      'react-compiler': reactCompiler,
      'react-hooks': reactHooks,
    },
    rules: {
      // React Compiler 官方规则：编译器无法处理的代码直接报错
      'react-compiler/react-compiler': 'error',
      // React 19 + Compiler 时代的 hooks 规则集（含 rules-of-hooks、immutability、refs 等）
      ...reactHooks.configs['recommended-latest'].rules,
    },
  },
]