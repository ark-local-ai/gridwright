"""生成 gridwright 的应用图标（桌面快捷方式 / 任务栏 / 窗口 / favicon）。

概念取自产品名：**grid（表格）+ wright（匠人）**。
一个有 4 格的表格，其中**一格被"写进去"了** —— 这就是产品做的事：
盯着表，按规矩往里写，且每次写入都留下痕迹。

为什么用脚本画而不是找 Designer / 下载素材：
  颜色是品牌令牌的副本（--brand #2f5d8a）。手做的图在下次调色时会悄悄过期，
  而没人会记得重做图标。脚本重跑一次就跟上。

为什么"一个实心格"是关键：
  图标要能在 16x16 下被认出来。满格的网格在任务栏里就是一块糊掉的方块；
  留三空一实，缩到 16px 仍有明暗对比可辨认。

用法：python apps/desktop/scripts/gen_app_icon.py
产出：apps/desktop/src-tauri/icons/*.png + icon.ico
      apps/frontend/public/favicon.svg + favicon.ico
"""
from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw

# ---- 品牌令牌（与 apps/frontend/src/index.css 一致） ----
BRAND = (47, 93, 138)        # --brand #2f5d8a
BRAND_DEEP = (30, 64, 99)    # 渐变深端
BRAND_LIGHT = (94, 143, 190)  # 渐变浅端（给"空"格一点可见度）
WHITE = (255, 255, 255)

ROOT = Path(__file__).resolve().parents[3]
ICONS = ROOT / "apps" / "desktop" / "src-tauri" / "icons"
PUBLIC = ROOT / "apps" / "frontend" / "public"

# 渲染一个较大的母版再缩小：小尺寸下的抗锯齿更干净
MASTER = 1024


def _rounded_gradient(size: int) -> Image.Image:
    """竖直渐变底 + 圆角。圆角半径按 macOS/Windows 现代图标的观感取 ~22%。"""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    grad = Image.new("RGB", (1, size))
    gd = ImageDraw.Draw(grad)
    for y in range(size):
        t = y / max(size - 1, 1)
        c = tuple(round(BRAND[i] + (BRAND_DEEP[i] - BRAND[i]) * t) for i in range(3))
        gd.point((0, y), fill=c)
    grad = grad.resize((size, size))
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, size - 1, size - 1], radius=int(size * 0.22), fill=255)
    img.paste(grad, (0, 0), mask)
    return img


def _cell_rect(size: int, row: int, col: int, cx: float, cy: float, gap: float) -> list[float]:
    """第 (row,col) 格的方框坐标。cx/cy=格子边长占比，gap=格间距占比。"""
    cw = size * cx
    total = cw * 2 + size * gap
    x0 = (size - total) / 2 + col * (cw + size * gap)
    y0 = (size - total) / 2 + row * (cw + size * gap)
    return [x0, y0, x0 + cw, y0 + cw]


def master_icon(size: int = MASTER) -> Image.Image:
    """母版：圆角渐变底 + 2x2 网格，左上为实心（"被写进去的那一格"）。

    实心格不用纯白、用近白：纯白在深底上太跳，近白更像"纸上的字"。
    三个空格用半透明白描边 —— 缩到小尺寸时，描边会比填充更早消失，
    所以填充必须是实打实的对比，这也是选"一实三空"的原因。

    为什么**没有**右下角那个小缺口：画过一版带缺口的，结果整个图形
    读成"键盘退格键"（一个实心块 + 一个角标）。去掉之后才像一张表。
    """
    img = _rounded_gradient(size)
    d = ImageDraw.Draw(img, "RGBA")

    gap = 0.055
    cw = 0.30
    r = int(size * 0.045)
    lw = max(2, int(size * 0.022))

    # 空格：半透明白描边
    for (row, col) in [(0, 1), (1, 0), (1, 1)]:
        box = _cell_rect(size, row, col, cw, cw, gap)
        d.rounded_rectangle(box, radius=r, outline=(255, 255, 255, 105), width=lw)

    # 实心格：近白填充（这一格 = 写进去的那一笔）
    box = _cell_rect(size, 0, 0, cw, cw, gap)
    d.rounded_rectangle(box, radius=r, fill=(246, 250, 253, 255))

    return img


def favicon_svg(size: int = 64) -> str:
    """浏览器标签页图标（SVG，可缩放）。与 app 图标同一套图形。"""
    def rect(row, col, fill, stroke, sw):
        cw, gap = 0.30, 0.055
        total = cw * 2 + gap
        x = (1 - total) / 2 + col * (cw + gap)
        y = (1 - total) / 2 + row * (cw + gap)
        return (f'<rect x="{x:.4f}" y="{y:.4f}" width="{cw:.4f}" height="{cw:.4f}" '
                f'rx="0.045" fill="{fill}" stroke="{stroke}" stroke-width="{sw}"/>')
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#2f5d8a"/>
      <stop offset="1" stop-color="#1e4063"/>
    </linearGradient>
  </defs>
  <rect width="1" height="1" rx="0.22" fill="url(#g)"/>
  {rect(0, 1, "none", "rgba(255,255,255,0.41)", 0.022)}
  {rect(1, 0, "none", "rgba(255,255,255,0.41)", 0.022)}
  {rect(1, 1, "none", "rgba(255,255,255,0.41)", 0.022)}
  {rect(0, 0, "#f6fafd", "none", 0)}
</svg>
'''


def main() -> None:
    m = master_icon()

    # Tauri 需要的尺寸（tauri.conf.json 里列的那几个必须存在）
    sizes = {
        "32x32.png": 32,
        "64x64.png": 64,
        "128x128.png": 128,
        "128x128@2x.png": 256,
        "icon.png": 512,
    }
    for name, px in sizes.items():
        m.resize((px, px), Image.LANCZOS).save(ICONS / name, "PNG")

    # Windows 用 .ico（多尺寸打包，任务栏/资源管理器各取所需）
    ico_sizes = [(16, 16), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    m.save(ICONS / "icon.ico", format="ICO", sizes=ico_sizes)

    # Windows 磁贴用的方图（Tauri 模板里有这些名字，一并画上以免留着默认图）
    for px in (30, 44, 71, 89, 107, 142, 150, 284, 310):
        sq = m.resize((px, px), Image.LANCZOS)
        (ICONS / f"Square{px}x{px}Logo.png").parent.mkdir(parents=True, exist_ok=True)
        sq.save(ICONS / f"Square{px}x{px}Logo.png", "PNG")

    # 浏览器 favicon：SVG（缩放清晰）+ ICO（老浏览器回退）
    PUBLIC.mkdir(parents=True, exist_ok=True)
    (PUBLIC / "favicon.svg").write_text(favicon_svg(), encoding="utf-8")
    m.resize((64, 64), Image.LANCZOS).save(PUBLIC / "favicon.ico", format="ICO",
                                          sizes=[(16, 16), (32, 32), (48, 48), (64, 64)])

    print("app icons ->", ICONS)
    for name in ["32x32.png", "128x128.png", "icon.ico"]:
        p = ICONS / name
        print(f"  {name:16} {p.stat().st_size:>7} bytes")
    print("favicon ->", PUBLIC / "favicon.svg", (PUBLIC / "favicon.svg").stat().st_size, "bytes")


if __name__ == "__main__":
    main()
