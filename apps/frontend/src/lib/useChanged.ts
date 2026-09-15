import { useEffect, useRef, useState } from "react";

/**
 * 数值/文本变化时短暂返回 true —— 用来给"变了的那个数"闪一下高亮。
 *
 * 为什么值得单独做个 hook：账目工具里"数字变了"是最重要的事件，
 * 但静态界面只会让数字**悄悄变掉**，人得自己逐个比对才发现。闪一下
 * 比任何"已更新"的提示都直接——它就发生在你要看的那个数上。
 *
 * 首帧不闪：初次渲染只是"读到了值"，不是"值变了"。若首帧也闪，
 * 每次刷新满屏都在闪，噪音反而盖住真正的变化。
 */
export function useChanged<T>(value: T, ms = 1100): boolean {
  const prev = useRef<T>(value);
  const first = useRef(true);
  const [changed, setChanged] = useState(false);

  useEffect(() => {
    if (first.current) {
      first.current = false;
      prev.current = value;
      return;
    }
    if (prev.current === value) return;
    prev.current = value;
    setChanged(true);
    const t = setTimeout(() => setChanged(false), ms);
    return () => clearTimeout(t);
  }, [value, ms]);

  return changed;
}

/**
 * 给一组数字做"整体变化"检测：任意一项变了就返回 true。
 * 用在"改完一批后刷新"的场景——批量改动时整块闪一次，比逐格闪更可读。
 */
export function useAnyChanged(values: unknown[], ms = 1100): boolean {
  const key = JSON.stringify(values);
  return useChanged(key, ms);
}
