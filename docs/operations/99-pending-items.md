# 灰区证据闭环追踪表

> 来源：架构师事实层摸底报告（issue [TES-69](mention://issue/7628a2ad-bf09-4050-b197-98910ff11357) 评论 `e2af1c61`）+ 第二轮证据闭环（评论 `87c3958d`，2026-05-21）
> 维护规则：每条含 编号 / 现象描述 / 当前手册措辞 / 处置结论 / 状态
> 关闭流程：架构师追加证据 → 文档维护专家更新对应章节 + 关闭本表中的条目（状态改 `closed-YYYY-MM-DD`）。
>
> 本轮 12 条灰区已 100% 取得证据，全部 `closed-2026-05-21`。第 13 条事实清楚，状态保持 `closed-by-fact`。
> 5 项后续修复建议另开 issue 由 PJM 跟踪，集中收口在末尾「附录 · 已知改进点」章节。

---

## 1. 未注册模型默认倍率：**两路径不对称**

| 项 | 内容 |
|---|---|
| **现象描述** | `setting/ratio_setting/model_ratio.go:403-417 GetModelRatio` 在模型未注册时返回 `(37.5, operation_setting.SelfUseModeEnabled, name)`。第二个返回值是「闸门」，不是「真正生效」。 |
| **证据链** | <ul><li>**文本路径（OpenAI / Claude / Gemini）有两道闸门**（`relay/helper/price.go:95-104, 182-191`）：① 系统级 `SelfUseModeEnabled` ② 用户级 `dto.UserSetting.AcceptUnsetRatioModel`（`dto/user_settings.go:14`）。两者都关 → 返回 `modelPriceNotConfiguredError`（`relay/helper/price.go:20-33`）拒绝调用。</li><li>**音频/Realtime/异步任务路径**（`service/quota.go:109`、`service/task_billing.go:258`、`controller/task_video.go:159`）调用 `GetModelRatio` 时**忽略 success 返回值**，未注册模型直接按 37.5 倍率静默扣费。</li></ul> |
| **当前手册措辞** | 第 1 章 1.2.4 + 第 3 章 3.1.4 + 第 5 章场景 1：分文本路径与音频/任务路径分别说明扣费行为。 |
| **处置结论** | 不依赖默认 37.5 倍率，所有上线模型必须在 `ModelRatio` / `ModelPrice` 显式注册。运营无需关心系统级 / 用户级闸门，注册即可避免任何不确定。 |
| **状态** | `closed-2026-05-21` |

---

## 2. TopupGroupRatio：**充值折扣/加价系数，不影响计费**

| 项 | 内容 |
|---|---|
| **现象描述** | option key `TopupGroupRatio` 用于充值时按用户分组做折算。 |
| **证据链** | <ul><li>默认 map：`common/topup-ratio.go:8-12` `{default:1, vip:1, svip:1}`，`GetTopupGroupRatio(name)` 未命中 fallback 1.0 + SysError 日志（`common/topup-ratio.go:32-41`），**不阻断充值**。</li><li>公共定价函数：`controller/topup.go:148-176 getPayMoney` — `payMoney = amount × Price × TopupGroupRatio × Discount`，`topupGroupRatio == 0` 强制设为 1（line 158-160 防免单 bug）。</li><li>四条充值路径调用：epay (`controller/topup.go:206`)、Stripe (`controller/topup_stripe.go:389, 403`)、Waffo (`controller/topup_waffo.go:82`)、Waffo-Pancake (`controller/topup_waffo_pancake.go:59`)。</li></ul> |
| **当前手册措辞** | 第 2 章 2.3.1 + 第 5 章场景 10：明确为「充值折扣 / 加价系数」，仅影响付款金额，不影响调用计费侧（计费走 `GroupRatio`）。 |
| **处置结论** | 用作充值打折（`<1`）或加价订阅（`>1`）；fallback 1.0 是有意设计，未配置 group 不会阻断充值。 |
| **状态** | `closed-2026-05-21` |

---

## 3. 未注册 GroupRatio 默默按 1 倍计费

| 项 | 内容 |
|---|---|
| **现象描述** | `setting/ratio_setting/group_ratio.go:84-91`：未注册 group 名 fallback 1.0，仅打 `SysLog`（不是 SysError），不阻断。 |
| **证据链** | 与首轮一致；后端 `controller/channel.go` 中**未发现** channel.group 的白名单校验。 |
| **当前手册措辞** | 第 1 章 1.3.3 + 1.3.5 + 第 5 章场景 5、5.5 升级路径 + 灰区表 #3：「修改 `GroupRatio` 时同步检查所有 `channels.group` 引用，未注册项会按 1 倍静默计费」。 |
| **处置结论** | 保留 fallback 1.0 行为（兼容历史 channel 配置带过期 group 名的迁移期）。建议另开 issue：在 channel 保存接口（`POST/PUT /api/channel/`）加 group 白名单校验。运营手册仅做事实提示。 |
| **状态** | `closed-2026-05-21` |

---

## 4. FixAbility 高并发不一致

| 项 | 内容 |
|---|---|
| **现象描述** | `model/ability.go:285-341 FixAbility` 是 truncate `abilities` 表 + 按所有 channel 重建。 |
| **证据链** | <ul><li>已加进程级 `sync.Mutex`：`fixLock` (`model/ability.go:285`) — `TryLock()` 失败即返回「已经有一个修复任务在运行中」。</li><li>**没有跨进程锁**：单实例 OK，多实例集群下两节点同时点修复仍可能并发。</li><li>单实例下：执行期间（清空 → 重建完成）`abilities` 表为空 → `service.CacheGetRandomSatisfiedChannel` 全部 miss → 所有请求 503。</li></ul> |
| **当前手册措辞** | 第 1 章 1.4.3 + 第 5 章场景 12 + 灰区表 #4：「FixAbility 期间所有请求会暂时 503，建议在低峰期单实例操作；集群部署需运维分时」。 |
| **处置结论** | 不引入跨进程分布式锁（成本高、价值有限）。建议另开 issue：`FixAbility` 注释中说明集群部署约束；远期评估「先建后删」改造。运营手册仅做时段提示。 |
| **状态** | `closed-2026-05-21` |

---

## 5. GroupRatio 与 UserUsableGroups 必须双写

| 项 | 内容 |
|---|---|
| **现象描述** | `middleware/auth.go:382-398`：`tokens.group` 必须同时存在于 `service.GetUserUsableGroups(user.group)` 与 `ratio_setting.ContainsGroupRatio(token.group)`，任一缺失即 403（`auto` 分组豁免 GroupRatio 检查）。 |
| **证据链** | 两套配置在 `/system-settings/billing/group-pricing` 不同 tab 分别保存，**前端无联动校验**。 |
| **当前手册措辞** | 第 1 章 1.3.5 + 第 2 章 2.1.4 + 第 5 章场景 2、3 + 灰区表 #5：「保留 GroupRatio ↔ UserUsableGroups 双写提示，标注『自动联动校验尚未实现』」。 |
| **处置结论** | 不在后端强制校验（保留管理员高级用法：内部测试时只配 GroupRatio 不放给用户）。建议另开 issue：前端 `group-pricing` 保存按钮处加联动校验弹窗。运营手册保留双写提示。 |
| **状态** | `closed-2026-05-21` |

---

## 6. SVIP 默认半启用

| 项 | 内容 |
|---|---|
| **现象描述** | `setting/ratio_setting/group_ratio.go:12-16` `defaultGroupRatio = {default:1, vip:1, svip:1}`；`setting/user_usable_group.go:10-13` `userUsableGroups = {default:"默认分组", vip:"vip分组"}`（无 svip）。结果：svip 在 GroupRatio 注册但不在 UserUsableGroups → 用户 token.group="svip" 被 `middleware/auth.go:386` 阻断。 |
| **证据链** | 与首轮一致。 |
| **当前手册措辞** | 第 1 章 1.3.5 + 第 5 章场景 13 + 灰区表 #6：「首次部署需手动追加 svip 到 UserUsableGroups（workaround）」。 |
| **处置结论** | 建议另开 issue：1 行改动 `setting/user_usable_group.go:12` 增加 `"svip": "svip分组"`，对齐两份默认值。运营手册保留首次部署 workaround，作为版本未升级前的过渡指引。 |
| **状态** | `closed-2026-05-21` |

---

## 7. logs.ip 默认不记

| 项 | 内容 |
|---|---|
| **现象描述** | `model/log.go:155-160, 218-223`：`users.setting.RecordIpLog == true` 才写 `logs.ip`，默认 `false`（`dto/user_settings.go:15`）。 |
| **证据链** | 与首轮一致。 |
| **当前手册措辞** | 第 2 章 2.1.3 + 第 4 章 4.1.4 + 第 5 章场景 9、5.3 排错矩阵 + 灰区表 #7：FAQ 显式提示「按 IP 排查 → 先在用户设置开启『记录请求 IP』，历史日志补不回来」。 |
| **处置结论** | 保持默认 false（合规：GDPR / PIPL 个人信息最小化原则）。运营手册补 FAQ 排查链，不改默认值。 |
| **状态** | `closed-2026-05-21` |

---

## 8. model_limits 为空 403：**错误文案订正**

| 项 | 内容 |
|---|---|
| **现象描述** | 令牌 `model_limits_enabled=true` 但 `model_limits=''` 时全模型不可用。 |
| **证据链订正** | 首轮文档 / 报告中描述的「token model limit is empty, all models are not allowed」**不会触发**：<ul><li>`middleware/auth.go:421-426`：`ModelLimitsEnabled=true` 时 `c.Set("token_model_limit", token.GetModelLimitsMap())`。</li><li>`model/token.go:343-350 GetModelLimitsMap`：`model_limits=""` → `GetModelLimits()` 返回空切片 → `limitsMap` 是**空 map（非 nil）**。</li><li>`middleware/distributor.go:57-74`：`GetContextKey` 返回 `(空map, true)`，**不会**走 line 61-63 的「token model limit is empty」分支；落到 line 71 `tokenModelLimit[matchName]` 不存在 → 抛 `i18n.MsgDistributorTokenModelForbidden`「该 token 不允许使用模型 X」。</li></ul> |
| **当前手册措辞** | 第 2 章 2.2.2 + 第 5 章场景 4 + 灰区表 #8：FAQ 给出「『不允许使用模型』排查链」（① 检查 token.model_limits_enabled ② 检查 model_limits 是否包含目标模型）。 |
| **处置结论** | 实际效果与原描述一致（任何模型都不可用），但错误文案不同。建议另开 issue：前端 `/keys` 页保存校验，阻止「启用白名单 + 空 model_limits」组合。 |
| **状态** | `closed-2026-05-21` |

---

## 9. OtherRatios：**任务/图像类计费的动态参数倍率**

| 项 | 内容 |
|---|---|
| **现象描述** | `service/text_quota.go:281-285` 文本路径连乘 `Π OtherRatios`，但首轮未找到赋值路径与前端入口。 |
| **证据链** | <ul><li>**OtherRatios 不是预留字段，而是任务/图像计费的活跃路径**。</li><li>类型：`types.PriceData.OtherRatios map[string]float64`，赋值入口 `AddOtherRatio(key, value)`（`relay/relay_task.go:113, 192` 等）。</li><li>任务主流程：`relay/relay_task.go:144-203 RelayTaskSubmit` — 步骤 5 `adaptor.EstimateBilling` 返回 `{"seconds": N, "size": M}` 等键值；步骤 6 `Quota *= ratio`（连乘所有 OtherRatios 值）；步骤 11 `AdjustBillingOnSubmit` 校准。</li><li>实际赋值的 adapter：`relay/channel/task/sora/adaptor.go:97-130`（OpenAI Sora）、`relay/channel/task/ali/adaptor.go:193`（阿里通义视频）、`relay/channel/task/gemini/adaptor.go:160`（Veo）、`relay/channel/task/vertex/adaptor.go:124`（Vertex Veo）、`relay/channel/ali/image.go:53,58,331,333` + `image_wan.go:37`（图像 `n` / `prompt_extend`）、`relay/image_handler.go:124-125`（通用图像 `n`）。</li><li>透出：HTTP 头 `X-New-Api-Other-Ratios`（`relay/relay_task.go:234`）；日志 `logs.other` 记录命中明细（`service/task_billing.go:26-29, 128-129, 286-289`）。</li><li>文本路径中的 for 循环是统一公式预留位，文本 adapter 当前未赋值，连乘为 no-op，但**不是废弃字段**。</li></ul> |
| **当前手册措辞** | 第 3 章 3.1.3 + 灰区表 #9：明确为「任务/图像类计费的动态参数倍率」，由 adapter 根据用户请求参数（视频 `seconds`/`size`，图像 `n`）自动计算；运营**无需也不能**手动配置；日志 `other` JSON 字段记录明细。 |
| **处置结论** | 无前端入口是设计意图，运营无需配置。 |
| **状态** | `closed-2026-05-21` |

---

## 10. logs.group 仅记 usingGroup

| 项 | 内容 |
|---|---|
| **现象描述** | `model/log.go:239 Group: params.Group` 写入 `relayInfo.UsingGroup`（最终生效）；`logs.other` 中仅在命中 `GroupGroupRatio` 时写 `group_ratio_special`（`service/text_quota.go:425` 附近），无独立 `user_group` / `token_group` 字段。 |
| **证据链** | 与首轮一致。 |
| **当前手册措辞** | 第 4 章 4.2.3 + 灰区表 #10：「按 group 统计时明示口径；追溯切组路径需打开日志详情查 `other.user_group / token_group`（待扩展 other 字段后）」。 |
| **处置结论** | 建议另开 issue：扩展 `logs.other` JSON 而非 schema（避免三端迁移成本），追加 `user_group` / `token_group` 键。运营手册保留事实并提示「需追溯切组时打开 other 详情」。 |
| **状态** | `closed-2026-05-21` |

---

## 11. rpm/tpm 是 60 秒截面

| 项 | 内容 |
|---|---|
| **现象描述** | `model/log.go:482 rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())` 固定 60 秒窗口；`controller/log.go:98-121 GetLogsStat` 直接透出 `rpm/tpm` 字段名。 |
| **证据链** | 与首轮一致。 |
| **当前手册措辞** | 第 4 章 4.3 + 第 5 章场景 8、5.3 排错矩阵 + 灰区表 #11：字段统一标注「截面值，按最近 60 秒计算」；区间均值需自跑 SQL。 |
| **处置结论** | 不改后端语义（兼容已存量调用）。建议另开 issue：前端 `/usage-logs` 在 RPM/TPM 字段旁添加 ⓘ tooltip。 |
| **状态** | `closed-2026-05-21` |

---

## 12. quota_data 表无清理路径

| 项 | 内容 |
|---|---|
| **现象描述** | `model/usedata.go` 全文 138 行，`grep -n "DELETE\|Delete\|truncate"` 0 匹配；`controller/log.go:153 DeleteHistoryLogs` 只清 `logs` 表。 |
| **证据链** | 量级评估：每用户每模型每小时 1 行，10000 用户 × 20 模型 × 24h × 365 天 ≈ 17 亿行，5 年级会达瓶颈，1 年内通常无影响。优先级 P3。 |
| **当前手册措辞** | 第 4 章 4.4.5 + 第 5 章 5.3 排错矩阵 + 灰区表 #12：「`quota_data` 表暂无清理入口，长生命周期实例需 DBA 手动维护」。 |
| **处置结论** | 建议另开 issue：在 `model/usedata.go` 增加 `DeleteOldQuotaData(targetTimestamp int64) error`（仿 `model/log.go:518 DeleteOldLog`），并在 `DeleteHistoryLogs` 同步调用。优先级 P3。运营手册保留「DBA 手动维护」提示。 |
| **状态** | `closed-2026-05-21` |

---

## 13. channels.balance 不参与计费

| 项 | 内容 |
|---|---|
| **现象描述** | `channels.balance` / `balance_updated_time` 是上游账户余额（通过 `update_balance` API 拉取，`controller/channel-billing.go`），**不参与 NewAPI 内部 quota 计算**。 |
| **当前手册措辞** | 第 1 章 1.4.2 + 第 5 章场景 11、5.3 排错矩阵 + 灰区表 #13：「客户咨询『渠道余额为 0』时明确说不影响扣费；该字段只用于运营监控上游账户是否需要充值」。 |
| **处置结论** | 事实清楚，仅作存档。 |
| **状态** | `closed-by-fact` |

---

## 状态汇总

| 状态 | 数量 | 编号 |
|---|---|---|
| `closed-2026-05-21` | 12 | #1 ~ #12 |
| `closed-by-fact` | 1 | #13 |
| `open` | 0 | — |

12 条灰区已 100% 闭环，无 open 项。

---

## 附录 · 已知改进点（PJM 另开 issue 跟踪）

> 架构师在第二轮证据闭环中给出的修复建议清单，**不阻塞本手册门 6**。
> 由 PJM 另开跟踪 issue，独立于运营手册迭代。

| # | 主题 | 关联灰区 | 优先级 | 描述 |
|---|---|---|---|---|
| A1 | 音频/任务路径补 `AcceptUnsetRatioModel` 闸门 | #1 | 中 | 与文本路径对齐，避免未注册模型在音频/Realtime/任务路径下静默按 37.5 倍率扣费。改动点：`service/quota.go:109`、`service/task_billing.go:258`、`controller/task_video.go:159` 调用 `GetModelRatio` 时检查 success 返回值。 |
| A2 | 默认值对齐：`UserUsableGroups` 加 svip | #6 | 低 | `setting/user_usable_group.go:12` 增加 `"svip": "svip分组"`。仅影响数据库 option 表为空的全新部署，已部署实例不受影响。 |
| A3 | 三处前端校验联动 | #3 / #5 / #8 | 中 | <ul><li>`/channels` 保存：要求 `channels.group` ⊂ `GroupRatio` 已注册集合，否则提示。</li><li>`/keys` 保存：阻止「`model_limits_enabled=true` + 空 `model_limits`」组合。</li><li>`/system-settings/billing/group-pricing` 保存：检测 GroupRatio 与 UserUsableGroups 不一致时弹窗提示同步。</li></ul> |
| A4 | UI tooltip：RPM / TPM 字段加截面说明 | #11 | 低 | `web/default/src/features/usage-logs/`：RPM/TPM 字段旁加 ⓘ「按最近 60 秒计算」。 |
| A5 | 数据看板表清理接口 | #12 | P3 | 在 `model/usedata.go` 增加 `DeleteOldQuotaData`，仿 `DeleteOldLog` 分批 100 条删除；`controller/log.go DeleteHistoryLogs` 同步调用，或单独提供 `DELETE /api/data/`。 |

> 闭环约定：上述任一改进点合入 `main` 后，由 PJM 通知文档维护专家，本表对应行追加 PR 链接 + 状态改 `done-YYYY-MM-DD`；运营手册中相关「workaround」段落同步删除。

---

## 维护说明

- 本表由文档维护专家维护。
- 灰区编号一旦确定不再变更；新发现的灰区**追加为 #14、#15...**，不要改既有编号。
- 改进点编号 A1、A2... 同上。
