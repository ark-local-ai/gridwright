import { useEffect, useMemo, useState } from "react";
import { ftColor, ftLabel } from "../data/mock";
import {
  listSpaces, listSpaceFiles, createSpace, setActiveSpace, deleteSpace,
  workspaceUrl, type SpaceDto, type WorkspaceFileDto,
} from "../api";

export default function Workspace() {
  const [spaces, setSpaces] = useState<SpaceDto[]>([]);
  const [activeId, setActiveId] = useState<string>("default");
  const [files, setFiles] = useState<WorkspaceFileDto[] | null>(null);
  const [err, setErr] = useState(false);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");

  // 加载空间列表，默认选中活动空间
  const loadSpaces = () => {
    listSpaces()
      .then((ss) => {
        setSpaces(ss);
        const act = ss.find((s) => s.isActive) ?? ss[0];
        if (act) setActiveId(act.id);
      })
      .catch(() => setErr(true));
  };
  useEffect(loadSpaces, []);

  // 当前选中空间
  const active = useMemo(() => spaces.find((s) => s.id === activeId) ?? null, [spaces, activeId]);

  // 加载当前空间的文件
  const loadFiles = (id: string) => {
    setFiles(null);
    setErr(false);
    listSpaceFiles(id).then(setFiles).catch(() => { setFiles([]); setErr(true); });
  };
  useEffect(() => { loadFiles(activeId); }, [activeId]);

  const switchSpace = async (id: string) => {
    await setActiveSpace(id).catch(() => {});
    setActiveId(id);
    await listSpaces().then(setSpaces).catch(() => {});
  };

  const doCreate = async () => {
    const name = newName.trim();
    if (!name) return;
    try {
      const sp = await createSpace(name);
      await listSpaces().then(setSpaces).catch(() => {});
      setNewName("");
      setCreating(false);
      if (sp) await switchSpace(sp.id);
    } catch { /* 忽略 */ }
  };

  const doDelete = async () => {
    if (!active) return;
    try {
      await deleteSpace(active.id);
      await listSpaces().then((ss) => {
        setSpaces(ss);
        const def = ss.find((s) => s.id === "default") ?? ss[0];
        if (def) { setActiveId(def.id); setActiveSpace(def.id).catch(() => {}); }
      }).catch(() => {});
    } catch { /* 忽略 */ }
  };

  // 成果网格：官方交付文件（Office/pdf/md/html）按类型分组
  const grid = useMemo(() => (files ?? []).filter((f) => f.kind !== "other"), [files]);
  // 类型归组摘要：XLS / DOC / PPT / PDF 各多少
  const kindCounts = useMemo(() => {
    const m = new Map<string, number>();
    grid.forEach((f) => m.set(f.kind, (m.get(f.kind) ?? 0) + 1));
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [grid]);

  return (
    <div className="page">
      <div className="workspace-wrap">
        <div className="space-list">
          <b style={{ fontSize: 13.5 }}>工作空间</b>
          <div style={{ marginTop: 12 }}>
            {spaces.map((s) => (
              <div key={s.id} className={`space-item${s.id === activeId ? " on" : ""}`}
                onClick={() => switchSpace(s.id)}>
                <span>▸ {s.name}{s.isActive ? " ·进行中" : ""}</span>
                <span className="cnt">{s.id === activeId ? files?.length ?? 0 : s.files ?? 0}</span>
              </div>
            ))}
            {creating ? (
              <div className="space-new">
                <input autoFocus value={newName}
                  placeholder="空间名称"
                  onChange={(e) => setNewName(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") doCreate(); if (e.key === "Escape") setCreating(false); }} />
                <div className="space-new-ops">
                  <button className="btn soft sm" onClick={() => setCreating(false)}>取消</button>
                  <button className="btn sm" onClick={doCreate}>创建</button>
                </div>
              </div>
            ) : (
              <div className="space-item add" style={{ color: "var(--brand)" }} onClick={() => setCreating(true)}>
                ＋ 新建工作空间
              </div>
            )}
            {active && active.id !== "default" && (
              <div className="space-item" style={{ color: "var(--warn)", fontSize: 12 }} onClick={doDelete}>
                删除「{active.name}」
              </div>
            )}
          </div>
          <div className="tree" style={{ borderTop: "1px solid var(--line)", paddingTop: 12, marginTop: 12 }}>
            <div style={{ fontSize: 11.5, color: "var(--text-3)", marginBottom: 6 }}>
              文件树 · «{active?.name ?? ""}» 目录
            </div>
            {err && <div style={{ fontSize: 12, color: "var(--warn)" }}>后端未启动，无法扫描</div>}
            {!err && (files ?? []).length === 0 && (
              <div style={{ fontSize: 12, color: "var(--text-3)" }}>该空间暂无交付文件</div>
            )}
            {(files ?? []).map((f, i) => (
              <div key={i} className={`tree-row${f.kind === "other" ? "" : " dir"}`}>
                <span style={{ paddingLeft: 0 }}>{f.name}</span>
              </div>
            ))}
          </div>
        </div>

        <div>
          <div style={{ display: "flex", alignItems: "baseline", gap: 12, marginBottom: 14 }}>
            <b style={{ fontSize: 15 }}>成果文件 · {active?.name ?? "默认工作空间"}</b>
            <span style={{ fontSize: 12, color: "var(--text-3)" }}>真实落盘 · 可直接在 Office 打开继续编辑</span>
            <button className="btn ghost sm" style={{ marginLeft: "auto" }} onClick={() => loadFiles(activeId)}>刷新</button>
          </div>
          {kindCounts.length > 0 && (
            <div className="kind-summary">
              {kindCounts.map(([k, n]) => (
                <span className="kind-pill" key={k}>
                  <span className="kd" style={{ background: ftColor[k] }} />
                  {ftLabel(k)} <b>{n}</b>
                </span>
              ))}
            </div>
          )}
          {err ? (
            <div className="empty" style={{ color: "var(--warn)" }}>
              无法连接后端（127.0.0.1:4000），请先启动 apps/backend
            </div>
          ) : grid.length === 0 ? (
            <div className="empty">这个空间还没有成果文件，去首页提一个任务吧</div>
          ) : (
            <div className="files-grid">
              {grid.map((f, i) => (
                <div key={i} className="file-card card">
                  <div className="ftext" style={{ background: ftColor[f.kind] }}>{ftLabel(f.kind)}</div>
                  <div className="fname" title={f.name}>{f.name}</div>
                  <div className="fmeta">{f.sizeText} · {f.time}</div>
                  <div className="fops">
                    <a className="btn soft sm" href={workspaceUrl(f.name, activeId)} download>下载</a>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
