# 第 4 章 · 日志与数据看板

> 适用对象：客户已可调用并扣费，本章解释「调用之后的数据落到哪、怎么查、怎么聚合」。
>
> 三件事：单笔流水（`logs` 表）、小时级聚合（`quota_data` 表）、运营辅助（渠道亲和度）。

---

## 4.1 单笔流水：`logs` 表

> 这是 NewAPI 的「主账本」——所有计费、充值、管理、错误事件都在这里。
>
> **可单独使用 `LOG_DB`**：日志库可与主库分离，避免互相影响（`model/log.go:20-42`）。

### 4.1.1 表结构

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `user_id` | int | 复合索引 |
| `created_at` | int64 | unix 秒 |
| **`type`** | int | **日志类型，下文详解** |
| `content` | text | 人类可读说明（含倍率 / 价格摘要） |
| `username` | string | 冗余（写入时打平） |
| `token_name` | string | 冗余 |
| `model_name` | string | 冗余（被 `gpt-4-gizmo-*` 等折叠，`service/text_quota.go:378-385`） |
| `quota` | int | 单次扣费 quota |
| `prompt_tokens` | int | 上游返回的输入 token 数 |
| `completion_tokens` | int | 上游返回的输出 token 数 |
| `use_time` | int | 调用耗时（秒） |
| `is_stream` | bool | 是否流式 |
| `channel_id` | int | 渠道 |
| `channel_name` | string (`gorm:"->"` 虚拟列) | 查询时 join 填充 |
| `token_id` | int | 令牌 |
| `group` | string | **最终生效的 usingGroup**（不是 user.group / channel.group） |
| `ip` | string | 客户端 IP（**默认不记**，见下文） |
| `request_id` | string | NewAPI 自生成 |
| `upstream_request_id` | string | 上游 API 返回的 request id |
| `other` | text(JSON) | 详细计费信息（见 4.1.3） |

### 4.1.2 `type` 取值

证据：`model/log.go:45-53`

| `type` | 含义 | 写入函数 |
|---|---|---|
| `1` | **Topup**（充值） | `model.RecordTopupLog`（`model/log.go:119`） |
| `2` | **Consume**（消费，最常见） | `model.RecordConsumeLog`（`model/log.go:208`） |
| `3` | **Manage**（管理操作） | `model.RecordLog` / `RecordLogWithAdminInfo`（`model/log.go:77, 96`） |
| `4` | **System**（系统事件） | `model.RecordLog` |
| `5` | **Error**（错误） | `model.RecordErrorLog`（`model/log.go:147`） |
| `6` | **Refund**（退款） | （由业务流程触发 `RecordLog`，不单独定义函数） |
| — | 任务计费（独立路径，仍写到 `logs`） | `model.RecordTaskBillingLog`（`model/log.go:273`） |

### 4.1.3 `other` JSON 含什么

由 `service/text_quota.go:388-450` 写入，主要包括：

- 倍率明细：`model_ratio`、`group_ratio`、`completion_ratio`、`cache_ratio`、`image_ratio`、`audio_ratio` 等的实际取值
- token 拆分：`prompt_tokens` / `completion_tokens` / `cached_tokens` / `cached_creation_tokens` / `image_tokens` / `audio_tokens`
- 工具调用：每个工具的 `price` × `count`
- 分级计费快照：若命中 `tiered_expr`，记录表达式与求值过程
- 管理员信息（`type=3`）：`admin_info` 含操作者 IP、节点名、版本（`RecordLogWithAdminInfo`）

> 💡 **客户对账要细节时翻 `other`**。这是「为什么扣这么多」的最完整证据。

### 4.1.4 `ip` 字段默认不记

`model/log.go:155-160, 218-223`：仅当 `users.setting.record_ip_log == true` 时才写。

> ⚠️ 运营按 IP 排错前需要：
> 1. 让客户在个人设置开启 IP 日志，或
> 2. 后台批改 `users.setting.record_ip_log = true`。
>
> 详见末尾追踪表 [#7](99-pending-items.md#7-logsip-默认不记)。

### 4.1.5 日志清理

`controller/log.go:153 DeleteHistoryLogs` → `model.DeleteOldLog`：

- 按 `created_at < target_timestamp` 删除
- 分批 100 条删除（`model/log.go:518`）
- 前端入口：`/usage-logs` 管理员可见的「清理历史日志」按钮

---

## 4.2 日志查询 API + 前端入口

`<截图：使用日志 - 通用日志列表 - 筛选条件>`

| 入口 | 路径 | controller | model 层 |
|---|---|---|---|
| 管理员看全量 | `GET /api/log/`（paged） | `controller/log.go:13 GetAllLogs` | `model.GetAllLogs`（`model/log.go:304`） |
| 用户看自己 | `GET /api/log/self` | `controller/log.go:36 GetUserLogs` | `model.GetUserLogs`（`model/log.go:387`） |
| 管理员统计 | `GET /api/log/stat` | `controller/log.go:98 GetLogsStat` | `model.SumUsedQuota`（`model/log.go:451`） |
| 用户自统计 | `GET /api/log/self/stat` | `controller/log.go:125 GetLogsSelfStat` | 同上 |
| 按 token key 查 | `GET /api/log/token` | `controller/log.go:74 GetLogByKey` | `model.GetLogByTokenId`（`model/log.go:71`） |
| 清理 | `DELETE /api/log/` | `controller/log.go:153 DeleteHistoryLogs` | `model.DeleteOldLog`（`model/log.go:518`） |
| ~~`SearchAllLogs` / `SearchUserLogs`~~ | ~~`GET /api/log/search` / `self/search`~~ | **已废弃**（`controller/log.go:58-72`） |  |

### 4.2.1 前端入口

| 路径 | 用途 |
|---|---|
| `/usage-logs/common` | 文本类调用日志 |
| `/usage-logs/drawing` | 绘图类（对应 `/api/mj`） |
| `/usage-logs/task` | 异步任务类（对应 `/api/task`） |

证据：`web/default/src/features/usage-logs/section-registry.tsx:24-43`、`web/default/src/features/usage-logs/api.ts:35-37`。

普通用户与管理员**进同一页面**：`buildApiPath` 自动切换 `/api/log/self` vs `/api/log/`。

### 4.2.2 过滤维度

均支持（用 `LIKE+ESCAPE` 方式做模糊匹配的字段标星）：

- `username` ★
- `token_name` ★
- `model_name` ★
- `channel_id`
- `group`（**最终 usingGroup**）
- `type`（按类型过滤）
- `request_id`
- `upstream_request_id`
- `created_at` 区间（start / end）

### 4.2.3 `logs.group` 的口径

> 🔴 **关键运营提醒**：`logs.group` 写的是**最终 usingGroup**。
>
> 客户原本在 `vip` 但调用时令牌切到 `svip` → `logs.group = 'svip'`。
> 想分开看「以 vip 身份切到 svip」与「直接 svip 用户」当前**做不到** — 因为 user_group 与 token_group **未单独落库**，只在 `other.group_ratio_special` 命中 GroupGroupRatio 时写一处。
>
> **追溯切组路径**：打开日志详情查 `other.user_group / other.token_group`（**待 logs.other 扩展后可用**，详见 `99-pending-items.md` 附录 A 跟踪 issue）。当前期间，按用户 ID + 令牌 ID 维度聚合是 workaround。
>
> 详见末尾追踪表 [#10](99-pending-items.md#10-logsgroup-仅记-usinggroup)。

---

## 4.3 统计口径（`SumUsedQuota`）

> 「客户问消费多少 / RPM / TPM 是多少」时，看这里。

证据：`model/log.go:451`

```
quota = sum(logs.quota) WHERE type=2 (Consume)
        + 任意组合过滤（username / token_name / model_name / channel / group / start / end）

rpm   = count(*)
        WHERE type=2 AND created_at >= now-60s

tpm   = sum(prompt_tokens) + sum(completion_tokens)
        WHERE type=2 AND created_at >= now-60s
```

> ⚠️ **`rpm` / `tpm` 是「截面值，按最近 60 秒计算」**（`model/log.go:482`），不是用户选定时间区间的平均。
> controller 返回的字段名只叫 `rpm/tpm`，前端可能误解。**运营对外解释时务必显式说「按最近 60 秒计算」**。
> 区间均值需自跑 SQL（见第 5 章 5.4 常用查询）。
> 详见末尾追踪表 [#11](99-pending-items.md#11-rpmtpm-是-60-秒截面)。

---

## 4.4 数据看板：`quota_data` 表（小时级聚合）

`<截图：仪表盘 - 模型 × 小时柱状图>`

### 4.4.1 表结构

证据：`model/usedata.go:13-22`

| 字段 | 含义 |
|---|---|
| `id` | 主键 |
| `user_id` | 用户 |
| `username` | 冗余 |
| `model_name` | 模型名 |
| `created_at` | **按小时取整**（`model/log.go:60`） |
| `token_used` | 当前 (user, model, hour) 的累计 token |
| `count` | 当前 (user, model, hour) 的累计请求数 |
| `quota` | 当前 (user, model, hour) 的累计 quota |

### 4.4.2 写入流程

```
每条 Consume 日志（model.RecordConsumeLog 第 254-258 行）
  └─ 启动 goroutine 调用 LogQuotaData
       └─ 内存累加进 CacheQuotaData map
            └─ 每 common.DataExportInterval 分钟（默认 5）刷盘
                 └─ 写入 quota_data 表（model/usedata.go:24-32 UpdateQuotaData）
```

**开关**：`common.DataExportEnabled`（默认 true）。关掉后看板停止更新，但 `logs` 仍写入。

### 4.4.3 查询 API

| 入口 | 文件 |
|---|---|
| 管理员模型维度 | `GET /api/data/` → `GetAllQuotaDates`（按 model_name + created_at 聚合） |
| 管理员用户维度 | `GET /api/data/users` → `GetQuotaDatesByUser` |
| 用户自己 | `GET /api/data/self` → `GetUserQuotaDates` |

### 4.4.4 前端入口

`/dashboard`（`web/default/src/features/dashboard/`）：模型 × 小时柱状图，可选 `quota` / `count` / `token_used` 三个维度。

### 4.4.5 表清理

> ⚠️ **`quota_data` 表暂无清理入口，长生命周期实例需 DBA 手动维护**：
>
> - `model/usedata.go` 全文 138 行，只有 INSERT / UPDATE / SELECT（`grep -n "DELETE|Delete|truncate"` 0 匹配）。
> - `controller/log.go:153 DeleteHistoryLogs` 只清 `logs` 表，不动 `quota_data`。
> - 量级评估：每用户每模型每小时 1 行，10000 用户 × 20 模型 × 24h × 365 天 ≈ 17 亿行；**1 年以内通常无影响，5+ 年级别会达瓶颈**。
> - 建议每 6 个月由 DBA 手动归档 12 个月以前的行。
>
> 后续将由代码层补清理接口（`99-pending-items.md` 附录 A5），届时本提示失效。
> 详见末尾追踪表 [#12](99-pending-items.md#12-quota_data-表无清理路径)。

---

## 4.5 渠道亲和度统计（运营辅助，非财务口径）

> 用来让「同一用户在短时间内黏到上次成功的 channel」，提升体验与稳定性。
> **不参与计费**，仅影响路由决策。

| 项 | 内容 |
|---|---|
| 实现 | `service/channel_affinity.go`（25.9K） |
| API（admin） | `GET /api/option/channel_affinity_cache`、`GET /api/log/channel_affinity_usage_cache` |
| 用途 | 优化渠道选择，**不写消费日志、不影响 used_quota** |

---

## 4.6 配额变更日志：哪去了？

> NewAPI **没有独立的 `quota_change_log` 表**。所有 quota 变更体现在三处：

1. **`users.used_quota` / `users.quota` 的实时增量**（无审计行）；
2. **`logs` 表中**的 `type=1`（Topup）/ `type=6`（Refund）/ `type=2`（Consume）/ `type=3`（Manage）行；
3. **管理员后台改额度**：通过 `RecordLogWithAdminInfo` 写到 `logs.other.admin_info`，含操作者 IP、节点名、版本（`model/log.go:96-117`）。

运营做审计时：
- 客户主动行为（充值 / 调用 / 退款）：直接看 `logs` 对应类型行。
- 内部调整（手动加减额度）：看 `logs.type=3`，并展开 `other.admin_info` 看是谁操作的。

---

## 4.7 表与统计速查

| 表 | 角色 | 是否财务源 |
|---|---|---|
| `users` | 用户余额 / 累计消费 / 请求数 | **是**（`quota`, `used_quota`, `request_count`） |
| `tokens` | 令牌余额 / 分组 / 模型白名单 | **是**（`remain_quota`, `used_quota`） |
| `channels` | 渠道身份 + 累计消耗 | **是**（`used_quota`） |
| `abilities` | (group × model → channel) 派生路由表 | 否（仅路由） |
| `logs` | 单笔流水（消费 / 充值 / 管理 / 错误 / 退款 / 系统 / 任务计费） | **是**（`quota` 是审计源） |
| `quota_data` | 用户 × 模型 × 小时聚合 | 否（看板用，从 logs 派生） |
| `models` | 模型元数据 | 否 |
| `vendors` | 供应商元数据 | 否 |
| `options` | 全部 ratio / group / 系统设置 JSON | **是**（所有倍率定义） |
| `redemptions` | 兑换码 | 充值入口 |
| `topup` | 充值订单 | 充值入口 |

---

## 参考代码索引

- `model/log.go:20-42, 45-53, 60, 71, 77, 96-117, 119, 147, 155-160, 208, 218-223, 254-258, 273, 304, 387, 451, 482, 518` — logs 表 / 写入 / 查询 / 清理 / 统计
- `model/usedata.go:13-22, 24-32` — quota_data 表
- `controller/log.go:13, 36, 58-72, 74, 98, 125, 153` — 日志查询 / 清理 controller
- `service/text_quota.go:378-385, 388-450` — model_name 折叠 + other JSON
- `service/channel_affinity.go` — 渠道亲和度
- `web/default/src/features/usage-logs/section-registry.tsx:24-43` — 前端日志入口
- `web/default/src/features/usage-logs/api.ts:35-37` — 前端 API 路径切换
- `web/default/src/features/dashboard/` — 看板前端
