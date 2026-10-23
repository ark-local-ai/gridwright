package logx

// 这个文件只放两个给"流程细节"用的打印 helper：
// 内容长、平时不想看的正文（提示词、模型返回的 JSON、改动清单、表格行预览）用 Block，
// "流程走到哪一步"用 Step。
//
// 为什么要单独抽出来：正文直接塞进 Info 会让 engine.log 大到没法看，而**完全不记**又会在
// "模型为什么这么答"的时候两眼一抹黑。所以正文统一走 Debug（默认不出），步骤走 Info。

import (
	"log/slog"
	"strings"
)

// Block 把一段多行内容作为一个**缩进块**写进日志（Debug 级）。
//
// 输出形如（每一个物理行都是独立的 slog 记录，所以每行都有自己的 time=/level=）：
//
//	time=... level=DEBUG msg=提示词
//	time=... level=DEBUG msg="  # 用户说"
//	time=... level=DEBUG msg="  把 D 列的名字对到销售明细表，查不到就新增"
//
// 为什么缩进：slog 的记录是一行纯文本，正文若与记录头同列，人和工具都分不清哪几行属于
// 同一条记录。缩进是唯一不引入额外格式（不改成 JSON、不加分隔符）就能分组的手段。
//
// 为什么是 Debug：这些正文又长又多，默认级别下**不能**进 engine.log ——
// 那是用户会拿去发给别人看的文件，而且一天能涨到几十 MB。
// 调用点如果拼这段正文很贵（比如序列化整张表），先自己判断再拼：
//
//	if logx.Enabled(slog.LevelDebug) {
//		logx.Block("工作表行预览", dump)
//	}
//
// 正文里的空行照缩进保留：表格/JSON 里空行是结构的一部分，丢掉就看不出层级了。
func Block(title, body string) {
	if !Enabled(slog.LevelDebug) {
		return // 关了 debug 就别做切行/缩进这些无用功
	}
	if title = strings.TrimRight(title, "\n"); title != "" {
		slog.Debug(title)
	}
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return
	}
	for _, line := range strings.Split(body, "\n") {
		slog.Debug("  " + line)
	}
}

// Step 记一条流程步骤（Info 级）：flow 是哪条流程（如 plan/chat/apply），
// step 是走到哪一步（如 locate/writeback），kv 是同一条记录上追加的结构化字段，
// 用法与 slog.Info 相同（键值成对，可给多对）。
//
// 为什么把 flow/step 定成固定字段、而不是拼进 msg：一次对话会同时跑澄清、定位、生成、
// 写回好几条流程，光看 msg 分不清谁是谁。固定字段之后可以直接
// `flow=plan step=locate` 精确捞出某条流程的一段，不用读全文。
//
// kv 拼错（少了值/奇数个）不在这里兜：slog 自己会渲染成 !BADKEY/!BADVALUE，
// 调用点的错误在日志里看得见，比悄悄吞掉好。
func Step(flow, step string, kv ...any) {
	args := make([]any, 0, 4+len(kv))
	args = append(args, "flow", flow, "step", step)
	args = append(args, kv...)
	slog.Info("流程步骤", args...)
}
