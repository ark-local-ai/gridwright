# Gridwright · agent（"手"）

跑在办公机（含 Win7）上的单文件可执行程序：盯住一个工作区文件夹，新数据进来就调云端"脑"（OpenAI 兼容 API）决定怎么改 Excel，执行、记账、发通知。

> 完整规格见 [`../../docs/gridwright-MVP-spec.md`](../../docs/gridwright-MVP-spec.md)。
> 这是 **M1 骨架**：盯文件夹 + 读表 + 调 LLM + 执行 edits + 记账。

## 构建（Go，纯静态、CGO 关闭、无运行时依赖）

```bash
cd apps/agent
go build ./...                          # 开发机（Win）：产出 gridwright.exe
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/gridwright.exe ./cmd/gridwright
```

产物是单个 `gridwright.exe`（~11 MB），拷到目标机即可运行。

> ⚠️ **Win7 注意**：Go 1.22 起把 Windows 7 从"官方支持"降级，1.25 对 Win7 不再保证。若要稳妥支持 Win7，用 Go 1.21.x 交叉编译并在一台真 Win7 上冒烟验证（本仓库默认用 Go 1.25，Win10/11 没问题）。

## 运行

```bash
# 1. 复制配置并填（workspace 指向你的文件夹；API key 建议走环境变量）
copy config.yaml.example config.yaml
set LLM_API_KEY=sk-xxxx        # 或写进 config.yaml 的 llm.api_key
set WORKSPACE=C:\work\台账     # 可选，覆盖 config.yaml

# 2. 跑
gridwright.exe -config config.yaml
```

## 工作区约定（一个文件夹）

```
C:\work\台账\
├── 销售.xlsx          ← 被看管的表（*.xlsx，可多张）
├── inbox\             ← 新数据丢这里（csv / xlsx）
│   └── done\          ← 处理完的文件自动移这里
├── rules.yaml         ← 规则（长期记忆，记事本可编辑；含 forbid 护栏）
├── state.yaml         ← 滚动摘要（LLM 定期重写）
├── ledger.csv         ← 账目（append-only，含旧值→可回滚）
└── archive\           ← 按月 gzip 旧账目（M2+）
```

### 一次运行的流程（spec §4）
```
inbox 出现新文件 / 定时到点
  → 目标表被 Excel 占用（存在 ~$ 锁文件）则跳过本次
  → 组 prompt（表结构+样例、新数据、最近 20 条账目、state.yaml、rules.yaml）
  → 脑返回结构化编辑指令 JSON
  → 校验 forbid 护栏（动了禁止列 → 该条 rejected）
  → 备份原文件（.bak，保留 5 份）→ excelize 执行 edits
  → 追加账目（每格一条，含旧值）→ inbox 文件移入 done\
  → 发通知（M1 打印到日志；M3 接 Server酱/PushPlus/企微）
```

## 里程碑
- **M1（本骨架）**：盯文件夹 + 读表 + 调 LLM + 执行 edits + 记账 ✅
- **M2**：rules.yaml forbid 护栏短路 + 定时触发 + 备份/回滚细化
- **M3**：微信通知 + `127.0.0.1:8080` 状态页（账目/运行状态/开关，go:embed 静态页）
- **M4（后）**：多表关联、失败重试、通知升级（钉钉）

## 目录
- `cmd/gridwright` — 入口：fsnotify 盯 inbox + 轮询兜底 + 优雅退出
- `internal/config` — config.yaml + 环境变量
- `internal/workspace` — 工作区文件夹约定（inbox/done/锁检测）
- `internal/xl` — excelize 读结构/执行 edits/备份
- `internal/llm` — 云端 OpenAI 兼容调用 + prompt 组装
- `internal/memory` — rules.yaml + state.yaml
- `internal/ledger` — append-only 账目
- `internal/notify` — 通知出口（M1 console）
- `internal/agent` — 一次运行的编排核心
- `internal/plan` — LLM↔执行器结构化契约（Edit/Plan/Result）
