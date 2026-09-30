import { useState } from "react";
import "./generate.css";
import { agentApi } from "../api-agent";
import type { GenerateResult } from "../api-agent";
import { IconNote, IconSpark, IconX } from "../components/icons";

/* 生成（见 docs/agent-architecture/27-生成类任务.md、29-UI升级与剩余功能.md 第 4 项）
   以前它挂在**每一条**对话提议下面（无条件的「出一份草稿」），对话一长就堆满按钮、
   也容易被误点。现在它是一个独立入口：在这里说清要什么，产出落到工作区 生成/，
   **不碰原表**（原表的改动一律走「待确认」那条路）。 */

export default function GeneratePanel({ onClose }: { onClose: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [res, setRes] = useState<GenerateResult | null>(null);

  const run = async () => {
    const instruction = text.trim();
    if (!instruction) return;
    setBusy(true);
    setErr("");
    try {
      setRes(await agentApi.generate(instruction));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="gp-overlay" onClick={onClose}>
      <div className="gp-panel" onClick={(e) => e.stopPropagation()}>
        <header className="gp-head">
          <IconSpark size={15} />
          <b>生成</b>
          <span className="gp-sub">说清要什么，产出一份新文件；<b>不动原表</b></span>
          <button className="gp-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="gp-body">
          <textarea
            className="gp-input"
            rows={2}
            value={text}
            placeholder="例如：出一份 9 月欠租清单"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              // Enter 生成，Shift+Enter 换行（需求常常要写成两行）
              if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); void run(); }
            }}
            disabled={busy}
          />
          <div className="gp-ops">
            <button className="btn primary sm" onClick={() => void run()} disabled={busy || !text.trim()}>
              {busy ? "正在生成…" : "生成"}
            </button>
            <span className="gp-hint"><IconNote size={11} />结果落在工作区 <code>生成/</code></span>
          </div>
          {err && <p className="gp-err">{err}</p>}

          {res && (
            <div className="gen-card">
              <div className="gc-h">
                <b>{res.title || "已生成"}</b>
                <span className="gc-kind">草稿</span>
              </div>
              <div className="gc-meta">
                {res.rows > 0 && <span>{res.rows} 行</span>}
                {res.columns?.length > 0 && <span>列：{res.columns.join(" / ")}</span>}
              </div>
              {res.sum && Object.keys(res.sum).length > 0 && (
                <div className="gc-sum">
                  {Object.entries(res.sum).map(([k, v]) => (
                    <span key={k} className="gc-sum-i"><em>{k}</em>{v.toLocaleString()}</span>
                  ))}
                </div>
              )}
              {res.content && <pre className="gc-content">{res.content.slice(0, 600)}</pre>}
              <div className="gc-file">
                <code>{res.file}</code>
                <span className="gc-note">{res.notes?.[0] ?? "生成不改动原表"}</span>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
