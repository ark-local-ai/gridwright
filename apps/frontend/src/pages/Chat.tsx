import { useRef, useState } from "react";
import { IconSend } from "../components/icons";
import { sendChat, type ChatMsg } from "../api";

interface Msg { role: "user" | "ai"; text: string }

const INITIAL: Msg[] = [
  { role: "ai", text: "你好，我是 Gridwright。我可以帮你做调研、写文档、做 PPT、分析数据等。告诉我你想做什么？" },
];

export default function Chat() {
  const [msgs, setMsgs] = useState<Msg[]>(INITIAL);
  const [v, setV] = useState("");
  const [busy, setBusy] = useState(false);
  const historyRef = useRef<ChatMsg[]>([]);

  const send = () => {
    const text = v.trim();
    if (!text || busy) return;
    setV("");
    historyRef.current.push({ role: "user", content: text });
    setMsgs((m) => [...m, { role: "user", text }, { role: "ai", text: "" }]);
    setBusy(true);

    // 新 AI 气泡游标（此时 msgs 尚未加入两条，故旧长度 + 1）
    const aiIndex = msgs.length + 1;
    let acc = "";

    sendChat(
      text,
      historyRef.current,
      (t) => {
        acc += t;
        setMsgs((m) => {
          const next = [...m];
          next[aiIndex] = { role: "ai", text: next[aiIndex].text + t };
          return next;
        });
      },
      (full) => {
        historyRef.current.push({ role: "assistant", content: full || acc });
        setBusy(false);
      },
      () => setBusy(false),
    );
  };

  return (
    <div className="page chat-page">
      <div className="chat-list" style={{ maxWidth: 760, margin: "0 auto" }}>
        {msgs.map((m, i) => (
          <div key={i} className={`msg ${m.role}`}>
            <div className="msg-bub">
              {m.text}
              {busy && i === msgs.length - 1 && m.role === "ai" && <span className="caret" />}
            </div>
          </div>
        ))}
      </div>
      <div className="inputbar" style={{ maxWidth: 760, margin: "20px auto 0" }}>
        <textarea rows={1} placeholder="输入消息…" value={v}
          onChange={(e) => setV(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } }} />
        <div className="row">
          <span className="pill">＋</span>
          <span className="pill blue">DeepSeek ▾</span>
          <button className="send" onClick={send} disabled={busy}><IconSend /></button>
        </div>
      </div>
    </div>
  );
}
