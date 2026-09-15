/**
 * 稳定的分类配色：同一个对象，在哪儿都是同一个颜色。
 *
 * 为什么不是"按序号取色"：序号会随排序变化——今天 b50 排第 1 是钢蓝，
 * 明天它掉到第 3 就变陶土，用户的"那个蓝的"记忆就断了。颜色要认对象不认位置。
 *
 * 所以用名字做哈希，映射到 8 个色相之一。同一个铺位/家族名永远同色，
 * 跨列表、跨图、跨会话都不变。
 */

/** 分类色相数量，与 index.css 的 --hue-1..8 一一对应。 */
export const HUE_COUNT = 8;

/** 名字 → 稳定色相序号（0..7）。同一名字恒定。 */
export function hueOf(name: string): number {
  // FNV-1a：短字符串上分布够匀，且实现只有几行
  let h = 0x811c9dc5;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return Math.abs(h) % HUE_COUNT;
}

/** 色相序号 → CSS 变量引用（交给 CSS 决定具体色值，便于统一调色）。 */
export function hueVar(name: string): string {
  return `var(--hue-${hueOf(name) + 1})`;
}

/**
 * 给**一组**名字分配互不撞色的色相，且结果只取决于这组名字本身。
 *
 * 为什么需要它：纯哈希在同一屏 8 行里会撞（实测 b50 与 b43 都落钢蓝），
 * 而"同一屏里两个铺位同色"比"跨会话颜色漂移"更伤——用户就是靠颜色分区看的。
 *
 * 做法：先按名字哈希定一个起点，撞了就在色相环上往后探一格（线性探测）。
 * 因为先按名字排序再分配，**同一组输入永远得到同一组结果**（与列表顺序无关，
 * 所以排序变化不会让颜色跳）。对象数超过色相数时才开始重复——这时无解，
 * 但 8 个以内保证两两不同。
 */
export function assignHues(names: string[]): Map<string, string> {
  const out = new Map<string, string>();
  const used = new Set<number>();
  for (const name of [...names].sort()) {
    let k = hueOf(name);
    for (let i = 0; i < HUE_COUNT && used.has(k); i++) k = (k + 1) % HUE_COUNT;
    used.add(k);
    out.set(name, `var(--hue-${k + 1})`);
  }
  return out;
}

/** 严重度档位：0 平静 / 1 留意 / 2 要查 / 3 坏了。 */
export type Severity = 0 | 1 | 2 | 3;

/**
 * 严重度 → CSS 变量。**与色相正交**：色相说"属谁"，这一档说"多急"。
 * 两套同时上色也不会冲突（图标用色相，右侧数字用严重度）。
 */
export function sevVar(s: Severity): string {
  return `var(--sev-${["calm", "note", "warn", "bad"][s]})`;
}

/** 差额金额 → 严重度：差得越多越暖。阈值按真实台账的分布取（万级起跳）。 */
export function gapSeverity(gap?: number): Severity {
  if (gap === undefined) return 0;
  if (gap >= 1e6) return 3;   // 百万级：坏了
  if (gap >= 1e5) return 2;   // 十万级：要查
  if (gap >= 1e4) return 1;   // 万级：留意
  return 0;
}

/** 出问题次数 → 严重度：反复出问题的对象比偶发的重要。 */
export function countSeverity(count: number): Severity {
  if (count >= 6) return 3;
  if (count >= 4) return 2;
  if (count >= 2) return 1;
  return 0;
}
