import { useMemo } from "react";
import type { GraphData, GraphNode, ScanIssue } from "../api-agent";
import { linkHealth, families, brokenRefs } from "../lib/linkage";

/**
 * LinkMap —— 「表的连接」用图表达，而且这张图回答一个问题：
 * **哪些表该连着，现在断开了？**
 *
 * 为什么不画力导向关系网：真表上只有 6 条边、24 个节点，画出来是个稀疏的
 * 星形，看不出任何东西——用户的原话"很丑，很无力"，说得对。
 *
 * 真正有信息量的是**断裂**：
 *   · 14 张月表本该是一族，5 张连着主数据、9 张断了（2026 全年）
 *   · 断掉的那些地方留下 113 处 #REF!
 * 所以这里画的是"族谱 + 断口"：一行一个家族，连着的是实线，断开的是虚线缺口。
 *
 * 每条都能点：跳到那张表，或跳到断掉的那一格。
 */
export default function LinkMap({ graph, issues, onOpenSheet, onOpenIssue }: {
  graph: GraphData;
  issues: ScanIssue[];
  onOpenSheet: (n: GraphNode) => void;
  onOpenIssue: (it: ScanIssue) => void;
}) {
  const health = useMemo(() => linkHealth(graph), [graph]);
  const fams = useMemo(() => families(graph), [graph]);
  const breaks = useMemo(() => brokenRefs(issues), [issues]);
  const nodeOf = useMemo(() => {
    const m = new Map<string, GraphNode>();
    for (const n of graph.nodes) m.set(n.sheet, n);
    return m;
  }, [graph]);

  return (
    <div className="lm">
      {/* 一句话结论：用户不该自己读图得出结论 */}
      <p className={`lm-verdict${health.isolated > 0 ? " warn" : " ok"}`}>{health.verdict}</p>

      {/* 读数：连着 / 断开 */}
      <div className="lm-stats">
        <span className="lm-st led"><i />连着 <b>{health.connected}</b></span>
        <span className="lm-st iso"><i />断开 <b>{health.isolated}</b></span>
        <span className="lm-st tot">共 <b>{health.total}</b> 张</span>
      </div>

      {/* 族谱：一行一族。连着的实心，断开的空心虚线。 */}
      {fams.length > 0 && (
        <div className="lm-fams">
          {fams.map((f) => (
            <div key={f.name} className="lm-fam">
              <div className="lm-fam-h">
                <span className="lm-fam-n">{f.name}</span>
                <span className="lm-fam-c">
                  {f.linked.length} 连着 · {f.isolated.length} 断开
                </span>
              </div>
              {/* 每个成员一个小方块：实=连着、空=断开。横排，像一排开关 */}
              <div className="lm-dots">
                {f.members.map((m) => {
                  const isLinked = f.linked.includes(m);
                  const nd = nodeOf.get(m);
                  return (
                    <button
                      key={m}
                      className={`lm-dot${isLinked ? " on" : ""}`}
                      title={`${m}${isLinked ? "（有跨表引用）" : "（孤立：改动不会传进来，也不会传出去）"}`}
                      onClick={() => nd && onOpenSheet(nd)}
                    />
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 断口：具体是哪些表、多少处。点一下就跳到那一格。 */}
      {breaks.length > 0 && (
        <div className="lm-breaks">
          <div className="lm-breaks-h">
            <span>引用断掉的地方</span>
            <span className="lm-breaks-n">{breaks.reduce((n, b) => n + b.count, 0)} 处</span>
          </div>
          <ul className="lm-break-list">
            {breaks.map((b) => (
              <li key={b.sheet}>
                <button className="lm-break-row" onClick={() => onOpenIssue(b.sample)}
                  title={`点开跳到 ${b.sample.ref}`}>
                  <span className="lm-break-sheet">{b.sheet.replace(/\s*（日）\s*/, "")}</span>
                  <code className="lm-break-ref">{b.sample.ref}</code>
                  <span className="lm-break-n">{b.count} 处 #REF!</span>
                </button>
              </li>
            ))}
          </ul>
          <p className="lm-break-hint">
            公式原来指向被改动的表；现在指向空处，所以这一格算不出来。
          </p>
        </div>
      )}
    </div>
  );
}
