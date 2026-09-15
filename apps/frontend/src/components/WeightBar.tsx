/**
 * WeightBar —— 权重条谱的一根条。
 *
 * 画法上有个关键取舍：**每行都画一条底槽**（track），而不是"有值才画条"。
 * 原因是真实台账里绝大多数表权重为 0——只画有值的会让整页变成
 * "两根条 + 一片空白"，看着像坏了。画了底槽之后，空槽本身就成为信息：
 * **"这张表目前没有被别的东西依赖"**，是一个明确的陈述，不是缺数据。
 *
 * 比例用**绝对值**（attention 0~1 直接映射轨道宽度），不做"按最大值归一化"。
 * 归一化会让第一名永远满格、看不出它到底多重要；绝对值下 0.80 就是 80%，
 * 一眼知道"它牵动面很宽"。
 */

export default function WeightBar({ value, master, title }: {
  /** 注意力分 0~1 */
  value: number;
  /** 主数据表：用品牌色实心（唯一用蓝的地方之一） */
  master?: boolean;
  title?: string;
}) {
  const pct = Math.max(0, Math.min(1, value)) * 100;
  const zero = pct <= 0;
  const cls = ["wb", zero ? "zero" : "", master ? "master" : ""].filter(Boolean).join(" ");
  return (
    <span className={cls} title={title}>
      <span className="wb-track">
        <i style={{ width: `${pct}%` }} />
      </span>
      <span className="wb-val">{zero ? "—" : Math.round(pct)}</span>
    </span>
  );
}
