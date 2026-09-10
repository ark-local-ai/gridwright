import { useEffect, useMemo, useState } from "react";
import { NavLink, useNavigate } from "react-router-dom";
import {
  IconAssistant, IconChevD, IconChevD2, IconChevU2, IconClock, IconDoc,
  IconFolder, IconFolderOpen, IconGear, IconLibrary, IconLink, IconNote, IconSearch,
  IconShare, IconRename, IconDots, IconSpark, IconTrash, IconUsers, IconChart,
  ArkLogo, IconCollapse, IconArchive, IconFiles, IconShield,
} from "../components/icons";
import { recentTasks as mockRecentTasks } from "../data/mock";
import { listTasks, deleteTask, retryTask, setTaskArchived, listSpaces, createSpace, setActiveSpace, deleteSpace, searchWorkspace, searchChatMessages, searchMemories, subscribeGlobal, getAuthStatus, type SpaceDto, type AuthUser, type WorkspaceSearchResult, type ChatSearchHit, type MemoryDto } from "../api";
import AuthModal from "../components/AuthModal";

type Mode = "task" | "space";

const NAV = [
  { to: "/app", label: "新建任务", icon: <IconNote size={15} />, end: true, sub: null as { label: string; to: string; icon: React.ReactNode }[] | null },
  { to: "/app/chat", label: "助理", icon: <IconAssistant size={15} />, sub: null },
  {
    to: "/app/experts", label: "专家 · 技能 · 连接器", icon: <IconUsers size={15} />,
    sub: [
      { label: "专家", to: "/app/experts", icon: <IconUsers size={13} /> },
      { label: "技能", to: "/app/skills", icon: <IconSpark size={13} /> },
      { label: "连接器", to: "/app/connectors", icon: <IconLink size={13} /> },
    ],
  },
  { to: "/app/automation", label: "自动化", icon: <IconClock size={15} />, sub: null },
  { to: "/app/templates", label: "任务模板", icon: <IconFiles size={15} />, sub: null },
  { to: "/app/workspace", label: "资料库", icon: <IconLibrary size={15} />, sub: null },
  { to: "/app/stats", label: "统计", icon: <IconChart size={15} />, sub: null },
  { to: "/app/admin", label: "管理", icon: <IconShield size={15} />, sub: null },
  { to: "/app/memories", label: "记忆", icon: <IconSpark size={15} />, sub: null },
  { to: "/app/im", label: "IM 消息桥", icon: <IconLink size={15} />, sub: null },
];

export default function Sidebar({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  const nav = useNavigate();
  const [mode, setMode] = useState<Mode>("task");
  const [findOpen, setFindOpen] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [q, setQ] = useState("");
  const [fileHits, setFileHits] = useState<WorkspaceSearchResult[]>([]);
  const [chatHits, setChatHits] = useState<ChatSearchHit[]>([]);
  // M56：Cmd+K 同时搜记忆（FTS）——搜索弹板打开且有输入时拉取
  const [memHits, setMemHits] = useState<MemoryDto[]>([]);
  const [fold, setFold] = useState<Record<string, boolean>>({});
  const [delTarget, setDelTarget] = useState<null | { kind: "task" | "space"; id: string }>(null);
  const [ctxMenu, setCtxMenu] = useState<null | { kind: "task" | "space"; id: string; x: number; y: number; title: string; archived?: boolean }>(null);

  const [tasks, setTasks] = useState(mockRecentTasks);
  const [showArchived, setShowArchived] = useState(false);
  const refreshTasks = () => {
    listTasks(showArchived)
      .then((ts) => setTasks(ts.map((t) => ({ id: t.id, title: t.title, time: t.created, space: null as string | null, status: t.status, archived: t.archived }))))
      .catch(() => {});
  };
  useEffect(refreshTasks, [showArchived]);
  // M21：订阅全局任务事件，任务完成/失败时自动刷新列表（修复“创建/跑完任务后列表不更新”的 stale 问题）
  useEffect(() => {
    return subscribeGlobal((ev) => {
      if (ev.type === "done" || ev.type === "error") refreshTasks();
    });
  }, [showArchived]);
  // 真实空间列表（来自后端 /api/spaces），排除默认空间（放进「任务」抽屉）
  const [spaceList, setSpaceList] = useState<SpaceDto[]>([]);
  useEffect(() => {
    listSpaces()
      .then((ss) => setSpaceList(ss.filter((s) => s.id !== "default")))
      .catch(() => {});
  }, []);
  // 新建空间（从侧栏 + 按钮触发）
  const [newing, setNewing] = useState(false);
  const [newSpaceName, setNewSpaceName] = useState("");
  const doCreateSpace = async () => {
    const name = newSpaceName.trim();
    if (!name) return;
    try {
      const sp = await createSpace(name);
      await setActiveSpace(sp.id).catch(() => {});
      await listSpaces().then((ss) => setSpaceList(ss.filter((s) => s.id !== "default"))).catch(() => {});
      setNewSpaceName("");
      setNewing(false);
      nav("/app/workspace");
    } catch { /* 忽略 */ }
  };

  // 本地多用户认证状态
  const [me, setMe] = useState<AuthUser | null>(null);
  const [hasUsers, setHasUsers] = useState(false);
  const [authOpen, setAuthOpen] = useState(false);
  useEffect(() => {
    getAuthStatus().then((s) => { setMe(s.me); setHasUsers(s.hasUsers); }).catch(() => {});
  }, []);

  const kw = q.trim().toLowerCase();
  const matched = useMemo(
    () => tasks.filter((t) => t.title.toLowerCase().includes(kw)),
    [kw],
  );

  // M20：Cmd+K 同时搜工作空间文件（FTS）——搜索弹板打开且有输入时拉取
  useEffect(() => {
    if (!findOpen || !kw) { setFileHits([]); return; }
    let stale = false;
    searchWorkspace(q.trim()).then((h) => { if (!stale) setFileHits(h); }).catch(() => { if (!stale) setFileHits([]); });
    return () => { stale = true; };
  }, [findOpen, kw, q]);

  // M23：Cmd+K 同时搜历史对话（FTS5 trigram）——按消息内容命中会话
  useEffect(() => {
    if (!findOpen || !kw) { setChatHits([]); return; }
    let stale = false;
    searchChatMessages(q.trim()).then((h) => { if (!stale) setChatHits(h); }).catch(() => { if (!stale) setChatHits([]); });
    return () => { stale = true; };
  }, [findOpen, kw, q]);

  // M56：Cmd+K 同时搜记忆（FTS5 trigram）——搜索弹板打开且有输入时拉取，点击跳记忆页
  useEffect(() => {
    if (!findOpen || !kw) { setMemHits([]); return; }
    let stale = false;
    searchMemories(q.trim()).then((h) => { if (!stale) setMemHits(h); }).catch(() => { if (!stale) setMemHits([]); });
    return () => { stale = true; };
  }, [findOpen, kw, q]);

  const openTask = () => { setFindOpen(false); setMenuOpen(false); nav("/app/task"); };
  const openChatSession = (sessionId: string) => { setFindOpen(false); setMenuOpen(false); nav(`/app/chat?sess=${sessionId}`); };
  const openMemories = () => { setFindOpen(false); setMenuOpen(false); nav("/app/memories"); };

  const taskList = tasks.filter((t) => !t.space || t.space === "默认工作空间");
  const spaceGroups = spaceList.map((s) => ({
    ...s,
    items: tasks.filter((t) => t.space === s.name),
  }));
  const toggleFold = (k: string) => setFold((f) => ({ ...f, [k]: !f[k] }));
  const setAllFold = (v: boolean) => setFold(() => {
    const all: Record<string, boolean> = {};
    spaceGroups.forEach((s) => { all[`sp-${s.id}`] = v; });
    all["tasklist"] = v;
    return all;
  });
  const allFolded = (() => {
    const keys = [...spaceGroups.map((s) => `sp-${s.id}`), "tasklist"];
    return keys.every((k) => fold[k]);
  })();

  const confirmDel = () => {
    if (!delTarget) return;
    if (delTarget.kind === "task") {
      // 删除真实任务（调后端，含步骤/产物）
      deleteTask(delTarget.id).catch(() => {});
      setTasks((ts) => ts.filter((t) => t.id !== delTarget.id));
    } else {
      // 删除真实空间（调后端，而非仅本地过滤）
      deleteSpace(delTarget.id).catch(() => {});
      setSpaceList((ls) => ls.filter((s) => s.id !== delTarget.id));
      setTasks((ts) => ts.filter((t) => t.space !== delTarget.id));
    }
    setDelTarget(null);
  };

  // 重试失败/已完成任务（M17）：重新入队后用原 id 重新拉一次列表
  const doRetryTask = async (id: string) => {
    setCtxMenu(null);
    try {
      await retryTask(id);
      nav("/app/task");
    } catch { /* 忽略 */ }
  };

  const openCtx = (kind: "task" | "space", id: string, title: string, e: React.MouseEvent) => {
    const btn = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const t = tasks.find((x) => x.id === id);
    setCtxMenu({ kind, id, title, archived: t?.archived, x: btn.right - 4, y: btn.bottom + 4 });
  };
  const closeCtx = () => setCtxMenu(null);

  // 归档 / 取消归档任务（M22）：调后端后刷新列表与上下文菜单
  const doArchiveTask = async (id: string, archived: boolean) => {
    setCtxMenu(null);
    try {
      await setTaskArchived(id, archived);
      refreshTasks();
    } catch { /* 忽略 */ }
  };

  return (
    <aside className={`sb${collapsed ? " collapsed" : ""}`}>
      {!collapsed && (
        <>
          {/* 侧栏头部：左上 ARK·方舟 品牌 + 右侧收起按钮 */}
          <div className="sb-head">
            <div className="brandl">
              <div className="brandrow">
                <ArkLogo h={15} />
                <span className="brand-name">方舟</span>
              </div>
              <span className="brand-ver">管理 · v0.1.0</span>
            </div>
            <button
              className="sb-collapse"
              aria-label="收起侧栏"
              onClick={onToggle}
            >
              <IconCollapse size={15} />
            </button>
          </div>

          {/* 导航队列 */}
          <nav className="sb-nav">
            {NAV.map((n) => (
              <div className="sb-item-group" key={n.to}>
                <NavLink to={n.to} end={n.end}
                  className={({ isActive }) => `sb-item${isActive ? " on" : ""}`}>
                  <span className="icw">{n.icon}</span>
                  <span>{n.label}</span>
                </NavLink>
                {n.sub && (
                  <div className="sb-submenu">
                    {n.sub.map((s) => (
                      <button key={s.label} className="sb-submenu-item"
                        onClick={() => { nav(s.to); }}>
                        {s.icon}{s.label}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </nav>

          {/* 对话 / 空间 切换 + 搜索（搜索固定在侧栏右边缘） */}
          <div className="sb-switch-wrap">
            <div className="sb-switch">
              <div className={`seg${mode === "space" ? " space" : ""}`}>
                <button className={`seg-btn${mode === "task" ? " on" : ""}`} onClick={() => setMode("task")}>
                  <IconDoc size={10} /> 对话
                </button>
                <button className={`seg-btn${mode === "space" ? " on" : ""}`} onClick={() => setMode("space")}>
                  <IconFolder size={10} /> 空间
                </button>
              </div>
            </div>
            {mode === "space" && (
              <button
                className="sb-expall"
                aria-label={allFolded ? "展开全部" : "收起全部"}
                title={allFolded ? "展开全部" : "收起全部"}
                onClick={() => setAllFold(!allFolded)}
              >
                {allFolded ? <IconChevD2 size={14} /> : <IconChevU2 size={14} />}
              </button>
            )}
            <button className={`sb-find${findOpen ? " on" : ""}`} aria-label="搜索"
              onClick={() => { setFindOpen((v) => !v); setQ(""); }}>
              <IconSearch size={14} />
            </button>
          </div>

          {/* 面板 */}
          <div className="sb-panel">
            {mode === "task" ? (
              /* 对话：全量视图，所有对话（不论空间）直接平铺 */
              <>
                <button className="sb-archive-toggle" onClick={() => setShowArchived((v) => !v)}>
                  <IconArchive size={13} />
                  <span>{showArchived ? "归档任务" : "查看归档"}</span>
                  <span className="arch-badge">{showArchived ? "×" : ""}</span>
                </button>
                <div className="sb-grpwrap">
                  {tasks.map((t) => (
                    <Row key={t.id} title={t.title} time={t.time} onClick={openTask}
                      onMenu={(e) => openCtx("task", t.id, t.title, e)} />
                  ))}
                  {tasks.length === 0 && <div className="sb-empty">{showArchived ? "暂无归档任务" : "暂无对话"}</div>}
                </div>
              </>
            ) : (
              /* 空间：按空间分类，未命名/默认对话放进「任务」抽屉 */
              <>
                {spaceGroups.map((s) => {
                  const items = s.items;
                  const key = `sp-${s.id}`;
                  return (
                    <div className="sb-grpwrap" key={s.id}>
                      <button className="sb-grp" onClick={() => toggleFold(key)}>
                        <IconChevD size={12} className={fold[key] ? "fold" : ""} />
                        <IconFolder size={12} />
                        <span className="lb">{s.name}</span>
                        <span className="grp-add" onClick={(e) => { e.stopPropagation(); setMode("task"); nav("/app"); }}>
                          <IconNote size={13} />
                        </span>
                        <span className="grp-del" onClick={(e) => { e.stopPropagation(); openCtx("space", s.id, s.name, e); }}>
                          <IconDots size={13} />
                        </span>
                      </button>
                      {!fold[key] && items.map((t) => (
                        <Row key={t.id} title={t.title} time={t.time} onClick={openTask}
                          onMenu={(e) => openCtx("task", t.id, t.title, e)} />
                      ))}
                    </div>
                  );
                })}

                {/* 新建工作空间（内联输入） */}
                <div className="sb-grpwrap">
                  {newing ? (
                    <div className="sb-newspace">
                      <input autoFocus value={newSpaceName} placeholder="空间名称"
                        onChange={(e) => setNewSpaceName(e.target.value)}
                        onKeyDown={(e) => { if (e.key === "Enter") doCreateSpace(); if (e.key === "Escape") setNewing(false); }} />
                      <div className="sb-newspace-ops">
                        <button className="btn ghost sm" onClick={() => setNewing(false)}>取消</button>
                        <button className="btn sm" onClick={doCreateSpace}>创建</button>
                      </div>
                    </div>
                  ) : (
                    <button className="sb-grp add-grp" onClick={() => setNewing(true)}>
                      <IconNote size={12} />
                      <span className="lb" style={{ color: "var(--brand)" }}>＋ 新建工作空间</span>
                    </button>
                  )}
                </div>

                {/* 任务抽屉：未指定命名空间/默认新建的对话 */}
                <div className="sb-grpwrap">
                  <button className="sb-grp" onClick={() => toggleFold("tasklist")}>
                    <IconChevD size={12} className={fold.tasklist ? "fold" : ""} />
                    <IconDoc size={12} />
                    <span className="lb">任务</span>
                  </button>
                  {!fold.tasklist && taskList.map((t) => (
                    <Row key={t.id} title={t.title} time={t.time} onClick={openTask}
                      onMenu={(e) => openCtx("task", t.id, t.title, e)} />
                  ))}
                  {taskList.length === 0 && <div className="sb-empty">暂无对话</div>}
                </div>
              </>
            )}
          </div>

          {/* 用户区 + 头像菜单 */}
          <div className="sb-user-wrap">
            {menuOpen && (
              <>
                <div className="sb-overlay" onClick={() => setMenuOpen(false)} />
                <div className="sb-menu">
                  <button className="sb-menu-item" onClick={() => { setMenuOpen(false); nav("/app/settings"); }}>
                    <IconGear size={14} /> 设置
                  </button>
                  <div className="sb-menu-meta">Ark v0.1.0 · 本地运行 · 数据不出本机</div>
                </div>
              </>
            )}
            <button className="sb-user" onClick={() => (me ? setMenuOpen((v) => !v) : setAuthOpen(true))}>
              <span className="ava">{me ? me.displayName.slice(0, 1).toUpperCase() : "客"}</span>
              <span className="sb-user-t">
                <b>{me ? me.displayName : (hasUsers ? "登录" : "创建账号")}</b>
                <span>{me ? `@${me.username}` : "本地工作区 · 数据不出本机"}</span>
              </span>
            </button>
          </div>
        </>
      )}

      <AuthModal
        open={authOpen}
        me={me}
        hasUsers={hasUsers}
        onClose={() => setAuthOpen(false)}
        onChanged={(u) => setMe(u)}
      />

      {/* 居中搜索弹板 */}
      {findOpen && (
        <>
          <div className="sb-overlay" style={{ zIndex: 75 }} onClick={() => setFindOpen(false)} />
          <div className="sb-search-pop">
            <div className="sb-search-input">
              <IconSearch size={16} />
              <input autoFocus placeholder="搜索任务或文件…" value={q}
                onChange={(e) => setQ(e.target.value)}
                onKeyDown={(e) => e.key === "Escape" && setFindOpen(false)} />
            </div>
            <div className="sb-pop-sub">最近任务</div>
            <div className="sb-pop-list">
              {matched.map((t) => <Row key={t.id} title={t.title} time={t.time} onClick={openTask} />)}
              {matched.length === 0 && <div className="sb-pop-empty">没有找到相关任务</div>}
            </div>
            {kw && (
              <>
                <div className="sb-pop-sub">工作空间文件</div>
                <div className="sb-pop-list">
                  {fileHits.map((f) => (
                    <Row key={`${f.spaceName}/${f.name}`} title={f.name} time={`${f.spaceName} · ${f.sizeText}`}
                      onClick={() => { setFindOpen(false); setMenuOpen(false); nav("/app/workspace"); }} />
                  ))}
                  {fileHits.length === 0 && <div className="sb-pop-empty">没有找到匹配文件</div>}
                </div>
                <div className="sb-pop-sub">历史对话</div>
                <div className="sb-pop-list">
                  {chatHits.map((c) => (
                    <Row key={c.sessionId} title={`${c.title} · ${c.role === "assistant" ? "助理" : "我"}`} time={c.snippet}
                      onClick={() => openChatSession(c.sessionId)} />
                  ))}
                  {chatHits.length === 0 && <div className="sb-pop-empty">没有找到匹配对话</div>}
                </div>
                <div className="sb-pop-sub">记忆</div>
                <div className="sb-pop-list">
                  {memHits.map((m) => (
                    <Row key={m.id} title={`${m.kind !== "note" ? `[${m.kind}] ` : ""}${m.content}`} time={m.created}
                      onClick={() => openMemories()} />
                  ))}
                  {memHits.length === 0 && <div className="sb-pop-empty">没有找到匹配记忆</div>}
                </div>
              </>
            )}
          </div>
        </>
      )}

      {/* 行/分组 更多操作菜单 */}
      {ctxMenu && (
        <>
          <div className="sb-overlay" style={{ zIndex: 88 }} onClick={closeCtx} />
          <div className="sb-ctx" style={{ left: ctxMenu.x, top: ctxMenu.y }}>
            {ctxMenu.kind === "space" ? (
              <>
                <button className="sb-ctx-item" onClick={closeCtx}><IconFolderOpen size={14} /> 打开文件夹</button>
                <button
                  className="sb-ctx-item danger"
                  onClick={() => { setCtxMenu(null); setDelTarget({ kind: "space", id: ctxMenu.id }); }}
                >
                  <IconTrash size={14} /> 从列表中删除
                </button>
              </>
            ) : (
              <>
                {(() => {
                  const t = tasks.find((x) => x.id === ctxMenu.id);
                  const canRetry = t && (t.status === "failed" || t.status === "done");
                  return (
                    <>
                      <button className="sb-ctx-item" onClick={closeCtx}><IconFolderOpen size={14} /> 打开文件夹</button>
                      <button className="sb-ctx-item" onClick={closeCtx}><IconRename size={14} /> 重命名</button>
                      <button className="sb-ctx-item" onClick={closeCtx}><IconShare size={14} /> 分享任务</button>
                      {canRetry && (
                        <button className="sb-ctx-item" onClick={() => doRetryTask(ctxMenu.id)}>
                          <IconClock size={14} /> 重试
                        </button>
                      )}
                      <button className="sb-ctx-item" onClick={() => doArchiveTask(ctxMenu.id, !ctxMenu.archived)}>
                        <IconArchive size={14} /> {ctxMenu.archived ? "取消归档" : "归档"}
                      </button>
                      <button
                        className="sb-ctx-item danger"
                        onClick={() => { setCtxMenu(null); setDelTarget({ kind: "task", id: ctxMenu.id }); }}
                      >
                        <IconTrash size={14} /> 删除任务
                      </button>
                    </>
                  );
                })()}
              </>
            )}
          </div>
        </>
      )}

      {/* 删除确认提示框 */}
      {delTarget && (
        <>
          <div className="sb-overlay" style={{ zIndex: 85 }} onClick={() => setDelTarget(null)} />
          <div className="sb-confirm">
            <p>{delTarget.kind === "space" ? "删除这个工作空间？" : "删除这条对话？"}</p>
            <span className="confirm-sub">此操作不可撤销</span>
            <div className="confirm-actions">
              <button className="btn ghost sm" onClick={() => setDelTarget(null)}>取消</button>
              <button className="btn danger sm" onClick={confirmDel}>删除</button>
            </div>
          </div>
        </>
      )}
    </aside>
  );
}

function Row({ title, time, onClick, onMenu }: { title: string; time: string; onClick: () => void; onMenu?: (e: React.MouseEvent) => void }) {
  return (
    <div className="sb-row" onClick={onClick}>
      <span className="tt">{title}</span>
      <span className="tm">{time}</span>
      {onMenu && (
        <button className="row-x" aria-label="任务操作" title="更多"
          onClick={(e) => { e.stopPropagation(); onMenu(e); }}>
          <IconDots size={14} />
        </button>
      )}
    </div>
  );
}
