import { useEffect, useState } from "react";
import { SkPanel } from "../components/Skeleton";
import { agentApi } from "../api-agent";
import type { InboxItem } from "../api-agent";
import { IconFiles, IconRefresh, IconCheck, IconX } from "../components/icons";
import "./ledger.css";
import "./inbox.css";

/* 收件箱队列。
   以前"新数据进来了没有"只能去资源管理器看 inbox；接口其实已经返回
   inboxCount，界面却没展示。这里把等待/处理中/失败分开显示，
   失败原因来自工作区的 inbox_failures.json（不是日志）。 */

export default function InboxPanel({ onClose, onChanged }: {
  onClose: () => void;
  onChanged?: () => void;
}) {
  const [items, setItems] = useState<InboxItem[]>([]);
  const [load, setLoad] = useState(true);
  const [busy, setBusy] = useState("");
  const [msg, setMsg] = useState("");

  const refresh = async () => {
    setLoad(true);
    try {
      const r = await agentApi.inbox();
      setItems(r.items ?? []);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setLoad(false);
    }
  };
  useEffect(() => { void refresh(); }, []);

  const runOne = async (name: string) => {
    setBusy(name);
    setMsg("");
    try {
      const r = await agentApi.inboxRun(name);
      if (r.claimed === false) setMsg(`${name} 正在被处理，稍后刷新`);
      else if (r.ok === false) setMsg(r.error ?? "处理失败");
      else setMsg(`${name} 已处理`);
      await refresh();
      onChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const runAll = async () => {
    setBusy("*");
    setMsg("");
    try {
      const r = await agentApi.inboxRun();
      setItems(r.items ?? []);
      setMsg(`处理完成，剩余 ${r.count ?? 0} 个文件`);
      onChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const skip = async (name: string) => {
    setBusy(name);
    setMsg("");
    try {
      await agentApi.inboxSkip(name, "人工跳过（收件箱面板）");
      setMsg(`${name} 已移到 inbox/skipped（不会被当成已处理）`);
      await refresh();
      onChanged?.();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy("");
    }
  };

  const failed = items.filter((i) => i.status === "failed").length;

  return (
    <div className="lg-overlay" onClick={onClose}>
      <div className="lg-panel" onClick={(e) => e.stopPropagation()}>
        <header className="lg-head">
          <IconFiles size={15} />
          <b>收件箱</b>
          <span className="lg-sub">
            {items.length} 个待处理{failed > 0 ? ` · ${failed} 个失败` : ""} · 成功后会移入 inbox/done
          </span>
          <button className="lg-x" onClick={onClose} aria-label="关闭收件箱"><IconX size={14} /></button>
        </header>

        <div className="lg-bar">
          <span className="lg-count">{load ? "读取中…" : `${items.length} 个文件`}</span>
          <button className="btn ghost sm" onClick={() => void refresh()} disabled={load}>
            <IconRefresh size={12} />刷新
          </button>
          <button className="btn primary sm" disabled={busy !== "" || items.length === 0}
            onClick={() => void runAll()}>
            {busy === "*" ? "处理中…" : "全部处理"}
          </button>
        </div>

        {msg && <p className="lg-msg"><IconCheck size={12} />{msg}</p>}

        <div className="lg-body">
          {load && <div className="lg-sk"><SkPanel rows={5} /></div>}
          {!load && items.length === 0 && (
            <p className="lg-empty">收件箱是空的。把新数据丢进 inbox/ 就会出现在这里。</p>
          )}
          {!load && items.length > 0 && (
            <ul className="ib-list">
              {items.map((it) => (
                <li key={it.name} className={`ib-row ${it.status}`}>
                  <div className="ib-main">
                    <span className="ib-name" title={it.name}>{it.name}</span>
                    <span className={`ib-badge ${it.status}`}>
                      {it.status === "processing" ? "处理中" : it.status === "failed" ? "失败" : "等待"}
                    </span>
                    {it.attempts ? <span className="ib-attempts">已试 {it.attempts} 次</span> : null}
                    <span className="ib-meta">{fmtSize(it.size)} · {it.modified}</span>
                  </div>
                  {it.status === "failed" && (
                    <p className="ib-err" title={it.error}>
                      {it.errorAt ? `${it.errorAt} · ` : ""}{it.error}
                    </p>
                  )}
                  {it.claimStale && <p className="ib-err">上一次处理可能中断了，下一轮会自动重试。</p>}
                  <div className="ib-actions">
                    <button className="btn ghost sm" disabled={busy !== "" || it.status === "processing"}
                      onClick={() => void runOne(it.name)}>
                      {busy === it.name ? "处理中…" : "现在处理"}
                    </button>
                    <button className="btn ghost sm" disabled={busy !== "" || it.status === "processing"}
                      onClick={() => void skip(it.name)}>
                      跳过
                    </button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}

function fmtSize(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
