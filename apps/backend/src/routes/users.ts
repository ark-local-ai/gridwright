// ===== 管理端 API（M67）：用户管理 =====
// GET  /api/users              → 用户列表 + 用量（任务/记忆/会话/最近活跃）
// POST /api/users/:id/disable  → 禁用用户（其会话立即失效；不能禁自己/最后一个管理员）
// POST /api/users/:id/enable   → 启用用户
// DELETE /api/users/:id        → 删除用户（不能删自己/最后一个管理员；连带清数据）
// 全部为「首个注册用户即管理员」的自托管多人服务端设计，仅管理员可访问。
// 读操作（GET）经 adminGuard 也要求管理员——用户清单属敏感管理信息。

import type { FastifyInstance, FastifyRequest } from "fastify";
import {
  listUsers, setUserDisabled, countAdmins, findUserById,
} from "../db/store.js";
import { currentUser } from "./auth.js";

function requireAdmin(req: FastifyRequest): boolean {
  const me = currentUser(req);
  return !!me && me.isAdmin;
}

export async function userRoutes(app: FastifyInstance) {
  // 读用户清单也要管理员（管理信息，不求开放浏览）
  app.addHook("preHandler", async (req, reply) => {
    if (!requireAdmin(req)) {
      return reply.code(403).send({ error: "仅管理员可访问" });
    }
  });

  app.get("/", async () => ({ users: listUsers() }));

  app.post<{ Params: { id: string } }>("/:id/disable", async (req, reply) => {
    const me = currentUser(req)!;
    const target = findUserById(req.params.id);
    if (!target) return reply.code(404).send({ error: "用户不存在" });
    if (target.id === me.id) return reply.code(400).send({ error: "不能禁用自己" });
    if (target.isAdmin && countAdmins() <= 1) return reply.code(400).send({ error: "不能禁用最后一个管理员" });
    setUserDisabled(target.id, true);
    return { ok: true };
  });

  app.post<{ Params: { id: string } }>("/:id/enable", async (req, reply) => {
    const target = findUserById(req.params.id);
    if (!target) return reply.code(404).send({ error: "用户不存在" });
    setUserDisabled(target.id, false);
    return { ok: true };
  });

  app.delete<{ Params: { id: string } }>("/:id", async (req, reply) => {
    const me = currentUser(req)!;
    const target = findUserById(req.params.id);
    if (!target) return reply.code(404).send({ error: "用户不存在" });
    if (target.id === me.id) return reply.code(400).send({ error: "不能删除自己" });
    if (target.isAdmin && countAdmins() <= 1) return reply.code(400).send({ error: "不能删除最后一个管理员" });
    // 删除用户：连带其数据（任务/记忆/会话），从库清理 + 工作区文件略（任务交付文件随任务删除）
    const { deleteUserWithData } = await import("../db/store.js");
    deleteUserWithData(target.id);
    return { ok: true };
  });
}
