// ===== 本地数据/工作空间/技能 目录解析（统一入口） =====
// 散落各处的硬编码路径（store.ts dataDir、server/spaces workspace、routes/skills 的 skills）统一收敛到这里。
// 约束：
//   - **CJS 兼容**：不用 import.meta.url（fastify 是 CJS，桌面单文件 exe 用 esbuild --format=cjs 打包，
//     import.meta 在 CJS 下不可用）；
//   - **开发态 cwd 无关**：用 process.argv[1]（入口脚本真实路径，dist/server.js 或 bundle）定位 backend 根，
//     无论从哪个 cwd 启动都能找到 apps/backend 旁的 data / frontend/public/workspace；
//   - **打包态**：由 Tauri 壳注入 ARK_DATA_DIR / ARK_WORKSPACE_DIR / ARK_SKILLS_DIR（用户可写目录），
//     优先生效，避免数据写进 exe/安装目录。

import { dirname, join, isAbsolute, resolve } from "node:path";

function backendRoot(): string {
  if (process.env.ARK_BASE_DIR) {
    return isAbsolute(process.env.ARK_BASE_DIR)
      ? process.env.ARK_BASE_DIR
      : join(process.cwd(), process.env.ARK_BASE_DIR);
  }
  const entry = process.argv[1];
  if (entry) {
    // 入口真实路径：.../apps/backend/dist/server.js（node dist）或 .../apps/backend/src/server.ts（tsx dev）
    const here = dirname(resolve(entry)); // .../apps/backend/dist 或 .../apps/backend/src
    return join(here, ".."); // 上取一层 → apps/backend（修正：原先 ../.. 会多跳一层到 apps）
  }
  return process.cwd();
}

function envOr(name: string, devPath: string): string {
  const v = process.env[name];
  if (v) return isAbsolute(v) ? v : join(process.cwd(), v);
  return devPath;
}

const root = backendRoot();

/** SQLite 数据目录（ark.db） */
export const dataDir = envOr("ARK_DATA_DIR", join(root, "data"));

/** 工作空间根目录（资料/成果文件） */
export const workspaceRoot = envOr("ARK_WORKSPACE_DIR", join(root, "..", "frontend", "public", "workspace"));

/** 技能 Markdown 目录 */
export const skillsDir = envOr("ARK_SKILLS_DIR", join(root, "skills"));
