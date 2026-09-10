// ===== 多工作空间 API（SQLite spaces 表 + 磁盘目录） =====
// GET    /api/spaces            空间列表（含各目录文件数）
// POST   /api/spaces            {name, dir?} 新建（自动建目录）
// PUT    /api/spaces/:id        {name?, dir?, on?} 更新（改名/改目录/设活动）
// DELETE /api/spaces/:id        删除空间（可保留磁盘文件）
// POST   /api/spaces/:id/active 设为活动空间

import type { FastifyInstance } from "fastify";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { scanWorkspace } from "../tools/workspace";
import { currentUser } from "./auth.js";
import {
  listSpaces, getSpace, createSpace, updateSpace, deleteSpace, getActiveSpace, getSpaceByDir,
} from "../db/store";

// 用 __dirname 定位（与 server.ts 一致），不依赖 process.cwd()：
// 保证无论后端从哪个目录启动，web 与桌面版都读写同一个工作空间目录，存档保持同步。
const __dirname = dirname(fileURLToPath(import.meta.url));
const workRoot = join(__dirname, "..", "..", "..", "frontend", "public", "workspace");

/** 空间工作目录：dir 为空串 → 根目录（默认工作空间，兼容既有平铺文件） */
export function spaceDir(dir: string): string {
  return dir ? join(workRoot, dir) : workRoot;
}

/** 空间 ID → 磁盘目录；未找到返回 null */
export function resolveSpaceDir(id: string): string | null {
  const s = getSpace(id);
  if (!s) return null;
  return spaceDir(s.dir);
}

export async function spaceRoutes(app: FastifyInstance) {
  // 空间列表（含文件数）；多用户隔离：登录用户见自己的+全局，未登录见全部
  app.get("/", async (req) => {
    const userId = currentUser(req)?.id;
    return listSpaces(userId).map((s) => ({
      ...s,
      files: scanWorkspace(spaceDir(s.dir)).length,
    }));
  });

  // 新建：生成 id 作为目录名（避免中文/重复目录），也可显式传入 dir
  app.post<{ Body: { name?: string; dir?: string } }>("/", async (req, reply) => {
    const name = req.body?.name?.trim();
    if (!name) return reply.code(400).send({ error: "name 不能为空" });
    const id = `sp-${Math.random().toString(36).slice(2, 8)}`;
    const dir = req.body?.dir?.trim() || id;
    // 目录冲突检查
    if (getSpaceByDir(dir)) return reply.code(400).send({ error: "目录已存在" });
    const userId = currentUser(req)?.id; // 归属当前登录用户；未登录 → 全局
    const space = createSpace(name, dir, id, userId);
    mkdirSync(spaceDir(dir), { recursive: true });
    return reply.code(201).send(space);
  });

  app.put<{ Params: { id: string }; Body: { name?: string; dir?: string; isActive?: boolean } }>(
    "/:id", async (req, reply) => {
      const space = updateSpace(req.params.id, req.body ?? {});
      if (!space) return reply.code(404).send({ error: "空间不存在" });
      if (req.body?.dir && req.body.dir !== space.dir) mkdirSync(spaceDir(space.dir), { recursive: true });
      return reply.send(space);
    },
  );

  app.delete<{ Params: { id: string } }>("/:id", async (req, reply) => {
    if (!deleteSpace(req.params.id)) return reply.code(404).send({ error: "空间不存在" });
    return reply.code(204).send();
  });

  // 设为活动空间
  app.post<{ Params: { id: string } }>("/:id/active", async (req, reply) => {
    const target = getSpace(req.params.id);
    if (!target) return reply.code(404).send({ error: "空间不存在" });
    // 清空其它空间的活动标记，仅目标置活动
    listSpaces().forEach((s) => updateSpace(s.id, { isActive: s.id === target.id }));
    return reply.send(updateSpace(target.id, { isActive: true }));
  });

  // 当前活动空间
  app.get("/active", async () => getActiveSpace() ?? null);

  // 某空间目录下的文件列表
  app.get<{ Params: { id: string } }>("/:id/files", async (req, reply) => {
    const dir = resolveSpaceDir(req.params.id);
    if (!dir) return reply.code(404).send({ error: "空间不存在" });
    return scanWorkspace(dir);
  });
}
