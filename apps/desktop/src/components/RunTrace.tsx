import { useState } from "react";
import "./run-trace.css";
import type { TraceData } from "../api-agent";

/* 运行详情（后端 internal/trace 的产物）。
 *
 * 为什么要有它：engine.log 只有两层——HTTP 请求行，和一行「[llm] ok（29.6s，22304B）」。
 * 中间那层（读了哪些表、扫了联动图、发给模型什么、模型答了什么、定位到多少格）
 * 完全没有，于是出问题时看不出「思考到了哪一步、哪一步调用了什么」。
 * 这里把后端那次运行的步骤直接摊开，就在用户已经在看的地方（对话/待确认）。
 */
export default function RunTrace({ trace, label = "运行详情" }: {
  trace?: TraceData | null;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const [showPrompt, setShowPrompt] = useState(false);
  const [showRaw, setShowRaw] = useState(false);
  if (!trace || (!trace.steps?.length && !trace.prompt && !trace.raw)) return null;
  const steps = trace.steps ?? [];
  const total = steps.reduce((s, x) => s + (x.ms || 0), 0);
  return (
    <div className="rt">
      <button className="rt-h" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <span className={`rt-caret${open ? " open" : ""}`}>›</span>
        {label}
        <span className="rt-sub">{steps.length} 步 · 共 {total}ms</span>
      </button>
      {open && (
        <div className="rt-body">
          <ol className="rt-steps">
            {steps.map((s, i) => (
              <li key={i}>
                <span className="rt-name">{s.name}</span>
                {s.detail && <span className="rt-detail">{s.detail}</span>}
                {s.ms > 0 && <span className="rt-ms">{s.ms}ms</span>}
              </li>
            ))}
          </ol>
          {trace.prompt && (
            <div className="rt-part">
              <button className="rt-toggle" onClick={() => setShowPrompt((v) => !v)}>
                {showPrompt ? "收起" : "查看"}发给模型的提示（{trace.prompt.length.toLocaleString()} 字）
              </button>
              {showPrompt && <pre className="rt-pre">{trace.prompt}</pre>}
            </div>
          )}
          {trace.raw && (
            <div className="rt-part">
              <button className="rt-toggle" onClick={() => setShowRaw((v) => !v)}>
                {showRaw ? "收起" : "查看"}模型原样返回（{trace.raw.length.toLocaleString()} 字）
              </button>
              {showRaw && <pre className="rt-pre">{trace.raw}</pre>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
