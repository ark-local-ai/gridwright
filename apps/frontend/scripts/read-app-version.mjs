// 读出**官网自己的**版本号（apps/frontend/package.json）。
//
// 为什么单独成文件而不是在 shell 里 `node -p "require('...')"`：
// 这个仓库的路径含中文，在 Windows 的命令行里传内联 JS 会因转义而失败
// （实测 MODULE_NOT_FOUND）。用 import.meta.url 相对定位就与调用方 cwd 无关。
//
// 曾经这里读的是 apps/desktop/package.json。前端拆包后官网与桌面端是两个独立
// 发布的产品，官网的版本号由官网自己的 package.json 决定——vite.config.ts 的
// VITE_APP_VERSION 也是从那里取的。读错文件就会出现"发布脚本拿桌面端的版本号
// 去校验官网产物"：两边一旦不同步，部署会被拒绝，而报错完全指不到真正的原因。
import { readFileSync } from 'node:fs'

const pkg = JSON.parse(
  readFileSync(new URL('../package.json', import.meta.url), 'utf-8'),
)
process.stdout.write(pkg.version)
