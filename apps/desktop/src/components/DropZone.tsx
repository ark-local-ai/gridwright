import { useEffect, useRef, useState } from "react";
import { agentApi } from "../api-agent";
import { IconXls, IconFolder, IconNote } from "./icons";

/**
 * DropZone —— 首屏那一个空框：把表拖进来，或者点它选文件。
 *
 * 为什么是这个形态（用户的原话："一开始默认工作区应该是空的，
 * 先做空白显示，就是一个框，点击拖动文件或者新建工作区"）：
 *   一屏数据是给"已经放好表的人"看的；第一次打开的人什么都没有，
 *   这时候给他一屏 0 会显得像坏了。空框是**邀请**，不是报告。
 *
 * 两条路径都支持，因为两种外壳能力不同：
 *   · 桌面壳（Tauri）：拿到真实路径 → 走 import（服务端复制文件/切工作区）
 *   · 浏览器/单文件版：只有 File 对象，没有路径 → 提示用"选择文件夹"
 *     （浏览器不允许网页读任意本机路径，这是安全边界，不是偷懒）
 */

const EXTS = [".xlsx", ".xlsm", ".xls"];

function isExcel(name: string) {
  const n = name.toLowerCase();
  return EXTS.some((e) => n.endsWith(e));
}

/** 原生拖拽回调。结构与 desktop/src/native.ts 的 DropHandlers 一致。 */
type DropHandlers = {
  enter: () => void;
  leave: () => void;
  drop: (paths: string[]) => void;
};

export default function DropZone({ onDone, pickFolder, watchDrop }: {
  onDone: () => void;
  /** 弹系统文件夹选择框（桌面壳注入；浏览器里没有，退回服务端列目录） */
  pickFolder?: () => Promise<string | null>;
  /**
   * 监听原生拖拽（桌面壳注入）。
   *
   * 为什么要注入而不是组件自己 import：Tauri 的 API 只有桌面包装了，
   * 这个组件同时被前端包（浏览器/单文件版）复用。之前组件里用变量路径
   * 动态 import Tauri，Vite 解析不了，产物里留了个 webview 认不得的裸模块名，
   * 运行时静默失败——表现就是"拖文件夹进去毫无反应"。现在改由桌面壳注入，
   * 那里的 import 是静态的、能正常打包。
   */
  watchDrop?: (h: DropHandlers) => Promise<() => void>;
}) {
  const [over, setOver] = useState(false);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ name: string; ok: boolean; err?: string }[]>([]);
  const [err, setErr] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  // 浏览器兜底用的隐藏 input（桌面壳走原生拖拽，不用它）
  const fileRef = useRef<HTMLInputElement>(null);
  // 原生回调里要调最新的 importPaths，但订阅只该发生一次（onDone 每次渲染都是新函数，
  // 放进依赖会让监听反复重订阅）。所以用 ref 转一道：每次渲染后把 ref 指向最新的实现。
  const importRef = useRef<(paths: string[]) => void>(() => {});
  useEffect(() => {
    importRef.current = (paths) => void importPaths(paths);
  });

  // 桌面壳：订阅 Tauri 的原生拖拽事件。它带**真实路径**（浏览器 DataTransfer 没有），
  // 这是桌面版能"拖进来就直接用"的原因。回调未注入（浏览器）时什么都不做。
  useEffect(() => {
    if (!watchDrop) return;
    let un: (() => void) | undefined;
    let alive = true;
    void watchDrop({
      enter: () => setOver(true),
      leave: () => setOver(false),
      drop: (paths) => { setOver(false); importRef.current(paths); },
    }).then((u) => {
      // 订阅可能在等待期间就被卸载了，别留下悬空的监听
      if (alive) un = u;
      else u();
    });
    return () => { alive = false; un?.(); };
  }, [watchDrop]);

  const importPaths = async (paths: string[]) => {
    // 不在前端筛：拖进来的可能是文件夹（=打开为工作区）也可能是表（=复制进来），
    // 只有后端能 stat 出是哪种。前端先筛一道会把文件夹误杀成"只收 Excel 表"。
    if (paths.length === 0) return;
    setBusy(true);
    setErr("");
    try {
      const r = await agentApi.importFiles(paths);
      setResults(r.results ?? []);
      // 有一个成功就刷新（哪怕同批里有的失败了）
      if ((r.results ?? []).some((x) => x.ok)) onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  // 浏览器兜底：只能拿到文件名，没有路径 —— 明确告诉用户走"选文件夹"
  const onBrowserFiles = (files: FileList | null) => {
    if (!files || files.length === 0) return;
    const names = [...files].map((f) => f.name).filter(isExcel);
    if (names.length === 0) {
      setErr("只收 Excel 表（.xlsx / .xlsm / .xls）");
      return;
    }
    setErr("浏览器里读不到文件的完整路径，请用下面的「选择文件夹」指到表所在的目录。");
  };

  const pick = async () => {
    if (pickFolder) {
      const dir = await pickFolder();
      if (dir) {
        setBusy(true);
        try {
          await agentApi.openWorkspace(dir);
          onDone();
        } catch (e) {
          setErr(e instanceof Error ? e.message : String(e));
        } finally {
          setBusy(false);
        }
        return;
      }
    }
    fileRef.current?.click();
  };

  const createWs = async () => {
    if (!newName.trim()) return;
    setBusy(true);
    setErr("");
    try {
      await agentApi.createWorkspace(newName.trim());
      onDone();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const failed = results.filter((r) => !r.ok);

  return (
    <div className="dz-wrap">
      <button
        className={`dz${over ? " over" : ""}${busy ? " busy" : ""}`}
        onClick={() => void pick()}
        onDragOver={(e) => { e.preventDefault(); setOver(true); }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => { e.preventDefault(); setOver(false); onBrowserFiles(e.dataTransfer.files); }}
        disabled={busy}
      >
        <span className="dz-ic"><IconXls size={44} /></span>
        <b className="dz-title">{busy ? "正在读取…" : over ? "松开鼠标即可放入" : "把表格或文件夹拖到这里"}</b>
        <span className="dz-sub">
          拖表格 → 复制进当前工作区；拖文件夹 → 直接把它当作工作区
          <br />
          支持 .xlsx / .xlsm / .xls · 表留在你自己的机器上，不会被上传
        </span>
      </button>

      <input
        ref={fileRef}
        type="file"
        multiple
        accept=".xlsx,.xlsm,.xls"
        style={{ display: "none" }}
        onChange={(e) => { onBrowserFiles(e.target.files); e.target.value = ""; }}
      />

      <div className="dz-alt">
        <button className="btn ghost sm" onClick={() => void pick()} disabled={busy}>
          <IconFolder size={13} />选择文件夹
        </button>
        <button className="btn ghost sm" onClick={() => setCreating(true)} disabled={busy}>
          新建工作区
        </button>
        <span className="dz-alt-note">表格已在某个文件夹中？直接选择该文件夹即可</span>
      </div>

      {/* 新建：建一个空文件夹当工作区，再把表拖进去 */}
      {creating && (
        <div className="dz-new">
          <input
            autoFocus
            value={newName}
            placeholder="工作区名称，例如：御龙湾台账"
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void createWs();
              if (e.key === "Escape") setCreating(false);
            }}
          />
          <button className="btn primary sm" onClick={() => void createWs()} disabled={busy || !newName.trim()}>
            创建
          </button>
          <button className="btn ghost sm" onClick={() => setCreating(false)}>取消</button>
        </div>
      )}

      {err && <p className="dz-err">{err}</p>}

      {/* 逐个报结果：批量导入时"哪几个进来了、哪几个没有、为什么"要说清 */}
      {failed.length > 0 && (
        <ul className="dz-results">
          {failed.map((r) => (
            <li key={r.name}>
              <span className="dz-res-name">{r.name}</span>
              <span className="dz-res-err">{r.err}</span>
            </li>
          ))}
        </ul>
      )}

      <p className="dz-tip">
        <IconNote size={12} /> 放进来之后它会自己体检一遍，告诉你哪几处账对不上。
      </p>
    </div>
  );
}
