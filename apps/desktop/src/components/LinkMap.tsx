import { useMemo } from "react";
import type { GraphData, GraphNode, ScanIssue } from "../api-agent";
import { nodeId } from "../api-agent";
import { linkHealth, families, brokenRefs } from "../lib/linkage";
import { assignHues } from "../lib/hues";
import { IconXls, IconTable } from "./icons";

/**
 * LinkMap —— 「表的连接」。
 *
 * 这个面板要讲清一件事：**谁引用谁、谁断了**。
 *
 * 为什么按"工作区 → 工作簿 → 工作表"三层来列，而不是平铺：
 * 这是数据本来的形状——用户原话「一个工作区肯定不只是一个 xls 文件，
 * 然后一个 xls 文件肯定有多个 sheet」。平铺成一条长列表，就把"哪几张
 * 属于同一个文件"这个最关键的信息抹掉了；而跨表引用**绝大多数发生在
 * 同一个工作簿内**（sheet 之间），跨文件的才是少数。所以先按文件分块，
 * 块内才是 sheet，层级本身就是信息。
 *
 * 视觉上不再画缩略图（那点尺寸画不出内容，只是装饰）：改用**带色的小图标
 * 与色块 chip** —— 每张 sheet 一个稳定色相，连着的是实心、孤立的是空心。
 * 颜色认对象不认位置（见 lib/hues），所以同一张表在哪儿都是同一个颜色。
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

  // 有跨表引用的表（判"连着/孤立"）
  const linkedIds = useMemo(() => {
    const s = new Set<string>();
    for (const e of graph.edges) {
      if (e.from.sheet !== e.to.sheet) {
        s.add(nodeId(e.from));
        s.add(nodeId(e.to));
      }
    }
    return s;
  }, [graph.edges]);

  // 按**文件**分组——这就是"工作区 → 工作簿 → sheet"的中间那层。
  // 文件内的 sheet 顺序按名字排，保证每次渲染一致（不随图返回顺序抖动）。
  const byFile = useMemo(() => {
    const m = new Map<string, GraphNode[]>();
    for (const n of graph.nodes) {
      const k = n.file || "（外部文件）";
      if (!m.has(k)) m.set(k, []);
      m.get(k)!.push(n);
    }
    for (const list of m.values()) list.sort((a, b) => a.sheet.localeCompare(b.sheet, "zh"));
    return [...m.entries()].sort((a, b) => b[1].length - a[1].length);
  }, [graph.nodes]);

  // 每张 sheet 一个稳定色相。先按名字排序再分配 → 同一批表永远同色。
  const sheetHue = useMemo(
    () => assignHues(graph.nodes.map((n) => n.sheet)),
    [graph.nodes],
  );

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

      {/* ===== 工作区 → 工作簿 → 工作表 =====
          一层一块。每块头是"这个文件"，块内是它的 sheet。 */}
      <div className="lm-tree">
        <div className="lm-tree-h">
          <span className="lm-tree-k">工作区</span>
          <span className="lm-tree-n">
            {graph.files.length} 个工作簿 · {graph.nodes.length} 张工作表
          </span>
        </div>

        {byFile.map(([file, nodes]) => {
          const linkedN = nodes.filter((n) => linkedIds.has(nodeId(n))).length;
          const isoN = nodes.length - linkedN;
          return (
            <section key={file} className="lm-book">
              {/* 工作簿这一层：图标 + 名字 + 本文件内的连接概况 */}
              <header className="lm-book-h">
                <span className="lm-book-ic"><IconXls size={14} /></span>
                <span className="lm-book-name" title={file}>
                  {file.replace(/\.xlsx?$/i, "")}
                </span>
                <span className="lm-book-n">
                  {nodes.length} 张表
                  <span className="lm-book-c">
                    {linkedN > 0 && <><i className="ok" />{linkedN} 连着</>}
                    {isoN > 0 && <><i className="iso" />{isoN} 孤立</>}
                  </span>
                </span>
              </header>

              {/* 工作表这一层：一排 chip。实心=有跨表引用，空心=孤立。
                  点一下打开那张表。 */}
              <div className="lm-chips">
                {nodes.map((n) => {
                  const id = nodeId(n);
                  const on = linkedIds.has(id);
                  const hue = sheetHue.get(n.sheet) ?? "var(--hue-6)";
                  return (
                    <button
                      key={id}
                      className={`lm-chip${on ? " on" : ""}`}
                      style={on ? { background: hue } : { color: hue, borderColor: hue }}
                      onClick={() => onOpenSheet(n)}
                      title={`${n.sheet}${on ? "：有跨表引用（改动会传下去）" : "：孤立表（改动不会传进来，也不会传出去）"}`}
                    >
                      <IconTable size={11} />
                      <span className="lm-chip-n">{n.sheet.replace(/\s*（日）\s*/, "")}</span>
                    </button>
                  );
                })}
              </div>
            </section>
          );
        })}
      </div>

      {/* 同名系列（如 14 张月表）：单独讲"这一族谁断了"。
          这不是重复——上面按文件列的是"属于谁"，这里讲的是"这一串该连着"。 */}
      {fams.length > 0 && (
        <div className="lm-fams">
          {fams.map((f) => (
            <div key={f.name} className="lm-fam">
              <span className="lm-fam-n">{f.name}</span>
              <span className="lm-fam-c">
                {f.linked.length} 连着 · {f.isolated.length} 断开
              </span>
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
            {breaks.map((b) => {
              const hue = sheetHue.get(b.sheet) ?? "var(--danger)";
              return (
                <li key={b.sheet}>
                  <button className="lm-break-row" onClick={() => onOpenIssue(b.sample)}
                    title={`点开跳到 ${b.sample.ref}`}>
                    <span className="lm-break-dot" style={{ background: hue }} aria-hidden />
                    <span className="lm-break-sheet">{b.sheet.replace(/\s*（日）\s*/, "")}</span>
                    <code className="lm-break-ref">{b.sample.ref}</code>
                    <span className="lm-break-n">{b.count} 处</span>
                  </button>
                </li>
              );
            })}
          </ul>
          <p className="lm-break-hint">
            公式原来指向被改动的表；现在指向空处，所以这一格算不出来。
          </p>
        </div>
      )}
    </div>
  );
}
