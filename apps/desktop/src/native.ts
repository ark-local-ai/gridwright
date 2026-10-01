// 桌面端原生能力封装（见 docs/agent-architecture/19-界面设计.md 阶段 2 / 20）。
// 在浏览器里预览时这些能力不可用，函数会返回 null，由调用方降级（例如让用户手输路径）。
//
// 为什么原生能力都收在这个文件里：这里是唯一**静态**依赖 @tauri-apps/api 的地方，
// 界面代码要能在普通浏览器里预览（dev 模式直接开 5174），不该被 Tauri 依赖绑死；
// 所以凡是碰 Tauri 的代码一律留在这里，由 App.tsx 注入给界面，
// 而不是界面自己去 import。
import { getCurrentWebview } from "@tauri-apps/api/webview";

/** 是否运行在 Tauri 壳里。 */
export function inTauri(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

/**
 * 弹系统目录选择框（可自定义标题）。工作区目录与日志目录都用它，只是标题不同。
 * 在浏览器里（非 Tauri）直接返回 null，调用方应降级。
 */
export async function pickDirectory(title: string): Promise<string | null> {
  if (!inTauri()) return null;
  try {
    const { open } = await import("@tauri-apps/plugin-dialog");
    const picked = await open({ directory: true, multiple: false, title });
    return typeof picked === "string" ? picked : null;
  } catch {
    return null;
  }
}

/** 弹系统文件夹选择框，返回所选目录绝对路径；取消或不可用时返回 null。 */
export async function pickFolder(): Promise<string | null> {
  return pickDirectory("选择工作区文件夹");
}

/**
 * 日志目录（桌面壳命令）。日志是**壳**在写（引擎只打 stdout），所以“日志放哪”
 * 由壳控制；这三个命令对应壳里的 get_log_dir / set_log_dir / open_log_dir。
 * 非 Tauri（浏览器/单文件版）下没有日志文件，读回 null、写抛错。
 */
export async function getLogDir(): Promise<string | null> {
  if (!inTauri()) return null;
  try {
    const { invoke } = await import("@tauri-apps/api/core");
    return await invoke<string>("get_log_dir");
  } catch {
    return null;
  }
}

/** 改日志目录并落盘（立即生效）；返回规范化后的路径。 */
export async function setLogDir(dir: string): Promise<string> {
  const { invoke } = await import("@tauri-apps/api/core");
  return await invoke<string>("set_log_dir", { dir });
}

/** 用资源管理器打开日志目录。 */
export async function openLogDir(): Promise<void> {
  if (!inTauri()) return;
  const { invoke } = await import("@tauri-apps/api/core");
  await invoke("open_log_dir");
}

/** 拖拽文件/文件夹进窗口时，Tauri 报给我们的三种时刻。 */
export type DropHandlers = {
  /** 有东西被拖到窗口上方（用于把空框点亮成"可以松手了"） */
  enter: () => void;
  /** 拖走了 / 放下了 */
  leave: () => void;
  /** 松手了，paths 是本机**绝对路径**（文件夹或文件都有） */
  drop: (paths: string[]) => void;
};

/**
 * 监听拖入窗口的文件/文件夹，返回取消监听的函数。
 *
 * 为什么必须是**原生**事件而不是 HTML5 的 onDrop：Tauri v2 的 webview 默认接管
 * 原生拖拽（dragDropEnabled 未关），此时网页层的 dragover/drop **根本不会触发**；
 * 而且浏览器的 DataTransfer 只给 File 对象、拿不到完整路径。只有这个原生事件
 * 同时给到"绝对路径"和"是文件还是目录"，后端才能 stat 出该复制文件还是切工作区。
 *
 * 注意这里是**静态** import：写成变量路径（import(mod)）会让 Vite 无法解析，
 * 产物里留一个 webview 解析不了的裸模块名，运行时静默失败——拖拽于是"毫无反应"。
 * （这正是之前拖文件夹没反应的真因：监听器压根没注册上。）
 */
export async function watchFileDrop(h: DropHandlers): Promise<() => void> {
  if (!inTauri()) return () => {};
  try {
    return await getCurrentWebview().onDragDropEvent((ev) => {
      const p = ev.payload;
      if (p.type === "enter") h.enter();
      else if (p.type === "leave") h.leave();
      else if (p.type === "drop") {
        h.leave();
        h.drop(p.paths ?? []);
      }
    });
  } catch {
    return () => {};
  }
}

