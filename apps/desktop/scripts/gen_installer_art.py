#!/usr/bin/env python3
"""生成 NSIS 安装界面的品牌图（Windows 安装包）。

为什么不直接放两张 PNG 进仓库：颜色是**品牌令牌的副本**。品牌色一改，
手工做的图就悄悄过期了，而且没人会记得重做。这里把令牌写在脚本里、
每次由脚本重画，改色时只需改一处。

产出（尺寸与格式是 NSIS/MUI2 的硬要求，不能改）：
  sidebar.bmp  164 x 314  —— 欢迎页/完成页左侧大图
  header.bmp   150 x 57   —— 安装过程各内页右上角小图
两者都必须是 BMP：MUI_WELCOMEFINISHPAGE_BITMAP / MUI_HEADERIMAGE_BITMAP
只吃 BMP，给 JPG 会静默降级成默认图（makensis 只报 warning 5040，不报错，
安装包照样出——所以这个坑只能靠看构建日志发现）。

用深底还是浅底：sidebar 是整幅侧栏，用品牌渐变（深）——
安装向导里它是唯一一块"品牌面"，浅底会淹没在白色向导里。
header 只有 150x57 且贴着标题区，必须浅底，否则像贴了块补丁。

用法：python apps/desktop/scripts/gen_installer_art.py
"""
from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

# ---------- 品牌令牌（与 apps/frontend/src/index.css 保持一致） ----------
BRAND = (47, 93, 138)        # --brand        #2f5d8a 钢蓝
BRAND_DEEP = (24, 58, 92)    # 渐变的深端（比 --brand-strong 再深一档，压得住）
INK = (20, 24, 33)           # --text-1
TEXT_2 = (90, 100, 114)      # --text-2
SURFACE = (255, 255, 255)

OUT = Path(__file__).resolve().parent.parent / "src-tauri" / "installer"

# Segoe UI：Windows 原生界面字体，装包界面用它才不会"外语感"
FONT_SEMI = "C:/Windows/Fonts/seguisb.ttf"   # Segoe UI Semibold
FONT_REG = "C:/Windows/Fonts/segoeui.ttf"
FONT_CN = "C:/Windows/Fonts/msyh.ttc"        # 微软雅黑（中文标签用）


def _font(path: str, size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(path, size)


def _vgradient(size: tuple[int, int], top: tuple, bottom: tuple) -> Image.Image:
    """竖直渐变。逐行画——314 行，开销可忽略，换来无依赖。"""
    w, h = size
    img = Image.new("RGB", size)
    d = ImageDraw.Draw(img)
    for y in range(h):
        t = y / max(h - 1, 1)
        c = tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3))
        d.line([(0, y), (w, y)], fill=c)
    return img


def _grid_overlay(img: Image.Image, step: int = 22, alpha: int = 16) -> None:
    """淡淡的网格线——呼应产品名里的 grid（网格），也给纯渐变换来一点结构。"""
    w, h = img.size
    ov = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(ov)
    line = (255, 255, 255, alpha)
    for x in range(step, w, step):
        d.line([(x, 0), (x, h)], fill=line, width=1)
    for y in range(step, h, step):
        d.line([(0, y), (w, y)], fill=line, width=1)
    img.paste(Image.alpha_composite(img.convert("RGBA"), ov).convert("RGB"), (0, 0))


def _row_motif(img: Image.Image, top: int, rows: int = 7, row_h: int = 15) -> None:
    """淡的"表格行"纹样：一列短横线，像一张被看管的表。

    选这个纹样而不是抽象网格：产品就是"盯表"的，行与列是它的本行语言。
    线要极淡（alpha ~26）——安装界面是配角，纹样只该提供质感，不该被读到。
    最右一条稍长、稍亮，暗示"盯到的那一行"，给静态图一个焦点。
    """
    w, _ = img.size
    ov = Image.new("RGBA", img.size, (0, 0, 0, 0))
    d = ImageDraw.Draw(ov)
    for i in range(rows):
        y = top + i * row_h
        # 每条行线长度不同，像真实表格里参差的单元格
        long_tail = i == rows - 2
        end = w - 16 if long_tail else w - 16 - (18 if i % 3 else 34)
        alpha = 60 if long_tail else 30
        d.line([(16, y), (end, y)], fill=(255, 255, 255, alpha), width=1)
    img.paste(Image.alpha_composite(img.convert("RGBA"), ov).convert("RGB"), (0, 0))


def sidebar(path: Path) -> None:
    """欢迎页/完成页左侧大图：品牌渐变 + 表格行纹样 + 文字字标。"""
    W, H = 164, 314
    img = _vgradient((W, H), BRAND, BRAND_DEEP)
    # 行纹样放在上 2/3（下 1/3 留给字标，避免压字）
    _row_motif(img, top=54, rows=7, row_h=16)
    d = ImageDraw.Draw(img)

    # 底部压暗：字压在上面才读得清（渐变的浅端在上，字在下）
    shade = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    sd = ImageDraw.Draw(shade)
    for y in range(H // 2, H):
        t = (y - H // 2) / max(H - H // 2 - 1, 1)
        sd.line([(0, y), (W, y)], fill=(8, 22, 38, round(80 * t)))
    img.paste(Image.alpha_composite(img.convert("RGBA"), shade).convert("RGB"), (0, 0))

    # 字标：与界面里的 GridwrightLogo 同一套做法（小写、负字距、粗）
    f_logo = _font(FONT_SEMI, 22)
    d.text((16, H - 74), "gridwright", font=f_logo, fill=(255, 255, 255))

    # 一句话：说清它是什么（安装界面上第一次见到它的人要能懂）
    f_cn = _font(FONT_CN, 11)
    d.text((16, H - 44), "跑在你机器上的数据管家", font=f_cn, fill=(214, 228, 242))

    img.save(path, "BMP")  # MUI_WELCOMEFINISHPAGE_BITMAP 只接受 BMP


def header(path: Path) -> None:
    """内页右上角小图：浅底 + 右侧字标（左侧留给 MUI 的标题文字）。"""
    W, H = 150, 57
    img = Image.new("RGB", (W, H), SURFACE)
    d = ImageDraw.Draw(img)

    # 只放字标，右对齐——它挨着向导标题区，多一个图形就会抢
    f_logo = _font(FONT_SEMI, 19)
    text = "gridwright"
    tw = d.textlength(text, font=f_logo)
    d.text((W - tw - 14, H / 2 - 11), text, font=f_logo, fill=BRAND)

    # 左侧一道品牌短线：给浅底一点"这是同一套品牌"的锚
    d.line([(14, H / 2 - 7), (14, H / 2 + 7)], fill=BRAND, width=2)

    img.save(path, "BMP")  # NSIS 的 MUI_HEADERIMAGE_BITMAP 要 BMP


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    sidebar(OUT / "sidebar.bmp")
    header(OUT / "header.bmp")
    for f in sorted(OUT.iterdir()):
        if f.is_file():
            print(f"{f.name:12} {f.stat().st_size:>7} bytes")


if __name__ == "__main__":
    main()
