import { useEffect, useState } from "react";
import { SkPanel } from "../components/Skeleton";
import "./ledger.css";
import { agentApi } from "../api-agent";
import type { RollbackItem } from "../api-agent";
import { IconRefresh, IconCheck, IconNote, IconX } from "../components/icons";

/* 账目 + 回滚（见 docs/agent-architecture/29-UI升级与剩余功能.md）
   为什么要有这一页：官网写着"能回滚"，就得真能在这里点。
   回滚本身也记账，所以可以再倒回去（=重做），历史永不丢。 */

export default function LedgerPanel({ onClose, onChanged }: {
  onClose: () => void;
  onChanged?: () => void;
}) {
  const [items, setItems] = useState<RollbackItem[]>([]);
  const [sel, setSel] = useState<Set<number>>(new Set());
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState("");
  const [load, setLoad] = useState(true);

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
    }
  };

  const rollbackable = items.filter((i) => i.canRollback);

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
            {items.length} 条改动 · 可回滚 {rollbackable.length} 条
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

        {msg && <p className="lg-msg"><IconCheck size={12} />{msg}</p>}

        <div className="lg-body">
          {load && <div className="lg-sk"><SkPanel rows={6} /></div>}
          {!load && items.length === 0 && (
            <p className="lg-empty">
              还没有改动记录。让它改一次表，这里就会留下每一格的旧值。
            </p>
          )}
          {!load && items.length > 0 && (
            <table className="lg-table">
              <thead>
                <tr>
                  <th className="lg-c-check" />
                  <th>时间</th>
                  <th>位置</th>
                  <th>改动</th>
                  <th>原因</th>
                </tr>
              </thead>
              <tbody>
                {items.map((it) => (
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
