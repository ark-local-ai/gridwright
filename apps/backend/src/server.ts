import Fastify from "fastify";
import cors from "@fastify/cors";
import { createReadStream } from "node:fs";
import { dirname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import { taskRoutes } from "./routes/tasks.js";
import { channelRoutes } from "./routes/channels.js";
import { chatRoutes } from "./routes/chat.js";
import { skillRoutes } from "./routes/skills.js";
import { expertRoutes } from "./routes/experts.js";
import { jobRoutes } from "./routes/jobs.js";
import { connectorRoutes } from "./routes/connectors.js";
import { scenarioRoutes } from "./routes/scenarios.js";
import { spaceRoutes, resolveSpaceDir } from "./routes/spaces.js";
import { authRoutes } from "./routes/auth.js";
import { statsRoutes } from "./routes/stats.js";
import { chatSessionRoutes } from "./routes/chat-sessions.js";
import { globalEventsRoutes } from "./routes/events.js";
import { auditRoutes, installAuditHook } from "./routes/audit.js";
import { installAuthGate } from "./routes/guard.js";
import { queueRoutes } from "./routes/queue.js";
import { maintenanceRoutes } from "./routes/maintenance.js";
import { templateRoutes } from "./routes/templates.js";
import { versionRoutes } from "./routes/versions.js";
import { memoryRoutes } from "./routes/memories.js";
import { toolRoutes } from "./routes/tools.js";
import { imRoutes } from "./routes/im.js";
import { userRoutes } from "./routes/users.js";
import { scanWorkspace } from "./tools/workspace.js";
import { searchWorkspaceFiles, reindexWorkspace } from "./tools/searchIndex.js";
import { startScheduler } from "./scheduler/jobs.js";
import { pruneSessions } from "./db/store.js";
import { resumeUnfinishedTasks } from "./agent/runner.js";
import { loggerOptions } from "./util/log.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const workDir = join(__dirname, "..", "..", "frontend", "public", "workspace");

export async function buildApp() {
  // 用共享 logger 配置，让 HTTP 请求日志带 service 常驻字段。
  // Fastify 默认已为每个请求生成唯一 reqId（req.log 可用），无需自定义。
  const app = Fastify({ logger: loggerOptions });

  // 全局写请求审计钩子（必须在根上下文按装，否则只审到自己的插件路由）
  installAuditHook(app);
  // C3 认证硬门禁（写请求需登录；根上下文按装，ARK_REQUIRE_AUTH=0 可关）
  installAuthGate(app);

  app.register(cors, { origin: true });

  // API 路由
  app.register(taskRoutes, { prefix: "/api/tasks" });
  app.register(channelRoutes, { prefix: "/api/channels" });
  app.register(chatRoutes, { prefix: "/api/chat" });
  app.register(chatSessionRoutes, { prefix: "/api/chat/sessions" });
  app.register(globalEventsRoutes, { prefix: "/api/events" });
  app.register(skillRoutes, { prefix: "/api/skills" });
  app.register(expertRoutes, { prefix: "/api/experts" });
  app.register(jobRoutes, { prefix: "/api/jobs" });
  app.register(connectorRoutes, { prefix: "/api/connectors" });
  app.register(scenarioRoutes, { prefix: "/api/scenarios" });
  app.register(spaceRoutes, { prefix: "/api/spaces" });
  app.register(authRoutes, { prefix: "/api/auth" });
  app.register(statsRoutes, { prefix: "/api/stats" });
  app.register(auditRoutes, { prefix: "/api/audit" });
  app.register(queueRoutes, { prefix: "/api/queue" });
  app.register(maintenanceRoutes, { prefix: "/api/maintenance" });
  app.register(templateRoutes, { prefix: "/api/templates" });
  app.register(versionRoutes, { prefix: "/api/versions" });
  app.register(memoryRoutes, { prefix: "/api/memories" });
  app.register(toolRoutes, { prefix: "/api/tools" });
  app.register(imRoutes, { prefix: "/api/im" });
  app.register(userRoutes, { prefix: "/api/users" });

  // 工作空间列表：?space=<id> 指定空间目录，缺省扫根（默认工作空间）
  app.get("/api/workspace", async (req) => {
    const q = (req.query as { space?: string }).space;
    if (q) {
      const dir = resolveSpaceDir(q);
      if (!dir) return { error: "空间不存在" } as never;
      return scanWorkspace(dir);
    }
    return scanWorkspace(workDir);
  });

  // 工作空间全文搜索（M20）：/api/workspace/search?q=<子串>（须在 /* 通配前注册，否则被当静态文件命中）
  app.get("/api/workspace/search", async (req) => {
    const q = (req.query as { q?: string }).q ?? "";
    return searchWorkspaceFiles(q);
  });

  // 工作空间静态文件（成果下载）：?space=<id> 支持从空间子目录取
  app.get("/api/workspace/*", (req, reply) => {
    const name = (req.params as { "*": string })["*"];
    const q = (req.query as { space?: string }).space;
    const root = q && resolveSpaceDir(q) ? resolveSpaceDir(q)! : workDir;
    // 防目录穿越：只允许工作空间内的文件
    const file = normalize(join(root, name));
    if (!file.startsWith(root)) return reply.code(403).send({ error: "禁止访问" });
    return reply.type("application/octet-stream").send(createReadStream(file));
  });

  app.get("/health", async () => ({ ok: true }));

  return app;
}

// 直接运行时监听启动（tsx src/server.ts）
// 直接运行时监听启动（tsx src/server.ts）。测试导入 buildApp 时用 ARK_TEST=1 跳过监听。
// 直接运行时监听启动（tsx src/server.ts）。测试导入 buildApp 时用 ARK_TEST=1 跳过副作用（监听/调度/恢复）。
const port = Number(process.env.PORT ?? 4000);
if (!process.env.ARK_TEST) {
  pruneSessions(); // 启动时清理过期会话
  reindexWorkspace(); // M20：启动时重建工作空间文件名索引（原为导入副作用，现显式调用避免测试 DB 锁）
  startScheduler(); // 启动本地定时任务调度器
  resumeUnfinishedTasks(); // M24：把上次进程遗留的 queue/running 任务重新入队续跑
  await buildApp().then((app) => app.listen({ port, host: "127.0.0.1" }));
}
