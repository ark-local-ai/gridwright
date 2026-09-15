import { useEffect, useState } from "react";
import "./sheet.css";
import { agentApi } from "../api-agent";
import type { SheetPreview } from "../api-agent";
import { IconXls } from "../components/icons";
import { SkTable } from "../components/Skeleton";

/* 表预览（见 docs/agent-architecture/19-界面设计.md 阶段 1）
   这是"进入表格的入口"：看真实数据、点坐标跳体检条目。
   诚实边界：excelize 不求值，公式格显示的是上次保存的值。 */

/** 判断某一列是不是"数字列"——账页式渲染的关键：
    数字列右对齐 + 等宽，位数一眼对齐；文本列左对齐。
    抽样该列数据行，超过六成能解析成数字就认。 */
function numericCols(rows: string[][], cols: number): boolean[] {
  const out: boolean[] = [];
  for (let c = 0; c < cols; c++) {
    let num = 0, seen = 0;
    for (const row of rows) {
      const v = (row[c] ?? "").trim();
      if (!v) continue;
      seen++;
      // 容忍千分位、货币符号、百分号、括号负数
      if (/^[¥$€]?\s*-?[\d,]+(\.\d+)?\s*%?$/.test(v) || /^\([\d,.]+\)$/.test(v)) num++;
      if (seen >= 40) break;
    }
    out[c] = seen >= 3 && num / seen >= 0.6;
  }
  return out;
}

export default function SheetView({ file, sheet, highlight, onBack }: {
  file?: string;
  sheet: string;
  highlight?: { ref: string } | null;
  onBack: () => void;
}) {
  const [pv, setPv] = useState<SheetPreview | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    agentApi.preview(sheet, file, 80)
      .then((d) => { if (alive) { setPv(d); setErr(""); } })
      .catch((e) => { if (alive) setErr(e instanceof Error ? e.message : String(e)); })
      .finally(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, [file, sheet]);

  // 高亮的坐标（体检条目点进来的）
  const hlCol = highlight?.ref ? refCol(highlight.ref) : -1;
  const hlRow = highlight?.ref ? refRow(highlight.ref) : -1;
  // 数字列判定（账页式对齐）
  const numCols = pv ? numericCols(pv.sample, pv.header.length) : [];

  return (
    <div className="sheet-view">
      <header className="sv-top">
        <button className="btn ghost sm" onClick={onBack}>返回看板</button>
        <div className="sv-title">
          <IconXls size={15} />
          <b>{sheet}</b>
          <span>{file ?? ""}</span>
        </div>
        {pv && (
          <div className="sv-meta">
            {pv.rows} 行 · {pv.cols} 列 · {pv.formulas} 公式
          </div>
        )}
      </header>

      {pv?.note && <p className="sv-note">{pv.note}</p>}

      {loading && (
        <div className="sv-scroll">
          {/* 表格用表骨架：格子对格子，不写"读取中…"（动画 > 文字） */}
          <SkTable rows={10} cols={7} />
        </div>
      )}
      {err && (
        <div className="sv-msg">
          <p>读不出这张表：{err}</p>
          <button className="btn ghost sm" onClick={onBack}>返回</button>
        </div>
      )}

      {pv && (
        <div className="sv-scroll">
          {/* 概览：表顶部的汇总行（如 本月收租率/应收/实收） */}
          {pv.summaries.length > 0 && (
            <div className="sv-sums">
              {pv.summaries.map((s, i) => (
                <div key={i} className="sv-sum">
                  <span className="sv-sum-lab">{s.label || "汇总"}</span>
                  <span className="sv-sum-vals">
                    {s.values.map((v, j) => (
                      <span key={j} className="sv-sum-v">{v}</span>
                    ))}
                  </span>
                </div>
              ))}
            </div>
          )}

          <table className="sv-table">
            <thead>
              <tr>
                <th className="sv-rn">#</th>
                {pv.header.map((h, i) => (
                  <th key={i} className={`${hlCol === i + 1 ? "sv-hl" : ""}${numCols[i] ? " sv-num" : ""}`.trim() || undefined}>
                    {h || `列${i + 1}`}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {pv.sample.map((row, ri) => {
                const rowNum = pv.headerRow + ri + 1;
                return (
                  <tr key={ri} className={hlRow === rowNum ? "sv-row-hl" : ""}>
                    <td className="sv-rn">{rowNum}</td>
                    {pv.header.map((_, ci) => {
                      const v = row[ci] ?? "";
                      const isHl = hlRow === rowNum && hlCol === ci + 1;
                      const cls = [numCols[ci] ? "sv-num" : "", isHl ? "sv-cell-hl" : ""].filter(Boolean).join(" ");
                      return (
                        <td key={ci} className={cls || undefined} title={v}>
                          {v}
                        </td>
                      );
                    })}
                  </tr>
                );
              })}
              {pv.sample.length === 0 && (
                <tr><td className="sv-empty" colSpan={pv.header.length + 1}>这张表没有数据行</td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/* ---- A1 坐标解析 ---- */

function refCol(ref: string): number {
  const m = /^([A-Z]+)\d+$/.exec(ref);
  if (!m) return -1;
  let n = 0;
  for (const ch of m[1]) n = n * 26 + (ch.charCodeAt(0) - 64);
  return n;
}

function refRow(ref: string): number {
  const m = /^[A-Z]+(\d+)$/.exec(ref);
  return m ? parseInt(m[1], 10) : -1;
}

/* 供外部复用（体检条目 → 坐标） */
export { refCol, refRow };
