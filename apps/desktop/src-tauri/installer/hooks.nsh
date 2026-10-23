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

; =====================================================================
; 安装/卸载钩子
;
; 为什么需要这两个：模板（tauri-bundler 2.9.4 的 installer.nsi）有两处会导致
; **卸载不掉**，实测踩到过：
;
;   ① RestorePreviousInstallLocation（模板 850 行）把注册表里记的上次安装路径
;      直接写进 $INSTDIR，**不检查那个目录还在不在**。用户手删文件夹当卸载
;      （很常见）之后，注册项就成了孤儿：重装会默认又指向那个已删除的路径。
;   ② 卸载段只做 DeleteRegKey /ifempty，而 MANUPRODUCTKEY 的默认值存着
;      $INSTDIR，默认值在 → 键**永远不为空** → 那条陈旧路径永远删不掉。
;
; 钩子必须在模板之前 !define 好（本文件正是这个时机），模板里用
; !ifmacrodef NSIS_HOOK_POSTINSTALL / POSTUNINSTALL 插入。
; =====================================================================

; 装完（POSTINSTALL 在开始菜单/桌面快捷方式之后执行）：
; 补一个「卸载」快捷方式。模板默认不建，于是用户还以为没有卸载程序。
!macro NSIS_HOOK_POSTINSTALL
  ; 与主快捷方式同目录：有 startMenuFolder 就进那个子目录，否则放开始菜单根
  !if "${STARTMENUFOLDER}" != ""
    CreateShortcut "$SMPROGRAMS\$AppStartMenuFolder\卸载 gridwright.lnk" "$INSTDIR\uninstall.exe"
  !else
    CreateShortcut "$SMPROGRAMS\卸载 gridwright.lnk" "$INSTDIR\uninstall.exe"
  !endif
!macroend

; 卸载时：
;   - 删掉自己建的那个「卸载」快捷方式。模板的清理逻辑只删**指向主程序**的
;     快捷方式（IsShortcutTarget 比对 exe 路径），我这个指向 uninstall.exe，
;     匹配不上，不删就会留在开始菜单里变成一个打不开的死链接。
;   - 清掉路径记录本身（而不只是"空了才删"）。否则 MANUPRODUCTKEY 的默认值
;     还在 → 键永不为空 → 那条陈旧路径永远删不掉，下次重装又回到已删目录。
!macro NSIS_HOOK_POSTUNINSTALL
  !insertmacro MUI_STARTMENU_GETFOLDER Application $AppStartMenuFolder
  !if "${STARTMENUFOLDER}" != ""
    Delete "$SMPROGRAMS\$AppStartMenuFolder\卸载 gridwright.lnk"
    RMDir "$SMPROGRAMS\$AppStartMenuFolder"
  !else
    Delete "$SMPROGRAMS\卸载 gridwright.lnk"
  !endif
  ; 删整个键：DeleteRegValue 用空串删"默认值"不生效（实测），
  ; 而 DeleteRegKey 是可靠的。这个键只存安装路径与界面语言。
  DeleteRegKey HKCU "${MANUPRODUCTKEY}"
!macroend

; =====================================================================
; 默认安装目录：改到 D 盘
;
; 为什么需要这个：模板的 .onInit 在 installMode=currentUser 时把 $INSTDIR 设成
; "$LOCALAPPDATA\${PRODUCTNAME}"（也就是 C 盘）。而模板**没有** preInit 钩子，
; NsisConfig 里也**没有** installDir 配置项（只有 installMode/template/installerHooks
; 等），所以只能在「选择安装目录」页显示的那一刻改默认值。
;
; 时机：MUI2 的目录页 show 回调会 Call MUI_PAGE_CUSTOMFUNCTION_SHOW，并随即
; !undef（不会漏给后面的页）——见 NSIS 的 Contrib\Modern UI 2\Pages.nsh 里的
; MUI_PAGE_FUNCTION_CUSTOM。本文件在模板顶部 include，正好赶在
; "!insertmacro MUI_PAGE_DIRECTORY" 之前，所以在这里 !define 就生效。
;
; 只改「全新安装」的默认：若 .onInit 已通过 RestorePreviousInstallLocation 把
; $INSTDIR 恢复成上次装过的路径（升级/重装），就不动它——否则升级会把已装位置
; 悄悄搬走。判据就是模板那个默认值本身，故这里写字面量。
;
; 两个「不能踩」的坑（都跟 NSIS 的解析顺序有关）：
;   ① 本文件 include 得很早，**不能**引用 ${PRODUCTNAME}/${MANUPRODUCTKEY} 这类
;      后面才 !define 的符号（!define 在解析期展开，会报未定义）。
;   ② 同理不能引用 $mui.DirectoryPage.Directory 这个 Var（它要到目录页声明时才
;      存在），所以这里用 FindWindow/GetDlgItem + 控件 id 1019——MUI2 自己也用这个 id。
;
; 说明：静默安装（/S）与被动模式（/P）会跳过目录页，show 回调不执行，默认仍是
; C 盘；那种场景请显式传 /D=<目标目录>。
; =====================================================================

!ifdef GRIDWRIGHT_DEFAULT_INSTDIR
; 只有构建时显式传 /DGRIDWRIGHT_DEFAULT_INSTDIR=<目录> 才改默认安装位置。
; 不在源码里写死盘符：公开产物不应带有某台机器的目录习惯，
; 没有 D 盘的机器也会被装到不存在的路径。
!define MUI_PAGE_CUSTOMFUNCTION_SHOW GridwrightDefaultInstallDir

Function GridwrightDefaultInstallDir
  StrCmp $INSTDIR "$LOCALAPPDATA\gridwright" 0 gridwright_default_dir_done
    StrCpy $INSTDIR "${GRIDWRIGHT_DEFAULT_INSTDIR}"
    ; 输入框里的文字也得换：离开本页时 NSIS 是从输入框读回 $INSTDIR 的，
    ; 只改 $INSTDIR 而不改输入框，用户点「下一步」时会被旧值覆盖回来。
    FindWindow $R0 "#32770" "" $HWNDPARENT
    GetDlgItem $R1 $R0 1019
    SendMessage $R1 ${WM_SETTEXT} 0 "STR:$INSTDIR"
  gridwright_default_dir_done:
FunctionEnd
!endif
