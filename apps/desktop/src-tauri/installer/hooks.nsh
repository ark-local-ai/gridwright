; =====================================================================
; gridwright 安装界面 · 品牌化钩子
;
; 这个文件被 tauri-bundler 在 installer.nsi 顶部（MUI2.nsh 之后、
; MUI_PAGE_WELCOME 之前）include 进来，所以这里 !define 的 MUI_* 值
; 会先于向导页插入生效——这是 MUI2 定制的标准时机。
;
; 语言选择：只出简体中文。理由是本产品（含界面、文档）就是中文的，
; 若同时挂英文，MUI 的本地化字符串会切英文、而这里的 !define 文本
; 仍是中文，装出来的界面就会中英混排。宁可用一门语言做干净。
; （MUI 的 MUI_WELCOMEPAGE_TITLE 只接受 !define，不接受 LangString——
;   LangString 必须在 MUI_LANGUAGE 之后声明，而本文件在它之前 include。
;   这是 MUI2 的硬限制，不是偷懒。）
; =====================================================================

; 欢迎页：说清"这是什么、装完能干嘛"，与官网 hero 同一套说法
!define MUI_WELCOMEPAGE_TITLE "安装 gridwright"
!define MUI_WELCOMEPAGE_TEXT "gridwright 是跑在这台机器上的数据管家：新数据一进 inbox，它按你定的规矩自动改表——每一格改动都留账目、能回滚、改完通知你。$\r$\n$\r$\n数据全部留在本机，不联网也能用。$\r$\n$\r$\n点击「下一步」继续。"

; 完成页：卸下"装完了然后呢"的疑问（官网 CTA 的同一句话）
!define MUI_FINISHPAGE_TITLE "gridwright 装好了"
!define MUI_FINISHPAGE_TEXT "双击桌面图标即可开始。首次打开会拉起本地引擎，稍等几秒。$\r$\n$\r$\n你的表放在哪个文件夹，就在里面建一个 inbox——丢进去的数据会被自动认领。"
