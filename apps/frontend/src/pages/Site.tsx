import { Link } from "react-router-dom";
import "./site.css";
import {
  ArkLogo, IconCheck, IconClock, IconDoc, IconDown, IconFolder,
  IconLink, IconSearch, IconSpark, IconSlides, IconXls,
} from "../components/icons";

/* 下载入口：桌面客户端发布页（安装包由 Tauri 构建产出） */
const DOWNLOAD_URL = "https://github.com/ark-local-ai/ark/releases";

export default function Site() {
  return (
    <div className="site">
      {/* 顶部导航 */}
      <nav className="site-nav">
        <div className="left">
          <div className="site-logo"><ArkLogo h={16} /></div>
          <div className="site-menu">
            <a href="#deliver">交付</a>
            <a href="#data">数据</a>
            <a href="#pipeline">管道</a>
            <a href="#continuous">持续交付</a>
            <a href="#local">本地安全</a>
          </div>
        </div>
        <div className="site-cta">
          <a className="btn primary" href={DOWNLOAD_URL} target="_blank" rel="noreferrer">下载客户端</a>
        </div>
      </nav>

      {/* Hero：主题特征物 = 输入 → 管道 → 交付物 的活体示意 */}
      <header className="site-hero">
        <div className="hero-copy">
          <h1>把一句话需求，<br />做成能直接打开的<em>交付物</em></h1>
          <p className="sub">
            方舟是本地运行的 AI 交付工作台：说出需求，它拆成步骤、跑完管道，
            交付 Excel、Word、PPT 等可编辑文件 —— 数据不出本机。
          </p>
          <div className="hero-cta">
            <a className="btn primary lg" href={DOWNLOAD_URL} target="_blank" rel="noreferrer">下载客户端</a>
            <a className="btn ghost lg" href="#pipeline">看它怎么交付</a>
          </div>
          <p className="hero-meta">Windows / macOS · 本地运行 · 多模型切换</p>
        </div>

        <div className="hero-pipe" aria-label="任务管道示意">
          <div className="hp-in">
            <span className="hp-label">需求</span>
            <p>汇总各门店本月销售，出一份带透视的 Excel 报表</p>
          </div>
          <svg className="hp-flow" viewBox="0 0 120 8" aria-hidden="true">
            <line className="flow-line" x1="0" y1="4" x2="120" y2="4" />
          </svg>
          <div className="hp-stages">
            <div className="hp-stage done"><span className="st-ic"><IconCheck size={13} /></span>拆解任务</div>
            <div className="hp-stage done"><span className="st-ic"><IconCheck size={13} /></span>读取数据</div>
            <div className="hp-stage run"><span className="st-ic live-pulse"><IconSpark size={13} /></span>整编校验</div>
            <div className="hp-stage wait"><span className="st-ic">4</span>生成文件</div>
          </div>
          <svg className="hp-flow" viewBox="0 0 120 8" aria-hidden="true">
            <line className="flow-line" x1="0" y1="4" x2="120" y2="4" />
          </svg>
          <div className="hp-out">
            <span className="hp-label">交付物</span>
            <div className="hp-file"><span className="ftext" style={{ background: "var(--ft-xls)" }}>XLS</span>
              <div><b>门店销售报表.xlsx</b><span>4 个工作表 · 原生公式</span></div>
              <button className="btn ghost sm hp-dl" aria-label="下载"><IconDown size={12} /></button>
            </div>
          </div>
        </div>
      </header>

      {/* 交付：Office 成果即文件 */}
      <section id="deliver" className="site-section">
        <h2 className="sec-title">交付的不是聊天记录，是文件</h2>
        <p className="sec-sub">成果真实写入磁盘，交付即打开可编辑 —— 在 Word / Excel / PPT 里继续改，而不是截图粘贴。</p>
        <div className="site-grid cols-3">
          <div className="site-card">
            <div className="k"><span className="kic"><IconXls /></span>Excel</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>多工作表结构</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>公式原生可编辑</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>执行清单自动编号</div>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconDoc /></span>Word</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>图文混排</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>目录可更新</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>按场景结构化</div>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconSlides /></span>PPT</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>大纲转成页</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>数据用表格</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>版式自动排版</div>
          </div>
        </div>
      </section>

      {/* 数据：文件与工作空间 */}
      <section id="data" className="site-section site-alt">
        <div className="site-grid cols-2" style={{ alignItems: "center" }}>
          <div>
            <h2 className="sec-title">文件数据，落在自己的空间</h2>
            <p className="sec-sub">
              每一项任务的产出都归档进本机工作空间：按空间分组、按类型识别，
              随取随下。工作空间就是一个普通文件夹 —— 你能用资源管理器打开它。
            </p>
            <div style={{ marginBottom: 6 }}>
              {["空间分组", "类型识别", "交付物归档", "一键下载"].map((t) => (
                <span className="site-tag" key={t}>{t}</span>
              ))}
            </div>
          </div>
          <div className="site-card file-demo">
            <div className="fd-row"><span className="ftext" style={{ background: "var(--ft-xls)" }}>XLS</span>
              <b>门店销售报表.xlsx</b><span className="fd-m">128 KB · 今天</span></div>
            <div className="fd-row"><span className="ftext" style={{ background: "var(--ft-doc)" }}>DOC</span>
              <b>项目周报-更新版.docx</b><span className="fd-m">86 KB · 今天</span></div>
            <div className="fd-row"><span className="ftext" style={{ background: "var(--ft-ppt)" }}>PPT</span>
              <b>项目路演.pptx</b><span className="fd-m">2.4 MB · 昨天</span></div>
            <div className="fd-row"><span className="ftext" style={{ background: "var(--ft-pdf)" }}>PDF</span>
              <b>调研报告.pdf</b><span className="fd-m">1.1 MB · 昨天</span></div>
          </div>
        </div>
      </section>

      {/* 管道：任务如何跑完 */}
      <section id="pipeline" className="site-section">
        <h2 className="sec-title">一条看得见的管道</h2>
        <p className="sec-sub">
          每个任务被拆成有序步骤：拆解 → 处理 → 校验 → 生成。哪一步在跑、
          哪一步已完、交付物何时落盘，全程可见、可验收。
        </p>
        <div className="site-grid cols-2">
          <div className="site-card">
            <div className="k">执行过程 · 逐步推进</div>
            <div className="site-steps">
              {[
                { t: "拆解任务", s: "done", d: "3 个阶段 · 11 个步骤" },
                { t: "读取与整编", s: "done", d: "4 个数据源" },
                { t: "生成文件", s: "run", d: "正在写入工作空间" },
                { t: "验收", s: "wait", d: "3 项检查清单" },
              ].map((s) => (
                <div className={`site-step ${s.s}`} key={s.t}>
                  <span className="n">{s.s === "done" ? <IconCheck size={12} /> : s.t}</span>
                  <div><b>{s.t}</b><span>{s.d}</span></div>
                </div>
              ))}
            </div>
          </div>
          <div className="site-card">
            <div className="k">验收清单 · 交付前自查</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>结构符合场景模板</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>数据项无缺失</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>文件可在 Office 中打开</div>
            <p style={{ marginTop: 14 }}>检查不过关，就不会标记为交付。</p>
          </div>
        </div>
      </section>

      {/* 持续交付：调研 + 定时 */}
      <section id="continuous" className="site-section site-alt">
        <h2 className="sec-title">从一次交付，到持续交付</h2>
        <p className="sec-sub">重复的工作交给方舟自动完成：定期出报告，按点推送。</p>
        <div className="site-grid cols-2">
          <div className="site-card">
            <div className="k"><span className="kic"><IconSearch /></span>深入调研 · 带引用</div>
            <p>内置联网搜索与真渲染浏览器，检索多方来源、读取正文，产出带引用的报告 —— 搜到的资料自动归档进工作空间。</p>
            <div className="site-check" style={{ marginTop: 12 }}><span className="ch"><IconLink size={15} /></span>引用可回源</div>
            <div className="site-check"><span className="ch"><IconFolder size={15} /></span>资料即归档</div>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconClock /></span>定时任务 · 自动执行</div>
            <p>按预设时间自动执行：晨报、周报、日报，到点产出文件。</p>
            <div className="site-check" style={{ marginTop: 12 }}><span className="ch"><IconCheck size={15} /></span>计划可视化</div>
            <div className="site-check"><span className="ch"><IconCheck size={15} /></span>执行留痕</div>
          </div>
        </div>
      </section>

      {/* 本地安全 */}
      <section id="local" className="site-section">
        <h2 className="sec-title">数据不出本机</h2>
        <p className="sec-sub">方舟在你的电脑本地运行：工作空间是本机文件夹，默认只监听 127.0.0.1，不主动外发。模型可自由切换 —— 云端或本地，你决定。</p>
        <div className="site-grid cols-3">
          <div className="site-card"><div className="k">本地工作空间</div><p>文件就在你的磁盘上，资源管理器直接打开。</p></div>
          <div className="site-card"><div className="k">只监听本机</div><p>默认 127.0.0.1，不主动外发任何数据。</p></div>
          <div className="site-card"><div className="k">模型可切换</div><p>云端 API 或本地 Ollama，多渠道自由切换。</p></div>
        </div>
      </section>

      {/* 结尾 CTA */}
      <section className="site-cta-end">
        <h2>把交付，交给方舟</h2>
        <p>本地运行、数据自主，让每一项工作都有文件落地。</p>
        <a className="btn primary lg" href={DOWNLOAD_URL} target="_blank" rel="noreferrer">下载客户端</a>
      </section>

      <footer className="site-foot">
        <span>Ark · 方舟 — 数据不出本机</span>
        <span>Windows / macOS · 免费开源</span>
      </footer>
    </div>
  );
}
