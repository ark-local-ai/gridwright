type P = { size?: number; className?: string };

function base(size?: number, className?: string) {
  return {
    width: size ?? 16,
    height: size ?? 16,
    viewBox: "0 0 24 24",
    className: className ? `ic ${className}` : "ic",
  } as const;
}

/* ---------- 通用线性图标（细节丰富 · 圆头线条） ---------- */

export const IconPlus = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M12 5v14M5 12h14" /></svg>
);
export const IconAdd = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <rect x="4.2" y="4.2" width="15.6" height="15.6" rx="4.2" />
    <path d="M12 8.4v7.2M8.4 12h7.2" />
  </svg>
);
export const IconBubble = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="12" cy="12" r="8.6" />
    <path d="M12 8.2v7.6M8.2 12h7.6" />
    <path d="M12 3.4v1.3M20.6 12h-1.3M12 20.6v-1.3M3.4 12h1.3" />
  </svg>
);
export const IconHome = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M3.5 10.8 12 4l8.5 6.8" />
    <path d="M5.8 9.2V20h12.4V9.2" />
    <path d="M10 20v-5.2h4V20" />
    <path d="M9.5 4.8V3.2h1.8" />
  </svg>
);
export const IconChat = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M21 11.6a8 8 0 0 1-8 8H7.2L3 22.4V14a8 8 0 0 1 8-8h2a8 8 0 0 1 8 5.6z" />
    <circle cx="8.6" cy="12" r="0.9" fill="currentColor" stroke="none" />
    <circle cx="12.2" cy="12" r="0.9" fill="currentColor" stroke="none" />
    <circle cx="15.8" cy="12" r="0.9" fill="currentColor" stroke="none" />
  </svg>
);
export const IconSpark = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M11 4.5l1.9 4.6 4.6 1.9-4.6 1.9L11 17.5l-1.9-4.6-4.6-1.9 4.6-1.9z" />
    <path d="M18.5 14.5l.9 2.1 2.1.9-2.1.9-.9 2.1-.9-2.1-2.1-.9 2.1-.9z" />
  </svg>
);
export const IconUsers = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="9" cy="8" r="3.1" />
    <path d="M3.5 19.6c0-3.1 2.5-5.2 5.5-5.2s5.5 2.1 5.5 5.2" />
    <circle cx="17.2" cy="9.2" r="2.4" />
    <path d="M17.9 14.4c1.8.6 3 2.1 3 4" />
    <path d="M13.8 5.3a2.6 2.6 0 1 1 1.5 4.9" />
  </svg>
);
export const IconClock = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="12" cy="12" r="8.4" />
    <path d="M12 7.4V12l3.1 2.6" />
    <path d="M12 3.6v1.2M20.4 12h-1.2M12 20.4v-1.2M3.6 12h1.2" />
  </svg>
);
export const IconFolder = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M3.5 7A1.5 1.5 0 0 1 5 5.5h4.1l1.9 2.1H19A1.5 1.5 0 0 1 20.5 9v8A1.5 1.5 0 0 1 19 18.5H5A1.5 1.5 0 0 1 3.5 17z" />
    <path d="M3.5 9.6h17" />
  </svg>
);
export const IconGear = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="12" cy="12" r="6.3" />
    <circle cx="12" cy="12" r="2.4" />
    <path d="M12 2.6v2.5M12 18.9v2.5M2.6 12h2.5M18.9 12h2.5M5.35 5.35l1.8 1.8M16.85 16.85l1.8 1.8M18.65 5.35l-1.8 1.8M7.15 16.85l-1.8 1.8" />
  </svg>
);
export const IconSend = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M4.5 11.4 20 4l-4.3 16-4.4-6.3z" />
    <path d="M20 4l-8.7 9.7" />
  </svg>
);
export const IconImage = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
    <circle cx="8.5" cy="10" r="1.8" />
    <path d="M4 17l4.8-4.8 3.4 3.4 2.6-2.6L20 17.5" />
  </svg>
);
export const IconSearch = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="11" cy="11" r="6.2" />
    <path d="M19.6 19.6 15.2 15.2" />
    <path d="M8.1 9.4a3.5 3.5 0 0 1 2-1.9" />
  </svg>
);
export const IconCheck = (p: P) => (
  <svg {...base(p.size, p.className)} style={{ strokeWidth: 2.6 }}><path d="M5 12.6l4.2 4.2L19 7" /></svg>
);
/* 关闭 / 移除：一条干净的叉。线长与其它图标一致（两端留 5.5 的边距），
   线帽圆头，和全站线条语汇统一。
   （旧版在叉之外还画了一圈小刻度，看着像"坏掉的图标"，已去掉。） */
export const IconX = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M6.6 6.6l10.8 10.8M17.4 6.6L6.6 17.4" />
  </svg>
);
export const IconTrash = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M4 6.5h16" />
    <path d="M8.5 6.5V4.8A1.3 1.3 0 0 1 9.8 3.5h4.4a1.3 1.3 0 0 1 1.3 1.3v1.7" />
    <path d="M6 6.5l.8 11.4A1.5 1.5 0 0 0 8.3 19.4h7.4a1.5 1.5 0 0 0 1.5-1.5L18 6.5" />
    <path d="M10 10v5.5M14 10v5.5" />
  </svg>
);
export const IconDoc = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M6.5 3H13l5 5v13H6.5z" />
    <path d="M13 3v5h5" />
    <path d="M9.5 12.2h5M9.5 15.6h5" />
  </svg>
);
export const IconDown = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M18.5 9.5 12 16 5.5 9.5" /></svg>
);
export const IconChevR = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M9.5 6.5 15 12l-5.5 5.5" /></svg>
);
export const IconChevD = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M6.5 9.5 12 15l5.5-5.5" /></svg>
);
export const IconChevD2 = (p: P) => (
  <svg {...base(p.size, p.className)}>
    {/* 展开全部：左上、右下两座角向外扩张的箭簇 */}
    <path d="M15 7l-2.4 2.4" />
    <path d="M17.5 5.5V10M13 6.5h4.5" />
    <path d="M9 17l2.4-2.4" />
    <path d="M6.5 18.5V14M11 17.5H6.5" />
  </svg>
);
export const IconChevU2 = (p: P) => (
  <svg {...base(p.size, p.className)}>
    {/* 收起全部：右上—左下 斜向双箭头（两端各一箭头尖） */}
    <path d="M18 6.5 6.5 18" />
    <path d="M13 7.5h5v5" />
    <path d="M11 16.5H6v-5" />
  </svg>
);
export const IconRefresh = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M20 7.5v5h-5" />
    <path d="M4 16.5v-5h5" />
    <path d="M5.6 9A7.1 7.1 0 0 1 17.8 6.2L20 7.5M18.4 15A7.1 7.1 0 0 1 6.2 17.8L4 16.5" />
  </svg>
);
export const IconStop = (p: P) => (
  <svg {...base(p.size, p.className)}><rect x="6.5" y="6.5" width="11" height="11" rx="2.2" /></svg>
);
export const IconFiles = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
    <path d="M14 3v5h5" />
    <path d="M9.3 13h5.4M9.3 16.4h5.4" />
  </svg>
);
export const IconArrowUp = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M12 19V5M6 11l6-6 6 6" /></svg>
);
export const IconLink = (p: P) => (
  <svg {...base(p.size, p.className)}><path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1.5 1.5" /><path d="M14 10a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1.5-1.5" /></svg>
);
export const IconChart = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M4.5 4.5v15h15" />
    <path d="M8.5 16v-4.3M12.5 16V8.2M16.5 16v-6.4" />
  </svg>
);
export const IconXls = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <rect x="4" y="4" width="16" height="16" rx="1.6" />
    <path d="M4 9.3h16M4 14.7h16M9.3 9.3V20" />
    <path d="M9.3 14.7l5.4 5.3" />
  </svg>
);
export const IconTable = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <rect x="3.5" y="4.5" width="17" height="15" rx="1.4" />
    <path d="M3.5 9.3h17M3.5 14.1h17" />
    <path d="M9.8 4.5v15M15 4.5v15" />
  </svg>
);
export const IconPipe = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="5" cy="12" r="2.1" />
    <circle cx="12" cy="12" r="2.1" />
    <circle cx="19" cy="12" r="2.1" />
    <path d="M7.1 12h2.8M12.1 12h2.8" />
  </svg>
);
export const IconSlides = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <rect x="3.5" y="4.5" width="17" height="11.5" rx="1.6" />
    <path d="M7 12.3l2.5-2.8 2.2 1.8 3.2-3.6" />
    <path d="M12 16v2.3M8.8 20.6 12 18.3l3.2 2.3" />
  </svg>
);
export const IconLibrary = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M3.5 20.5h17" />
    <path d="M5.6 20.5V6.9a1 1 0 0 1 1-1h1.5a1 1 0 0 1 1 1v13.6" />
    <path d="M11.2 20.5V5.5a1 1 0 0 1 1-1h1.5a1 1 0 0 1 1 1v15" />
    <path d="M17 20.5l-1.3-13a1 1 0 0 1 .9-1.1l1.4-.14a1 1 0 0 1 1.1.9l1.3 12.9" />
  </svg>
);
export const IconShield = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M12 3.4l7 2.6v5.3c0 4.6-3 7.7-7 9.3-4-1.6-7-4.7-7-9.3V6z" />
    <path d="M9.1 11.9l2 2 3.8-4" />
  </svg>
);
export const IconAssistant = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="12" cy="8.4" r="3.4" />
    <path d="M4.5 20a7.5 7.5 0 0 1 15 0" />
    <path d="M12 4.6v-1.2M16 3l-1 1M8 3l1 1" />
  </svg>
);
export const IconCollapse = (p: P) => (
  <svg {...base(p.size, p.className)}>
    {/* 侧栏坍缩：圆角面板 + 左侧细条分隔（收起=左条变窄） */}
    <rect x="4" y="4" width="16" height="16" rx="3.2" />
    <path d="M8.5 4v16" />
  </svg>
);
export const IconExpand = (p: P) => (
  <svg {...base(p.size, p.className)}>
    {/* 侧栏展开：圆角面板 + 分隔条靠右（展开=主面板变宽） */}
    <rect x="4" y="4" width="16" height="16" rx="3.2" />
    <path d="M15.5 4v16" />
  </svg>
);
export const IconNote = (p: P) => (
  <svg {...base(p.size, p.className)}>
    {/* 圆角气泡 + 加号：新建任务 */}
    <path d="M4.4 3.8C4.4 2.56 5.42 1.55 6.66 1.55h10.68c1.24 0 2.26 1.01 2.26 2.25v10.4c0 1.24-1.02 2.25-2.26 2.25H9.5l-3.86 2.76a.62.62 0 0 1-.96-.51V3.8z" />
    <path d="M12 6.9v6.2M8.9 10h6.2" />
  </svg>
);

/* ---------- 行/分组操作菜单用图标 ---------- */

/* 水平三点：打开更多操作菜单 */
export const IconDots = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M4.6 10.5h.01M12 10.5h.01M19.4 10.5h.01" />
  </svg>
);
export const IconFolderOpen = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M3.5 8.5A1.5 1.5 0 0 1 5 7h4l1.8 2h7.9A1.3 1.3 0 0 1 20 10.3L19 15" />
    <path d="M5.5 10h13.2a1.3 1.3 0 0 1 1.2 1.7L18 20H4.2a1.2 1.2 0 0 1-1.2-1.2l1-8.3a1 1 0 0 1 1-.5z" />
    <path d="M18.8 12.5 18 20" />
  </svg>
);
export const IconRename = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <path d="M13.5 6.2 17.8 10.5" />
    <path d="M4.5 19.5l.9-4.4 11.7-11.7a1.2 1.2 0 0 1 1.7 0l1.2 1.2a1.2 1.2 0 0 1 0 1.7L8.3 17.8l-4.4.9z" />
  </svg>
);
export const IconShare = (p: P) => (
  <svg {...base(p.size, p.className)}>
    <circle cx="6" cy="12" r="2.6" />
    <circle cx="17.5" cy="5.8" r="2.6" />
    <circle cx="17.5" cy="18.2" r="2.6" />
    <path d="M8.3 10.9l6.9-4M8.3 13.1l6.9 4" />
  </svg>
);

/* ---------- Gridwright 字标 Logo（纯文字 wordmark · 可作水印） ----------
   小写 "gridwright"，强字重 + 紧字距，纯 currentColor，任意背景自适应。
   h = 字号（px）。thin 用于大水印 / 关于面板（字重略降、字距略松）。 */
export const GridwrightLogo = ({ h = 16, thin = false, className }: { h?: number; thin?: boolean; className?: string }) => (
  <span
    className={className ? `gw-logo ${className}` : "gw-logo"}
    style={{
      fontSize: h,
      lineHeight: 1,
      fontWeight: thin ? 600 : 700,
      letterSpacing: thin ? "-0.02em" : "-0.04em",
      color: "currentColor",
      whiteSpace: "nowrap",
      userSelect: "none",
      display: "inline-block",
      fontFamily: "var(--font)",
    }}
    role="img"
    aria-label="gridwright"
  >
    gridwright
  </span>
);
