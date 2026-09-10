// 后端生产构建的 ESM 修复脚本。
// 问题：tsconfig 用 `moduleResolution: "bundler"`，允许源码写无扩展名的相对导入
// （`from "../db/store"`），但 tsc 原样emit到 dist。Node ESM 在运行时不解析无扩展名，
// 导致 `node dist/server.js` 抛 ERR_MODULE_NOT_FOUND（如 Cannot find module dist/models/router）。
// 解决：构建后遍历 dist/**/*.js，把相对导入/动态导入补上 `.js`（幂等）。
import { readdirSync, statSync, readFileSync, writeFileSync } from "node:fs";
import { join, resolve, extname, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../dist", import.meta.url));
let changed = 0;

function walk(dir) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) {
      walk(full);
    } else if (extname(full) === ".js") {
      fix(full);
    }
  }
}

// 把 `from "./x"` / `from "../x"` 和 `import("./x")` 中不带扩展名的相对路径补 `.js`
function fix(file) {
  const src = readFileSync(file, "utf8");
  let out = "";
  let i = 0;
  const re = /(from\s*["']|import\s*\(\s*["'])(\.[^"')]+)(["'])/g;
  let last = 0;
  let m;
  let localChanged = 0;
  while ((m = re.exec(src)) !== null) {
    out += src.slice(last, m.index);
    let spec = m[2];
    // 去掉可能已有的 .js/.json，幂等处理
    const hadExt = /\.[a-z]+$/i.test(spec);
    if (hadExt) {
      out += m[1] + spec + m[3];
    } else {
      out += m[1] + spec + ".js" + m[3];
      localChanged++;
    }
    last = m.index + m[0].length;
  }
  out += src.slice(last);
  if (localChanged > 0) {
    writeFileSync(file, out, "utf8");
    changed += localChanged;
    console.log(`fix-esm: ${file.replace(root + sep, "")} (+${localChanged} specifier(s))`);
  }
}

walk(root);
console.log(`fix-esm done: patched ${changed} import specifier(s) under dist/.`);
