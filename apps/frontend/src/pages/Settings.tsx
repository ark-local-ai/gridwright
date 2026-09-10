import { useEffect, useState } from "react";
import {
  listChannels, createChannel, updateChannel, deleteChannel, setDefaultChannel, testChannel,
  getSearchSource, setSearchSourceEnabled, setSearchProvider, setSearchQuota, webSearch,
  type ChannelDto, type SearchSourceStatus,
} from "../api";
import Stats from "./Stats";
import Memories from "./Memories";
import SettingsUsers from "./SettingsUsers";

const GROUPS: { label?: string; items: string[] }[] = [
  { items: ["应用", "通用", "外观", "通知"] },
  { label: "能力", items: ["模型", "联网搜索", "技能", "连接器", "专家", "Agent"] },
  { label: "工具", items: ["统计", "记忆", "用户管理"] },
  { label: "智能", items: ["长期记忆", "自进化", "回归评测"] },
  { label: "系统", items: ["工作目录", "快捷键", "备份", "安全"] },
];

const EMPTY: { name: string; proto: "openai" | "anthropic"; model: string; baseUrl: string; apiKey: string; priority: string; cost: string } =
  { name: "", proto: "openai", model: "", baseUrl: "", apiKey: "", priority: "50", cost: "0" };

export default function Settings() {
  const [sec, setSec] = useState("模型");
  const [channels, setChannels] = useState<ChannelDto[]>([]);
  const [err, setErr] = useState(false);
  const [form, setForm] = useState(EMPTY);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [testing, setTesting] = useState<string | null>(null);

  // —— M53 联网搜索源管理 ——
  const [ws, setWs] = useState<SearchSourceStatus | null>(null);
  const [wsErr, setWsErr] = useState("");
  const [wsProv, setWsProv] = useState<"duckduckgo" | "custom">("duckduckgo");
  const [wsEndpoint, setWsEndpoint] = useState("");
  const [wsKey, setWsKey] = useState("");
  const [wsRpm, setWsRpm] = useState("30");
  const [wsTest, setWsTest] = useState("");

  const loadSearchSource = () => {
    setWsErr("");
    getSearchSource().then((s) => {
      setWs(s);
      setWsProv(s.provider);
      setWsEndpoint(s.endpoint ?? "");
      setWsRpm(String(s.quote.rpm));
    }).catch(() => setWsErr("后端未启动，搜索源读不到"));
  };
  useEffect(() => { if (sec === "联网搜索") loadSearchSource(); }, [sec]);

  const load = () => {
    setErr(false);
    listChannels().then(setChannels).catch(() => setErr(true));
  };
  useEffect(load, []);

  const closeForm = () => { setForm(EMPTY); setEditingId(null); setShowForm(false); };

  const submit = async () => {
    if (!form.name.trim() || !form.baseUrl.trim()) return;
    const priority = Math.max(0, Math.min(100, Number(form.priority) || 50));
    const cost = Math.max(0, Number(form.cost) || 0);
    const body = {
      name: form.name, proto: form.proto, model: form.model || "默认模型",
      baseUrl: form.baseUrl, apiKey: form.apiKey || undefined,
      priority, cost,
    };
    if (editingId) {
      await updateChannel(editingId, body);
    } else {
      await createChannel(body);
    }
    closeForm();
    load();
  };

  const edit = (c: ChannelDto) => {
    setEditingId(c.id);
    setForm({
      name: c.name, proto: c.proto, model: c.model, baseUrl: c.baseUrl ?? "",
      apiKey: c.apiKey ?? "", priority: String(c.priority ?? 50), cost: String(c.cost ?? 0),
    });
    setShowForm(true);
  };

  const del = async (c: ChannelDto) => {
    if (!window.confirm(`删除渠道「${c.name}」？`)) return;
    await deleteChannel(c.id);
    load();
  };

  const setDefault = async (c: ChannelDto) => {
    await setDefaultChannel(c.id);
    load();
  };

  const test = async (c: ChannelDto) => {
    setTesting(c.id);
    const r = await testChannel(c.id).catch(() => ({ ok: false }));
    setTesting(null);
    window.alert(`${c.name} 验活${r.ok ? "成功 ✓（可连通）" : "失败 ✗（无法连通，检查 URL / Key）"}`);
  };

  return (
    <div className="page">
      <div className="settings-wrap">
        <nav className="settings-nav">
          {GROUPS.map((g, gi) => (
            <div key={gi}>
              {g.label && <div style={{ fontSize: 11, color: "var(--text-3)", padding: "8px 10px 4px", fontWeight: 600 }}>{g.label}</div>}
              {g.items.map((it) => (
                <div key={it} className={`s-nav-item${it === sec ? " on" : ""}`} onClick={() => setSec(it)}>{it}</div>
              ))}
            </div>
          ))}
        </nav>

        <div>
          {sec === "模型" ? (
            <>
              <h2 style={{ fontSize: 20, marginBottom: 6 }}>模型</h2>
              <p style={{ fontSize: 12.5, color: "var(--text-2)", marginBottom: 4 }}>本地模型渠道：增删改、验活、设默认（存本地，数据不出本机）</p>
              <p style={{ fontSize: 12, color: "var(--text-3)", marginBottom: 16 }}>填一个服务商的 Key 就能开工。不登录也照样能配、能用。</p>
              <button className="btn primary" style={{ marginBottom: 16 }} onClick={() => { setEditingId(null); setForm(EMPTY); setShowForm((s) => !s); }}>
                {showForm && !editingId ? "取消新增" : "＋ 新增渠道"}
              </button>

              {showForm && (
                <div className="card" style={{ padding: "16px 18px", marginBottom: 16 }}>
                  <div style={{ fontSize: 13.5, fontWeight: 600, marginBottom: 12 }}>{editingId ? "编辑渠道" : "新增渠道"}</div>
                  <div style={{ display: "grid", gap: 10 }}>
                    <input className="inp" placeholder="名称，如：DeepSeek" value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} />
                    <input className="inp" placeholder="模型，如：deepseek-chat（可留空）" value={form.model} onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))} />
                    <input className="inp" placeholder="Base URL，如：https://api.deepseek.com/v1" value={form.baseUrl} onChange={(e) => setForm((f) => ({ ...f, baseUrl: e.target.value }))} />
                    <input className="inp" type="password" placeholder="API Key（可留空，本地 Ollama 不需要）" value={form.apiKey} onChange={(e) => setForm((f) => ({ ...f, apiKey: e.target.value }))} />
                    <div style={{ display: "flex", gap: 10 }}>
                      <input className="inp" placeholder="优先级 0-100（默认 50）" value={form.priority} onChange={(e) => setForm((f) => ({ ...f, priority: e.target.value }))} />
                      <input className="inp" placeholder="成本 $/1K tokens（0=免费/本地）" value={form.cost} onChange={(e) => setForm((f) => ({ ...f, cost: e.target.value }))} />
                    </div>
                    <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                      <label style={{ fontSize: 13 }}>协议
                        <select className="inp" style={{ width: "auto", marginLeft: 8 }} value={form.proto}
                          onChange={(e) => setForm((f) => ({ ...f, proto: e.target.value as "openai" | "anthropic" }))}>
                          <option value="openai">openai</option><option value="anthropic">anthropic</option>
                        </select>
                      </label>
                      <button className="btn primary sm" onClick={submit}>保存</button>
                      <button className="btn ghost sm" onClick={closeForm}>取消</button>
                    </div>
                  </div>
                </div>
              )}

              {err && <div style={{ fontSize: 12, color: "var(--warn)", marginBottom: 10 }}>后端未启动，渠道读不到</div>}
              {channels.map((c) => (
                <div key={c.id} className="channel">
                  <div>
                    <div className="c-name">{c.name}</div>
                    <div className="c-url">{c.model} · {c.baseUrl ?? ""}</div>
                    {typeof c.rate === "number" && (
                      <div className="c-url" style={{ color: "var(--text-3)" }}>
                        成功率 {c.rate}%（{c.ok} 成 / {c.fail} 败）
                        {c.latency ? ` · 均延迟 ${Math.round(c.latency)}ms` : ""}
                        {typeof c.cost === "number" && c.cost > 0 ? ` · 成本 $${c.cost}/1K` : c.cost === 0 ? " · 近乎免费" : ""}
                        · 优先级 {c.priority ?? 50}
                        {typeof c.score === "number" ? ` · 评分 ${c.score.toFixed(2)}` : ""}
                      </div>
                    )}
                  </div>
                  <span className="pill" style={{ background: "var(--brand-soft)", color: "var(--brand)" }}>{c.proto}</span>
                  {c.default && <span className="c-def">✓ 当前默认</span>}
                  <div className="c-ops">
                    <button className="btn ghost sm" onClick={() => test(c)} disabled={testing === c.id}>{testing === c.id ? "验活中…" : "验活"}</button>
                    <button className="btn ghost sm" onClick={() => edit(c)}>编辑</button>
                    <button className="btn ghost sm" onClick={() => del(c)}>删除</button>
                    {!c.default && <button className="btn soft sm" onClick={() => setDefault(c)}>设为默认</button>}
                  </div>
                </div>
              ))}
            </>
          ) : sec === "联网搜索" ? (
            <>
              <h2 style={{ fontSize: 20, marginBottom: 6 }}>联网搜索</h2>
              <p style={{ fontSize: 12.5, color: "var(--text-2)", marginBottom: 16 }}>
                联网搜索会把你的查询经本机出网。默认免 key 的 DuckDuckGo，可换成自配 provider；数据不出本机原则下，出网可随时显式关停。
              </p>

              {wsErr && <div style={{ fontSize: 12, color: "var(--warn)", marginBottom: 12 }}>{wsErr}</div>}

              {/* 启用开关 */}
              <div className="card" style={{ padding: "14px 18px", marginBottom: 14 }}>
                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <div>
                    <div style={{ fontSize: 13.5, fontWeight: 600 }}>联网搜索</div>
                    <div style={{ fontSize: 12, color: "var(--text-3)", marginTop: 3 }}>
                      {ws?.enabled === false ? "已关停：编排不再自动联网，web.search 将返回 403" : "已启用：搜索/调研类提示词会自动联网"}
                    </div>
                  </div>
                  <button
                    className={`btn ${ws?.enabled === false ? "primary" : "ghost"} sm`}
                    onClick={async () => {
                      const next = !(ws?.enabled ?? true);
                      await setSearchSourceEnabled(next).catch(() => {});
                      loadSearchSource();
                    }}
                  >
                    {ws?.enabled === false ? "启用" : "关停"}
                  </button>
                </div>
              </div>

              {/* 供应商配置 */}
              <div className="card" style={{ padding: "16px 18px", marginBottom: 14 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, marginBottom: 10 }}>搜索供应商</div>
                <div style={{ display: "grid", gap: 10 }}>
                  <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                    <label style={{ fontSize: 13 }}>来源
                      <select className="inp" style={{ width: "auto", marginLeft: 8 }} value={wsProv}
                        onChange={(e) => setWsProv(e.target.value as "duckduckgo" | "custom")}>
                        <option value="duckduckgo">DuckDuckGo（免 key 默认）</option>
                        <option value="custom">自配 provider</option>
                      </select>
                    </label>
                    <span className="pill" style={{ background: "var(--brand-soft)", color: "var(--brand)" }}>
                      {ws?.provider === "custom" ? "自配 · 已生效" : "免 key"}
                    </span>
                  </div>

                  {wsProv === "custom" && (
                    <>
                      <input className="inp" placeholder="Endpoint，如：https://search.example.com/v1"
                        value={wsEndpoint} onChange={(e) => setWsEndpoint(e.target.value)} />
                      <input className="inp" type="password" placeholder="API Key（可选）"
                        value={wsKey} onChange={(e) => setWsKey(e.target.value)} />
                      {ws?.hasKey && wsKey === "" && (
                        <div style={{ fontSize: 12, color: "var(--text-3)" }}>已保存过一个 Key（此处留空则沿用）</div>
                      )}
                    </>
                  )}

                  <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                    <button className="btn primary sm" onClick={async () => {
                      setWsErr("");
                      const r = await setSearchProvider(wsProv,
                        wsProv === "custom" ? wsEndpoint : undefined,
                        wsProv === "custom" ? wsKey : undefined,
                      ).catch(() => ({ provider: "duckduckgo", endpointConfigured: false, configuredEndpoint: "" }) as { provider: "duckduckgo" | "custom"; endpointConfigured: boolean; configuredEndpoint: string });
                      if (r.provider === "custom" && !r.endpointConfigured) {
                        setWsErr("自配 provider 需要有效的 Endpoint（未配置则回落 DuckDuckGo）");
                      } else {
                        setWsKey("");
                      }
                      loadSearchSource();
                    }}>保存供应商</button>
                    <span style={{ fontSize: 12, color: "var(--text-3)" }}>
                      {ws?.endpoint ? `当前：${ws.endpoint}` : "当前：DuckDuckGo 免 key"}
                    </span>
                  </div>
                </div>
              </div>

              {/* 配额 */}
              <div className="card" style={{ padding: "16px 18px", marginBottom: 14 }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, marginBottom: 10 }}>配额（防出网打爆）</div>
                <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
                  <input className="inp" style={{ width: 130 }} placeholder="次/分钟"
                    value={wsRpm} onChange={(e) => setWsRpm(e.target.value)} />
                  <button className="btn primary sm" onClick={async () => {
                    setWsErr("");
                    await setSearchQuota(Number(wsRpm) || 30).catch(() => {});
                    loadSearchSource();
                  }}>保存配额</button>
                  {ws && (
                    <span style={{ fontSize: 12.5, color: "var(--text-3)" }}>
                      本分钟已用 <b>{ws.quote.used}</b> / {ws.quote.rpm}
                      {ws.quote.limited ? "（已达上限，超出的搜索将返回『请稍后再试』）" : `，剩余 ${ws.quote.remaining}`}
                    </span>
                  )}
                </div>
              </div>

              {/* 测试搜索 */}
              <div className="card" style={{ padding: "16px 18px" }}>
                <div style={{ fontSize: 13.5, fontWeight: 600, marginBottom: 10 }}>测试搜索</div>
                <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
                  <input className="inp" placeholder="输入关键词试搜，如：行业报告"
                    value={wsTest} onChange={(e) => setWsTest(e.target.value)} />
                  <button className="btn soft sm" disabled={!wsTest.trim()} onClick={async () => {
                    setWsErr("");
                    const r = await webSearch(wsTest.trim());
                    if (r.error) {
                      setWsErr(`试搜失败：${r.error}`);
                    } else {
                      setWsErr(`试搜成功：返回 ${r.count} 条结果（第一条「${r.hits[0]?.title ?? ""}」）`);
                    }
                  }}>试搜</button>
                </div>
              </div>
            </>
          ) : sec === "统计" ? (
            <div className="sec-embed"><Stats /></div>
          ) : sec === "记忆" ? (
            <div className="sec-embed"><Memories /></div>
          ) : sec === "用户管理" ? (
            <div className="sec-embed"><SettingsUsers /></div>
          ) : (
            <div className="hint-wrap">
              <span style={{ fontSize: 13.5 }}>「{sec}」设置将在后续接入 —— 这里先占好坑位。</span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
