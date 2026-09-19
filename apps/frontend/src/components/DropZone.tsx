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
 *   · 桌面壳（Tauri）：拿到真实路径 → 走 import（服务端复制文件）
 *   · 浏览器/单文件版：只有 File 对象，没有路径 → 提示用"选择文件夹"
 *     （浏览器不允许网页读任意本机路径，这是安全边界，不是偷懒）
 */

const EXTS = [".xlsx", ".xlsm", ".xls"];

function isExcel(name: string) {
  const n = name.toLowerCase();
  return EXTS.some((e) => n.endsWith(e));
}

export default function DropZone({ onDone, pickFolder }: {
  onDone: () => void;
  pickFolder?: () => Promise<string | null>;
}) {
  const [over, setOver] = useState(false);
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ name: string; ok: boolean; err?: string }[]>([]);
  const [err, setErr] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  // 浏览器兜底用的隐藏 input（桌面壳走 Tauri 拖拽事件，不用它）
  const fileRef = useRef<HTMLInputElement>(null);

  // 桌面壳：Tauri 的原生拖拽事件带**真实路径**，浏览器的 DataTransfer 没有。
  // 这是桌面版能"拖进来就直接用"的原因。
  useEffect(() => {
    const w = window as unknown as { __TAURI_INTERNALS__?: unknown };
    if (!w.__TAURI_INTERNALS__) return;
    let un: (() => void) | undefined;
    void (async () => {
      try {
        // 动态 import + 变量路径：这个组件同时被前端包（没有 @tauri-apps/api
        // 依赖）和桌面壳使用。写成静态 import 会让前端构建直接失败，
        // 所以用运行时才解析的写法，浏览器里走 catch 分支即可。
        const mod = "@tauri-apps/api/webview";
        const webview = (await import(/* @vite-ignore */ mod)) as {
          getCurrentWebview: () => {
            onDragDropEvent: (cb: (ev: { payload: { type: string; paths?: string[] } }) => void) => Promise<() => void>;
          };
        };
        un = await webview.getCurrentWebview().onDragDropEvent((ev) => {
          const p = ev.payload;
          if (p.type === "enter") setOver(true);
          else if (p.type === "leave") setOver(false);
          else if (p.type === "drop" && p.paths) {
            setOver(false);
            void importPaths(p.paths);
          }
        });
      } catch {
        /* 不是 Tauri 宿主（浏览器/免安装版），忽略：拖拽走浏览器兜底 */
      }
    })();
    return () => un?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
        <span className="dz-ic"><IconXls size={30} /></span>
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
