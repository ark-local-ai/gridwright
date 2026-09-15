import type { GraphData, ScanIssue } from "../api-agent";

/**
 * 分析"表之间的连接健康度"——不是画个关系网好看，而是回答一个问题：
 * **哪些表本来该连着，现在断开了？**
 *
 * 这个分析来自真表上的一次实测发现（松茂御龙湾 24 表）：
 *   · 2023 年 3 张月表 引用「商铺台账（详）」→ 113 处 #REF!（台账改过，引用断了）
 *   · 2026 年 10 张月表 **一条跨表引用都没有** → 整年新表是孤岛
 * 也就是说：同一个工作簿里，老表连着但断了一半，新表干脆另起炉灶。
 * **这才是"改一处、下游没跟着改"的根因**，比画个星形图有意义得多。
 *
 * Excel 给不出这个判断：它不告诉你"这张表按理该引用主数据，却没有引用"。
 */

/** 一个"家族"：同一系列的表（如 14 张月租金表）。 */
export type Family = {
  /** 家族名（如"租金（日）"） */
  name: string;
  /** 成员 sheet 名 */
  members: string[];
  /** 其中真正有跨表引用的 */
  linked: string[];
  /** 其中孤立（无任何跨表引用）的 */
  isolated: string[];
};

/** 表名去掉期数后的主干（与左栏系列的算法保持一致）。 */
function familyKey(sheet: string): string {
  return sheet.replace(/\d+/g, "#").replace(/\s+/g, "");
}

/**
 * 按家族分组，并标出"这个家族里谁断了"。
 * 只返回成员 ≥2 的家族——单张表谈不上"家族断裂"。
 */
export function families(graph: GraphData): Family[] {
  // 有跨表引用的表（无论方向，都算"连着"）
  const connected = new Set<string>();
  for (const e of graph.edges) {
    if (e.from.sheet !== e.to.sheet) {
      connected.add(e.from.sheet);
      connected.add(e.to.sheet);
    }
  }
  const byKey = new Map<string, string[]>();
  for (const n of graph.nodes) {
    const k = familyKey(n.sheet);
    if (!byKey.has(k)) byKey.set(k, []);
    byKey.get(k)!.push(n.sheet);
  }
  const out: Family[] = [];
  for (const [, members] of byKey) {
    if (members.length < 2) continue;
    const linked = members.filter((m) => connected.has(m));
    const isolated = members.filter((m) => !connected.has(m));
    out.push({
      name: members[0].replace(/\d+/g, "").replace(/\s+/g, "").replace(/^[年月日]+/, "") || members[0],
      members, linked, isolated,
    });
  }
  // 断裂最多的排前面（那是问题最大的家族）
  return out.sort((a, b) => b.isolated.length - a.isolated.length);
}

/**
 * 断链结论：整本书里"该连而没连"的规模。
 * 用来在界面上给出一句话总结，而不是让用户自己看图。
 */
export type LinkHealth = {
  /** 有跨表引用的表数 */
  connected: number;
  /** 孤岛表数（无任何跨表引用） */
  isolated: number;
  total: number;
  /** 断裂最多的家族（可能为空） */
  worst?: Family;
  /** 一句话结论 */
  verdict: string;
};

export function linkHealth(graph: GraphData): LinkHealth {
  const fams = families(graph);
  const isolatedAll = new Set<string>();
  const connectedAll = new Set<string>();
  for (const e of graph.edges) {
    if (e.from.sheet !== e.to.sheet) {
      connectedAll.add(e.from.sheet);
      connectedAll.add(e.to.sheet);
    }
  }
  for (const n of graph.nodes) {
    if (!connectedAll.has(n.sheet)) isolatedAll.add(n.sheet);
  }
  const worst = fams[0];
  let verdict = "";
  if (worst && worst.isolated.length > 0) {
    const n = worst.isolated.length;
    verdict = `「${worst.name}」这一族 ${worst.members.length} 张表里，有 ${n} 张没有任何跨表引用——改了主数据不会传到它们。`;
  } else if (isolatedAll.size > 0) {
    verdict = `有 ${isolatedAll.size} 张表没有任何跨表引用（孤立表，改了别处不会影响它们，它们也不会自动更新）。`;
  } else {
    verdict = "所有表都连着，改一处能顺着引用传下去。";
  }
  return {
    connected: connectedAll.size,
    isolated: isolatedAll.size,
    total: graph.nodes.length,
    worst,
    verdict,
  };
}

/** 断掉的具体位置：#REF! 落在哪些表、多少个。 */
export type BrokenRef = { sheet: string; count: number; sample: ScanIssue };
export function brokenRefs(issues: ScanIssue[]): BrokenRef[] {
  const by = new Map<string, ScanIssue[]>();
  for (const it of issues) {
    if (!/#REF!/.test(it.message)) continue;
    const k = it.sheet.trim();
    if (!by.has(k)) by.set(k, []);
    by.get(k)!.push(it);
  }
  return [...by.entries()]
    .map(([sheet, list]) => ({ sheet, count: list.length, sample: list[0] }))
    .sort((a, b) => b.count - a.count);
}
