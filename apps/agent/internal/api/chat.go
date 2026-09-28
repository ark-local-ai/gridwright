package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/agent"
	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/llm"
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
	cfg, layout, led, _ := s.cur()
	if !cfg.BrainReady() {
		writeErr(w, http.StatusBadRequest, "还没配置模型（脑）：请在设置里填 base_url 与 api_key 后就能对话了")
		return
	}
	store, err := s.convoStore()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 取或建会话，先把用户的话记下来
	c, err := store.Ensure(req.ConversationID, "")
	if err != nil {
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	ag := agent.New(cfg, layout, led, s.brainClient())
	imgs := make([]llm.ImageInput, 0, len(req.Images))
	for _, d := range imgsForStore(req.Images) {
		imgs = append(imgs, llm.ImageInput{DataURL: d})
	}
	reply, err := ag.ChatWithImages(r.Context(), req.Message, names, imgs, history)
	if err != nil {
		// 记下失败，避免对话看起来"没反应"
		_, _ = store.Append(c.ID, convo.Message{Role: convo.RoleSystem, Text: "出错：" + err.Error()})
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := store.Append(c.ID, *reply)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversationId": c.ID, "conversation": updated})
}

// convoStore 取当前工作区的会话存储（跟着工作区走）。
func (s *Server) convoStore() (*convo.Store, error) {
	_, layout, _, _ := s.cur()
	return convo.Open(layout.Root)
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
	cands, err := ag.Distill(r.Context(), c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cands == nil {
		cands = []agent.DistillCandidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversationId": c.ID,
		"title":          c.Title,
		"candidates":     cands,
	})
}
