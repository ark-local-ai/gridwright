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
