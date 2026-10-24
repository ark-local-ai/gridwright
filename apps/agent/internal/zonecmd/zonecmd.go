// Package zonecmd 解析对话里的「@区名 运行」指令。
//
// 为什么要有这一层：对话是作业区任务的唯一入口，但把这句话交给模型是有前提的
// ——得先配好模型。没配模型时用户连「试运行一下」都做不到，而这恰恰是最该先
// 用起来的功能。这条指令的语义完全确定（哪个区、真跑还是演习），没有任何需要
// 判断的地方，本地解析就够了，也更省一次调用、不会解析跑偏。
//
// 解析与匹配都是纯函数：不碰磁盘、不碰网络，方便测试，也保证同样的输入永远
// 得到同样的结果——派单这种事不该有惊喜。
package zonecmd

import (
	"strings"
	"unicode"

	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// actions 是动作词，按长度从长到短排列。
//
// 顺序有讲究：「试运行」以「运行」结尾，先匹配「运行」会把前者截成
// 「试」当区名，于是永远找不到作业区。宁可让更具体的词先认领。
var actions = []struct {
	word   string
	dryRun bool
}{
	{"试运行", true},
	{"运行", false},
	{"跑一次", false},
}

// Parse 从一句话里认出「@区名 动作」。
//
// 返回 ref（区名或 ID，已去掉首尾空白）、dryRun（是否为试运行）、ok。
// 不是这条指令就返回 ok=false，调用方继续走原来的模型流程——解析失败必须
// 等同于「不做判断」，而不是报错。
func Parse(text string) (ref string, dryRun bool, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "@") {
		return "", false, false
	}
	rest := strings.TrimSpace(t[1:])
	if rest == "" {
		return "", false, false
	}
	for _, a := range actions {
		if !strings.HasSuffix(rest, a.word) {
			continue
		}
		ref = strings.TrimSpace(strings.TrimSuffix(rest, a.word))
		if ref == "" {
			return "", false, false
		}
		return ref, a.dryRun, true
	}
	return "", false, false
}

// Match 在给定作业区里找 ref 对应的那一个。
//
// 只认两种相等：ID 完全相同，或名称（去空白后）大小写不敏感相等。
// 刻意不做前缀/包含/相似度匹配：工作区里常有「发票目录」「发票目录(旧)」这类
// 名字，模糊匹配会挑错对象，而派单挑错的代价是真改错表，不如直接让用户说清。
func Match(zlist []zones.Zone, ref string) (zones.Zone, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return zones.Zone{}, false
	}
	for _, z := range zlist {
		if z.ID == ref {
			return z, true
		}
	}
	for _, z := range zlist {
		if strings.EqualFold(strings.TrimFunc(z.Name, unicode.IsSpace), ref) {
			return z, true
		}
	}
	return zones.Zone{}, false
}
