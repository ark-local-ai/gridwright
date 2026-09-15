// 读出应用版本号（apps/desktop/package.json 是唯一来源，见 frontend/vite.config.ts）。
//
// 为什么单独成文件而不是在 shell 里 `node -p "require('...')"`：
// 这个仓库的路径含中文，在 Windows 的命令行里传内联 JS 会因转义而失败
// （实测 MODULE_NOT_FOUND）。用 import.meta.url 相对定位就与调用方 cwd 无关。
import { readFileSync } from 'node:fs'

const pkg = JSON.parse(
  readFileSync(new URL('../../desktop/package.json', import.meta.url), 'utf-8'),
)
process.stdout.write(pkg.version)
