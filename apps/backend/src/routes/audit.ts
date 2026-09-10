// ===== 操作审计（M29） =====
// 用 Fastify onResponse 钩子统一审计所有「写请求」（POST/PUT/DELETE），
// 从 method+url 推导可读动作，用 currentUser 取当前用户（未登录记匿名）。
// 一处实现、覆盖所有端点（含未来新增），零侵入各路由。写入 fire-and-forget，
// 审计失败不影响业务响应。
//
// 刻意不审计 GET（读操作噪音大、无副作用）；登录/注册本身也是写，一并记录。

import type { FastifyInstance } from "fastify";
import { appendAudit, listAudit } from "../db/store.js";
import { currentUser } from "./auth.js";

/** method+url 前缀 → 可读动作。npm normalize：去掉参数段、统一动作词。 */
function labelFor(method: string, url: string): string {
  const u = url.split("?")[0];
  const m = method.toUpperCase();
  // 鉴权
  if (u.startsWith("/api/auth/register") && m === "POST") return "注册账号";
  if (u.startsWith("/api/auth/login") && m === "POST") return "登录";
  if (u.startsWith("/api/auth/logout") && m === "POST") return "登出";
  // 任务
  if (u.startsWith("/api/tasks") && m === "POST" && u.includes("/retry")) return "重试任务";
  if (u.startsWith("/api/tasks") && m === "POST" && u.includes("/archive")) return "归档/取消归档任务";
  if (u.startsWith("/api/tasks") && m === "POST" && u.includes("/template")) return "另存为模板";
  if (u.startsWith("/api/tasks") && m === "POST") return "创建任务";
  if (u.startsWith("/api/tasks") && m === "DELETE") return "删除任务";
  // 渠道
  if (u.startsWith("/api/channels") && m === "POST" && u.includes("/default")) return "设置默认渠道";
  if (u.startsWith("/api/channels") && m === "POST" && u.includes("/test")) return "验活渠道";
  if (u.startsWith("/api/channels") && m === "POST") return "创建渠道";
  if (u.startsWith("/api/channels") && m === "PUT") return "编辑渠道";
  if (u.startsWith("/api/channels") && m === "DELETE") return "删除渠道";
  // 空间
  if (u.startsWith("/api/spaces") && m === "POST" && u.includes("/active")) return "切换活动空间";
  if (u.startsWith("/api/spaces") && m === "POST") return "创建空间";
  if (u.startsWith("/api/spaces") && m === "PUT") return "编辑空间";
  if (u.startsWith("/api/spaces") && m === "DELETE") return "删除空间";
  // 对话会话
  if (u.startsWith("/api/chat/sessions") && m === "DELETE") return "删除对话会话";
  if (u.startsWith("/api/chat/sessions") && m === "POST") return "创建对话会话";
  // 聊天消息（发消息也是写）
  if (u.startsWith("/api/chat") && m === "POST") return "发送消息";
  // 技能/专家/自动化
  if (u.startsWith("/api/skills") && (m === "PUT" || m === "POST")) return "编辑技能";
  if (u.startsWith("/api/skills") && m === "DELETE") return "删除技能";
  if (u.startsWith("/api/experts") && m === "POST") return "创建专家";
  if (u.startsWith("/api/experts") && m === "PUT") return "编辑专家";
  if (u.startsWith("/api/experts") && m === "DELETE") return "删除专家";
  if (u.startsWith("/api/memories") && m === "POST") return "创建记忆";
  if (u.startsWith("/api/memories") && m === "PUT") return "编辑记忆";
  if (u.startsWith("/api/memories") && m === "DELETE") return "删除记忆";
  if (u.startsWith("/api/tools/web.read") && m === "POST") return "网页读取工具调用";
  if (u.startsWith("/api/tools/search.knowledge") && m === "POST") return "知识检索工具调用";
  if (u.startsWith("/api/tools/web.search") && m === "POST") return "联网搜索工具调用";
  if (u.startsWith("/api/tools/browser.render") && m === "POST") return "浏览器渲染工具调用";
	if (u.startsWith("/api/tools/save") && m === "POST") return "保存到资料库";
  if (u.startsWith("/api/tools/refine") && m === "POST") return "资料加工（总结/翻译）";
	if (u.startsWith("/api/tools/search/source/enabled") && m === "POST") return "联网搜索源开关";
  if (u.startsWith("/api/tools/search/source/provider") && m === "POST") return "配置联网搜索供应商";
  if (u.startsWith("/api/tools/search/source/quota") && m === "POST") return "调整联网搜索配额";
  if (u === "/api/im" && m === "POST") return "IM 入站消息";
  if (u.startsWith("/api/im") && m === "PUT") return "修改 IM 桥配置";
  if (u.startsWith("/api/jobs") && m === "POST" && u.includes("/run")) return "手动执行任务";
  if (u.startsWith("/api/jobs") && m === "POST") return "新建定时任务";
  if (u.startsWith("/api/jobs") && m === "PUT") return "编辑定时任务";
  if (u.startsWith("/api/jobs") && m === "DELETE") return "删除定时任务";
  if (u.startsWith("/api/templates") && m === "POST" && u.includes("/run")) return "应用任务模板";
  if (u.startsWith("/api/templates") && m === "POST") return "创建任务模板";
  if (u.startsWith("/api/templates") && m === "PUT") return "编辑任务模板";
  if (u.startsWith("/api/templates") && m === "DELETE") return "删除任务模板";
  // 管理端（M67）
  if (u.startsWith("/api/users") && m === "DELETE") return "删除用户";
  if (u.startsWith("/api/users") && u.endsWith("/disable")) return "禁用用户";
  if (u.startsWith("/api/users") && u.endsWith("/enable")) return "启用用户";
  if (u.startsWith("/api/maintenance") && m === "POST" && u.includes("/cleanup")) return "手动清理";
  return `${m} ${u}`;
}

/** 对单条 URL 触发的审计；返回 target 片段（去掉动作词后作为目标，如任务 id/渠道 id） */
function targetFor(url: string): string | undefined {
  const u = url.split("?")[0];
  // 取最后一个路径段的 id（若有）
  const segs = u.split("/").filter(Boolean);
  const last = segs[segs.length - 1];
  if (last && last !== "tasks" && last !== "channels" && last !== "spaces" &&
    last !== "sessions" && last !== "skills" && last !== "jobs" &&
    last !== "archive" && last !== "retry" && last !== "active" && last !== "default" && last !== "test") {
    return last;
  }
  return undefined;
}

const WRITE_METHODS = new Set(["POST", "PUT", "DELETE"]);

/** 全局写请求审计钩子。必须在最外层（根）app 上注册——
 *  Fastify 的 onResponse 是封装的，只在「注册它的上下文内」的路由生效，
 *  若放进带 prefix 的插件，只会审计该插件自己的路由，其余端点全漏。 */
function installAuditHook(app: FastifyInstance) {
  app.addHook("onResponse", async (req) => {
    const method = req.method.toUpperCase();
    if (method === "GET") return; // 只审写
    if (!WRITE_METHODS.has(method)) return;
    const url = (req.url ?? "").toString();
    if (!url.startsWith("/api/")) return; // 只审业务 API（排除 /health、静态资源）
    const me = currentUser(req);
    appendAudit({
      userId: me?.id,
      userName: me?.displayName ?? me?.username,
      action: labelFor(method, url),
      target: targetFor(url),
      detail: `${method} ${url}`,
    });
  });
}

export { installAuditHook };

export async function auditRoutes(app: FastifyInstance) {
  // 查看审计日志（最近 N 条，按时间倒序）
  app.get<{ Querystring: { limit?: string } }>("/", async (req) => {
    const limit = Number(req.query.limit) || 200;
    return listAudit(Math.min(Math.max(limit, 1), 1000));
  });
}

