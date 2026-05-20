# 计费公式权威说明

> 适用版本：v1.0.0+　｜　最后核对：2026-05-20　｜　代码基线：`relay/helper/price.go`、`service/text_quota.go`

## 一句话总结

> **结算价 = 模型成本（ModelPrice 或 ModelRatio×Tokens 二选一）× 分组倍率（GroupGroupRatio 或 GroupRatio 二选一，前者优先覆盖）。**
>
> ModelPrice 与 ModelRatio 互斥；GroupGroupRatio 覆盖 GroupRatio；ModelRatio 与 GroupRatio 是相乘，不是优先级。

---

## 三种"优先级"语义

系统中存在三种容易混淆的"优先级"关系，它们各自独立：

| 关系 | 类型 | 代码证据 | 说明 |
|------|------|----------|------|
| **Model Price 与 Model Ratio** | 互斥优先（前者命中即跳过后者） | `relay/helper/price.go:67-121` | 先查 `GetModelPrice`；命中（`usePrice=true`）则按次计费，**完全不用 ModelRatio**。 |
| **GroupGroupRatio 与 GroupRatio** | 覆盖优先（特殊倍率覆盖普通倍率） | `relay/helper/price.go:39-65`、`ratio_setting/group_ratio.go:84-103` | `GetGroupGroupRatio(userGroup, usingGroup)` 命中则整体覆盖；否则用 `GetGroupRatio(usingGroup)`。 |
| **Model Ratio 与 Group Ratio** | 相乘关系（不是优先级） | `service/text_quota.go:228` | 二者地位平等，按 token 计费时同时乘入。 |

---

## 完整结算公式

### 按 Token 计费分支

代码引用：`service/text_quota.go:200-310`（`calculateTextQuotaSummary`）

```
let baseTokens = PromptTokens
              − CacheTokens                          // 缓存读命中
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

### 按次计费分支（ModelPrice 命中时）

代码引用：`relay/helper/price.go:167-225`（`ModelPriceHelperPerCall`）

```
quota = ModelPrice × QuotaPerUnit × GroupRatio
      + ToolCallSurcharge
      + AudioInputQuota

∀ otherRatio in OtherRatios:  quota *= otherRatio
```

### 表达式分段计费（TieredExpr）

代码引用：`relay/helper/price.go:241-306`，详见 `pkg/billingexpr/expr.md`

```
rawCost     = expr(p, c, ...)        // 单位：$/1M
quotaBefore = rawCost / 1_000_000 × QuotaPerUnit
quota       = round(quotaBefore × GroupRatio)
```

> 注意：表达式内部不要再把 `GroupRatio` 变量写进表达式，否则会双重相乘。

---

## 补充规则

| 条件 | 行为 | 代码位置 |
|------|------|----------|
| Claude Usage 语义路径 | `baseTokens` 不再扣减，按上游已结构化的 usage 字段计入 | `text_quota.go:237-247` |
| `ratio ≠ 0` 且 `quota ≤ 0` | 强制 `quota = 1`（避免免费模型被误标） | `text_quota.go` |
| `TotalTokens == 0` | 强制 `quota = 0` | `text_quota.go` |
| `GetModelRatio` 未命中 + 自用模式开启 | 返回 37.5 + `success=true` | `relay/helper/price.go:20-33` |
| `GetModelRatio` 未命中 + 自用模式关闭 | 触发 `modelPriceNotConfiguredError`，前端报"模型 X 的价格未配置" | `relay/helper/price.go:20-33` |
| `GetGroupRatio` 未命中 | 日志告警 + 默认返回 `1.0` | `ratio_setting/group_ratio.go:84-91` |
| `GroupRatio == 0` 且 `EnableFreeModelPreConsume == false` | 预扣额度强制置 0（标记 `freeModel=true`） | `relay/helper/price.go` |

---

## 术语速查

| 术语 | 含义 |
|------|------|
| ModelPrice | 按次计费单价（$/次），命中后跳过 ModelRatio |
| ModelRatio | 模型倍率，与 token 数相乘得到模型成本 |
| GroupRatio | 分组倍率，对最终额度做整体缩放 |
| GroupGroupRatio | 用户组→目标组的特殊倍率，命中后覆盖 GroupRatio |
| CompletionRatio | 补全 token 相对于 prompt token 的倍率 |
| CacheRatio | 缓存读命中 token 的折扣倍率 |
| QuotaPerUnit | 系统内部额度单位与美元的换算系数 |
| OtherRatios | 附加倍率列表（如渠道特殊加成），逐一相乘 |

---

*变更记录：2026-05-20 由文档维护专家创建，基于 design.md §3 与代码 `relay/helper/price.go`、`service/text_quota.go` 直接核对。*
