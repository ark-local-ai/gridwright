import type { SheetShape } from "../api-agent";

/**
 * SheetThumb —— 一张工作表的「微缩缩略图」。
 *
 * 为什么用缩略图而不是文字：左边这块要你一眼认出"哪张表"。文字要靠**读**
 * （"2026年9月租金"和"2026年8月租金"读起来几乎一样），而形状靠**看**——
 * 304 行 × 15 列的表和 64 行 × 8 列的表，轮廓完全不同。
 * 这是台账工具里最自然的语汇：表本来就有形状。
 *
 * 画法：把真实行列数**按比例**映射成一小片网格（最多 12×7 格），
 * 网格密=表大、网格疏=表小。不画真实内容（那会糊成一团），只画"骨架密度"。
 */

const MAX_COLS = 12;
const MAX_ROWS = 7;

/** 把真实行列换算成缩略图格子数：按比例，但至少 2×2（否则看不出是一张表）。 */
function grid(r: number, c: number): { rows: number; cols: number } {
  // 用对数压缩：台账里行数是几十到几百，线性映射会让小表全挤成 1 格
  const lr = Math.log10(Math.max(r, 10));
  const lc = Math.log10(Math.max(c, 3));
  const rows = Math.max(2, Math.min(MAX_ROWS, Math.round((lr / Math.log10(1000)) * MAX_ROWS)));
  const cols = Math.max(2, Math.min(MAX_COLS, Math.round((lc / Math.log10(30)) * MAX_COLS)));
  return { rows, cols };
}

export default function SheetThumb({ shape, active, tone }: {
  shape?: SheetShape;
  /** 选中态：钢蓝描边（唯一用蓝的地方） */
  active?: boolean;
  /** 密度色：主数据/高权重表用更实的墨色 */
  tone?: "master" | "normal";
}) {
  // 没有形状数据（还没读到 / 引擎不可用）：画一块空骨架，别留白洞
  const g = shape ? grid(shape.rows, shape.cols) : { rows: 3, cols: 5 };
  const cells: boolean[] = [];
  // 用固定模式填充，保证同一张表每次画得一样（不随机——随机会让界面"抖"）
  for (let i = 0; i < g.rows * g.cols; i++) {
    // 第一行恒为实（表头），其余按稀疏模式
    const row = Math.floor(i / g.cols);
    cells.push(row === 0 || (i * 7) % 11 !== 0);
  }
  const cls = [
    "sh-thumb",
    shape ? "" : "sh-thumb-empty",
    active ? "on" : "",
    tone === "master" ? "master" : "",
  ].filter(Boolean).join(" ");

  return (
    <span
      className={cls}
      style={{ gridTemplateColumns: `repeat(${g.cols}, 1fr)` }}
      aria-hidden="true"
    >
      {cells.map((on, i) => (
        <i key={i} className={on ? "" : "off"} />
      ))}
    </span>
  );
}
