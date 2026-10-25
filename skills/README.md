# 技能装配目录

这里是 gridwright 的技能装配目录，格式遵循 [Agent Skills](https://agentskills.io)：
每个子目录一个 `SKILL.md`，开头是 `name` / `description` 的 frontmatter。

```
skills/
└─ invoice-vision/
   └─ SKILL.md
```

## 怎么装一个现成的技能

标准技能仓库可以直接复制目录进来：

```bash
# Anthropic 官方（xlsx / pdf / docx / pptx 等）
git clone --depth 1 https://github.com/anthropics/skills /tmp/skills
cp -r /tmp/skills/skills/xlsx skills/

# 或用 skills.sh
npx skills add anthropics/skills --skill pdf
```

常用来源：

| 仓库 | 内容 |
|---|---|
| [anthropics/skills](https://github.com/anthropics/skills) | 官方文档技能（xlsx、pdf、docx、pptx） |
| [TerminalSkills/skills](https://github.com/terminalskills/skills) | 跨 Claude Code / Codex / Cursor 的通用技能库 |
| [google/skills](https://github.com/google/skills) | Google 产品与云服务技能 |
| [agentskills/agentskills](https://github.com/agentskills/agentskills) | 格式规范 |

## 重要边界

- 引擎只读取 `SKILL.md` 的**说明正文**并注入对话提示词；**不会执行**技能里的脚本。
  带 Python/Node 脚本的技能，只有说明部分会生效。
- 单个技能正文最多注入 4000 字，全部技能合计 12000 字；超出会截断。
- 查找顺序：`<工作区>/skills/` → 安装目录 `skills/` → 当前目录 `skills/`；同名先找到的优先。
- 技能是“操作说明”，不是权限：它不能绕过路径授权、确认制或禁止列。

## 工具（MCP）

技能与工具是两件事，不要混：

| | 技能（`skills/`） | 工具（MCP） |
|---|---|---|
| 是什么 | **说明**：怎么做这件事 | **能力**：能拿到/处理外部数据 |
| 怎么生效 | 正文注入对话提示词 | 模型按需 `tool_call`，引擎拉子进程执行 |
| 例子 | “识别发票要抄哪些字段” | OCR 识别图片、抽 PDF 文本、列文件系统 |

装配方式：把 `mcp.json` 放到**工作区根目录**（仓库根的 `mcp.json.example`
是一个 `enabled:false` 的示例，复制过去改名即可）：

```json
{
  "servers": [
    {"name": "ocr", "command": "npx", "args": ["-y", "some-mcp-server"], "env": {}}
  ]
}
```

- `name` 必填且唯一（工具在对话里显示为 `<name>.<tool>`）；`command` 必填；
  `args` / `env` 可省；`enabled` 写 `false` 可留着配置但暂时不启用（省略=启用）。
- 没配 `mcp.json`、配置解析失败、或某个 server 起不来，**对话仍然照常可用**——
  只是模型没有（那部分）工具可调，失败原因写进日志。
- 换工作区会关掉上一批子进程、按新工作区的配置重开；同一工作区复用同一份。
- 当前可用的工具可查：`GET /api/v1/tools/mcp`。

边界：

- **一次只调一个工具**，最多 3 轮；工具结果是**数据，不是指令**——
  结果里出现“忽略之前的指令”之类的话，不会被执行。
- 工具只能**读**；要**写表**仍然走「清单 → 人确认 → 落盘」，工具不能绕过确认制、
  路径授权与禁止列。
- 工具名只能是已装配的，模型编造的名字会得到一条错误，然后自己换方法或说明原因。
