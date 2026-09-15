// 骨架屏与"数值变化"高亮：把"演"放在"写"前面。
//
// 为什么不用"读取中…"这类文字：文字要人**读**才能懂，骨架一眼就懂，
// 而且它占的尺寸和真实内容一致——加载完不会跳版。对一个台账工具来说，
// "不跳版"比"有个提示"重要得多（内容跳一下，人就找不到刚才看的那一行了）。

/** 一行骨架。w 是宽度档位，用来贴近真实文本的长短。 */
export function SkLine({ w }: { w?: 40 | 60 | 80 }) {
  const cls = w ? `sk sk-line w-${w}` : "sk sk-line";
  return <span className={cls} />;
}

/**
 * 面板骨架：贴在真实面板的位置，尺寸向真实内容靠。
 * rows 是"看起来有几行内容"，比"转圈"更能说明还要等多久。
 */
export function SkPanel({ rows = 3, className = "" }: { rows?: number; className?: string }) {
  const ws: Array<40 | 60 | 80> = [80, 60, 40];
  return (
    <div className={`sk-group ${className}`.trim()} aria-label="读取中" role="status">
      {Array.from({ length: rows }, (_, i) => (
        <SkLine key={i} w={ws[i % ws.length]} />
      ))}
    </div>
  );
}

/** 表骨架：表头 + 若干行，用在"打开表格"的加载态（表是格子，骨架也该是格子）。 */
export function SkTable({ rows = 8, cols = 6 }: { rows?: number; cols?: number }) {
  return (
    <div className="sk-table" aria-label="读取中" role="status">
      <div className="sk-table-head">
        {Array.from({ length: cols }, (_, i) => <span key={i} className="sk sk-cell" />)}
      </div>
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="sk-table-row">
          {Array.from({ length: cols }, (_, c) => (
            <span key={c} className={`sk sk-cell${c === 0 ? " sk-cell-narrow" : ""}`} />
          ))}
        </div>
      ))}
    </div>
  );
}
