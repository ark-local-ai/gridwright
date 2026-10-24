package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
	"github.com/ark-local-ai/ark/apps/agent/internal/logx"
	"github.com/ark-local-ai/ark/apps/agent/internal/trace"
	"github.com/ark-local-ai/ark/apps/agent/internal/zonecmd"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// 会话接口（见 docs/agent-architecture/7-对话与自动化任务.md）。

// handleConversations GET /api/v1/conversations —— 列出本工作区的会话
func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": store.List()})
}

// handleConversation GET/DELETE/PUT /api/v1/conversation?id=
//   - GET    取一条会话（含消息）
//   - PUT    改标题（{title}）——记录要能翻回来，就得能起个记得住的名字
//   - DELETE 删掉这条记录
func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.URL.Query().Get("id")
	switch r.Method {
	case http.MethodGet:
		c := store.Get(id)
		if c == nil {
			writeErr(w, http.StatusNotFound, "会话不存在")
			return
		}
		writeJSON(w, http.StatusOK, c)
	case http.MethodPut:
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式错误")
			return
		}
		c, err := store.Rename(id, req.Title)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "title": c.Title})
	case http.MethodDelete:
		if err := store.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/PUT/DELETE")
	}
}

type chatReq struct {
	ConversationID string `json:"conversationId"`
	Message        string `json:"message"`
	// Images 是随消息附的图（data URL，形如 "data:image/png;base64,..."）。
	// 走 JSON 而不是 multipart：一张截图通常几百 KB，data URL 足够；
	// 而且要跟着会话一起落库才能"回看当时给的是什么图"。
	Images []string `json:"images,omitempty"`
	// ZoneID 是当前派单的作业区（可选）。作用只是给模型一行上下文，
	// 真正的写操作仍走 /plan → /apply 并带 zone_id。
	ZoneID string `json:"zoneId,omitempty"`
}

// imgsForStore 过滤出合法的图片 data URL。
//
// 单独成函数是为了**只校验一次**：落库与送模型必须看到同一份数据，
// 否则会出现"库里存了 3 张、模型只收到 2 张"这种对不上的情况。
// 只放行 data:image/ 前缀——不信任客户端传来的任意 URL（否则等于让它
// 通过我们的密钥去访问任意地址）。
func imgsForStore(in []string) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		if strings.HasPrefix(d, "data:image/") {
			out = append(out, d)
		}
	}
	return out
}

// handleChat POST /api/v1/chat —— 说一句话，得到回话 + 结构化建议
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeErr(w, http.StatusBadRequest, "缺少 message")
		return
	}
	// 接口层只记"带了什么"（字数/图数/会话），具体步骤在 agent 层 flow=chat 里。
	logx.Step("api.chat", "enter", "conversation", req.ConversationID,
		"chars", len([]rune(req.Message)), "images", len(req.Images), "zone", req.ZoneID)
	cfg, layout, led, _ := s.cur()
	store, err := s.convoStore()
	if err != nil {
		logx.Step("api.chat", "exit", "conversation", req.ConversationID, "err", "打开会话存储失败："+err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 取或建会话，先把用户的话记下来
	c, err := store.Ensure(req.ConversationID, "")
	if err != nil {
		logx.Step("api.chat", "exit", "conversation", req.ConversationID, "err", "准备会话失败："+err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 把**本轮之前**说过的话拷一份当历史。
	//
	// 为什么要在 Append 之前拷：c 指向存储里的同一条记录（Ensure 返回的是切片元素指针），
	// Append 会往同一个切片里追加，本轮的这句话就会混进历史——模型会看到“自己还没回答
	// 的、用户刚说的话”被当成上一轮，那正是重复追问的成因。所以先剪好再追加。
	history := append([]convo.Message(nil), c.Messages...)
	if _, err := store.Append(c.ID, convo.Message{Role: convo.RoleUser, Text: req.Message, Images: imgsForStore(req.Images)}); err != nil {
		logx.Step("api.chat", "exit", "conversation", c.ID, "err", "存用户消息失败："+err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 「@区名 运行 / 试运行」走本地解析，**在检查模型之前**。
	//
	// 顺序是有意的：这句话的语义完全确定，不需要模型判断；而它恰恰是没配模型
	// 时用户唯一能用的入口。放到 BrainReady 后面，就等于「想试运行，先配模型」，
	// 把最该先跑通的事挡在门外。
	if ref, dryRun, ok := zonecmd.Parse(req.Message); ok {
		zlist := []zones.Zone{}
		if st := s.zonesStore(); st != nil {
			zlist = st.List()
		}
		z, hit := zonecmd.Match(zlist, ref)
		if !hit {
			msg := "没有匹配的作业区：" + ref
			_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleSystem, Text: msg})
			logx.Step("chat", "@zone", "conversation", c.ID, "ref", ref, "err", "未找到")
			writeErr(w, http.StatusNotFound, msg)
			return
		}
		t, err := s.submitZoneTask(z.ID, dryRun, "chat", c.ID)
		if err != nil {
			_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleSystem, Text: "出错：" + err.Error()})
			logx.Step("chat", "@zone", "conversation", c.ID, "zone", z.Name, "err", err.Error())
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 回话写的是**真正生效**的模式：submitZoneTask 会把非 apply 的区强制成演习，
		// 这里若照抄用户说的「运行」，界面就会显示一件没发生的事。
		mode := "运行"
		if t.DryRun {
			mode = "试运行"
		}
		text := fmt.Sprintf("已加入任务队列：%s（%s）", z.Name, mode)
		updated, err := store.Append(c.ID, convo.Message{Role: convo.RoleAgent, Text: text})
		if err != nil {
			logx.Step("chat", "@zone", "conversation", c.ID, "err", "存回话失败："+err.Error())
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		logx.Step("chat", "@zone", "conversation", c.ID, "zone", z.Name, "task", t.ID, "dryRun", t.DryRun)
		writeJSON(w, http.StatusOK, map[string]any{"conversationId": c.ID, "conversation": updated, "task": t})
		return
	}

	if !cfg.BrainReady() {
		logx.Step("api.chat", "exit", "err", "未配置模型")
		writeErr(w, http.StatusBadRequest, "还没配置模型（脑）：请在设置里填 base_url 与 api_key 后就能对话了")
		return
	}

	// 问脑：告诉它工作区里**有哪些表**。
	//
	// 关键（实测踩过的坑）：只说文件名是不够的。用户说"填进销售明细表"时，
	// 「销售明细表」是一个 **sheet 名**，而当时 prompt 里只有
	// 《御城二期销售报表2026.09.13.xlsx》——模型找不到这个名字，只能回一句
	// "工作区里没有名为销售明细表的文件，请确认"，把一件能做的事问成了澄清。
	// 一个工作区多个 xlsx、一个 xlsx 多个 sheet 是常态，所以清单必须到 sheet 层。
	files, _ := layout.DataFiles()
	names := workspaceInventory(files)
	// trace 收「这一步做了什么」，随响应回给界面（运行详情）。
	ctx, tr := trace.New(r.Context(), "chat")
	ag := agent.New(cfg, layout, led, s.brainClient())
	imgs := make([]llm.ImageInput, 0, len(req.Images))
	for _, d := range imgsForStore(req.Images) {
		imgs = append(imgs, llm.ImageInput{DataURL: d})
	}
	// 作业区上下文只加在**送给模型**的这句里，不写进会话原文：
	// 用户说的是“补一下这个月的”，历史里不该多出一句自己没说过的话。
	chatText := req.Message
	zoneLines := []string{}
	if st := s.zonesStore(); st != nil {
		for _, z := range st.List() {
			zoneLines = append(zoneLines, fmt.Sprintf("%s｜%s｜输入=%s｜目标=%s｜mode=%s",
				z.ID, z.Name, z.Input.Type, z.Target.File, z.Mode))
		}
	}
	if req.ZoneID != "" {
		st := s.zonesStore()
		z, ok := zones.Zone{}, false
		if st != nil {
			z, ok = st.Get(req.ZoneID)
		}
		if !ok {
			writeErr(w, http.StatusNotFound, "zone 不存在："+req.ZoneID)
			return
		}
		chatText = fmt.Sprintf("（当前作业区：%s；目标表：%s）\n%s", z.Name, z.Target.File, req.Message)
	}
	reply, err := ag.ChatWithImages(ctx, chatText, names, imgs, history, zoneLines...)
	if err != nil {
		// 记下失败，避免对话看起来"没反应"
		_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleSystem, Text: "出错：" + err.Error()})
		logx.Step("api.chat", "exit", "conversation", c.ID, "err", err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := store.Append(c.ID, *reply)
	if err != nil {
		logx.Step("api.chat", "exit", "conversation", c.ID, "err", "存回话失败："+err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	logx.Step("api.chat", "exit", "conversation", c.ID, "messages", len(updated.Messages))
	writeJSON(w, http.StatusOK, map[string]any{"conversationId": c.ID, "conversation": updated, "trace": tr})
}

// convoStore 取当前工作区的会话存储（跟着工作区走）。
//
// 返回 Server 上共享的那一份实例，而不是每次新开一个：Store 的互斥锁只在实例内有效，
// 每个请求各开一个就等于没上锁——并发追加会各自读文件、各自整份写回，后写的覆盖先写的，
// 消息就丢了。切换工作区时指针会被换掉，所以要在读锁内取。
func (s *Server) convoStore() (*convo.Store, error) {
	s.mu.RLock()
	st := s.convos
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("会话存储不可用")
	}
	return st, nil
}

// sourceTitles 会返回「会话 id → 标题」。账目里的 source 写的是 "对话:<id>"，
// 展示时要译回标题——用户看的是「哪次对话让我改的」，不是一串 id。
// 标题会改，id 不会，所以账目里只存 id，这里现查。
func (s *Server) sourceTitles() map[string]string {
	out := map[string]string{}
	store, err := s.convoStore()
	if err != nil {
		return out
	}
	for _, c := range store.List() {
		out[c.ID] = c.Title
	}
	return out
}

// handleDistill POST /api/v1/conversation/distill?id=
//
// 把一段**记录**提炼成**记忆候选**。这是两个概念之间的那道闸门：
// 记录是全自动的流水，记忆要精炼、会过期、必须人点头。
// 本接口只读——它返回候选，不写任何东西；落盘走 POST /api/v1/memory（需 approve）。
//
// 为什么要单独一步而不是自动沉淀：记忆是长期资产，一条错的记忆会污染以后
// 所有判断，而用户很难事后查出是哪条带偏的。所以让人逐条过目。
func (s *Server) handleDistill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	cfg, layout, led, _ := s.cur()
	// 先查"这条记录在不在"，再查模型配没配。
	//
	// 顺序是有意的：两者都可能出错，但**记录不存在**意味着界面上的东西已经
	// 过期了（点了一个已被删掉的会话），这时"去配模型"根本解决不了他的问题，
	// 反而把人支到错的方向。所以先报那个更具体、更贴近他这次点击的错。
	id := r.URL.Query().Get("id")
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	c := store.Get(id)
	if c == nil {
		writeErr(w, http.StatusNotFound, "会话不存在")
		return
	}
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest, "还没配置模型（脑）：提炼记忆需要模型判断")
		return
	}
	ag := agent.New(cfg, layout, led, s.brainClient())
	logx.Step("api.chat", "提炼记忆 enter", "conversation", c.ID)
	cands, err := ag.Distill(r.Context(), c)
	if err != nil {
		logx.Step("api.chat", "提炼记忆 exit", "conversation", c.ID, "err", err.Error())
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cands == nil {
		cands = []agent.DistillCandidate{}
	}
	logx.Step("api.chat", "提炼记忆 exit", "conversation", c.ID, "candidates", len(cands))
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationId": c.ID,
		"title":          c.Title,
		"candidates":     cands,
	})
}
