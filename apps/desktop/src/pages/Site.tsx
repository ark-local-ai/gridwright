import { useState } from "react";
import "./site.css";
import {
  GridwrightLogo, IconCheck, IconFolder,
  IconLink, IconNote, IconRefresh, IconShield, IconTable, IconXls,
} from "../components/icons";
import { APP_VERSION } from "../lib/version";

/* 下载发布页。
   版本号**不再手写**：下载链的 tag 与安装包文件名都由 APP_VERSION 拼出来，
   所以发版只需改 apps/desktop/package.json 一处。
   之前这里写死 v0.1.1，而应用已经到 0.1.2——链接就悄悄指到了旧包。 */
const RELEASES = "https://github.com/ark-local-ai/gridwright/releases";
const TAG = `v${APP_VERSION}`;
const DL = `${RELEASES}/download/${TAG}`;
const SETUP_FILE = `gridwright_${APP_VERSION}_x64-setup.exe`;

/* 发布渠道。
   现在只做 Windows 桌面客户端（Tauri 安装包，见 scripts/build-desktop.sh）：
   双击安装、有窗口、引擎随包，用户不需要另装东西。
   免安装的单文件版仍保留（同一个引擎 + 界面已内嵌），给"不想装"的场景用。 */
type DownloadOS = { os: string; label: string; kind: string; note: string; file: string; url: string };
const DOWNLOADS: DownloadOS[] = [
  { os: "setup", label: "Windows 安装版", kind: "安装包", note: "双击安装 · 有窗口 · 引擎随包", file: SETUP_FILE, url: `${DL}/${SETUP_FILE}` },
  { os: "portable", label: "免安装版", kind: "单文件", note: "不装 · 双击运行 · 浏览器打开", file: "gridwright-windows-amd64.exe", url: `${DL}/gridwright-windows-amd64.exe` },
];

export default function Site() {
  const [os, setOs] = useState("win10");
  const sel = DOWNLOADS.find((d) => d.os === os) ?? DOWNLOADS[0];

  return (
    <div className="site">
      {/* 顶部导航 */}
      <nav className="site-nav">
        <div className="left">
          <div className="site-logo">
            <GridwrightLogo h={21} />
          </div>
          <div className="site-menu">
            <a href="#watch">盯表</a>
            <a href="#rules">规则</a>
            <a href="#ledger">账目</a>
            <a href="#legacy">本地</a>
            <a href="#download">下载</a>
          </div>
        </div>
        <div className="site-cta">
          <a className="btn primary" href="#download">下载</a>
        </div>
      </nav>

      {/* Hero。
          标题改过一次：原来是「你的表，有人替你看着」——那是句广告语，
          读的人第一眼看不出**这是什么产品**（"表"是 Excel 还是别的？
          "有人"是谁？）。现在先说清是什么（Excel 台账管家），
          再说它做什么（盯、改、留账），最后才是那句价值主张。 */}
      <header className="site-hero">
        <div className="hero-copy">
          <h1>守 Excel 台账的<em>本地助手</em></h1>
          <p className="sub">
            盯着你的台账文件夹：新数据一进来，按你定的规矩自动改表；
            每一格改动都留账、能回滚、改完通知你。数据不出本机。
          </p>
          <div className="hero-cta">
            <a className="btn primary lg" href="#download">下载 Gridwright</a>
            <a className="btn ghost lg" href="#watch">了解工作方式</a>
          </div>
          <p className="hero-meta">Windows 桌面客户端 · 数据存储于本机 · 改动全程可查</p>
        </div>

        <div className="hero-pipe" aria-label="数据管家运行示意">
          <div className="hp-in">
            <span className="hp-label">新数据</span>
            <p>报价单 quote.csv 丢进 inbox</p>
          </div>
          <svg className="hp-flow" viewBox="0 0 120 8" aria-hidden="true">
            <line className="flow-line" x1="0" y1="4" x2="120" y2="4" />
          </svg>
          <div className="hp-stages">
            <div className="hp-stage done"><span className="st-ic"><IconCheck size={13} /></span>读懂数据</div>
            <div className="hp-stage done"><span className="st-ic"><IconCheck size={13} /></span>按规矩改表</div>
            <div className="hp-stage run"><span className="st-ic live-pulse"><IconRefresh size={13} /></span>记账留痕</div>
            <div className="hp-stage wait"><span className="st-ic">4</span>微信通知</div>
          </div>
          <svg className="hp-flow" viewBox="0 0 120 8" aria-hidden="true">
            <line className="flow-line" x1="0" y1="4" x2="120" y2="4" />
          </svg>
          <div className="hp-out">
            <span className="hp-label">结果</span>
            <div className="hp-file"><span className="ftext" style={{ background: "var(--ft-xls)" }}>XLS</span>
              <div><b>销售.xlsx 已更新</b><span>价格 ×1 · 新增 ×1 · 账目 +1 行</span></div>
            </div>
            <div className="hp-ledger"><IconNote size={12} /><code>2026-09-11 10:32 · 销售 · A12 · 320→455 · 报价#14 · ok</code></div>
          </div>
        </div>
      </header>

      {/* 盯表：新数据进来，它自己会动 */}
      <section id="watch" className="site-section">
        <h2 className="sec-title">新数据进来，自动入账</h2>
        <p className="sec-sub">
          无需人工盯守。将新表或新数据放入工作区的 inbox，Gridwright 会自动接续：
          读懂表头和新数据，交给"脑"决定怎么改，改完写回 Excel。
        </p>
        <div className="site-grid cols-3">
          <div className="site-card">
            <div className="k"><span className="kic"><IconFolder /></span>盯住一个文件夹</div>
            <p>工作区就是一个普通文件夹。新数据进 inbox、处理后自动归档到 done，资源管理器随时能看。</p>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconTable /></span>读懂你的表</div>
            <p>表头每次实时读，不缓存——你怎么改列它都跟得上。新数据该进哪张表，它自己分诊。</p>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconXls /></span>改完就落盘</div>
            <p>原生 Excel 公式可继续编辑，不是截图粘贴。目标表正被 Excel 打开时自动跳过、等下次。</p>
          </div>
        </div>
      </section>

      {/* 规则：规矩你定，它不敢越线 */}
      <section id="rules" className="site-section site-alt">
        <h2 className="sec-title">规则先行，越界即拦</h2>
        <p className="sec-sub">
          如何修改由规则决定，而非模型自行判断。规则是可读的 yaml 文件，用记事本即可编辑。
        </p>
        <div className="site-grid cols-2">
          <div className="site-card">
            <div className="k"><span className="kic"><IconShield /></span>禁止列 = 硬护栏</div>
            <p>
              <code>forbid</code> 里写死的列——成本、结算日期——连大模型都动不了。
              它真想改，也会被拦下、单独记账成"已跳过"，而不是偷偷改。
            </p>
            <div className="site-check" style={{ marginTop: 12 }}><span className="ch"><IconCheck size={15} /></span>越界的改动，看得见</div>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconNote /></span>样板化 = 花得起的算力</div>
            <p>
              规则命中就短路：固定列映射这种简单改法直接执行，不花 token。
              只有真正需要"理解"的部分才交给脑——发送量恒定，不随次数膨胀。
            </p>
            <div className="site-check" style={{ marginTop: 12 }}><span className="ch"><IconCheck size={15} /></span>人看得懂、可版本化</div>
          </div>
        </div>
      </section>

      {/* 账目：每一格改动，都留有账 */}
      <section id="ledger" className="site-section">
        <h2 className="sec-title">每一格改动，均有记录</h2>
        <p className="sec-sub">
          不是"改完了"三个字就完事。ledger 是 append-only 的流水，每一格一条，
          旧值留着——改错了能回滚，审计时翻得出。
        </p>
        <div className="site-grid cols-2">
          <div className="site-card">
            <div className="k"><span className="kic"><IconRefresh /></span>可回滚</div>
            <p>
              账目记着每格的旧值。哪一次改坏了，按账目逆向执行就能还原——
              这是"过程样板化"的底气：每一步都有据可查。
            </p>
          </div>
          <div className="site-card">
            <div className="k"><span className="kic"><IconLink /></span>改完就通知</div>
            <p>
              改完表 30 秒内，微信推一条：哪张表、改了几处、跳过了几处。
              被拒的改动必须报出来，不装没发生。
            </p>
          </div>
        </div>
      </section>

      {/* 本地优先 + 脑可换 */}
      <section id="legacy" className="site-section site-alt">
        <h2 className="sec-title">数据留在本机，模型可自选</h2>
        <p className="sec-sub">
          手脑分离：你的电脑只当"手"，负责盯表、改表、记账、备份；
          "脑"负责判断（OpenAI 兼容接口，换模型只改一个 baseUrl）。
          发给脑的只有表结构、新数据、账目、规则——表本体不打包上传。
        </p>
        <div className="site-grid cols-3">
          <div className="site-card"><div className="k">桌面客户端</div><p>Windows 安装包，双击安装、有窗口，引擎随包一起装好，不用另外配环境。</p></div>
          <div className="site-card"><div className="k">离线也能用</div><p>看表、体检、联动图、账目都不需要联网；只有"要它判断"时才用到脑。</p></div>
          <div className="site-card"><div className="k">脑是替换件</div><p>默认云端 API；接口统一，想换本地模型只改配置。手永远在本机。</p></div>
        </div>
      </section>

      {/* 下载：按系统版本选构建。
          这个标题原来是「把看表的活，交给 Gridwright」——"活"字太口语，
          像街边吆喝。下载区的标题就该说清下载的是什么。 */}
      <section id="download" className="site-cta-end">
        <h2>下载 Gridwright，开始守你的台账</h2>
        <p>推荐安装版：双击安装，自带窗口与引擎。免安装版为单文件，双击运行后在浏览器中使用。</p>
        <div className="dl-picker" role="group" aria-label="选择下载方式">
          {DOWNLOADS.map((d) => (
            <button
              key={d.os}
              type="button"
              className={`dl-opt ${d.os === os ? "on" : ""}`}
              onClick={() => setOs(d.os)}
              aria-pressed={d.os === os}
            >
              {d.label}
            </button>
          ))}
        </div>
        {/* 下载按钮直接用真实资产的 URL。之前这里包了一层 BUILT 开关
            （未上传时显示"暂未开放"），现在资产随版本一起发，开关本身
            又成了一个发版时会忘记改的地方，索性去掉。 */}
        <a className="btn primary lg dl-btn" href={sel.url} target="_blank" rel="noreferrer">
          下载 {sel.kind} · {sel.file}
        </a>
        <p className="dl-meta">
          {sel.note} · 免安装 · 数据不出本机
          {" · "}<a href={RELEASES} target="_blank" rel="noreferrer">查看全部版本</a>
        </p>
      </section>

      <footer className="site-foot">
        <span>gridwright — 守 Excel 台账的本地助手</span>
        <span>Windows 桌面客户端 · 数据不出本机 · 免费开源</span>
      </footer>
    </div>
  );
}
