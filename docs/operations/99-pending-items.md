# 待确认事项追踪表

> 来源：架构师事实层摸底报告（issue [TES-69](mention://issue/7628a2ad-bf09-4050-b197-98910ff11357) 评论 `e2af1c61` 第 4 节）
> 维护规则：每条含 编号 / 现象描述 / 当前手册措辞 / 责任人 / 状态（open / closed）
> 关闭流程：架构师追加证据 → 文档维护专家更新对应章节 + 关闭本表中的条目（状态改 `closed` 并附证据 PR / commit）

---

## 1. 未注册模型默认倍率

| 项 | 内容 |
|---|---|
| **现象描述** | `setting/ratio_setting/model_ratio.go:403-417 GetModelRatio` 在模型未注册时返回 `(37.5, operation_setting.SelfUseModeEnabled, name)`，含义是默认 37.5 倍率，且只有 `SelfUseMode` 开启时才算「有定价」。SelfUseMode 关闭时这个 37.5 是否真扣费、`relayInfo.PriceData` 装载逻辑是否会绕开它，**未追完证据**。 |
| **当前手册措辞** | 第 1 章 1.2.4 与第 5 章场景 1 / 灰区 #1：「上线新模型前必须在 ModelRatio 显式注册；未注册时的扣费行为（疑似 37.5 倍率且仅 SelfUseMode 生效）待二次确认。」 |
| **责任人** | 架构师 |
| **状态** | open |
| **闭环需要的证据** | `relayInfo.PriceData` 装载链 + SelfUseMode 关闭时实际走哪条分支 |

---

## 2. TopupGroupRatio 生效路径

| 项 | 内容 |
|---|---|
| **现象描述** | option key `TopupGroupRatio` 已注册（`web/default/src/features/system-settings/billing/section-registry.tsx:43`），用于充值打折 / 到账折算，但具体在 `controller/topup.go` 的哪条分支里完成换算**未追完**。 |
| **当前手册措辞** | 第 2 章 2.3.1 与第 5 章场景 10：「用于充值打折 / 到账折算，具体在 `controller/topup.go` 的生效分支待二次确认。」 |
| **责任人** | 架构师 |
| **状态** | open |
| **闭环需要的证据** | `controller/topup.go` 中 `TopupGroupRatio` 被读取的具体函数与分支 |

---

## 3. 未注册 GroupRatio 默默按 1 倍计费

| 项 | 内容 |
|---|---|
| **现象描述** | `setting/ratio_setting/group_ratio.go:84-91`：未在 `groupRatioMap` 注册的 group 被消费时，会打 SysLog 并按 1 倍计费，**不报错也不阻断**。 |
| **当前手册措辞** | 第 1 章 1.3.3 + 第 5 章灰区 #3：「`channels.group` 中出现的每个分组必须先在 `GroupRatio` 注册。」 |
| **责任人** | 架构师（确认是否为故意设计） |
| **状态** | open（已有代码证据，但**是否需要把 fallback 改为报错或显式 0** 待产品决定） |
| **闭环需要的证据** | 产品 / 架构师明确：fallback 1 是有意还是 bug |

---

## 4. FixAbility 高并发不一致

| 项 | 内容 |
|---|---|
| **现象描述** | `model/ability.go:295-307 FixAbility` 是 truncate `abilities` 表 + 按所有 channel 重建。高并发下若与渠道写入并发，可能短暂不一致。 |
| **当前手册措辞** | 第 1 章 1.4.3 + 第 5 章场景 12 / 灰区 #4：「修复路由错乱时选低峰期点修复按钮；点完后等 1 分钟再用测试令牌验证一次。」 |
| **责任人** | 架构师（评估是否需要加锁或换为增量同步） |
| **状态** | open |
| **闭环需要的证据** | 是否需要排他锁；并发窗口实测时长 |

---

## 5. GroupRatio 与 UserUsableGroups 必须双写

| 项 | 内容 |
|---|---|
| **现象描述** | `middleware/auth.go:391-396` 要求 `tokens.group` 同时存在于 `GroupRatio` 与 `UserUsableGroups[user.group]`，但前端在两个 tab 编辑（`/system-settings/billing/group-pricing`），**未做联动校验**。 |
| **当前手册措辞** | 第 1 章 1.3.5 + 第 5 章场景 2 / 场景 3 / 灰区 #5：「配置分组时两套配置同步更新，否则用户切换令牌分组时报 403。」 |
| **责任人** | 架构师 / 前端（评估是否在保存时做联动校验提示） |
| **状态** | open |
| **闭环需要的证据** | 决策：是写到文档强约束就够，还是要在前端加保存校验 |

---

## 6. SVIP 默认半启用

| 项 | 内容 |
|---|---|
| **现象描述** | 后端默认 `defaultGroupRatio` 写死 `default/vip/svip = 1`（`setting/ratio_setting/group_ratio.go:12-16`），但 `setting/user_usable_group.go:10-13` 默认只放 `default/vip`。结果是 SVIP 默认存在于 `GroupRatio` 但不在 `UserUsableGroups`，任何用户都无法把令牌切到 svip 直到运营手动添加。 |
| **当前手册措辞** | 第 1 章 1.3.5 + 第 5 章场景 13 / 灰区 #6：「想真正启用 SVIP，手动把 svip 加入 UserUsableGroups。」 |
| **责任人** | 架构师 / 产品（决定是否对齐两份默认值） |
| **状态** | open |
| **闭环需要的证据** | 决策：默认值是否对齐；若不对齐，是否在 `/system-settings/billing/group-pricing` 给运营显式提示 |

---

## 7. iplog 默认不记

| 项 | 内容 |
|---|---|
| **现象描述** | `model/log.go:155-160, 218-223`：`logs.ip` 仅在 `users.setting.record_ip_log == true` 时写入，默认 false。 |
| **当前手册措辞** | 第 2 章 2.1.3 + 第 4 章 4.1.4 + 第 5 章场景 9 / 灰区 #7：「排错前先确认对应用户已开启；历史日志补不回来。」 |
| **责任人** | 架构师 / 产品 |
| **状态** | open（更多是产品决策：默认值是否要改为 true，还是仅在管理员后台增加批量开关） |
| **闭环需要的证据** | 决策：默认值是否要改 |

---

## 8. modellimits 为空 403

| 项 | 内容 |
|---|---|
| **现象描述** | `middleware/distributor.go:58-65`：`tokens.model_limits_enabled=true` 但 `model_limits=''` 时直接 403 "token model limit is empty, all models are not allowed"——前端创建令牌时若误选启用却忘填模型，整个 token 不可用且报错信息不直观。 |
| **当前手册措辞** | 第 2 章 2.2.2 + 第 5 章场景 4 / 灰区 #8：「客户创建令牌时勾选白名单必须至少填一个模型；运营在前端教程中显著提示。」 |
| **责任人** | 前端（评估保存时是否阻止此组合） |
| **状态** | open |
| **闭环需要的证据** | 决策：前端是否加保存校验 |

---

## 9. OtherRatios 疑似预留

| 项 | 内容 |
|---|---|
| **现象描述** | `service/text_quota.go:281-285`：从 `relayInfo.PriceData.OtherRatios` 取值并连乘，但本次未找到给 `OtherRatios` 赋值的代码路径，且没有前端配置入口。 |
| **当前手册措辞** | 第 3 章 3.1.3 + 第 5 章灰区 #9：「代码中有 Π OtherRatios 连乘但无赋值路径与前端入口，疑似预留字段，当前运营无需配置。」 |
| **责任人** | 架构师 |
| **状态** | open |
| **闭环需要的证据** | 是预留 / 已废弃 / 内部隐藏字段中的哪种？ |

---

## 10. logsgroup 无法区分 user vs token 切换

| 项 | 内容 |
|---|---|
| **现象描述** | `logs.group` 写的是 `usingGroup`（最终生效那个），**未把 user_group / token_group 单独落库**。`other` JSON 里仅在命中 GroupGroupRatio 时存 `group_ratio_special`。管理员按 group 检索时无法直接区分「客户原本属于 vip，但调用时用了 token.group=svip」。 |
| **当前手册措辞** | 第 4 章 4.2.3 + 第 5 章灰区 #10：「按 group 统计时明示口径；需要拆分时单独按用户 ID + 令牌 ID 维度聚合。」 |
| **责任人** | 架构师 / 产品（评估是否扩展 logs schema） |
| **状态** | open |
| **闭环需要的证据** | 是否要新增 `logs.user_group` / `logs.token_group` 字段 |

---

## 11. rpmtpm 是 60 秒截面

| 项 | 内容 |
|---|---|
| **现象描述** | `model/log.go:482`：`rpm` / `tpm` 用 `created_at >= now-60s` 计算，是「最近 60 秒」的截面，不是用户选定时间区间的平均。`controller/log.go:113-121` 返回字段名只叫 `rpm/tpm`，前端可能误解为时间区间均值。 |
| **当前手册措辞** | 第 4 章 4.3 + 第 5 章场景 8 / 灰区 #11：「对外解释永远显式说『最近 60 秒』；区间 RPM/TPM 需自跑 SQL。」 |
| **责任人** | 前端（评估是否在 UI 上加 tooltip 说明） |
| **状态** | open |
| **闭环需要的证据** | UI 文案改进决定 |

---

## 12. quotadata 无清理路径

| 项 | 内容 |
|---|---|
| **现象描述** | `model/usedata.go` 全文 138 行，只有 INSERT / UPDATE / SELECT，**未发现 DELETE 入口**。运营若做了 5+ 年数据，这张表会无限增长。 |
| **当前手册措辞** | 第 4 章 4.4.5 + 第 5 章灰区 #12：「定期由 DBA 归档历史数据；建议每 6 个月清理一次 12 个月以前的行。」 |
| **责任人** | 架构师 / DBA |
| **状态** | open |
| **闭环需要的证据** | 是否补一个清理接口 / cron job |

---

## 13. channelsbalance 不参与计费

| 项 | 内容 |
|---|---|
| **现象描述** | `channels.balance` / `balance_updated_time` 是上游账户余额（通过 `update_balance` API 拉取，`controller/channel-billing.go`），**不参与 NewAPI 内部 quota 计算**。运营看到 channel 余额为 0 ≠ 客户扣费失败。 |
| **当前手册措辞** | 第 1 章 1.4.2 + 第 5 章场景 11 / 灰区 #13：「客户咨询『渠道余额为 0』时明确说不影响扣费；该字段只用于运营监控上游账户是否需要充值。」 |
| **责任人** | （已有事实证据，事实层无需追加） |
| **状态** | closed-by-fact（事实清楚，仅作存档；运营手册中已明示口径） |
| **闭环需要的证据** | — |

---

## 状态汇总

| 状态 | 数量 |
|---|---|
| open | 12（#1 ~ #12） |
| closed-by-fact | 1（#13） |

---

## 维护说明

- 本表由文档维护专家维护，架构师追加证据后由文档维护专家关闭对应条目并更新正文措辞。
- 关闭一条 = 在状态列改为 `closed-YYYY-MM-DD` 并保留闭环证据链接（commit / PR / issue 评论）。
- 运营在排错过程中如果发现新的灰区，**追加为 #14、#15...**，不要改既有编号。
