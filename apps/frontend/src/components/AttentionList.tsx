import { useMemo, useState } from "react";
import type { ScanIssue } from "../api-agent";
import { hotspots, bySheet, shortMoney } from "../lib/issues";
import { assignHues, gapSeverity, sevVar } from "../lib/hues";
import { IconShield, IconChart } from "./icons";

/**
 * AttentionList —— 「该查什么」的清单。
 *
 * 取代了原来的"收租率大数字"。理由：收租率、应收、实收这些是**单表汇总**，
 * Excel 一个透视表就拉了，工具替用户重算一遍没有增量价值。真正需要工具的
 * 是**跨表追踪**：
 *   · 同一个铺位的账，在 13 张月表里对不上（人要翻 13 次）
 *   · 哪个铺位反复错（9 次 vs 1 次，Excel 排不出来）
 *   · 哪个月集中出错（"那个月有问题"）
 *
 * 所以这一栏展示的是**结论**，不是数据本身：
 *   铺位 b50 · 对不上 9 次 · 差额 562万  ← 先查这个
 * 点开才看细节，细节里带可点击的坐标（跳到表里那一格）。
 */
export default function AttentionList({ issues, onOpen }: {
  issues: ScanIssue[];
  onOpen: (it: ScanIssue) => void;
}) {
  const [openName, setOpenName] = useState<string | null>(null);
  const mism = useMemo(() => issues.filter((i) => i.kind === "mismatch"), [issues]);
  const errs = useMemo(() => issues.filter((i) => i.kind !== "mismatch"), [issues]);
  const spots = useMemo(() => hotspots(mism), [mism]);
  const load = useMemo(() => bySheet(mism), [mism]);
  const errKinds = useMemo(() => {
    const m = new Map<string, number>();
    for (const i of errs) m.set(i.kind, (m.get(i.kind) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [errs]);
  // 同屏 8 行必须两两不同色，否则"看颜色分区"不成立（见 assignHues 注释）。
  // 只在**实际渲染的那 8 个**里分配：铺位可能多于 8 个，把没显示的分进色相池
  // 只会挤掉可见行的颜色（8 色 11 对象必撞）。这里 8 个以内保证两两不同。
  const shown = useMemo(() => spots.slice(0, 8), [spots]);
  const spotHue = useMemo(() => assignHues(shown.map((s) => s.name)), [shown]);
  const kindHue = useMemo(() => assignHues(errKinds.map(([k]) => k)), [errKinds]);

  if (issues.length === 0) {
    return <p className="dash-muted">本次未发现问题</p>;
  }

  return (
    <div className="att">
      {/* ① 账对不上：按铺位聚合。这是最需要人判断的一类，排最前。 */}
      {spots.length > 0 && (
        <section className="att-sec">
          <div className="att-h">
            <span>账目对不上</span>
            <span className="att-n warn">{mism.length} 处 · {spots.length} 个铺位</span>
          </div>
          {/* 哪个月集中出错：**一行**结论 + 深浅小方格，不再铺 6 个长 pill。
              之前那种"2025年11月租金 22"整排 pill 会换行堆成两三行，
              视觉上抢成了第二主角——而它只是个提示，不配。 */}
          {load.length > 0 && (() => {
            const max = Math.max(...load.map((l) => l.count));
            const top = load[0];
            return (
              <p className="att-load-line">
                <span>集中在 {top.sheet.replace(/\s*（日）\s*/, "").replace(/租金$/, "")}</span>
                <span className="att-load-squares">
                  {load.map((l) => {
                    const step = loadStep(l.count, max);
                    return (
                      <i key={l.sheet} className="sq" data-step={step}
                        style={{ background: `var(--viz-${step})` }}
                        title={`${l.sheet}：${l.count} 处`} />
                    );
                  })}
                </span>
                <span className="att-load-more">{top.count} 处最多</span>
              </p>
            );
          })()}
          <ul className="att-list">
            {shown.map((s) => {
              const open = openName === s.name;
              const sev = gapSeverity(s.gap);
              return (
                <li key={s.name} className="att-item">
                  <button className="att-row" onClick={() => setOpenName(open ? null : s.name)}>
                    {/* 两条颜色语言同时上，各说各的：
                        · 左细条 = 严重度（暖度表急迫，横向可比"谁先查"）
                        · 图标   = 对象身份色相（b50 永远是同一个蓝/绿，认色如认人） */}
                    <span className="att-rail" style={{ background: sevVar(sev) }} aria-hidden />
                    <span className="att-ic" style={{ color: spotHue.get(s.name) }}>
                      <IconChart size={14} />
                    </span>
                    <span className="att-name">{s.name}</span>
                    <span className="att-cnt" title={`在 ${s.sheets.length} 张表里共 ${s.count} 处对不上`}>
                      {s.count} 处
                    </span>
                    {s.gap !== undefined && (
                      <span className="att-gap" style={{ color: sevVar(sev) }}
                        title={`最大差额（原值）：${s.gap.toFixed(2)}`}>
                        差 {shortMoney(s.gap)}
                      </span>
                    )}
                    <span className={`cr-caret${open ? " open" : ""}`}>›</span>
                  </button>
                  {/* 展开：这个铺位的每一处，带可点坐标跳到表里 */}
                  {open && (
                    <ul className="att-sub">
                      {s.issues.map((it, i) => (
                        <li key={i}>
                          <button className="att-sub-row" onClick={() => onOpen(it)}
                            title="打开表格并定位到该格">
                            <span className="att-sub-sheet">{it.sheet.replace(/\s*（日）\s*/, "")}</span>
                            <code className="att-sub-ref">{it.ref}</code>
                            <span className="att-sub-msg">{it.message}</span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
          {spots.length > 8 && (
            <p className="att-more">另有 {spots.length - 8} 个铺位也有对不上</p>
          )}
        </section>
      )}

      {/* ② #REF! 一类：同质的错误值，**折叠成一行**。
          196 条几乎一样的话不该占屏，但它们必须在（用户要知道表格坏了）。 */}
      {errKinds.length > 0 && (
        <section className="att-sec">
          <div className="att-h">
            <span>公式/取值异常</span>
          </div>
          <ul className="att-list">
            {errKinds.map(([kind, n]) => {
              const first = errs.find((e) => e.kind === kind)!;
              return (
                <li key={kind} className="att-item">
                  <button className="att-row" onClick={() => onOpen(first)}>
                    <span className="att-rail" style={{ background: sevVar(3) }} aria-hidden />
                    <span className="att-ic" style={{ color: kindHue.get(kind) }}>
                      <IconShield size={14} />
                    </span>
                    <span className="att-name">{kindLabel(kind)}</span>
                    <span className="att-cnt">{n} 处</span>
                    <span className="cr-caret">›</span>
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </div>
  );
}

function kindLabel(kind: string): string {
  switch (kind) {
    case "bad_value": return "单元格是错误值（#REF! 等）";
    case "bad_ref": return "公式引用已失效";
    case "sheet_missing": return "月表缺失";
    case "sheet_unfit": return "表缺关键列";
    case "scan_error": return "该表扫描出错";
    default: return kind;
  }
}

/** 该月问题数相对峰值 → viz 刻度档（1..5）。相对刻度让"最多的月"总是最深。 */
function loadStep(count: number, max: number): number {
  if (max <= 1) return 5;
  const r = count / max;
  if (r >= 0.85) return 5;
  if (r >= 0.6) return 4;
  if (r >= 0.35) return 3;
  if (r >= 0.15) return 2;
  return 1;
}
