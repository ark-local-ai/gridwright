import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import {
  GridwrightLogo, IconArrowUp, IconChevD, IconFolder,
  IconLibrary, IconSpark, IconPlus, IconX,
} from "../components/icons";
import { scenarios as mockScenarios } from "../data/mock";
import { createTask, listScenarios, type ScenarioDto } from "../api";

export default function Home() {
  const nav = useNavigate();
  const loc = useLocation();
  // 场景库「使用」跳回时带入 { scene, prompt }，与芯片选中的落点一致
  const preset = (loc.state as { scene?: string; prompt?: string } | null) ?? null;
  const [v, setV] = useState(preset?.prompt ?? "");
  const [tag, setTag] = useState<string | null>(preset?.scene ?? null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [scenarios, setScenarios] = useState<ScenarioDto[]>(mockScenarios);
  useEffect(() => { listScenarios().then(setScenarios).catch(() => {}); }, []);

  const applyPrompt = (sceneName: string, prompt: string) => {
    setTag(sceneName);
    setV(prompt);
    setOpenId(null);
  };

  const submit = async () => {
    if (!v.trim() || submitting) return;
    setSubmitting(true);
    try {
      const taskId = await createTask(v.trim());
      nav(`/app/task?taskId=${taskId}`);
    } catch (e) {
      console.error(e);
      alert("创建任务失败，请确认后端已启动（npm run dev —— apps/backend）");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="page">
      <div className="home">
        {/* gridwright 字标水印（替代原 A 图标） */}
        <div className="home-mark"><GridwrightLogo h={34} thin /></div>
        <h2>今天帮你做些什么？</h2>
        <p className="sub">说出需求，专家团队自主规划，在本地工作空间交付可验收的成果</p>

        {/* 场景胶囊行：点开下拉该场景的参考提示词（WorkBuddy 式封装） */}
        <div className="chips-row">
          <div className="chips">
            {scenarios.map((s) => (
              <div className="chip-wrap" key={s.id}>
                <button
                  className={`chip${openId === s.id ? " on" : ""}`}
                  onClick={() => setOpenId(openId === s.id ? null : s.id)}
                >
                  {s.name}<IconChevD size={11} />
                </button>
                {openId === s.id && (
                  <div className="chip-menu">
                    <div className="chip-menu-h">{s.desc}</div>
                    {s.prompts.map((p, i) => (
                      <button key={i} className="chip-menu-item"
                        onClick={() => applyPrompt(s.name, p.text)}>
                        <span className="cm-note">{p.note}</span>
                        <span className="cm-text">{p.text}</span>
                      </button>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
          {/* 全部场景：挪到最右、只留图标 */}
          <button className="chip-all" title="全部场景" aria-label="全部场景" onClick={() => nav("/app/prompts")}>
            <IconLibrary size={15} />
          </button>
        </div>
        {openId && <div className="menu-overlay" onClick={() => setOpenId(null)} />}

        {/* 输入卡：场景标签（可移除）+ 提示词正文 */}
        <div className="inputbar">
          {tag && (
            <div className="input-tags">
              <span className="input-tag">
                {tag}
                <button className="tag-x" aria-label="移除场景" onClick={() => setTag(null)}>
                  <IconX size={10} />
                </button>
              </span>
            </div>
          )}
          <textarea rows={2} placeholder="今天帮你做些什么？ @ 引用工作区文件，/ 调用技能与指令"
            value={v} onChange={(e) => setV(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); } }} />
          <div className="row">
            <button className="ic-btn" aria-label="添加附件"><IconPlus size={15} /></button>
            <button className="send" onClick={submit} aria-label="发送"><IconArrowUp size={14} /></button>
          </div>
          <div className="foot">
            <button className="foot-chip"><IconFolder size={13} />选择工作空间 ▾</button>
            <span className="pill ghost auto-pill"><IconSpark size={12} />Auto ▾</span>
          </div>
        </div>
      </div>
    </div>
  );
}
