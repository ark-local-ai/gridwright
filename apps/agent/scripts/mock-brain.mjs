// 本地 mock「脑」：OpenAI 兼容的 /v1/chat/completions。
//
// 用途：**不配真模型也能验证整条闭环**（对话 → 计划 → 确认 → 执行 → 自检）。
// 它按 prompt 内容区分两种契约：
//   1) 会话契约（assembleChatPrompt 含「# 你在做什么」）→ 回 {reply, proposal}
//   2) 语义计划契约（PlanSemantic）→ 回 {edits, summary, skip_reasons, questions}
//
// 跑法：
//   node apps/agent/scripts/mock-brain.mjs          # 监听 127.0.0.1:7799
// 然后把引擎的设置指向它：
//   curl -X PUT http://127.0.0.1:7700/api/v1/settings -H "Content-Type: application/json" //     -d "{\"baseUrl\":\"http://127.0.0.1:7799/v1\",\"apiKey\":\"mock\",\"model\":\"mock\"}"
import http from "node:http";

const SEMANTIC = {
  edits: [
    {
      sheet: "2023年1月租金 （日）",
      key: { 物业位置: "A201" },
      field: "本月实收",
      op: "add",
      value: 1000,
      reason: "E2E 验证：补记一笔实收",
    },
  ],
  summary: "E2E：A201 本月实收 +1000",
  skip_reasons: [],
  questions: [],
};

const CHAT = {
  reply: "好的，这是改动表：先出一份清单给你确认，你点头我才改。",
  proposal: {
    kind: "plan",
    title: "补记 A201 本月实收 1000",
    detail: "给「2023年1月租金 （日）」表 A201 行的「本月实收」加 1000。",
    action: "记一笔：A201 御龙湾幼儿园 本月实收 1000",
  },
};

const server = http.createServer((req, res) => {
  if (req.method !== "POST" || !req.url.endsWith("/chat/completions")) {
    res.writeHead(404).end("nope");
    return;
  }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    let isChat = false;
    try {
      const parsed = JSON.parse(body);
      const text = (parsed.messages || []).map((m) => m.content || "").join("\n");
      // 会话 prompt 独有「# 你在做什么」；计划 prompt 独有「# 工作区表结构」。别用 # 输出要求（两者都有）。
      isChat = text.includes("# 你在做什么");
    } catch {}
    const payload = isChat ? CHAT : SEMANTIC;
    const out = {
      id: "mock-1",
      object: "chat.completion",
      choices: [
        { index: 0, message: { role: "assistant", content: JSON.stringify(payload) }, finish_reason: "stop" },
      ],
    };
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(out));
  });
});
server.listen(7799, "127.0.0.1", () => console.log("mock brain on 127.0.0.1:7799"));
