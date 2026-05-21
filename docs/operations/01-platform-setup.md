# 第 1 章 · 平台搭建上线

> 适用对象：第一次接手 NewAPI 运营的同学。
> 目标：跑完本章后，平台具备「客户拿到令牌即可发起调用」的最小可用状态。
>
> **执行顺序固定**：供应商 → 模型元数据 → 价格/倍率 → 分组 → 渠道。倒序会导致渠道挂载时找不到分组或模型，必须返工。
>
> 文中「`文件:行号`」均指仓库 `https://github.com/yujipeng/new-api` 基线 `2d1ca153` 的代码位置。

---

## 1.0 关键概念速览（动手前先看一眼）

| 概念 | 一句话定义 | 落库位置 |
|---|---|---|
| 供应商（Vendor） | 模型背后的服务提供商，决定 `/pricing` 页面的图标和归类 | `vendors` 表 |
| 模型（Model） | 模型元数据（描述、图标、端点），不含价格 | `models` 表 |
| 渠道（Channel） | 对接上游 API 的通道，承载 key、base_url、可服务模型与分组 | `channels` 表（派生 `abilities` 表） |
| 用户分组（User Group） | 客户身份所属分组，写在 `users.group` | `users.group` |
| 令牌分组（Token Group） | 令牌可临时切换到的分组，写在 `tokens.group` | `tokens.group` |
| 渠道分组（Channel Group） | 渠道挂在哪些分组下（**逗号分隔多值**） | `channels.group` |
| 分组倍率（GroupRatio） | 每个分组的全局计费倍率 | `options` 表 key=`GroupRatio` |
| 模型倍率（ModelRatio） | 每个模型的计费倍率 | `options` 表 key=`ModelRatio` |

> ⚠️ **三个 group 不是同一个东西**。详见 1.4 节。

派生关系（来自 `model/ability.go:146-185 AddAbilities` / `:193-261 UpdateAbilities`）：

```
新增/更新 channel
   └─ 把 (channel.group 拆开 × channel.models 拆开) 笛卡尔展开
        └─ 同步写入 abilities 表：联合主键 (group, model, channel_id)
```

`abilities` 表是路由器查「这个 group 下能服务这个 model 的渠道有哪些」的源头。**不要直接改 `abilities` 表**，所有变更都走 channel 表，由 NewAPI 内部维护一致性。

---

## 1.1 步骤一：定义供应商与模型元数据

> **可选但推荐**：影响 `/pricing` 公开价格页的展示，也是模型管理统一界面的入口。

### 1.1.1 添加供应商（Vendor）

`<截图：系统设置 - 模型管理 - 供应商列表>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/system-settings/models/$section`（section = `vendors`） |
| 前端代码 | `web/default/src/features/system-settings/models/` |
| 后端 API | `GET/POST/PUT /api/vendors`（`router/api-router.go:340-363`） |
| 数据库表 | `vendors`（`model/vendor_meta.go:15-24`） |

`vendors` 表字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `name` | string (unique) | 供应商名（唯一） |
| `description` | string | 描述 |
| `icon` | string | 图标 URL |
| `status` | int | 启停 |
| `created_time` / `updated_time` | int64 | 时间戳 |
| `deleted_at` | gorm soft delete | 软删 |

### 1.1.2 添加模型（Model 元数据）

`<截图：系统设置 - 模型管理 - 模型列表 - 编辑模型>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/system-settings/models/$section`（section = `models`） |
| 后端 API | `GET/POST/PUT /api/models` |
| 数据库表 | `models`（`model/model_meta.go:23-44`） |

`models` 表字段：

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `model_name` | string (unique) | 模型名（如 `gpt-4o`） |
| `description` | string | 描述 |
| `icon` | string | 图标 |
| `tags` | string | 标签 |
| `vendor_id` | int | 关联 `vendors.id` |
| `endpoints` | text/JSON | 该模型支持的端点列表 |
| `status` | int | 启停 |
| `sync_official` | bool | 是否同步官方信息 |
| `name_rule` | int | 匹配规则（**关键，见下文**） |

`name_rule` 取值（`model/model_meta.go:11-16`）：

| 值 | 规则 | 示例 |
|---|---|---|
| `0` | exact（精确匹配） | `gpt-4o` 只匹配 `gpt-4o` |
| `1` | prefix（前缀） | `gpt-4-` 匹配 `gpt-4-0125`、`gpt-4-vision` 等 |
| `2` | contains（包含） | `vision` 匹配所有含 `vision` 的模型名 |
| `3` | suffix（后缀） | `-preview` 匹配所有以 `-preview` 结尾的模型 |

> 💡 选 `prefix/contains/suffix` 可以一次匹配多个版本号变体，但**仅作用于元数据展示与图标归类**，价格仍按 `ModelRatio` 中实际命中的 key 来算。

**注意事项**：
- 上线一个新模型时，本步只是给它建「身份证」，**还没赋予它价格**。价格在 1.2 节配。
- 不在此处建模型也不会阻断调用——只是 `/pricing` 页与前端图标无法显示。

---

## 1.2 步骤二：定义模型价格 / 倍率

> 这是真正决定**扣多少钱**的步骤。两套并行机制：「按 token 倍率计费」（`UsePrice=false`，默认）与「按次价计费」（`UsePrice=true`）。

`<截图：系统设置 - 计费设置 - 模型定价>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/system-settings/billing/model-pricing` |
| 前端代码 | `web/default/src/features/system-settings/billing/section-registry.tsx:106-119`（`RatioSettingsCard`） |
| 后端 API | `PUT /api/option/`（统一 option 接口，`controller/option.go`） |
| 数据库表 | `options`（key=ratio 名，value=JSON） |
| 内存结构 | `types.RWMap[string, float64]`，启动时由 `setting/ratio_setting/model_ratio.go:343-352 InitRatioSettings` 灌入 |

### 1.2.1 倍率类型一览

option key 列表（来自 `model/option.go:150-153, 508-509`）：

| key | 含义 | 触发分支 |
|---|---|---|
| `ModelRatio` | 模型按 token 倍率（`UsePrice=false` 路径） | 标准计费 |
| `ModelPrice` | 模型按次价（`UsePrice=true` 路径） | 按次扣 `price × QuotaPerUnit × GroupRatio` |
| `CompletionRatio` | 输出 token 相对输入的倍率 | 标准计费 |
| `CacheRatio` | 缓存命中部分 token 的倍率 | 含缓存的请求 |
| `CreateCacheRatio` | 创建缓存的 token 倍率 | 写缓存场景 |
| `ImageRatio` | 图像 token 倍率 | 图像输入 |
| `AudioRatio` | 音频 token 倍率 | 音频输入 |
| `AudioCompletionRatio` | 音频输出 token 倍率 | 音频输出 |

### 1.2.2 默认值与重置

- 后端预置约 700+ 个常见模型的默认倍率（写死在 `setting/ratio_setting/model_ratio.go:26 defaultModelRatio` 等 map 里）。
- 重置接口：`controller/pricing.go:79 ResetModelRatio`，前端「恢复默认」按钮调用。

### 1.2.3 量纲（必看）

> 🔴 **`1 USD = QuotaPerUnit = 500000 quota`**（`common/constants.go:62`）。
> 注释原话：`$0.002 / 1K tokens = 1 quota`。
> 汇率：`USD2RMB = 7.3`（`setting/ratio_setting/model_ratio.go:13`）。

前端展示量纲在 `/system-settings/billing/currency` 配置：
- `QuotaPerUnit` — 1 美元等于多少 quota（默认 500000）
- `USDExchangeRate` — 美元汇率（默认 7.3）
- `DisplayInCurrencyEnabled` — 是否按货币显示
- `general_setting.quota_display_type` — 显示口径

### 1.2.4 分级（tiered）计费

> 当一个模型既要按用量阶梯扣，又要支持自定义表达式时使用。

- 设计文档：仓库内 `pkg/billingexpr/expr.md`（**配置前必读**）。
- 配置位置：`setting/billing_setting`。
- 触发条件：模型挂 `BillingMode='tiered_expr'` + `BillingExpr=<表达式>`。
- 运行时入口：`service/tiered_settle.go:95 TryTieredSettle`，在标准计费完成后调用，命中则**覆盖** `summary.Quota`（`service/text_quota.go:340-346`、`service/quota.go:202-205`）。
- 一次请求内倍率不变：`relayInfo.TieredBillingSnapshot.ExprString` 在请求前置阶段冻结。

⚠️ 未注册到 ModelRatio 的模型扣费行为存在不确定性，详见末尾追踪表 [#1](99-pending-items.md#1-未注册模型默认倍率)。

---

## 1.3 步骤三：定义分组（**重点章节**）

NewAPI 共有 **3 层 group 概念**，全部以 JSON 形式存于 `options` 表，**没有独立 `groups` 表**。

### 1.3.1 三种 group 的角色对比

| 概念 | 谁在用 | 存哪里 | 默认值 |
|---|---|---|---|
| **User Group**（用户分组） | 客户身份，决定可见分组与默认计费倍率 | `users.group`（varchar(64)，`model/user.go:43`） | `default` |
| **Token Group**（令牌分组） | 客户在不同令牌上**临时切换**分组（如客户买了 vip 但临时想用 svip） | `tokens.group`（`model/token.go:29`） | `''`（空 = 沿用 user 分组） |
| **Channel Group**（渠道分组 / Ability Group） | 渠道挂在哪些分组下 | `channels.group`（**逗号分隔多个**，`model/channel.go:40`），并派生进 `abilities.group` | `default` |

**运行时解析**（`middleware/auth.go:382-399`）：

```
默认 usingGroup = users.group
若 tokens.group != '' 且 tokens.group ∈ UserUsableGroups[users.group]：
    usingGroup ← tokens.group
最终 usingGroup 写入请求上下文 ContextKeyUsingGroup
```

### 1.3.2 与分组相关的所有 option key

| option key | 类型 | 含义 | 文件 |
|---|---|---|---|
| `GroupRatio` | `{groupName: ratio}` | 每个分组的全局计费倍率 | `setting/ratio_setting/group_ratio.go:18` |
| `TopupGroupRatio` | `{groupName: ratio}` | 充值时的分组倍率（影响到账金额折算） | `setting/operation_setting/` |
| `GroupGroupRatio` | `{userGroup: {usingGroup: ratio}}` | 用户分组 × 临时切换到的分组的特殊倍率矩阵 | `setting/ratio_setting/group_ratio.go:26, 93-103` |
| `UserUsableGroups` | `{groupName: 描述}` | 用户/令牌可选分组的白名单 + 描述文案 | `setting/user_usable_group.go:10` |
| `group_ratio_setting.group_special_usable_group` | `{userGroup: {"+:name"/"-:name"/"name": desc}}` | 按用户分组追加 / 移除 / 重命名可见分组 | `setting/ratio_setting/group_ratio.go:28, 38` |
| `AutoGroups` | `[group, ...]` | `tokens.group="auto"` 时自动跨组重试的分组列表 | `setting/auto_group.go` |

### 1.3.3 分组倍率取数逻辑

`service/group.go:59 GetUserGroupRatio(userGroup, group)`：

```
先查 GroupGroupRatio[userGroup][group]
   └─ 没有则 fallback GroupRatio[group]
   └─ 还是没有则返回 1.0（且打 SysLog 警告）
```

⚠️ **GroupRatio 缺省返回 1**：未注册的 group 被消费时**不报错也不阻断**，会按原价计。详见末尾追踪表 [#3](99-pending-items.md#3-未注册-groupratio-默默按-1-倍计费)。

### 1.3.4 前端入口

`<截图：系统设置 - 计费设置 - 分组定价 - 倍率矩阵编辑>`

| 入口 | 路径 | 用途 |
|---|---|---|
| 分组定价 | `/system-settings/billing/group-pricing` | 编辑 GroupRatio / GroupGroupRatio / UserUsableGroups |
| 倍率矩阵 | `/system-settings/models/group-ratio-form.tsx`（`section-registry.tsx:120-134`） | 可视化矩阵编辑 GroupGroupRatio |

| API | 用途 |
|---|---|
| `GET /api/group/`（admin） | 列所有分组名（`controller/group.go:14`） |
| `GET /api/user/groups` 或 `/api/user/self/groups` | 用户视角可见分组 + 倍率（`controller/group.go:26`） |
| `PUT /api/option/` | 保存上述任意 option key |

### 1.3.5 操作准则

> 🔴 **更新 `GroupRatio` 时必须同步更新 `UserUsableGroups`**。
> `middleware/auth.go:391-396` 要求 `tokens.group` 同时在两套配置里出现，否则用户报 403（除 `auto` 分组外）。
> 详见末尾追踪表 [#5](99-pending-items.md#5-groupratio-与-userusablegroups-必须双写)。

> ⚠️ **默认 SVIP 分组无法直接使用**：后端默认在 `GroupRatio` 注册了 `default/vip/svip`（值都为 1），但 `UserUsableGroups` 默认只放 `default/vip`（`setting/user_usable_group.go:10-13`）。也就是说**SVIP 默认不可被令牌切换到**，需运营手动加入 `UserUsableGroups`。详见末尾追踪表 [#6](99-pending-items.md#6-svip-默认半启用)。

---

## 1.4 步骤四：添加渠道（Channel）

> 现在终于可以把上游 API key 接进来了。

`<截图：渠道管理 - 添加渠道 - 表单>`

| 维度 | 内容 |
|---|---|
| 前端路径 | `/channels`（**要求 admin 角色**，`web/default/src/routes/_authenticated/channels/index.tsx:35`） |
| 前端代码 | `web/default/src/features/channels/index.tsx` |
| 数据库表 | `channels`（`model/channel.go:23-60`），派生表 `abilities`（`model/ability.go:16-24`） |

### 1.4.1 后端 API 一览

| API | 用途 | 文件 |
|---|---|---|
| `GET /api/channel/` | 列出渠道 | `router/api-router.go:218-260` |
| `POST /api/channel/` | 新增 |  |
| `PUT /api/channel/` | 更新 |  |
| `DELETE /api/channel/:id` | 删除 |  |
| `POST /api/channel/batch` | 批量操作 |  |
| `GET /api/channel/test/:id` | 测试连通性 | `controller/channel-test.go` |
| `POST /api/channel/fix` | 重建 abilities 表 | 对应 `model/ability.go:287 FixAbility` |
| `POST /api/channel/multi_key/manage` | 多 key 模式管理 |  |
| `POST /api/channel/copy/:id` | 复制渠道 |  |
| `POST /api/channel/batch/tag` | 批量打 tag |  |
| `POST /api/channel/tag/disabled`、`/tag/enabled` | 按 tag 启停 |  |

### 1.4.2 `channels` 表关键字段

| 字段 | 类型 | 含义 |
|---|---|---|
| `id` | int | 主键 |
| `type` | int | 渠道供应商枚举（`constant.ChannelType*`） |
| `key` | string | 上游 API key（**支持多 key 模式**） |
| `name` | string | 渠道展示名 |
| `status` | int | 1=启用 |
| `weight` | int | 加权随机时的权重 |
| `models` | string | 逗号分隔的支持模型列表 |
| `group` | string | 逗号分隔的支持分组列表 |
| `priority` | int | 优先级（高优先先选） |
| `base_url` | string | 上游 API 基础 URL |
| `other` | string | Azure 版本 / Gemini api_version 等 |
| `model_mapping` | text(JSON) | 模型名映射（如客户请求 `gpt-4o-2024` 映射成上游 `gpt-4o`） |
| `status_code_mapping` | text | 上游错误码到 NewAPI 错误码的映射 |
| `param_override` | text | 请求参数覆盖 |
| `header_override` | text | 请求 header 覆盖 |
| `channel_info` | text(JSON) | 多 key 模式下各 key 的状态 |
| `setting` / `settings` | text | 渠道级配置（保留两套字段） |
| `used_quota` | int | 累计消耗 quota（统计用） |
| `balance` | float | 上游账户余额（拉取式，**不参与 NewAPI 内部扣费**） |
| `balance_updated_time` | int64 | 余额最近更新时间 |
| `response_time` | int | 上次测试响应时长 |
| `test_time` | int64 | 上次测试时间 |
| `tag` | string | 标签（用于批量操作） |
| `remark` | string | 备注 |
| `openai_organization` | string | OpenAI 组织 ID（仅 OpenAI 类） |
| `test_model` | string | 测试用模型 |
| `auto_ban` | int | 上游错误时是否自动封禁 |
| `created_time` | int64 |  |

### 1.4.3 `abilities` 派生表（不直接改！）

| 字段 | 类型 | 说明 |
|---|---|---|
| `group` | string | 联合主键之一 |
| `model` | string | 联合主键之一 |
| `channel_id` | int | 联合主键之一 |
| `enabled` | bool | 渠道是否启用（与 `channels.status` 联动，`UpdateAbilityStatus` 在 `model/channel.go:263`） |
| `priority` | int | 同 `channels.priority` |
| `weight` | int | 同 `channels.weight` |
| `tag` | string |  |

`FixAbility`（`model/ability.go:287-341`）= **truncate `abilities` 表 + 按所有 channel 重建**。前端「修复」按钮调用，路径 `POST /api/channel/fix`。

> ⚠️ 高并发下 `FixAbility` 与渠道写入并发，可能出现短暂不一致。详见末尾追踪表 [#4](99-pending-items.md#4-fixability-高并发不一致)。

### 1.4.4 完整调用链速记

> 客户端请求是怎么落到具体渠道并扣费的（运营排错时的心智模型）：

```
1. 客户端 Authorization: Bearer sk-xxx
   └─ middleware.TokenAuth (middleware/auth.go:280-407)
        ├─ 解 token，校验 IP / group / model_limits
        └─ 写 ctx：ContextKeyUsingGroup, token_id, user_id, ...

2. middleware.Distribute (middleware/distributor.go:30-165)
   └─ 取请求 model
        └─ service.CacheGetRandomSatisfiedChannel
             └─ 在 abilities 表按 (group + model) 找 priority 最高的一组
                  └─ 在该组内按 weight 加权随机选一个 channel
                       └─ 写 ctx：通道信息

3. relay/* 各 adapter
   ├─ 转发到上游 API
   ├─ 计算计费（详见第 3 章）
   └─ 写日志（详见第 4 章）
```

---

## 1.5 验收：平台是否可用？

跑完前 4 步后，做一次「冒烟自查」：

| 检查项 | 通过标准 |
|---|---|
| `/system-settings/models/vendors` 至少有一条供应商 | 表 `vendors` 行数 > 0 |
| `/system-settings/billing/model-pricing` 中目标模型有非默认（或显式默认）倍率 | `options` 表 `key='ModelRatio'` 含目标模型 |
| `/system-settings/billing/group-pricing` 中目标分组在 `GroupRatio` 与 `UserUsableGroups` 都存在 | 见 1.3.5 准则 |
| `/channels` 列表中渠道 `status=1` 且 `models` / `group` 都包含目标值 | 表 `channels.status=1` |
| `abilities` 表能查到 `(group, model, channel_id)` 行 | 见 1.4.3 |
| 渠道测试按钮（`/api/channel/test/:id`）返回成功 | response_time 写入 `channels` |

冒烟通过 = 平台可对外提供调用，进入第 2 章「客户接入」。

---

## 参考代码索引（按出现顺序）

- `model/ability.go:16-24, 146-185, 193-261, 287-341` — abilities 表与派生逻辑
- `model/vendor_meta.go:15-24` — vendors 表
- `model/model_meta.go:11-16, 23-44` — models 表与 name_rule
- `model/channel.go:23-60, 263, 824` — channels 表与状态联动
- `model/option.go:150-153, 508-509` — option key 注册
- `model/user.go:24-56` — users 表
- `model/token.go:14-32` — tokens 表
- `setting/ratio_setting/model_ratio.go:13, 26, 343-352, 403-417` — 默认倍率与 InitRatioSettings
- `setting/ratio_setting/group_ratio.go:12-16, 18, 26, 28, 38, 84-103` — 分组倍率
- `setting/user_usable_group.go:10-13` — 用户可用分组
- `controller/option.go` — option PUT 入口
- `controller/group.go:14, 26` — group 列表 API
- `controller/pricing.go:79` — 重置默认倍率
- `middleware/auth.go:280-407, 382-399, 391-396` — token 鉴权与 group 解析
- `middleware/distributor.go:30-165, 57-75` — 渠道分发与 model_limits 校验
- `service/group.go:59` — 分组倍率取数
- `router/api-router.go:218-260, 340-363` — channel / vendor / model API 注册
- `web/default/src/routes/_authenticated/channels/index.tsx:35` — 渠道页面路由
- `web/default/src/features/system-settings/billing/section-registry.tsx:43, 82-104, 106-119, 120-134` — 计费设置面板
- `web/default/src/features/system-settings/models/` — 模型管理面板
- `pkg/billingexpr/expr.md` — 分级计费表达式设计文档
- `common/constants.go:62` — `QuotaPerUnit = 500000`
