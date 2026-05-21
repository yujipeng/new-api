# 第 2 章 · 客户接入

> 适用对象：第 1 章已完成，平台可用。本章把客户带到「拿到 sk-xxx 即可发请求」的状态。
>
> 步骤：用户身份建档 → 创建令牌 → 充值 / 兑换码到账。

---

## 2.1 步骤一：建立用户身份

### 2.1.1 三种创建方式

| 来源 | 入口 | 备注 |
|---|---|---|
| 运营手动新增 | `/users` 后台（admin） | 用于内部账号、托管客户 |
| 用户自助注册 | `/(auth)/sign-up` 前端注册页 | 支持邮箱密码 |
| 第三方 OAuth | `/oauth/$provider` | github / discord / oidc / wechat / telegram / linux_do |

### 2.1.2 后端 API

`<截图：用户管理 - 新建用户 / 编辑用户>`

| API | 用途 | 文件 |
|---|---|---|
| `GET /api/user/` | 列用户（admin） | `router/api-router.go:64-180`、`controller/user.go` |
| `POST /api/user/` | 新增用户 |  |
| `PUT /api/user/` | 更新用户 |  |
| `DELETE /api/user/:id` | 删除用户 |  |
| `POST /api/user/manage` | 封禁 / 解封 / 提权 / 降权 |  |

### 2.1.3 `users` 表关键字段

`model/user.go:24-56`

**身份字段**：

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `username` | string (unique) | 登录名 |
| `password` | string | 加密密码 |
| `display_name` | string | 显示名 |
| `email` | string | 邮箱 |
| `role` | int | **1=common, 10=admin, 100=root** |
| `status` | int | 启停 / 封禁 |
| `github_id` / `discord_id` / `oidc_id` / `wechat_id` / `telegram_id` / `linux_do_id` | string | 各 OAuth 关联 |
| `access_token` | string | 系统管理用的内部 token（**与计费 token 是两回事**） |
| `stripe_customer` | string | Stripe 客户号（如启用 stripe 充值） |

**计费 / 统计字段**：

| 字段 | 类型 | 含义 |
|---|---|---|
| `quota` | int | 剩余额度（**实时扣减**，单位 quota） |
| `used_quota` | int | 累计消费 quota |
| `request_count` | int | 累计请求数 |
| `group` | string (varchar(64), default `default`) | 用户分组 |
| `aff_code` | string | 推广码 |
| `aff_count` | int | 已推广人数 |
| `aff_quota` | int | 推广奖励额度（可提现部分） |
| `aff_history` | int / json | 推广历史额度（DB 字段名 `aff_history`，JSON 字段 `aff_history_quota`） |
| `inviter_id` | int | 邀请人 user_id |

**时间与设置**：

| 字段 | 类型 | 含义 |
|---|---|---|
| `created_at` | int64 |  |
| `last_login_at` | int64 |  |
| `deleted_at` | gorm soft delete | 软删 |
| `setting` | text(JSON, `dto.UserSetting`) | 含 `record_ip_log`、`notify_type`、`quota_warning_threshold` 等 |

> ⚠️ **`users.setting.record_ip_log` 默认 false**。运营按 IP 排错前需要让用户在个人设置中开启，或通过后台批改 `users.setting`。详见末尾追踪表 [#7](99-pending-items.md#7-iplog-默认不记)。

### 2.1.4 操作准则

- 给客户分配分组时**先确认分组在 `GroupRatio` 与 `UserUsableGroups` 都注册了**（见 1.3.5）。
- `role=100`（root）只能由数据库或安装时确定，不要在普通运维流程中授予。
- 删除用户为软删，`deleted_at` 不为 NULL；如需硬删需 DBA 介入。

---

## 2.2 步骤二：用户创建令牌（sk-xxx）

> 令牌（Token）是客户调用 OpenAI 兼容接口时 `Authorization: Bearer sk-xxx` 的真身。

`<截图：令牌管理 - 新建令牌 - 表单>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/keys`（用户 / 管理员都可访问，`web/default/src/routes/_authenticated/keys/index.tsx`） |
| 后端 API | `POST/PUT/DELETE/GET /api/token/`，`POST /api/token/:id/key` 拿明文 key（`router/api-router.go:261-273`） |
| 数据库表 | `tokens`（`model/token.go:14-32`） |

### 2.2.1 `tokens` 表字段

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `user_id` | int (索引) | 所属用户 |
| `key` | string (varchar(128), unique) | 实际的 sk-xxx |
| `status` | int | 启停 |
| `name` | string | 令牌名 |
| `created_time` | int64 |  |
| `accessed_time` | int64 | 最近调用时间 |
| `expired_time` | int64 | 过期时间戳；`-1` = **永不过期** |
| `remain_quota` | int | 剩余额度（**实时扣减**） |
| `unlimited_quota` | bool | 是否不限额（不限额则不扣 `remain_quota`，但仍扣 `users.quota`） |
| `model_limits_enabled` | bool | 是否启用模型白名单 |
| `model_limits` | text | 逗号分隔模型白名单 |
| `allow_ips` | text | 回车分隔的 IP 白名单 |
| `used_quota` | int | 累计消费 |
| `group` | string | 令牌级分组（**可覆盖用户分组**） |
| `cross_group_retry` | bool | `group="auto"` 时是否跨组重试 |
| `deleted_at` | gorm soft delete | 软删 |

### 2.2.2 令牌可用性约束（鉴权两道关）

**第一道：分组校验**（`middleware/auth.go:380-398`）：

```
若 tokens.group != '':
    校验 tokens.group ∈ UserUsableGroups[users.group]   ← 不通过 = 403
    校验 GroupRatio[tokens.group] 存在                    ← 不通过 = 403
                                                          （auto 分组例外）
```

**第二道：模型白名单**（`middleware/distributor.go:57-75`）：

```
若 tokens.model_limits_enabled = true:
    若 tokens.model_limits = ''  →  403 "token model limit is empty, all models are not allowed"
    若 请求模型 ∉ tokens.model_limits  →  403
```

> ⚠️ **`model_limits_enabled=true` 但 `model_limits=''` 是常见误操作**：前端创建令牌时勾选启用却忘填模型，整个 token 不可用且报错信息不直观。运营提醒客户：要么不启用白名单，要么至少填一个模型。详见末尾追踪表 [#8](99-pending-items.md#8-modellimits-为空-403)。

### 2.2.3 操作准则

- 永久令牌（`expired_time = -1`）请确保配额合理，避免长期失控。
- 令牌的 `remain_quota` 与用户 `quota` 是**两个余额**：每次调用同时扣两边。`unlimited_quota=true` 时不扣令牌，但仍扣用户。
- IP 白名单 `allow_ips` 可以一行一个 IP 或 CIDR；空 = 不限制。

---

## 2.3 步骤三：充值与兑换码（让用户拥有 quota）

### 2.3.1 用户自助充值

`<截图：控制台 - 充值页>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/console/topup`（`web/default/src/routes/console/topup.tsx`） |
| 后端 controller | `controller/topup.go`（含 epay / stripe / creem / waffo 多支付方式） |
| 数据库表 | `topup`（`model/topup.go`） |

充值时的分组倍率：option key `TopupGroupRatio`，运营可对不同分组做「打折充值」。

> ⚠️ `TopupGroupRatio` 在 `controller/topup.go` 的具体生效分支当前**待二次确认**。详见末尾追踪表 [#2](99-pending-items.md#2-topupgroupratio-生效路径)。

### 2.3.2 兑换码（运营批量发码）

`<截图：兑换码管理 - 批量生成 - 列表>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/redemption-codes`（admin） |
| 后端 controller | `controller/redemption.go` |
| 数据库表 | `redemptions`（`model/redemption.go`） |

兑换码生成后，用户在 `/console/topup` 输入即可兑换 quota。

### 2.3.3 充值 / 兑换的余额体现

充值或兑换成功 → 直接增加 `users.quota`，并在 `logs` 表写入 `type=1`（Topup）一条流水（详见第 4 章）。

---

## 2.4 验收：客户是否可调用？

| 检查项 | 通过标准 |
|---|---|
| 用户已建立、`status=1`、`role` 合理（普通客户 = 1） | `users` 表存在该行 |
| 用户 `quota > 0`（除非走 `unlimited_quota` 令牌） | `users.quota` |
| 令牌已建立、`status=1`、未过期、`remain_quota > 0` | `tokens` 表 |
| 若设置了 `tokens.group`：`UserUsableGroups[users.group]` 与 `GroupRatio` 都包含 | 见 2.2.2 |
| 若设置了 `tokens.model_limits_enabled=true`：`model_limits` 非空 | 见 2.2.2 |
| 若设置了 `tokens.allow_ips`：客户出口 IP 在列表内 |  |
| `/channels` 至少有一条渠道支持 `(usingGroup, 请求模型)` 组合 | `abilities` 表存在对应行 |

跑一次 `curl` 自测：

```bash
curl https://your-newapi-host/v1/chat/completions \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "ping"}]
  }'
```

返回 200 + 模型回答 = 客户可调用，进入第 3 章「计费规则」。

---

## 参考代码索引

- `model/user.go:24-56` — users 表结构
- `model/token.go:14-32` — tokens 表结构
- `controller/user.go` — 用户 CRUD
- `controller/topup.go` — 充值入口
- `controller/redemption.go` — 兑换码
- `middleware/auth.go:280-407, 380-398` — token 鉴权 + 分组校验
- `middleware/distributor.go:57-75` — 模型白名单校验
- `router/api-router.go:64-180, 261-273` — user / token API 注册
- `web/default/src/routes/_authenticated/keys/index.tsx` — 令牌前端页
- `web/default/src/routes/console/topup.tsx` — 充值前端页
