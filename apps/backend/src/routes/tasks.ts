import { randomUUID } from "node:crypto";
import type { FastifyInstance } from "fastify";
import { enqueueTask } from "../agent/runner.js";
import { getTask, listTasks, deleteTask, insertTask } from "../db/store.js";
import { subscribe } from "../agent/events.js";
import { currentUser } from "./auth.js";

export async function taskRoutes(app: FastifyInstance) {
  // 创建任务并开始编排（后台异步推进，SSE 接收进度）
  app.post<{ Body: { prompt?: string } }>("/", async (req, reply) => {
    const prompt = req.body?.prompt?.trim();
    if (!prompt) {
      return reply.code(400).send({ error: "prompt 不能为空" });
    }
    const id = randomUUID().slice(0, 8);
    const userId = currentUser(req)?.id; // 归属当前登录用户；未登录 → 全局
    // 同步先落一条「排队中」任务行，避免前端立刻 GET /:id 撞 404 竞态
    // （TA-1：原实现 POST 入队后立即返回，任务行要等后台真正开跑才有，详情页首次加载必然 404）
    const created = new Date().toLocaleString("zh-CN", { hour12: false });
    insertTask(
      {
        id, title: prompt.slice(0, 20), prompt, status: "queue", model: "内置计划器",
        expert: "数据分析师", skills: ["文件生成"], workspace: "默认工作空间",
        checks: [], timeline: [{ time: "00:00", label: "创建任务" }], created,
        steps: [], artifacts: [], deliverable: null,
      } as never,
      userId,
    );
    // 立即返回任务 id；编排入队后台串行执行（队列 + 失败兜底 + 超时，见 runner）
    enqueueTask(id, prompt, userId);
    return reply.code(201).send({ taskId: id });
  });

  // 任务列表摘要（侧栏「最近任务」）；多用户隔离：登录用户只见自己的+全局
  app.get("/", async (req) => listTasks(50, currentUser(req)?.id));

  // 查询任务当前快照
  app.get<{ Params: { id: string } }>("/:id", async (req, reply) => {
    const task = getTask(req.params.id);
    if (!task) return reply.code(404).send({ error: "任务不存在" });
    return reply.send(task);
  });

  // 重试：把 failed / done 任务用原 prompt 重新入队再跑一次（M17）
  app.post<{ Params: { id: string } }>("/:id/retry", async (req, reply) => {
    const id = req.params.id;
    const task = getTask(id);
    if (!task) return reply.code(404).send({ error: "任务不存在" });
    if (task.status === "running" || task.status === "queue") {
      return reply.code(409).send({ error: "任务正在执行中，暂不能重试" });
    }
    enqueueTask(id, task.prompt, currentUser(req)?.id);
    return reply.send({ taskId: id, status: "queue" });
  });

  // 删除任务（含其步骤/产物，M17）
  app.delete<{ Params: { id: string } }>("/:id", async (req, reply) => {
    const ok = deleteTask(req.params.id);
    if (!ok) return reply.code(404).send({ error: "任务不存在" });
    return reply.send({ ok: true });
  });

  // SSE：订阅任务进度事件流
  app.get<{ Params: { id: string } }>("/:id/events", async (req, reply) => {
    const id = req.params.id;
    reply.raw.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    });
    const send = (event: { type: string; taskId: string; data: unknown }) => {
      reply.raw.write(`event: ${event.type}\ndata: ${JSON.stringify(event.data)}\n\n`);
    };
    const unsub = subscribe(id, (e) => send(e));
    // 心跳防断连
    const hb = setInterval(() => reply.raw.write(": ping\n\n"), 15000);
    req.raw.on("close", () => {
      clearInterval(hb);
      unsub();
    });
  });
}
