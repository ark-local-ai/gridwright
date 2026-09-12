import { useEffect, useState } from "react";
import "./settings.css";
import { agentApi } from "../api-agent";
import type { SettingsDto, WorkspaceListItem } from "../api-agent";
import { IconCheck, IconFolder, IconGear, IconX } from "../components/icons";

/* 设置（见 docs/agent-architecture/19-界面设计.md、20-离线可用与桌面交付.md）
   两件事：配"脑"（模型） + 管工作区。离线时明确告诉用户哪些能用、哪些要联网。 */

export default function Settings({ onClose, onWorkspaceChanged, pickFolder }: {
  onClose: () => void;
  onWorkspaceChanged?: () => void;
  /** 选文件夹（桌面端注入原生选择框；网页端不传则退化为主输路径） */
  pickFolder?: () => Promise<string | null>;
}) {
  const [s, setS] = useState<SettingsDto | null>(null);
  const [spaces, setSpaces] = useState<WorkspaceListItem[]>([]);
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState("");
  const [newName, setNewName] = useState("");
  const [manualPath, setManualPath] = useState("");
  const [busySpace, setBusySpace] = useState(false);

  const load = async () => {
    const [st, ws] = await Promise.all([
      agentApi.settings().catch(() => null),
      agentApi.workspaces().catch(() => ({ current: "", items: [] })),
    ]);
    if (st) {
      setS(st);
      setBaseUrl(st.baseUrl);
      setModel(st.model);
    }
    setSpaces(ws.items);
  };
  useEffect(() => { void load(); }, []);

  const save = async () => {
    setSaving(true);
    setMsg("");
    try {
      await agentApi.saveSettings({ baseUrl, model, apiKey: apiKey || undefined });
      setApiKey("");
      setMsg("已保存");
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const createWorkspace = async () => {
    if (!newName.trim()) return;
    setBusySpace(true);
    try {
      await agentApi.createWorkspace(newName.trim());
      setNewName("");
      await load();
      onWorkspaceChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusySpace(false);
    }
  };

  const switchTo = async (dir: string) => {
    setBusySpace(true);
    try {
      await agentApi.openWorkspace(dir);
      await load();
      onWorkspaceChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusySpace(false);
    }
  };

  // 打开已有文件夹当工作区：桌面端用系统选择框；网页端退化为主输路径
  const openFolder = async () => {
    if (pickFolder) {
      const dir = await pickFolder();
      if (dir) await switchTo(dir);
      return;
    }
    setMsg("网页预览版没有系统选择框；请在下面输入文件夹路径后点「打开」。");
  };

  const openManual = async () => {
    if (!manualPath.trim()) return;
    await switchTo(manualPath.trim());
    setManualPath("");
  };

  return (
    <div className="set-overlay" onClick={onClose}>
      <div className="set-panel" onClick={(e) => e.stopPropagation()}>
        <header className="set-head">
          <IconGear size={15} />
          <b>设置</b>
          <button className="set-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="set-body">
          {/* 脑（模型） */}
          <section className="set-sec">
            <h3>模型（脑）</h3>
            <p className="set-hint">
              配好之后，它才能判断该怎么改表、跟你对话。
              <b>看表、体检、联动图、账目不需要模型，断网也能用。</b>
            </p>

            <label className="set-field">
              <span>接口地址</span>
              <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)}
                placeholder="https://api.deepseek.com/v1" />
            </label>
            <label className="set-field">
              <span>模型名</span>
              <input value={model} onChange={(e) => setModel(e.target.value)}
                placeholder="deepseek-chat" />
            </label>
            <label className="set-field">
              <span>API 密钥</span>
              <input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)}
                placeholder={s?.hasApiKey ? "已配置（留空则不修改）" : "sk-…"} />
            </label>

            <div className="set-row">
              <button className="btn primary" onClick={() => void save()} disabled={saving}>
                {saving ? "保存中…" : "保存"}
              </button>
              <span className={`set-state ${s?.brainReady ? "ok" : "off"}`}>
                {s?.brainReady ? <><IconCheck size={13} />模型已就绪</> : "未配置 · 当前只能用只读功能"}
              </span>
              {msg && <span className="set-msg">{msg}</span>}
            </div>
          </section>

          {/* 工作区 */}
          <section className="set-sec">
            <h3>工作区</h3>
            <p className="set-hint">工作区就是一个文件夹，表就放在里面。切换后看板会按新文件夹重算。</p>

            <ul className="set-spaces">
              {spaces.map((w) => (
                <li key={w.path} className={`set-space${w.current ? " on" : ""}`}>
                  <span className="ss-name">
                    <IconFolder size={13} />
                    {w.name}
                    {w.current && <em>当前</em>}
                  </span>
                  <span className="ss-meta">{w.tables} 个表 · {w.opened}</span>
                  <span className="ss-path" title={w.path}>{w.path}</span>
                  {!w.current && (
                    <button className="btn ghost sm" disabled={busySpace}
                      onClick={() => void switchTo(w.path)}>切换</button>
                  )}
                </li>
              ))}
              {spaces.length === 0 && <li className="set-empty">还没有登记的工作区</li>}
            </ul>

            <div className="set-row">
              <input className="set-newspace" value={newName} placeholder="新建工作区名称"
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") void createWorkspace(); }} />
              <button className="btn ghost" onClick={() => void createWorkspace()}
                disabled={busySpace || !newName.trim()}>新建</button>
            </div>

            <div className="set-row">
              <button className="btn ghost" onClick={() => void openFolder()} disabled={busySpace}>
                <IconFolder size={13} />打开已有文件夹
              </button>
              <input className="set-newspace" value={manualPath} placeholder="或输入文件夹路径"
                onChange={(e) => setManualPath(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") void openManual(); }} />
              <button className="btn ghost sm" onClick={() => void openManual()}
                disabled={busySpace || !manualPath.trim()}>打开</button>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
