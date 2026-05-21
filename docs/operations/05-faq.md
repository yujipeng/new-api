# 第 5 章 · 常见运营场景速查 / 排错

> 「客户反馈了 X，我该看哪里、怎么处理？」
>
> 本章覆盖：① 高频运营场景；② 架构师摸底报告中标记的 13 条「灰区」翻译成的运营行动建议。
>
> 灰区编号与末尾追踪表（`99-pending-items.md`）一一对应。

---

## 5.1 高频运营场景

### 场景 1：上线一个新模型，全链路要动哪几步？

1. （可选）在 `/system-settings/models/vendors` 注册供应商。
2. 在 `/system-settings/models/models` 注册模型元数据，设置 `name_rule` 与 `endpoints`。
3. **在 `/system-settings/billing/model-pricing` 注册 `ModelRatio`（或 `ModelPrice`）+ `CompletionRatio`**。⚠️ 不注册的后果（两路径不对称扣费）见灰区 [#1](99-pending-items.md#1-未注册模型默认倍率两路径不对称)。
4. 在 `/channels` 把新模型加入支持该模型的渠道 `models` 列表，或新增一条专门渠道。
5. 验证 `abilities` 表已生成 `(group, model, channel_id)` 行（前端「修复」按钮 = `POST /api/channel/fix`）。
6. 用一个测试令牌 curl 调用，确认 200 + 扣费正确。

### 场景 2：上线一个新分组（如 `enterprise`）

1. 在 `/system-settings/billing/group-pricing` 同时配：
   - `GroupRatio['enterprise'] = X.X`
   - `UserUsableGroups['enterprise'] = '描述文案'`
   - （可选）`GroupGroupRatio[*][enterprise] = ...` 对特定用户分组打折
2. （可选）配 `TopupGroupRatio['enterprise']` 调整充值打折比例。
3. 在 `/channels` 把目标渠道的 `group` 字段追加 `enterprise`（逗号分隔）。
4. 把客户的 `users.group = 'enterprise'`（或在令牌上 `tokens.group = 'enterprise'`）。

> 🔴 **千万记得两边都配**（`GroupRatio` + `UserUsableGroups`），否则鉴权 403。详见灰区 [#5](99-pending-items.md#5-groupratio-与-userusablegroups-必须双写)。

### 场景 3：客户反馈调用 403 "no permission to use this group"

排查顺序：

1. 看令牌的 `tokens.group`。若非空：
   - 是否在 `UserUsableGroups[users.group]` 白名单内？
   - 是否在 `GroupRatio` 注册了对应倍率？
2. `auto` 分组例外（走 `AutoGroups` 列表），但仍需 `cross_group_retry` 配合。
3. 用 `GET /api/user/self/groups` 看客户视角能看到哪些分组——能看到的就一定能用。

### 场景 4：客户反馈 403「该 token 不允许使用模型 X」（`MsgDistributorTokenModelForbidden`）

> ✅ **错误文案订正**：实际报错文案是 i18n key `MsgDistributorTokenModelForbidden`（中文「该 token 不允许使用模型 X」），**不是**「token model limit is empty」。详见末尾追踪表 [#8](99-pending-items.md#8-model_limits-为空-403错误文案订正)。

证据：`middleware/distributor.go:57-75`、`model/token.go:343-350 GetModelLimitsMap`。

**排查链**（按顺序逐项核对）：

1. **看令牌是否启用了模型白名单**：`tokens.model_limits_enabled = true`？
   - `false` → 不应该报这个错，转去看场景 3 / 5。
2. **若启用，看 `tokens.model_limits` 是否为空字符串**：
   - 是 → 客户最常见的误操作。让客户编辑令牌，要么关闭白名单开关，要么至少填一个模型。
3. **若 `model_limits` 非空，看请求模型是否在白名单内**：
   - 不在 → 提醒客户在 `/keys` 页把目标模型加入 `model_limits` 列表。
   - 注意 `model_mapping` / `name_rule`：客户请求的 model 可能被 channel 映射成另一名字，模型白名单按**用户请求的原名**匹配。

> ℹ️ 历史上版本曾出现过「token model limit is empty」字样，但当前实现里 `GetModelLimitsMap` 始终返回非 nil 空 map，不会走到该分支。如果在生产环境真的看到这条文案，请反馈架构师。

### 场景 5：客户反馈调用某个模型扣费比预期高

排查顺序：

1. 打开 `/usage-logs/common`，找到对应 `request_id` 的行。
2. 展开 `other` JSON：
   - 看 `model_ratio`、`completion_ratio` 是否符合预期。
   - 看 `group_ratio` 是否是客户预期的分组倍率。
   - 看是否有 `tiered_settle_snapshot` 字段（分级计费覆盖了标准计费）。
   - 看 `tool` / `audio` 附加费是否符合预期。
3. 对照第 3 章公式手工核算一次，与 `logs.quota` 比对。
4. **客户分组核对**：`logs.group` = 最终 usingGroup，可能与 `users.group` 不同（令牌切换过）。

### 场景 6：客户反馈余额扣完了但找不到对账记录

1. `/usage-logs/common` 按 `username` + 时间区间过滤 `type=2` 全量导出。
2. 若有 Midjourney / Suno / 视频任务：也看 `/usage-logs/task`。
3. 与 `users.used_quota` 对账，差异 = 期间发生过的退款（`type=6`）或管理员调整（`type=3`）。
4. 充值差异：查 `type=1` 行。
5. 长期对账：用 `/dashboard` 看 `quota_data` 模型 × 小时聚合，注意 `quota_data` 至少滞后 `DataExportInterval`（默认 5 分钟）。

### 场景 7：客户反馈某次请求很慢 / 超时

1. `/usage-logs/common` 看对应 `request_id`：
   - `use_time` 字段（秒）。
   - `channel_id` / `channel_name` 是哪个渠道。
2. 到 `/channels` 看该渠道的 `response_time`、`test_time`，必要时点「测试」重测。
3. 看渠道亲和度（`/api/option/channel_affinity_cache`）是否把客户卡在了一个慢渠道——可清缓存让重新选路。

### 场景 8：客户问「我这个月一共花了多少 RPM / TPM」

> 🔴 **`rpm` / `tpm` 是「最近 60 秒」的截面值，不是时间区间的平均值**（灰区 [#11](99-pending-items.md#11-rpmtpm-是-60-秒截面)）。
>
> 想要区间平均：从 `logs` 表自己跑 SQL：
>
> ```sql
> SELECT COUNT(*) / (END - START) AS rpm_avg,
>        (SUM(prompt_tokens) + SUM(completion_tokens)) / (END - START) AS tpm_avg
>   FROM logs
>  WHERE type = 2 AND user_id = ? AND created_at BETWEEN START AND END;
> ```

### 场景 9：客户反馈调用了但日志没 IP（按 IP 排查的前置开关）

`logs.ip` **默认不写**（合规：GDPR / PIPL 个人信息最小化原则，灰区 [#7](99-pending-items.md#7-logsip-默认不记)）。

**按 IP 排查的完整链**：

1. **先开启用户级开关**（必须）：
   - 客户自助：让客户在「个人设置」中打开「记录请求 IP」。
   - 运营批改：直接 `PUT /api/user/` 把 `users.setting.record_ip_log` 改为 `true`。
2. **等客户重新发起请求** → 新日志的 `logs.ip` 字段才有值。
3. **历史日志补不回来**，只对开关打开后的新日志生效。
4. 排查完成后**是否关闭**由客户决定；运营不强行关闭。

### 场景 10：充值到账金额不对

证据：`controller/topup.go:148-176 getPayMoney`、`common/topup-ratio.go:32-41`。

**对账公式**（必须背下来）：

```
payMoney = amount × Price × TopupGroupRatio[user.group] × Discount
```

排查顺序：

1. **看 `topup` 表的原始订单**：`amount`（用户想充的 quota 数）、对应支付通道。
2. **看 `TopupGroupRatio[users.group]`**：折扣 / 加价系数。**仅影响付款金额，不影响调用扣费**（详见灰区 [#2](99-pending-items.md#2-topupgroupratio充值折扣加价系数不影响计费)）。
   - 例：`TopupGroupRatio['vip'] = 0.9` → vip 用户付 90% 金额拿同样 quota。
   - 未配置 group → fallback `1.0`（无折扣），写 SysError 日志，**不阻断充值**。
   - `topupGroupRatio == 0` 强制设为 1（防免单 bug）。
3. **看 `Price` 全局单价**（`operation_setting.Price`）。
4. **看金额阈值预设折扣** `operation_setting.GetPaymentSetting().AmountDiscount[amount]`：常见是「满 100 减 10」类阶梯。
5. **核对支付通道**：epay / Stripe / Waffo / Waffo-Pancake 四条路径都会读 `TopupGroupRatio`，逻辑一致；若不同通道结果不同，记录证据上报架构师。

### 场景 11：渠道余额（`channels.balance`）显示为 0，客户调用还能扣费吗？

**能**。`channels.balance` 是 NewAPI 主动拉取的**上游账户余额**，不参与内部 quota 计算。客户扣的是 `users.quota` / `tokens.remain_quota`，与 `channels.balance` 无关。

详见灰区 [#13](99-pending-items.md#13-channelsbalance-不参与计费)。

### 场景 12：渠道路由错乱 / `abilities` 与渠道列表不一致

1. 找一条具体证据：`(group, model)` 应当命中渠道 A，实际命中渠道 B（或 404 no_satisfied_channel）。
2. 点 `/channels` 上的「修复」按钮（`POST /api/channel/fix`） → `model/ability.go:287 FixAbility`：truncate + 重建。
3. ⚠️ `FixAbility` 高并发下与渠道写入并发可能短暂不一致（灰区 [#4](99-pending-items.md#4-fixability-高并发不一致)）。建议在低峰期执行。

### 场景 13：默认存在 `svip` 分组但客户的令牌切到 svip 就 403

原因：后端默认 `GroupRatio` 注册了 `default/vip/svip`，但 `UserUsableGroups` 默认只有 `default/vip`（`setting/user_usable_group.go:10-13`）。

处理：在 `/system-settings/billing/group-pricing` 把 `svip` 加入 `UserUsableGroups`。

详见灰区 [#6](99-pending-items.md#6-svip-默认半启用)。

---

## 5.2 灰区 → 运营行动建议汇总表

> 架构师摸底报告标记的 13 条「事实模糊、文档冲突或代码未覆盖」点已**全部闭环**（12 条 `closed-2026-05-21` + 1 条 `closed-by-fact`，详见 [`99-pending-items.md`](99-pending-items.md)）。
> 下表把每一条结论翻译成运营层面的「具体该怎么做」。

| # | 闭环结论 | 运营行动建议 |
|---|---|---|
| 1 | 未注册到 `ModelRatio` 的模型：文本路径有「`SelfUseMode` + `AcceptUnsetRatioModel`」两道闸门兜底；音频/Realtime/任务路径**无闸门**，按 37.5 倍率静默扣费 | **上线新模型前必须在 `ModelRatio` / `ModelPrice` 显式注册**。已上线但未注册的模型立即补录。不要依赖 37.5 默认值。 |
| 2 | `TopupGroupRatio` 是**充值折扣 / 加价系数**：`payMoney = amount × Price × TopupGroupRatio × Discount`，仅影响充值付款，不影响调用扣费。fallback 1.0 不阻断充值 | 上线新充值倍率前**做一次端到端小额测试**（如 1 USD），核对到账 quota；解释客户「充值打折」时记得这套与计费侧 `GroupRatio` 是两条独立路径。 |
| 3 | 未注册的 group 被消费时默默按 1 倍计费（fallback 是有意保留，兼容历史迁移） | **`channels.group` 中出现的每个分组必须先在 `GroupRatio` 注册**；新建渠道时审核分组名拼写。修改 `GroupRatio` 时同步检查所有 `channels.group` 引用。 |
| 4 | `FixAbility` 单实例已加 `sync.Mutex`；执行期间所有请求 503；多实例集群无跨进程锁 | 修复路由错乱时**选低峰期 + 单实例操作**；点完后等 1 分钟再用测试令牌验证一次；集群部署需运维分时。 |
| 5 | `GroupRatio` 与 `UserUsableGroups` 必须双写（前端两 tab 分开保存，**自动联动校验尚未实现**） | 配置分组时**两套配置同步更新**，否则用户切换令牌分组时报 403。 |
| 6 | 默认 SVIP 分组「半启用」（在 `GroupRatio` 但不在 `UserUsableGroups`） | 想真正启用 SVIP，**手动把 `svip` 加入 `UserUsableGroups`**（首次部署 workaround，后续版本会对齐默认值）。 |
| 7 | `logs.ip` 默认不记（合规：GDPR / PIPL 最小化原则，保持默认 false） | 排错前先在 `users.setting.record_ip_log` 开启；历史日志补不回来。 |
| 8 | `tokens.model_limits_enabled=true` + `model_limits=''` → 实际报错 `MsgDistributorTokenModelForbidden`（**不是**「token model limit is empty」，文案订正） | 客户创建令牌时**勾选白名单必须至少填一个模型**；FAQ 给出完整排查链。 |
| 9 | `OtherRatios` 是**任务/图像类计费的动态参数倍率**（视频 `seconds`/`size`、图像 `n` 等），由 adapter 自动计算，运营不需配置 | **运营无需也不能配置**；任务扣费高于预期时，看 `logs.other.OtherRatios` 字段确认命中了哪些动态倍率。 |
| 10 | `logs.group` 仅记最终 usingGroup，无独立 `user_group` / `token_group` 字段 | 按 group 统计时**明示口径**：`logs.group` = 最终生效分组，不区分来源。**追溯切组路径需打开日志详情查 `other.user_group / token_group`**（待扩展 other 字段后）。 |
| 11 | `rpm` / `tpm` 是「最近 60 秒」截面值（`model/log.go:482`） | 对外解释**永远显式说「按最近 60 秒计算」**；区间 RPM/TPM 需自跑 SQL 计算。 |
| 12 | `quota_data` 表无 DELETE 入口，需 DBA 手动维护 | **`quota_data` 表暂无清理入口**，长生命周期实例（5+ 年）需 DBA 手动维护，建议每 6 个月归档一次 12 个月以前的行。 |
| 13 | `channels.balance` 不参与扣费 | 客户咨询「渠道余额为 0」时**明确说不影响扣费**；该字段只用于运营监控上游账户是否需要充值。 |

> 5 项后续修复建议（PJM 另开 issue 跟踪）见 [`99-pending-items.md` 附录 · 已知改进点](99-pending-items.md#附录--已知改进点pjm-另开-issue-跟踪)。

---

## 5.3 排错速查矩阵

| 客户反馈 | 第一步看哪里 | 主要可能原因 |
|---|---|---|
| 401 invalid token | 令牌是否过期 / 被禁用 / IP 不在白名单 | `tokens.status`、`expired_time`、`allow_ips` |
| 403 group denied | `tokens.group` 与 `UserUsableGroups` / `GroupRatio` | 灰区 [#5](99-pending-items.md#5-groupratio-与-userusablegroups-必须双写) / [#6](99-pending-items.md#6-svip-默认半启用) |
| 403「该 token 不允许使用模型 X」（`MsgDistributorTokenModelForbidden`） | 令牌模型白名单 | 灰区 [#8](99-pending-items.md#8-model_limits-为空-403错误文案订正) |
| 404 no satisfied channel | `(usingGroup, model)` 在 `abilities` 找不到行 | 渠道未挂该分组 / 模型；或路由错乱（修复 abilities，灰区 [#4](99-pending-items.md#4-fixability-高并发不一致)） |
| 扣费金额不对 | `logs.other` JSON 倍率明细 | 第 3 章公式 + 客户分组 |
| 看不到 IP | `users.setting.record_ip_log` | 灰区 [#7](99-pending-items.md#7-logsip-默认不记) |
| 充值到账金额不对 | `topup` 表 + `TopupGroupRatio` | 灰区 [#2](99-pending-items.md#2-topupgroupratio充值折扣加价系数不影响计费) |
| 渠道余额 0 但能扣费 | `channels.balance` 与扣费无关 | 灰区 [#13](99-pending-items.md#13-channelsbalance-不参与计费) |
| 看板数据滞后 | `DataExportInterval`（默认 5 分钟）刷盘 | 正常现象 |
| 长期归档需求 | `logs.DeleteOldLog` 可清理；`quota_data` 无清理 | 灰区 [#12](99-pending-items.md#12-quota_data-表无清理路径) |

---

## 5.4 常用查询语句（DBA / 运营自助）

> 直连日志库（`LOG_DB`）跑只读查询。

```sql
-- 1. 某用户某天的总消费
SELECT SUM(quota) AS quota, COUNT(*) AS calls
  FROM logs
 WHERE type = 2 AND user_id = ?
   AND created_at BETWEEN UNIX_TIMESTAMP('2026-05-01') AND UNIX_TIMESTAMP('2026-05-02');

-- 2. 按渠道看本周消费
SELECT channel_id, SUM(quota) AS quota
  FROM logs
 WHERE type = 2
   AND created_at >= UNIX_TIMESTAMP() - 7*86400
 GROUP BY channel_id
 ORDER BY quota DESC;

-- 3. 找扣费最高的请求
SELECT id, user_id, model_name, quota, prompt_tokens, completion_tokens, created_at
  FROM logs
 WHERE type = 2
 ORDER BY quota DESC
 LIMIT 50;

-- 4. 按 usingGroup 看一段时间的消费分布
SELECT `group`, SUM(quota) AS quota, COUNT(*) AS calls
  FROM logs
 WHERE type = 2
   AND created_at BETWEEN ? AND ?
 GROUP BY `group`;

-- 5. 看某客户的最近 60 秒真实 RPM（与平台 stat 字段一致）
SELECT COUNT(*) AS rpm,
       SUM(prompt_tokens) + SUM(completion_tokens) AS tpm
  FROM logs
 WHERE type = 2 AND user_id = ?
   AND created_at >= UNIX_TIMESTAMP() - 60;
```

---

## 5.5 升级路径与回滚

> 给运营做参考，不替代研发的部署文档。

- 修改倍率（`PUT /api/option/`）→ 内存与数据库同步更新，**立即生效**，无需重启。
- 修改 `users.setting.record_ip_log` → 只对新日志生效。
- 修改令牌、渠道 → 立即生效，已建立的连接需要新请求才走新配置。
- 删除分组 → 须先把 `channels.group` / `users.group` / `tokens.group` 中的引用清空再删，否则会产生「未注册分组按 1 倍计费」的悄悄扣费（灰区 [#3](99-pending-items.md#3-未注册-groupratio-默默按-1-倍计费)）。
- 撤销倍率变更 → 在前端编辑回原值即可，或调用 `POST /api/reset_model_ratio` 系列重置接口（`controller/pricing.go:79`）恢复 NewAPI 默认。

---

## 参考代码索引

- `middleware/auth.go:382-399` — token 鉴权 + 分组校验
- `middleware/distributor.go:58-65` — 模型白名单 403 分支
- `model/ability.go:287 FixAbility` — abilities 重建
- `setting/user_usable_group.go:10-13` — UserUsableGroups 默认值
- `setting/ratio_setting/group_ratio.go:12-16, 84-91` — GroupRatio 默认值与 fallback
- `setting/ratio_setting/model_ratio.go:403-417 GetModelRatio` — 未注册模型 fallback
- `service/channel_affinity.go` — 渠道亲和度
- `controller/log.go:153 DeleteHistoryLogs` — 日志清理
- `model/log.go:451, 482` — SumUsedQuota / rpm/tpm 口径
