import type { ScanIssue } from "../api-agent";

/**
 * 把体检结果按**业务对象**聚合，而不是按"第几条"平铺。
 *
 * 为什么必须聚合：真实台账跑出 267 个问题，其中 71 个是"账对不上"，
 * 散在 13 张月表里。如果按行列出来，用户看到的是 71 条几乎一样的话；
 * 而按**铺位**聚合后，同一件事立刻清楚：
 *   b50 对不上 9 次 ← 惯犯    2025年11月一个月 22 处 ← 那个月有问题
 *
 * 这就是 Excel 做不到、而工具该做的事：**跨表追踪同一个对象**。
 * 单表汇总 Excel 一个透视表就拉了，不需要我们重算。
 */

/** 一个"反复出问题的对象"（铺位/商户）。 */
export type Hotspot = {
  /** 对象名（如 b50） */
  name: string;
  /** 出问题次数 */
  count: number;
  /** 涉及哪些月表 */
  sheets: string[];
  /** 差多少钱（能算出来才有） */
  gap?: number;
  /** 代表性问题（给人看一句话） */
  sample: ScanIssue;
  /** 该对象的全部问题 */
  issues: ScanIssue[];
};

/** 从问题描述里抠出"铺位 xxx"。抠不出就归到"（未标注铺位）"。 */
function shopOf(it: ScanIssue): string {
  const m = /铺位\s*([^\s的]+)/.exec(it.message);
  return m ? m[1] : "";
}

/** 从"上期欠款 A 与上月本月欠款 B 不符"里算出差额。 */
function gapOf(it: ScanIssue): number | undefined {
  const m = /上期欠款\s*([\d,.+-]+)\s*与上月本月欠款\s*([\d,.+-]+)\s*不符/.exec(it.message);
  if (!m) return undefined;
  const a = Number(m[1].replace(/,/g, ""));
  const b = Number(m[2].replace(/,/g, ""));
  if (Number.isNaN(a) || Number.isNaN(b)) return undefined;
  return Math.abs(b - a);
}

/** 金额压缩成"万/亿"，便于一眼读（原文进 tooltip）。 */
export function shortMoney(n: number): string {
  const abs = Math.abs(n);
  if (abs >= 1e8) return (n / 1e8).toFixed(2) + "亿";
  if (abs >= 1e4) return (n / 1e4).toFixed(1) + "万";
  return n.toFixed(2);
}

/**
 * 按铺位聚合成热点，按"出问题次数"降序。
 * 次数多的排前面——**反复出问题的铺位比偶发的重要得多**。
 */
export function hotspots(issues: ScanIssue[]): Hotspot[] {
  const byShop = new Map<string, ScanIssue[]>();
  for (const it of issues) {
    const k = shopOf(it) || "（未标注）";
    if (!byShop.has(k)) byShop.set(k, []);
    byShop.get(k)!.push(it);
  }
  const out: Hotspot[] = [];
  for (const [name, list] of byShop) {
    const gaps = list.map(gapOf).filter((n): n is number => n !== undefined);
    out.push({
      name,
      count: list.length,
      sheets: [...new Set(list.map((i) => i.sheet.trim()))],
      // 同一铺位多次差额取最大：先看最要命的那个数
      gap: gaps.length ? Math.max(...gaps) : undefined,
      sample: list[0],
      issues: list,
    });
  }
  return out.sort((a, b) => b.count - a.count);
}

/** 按月份/表聚合：哪张表问题最多（"那个月有问题"）。 */
export type SheetLoad = { sheet: string; count: number };
export function bySheet(issues: ScanIssue[], top = 6): SheetLoad[] {
  const m = new Map<string, number>();
  for (const it of issues) {
    const k = it.sheet.trim();
    m.set(k, (m.get(k) ?? 0) + 1);
  }
  return [...m.entries()]
    .map(([sheet, count]) => ({ sheet, count }))
    .sort((a, b) => b.count - a.count)
    .slice(0, top);
}
