package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestConcurrentConversationAppend 并发向同一条会话追加消息，验证没有丢更新。
//
// 回归背景：convoStore() 曾经每次请求都新开一个 *convo.Store，而 Store 的互斥锁
// 只在实例内有效——两个并发请求各自「读 conversations.json → 内存追加 → 整份写回」，
// 后写的会把先写的覆盖掉，消息就这么丢了。修复后全进程共享一份实例，锁才真正生效。
func TestConcurrentConversationAppend(t *testing.T) {
	s := newTestServer(t)
	store, err := s.convoStore()
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Ensure("", "t")
	if err != nil {
		t.Fatal(err)
	}

	const n = 20
	h := s.Handler()

	// 并发发请求：总之不能在 goroutine 里调用 t.Fatal/FailNow（那只对发起它的
	// goroutine 有效），所以失败先收集，等 Wait 之后再统一断言。
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := make([]string, 0, n)
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("并发消息-%02d", i)
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			body, err := json.Marshal(map[string]string{
				"conversationId": c.ID,
				"text":           text,
				"role":           "agent",
			})
			if err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s：编码请求失败 %v", text, err))
				mu.Unlock()
				return
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
				"/api/v1/conversation/append", strings.NewReader(string(body))))
			if rec.Code != http.StatusOK {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s：状态 %d %s", text, rec.Code, rec.Body.String()))
				mu.Unlock()
			}
		}(text)
	}
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("并发追加有 %d 个请求失败：%v", len(failures), failures)
	}

	// 严格断言：20 条一条不少。旧代码（每请求新开 store）会在这里挂掉。
	msgs := readConvo(t, s, c.ID)
	if len(msgs) != n {
		t.Fatalf("并发追加后应有 %d 条消息，实际 %d 条（丢了 %d 条）", n, len(msgs), n-len(msgs))
	}
	got := make(map[string]bool, n)
	for _, m := range msgs {
		got[m.Text] = true
	}
	for i := 0; i < n; i++ {
		text := fmt.Sprintf("并发消息-%02d", i)
		if !got[text] {
			t.Fatalf("消息 %q 丢失；实际收到 %+v", text, msgs)
		}
	}
}
