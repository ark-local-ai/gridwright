// ===== 桌面单文件后端 exe（Node SEA）构建脚本 =====
// 把后端打包成一个无需安装 Node 的 `ark-backend(.exe)`：
//   1. esbuild 把 dist/server.js 连同所有依赖打成单个 CJS 文件（fastify 是 CJS 运行时 require，故用 CJS）；
//   2. `node --experimental-sea-config` 把该 bundle 生成 SEA blob；
//   3. 复制 node.exe 为 ark-backend(.exe)；
//   4. postject 把 blob 注入 exe（Windows 需 --sentinel-fuse）。
// 产物输出到 dist/（gitignored）。运行：
//   node scripts/build-sea.mjs        # Windows → dist/ark-backend.exe
// 生成的 exe 独立运行（PORT 环境变量可改端口，ARK_DATA_DIR/ARK_WORKSPACE_DIR 可重定向数据目录）。
import { execSync } from "node:child_process";
import { copyFileSync, mkdirSync, rmSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = join(__dirname, "..");
const isWin = process.platform === "win32";
const exeName = isWin ? "ark-backend.exe" : "ark-backend";
const out = join(root, "dist", exeName);

// node.exe 与 postject 二进制路径
const nodeExe = process.execPath;
const postjectBin = join(root, "node_modules", ".bin", isWin ? "postject.cmd" : "postject");

mkdirSync(join(root, "dist"), { recursive: true });
rmSync(out, { force: true });

// 1) esbuild → 单文件 CJS bundle
const bundle = join(root, "dist", "_backend.bundle.cjs");
const esbuildBin = join(root, "node_modules", "esbuild", "bin", "esbuild");
console.log("→ esbuild bundle (CJS)");
run(`${process.execPath} ${JSON.stringify(esbuildBin)} dist/server.js --bundle --format=cjs --platform=node --target=node22 --outfile=${JSON.stringify(bundle)}`);

// 2) SEA config + blob
const seaConfig = join(root, "dist", "_sea-config.json");
writeFileSync(seaConfig, JSON.stringify({
  main: bundle,
  output: join(root, "dist", "_sea.blob"),
  disableExperimentalSEAWarning: true,
  useSnapshot: false,
  useCodeCache: false,
}, null, 2));
console.log("→ generate SEA blob");
run(`${process.execPath} --experimental-sea-config ${JSON.stringify(seaConfig)}`);

// 3) 复制 node.exe 为 exe
console.log(`→ copy node.exe → ${exeName}`);
copyFileSync(nodeExe, out);

// 4) postject 注入 blob（Windows 需要 sentinel fuse）
const blob = join(root, "dist", "_sea.blob");
const fuse = "NODE_SEA_FUSE_fce680ab2cc467b6e072b8b5df1996b2";
console.log("→ inject blob (postject)");
try {
  run(`${JSON.stringify(postjectBin)} ${JSON.stringify(out)} NODE_SEA_BLOB ${JSON.stringify(blob)} --sentinel-fuse ${fuse}`);
} catch (e) {
  // 兼容无 --sentinel-fuse 的 postject 老版本
  run(`${JSON.stringify(postjectBin)} ${JSON.stringify(out)} NODE_SEA_BLOB ${JSON.stringify(blob)}`);
}

// 清理中间文件
rmSync(bundle, { force: true });
rmSync(seaConfig, { force: true });
rmSync(blob, { force: true });

console.log(`\n✔ 后端单文件构建完成：${out}`);
if (!existsSync(out)) {
  console.error("! 产物未生成，请检查上方错误");
  process.exitCode = 1;
}

function run(cmd) {
  execSync(cmd, { stdio: "inherit", cwd: root });
}
