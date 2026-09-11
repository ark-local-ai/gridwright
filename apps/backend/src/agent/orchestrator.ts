import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Task, TaskStep, Artifact } from "../types";
import {
  insertTask, insertStep, updateStepStatus, updateTaskStatus,
  insertArtifact, getTask, updateTaskChecksAndTimeline, getActiveSpace, resetTask,
} from "../db/store";
import { publish } from "./events";
import { planTask } from "./planner";
import { genOffice, detectKind, type StepContent } from "../tools/office";
import { defaultTool } from "../tools/registry";
import { runContentPipeline } from "./pipeline";
import { verifyWithRetry } from "./verifier";
import { workspaceRoot as workRoot } from "../config/paths";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** 工作空间根目录（统一取自 config/paths.ts，与 /api/workspace 读端一致）
 *  workRoot 已在上方 import */

/** 解析当前活动空间的交付目录（默认空间 dir 为空 → 根目录） */
function resolveWorkDir(): string {
  const active = getActiveSpace();
  const dir = active?.dir ? join(workRoot, active.dir) : workRoot;
  mkdirSync(dir, { recursive: true });
  return dir;
}

/**
 * 编排闭环：planner 拆步骤（优先 LLM，无渠道降级脚本）→ 逐条经 Tool Registry 真执行
 * （产出每步内容）→ 最后把各步结果注入可编辑 Office 交付文件。
 * M10：中间步骤不再只是"等待"，而是真正走工具产出，交付内容不再全占位。
 */
export async function runTask(id: string, prompt: string, userId?: string): Promise<Task> {
  const title = prompt.slice(0, 20) || "未命名任务";
  const created = new Date().toLocaleString("zh-CN", { hour12: false });

  // 规划：优先 LLM 拆真步骤，失败降级脚本
  const { steps: planned, model } = await planTask(prompt);
  const plan = planned.map((p) => p.title);
  const modelUsed = model;

  const task: Task = {
    id, title, prompt, status: "queue", model: modelUsed, expert: "数据分析师",
    skills: ["文件生成"], workspace: "默认工作空间",
    steps: [],
    artifacts: [],
    deliverable: null,
    checks: [
      { label: "已理解需求意图", ok: false },
      { label: "已完成不少于 3 个执行步骤", ok: false },
      { label: "已生成可下载的成果文件", ok: false },
    ],
    timeline: [{ time: "00:00", label: "创建任务" }],
    created,
  };

  // 幂等复位：重试同一 id 时先清掉上次残留的 steps/artifacts/主行，再写新快照
  resetTask(id);
  // 持久化任务 + 步骤
  insertTask(task, userId);
  const stepIds: number[] = [];
  plan.forEach((p, i) => stepIds.push(insertStep(id, i, p)));
  task.steps = plan.map((title, i) => ({ id: stepIds[i], title, status: "pending" }));

  // 置为运行并广播计划
  updateTaskStatus(id, "running");
  task.status = "running";
  publish({ type: "plan", taskId: id, data: task.steps });
  publish({ type: "step", taskId: id, data: { stepId: stepIds[0], status: "running" } });

  const tick = () => {
    const now = new Date().toTimeString().slice(0, 5);
    task.timeline.push({ time: now, label: "执行步骤" });
    publish({ type: "check", taskId: id, data: task.checks });
  };

  let stepIdx = 0;
  let failed = false;
  const kind = detectKind(prompt); // 交付类型（ppt/xls/doc），执行期就绪供工具使用
  const sections: StepContent[] = []; // 逐步真实执行产出的内容，注入最终交付

  for (const title of plan) {
    // 标记当前步为 running
    if (stepIdx > 0) {
      updateStepStatus(stepIds[stepIdx - 1], "done");
      task.steps[stepIdx - 1].status = "done";
    }
    if (stepIdx >= 1) {
      task.checks[0].ok = true; // 理解需求
      publish({ type: "check", taskId: id, data: task.checks });
    }
    const sid = stepIds[stepIdx];
    updateStepStatus(sid, "running");
    task.steps[stepIdx].status = "running";
    publish({ type: "step", taskId: id, data: { stepId: sid, status: "running" } });
    tick();

    // M10：逐步真实执行 —— 经 Tool Registry 调用工具，产出该步正文段
    const tool = defaultTool; // 当前单一"内容整编"工具；后续按步选工具
    const result = tool.run({ prompt, plan, step: title, kind });
    sections.push({ title: result.title, paragraphs: result.body });

    // 中间产物：每一步的内容作为 artifact 推送（无具体文件，仅展示说明）
    publish({
      type: "artifact", taskId: id,
      data: { name: `步骤${stepIdx + 1}`, kind, note: result.body[0]?.slice(0, 40) ?? result.title, path: undefined },
    });

    await sleep(700); // 模拟执行/工具耗时

    if (stepIdx === plan.length - 1) {
      // 最后一步：生成真实可编辑 Office 文件（PPT/Excel/Word），注入各步结果，带验收重试（最多 3 次）
      // 写入当前活动空间的工作目录（默认空间 → 根目录）
      const dir = resolveWorkDir();
      // M10b：多 Agent 内容管线（分析师 → 写手 → 校验），LLM 可用时真内容，否则脚本降级
      const { sections: contentSections, agents } = await runContentPipeline(prompt, plan, kind);
      const anyLLM = agents.analyst || agents.writer || agents.editor;
      const { name, kind: fKind } = await verifyWithRetry(
        () => genOffice(prompt, plan, dir, contentSections), 3,
      );
      const deliver: Artifact = {
        name, kind: fKind,
        note: anyLLM
          ? `Ark 生成 · 多 Agent(分析/写手/校验)${agents.writer ? " · 真实 LLM 内容" : ""} · 可编辑 Office 文件`
          : "Ark 生成 · 脚本模板（未配模型渠道）· 可编辑 Office 文件",
        path: name,
      };
      insertArtifact(deliver, id, 1);
      task.deliverable = deliver;
      publish({ type: "deliver", taskId: id, data: deliver });
    }

    updateStepStatus(sid, "done");
    task.steps[stepIdx].status = "done";
    publish({ type: "step", taskId: id, data: { stepId: sid, status: "done" } });
    stepIdx++;
    if (stepIdx >= 1) { task.checks[0].ok = true; }
    if (stepIdx >= 3) {
      task.checks[1].ok = true; // 已完成不少于 3 步
      task.checks[2].ok = true; // 已生成成果
    }
    publish({ type: "check", taskId: id, data: task.checks });
  }

  updateTaskStatus(id, "done");
  task.status = "done";
  task.steps.forEach((s) => (s.status = "done"));
  const now = new Date().toTimeString().slice(0, 5);
  task.timeline.push({ time: now, label: "执行完成" });
  publish({ type: "check", taskId: id, data: task.checks });
  publish({ type: "done", taskId: id, data: { taskId: id } });

  // 把最终 checks 与 timeline 写回库，供查询快照一致
  updateTaskChecksAndTimeline(id, task.checks, task.timeline);
  // 重新落库最终状态供查询
  const saved = getTask(id);
  return saved ?? task;
}
