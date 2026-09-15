import { useMemo } from "react";
import type { SheetShape } from "../api-agent";
import { parseMetrics, findMetric, rateTone, money } from "../lib/kpi";

/**
 * LedgerBoard —— 「账怎么样」的展示台。
 *
 * 为什么取代了原来的"表的目录"：首页原来的问题是**在展示有哪些表**，
 * 而不是**账现在怎么样**。而翻开真表，关键数字一直躺在那里没人看：
 * 本月收租率 8.34%、应收 1034 万、实收 86 万、35 家没交齐。
 * 目录帮不了任何决策，这几个数才是每天要看的。
 *
 * 三件东西，按"该先看哪个"排：
 *   ① 收租率——最大的那个数，带语义色和进度条（一眼看出离 100% 多远）
 *   ② 金额/家数——支撑上面的三个具体数
 *   ③ 走势——14 个月的真实历史（以前完全没露出来过）
 *
 * 诚实边界：这些数字全部**来自表自己写的汇总行**，不是我们另算的，
 * 所以能跟 Excel 对上。哪个数没读到就不显示，不编。
 */
export default function LedgerBoard({ shapes, onOpen }: {
  shapes: Record<string, SheetShape>;
  onOpen?: (sheet: string, file: string) => void;
}) {
  // 按月表的时间顺序取"最近一期"与历史序列。
  // 月表名字里带年月（2026年9月），用它排序；排不出来的放最后。
  const months = useMemo(() => {
    const rows: { sheet: string; file: string; y: number; m: number; rate?: number; due?: number; got?: number; unpaid?: number }[] = [];
    for (const sh of Object.values(shapes)) {
      const ms = parseMetrics(sh.kpi);
      if (ms.length === 0) continue;
      const rate = findMetric(ms, "收租率");
      const due = findMetric(ms, "应收");
      const got = findMetric(ms, "实收");
      const unpaid = findMetric(ms, "未收齐", "商铺数量");
      if (!rate && !due && !got) continue;
      const ym = /(\d{4})\s*年\s*(\d{1,2})\s*月/.exec(sh.sheet);
      rows.push({
        sheet: sh.sheet, file: sh.file,
        y: ym ? Number(ym[1]) : 0, m: ym ? Number(ym[2]) : 0,
        rate: rate?.pct, due: due?.num, got: got?.num, unpaid: unpaid?.num,
      });
    }
    return rows.sort((a, b) => (a.y - b.y) || (a.m - b.m));
  }, [shapes]);

  if (months.length === 0) return null;

  const latest = months[months.length - 1];
  const tone = rateTone(latest.rate);
  const dueM = latest.due !== undefined ? money(String(latest.due)) : undefined;
  const gotM = latest.got !== undefined ? money(String(latest.got)) : undefined;
  const gap = latest.due !== undefined && latest.got !== undefined ? latest.due - latest.got : undefined;

  return (
    <div className="board">
      {/* ① 主读数：收租率。这是整页唯一"大到能一眼读出"的数字。 */}
      <button className={`bd-hero ${tone}`} onClick={() => onOpen?.(latest.sheet, latest.file)}
        title={`${latest.sheet}（点开这张表）`}>
        <span className="bd-hero-top">
          <span className="bd-hero-lab">本月收租率</span>
          <span className="bd-hero-period">{latest.y}年{latest.m}月</span>
        </span>
        <span className="bd-hero-val">
          {latest.rate !== undefined ? latest.rate.toFixed(2) : "—"}
          <em>%</em>
        </span>
        {/* 进度条：把"离 100% 还差多少"画出来。数字要读，条不用。 */}
        <span className="bd-meter" aria-hidden="true">
          <i style={{ width: `${Math.max(0, Math.min(100, latest.rate ?? 0))}%` }} />
        </span>
      </button>

      {/* ② 支撑数字：应收 / 实收 / 欠款 / 未收齐家数 */}
      <div className="bd-stats">
        {dueM && (
          <div className="bd-stat">
            <span className="bd-k">本月应收</span>
            <span className="bd-v" title={dueM.full}>{dueM.text}</span>
          </div>
        )}
        {gotM && (
          <div className="bd-stat">
            <span className="bd-k">本月实收</span>
            <span className="bd-v" title={gotM.full}>{gotM.text}</span>
          </div>
        )}
        {gap !== undefined && gap > 0 && (
          <div className="bd-stat bad">
            <span className="bd-k">未收差额</span>
            <span className="bd-v" title={String(gap)}>{money(String(gap)).text}</span>
          </div>
        )}
        {latest.unpaid !== undefined && (
          <div className="bd-stat bad">
            <span className="bd-k">未收齐店铺</span>
            <span className="bd-v">{latest.unpaid.toFixed(0)}<em>家</em></span>
          </div>
        )}
      </div>

      {/* ③ 走势：14 个月的真实历史。之前这些数字完全没露出来过。 */}
      {months.length > 1 && (
        <div className="bd-trend">
          <div className="bd-trend-h">
            <span>收租率走势</span>
            <span className="bd-trend-n">{months.length} 个月</span>
          </div>
          <div className="bd-bars">
            {months.map((r) => {
              const t = rateTone(r.rate);
              const h = Math.max(4, Math.min(100, r.rate ?? 0));
              return (
                <button
                  key={r.sheet}
                  className={`bd-bar ${t}`}
                  style={{ height: `${h}%` }}
                  title={`${r.y}年${r.m}月 · 收租率 ${r.rate?.toFixed(2) ?? "—"}%`}
                  onClick={() => onOpen?.(r.sheet, r.file)}
                />
              );
            })}
          </div>
          {/* 只标首尾两个月：中间不标，避免挤成一团（刻度靠等距自己说明） */}
          <div className="bd-axis">
            <span>{months[0].y}·{months[0].m}</span>
            <span>{latest.y}·{latest.m}</span>
          </div>
        </div>
      )}
    </div>
  );
}
