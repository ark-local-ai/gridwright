import type { SheetSummary } from "../api-agent";

/**
 * 从"标签/值交替"的汇总行里解出可用的 KPI。
 *
 * 表里的汇总行长这样：
 *   ["本月收租率","8.34%","本月应收","10,342,447.67","本月实收","862,156.52",
 *    "未收齐租金商铺数量","35.00","备注"]
 * 也就是 **label, value, label, value, …**，末尾可能挂一个没有值的标签（"备注"）。
 * 这是表自己写的格式，我们只忠实解析，不改写、不推算。
 */
export type Metric = {
  key: string;
  label: string;
  /** 原文（保留千分位，显示用） */
  raw: string;
  /** 数值（能解析出来才有） */
  num?: number;
  /** 百分比（"8.34%" 这类） */
  pct?: number;
  unit?: string;
};

const PCT = /^(-?[\d,.]+)\s*%$/;
const NUM = /^-?[\d,]+(\.\d+)?$/;

/** 把一条汇总行解析成指标数组。 */
export function parseMetrics(sums?: SheetSummary[]): Metric[] {
  if (!sums || sums.length === 0) return [];
  const out: Metric[] = [];
  for (const s of sums) {
    const v = (s.values ?? []).map((x) => String(x).trim()).filter(Boolean);
    for (let i = 0; i < v.length; i += 2) {
      const label = v[i];
      const raw = v[i + 1];
      if (raw === undefined) break; // 末尾孤立标签（如"备注"）没有值
      // 注意：**不要**给"第一个标签"开特例。它看起来像"整行标题"
      // （值与 s.label 相同），但其实就是一个普通的 label/value 对——
      // 之前给它特例并 continue，导致收租率这个最关键的指标从未被解析，
      // 首页永远显示 "—%"。教训：表头标签和普通指标同形，别区别对待。
      const m: Metric = { key: slug(label), label, raw };
      const p = PCT.exec(raw);
      if (p) {
        m.pct = Number(p[1].replace(/,/g, ""));
        m.num = m.pct;
        m.unit = "%";
      } else if (NUM.test(raw)) {
        m.num = Number(raw.replace(/,/g, ""));
      }
      out.push(m);
    }
  }
  return out;
}

/** 宽松匹配指标：按标签里的关键词找（表头措辞可能略有差别）。 */
export function findMetric(ms: Metric[], ...keys: string[]): Metric | undefined {
  for (const k of keys) {
    const hit = ms.find((m) => m.label.includes(k));
    if (hit) return hit;
  }
  return undefined;
}

function slug(s: string): string {
  return s.replace(/\s+/g, "").toLowerCase();
}

/**
 * 收租率的"健康"判定 —— 决定配色。
 * 阈值是业务判断，写在一处便于以后调：≥90% 好、≥60% 注意、其余偏低。
 */
export type Tone = "good" | "warn" | "bad" | "none";
export function rateTone(pct?: number): Tone {
  if (pct === undefined || Number.isNaN(pct)) return "none";
  if (pct >= 90) return "good";
  if (pct >= 60) return "warn";
  return "bad";
}

/** 金额显示：保留原文（带千分位），过长的做万/亿压缩，便于一眼读。 */
export function money(raw: string): { text: string; full: string } {
  const n = Number(raw.replace(/[, ]/g, ""));
  if (Number.isNaN(n)) return { text: raw, full: raw };
  const abs = Math.abs(n);
  if (abs >= 1e8) return { text: (n / 1e8).toFixed(2) + " 亿", full: raw };
  if (abs >= 1e4) return { text: (n / 1e4).toFixed(1) + " 万", full: raw };
  return { text: raw, full: raw };
}
