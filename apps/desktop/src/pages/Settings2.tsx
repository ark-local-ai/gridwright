import { useEffect, useState } from "react";
import "./settings.css";
import { agentApi } from "../api-agent";
import type { SettingsDto } from "../api-agent";
import { IconCheck, IconGear, IconX, IconSpark } from "../components/icons";
import MemoryPanel from "./MemoryPanel";

/* 设置（见 docs/agent-architecture/19-界面设计.md、20-离线可用与桌面交付.md）
   两栏：模型（脑）与记忆。分开的理由是**它们回答不同的问题**——
   模型栏是"它靠什么判断"，记忆栏是"它记住了什么、按什么规矩办"。
   都塞在一页里会让人以为记忆是模型的一个参数。

   工作区不在这里管了：它是"进哪份台账"这件事，属于主界面
   （拖文件夹进来 / 首屏选择），放在设置里反而让人以为是配置项。 */

type Tab = "model" | "memory";

export default function Settings({ onClose }: {
  onClose: () => void;
}) {
  const [tab, setTab] = useState<Tab>("model");
  const [s, setS] = useState<SettingsDto | null>(null);
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState("");

  const load = async () => {
    const st = await agentApi.settings().catch(() => null);
    if (st) {
      setS(st);
      setBaseUrl(st.baseUrl);
      setModel(st.model);
    }
  };
  useEffect(() => { void load(); }, []);

  const save = async () => {
    setSaving(true);
    setMsg("");
    try {
      const r = await agentApi.saveSettings({ baseUrl, model, apiKey: apiKey || undefined });
      setApiKey("");
      setMsg(r.saved ? "已保存到本机，更新后依然有效" : (r.warning ?? "已生效，但本地写入失败"));
      await load();
    } catch (e) {
      setMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="set-overlay" onClick={onClose}>
      <div className="set-panel" onClick={(e) => e.stopPropagation()}>
        <header className="set-head">
          <IconGear size={15} />
          <b>设置</b>
          {/* 两栏切换：与"分两页"等价的轻量做法，改动小、一眼看全有哪些可配 */}
          <nav className="set-tabs" role="tablist">
            <button className={`set-tab${tab === "model" ? " on" : ""}`} role="tab"
              aria-selected={tab === "model"} onClick={() => setTab("model")}>
              <IconGear size={12} />模型
              {s && !s.brainReady && <span className="set-tab-dot" title="还没配好" />}
            </button>
            <button className={`set-tab${tab === "memory" ? " on" : ""}`} role="tab"
              aria-selected={tab === "memory"} onClick={() => setTab("memory")}>
              <IconSpark size={12} />记忆
            </button>
          </nav>
          <button className="set-x" onClick={onClose} aria-label="关闭"><IconX size={14} /></button>
        </header>

        <div className="set-body">
          {tab === "model" && (
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
              {s?.configPath && (
                <p className="set-hint set-hint-sm">
                  配置保存在本机：<code className="set-path">{s.configPath}</code>（更新或重装后仍会复用）
                </p>
              )}
            </section>
          )}

          {tab === "memory" && <MemoryPanel />}
        </div>
      </div>
    </div>
  );
}

