# 第 3 章 · 计费规则

> 适用对象：客户已能调用，本章解释「每次调用到底扣了多少 quota」。
>
> 运营常被问到的问题都在这里：人话版公式、原始公式、量纲、特殊场景。

---

## 3.0 量纲（**先记住这三条**）

> 🔴 量纲是计费理解的地基，看不懂下面的公式 = 先回来看这三条。

| 表达 | 含义 |
|---|---|
| **`1 USD = QuotaPerUnit = 500000 quota`** | 1 美元 = 50 万 quota。源码注释 `$0.002 / 1K tokens = 1 quota`（`common/constants.go:62`） |
| **`1 quota ≈ $0.000002`** | 反过来看，量纲非常小 |
| **`USD2RMB = 7.3`** | 默认人民币汇率（`setting/ratio_setting/model_ratio.go:13`），可在 `/system-settings/billing/currency` 调整 |

举例：

- 客户消费了 `1000 quota` ≈ `$0.002` ≈ `¥0.0146`
- 给客户充值 `$10` = 给 `users.quota` 增加 `5,000,000`

> 前端显示时是否换算成美元 / 人民币，由 `/system-settings/billing/currency` 的 `DisplayInCurrencyEnabled` + `general_setting.quota_display_type` 决定。

---

## 3.1 主计费入口（文本类）

> **所有 OpenAI / Claude / Gemini 的文本与 chat completions 都走这一个函数**：`service/text_quota.go:159 calculateTextQuotaSummary`。

### 3.1.1 人话版（**给运营 / 客户的解释口径**）

NewAPI 的标准计费可以拆成三层：

```
单次扣费 quota
 = 输入贡献 + 输出贡献 + 工具调用附加费 + 音频输入附加费
 = (输入 token × ModelRatio) + (输出 token × CompletionRatio × ModelRatio)
   + 工具调用次数费用 + 音频独立计费
 × GroupRatio（分组倍率）
 × 一连串 OtherRatios（其他倍率，目前通常为 1）
```

**关键点（运营要会说清）**：

1. **输入和输出价格不同**：`CompletionRatio` 是输出倍率，一般 > 1（输出比输入贵）。
2. **缓存命中部分另算**：缓存命中的 token 走 `CacheRatio`，写入缓存的部分走 `CacheCreationRatio`，一般比标准价便宜。
3. **图像、音频 token 独立计价**：从 prompt_tokens 中扣除后单独乘 `ImageRatio` / `AudioRatio`。
4. **工具调用按次收费**：每个工具按 `(toolPrice / 1000) × callCount × GroupRatio × QuotaPerUnit` 算。
5. **GroupRatio 包揽客户级折扣**：VIP / SVIP / 内部分组的整体打折就靠它。
6. **最低收 1 quota**：单次计算结果 ≤ 0 但模型有价格时，强制扣 1 quota（避免免费调用）。

> 「客户问：为什么我的发票里输出比输入多很多？」→ 给他看 `CompletionRatio`。
> 「客户问：缓存命中怎么省钱？」→ 给他看 `CacheRatio`。
> 「客户问：我是 VIP 怎么还扣这么多？」→ 检查 `GroupRatio['vip']` 是否真的小于 1。

### 3.1.2 原始公式（按倍率计费分支，`UsePrice=false`）

证据：`service/text_quota.go:228-290`

```
ratio              = ModelRatio × GroupRatio                      (line 228)

baseTokens         = PromptTokens
                       - CachedTokens         (非 Claude 语义时)
                       - CachedCreationTokens (非 Claude 语义时)
                       - ImageTokens
                       - AudioTokens          (有单独定价时)

promptQuota        = baseTokens
                       + CachedTokens         × CacheRatio
                       + CachedCreationTokens × CacheCreationRatio
                       + ImageTokens          × ImageRatio
                       + (Claude split)
                         CC5m × CacheCreation5mRatio
                         + CC1h × CacheCreation1hRatio

completionQuota    = CompletionTokens × CompletionRatio

toolSurchargeQuota = Σ(toolPrice × callCount ÷ 1000) × GroupRatio × QuotaPerUnit
                     (text_quota.go:84-138)

audioInputQuota    = AudioInputPrice ÷ 1e6 × AudioTokens × GroupRatio × QuotaPerUnit

quota              = round( (promptQuota + completionQuota) × ratio
                            + toolSurchargeQuota
                            + audioInputQuota )
                       × Π OtherRatios                            (line 281-285)

if quota <= 0 and ratio != 0:
    quota = 1                                                     (line 287-289 兜底)
```

### 3.1.3 原始公式（按次价分支，`UsePrice=true`）

证据：`service/text_quota.go:291-301`

```
quota = round( ModelPrice × QuotaPerUnit × GroupRatio
               + toolSurchargeQuota
               + audioInputQuota )
        × Π OtherRatios
```

按次价场景：模型在 `ModelPrice` 中显式注册了价格，**整请求只按这个价**收费，与 token 数无关（但仍叠加工具与音频费）。

> ⚠️ `OtherRatios` 连乘当前没有赋值路径、没有前端入口，**疑似预留字段，运营无需配置**。详见末尾追踪表 [#9](99-pending-items.md#9-otherratios-疑似预留)。

---

## 3.2 音频 / Realtime 计费

> WebSocket 实时通道、音频 HTTP 接口走独立计费函数。

### 3.2.1 人话版

```
音频请求的 quota
 = (文本输入 token + 文本输出 token × CompletionRatio
    + 音频输入 token × AudioRatio
    + 音频输出 token × AudioRatio × AudioCompletionRatio)
 × ModelRatio × GroupRatio
```

要点：

1. 音频也分输入 / 输出，**双重叠加倍率**：音频输出 = `AudioRatio × AudioCompletionRatio`。
2. 文本和音频混合时，分开累加再统一乘 `ModelRatio × GroupRatio`。

### 3.2.2 原始公式

证据：`service/quota.go:50 calculateAudioQuota`

```
ratio = ModelRatio × GroupRatio
quota = ratio × ( textInputTokens
                + textOutputTokens × CompletionRatio
                + audioInputTokens × AudioRatio
                + audioOutputTokens × AudioRatio × AudioCompletionRatio )
```

入口函数：

| 入口 | 文件 |
|---|---|
| WSS 实时（如 OpenAI Realtime） | `service/quota.go:157 PostWssConsumeQuota` |
| 音频 HTTP（Whisper / TTS） | `service/quota.go:279 PostAudioConsumeQuota` |

---

## 3.3 异步任务计费（Midjourney / Suno / 视频）

| 模块 | 文件 |
|---|---|
| 主入口 | `service/task_billing.go`（301 行） |
| 写日志 | `model.RecordTaskBillingLog`（`model/log.go:273`） |

特点：

- 任务（Midjourney / Suno / 视频）是异步生成，计费时机在**任务完成回调**而非请求时。
- 落表与文本类相同：`logs` 表 + 更新 `users.used_quota` / `tokens.used_quota` / `channels.used_quota`。
- 计费维度通常是「按次」或「按时长」，由各任务 adapter 决定。

---

## 3.4 分级（tiered）计费

> 当一个模型既要按用量阶梯扣，又要支持自定义表达式时使用。

### 3.4.1 触发条件

模型挂上 `BillingMode='tiered_expr'` + `BillingExpr=<表达式>`。

### 3.4.2 运行时

证据：`service/tiered_settle.go:95 TryTieredSettle`

```
1. 走完标准计费（3.1 / 3.2）得到 summary.Quota
2. 调用 TryTieredSettle：
   ├─ 若模型未挂 BillingMode='tiered_expr' → 跳过
   └─ 若已挂：
        ├─ 用 relayInfo.TieredBillingSnapshot.ExprString 求值
        └─ ★ 覆盖 summary.Quota（不是叠加）★
```

调用点：`service/text_quota.go:340-346`、`service/quota.go:202-205`。

### 3.4.3 一次请求倍率不变

`TieredBillingSnapshot.ExprString` 在请求前置阶段**冻结**，即使运营在请求处理过程中改了表达式，本次请求仍按冻结的版本算。

### 3.4.4 配置入口与文档

- **表达式设计文档**：仓库内 `pkg/billingexpr/expr.md`（**配置前必读**）。
- **配置代码**：`setting/billing_setting`。

---

## 3.5 预扣 / 反扣 / 退款（钱什么时候真的扣）

> 「客户说扣费时机不对」时，对照下面这张表。

| 步骤 | 函数 | 文件:行 |
|---|---|---|
| 预扣额度（信任阈值以上则跳过） | `PreConsumeQuota` | `service/pre_consume_quota.go:33` |
| 预扣实操：扣 token | `PreConsumeTokenQuota` → `model.DecreaseTokenQuota` | `service/quota.go:382` / `model/token.go:405` |
| 失败返还预扣 | `ReturnPreConsumedQuota` → `PostConsumeQuota(-quota)` | `service/pre_consume_quota.go:17` |
| 实结：扣用户 quota | `model.DecreaseUserQuota` | `model/user.go:911` |
| 实结：累加用户 used | `model.UpdateUserUsedQuotaAndRequestCount`（支持 `BatchUpdateEnabled` 批量异步） | `model/user.go:963` |
| 实结：累加渠道 used | `model.UpdateChannelUsedQuota` | `model/channel.go:824` |
| 实结：扣令牌余量 | `model.DecreaseTokenQuota` | `model/token.go:405` |
| 订阅模式扣费 | `model.PostConsumeUserSubscriptionDelta` | `service/quota.go:406-431`（`relayInfo.BillingSource == BillingSourceSubscription`） |
| 配额预警通知 | `checkAndSendQuotaNotify` / `checkAndSendSubscriptionQuotaNotify` | `service/quota.go:452-546` |

### 3.5.1 客户层心智模型

```
请求到达
  ├─ PreConsumeQuota 估扣（防止超额提前拒）
  │   └─ 若超信任阈值：跳过预扣（信任客户不会跑路）
  ├─ 转发到上游
  ├─ 上游返回：拿到真实 token 数
  ├─ 计算真实 quota（3.1 / 3.2 / 3.3）
  ├─ 若分级计费触发 → TryTieredSettle 覆盖
  ├─ 若有订阅 → PostConsumeUserSubscriptionDelta
  └─ 否则 → 标准实结：
        ├─ DecreaseUserQuota
        ├─ DecreaseTokenQuota
        ├─ UpdateChannelUsedQuota
        └─ RecordConsumeLog（写流水，详见第 4 章）
```

### 3.5.2 失败场景

- 上游返回错误 → 仅返还预扣，**不写 Consume 流水**（写 Error 流水，`logs.type=5`）。
- 部分上游 / 部分失败 → 取上游真实返回的 token 计费。

---

## 3.6 量纲一览（再来一次，方便对账）

| 单位 | 等值 |
|---|---|
| 1 quota | $0.000002 |
| 1 USD | 500000 quota |
| 1 RMB | 500000 / 7.3 ≈ 68493 quota（默认汇率） |
| 1K tokens（按 $0.002 / 1K 基准） | 1 quota |

---

## 3.7 落表（计费数据持久化）

| 表.字段 | 含义 | 更新时机 |
|---|---|---|
| `users.quota` | 用户剩余额度 | 实结时减 |
| `users.used_quota` | 用户累计消费 | 实结时加（可批量异步） |
| `users.request_count` | 用户累计请求数 | 实结时 +1 |
| `tokens.remain_quota` | 令牌剩余额度 | 实结时减（`unlimited_quota=true` 时跳过） |
| `tokens.used_quota` | 令牌累计消费 | 实结时加 |
| `channels.used_quota` | 渠道累计消费 | 实结时加 |
| `logs.quota` | 单次调用的 quota 数（**审计源**） | 实结时插入一行 `type=2` |
| `quota_data.quota` | 用户 × 模型 × 小时聚合 quota | 异步刷盘（详见第 4 章） |

---

## 参考代码索引

- `common/constants.go:62` — `QuotaPerUnit = 500000`
- `setting/ratio_setting/model_ratio.go:13` — `USD2RMB`
- `service/text_quota.go:84-138, 159, 228-290, 291-301, 340-346` — 文本计费主入口
- `service/quota.go:50, 157, 202-205, 242, 279, 363, 382, 406-431, 452-546` — 音频 / 实时 / 实结 / 通知
- `service/tiered_settle.go:95` — 分级计费
- `service/task_billing.go` — 任务计费
- `service/pre_consume_quota.go:17, 33` — 预扣 / 返还
- `model/user.go:911, 963` — 用户 quota 增减
- `model/token.go:405` — 令牌 quota 减
- `model/channel.go:824` — 渠道累计
- `model/log.go:208, 273` — 流水写入
- `pkg/billingexpr/expr.md` — 分级计费表达式设计
