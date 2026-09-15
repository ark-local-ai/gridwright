"""生成 README 用的工程图（SVG）。

为什么是 SVG 不是截图/PNG：
  - 任何缩放下都清晰（README 在 GitHub 上会被缩放）
  - 体积小、可 diff、文字可被搜索到（PNG 里的字搜不到）
  - 版式与配色写成代码 = 品牌色改了重跑即可，手画的图会悄悄过期

画三张，各回答一个问题：
  why.svg          它解决什么问题（问题 → 数据流 → 结果）
  pipeline.svg     一次改表怎么走（确认制闭环，模型只在必要时出现）
  architecture.svg 它由什么组成（引擎 / 两种外壳 / 工作区）

用法：python apps/frontend/scripts/gen_diagrams.py
输出：assets/diagrams/*.svg  (README 引用，随仓库提交)

**不要放到 docs/**：docs/ 在 .gitignore 里（内部文档不推仓库），
放那里的话 README 的图片链接在 GitHub 上会全部 404 —— 本地却看得到，
所以这个错只会在别人打开仓库时才暴露。
"""
from __future__ import annotations

from pathlib import Path

# ---- 品牌令牌（与 apps/frontend/src/index.css 一致） ----
BRAND = "#2f5d8a"
BRAND_SOFT = "#eef4fa"
BRAND_LINE = "#d2e0ee"
INK = "#141821"
TEXT2 = "#5a6472"
TEXT3 = "#5f6a7a"
LINE = "#e9edf2"
LINE_STRONG = "#d3dae4"
SURFACE = "#ffffff"
BG = "#fbfcfd"
SIDEBAR = "#f4f6f9"
OK = "#21805f"
OK_BG = "#e9f5f0"
WARN = "#9a5f0d"
WARN_BG = "#fbf2e2"
DANGER = "#b73f39"
DANGER_BG = "#fceeed"
HUE = ["#2f5d8a", "#21805f", "#a55a2b", "#6b4a7a", "#0d7f8b", "#64748b", "#6b7a2b", "#a34d63"]

FONT = "'Segoe UI','PingFang SC','Microsoft YaHei',system-ui,sans-serif"
MONO = "'Cascadia Mono','Consolas','SF Mono',monospace"

OUT = Path(__file__).resolve().parents[3] / "assets" / "diagrams"


def esc(s: str) -> str:
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def svg_open(w: int, h: int) -> list[str]:
    return [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" '
        f'viewBox="0 0 {w} {h}" font-family="{FONT}">',
        f'<rect width="{w}" height="{h}" fill="{BG}"/>',
    ]


def text(x, y, s, size=13, fill=INK, weight=400, anchor="start", mono=False, ls=0):
    return (
        f'<text x="{x}" y="{y}" font-size="{size}" fill="{fill}" font-weight="{weight}" '
        f'text-anchor="{anchor}" font-family="{MONO if mono else FONT}"'
        + (f' letter-spacing="{ls}"' if ls else "")
        + f'>{esc(s)}</text>'
    )


def box(x, y, w, h, fill=SURFACE, stroke=LINE, r=10, sw=1, dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (
        f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" '
        f'stroke="{stroke}" stroke-width="{sw}"{d}/>'
    )


def arrow(x1, y1, x2, y2, color=LINE_STRONG, sw=1.5, dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (
        f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{color}" '
        f'stroke-width="{sw}" marker-end="url(#ah)"{d}/>'
    )


def arrow_poly(points, color=LINE_STRONG, sw=1.5) -> str:
    """折线箭头：points 是 [(x,y), ...]，最后一段的末端带箭头。

    比一连串直线好在：折角干净、且落点明确。用心算两段直线去拼折返，
    很容易画出一条悬空、看不出连去哪的箭头（第一版就是这个毛病）。
    """
    pts = " ".join(f"{x},{y}" for x, y in points)
    return (
        f'<polyline points="{pts}" fill="none" stroke="{color}" '
        f'stroke-width="{sw}" stroke-linejoin="round" marker-end="url(#ah)"/>'
    )


def defs(accent=BRAND) -> str:
    return (
        "<defs>"
        f'<marker id="ah" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" '
        f'markerHeight="7" orient="auto-start-reverse">'
        f'<path d="M0,0 L10,5 L0,10 z" fill="{accent}"/></marker>'
        "</defs>"
    )


def fig_why() -> str:
    """图一：为什么需要它 —— 一张表改了，下游未必跟着改。"""
    W, H = 960, 460
    s = svg_open(W, H)
    s.append(defs(DANGER))
    s.append(text(40, 46, "问题：改一处，下游没跟着改 —— Excel 不会告诉你", 17, INK, 600))

    # 左：主数据 + 引用它的表族（连着的 / 断开的）
    s.append(text(40, 84, "① 实际发生的事", 12, TEXT3, 600, ls="0.08em"))
    s.append(box(40, 100, 190, 54, BRAND_SOFT, BRAND_LINE))
    s.append(text(56, 122, "商铺台账（详）", 13, BRAND, 600))
    s.append(text(56, 140, "主数据 · 被编辑过", 11, TEXT2))

    # 连着的 3 张
    for i in range(3):
        y = 100 + i * 42
        s.append(box(300, y, 150, 32, SURFACE, LINE))
        s.append(text(312, y + 21, f"2023年{i+1}月", 11.5, TEXT2))
        s.append(arrow(230, 127, 298, y + 16, OK, 1.4))
    # 断开的 3 张（示意 9 张）
    for i in range(3):
        y = 100 + i * 42
        s.append(box(520, y, 150, 32, DANGER_BG, "#f0bdb9"))
        s.append(text(532, y + 21, f"2026年{i+1}月", 11.5, DANGER))
    s.append(arrow(230, 127, 518, 116, DANGER, 1.4, dash="4 4"))
    s.append(text(300, 262, "只有 5/14 张引用主数据", 11.5, OK, 500))
    s.append(text(520, 262, "9 张是孤岛（近一年新表）→ 113 处 #REF!", 11.5, DANGER, 500))

    # 右：后果
    s.append(text(40, 306, "② 后果", 12, TEXT3, 600, ls="0.08em"))
    facts = [
        ("71 处账对不上", "散在 13 张表里，按行看是 71 条一样的话", DANGER),
        ("改成按铺位看", "b50 对不上 9 次 ← 惯犯；2025年11月 22 处 ← 那个月有问题", BRAND),
        ("这一层 Excel 给不出", "透视表只汇总单表，说不出「该引用却没引用」", WARN),
    ]
    for i, (t1, t2, c) in enumerate(facts):
        y = 322 + i * 42
        s.append(box(40, y, 880, 34, SURFACE, LINE, r=8))
        s.append(box(40, y, 3, 34, c, c, r=2))
        s.append(text(58, y + 22, t1, 12.5, c, 600))
        s.append(text(230, y + 22, t2, 12, TEXT2))
    s.append("</svg>")
    return "\n".join(s)


def fig_pipeline() -> str:
    """图二：一次改表怎么走 —— 确认制闭环；模型只在"听懂"这一步出现。"""
    W, H = 1000, 430
    s = svg_open(W, H)
    s.append(defs(BRAND))
    s.append(text(40, 44, "一次改表：规则能定的短路，定不了的才问模型；你确认才写", 16, INK, 600))

    # 顶部两条入口
    s.append(box(40, 66, 440, 74, SURFACE, LINE))
    s.append(text(58, 90, "规则命中 → 直接算，不花 token", 12.5, OK, 600))
    s.append(text(58, 109, "rules.yaml 里 when/then 齐全的规则", 11, TEXT2))
    s.append(text(58, 126, "可复现 · 离线可用 · 毫秒级", 10.5, TEXT3))

    s.append(box(520, 66, 440, 74, SURFACE, LINE))
    s.append(text(538, 90, "规则定不了 → 问模型（只做这一件事）", 12.5, BRAND, 600))
    s.append(text(538, 109, "「听懂你要改什么」→ 结构化编辑指令", 11, TEXT2))
    s.append(text(538, 126, "算、定位、影响面一律不由模型决定", 10.5, TEXT3))

    # 主干：两行四列（一行八个会挤到看不清）
    stages = [
        ("读懂输入", "inbox / 一句话"),
        ("定位到格", "语义坐标 → 具体格子"),
        ("算出清单", "改哪格 · 旧值→新值"),
        ("影响面", "会牵动哪些表"),
        ("你确认", "确认才写"),
        ("备份 + 写回", "改动前先备份"),
        ("记账", "每格一行，含旧值"),
        ("自检", "只查相关表"),
    ]
    bw, gap, bh = 232, 16, 62
    x0, y0 = 40, 168
    for i, (t1, t2) in enumerate(stages):
        col, row = i % 4, i // 4
        x = x0 + col * (bw + gap)
        y = y0 + row * 100
        conf = t1 == "你确认"
        s.append(box(x, y, bw, bh, WARN_BG if conf else SURFACE,
                     WARN if conf else LINE, r=8, sw=1.5 if conf else 1))
        s.append(text(x + 16, y + 25, t1, 12.5, WARN if conf else INK, 600))
        s.append(text(x + 16, y + 45, t2, 10.5, TEXT3))
        if col < 3:
            s.append(arrow(x + bw + 1, y + bh / 2, x + bw + gap - 1, y + bh / 2, BRAND, 1.3))
    # 第一行末尾折到第二行开头：走"下 → 左 → 下 → 右"的直角折线，
    # 从左边缘进入第二行第一格。这样"第二行从这儿开始"是明确的；
    # 若直接从底部进入，读者会以为流程是从下面往上走的。
    a_x = x0 + 3 * (bw + gap) + bw / 2          # 影响面 底边中点
    rail_y = y0 + bh + 22                        # 两行之间的水平通道
    row2_cy = y0 + 100 + bh / 2                  # 第二行竖直中心
    rail_x = x0 - 18                             # 左侧折返通道（在页边距内）
    s.append(arrow_poly(
        [(a_x, y0 + bh), (a_x, rail_y), (rail_x, rail_y),
         (rail_x, row2_cy), (x0 - 1, row2_cy)],
        BRAND, 1.3,
    ))

    # 回滚注释（放在下方独立一行，不与任何框重叠）
    s.append(text(40, y0 + 200, "回滚：账目里存着旧值，随时倒回去；回滚本身也记账，所以能再倒回来（=重做）",
                  11.5, TEXT2))
    s.append(text(40, y0 + 222, "护栏：含宏的表硬拒绝（写回会丢宏）；动到禁止列的那一条被拦下并说明原因",
                  11.5, TEXT2))
    s.append("</svg>")
    return "\n".join(s)


def fig_architecture() -> str:
    """图三：它由什么组成 —— 一份引擎 + 两种外壳，界面只是引擎的视图。"""
    W, H = 980, 490
    s = svg_open(W, H)
    s.append(defs(BRAND))
    s.append(text(40, 44, "组成：一份 API + 一个 Go 引擎 + 两种外壳（界面只是引擎的视图）", 16, INK, 600))

    # 外壳层
    s.append(text(40, 78, "外壳", 12, TEXT3, 600, ls="0.08em"))
    s.append(box(100, 66, 300, 46, SURFACE, LINE))
    s.append(text(116, 86, "桌面客户端（Tauri 2）", 12.5, INK, 600))
    s.append(text(116, 103, "有窗口 · 引擎随包 · 双击即用", 11, TEXT2))
    s.append(box(430, 66, 250, 46, SURFACE, LINE))
    s.append(text(446, 86, "网页（官网 only）", 12.5, INK, 600))
    s.append(text(446, 103, "落地页 + 下载入口", 11, TEXT2))
    s.append(box(710, 66, 230, 46, SURFACE, LINE))
    s.append(text(726, 86, "免安装单文件", 12.5, INK, 600))
    s.append(text(726, 103, "界面已内嵌 exe", 11, TEXT2))

    # 引擎
    s.append(text(40, 152, "引擎（唯一业务实现）", 12, TEXT3, 600, ls="0.08em"))
    s.append(box(100, 140, 840, 200, SIDEBAR, LINE_STRONG, r=12))
    s.append(text(118, 166, "Go 引擎 · 本地 HTTP API 127.0.0.1:7700", 12.5, BRAND, 600))

    mods = [
        ("graph / locate", "表结构图\n语义坐标定位", HUE[0]),
        ("scan", "只读体检\n跨表一致性", HUE[1]),
        ("rules", "规则引擎\n命中短路", HUE[2]),
        ("propose / rollback", "确认制改表\n可回滚", HUE[3]),
        ("ledger", "账目\n每格含旧值", HUE[4]),
        ("safety", "含宏=硬拒绝\n不毁表", HUE[5]),
        ("impact / selfcheck", "影响面\n改动后自检", HUE[6]),
        ("memory / weight", "长期记忆\n表权重", HUE[7]),
    ]
    for i, (name, desc, hue) in enumerate(mods):
        col = i % 4
        row = i // 4
        x = 118 + col * 208
        y = 182 + row * 78
        s.append(box(x, y, 192, 66, SURFACE, LINE, r=8))
        s.append(box(x, y, 3, 66, hue, hue, r=2))
        s.append(text(x + 14, y + 24, name, 11.5, hue, 600, mono=True))
        for j, ln in enumerate(desc.split("\n")):
            s.append(text(x + 14, y + 42 + j * 14, ln, 10.5, TEXT2))

    # 工作区
    s.append(text(40, 378, "工作区 = 一个文件夹（数据都在这里，不出本机）", 12, TEXT3, 600, ls="0.08em"))
    files = [("*.xlsx", "被看管的表"), ("inbox/", "新数据 → done/"), ("rules.yaml", "规矩"),
             ("ledger.csv", "账目"), ("生成/", "草稿，不动原表")]
    for i, (f, d) in enumerate(files):
        x = 100 + i * 170
        s.append(box(x, 394, 156, 46, SURFACE, LINE, r=8))
        s.append(text(x + 12, 414, f, 11.5, INK, 600, mono=True))
        s.append(text(x + 12, 430, d, 10.5, TEXT2))
    s.append("</svg>")
    return "\n".join(s)


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    for name, fn in [("why", fig_why), ("pipeline", fig_pipeline), ("architecture", fig_architecture)]:
        p = OUT / f"{name}.svg"
        p.write_text(fn(), encoding="utf-8")
        print(f"{p.name:18} {p.stat().st_size:>6} bytes")


if __name__ == "__main__":
    main()
