// 桌面端原生能力封装（见 docs/agent-architecture/19-界面设计.md 阶段 2 / 20）。
// 在浏览器里预览时这些能力不可用，函数会返回 null，由调用方降级（例如让用户手输路径）。

/** 是否运行在 Tauri 壳里。 */
export function inTauri(): boolean {
  return typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;
}

/**
 * 弹系统文件夹选择框，返回所选目录绝对路径；取消或不可用时返回 null。
 * 在浏览器里（非 Tauri）直接返回 null，调用方应降级为手输。
 */
export async function pickFolder(): Promise<string | null> {
  if (!inTauri()) return null;
  try {
    const { open } = await import("@tauri-apps/plugin-dialog");
    const picked = await open({ directory: true, multiple: false, title: "选择工作区文件夹" });
    if (typeof picked === "string") return picked;
    return null;
  } catch {
    return null;
  }
}
