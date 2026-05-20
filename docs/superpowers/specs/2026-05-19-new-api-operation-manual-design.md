# NewAPI 操作手册 架构设计

> 日期：2026-05-19  ｜  状态：待评审  ｜  版本：v1
> 关联 PRD：`docs/superpowers/specs/2026-05-19-new-api-operation-manual-prd.md`

## 1. 背景与目标

### 1.1 业务背景

NewAPI 是聚合 50+ AI 上游供应商的网关系统。管理员需要完成两类高频操作：

1. **接入新供应商**：选类型、填密钥、配模型、配权重/优先级。
2. **调整计费**：在「模型倍率 / 分组倍率 / 用户组特殊倍率 / 按次模型价 / 表达式分段计费」等多种机制中选择并组合。

PRD 第 8 节明确指出："需要架构师确认 `relay` 模块中最新的倍率计算优先级逻辑"。本设计在第 3 节给出对该问题的代码级权威回答（含证据），并据此组织手册结构。

### 1.2 目标（来自 PRD）

- 文档覆盖：渠道、计费两大核心模块 100% 覆盖。
- 自愈能力：管理员能在不联系技术支持的前提下完成新增渠道与调价。
- 准确性：内容与当前主线代码逻辑一致；版本范围明确。

### 1.3 合规约束（来自 PRD §7）

- 上游政策：手册必须提示管理员遵守上游服务商的区域使用政策（如 OpenAI 不向某些地区提供 API、Claude 的内容政策）。
- 敏感信息：所有示例中的 API Key、Webhook Secret 必须使用掩码（`sk-***`、`***`），禁止粘贴真实密钥。
- 数据本地化：手册说明中需提示"渠道 BaseURL 选择会影响数据出境路径"，由管理员根据业务所在地的合规要求自行评估。

## 2. 现状与差距

### 2.1 已具备的能力（基于代码探索）

| 能力 | 代码位置 | 现状 |
| --- | --- | --- |
| 渠道类型注册 | `constant/channel.go`、`web/default/src/features/channels/constants.ts:24-79` | 50+ 类型已枚举；前端有显示顺序 |
| 渠道数据模型 | `model/channel.go:23-60`（`Channel` 结构体） | 含 Type、Key、Name、BaseURL、Group、Models、ModelMapping、Priority、Weight、Tag、Setting、ParamOverride、HeaderOverride、ChannelInfo |
| 多 Key 模式 | `model/channel.go:62-70`（`ChannelInfo`） | 已支持 key 状态轮询、禁用原因、禁用时间 |
| Token 计费主路径 | `relay/helper/price.go:67-164`、`service/text_quota.go:200-310` | 预扣 + 结算两阶段；decimal 精确计算 |
| 按次计费 | `relay/helper/price.go:167-225`（`ModelPriceHelperPerCall`） | MJ、Task 等 |
| 分段表达式计费 | `relay/helper/price.go:241-306`、`pkg/billingexpr/` | 已落地，文档在 `pkg/billingexpr/expr.md` |
| 倍率配置入口 | `web/default/src/features/system-settings/billing/section-registry.tsx` | 分 6 节：quota / currency / model-pricing / group-pricing / payment / checkin |
| 自动分组 | `setting/auto_group.go`、`relay/helper/price.go:46-50` | 上下文中可注入 `auto_group` 切换计费分组 |

### 2.2 差距

- 缺少**面向管理员的端到端手册**：现有 `docs/` 目录下仅有 `installation`、`openapi`、`channel/`（少量）、`images`，没有覆盖渠道接入与计费配置的完整 SOP。
- 缺少**计费机制的统一概念图**：四种计费机制（按次价 / 按 token 倍率 / 表达式分段 / 缓存补全等次级倍率）的"何时用谁、谁覆盖谁"散落在 `price.go` 与 UI 注释中，对运维不友好。
- 缺少**JSON 配置范式**：`Setting`、`ParamOverride`、`HeaderOverride`、`ModelMapping`、`GroupRatio`、`GroupGroupRatio`、`UserUsableGroups`、`GroupSpecialUsableGroup` 等字段的 JSON 范式没有集中文档。

## 3. relay 模块计费"优先级"权威说明（PRD §8 要求验证项）

**关键纠正**：PRD 第 5 节给出的公式 `(PromptTokens * ModelRatio + CompletionTokens * ModelRatio * CompletionRatio) * GroupRatio` 是**简化版**；与代码不完全等价。完整公式与"优先级"逻辑如下。

### 3.1 三种"优先级"对应三件不同的事

| 关系 | 类型 | 代码证据 | 说明 |
| --- | --- | --- | --- |
| **Model Price 与 Model Ratio** | **互斥优先**（前者命中即跳过后者） | `relay/helper/price.go:67-121`（`ModelPriceHelper`），`ratio_setting/model_ratio.go:367-389` | 先 `GetModelPrice`；命中（`usePrice=true`）则 `quota = ModelPrice × QuotaPerUnit × GroupRatio`，**完全不用 ModelRatio**。 |
| **GroupGroupRatio 与 GroupRatio** | **覆盖优先**（特殊倍率覆盖普通倍率） | `relay/helper/price.go:39-65`（`HandleGroupRatio`），`ratio_setting/group_ratio.go:84-103` | `GetGroupGroupRatio(userGroup, usingGroup)` 命中则整体覆盖；否则用 `GetGroupRatio(usingGroup)`。 |
| **Model Ratio 与 Group Ratio** | **相乘关系**（不是优先级） | `service/text_quota.go:228`（`ratio := dModelRatio.Mul(dGroupRatio)`） | 二者地位平等，按 token 计费时同时乘入。 |

### 3.2 完整结算公式（按 token 计费分支）

代码引用：`service/text_quota.go:200-310`（`calculateTextQuotaSummary`）。

```
let baseTokens = PromptTokens
              − CacheTokens                          // 缓存读
              − CacheCreationTokens                  // 缓存写
              − ImageTokens
              − AudioTokens                          // 当 AudioInputPrice > 0 时

promptQuota   = baseTokens
              + CacheTokens         × CacheRatio
              + CacheCreationTokens × CacheCreationRatio   (拆分为 5m/1h 时分段乘)
              + ImageTokens         × ImageRatio
completionQuota = CompletionTokens × CompletionRatio

quota = (promptQuota + completionQuota) × ModelRatio × GroupRatio
      + ToolCallSurcharge
      + AudioInputQuota         // 当 AudioInputPrice > 0：单独按 $/1M 价格 × GroupRatio × QuotaPerUnit
∀ otherRatio in OtherRatios:  quota *= otherRatio
```

补充规则：
- Claude Usage 语义路径下 `baseTokens` 不再扣减（`text_quota.go:237-247`），按上游已结构化的 usage 字段计入。
- 若 `ratio ≠ 0` 但 `quota ≤ 0`，强制 `quota = 1`（避免免费模型被误标）。
- 若 `TotalTokens == 0`，强制 `quota = 0`。

### 3.3 按次计费分支（`ModelPriceHelper` `usePrice=true`）

```
quota = ModelPrice × QuotaPerUnit × GroupRatio
      + ToolCallSurcharge
      + AudioInputQuota
∀ otherRatio in OtherRatios:  quota *= otherRatio
```

### 3.4 表达式分段计费（TieredExpr）

代码引用：`relay/helper/price.go:241-306`，详见 `pkg/billingexpr/expr.md`。

```
rawCost     = expr(p, c, ...)        // 单位：$/1M
quotaBefore = rawCost / 1_000_000 × QuotaPerUnit
quota       = round(quotaBefore × GroupRatio)
```

注意：表达式内部不要再把 `GroupRatio` 变量写进表达式，否则会双重相乘。

### 3.5 兜底与未配置

- `GetModelRatio` 未命中：若开启自用模式（`SelfUseModeEnabled`）返回 37.5 + `success=true`；否则返回 `success=false`，触发 `modelPriceNotConfiguredError`，前端报"模型 X 的价格未配置"——见 `relay/helper/price.go:20-33`。
- `GetGroupRatio` 未命中：日志告警 + 默认返回 `1.0`（`setting/ratio_setting/group_ratio.go:84-91`）。
- `GroupRatio == 0` 且 `EnableFreeModelPreConsume == false` 时，预扣额度被强制置 0（标记 `freeModel=true`）。

### 3.6 一句话总结（写入手册首页）

> **结算价 = 模型成本（ModelPrice 或 ModelRatio×Tokens 二选一）× 分组倍率（GroupGroupRatio 或 GroupRatio 二选一，前者优先覆盖）。**
> ModelPrice 与 ModelRatio 互斥；GroupGroupRatio 覆盖 GroupRatio；ModelRatio 与 GroupRatio 是相乘，不是优先级。

## 4. 候选方案

### 方案 A：纯任务驱动（PRD §3 方案 A）

**结构**：以「我要做什么」组织内容（如何添加 OpenAI 渠道 / 如何调整 gpt-4 倍率 / 如何为 enterprise 分组设置特价）。

- 优点：上手快，符合直觉；适合"按谷歌搜索来手册"的运维场景。
- 缺点：计费叠加逻辑（缓存倍率 × 模型倍率 × 分组倍率 × 特殊倍率）会被切碎在多个任务里，**不可避免地重复**；运维容易在排错时只看到局部，错误归因。
- 放弃 A 的代价：失去了"按搜索结果直达答案"的快感，运维需要先读概念。

### 方案 B：纯模块参考（PRD §3 方案 B）

**结构**：按系统菜单（渠道管理 / 计费设置 / 用户管理）逐字段说明。

- 优点：结构稳定，方便作为字典查阅；与 UI 一一对应。
- 缺点：跨模块联动（渠道 Group 字段 ↔ 分组倍率 ↔ 用户分组）不易理解；"为什么扣费不符合预期"这类问题在字典里找不到答案。
- 放弃 B 的代价：失去了"逐字段对照 UI 配置"的精确指南。

### 方案 C：混合结构（推荐，与 PRD §4 一致）

**结构**：四层递进 + 一层附录。

```
第一部分  概念基础       —— 计费公式 / 渠道与分组的关系图（覆盖 A 缺失的"全局观"）
第二部分  任务 SOP       —— 渠道：5 个 SOP / 计费：4 个 SOP（A 的优势）
第三部分  字段参考字典    —— 所有 JSON 字段、UI 选项的字典（B 的优势）
第四部分  排错与 FAQ     —— 把易错场景集中到一处
附录      JSON 范式集     —— PRD §6 要求的 ≥3 个示例集中存放
```

- 优点：先读概念再做操作再查字典，覆盖三类用户旅程（新手 / 操作 / 排错）；字段字典作为单一事实来源，减少正文重复。
- 缺点：篇幅最长；维护工作量上升。
- 放弃 C 的代价：除非 PRD 改写，否则放弃 C 等于放弃满足 PRD §4。

## 5. 推荐方案与理由

**采用方案 C（混合结构）**，与 PRD §4 完全一致。

| 对比维度 | A（任务） | B（模块） | C（混合，推荐） |
| --- | --- | --- | --- |
| 上手难度 | 低 | 中 | 中（先读概念再做） |
| 排错效率 | 中（场景碎片） | 低（字典找不到根因） | **高**（概念 + 排错章独立） |
| 长期维护成本 | 中 | 低 | 高（但有清晰边界，可拆 PR 维护） |
| 篇幅估算 | ~6k 字 | ~5k 字 | ~10k 字 |
| 与 PRD §4 一致 | 否 | 否 | **是** |
| 代码-文档一致性维护点 | 多处 | 少处 | **集中在第三部分字典 + 附录** |

## 6. 文档架构与组件拆分

### 6.1 文件组织

```
docs/operation-manual/
├── README.md                       # 入口与版本声明（适配 v1.0.0+）
├── 01-concepts/
│   ├── billing-formula.md         # §3 全文 + 一句话总结
│   ├── channel-group-relations.md # 渠道-分组-用户的关系图
│   └── glossary.md                # 术语表
├── 02-tasks-channels/
│   ├── add-channel-openai.md      # SOP-C1：添加 OpenAI 渠道
│   ├── add-channel-claude.md      # SOP-C2：添加 Anthropic 渠道
│   ├── add-channel-azure.md       # SOP-C3：添加 Azure（含 deployment）
│   ├── batch-test.md              # SOP-C4：批量测试 / 自动禁用
│   └── model-mapping.md           # SOP-C5：模型映射
├── 02-tasks-billing/
│   ├── set-model-ratio.md         # SOP-B1：调整模型倍率
│   ├── set-group-ratio.md         # SOP-B2：调整分组倍率
│   ├── set-special-ratio.md       # SOP-B3：用户组+目标组特殊倍率
│   └── troubleshoot-quota.md      # SOP-B4：扣费不符预期排错
├── 03-reference/
│   ├── channel-fields.md          # Channel 结构体每字段说明 + UI 入口
│   ├── billing-fields.md          # ModelRatio/GroupRatio/CacheRatio... 全字段
│   └── error-codes.md             # 计费 / 渠道相关错误码
├── 04-faq/
│   └── faq.md
└── appendix/
    ├── json-examples.md           # ≥3 个完整 JSON 范式（PRD §6 要求）
    └── api-reference.md           # 引用 docs/openapi/
```

### 6.2 各章组件细化

**第一部分（concepts）**：每页 ≤300 字 + 1 张关系图 / 公式块。重点是 §3.6 的"一句话总结"。

**第二部分（tasks）**：每个 SOP 严格遵循统一模板：
1. **目标**（一句话）
2. **前置条件**（含权限要求）
3. **操作步骤**（带 UI 截图占位符 + 字段填充示例）
4. **关键字段**（链接到 §6.1 字段字典）
5. **验证方式**（如何确认操作成功）
6. **常见错误**（链接到 04-faq）

**第三部分（reference）**：表格驱动，每行 = `字段名 | 类型 | UI 入口 | 取值范围 | 默认值 | 代码出处`。

**第四部分（faq）**：以 PRD §5 的"为什么扣费不符合预期"等真实场景为骨架。

**附录**：≥3 个完整 JSON 配置范式（PRD §6 强制要求）：
- 渠道 `Setting` JSON
- 渠道 `ParamOverride` / `HeaderOverride` JSON
- `GroupGroupRatio` / `GroupSpecialUsableGroup` JSON
- 模型映射 JSON

## 7. 技术选型

| 项 | 选择 | 拒绝候选 | 拒绝理由 |
| --- | --- | --- | --- |
| **文档格式** | Markdown | reStructuredText / AsciiDoc | 仓库已有 `docs/`、`README.md` 全是 MD；前端 i18n 工具与 PR 流程都对 MD 友好。 |
| **目录位置** | `docs/operation-manual/` | `docs/admin-guide/` / `docs/zh-CN/` | 与现有 `docs/installation/`、`docs/openapi/` 同级，命名直白；不混入语言子目录避免与 i18n 翻译流程冲突。 |
| **多语言** | 主体中文，关键术语保留英文 | 双语并排 / 英文先行 | PRD 与现有 `README.zh_CN.md`、错误信息中文为主；双语并排会让 SOP 步骤过长。后续若有英文需求，按 `docs/operation-manual/en/` 子目录新增。 |
| **示例展示** | Fenced code block + JSON | YAML / TOML | 系统配置接口为 JSON（见 `setting/ratio_setting/*.go` 的 `JSONString` 序列化）；YAML 会引入歧义。 |
| **截图** | 占位符 + 后续补图 | 实拍图 / Mermaid 全替代 | 截图必须由具备测试环境的人补充；占位符让设计阶段就锁定"图位置"，避免后续返工。Mermaid 仅用于关系图。 |
| **图表** | Mermaid（关系图、流程图） | PlantUML / 飞书画板 | 仓库已用 GitHub 渲染；Mermaid 是 GFM 内置；PlantUML 需要额外 server。 |
| **版本声明** | 在每页页眉标注"适用版本：v1.0.0+，最后核对：2026-05-19" | 全局一处声明 | PRD §9 风险点要求"标注适用的版本范围"；分页声明可让局部更新（如某 SOP 改版）不影响全局。 |
| **校验机制** | `docs/operation-manual/CHECKLIST.md`（清单） + CI 中的 markdown-lint | 自动化文档生成 | 自动化生成（如从 Swagger）覆盖不到 SOP 的"管理员视角"步骤；用清单 + lint 是最低成本的可执行保障。 |

## 8. 依赖与风险

### 8.1 依赖

| 依赖项 | 类型 | SLA / 状态 | 风险 | 缓解 |
| --- | --- | --- | --- | --- |
| `relay/helper/price.go` 计费实现 | 内部代码 | 稳定（最近 commit 显示仅在 tieredExpr 上有动作） | 主线代码若调整公式，文档过时 | §8.2 风险 R1 |
| `setting/ratio_setting/*` 倍率定义 | 内部代码 | 稳定 | 倍率类型新增（如 ImageCacheRatio）未同步 | §8.2 风险 R2 |
| `pkg/billingexpr/expr.md` | 内部文档 | 已存在，由该子系统维护 | 文档版本可能滞后 | 引用而非复制 |
| `web/default/src/features/system-settings/billing/section-registry.tsx` UI 结构 | 内部代码 | 稳定 | UI 重构会导致截图与"在哪里配置"过时 | 截图占位 + CI lint 提示 |
| `web/default/src/features/channels/constants.ts` 渠道类型枚举 | 内部代码 | 稳定但持续新增（最新已到 type=57 Codex） | 新渠道类型出现，手册未补 SOP | §8.2 风险 R3 |
| 上游服务商政策（OpenAI、Anthropic、Azure 等） | 外部 | 不可控 | 供应商区域政策变化导致 SOP 失效 | 在 SOP 中显式注明"请以上游官方文档为准" |

跨地区延迟与合规约束（PRD §7）：手册需要在"添加渠道"SOP 的 BaseURL 字段说明中提示"自建反代/数据出境的合规审查由管理员负责"。

### 8.2 风险表

| ID | 触发条件 | 影响范围 | 概率 | 缓解方案 | 失败回退 |
| --- | --- | --- | --- | --- | --- |
| R1 | `relay/helper/price.go` 公式改动（如新增第 N 类倍率） | 第一部分概念 + 第三部分字段字典 | 中 | CI lint 在 `relay/helper/price.go` 改动且 `docs/operation-manual/` 未改动时输出 warning（非阻塞） | 在 README 加显著提示"如发现公式与代码不符以代码为准" |
| R2 | 新增倍率字段（如 VideoRatio） | 字段字典 + 公式 | 中 | 同 R1 | 同 R1 |
| R3 | 新增渠道类型 | 第二部分 SOP | 高（每季度可能有 1-2 个新渠道） | 维护"通用 SOP 模板"作为兜底；SOP 模板覆盖未列出的渠道 | 通用模板兜底，不阻塞用户 |
| R4 | UI 重构（如 Section 重新分组） | 第二、三部分截图 | 低 | 截图统一占位；正文用"系统设置 → 计费设置 → 模型定价"等语义路径而非组件名 | 升级时手册重拍 |
| R5 | 翻译滞后导致中英文 UI 不一致 | 全文 | 中 | 仅维护中文版；术语章节列出 EN/ZH 对照 | 后续版本增 EN |
| R6 | 截图泄露真实 API Key 或 PII | 合规 | 低 | 在贡献流程清单中强制"截图前用占位符替换 Key" | 发现后立即移除并补强 lint |
| R7 | "三次失败规则"被触发（同一文档结构反复返工） | 进度 | 低 | 在 §10 待澄清项中保留升级通道 | 升级到 PM 重审 PRD |

## 9. 工期估算

**估算前提（假设条件）**：
- 由 1 名技术写作 + 1 名兼职管理员（提供截图与验证）共同执行；
- v1.0.0 主线代码不发生破坏性变更；
- 截图由验证环境提供，不需要架构师补 POC。

| 模块 | 估时（人天） | 说明 |
| --- | --- | --- |
| 第一部分 概念基础（3 文件） | 2 | 公式段已在 §3 完成 70%，迁移 + 关系图绘制 |
| 第二部分 渠道 SOP（5 文件） | 5 | 每个 SOP ≈1 人天（含截图 + 校对） |
| 第二部分 计费 SOP（4 文件） | 4 | 同上 |
| 第三部分 字段字典（3 文件） | 3 | 字段从 `model/channel.go` + `setting/ratio_setting/*.go` 反查整理 |
| 第四部分 FAQ（1 文件） | 1 | 从历史 issue 与 §3.6 推导 |
| 附录 JSON 范式 + API ref | 1 | 已有素材 |
| README + CHECKLIST + CI lint | 1 | |
| 评审 + 返工 buffer（30%） | 5 | PRD §10 P0 优先级，buffer 不下调 |
| **合计** | **22 人天** | |

按 1.5 人 FTE 计算 → **约 3 周日历时间**。

## 10. 演进路径

PRD §10 已说明"第一版聚焦于费率与渠道，后续版本增加令牌管理与数据看板说明"。本设计为后续版本预留的兼容性：

| 下一步扩展 | 兼容性评估 |
| --- | --- |
| 令牌管理（Token / API Key 管理） | **完全兼容**：新建 `02-tasks-tokens/` 平级目录，复用统一 SOP 模板。 |
| 数据看板（Dashboard / Logs） | **完全兼容**：同上。 |
| 英文版 | **兼容**：现结构无任何 ZH 强耦合；新增 `docs/operation-manual/en/` 平行目录。 |
| 多版本归档（v1.x / v2.x 不同公式） | **部分兼容**：当前页眉版本声明可支撑；若需要"切版本浏览"则需引入 docs site 工具（mkdocs / docusaurus），列入二期。 |
| 自动化校验文档与代码一致性 | **部分兼容**：CI lint 已留挂钩点；深度校验（解析 `price.go` AST 与文档公式比对）需独立工具，列入三期。 |

## 11. 验证证据

PRD §8 要求架构师确认 relay 模块倍率优先级。本设计的所有公式与"优先级"陈述基于当前主线代码（`agent/agent/7fae7cf9` 分支，截至 2026-05-19）的直接阅读。关键证据：

| 主张 | 代码位置 |
| --- | --- |
| Model Price 命中即跳过 ModelRatio | `relay/helper/price.go:67-121`（`if !usePrice { ... } else { preConsumedQuota = int(modelPrice * common.QuotaPerUnit * groupRatioInfo.GroupRatio) }`） |
| GroupGroupRatio 覆盖普通 GroupRatio | `relay/helper/price.go:39-65`（`HandleGroupRatio`），`setting/ratio_setting/group_ratio.go:84-103` |
| ModelRatio × GroupRatio 相乘 | `service/text_quota.go:228`（`ratio := dModelRatio.Mul(dGroupRatio)`） |
| 完整公式（含 cache/image/audio/completion） | `service/text_quota.go:200-310`（`calculateTextQuotaSummary`） |
| 按次计费分支 | `relay/helper/price.go:167-225`（`ModelPriceHelperPerCall`） |
| 表达式分段计费 | `relay/helper/price.go:241-306`，`pkg/billingexpr/expr.md` |
| 兜底：ModelRatio 未配置 | `relay/helper/price.go:20-33`（`modelPriceNotConfiguredError`），`setting/ratio_setting/model_ratio.go:413-414` |
| 兜底：GroupRatio 未配置返回 1 | `setting/ratio_setting/group_ratio.go:84-91` |
| 渠道结构 | `model/channel.go:23-70`（`Channel` + `ChannelInfo`） |
| 渠道类型枚举 | `web/default/src/features/channels/constants.ts:24-79` |
| 后台计费 UI 结构（6 section） | `web/default/src/features/system-settings/billing/section-registry.tsx:53-218` |

未做的验证：
- 未跑实际请求验证公式的浮点精度（`text_quota.go` 已用 `decimal.Decimal`，理论上精度不会丢；如需端到端验证，由文档实施阶段补 1 个回归 case 即可）。
- 未与运维实地访谈（PRD §2 用户画像描述充分，本阶段不阻塞设计）。

## 12. 待澄清项（不允许 TBD 留到此处）

| 项 | 由谁回答 | 何时 | 期望回答 |
| --- | --- | --- | --- |
| 截图素材的提供方与脱敏责任 | 项目管理 + 提交人 | writing-plans 阶段确认 | 指定一名提交人 + 检查清单 |
| 本手册是否需要纳入 i18n 流程（中→英） | PM | v1 发布前 | "v1 仅中文 / v2 起加 EN" 二选一 |
| CI lint 是否阻塞 PR | 项目管理 | writing-plans 阶段 | 阻塞 / 警告二选一（推荐警告） |
| Section URL 与 UI 实际语义路径的对照（如"模型定价"在 UI 上是 tab 还是 sidebar）需要前端确认 | 前端 owner | 进入 SOP 编写前 | 给出当前主线 UI 的稳定路径 |

---

**自检备忘（写入后供 self-review 阶段使用）**：
- 已扫描："TBD" "TODO" "fixme" 占位符 → 无残留（§12 标题"不允许 TBD 留到此处"是规则文本，非占位）。
- 已扫描："大概" "或许" "也许" 等模糊词 → 无残留；"可能"仅出现在 §8.1 / §8.2 风险表用于描述风险概率（"文档版本可能滞后"、"每季度可能有 1-2 个新渠道"），属于规约允许的概率表述。
- 已扫描：方案 A/B/C 与 PRD §3、§4 一致性 → 一致。
- 已扫描：合规约束（PRD §7）落地点 → §1.3 + §6.2 SOP 模板 + §8.1 依赖。
- 已扫描：版本声明（PRD §9） → §6.2 + §7"版本声明"行落地。
- 已扫描：JSON 范式 ≥3（PRD §6） → §6.1 附录"json-examples.md"已列举 4 项。
- 已扫描：内部矛盾 → §3.6 一句话总结与 §3.1 / §3.2 / §3.3 一致；§4 三方案与 §5 推荐结论一致；§6.1 文件树与 §6.2 章节描述一致；§9 工期总和（2+5+4+3+1+1+1+5=22）与"22 人天"一致。
- 已扫描：范围 → 仅"渠道 + 计费"两大主题，未越界到令牌/数据看板（PRD §10 二期）。
- 已扫描：歧义 → §3.1 三类"优先级"分别用三种关系命名（互斥优先/覆盖优先/相乘）避免混用一个词。
