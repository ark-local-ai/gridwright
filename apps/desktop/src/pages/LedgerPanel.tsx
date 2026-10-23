import { useEffect, useState } from "react";
import { SkPanel } from "../components/Skeleton";
import "./ledger.css";
import { agentApi } from "../api-agent";
import type { RollbackItem } from "../api-agent";
import { IconRefresh, IconCheck, IconNote, IconX } from "../components/icons";

/* 账目 + 回滚（见 docs/agent-architecture/29-UI升级与剩余功能.md）
   为什么要有这一页：官网写着"能回滚"，就得真能在这里点。
   回滚本身也记账，所以可以再倒回去（=重做），历史永不丢。 */

export default function LedgerPanel({ onClose, onChanged, onBusyChange }: {
  onClose: () => void;
  onChanged?: () => void;
  /** 回滚进行中回调：上层据此在对话里显示“正在执行”并锁住输入。 */
  onBusyChange?: (busy: boolean) => void;
}) {
  const [items, setItems] = useState<RollbackItem[]>([]);
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [load, setLoad] = useState(true);
  // 筛选：前端作用于已加载条目（回滚 ID 仍是原始下标，不能重排）；
  // 导出走后端同条件，避免“看到的”和“导出的”不是同一批。
  const [fTable, setFTable] = useState("");
  const [fStatus, setFStatus] = useState("");
  const [fOp, setFOp] = useState("");
  const [fSince, setFSince] = useState("");
  const [exportBusy, setExportBusy] = useState(false);

  const refresh = async () => {
    setLoad(true);
    try {
      const r = await agentApi.rollbackList(200);
      setItems(r.items ?? []);
      setSel(new Set());
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setLoad(false);
    }
  };
  useEffect(() => { void refresh(); }, []);

  const toggle = (id: number) => {
    setSel((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  };

  // 默认按"一批"选：同一次操作（同时间戳）一起倒，避免只倒一半
  const selectGroup = (group: string) => {
    setSel((s) => {
      const n = new Set(s);
      const ids = items.filter((i) => i.group === group && i.canRollback).map((i) => i.id);
      const allOn = ids.every((id) => n.has(id));
      ids.forEach((id) => (allOn ? n.delete(id) : n.add(id)));
      return n;
    });
  };

  const doRollback = async () => {
    const ids = [...sel];
    if (ids.length === 0) return;
    setBusy(true);
    onBusyChange?.(true);
    setMsg("");
    try {
      const r = await agentApi.rollback(ids);
      setMsg(r.note ?? `已回滚 ${r.rolled} 处`);
      await refresh();
      onChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
      onBusyChange?.(false);
    }
  };

  const rollbackable = items.filter((i) => i.canRollback);

  const filtered = items.filter((i) => {
    if (fTable && !i.table.toLowerCase().includes(fTable.toLowerCase()) &&
        !i.table.toLowerCase().replace(/\.xlsx$/, "").includes(fTable.toLowerCase().replace(/\.xlsx$/, ""))) return false;
    if (fStatus && i.status !== fStatus) return false;
    if (fOp && i.op !== fOp) return false;
    // 账目时间是本地墙上时间 "YYYY-MM-DD HH:MM"，与 <input type=date> 的字典序比较即可。
    if (fSince && i.ts.slice(0, 10) < fSince) return false;
    return true;
  });
  const hiddenSel = [...sel].filter((id) => !filtered.some((i) => i.id === id)).length;

  const doExport = async () => {
    setExportBusy(true);
    setMsg("");
    try {
      const blob = await agentApi.ledgerExport({
        table: fTable || undefined, status: fStatus || undefined,
        op: fOp || undefined, since: fSince || undefined,
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `gridwright-ledger-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setMsg(`已导出 ${filtered.length} 条（UTF-8 BOM，可用 Excel/WPS 直接打开）`);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setExportBusy(false);
    }
  };

  return (
    <div className="lg-overlay" onClick={onClose}>
      <div className="lg-panel" onClick={(e) => e.stopPropagation()}>
        <header className="lg-head">
          <IconNote size={15} />
          <b>账目</b>
          <span className="lg-sub">
            每一格改动都留了旧值；选中后可以倒回去
          </span>
          <button className="lg-x" onClick={onClose} aria-label="关闭账目"><IconX size={14} /></button>
        </header>

        <div className="lg-bar">
          <span className="lg-count">
            {filtered.length === items.length
              ? `${items.length} 条改动 · 可回滚 ${rollbackable.length} 条`
              : `筛选 ${filtered.length}/${items.length} 条 · 可回滚 ${rollbackable.length} 条`}
            {hiddenSel > 0 ? ` · 已选中 ${hiddenSel} 条被筛选隐藏` : ""}
          </span>
          <button className="btn ghost sm" onClick={() => void refresh()}><IconRefresh size={12} />刷新</button>
          <button
            className="btn primary sm"
            disabled={busy || sel.size === 0}
            onClick={() => void doRollback()}
          >
            {busy ? "回滚中…" : `回滚选中的 ${sel.size} 处`}
          </button>
        </div>

        {/* 筛选 + 导出：财务对账需要把账目交出去；没有导出这道工序做不完。 */}
        <div className="lg-filter">
          <input className="lg-input" placeholder="表名" value={fTable}
            onChange={(e) => setFTable(e.target.value)} />
          <select className="lg-input" value={fStatus} onChange={(e) => setFStatus(e.target.value)}>
            <option value="">全部状态</option>
            <option value="ok">成功</option>
            <option value="rejected">被拒</option>
          </select>
          <select className="lg-input" value={fOp} onChange={(e) => setFOp(e.target.value)}>
            <option value="">全部操作</option>
            <option value="set">改格</option>
            <option value="append">新增行</option>
            <option value="rollback">回滚</option>
          </select>
          <input className="lg-input" type="date" value={fSince} title="起始日期"
            onChange={(e) => setFSince(e.target.value)} />
          <button className="btn ghost sm lg-export" disabled={exportBusy} onClick={() => void doExport()}>
            {exportBusy ? "导出中…" : "导出 CSV"}
          </button>
        </div>

        {msg && <p className="lg-msg"><IconCheck size={12} />{msg}</p>}

        <div className="lg-body">
          {load && <div className="lg-sk"><SkPanel rows={6} /></div>}
          {!load && items.length === 0 && (
            <p className="lg-empty">
              还没有改动记录。让它改一次表，这里就会留下每一格的旧值。
            </p>
          )}
          {!load && items.length > 0 && filtered.length === 0 && (
            <p className="lg-empty">没有符合当前筛选的账目。清空表名/状态/日期再看。</p>
          )}
          {!load && items.length > 0 && filtered.length > 0 && (
            <table className="lg-table">
              <thead>
                <tr>
                  <th className="lg-c-check" />
                  <th>时间</th>
                  <th>位置</th>
                  <th>改动</th>
                  <th>原因</th>
                  <th>来源</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((it) => (
                  <tr key={it.id} className={it.canRollback ? "" : "no"}>
                    <td className="lg-c-check">
                      {it.canRollback ? (
                        <input
                          type="checkbox"
                          checked={sel.has(it.id)}
                          onChange={() => toggle(it.id)}
                          aria-label={`选择 ${it.sheet}!${it.cell}`}
                        />
                      ) : (
                        <span className="lg-nope" title={it.whyNot}>—</span>
                      )}
                    </td>
                    <td className="lg-t">
                      <button className="lg-grp" onClick={() => selectGroup(it.group)}
                        title="把这批（同一次操作）一起选中">
                        {it.ts}
                      </button>
                    </td>
                    <td className="lg-where">
                      <span className="lg-sheet">{it.sheet || it.table}</span>
                      <code>{it.cell || "＋行"}</code>
                    </td>
                    <td className="lg-delta">
                      <span className={it.op === "rollback" ? "lg-rb" : "lg-new"}>{it.new || "（空）"}</span>
                      <span className="lg-arrow">→</span>
                      <span className="lg-old">{it.old || "（空）"}</span>
                    </td>
                    <td className="lg-why">
                      {it.op === "rollback" ? <em className="lg-tag-rb">回滚</em> : null}
                      {it.reason || "—"}
                    </td>
                    <td className="lg-src" title={it.source || ""}>
                      {it.sourceTitle ? `对话·${it.sourceTitle}` : (it.source?.startsWith("对话:") ? "来自对话" : (it.source || "—"))}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}
