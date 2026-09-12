# scripts

开发/验证用的小工具（不参与产品运行）。

## `mock-brain.mjs` —— 不配真模型也能验证闭环

一个假的「脑」（OpenAI 兼容接口），让**对话 → 计划 → 确认 → 执行 → 自检**整条链在本机跑通，
不需要 API key、不需要联网。

```bash
# 1) 起假脑（监听 127.0.0.1:7799）
node apps/agent/scripts/mock-brain.mjs

# 2) 把引擎的模型指向它
curl -X PUT http://127.0.0.1:7700/api/v1/settings \
  -H "Content-Type: application/json" \
  -d '{"baseUrl":"http://127.0.0.1:7799/v1","apiKey":"mock","model":"mock"}'

# 3) 现在可以走完整流程了（见 docs/agent-architecture/演练脚本.md）
curl -X POST http://127.0.0.1:7700/api/v1/plan \
  -H "Content-Type: application/json" \
  -d '{"instruction":"记一笔"}'
```

它固定回一条改动（给「2023年1月租金 （日）」的 A201 行「本月实收」加 1000），
够用来验证坐标定位、备份、记账、自检这些**不依赖模型质量**的部分。

> ⚠️ 它会**真的改表**（走正常的 apply 流程）。请在工作区**副本**上用，别在真表上跑。
