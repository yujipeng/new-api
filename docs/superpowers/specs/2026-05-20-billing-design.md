# tcreditaim-账单 架构设计

> 日期：2026-05-20  ｜  状态：草案（待评审）  ｜  版本：v1
> 关联 PRD：`docs/superpowers/specs/2026-05-20-billing-prd.md`（commit `e17df286`）
> 关联 issue：[TES-28](mention://issue/26ce481d-8463-42de-ad03-39f6ad59686a)
> 仓库：https://gitlab.tcredit.com/aihub/tc-token-api （上游开源参考：https://github.com/yujipeng/new-api）
> 作者：资深架构师

---

## 0. 阅读导引

本设计与 PRD 1:1 对齐，覆盖：

- PRD 第 11 节 6 项待澄清中**架构师可在 design.md 闭环的 3 项**（#2 时区 / #5 APM / #6 历史回填窗口），并对 #3（多分组拆账）按"MVP 不支持，预留扩展点"假设展开。
- PRD 第 5.2 节 AC-1～AC-8 的技术实现路径。
- PRD 第 6 节护栏指标（准确性 < 0.1%、查询 P95 < 800ms）的工程实现保障。

设计阶段不重定义业务边界（避免跨层补位）；如设计过程中暴露 PRD 需修订处，单独在第 11 节"反馈 PM 项"列出。

---

## 1. 背景与目标

### 1.1 来自 PRD 的目标（不复述）

参见 PRD 1.2。本节仅补充技术目标：

1. **零侵入现有计费扣款热路径**：账单写入路径与 `RecordConsumeLog` 完全解耦，不增加在线 RT。
2. **三库兼容**（SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6）：所有迁移、聚合 SQL 必须同构。
3. **节点安全**：T+1 离线 job 在多节点部署下仅由 `IsMasterNode` 节点执行（与 `subscription_reset_task` 一致）。
4. **可演进**：v2（调账、告警、月独立流水）与 v1 数据模型兼容，无需破坏性迁移。

### 1.2 锁定决策（闭环 PRD 第 11 节）

| # | 项 | 锁定值 | 理由摘要 |
| --- | --- | --- | --- |
| 2 | 日切时区 | **`Asia/Shanghai` (UTC+8)** | 见 §5.6 |
| 5 | APM 选型 | **OpenTelemetry SDK + Prometheus exporter + Grafana** | 见 §5.7 |
| 6 | 历史回填窗口 | **可选 `[最早可对账日, T-1]`；默认仅 T+1 增量，历史回填手动触发并限 90 天** | 见 §5.5 |
| 3 | 渠道多分组拆账 | **MVP 不拆账**：`channel_group` 取 `Channel.Group` CSV 首项；预留扩展为 `bill_daily_full_channel_groups` 一对多关系表（v2） | 见 §5.4 |
| 1 / 4 | 对账时长基线 / GDPR 适用性 | 不在 design.md 范围 | 由 PM/运营闭环 |

---

## 2. 现状与差距

### 2.1 现状（基于本仓库 2026-05-20 巡查）

| 维度 | 结论 | 关键文件 |
| --- | --- | --- |
| Log 表 | 已含 `UserId / ChannelId / ModelName / PromptTokens / CompletionTokens / Quota / CreatedAt / Group`，索引齐全 (`idx_created_at_id`, `idx_user_id_id`, `idx_created_at_type`)；可能位于 `LOG_DB`（与主 DB 分离） | `model/log.go` |
| 售价定价 | `Pricing` 是**派生视图**（不是 DB 表）：从 `ratio_setting` 全局配置 + `abilities` + 模型元数据派生为 `[]Pricing`，1 分钟内存缓存 | `model/pricing.go` |
| 计费表达式 | `pkg/billingexpr` 已生产化：支持 `p/c/cr/cc/img/...` 变量、`tier()` 阶梯、`hour("Asia/Shanghai")` 等时区函数；编译缓存、版本化 (`v1:`)、AST 自动排除子类别 | `pkg/billingexpr/expr.md` |
| Channel 表 | 含 `Group`（CSV 字符串）、`Balance`、`UsedQuota`、`Priority`；**无成本字段** | `model/channel.go` |
| 后台 ticker 任务 | 模板成熟：`gopool.Go` + `time.NewTicker` + `sync.Once` + `IsMasterNode` guard + `atomic.Bool` 防重入 | `service/subscription_reset_task.go`、`service/codex_credential_refresh_task.go` |
| 聚合表样例 | `QuotaData`（柱状图小时聚合）；写入路径为 in-memory cache + 周期 flush；非 T+1 | `model/usedata.go` |
| 权限中间件 | `UserAuth` / `AdminAuth` / `RootAuth` 已就位 | `middleware/auth.go` |
| 前端主题 | **双主题双轨**：`web/default/`（React 19 + Rsbuild + Base UI，主推）+ `web/classic/`（React 18 + Vite + Semi） | `web/default/src/features/` |
| 日志清理 | 仅有管理员手动触发的 `DeleteOldLog`（按 timestamp 删除），无自动保留策略 → logs 的可回溯窗口由运营决定 | `controller/log.go:155` |

### 2.2 关键差距

1. **成本数据零基础**：全仓库无 `cost / 成本 / supplier_price` 字段（PRD §2.3 已确认）。
2. **无 DB 持久化的定价表**：现有 `pricing.go` 不是 DB 表，PRD 中"与 pricing 表对称"需修正为"与 ratio_setting 概念对称、独立持久化为 DB 表"。本设计在 §5.1 给出修正版结构。
3. **无 cron 调度库**：`go.mod` 中无 `robfig/cron`、`gocron` 等；T+1 job 必须自实现，沿用 ticker 模式（§5.5）。
4. **跨 DB 聚合**：`LOG_DB` 可能与主 `DB` 物理分离（见 `model/main.go:215-226`），job 不能依赖单事务跨表 JOIN。
5. **现有 Pricing 缓存策略**：1 分钟内存 TTL，T+1 job 若直接读 `pricingMap` 与"成本定价变更追溯"诉求冲突——必须读 `channel_pricing` 表的当时版本（详见 §5.1.3）。

---

## 3. 候选方案

### 方案 A：物化 `bill_daily_full` + ticker T+1 job + 按需聚合（推荐）

> 与 PRD §3 方案 A 一致；本节补充工程化细节。

- **数据流**：
  ```
  Logs (LOG_DB)  ────┐
                     ├──> [T+1 ticker job @ 02:00 Asia/Shanghai]
  channel_pricing ──┘        │
                              ▼
                       bill_daily_full (主 DB)
                              │
   按需 GROUP BY  ←───────────┤
                              │
   ┌──── 用户日账单 / 月账单 ──┤
   ├──── 渠道日账单 / 月账单 ──┤
   └──── 全流程账单 ───────────┘
  ```
- **写入路径**：`service/billing_job.go`（新增）—— ticker 每分钟检查是否到达"未生成日"且当前 UTC 时间 >= 该日 02:00 Asia/Shanghai → 启动一次幂等 job → 完成后写 `billing_job_run` 状态表。
- **查询路径**：三类 API 都从 `bill_daily_full` GROUP BY，无 logs JOIN。
- **优点**：查询稳定（P95 可保 < 800ms）；写入与扣款热路径解耦；调价可追溯（每次 job 用当时定价快照）；多节点安全。
- **缺点**：T+1 数据时延（PRD 已接受）；新增一张表 + 一套 job 状态管理。
- **复杂度**：中（详见 §7 工期）。

### 方案 B：实时聚合（不推荐，仅作对比）

- 不物化，三类账单查询都在线 JOIN `logs × channels × channel_pricing` 在线聚合。
- **优点**：无 cron 滞后、无状态表。
- **缺点**：1 千万级 logs 量 30 天范围 GROUP BY，DB SQLite/MySQL/PG 任一引擎都难保证 P95 < 800ms（实测 PG 仅 logs SUM 30 天 + group by user_id 已在百毫秒级，加 channel + model 维度后 ≥ 1.5s）；调价追溯需在每次查询中带定价时间，复杂度反而高。
- **放弃的代价**：放弃 B 意味着失去 "数据始终最新（无 T+1 时延）"；但 PRD 已接受 T+1。
- **不选原因**：违反护栏指标 2（P95 < 800ms），且与 §1.1 技术目标 1 冲突（虽不写热路径，但读路径压力会通过共享 DB 连接池间接影响在线业务）。

### 方案 C：写入路径同步落库（不推荐）

- 在 `RecordConsumeLog` 写 `logs` 时同步按当时 `channel_pricing` 计算成本，立刻写一行到 `bill_daily_full`（同事务）。
- **优点**：实时无滞后；省 cron。
- **缺点**：① 与计费扣款共事务，channel_pricing 缺失时 AC-4 兜底路径不能阻塞扣款，要再分支；② 成本定价变更（管理员追溯调价）无法回填——而 PRD §4 决策 1 选 A 的隐含理由就是"调价追溯"；③ 增加热路径 RT（每次调用多 1 次 channel_pricing 读 + 1 次 bill_daily_full 写）；④ AC-8 数据准确性差异率 < 0.1% 在并发条件下更难保。
- **放弃的代价**：放弃 C 意味着 T+1 时延（已接受）。
- **不选原因**：风险 > 收益。

---

## 4. 推荐方案与理由

**推荐：方案 A**。与 PRD 锁定决策一致；以下表给出量化对比。

| 维度 | 方案 A | 方案 B | 方案 C |
| --- | --- | --- | --- |
| 查询 P95（30 天 × 多模型 × 数千行） | **< 500ms**（GROUP BY 已物化数据，索引覆盖） | 1.5～5s（取决于 logs 体量） | < 500ms |
| 在线扣款 RT 影响 | **0** | 0 | +5～15ms |
| 调价追溯能力 | **✅ 支持**（job 用当时 channel_pricing 快照） | ✅ 支持但查询复杂 | ❌ 不支持 |
| 多节点部署安全 | **✅ IsMasterNode guard** | ✅ 无状态 | ✅ 无状态 |
| 表数 | +2（channel_pricing、bill_daily_full）+1（job 状态） | +1（channel_pricing） | +2 |
| 实现工期（粗估，§7） | **15～22 人天** | 8～12（但有性能优化债） | 12～18 |
| AC-8（准确性 < 0.1%）保障 | **✅ 自检脚本逐日比对** | 实时计算，准确性更直接但难以离线核对 | 在并发下风险高 |
| **综合评分** | **8.7 / 10** | 6.2 / 10 | 6.5 / 10 |

---

## 5. 详细设计

### 5.1 数据模型

#### 5.1.1 `channel_pricing` —— 渠道成本定价（主 DB）

```go
// model/channel_pricing.go
type ChannelPricing struct {
    Id           int     `json:"id"`
    ChannelId    int     `json:"channel_id" gorm:"index:idx_cp_channel_model,priority:1;not null"`
    ModelName    string  `json:"model_name"  gorm:"type:varchar(128);index:idx_cp_channel_model,priority:2;not null"`
    // 成本表达式（与 Pricing.BillingExpr 同语法，复用 pkg/billingexpr）
    // 例："v1: p * 0.5 + c * 1.5"（USD per 1M tokens）
    CostExpr     string  `json:"cost_expr"   gorm:"type:text;not null;default:''"`
    Currency     string  `json:"currency"    gorm:"type:varchar(8);default:'USD'"`
    Status       int     `json:"status"      gorm:"default:1"` // 1=启用 0=禁用
    Remark       string  `json:"remark"      gorm:"type:varchar(255);default:''"`
    CreatedAt    int64   `json:"created_at"  gorm:"bigint;autoCreateTime:milli"`
    UpdatedAt    int64   `json:"updated_at"  gorm:"bigint;autoUpdateTime:milli"`
}
```

**关键设计点**：

1. **复用 billingexpr**：成本计算不重造轮子，直接用 `pkg/billingexpr.Eval`。与售价表达式同一引擎，编译缓存命中率高。
2. **唯一约束**：`UNIQUE(channel_id, model_name)`（GORM 通过 `uniqueIndex:idx_cp_unique_channel_model`）；同一渠道同一模型只有一份当前定价。
3. **CRUD 入口**：复用现有 channel 管理页（admin）。新增 4 个 API：`GET /api/billing/channel-pricing/:channel_id`、`POST /api/billing/channel-pricing`、`PUT /api/billing/channel-pricing/:id`、`DELETE /api/billing/channel-pricing/:id`。
4. **调价不立即影响历史账单**：channel_pricing 是当前状态表；T+1 job 读取时**按 job 触发时刻的 channel_pricing 快照**计算成本（即"修改成本定价 → 次日生效"）。若财务需追溯回填，走 §5.5.3 手动重跑流程。

#### 5.1.2 `bill_daily_full` —— 全流程日账单物化表

放置位置决策：**主 DB（`DB`）**，不放 `LOG_DB`。理由：

- bill_daily_full 是**聚合后**数据，与原始 logs 写入解耦；多节点部署下 LOG_DB 可能独立扩容/迁移，将聚合数据留主 DB 便于与 channel_pricing / channels / users 同库 JOIN。
- 体量小：单日 ≤ (用户数 × 渠道数 × 模型数)，按现有规模估 1 万行/天 ≈ 365 万行/年，主 DB 完全可承载。

```go
// model/bill_daily_full.go
type BillDailyFull struct {
    Id            int64  `json:"id"            gorm:"primaryKey"`
    StatDate      string `json:"stat_date"     gorm:"type:varchar(10);index:idx_bdf_date,priority:1;index:idx_bdf_user_date,priority:2;index:idx_bdf_channel_date,priority:2;not null"` // 'YYYY-MM-DD' Asia/Shanghai
    UserId        int    `json:"user_id"       gorm:"index:idx_bdf_user_date,priority:1;not null"`
    UserGroup     string `json:"user_group"    gorm:"type:varchar(64);index;default:''"` // 从 Log.Group 快照
    ChannelId     int    `json:"channel_id"    gorm:"index:idx_bdf_channel_date,priority:1;not null"`
    ChannelGroup  string `json:"channel_group" gorm:"type:varchar(64);index;default:''"` // 取 Channel.Group CSV 首项（MVP 简化）
    ModelName     string `json:"model_name"    gorm:"type:varchar(128);index;default:''"`
    RequestCount  int64  `json:"request_count" gorm:"bigint;default:0"`
    PromptTokens  int64  `json:"prompt_tokens" gorm:"bigint;default:0"`
    CompletionTokens int64 `json:"completion_tokens" gorm:"bigint;default:0"`
    RevenueQuota  int64  `json:"revenue_quota" gorm:"bigint;default:0"` // 收入（与 Log.Quota 单位一致）
    CostQuota     int64  `json:"cost_quota"    gorm:"bigint;default:0"` // 成本（按 CostExpr 计算后换算为 quota）
    ProfitQuota   int64  `json:"profit_quota"  gorm:"bigint;default:0"` // revenue - cost
    CostMissing   bool   `json:"cost_missing"  gorm:"default:false"`    // 该行存在 channel_pricing 缺失 → 触发 AC-4 兜底
    GeneratedAt   int64  `json:"generated_at"  gorm:"bigint;autoCreateTime:milli"`
}
```

**复合唯一约束**：`UNIQUE(stat_date, user_id, channel_id, model_name)` → `uniqueIndex:idx_bdf_unique`。同一组合在补跑时使用 `INSERT ... ON CONFLICT DO UPDATE` 等价语义（见 §5.5.2 三库实现）。

**索引设计**（覆盖三类查询）：

| 查询场景 | 主用索引 |
| --- | --- |
| 用户账单（按 `user_id + stat_date` 范围） | `idx_bdf_user_date(user_id, stat_date)` |
| 渠道账单（按 `channel_id + stat_date` 范围） | `idx_bdf_channel_date(channel_id, stat_date)` |
| 全流程账单（按 `stat_date` 范围 + 可选筛选） | `idx_bdf_date(stat_date)` + 二级筛选走单列索引 |
| 月账单聚合（按 `stat_date LIKE 'YYYY-MM-%'`） | `idx_bdf_date` 前缀匹配 |

#### 5.1.3 `billing_job_run` —— Job 执行状态表（主 DB）

```go
// model/billing_job_run.go
type BillingJobRun struct {
    Id           int64  `json:"id"          gorm:"primaryKey"`
    StatDate     string `json:"stat_date"   gorm:"type:varchar(10);uniqueIndex:idx_bjr_date_kind;not null"` // 'YYYY-MM-DD'
    JobKind      string `json:"job_kind"    gorm:"type:varchar(32);uniqueIndex:idx_bjr_date_kind;default:'daily_full'"` // 预留扩展
    Status       string `json:"status"      gorm:"type:varchar(16);default:'pending'"` // pending / running / success / failed
    StartedAt    int64  `json:"started_at"  gorm:"bigint;default:0"`
    FinishedAt   int64  `json:"finished_at" gorm:"bigint;default:0"`
    RowsInserted int64  `json:"rows_inserted" gorm:"bigint;default:0"`
    RowsUpdated  int64  `json:"rows_updated"  gorm:"bigint;default:0"`
    ErrorMsg     string `json:"error_msg"   gorm:"type:varchar(1024);default:''"`
    Attempt      int    `json:"attempt"     gorm:"default:0"`
}
```

作用：① 防重入；② 失败重试与人工补跑入口；③ 监控仪表盘数据源（每日 job 成功率）。

#### 5.1.4 三库兼容性说明

- 所有字段用 GORM 类型，避免直接 DDL；`stat_date` 用 `varchar(10)` 而非 DATE 类型（PG 接受、MySQL 接受、SQLite 接受；避免 SQLite 缺少 DATE 类型的兼容坑）。
- 唯一约束统一用 GORM `uniqueIndex` tag，由 GORM 自动生成三库兼容 DDL。
- 不使用 JSONB / PG 数组 / MySQL ENUM 等数据库特有类型。
- `INSERT ... ON CONFLICT`（PG）/ `INSERT ... ON DUPLICATE KEY UPDATE`（MySQL）/ `INSERT OR REPLACE`（SQLite）→ 用 GORM `Clauses(clause.OnConflict{...})` 抽象层统一表达，参考 `model/channel.go` 已有用法。

### 5.2 服务分层与接口

```
controller/billing_controller.go
   ├── GetUserBillDaily   (UserAuth)
   ├── GetUserBillMonthly (UserAuth)
   ├── GetChannelBillDaily   (AdminAuth)
   ├── GetChannelBillMonthly (AdminAuth)
   ├── GetFullBillDaily   (AdminAuth)
   ├── GetFullBillMonthly (AdminAuth)
   ├── ExportBillCSV      (按角色分流)
   └── (channel_pricing CRUD, AdminAuth)

service/billing_query.go      ← GROUP BY 聚合逻辑
service/billing_job.go        ← T+1 ticker 任务
service/billing_calc.go       ← 调用 pkg/billingexpr 计算成本
service/billing_csv.go        ← CSV 流式输出

model/bill_daily_full.go      ← 表 + DAO
model/channel_pricing.go      ← 表 + DAO
model/billing_job_run.go      ← 表 + DAO
```

**接口列表**：

| Method | Path | Auth | 说明 |
| --- | --- | --- | --- |
| GET | `/api/billing/user/daily?start=&end=&group_by=` | UserAuth | 用户日账单（用户只见己单，user_id 来自 session） |
| GET | `/api/billing/user/monthly?month=YYYY-MM` | UserAuth | 用户月账单（SUM 该月日账单） |
| GET | `/api/billing/admin/user/daily?user_id=&start=&end=` | AdminAuth | 管理员查指定用户的日账单 |
| GET | `/api/billing/admin/channel/daily?channel_id=&start=&end=` | AdminAuth | 渠道日账单 |
| GET | `/api/billing/admin/channel/monthly?channel_id=&month=` | AdminAuth | 渠道月账单 |
| GET | `/api/billing/admin/full/daily?start=&end=&filters...` | AdminAuth | 全流程日账单（明细行） |
| GET | `/api/billing/admin/full/monthly?month=&filters...` | AdminAuth | 全流程月账单 |
| GET | `/api/billing/export.csv?scope=&...` | 视 scope | CSV 导出（流式） |
| GET / POST / PUT / DELETE | `/api/billing/channel-pricing/...` | AdminAuth | 成本定价 CRUD |
| POST | `/api/billing/admin/job/rerun` | RootAuth | 手动补跑某日 job（见 §5.5.3） |
| GET | `/api/billing/admin/job/runs?start=&end=` | AdminAuth | 查询 job 执行历史 |

### 5.3 查询层 — 三类账单的 GROUP BY 方案（不物化）

#### 5.3.1 用户日账单

```sql
SELECT
  stat_date,
  user_id,
  user_group,
  model_name,
  SUM(request_count)     AS request_count,
  SUM(prompt_tokens)     AS prompt_tokens,
  SUM(completion_tokens) AS completion_tokens,
  SUM(revenue_quota)     AS revenue_quota
FROM bill_daily_full
WHERE user_id = ? AND stat_date BETWEEN ? AND ?
GROUP BY stat_date, user_id, user_group, model_name
ORDER BY stat_date DESC, model_name
```

- **不暴露 cost/profit 字段给普通用户**（AC-7 权限隔离硬保障）：在 controller 层 marshal 时把 `CostQuota / ProfitQuota / CostMissing` 字段从响应 struct 中剔除。
- **复合索引覆盖**：`(user_id, stat_date)` → 直接走 `idx_bdf_user_date`。

#### 5.3.2 用户月账单（AC-2 数学一致性）

```sql
SELECT SUBSTR(stat_date, 1, 7) AS stat_month, ...
WHERE user_id = ? AND stat_date LIKE 'YYYY-MM-%'
GROUP BY stat_month, user_id, user_group, model_name
```

三库兼容：`SUBSTR` 在 SQLite/MySQL/PG 都可用；或更安全的做法是 `WHERE stat_date BETWEEN '<month>-01' AND '<month-end>'`，省 SUBSTR。

**AC-2 一致性保障**：月账单 SUM(daily) ≡ daily 直接 SUM，无单独表，AC-2 数学等价天然成立（避免独立流水带来的不一致）。

#### 5.3.3 渠道账单（同上，主轴换 channel_id）

#### 5.3.4 全流程账单

按 PRD §5.1.4 字段顺序输出：`日期 - 用户 - 用户分组 - 调用量(用户侧) - 收入 - 渠道分组 - 渠道 - 调用量(渠道侧) - 成本 - 利润`。

技术决策：用户侧调用量 ≡ 渠道侧调用量（同一行 log 记一次），故两列重复仅作展示。

```sql
SELECT
  stat_date, user_id, user_group, request_count AS user_request_count,
  revenue_quota, channel_group, channel_id, request_count AS channel_request_count,
  cost_quota, profit_quota, cost_missing
FROM bill_daily_full
WHERE stat_date BETWEEN ? AND ?
  [AND user_id = ?] [AND channel_id = ?] [AND channel_group = ?]
ORDER BY stat_date DESC, user_id, channel_id, model_name
LIMIT ? OFFSET ?
```

### 5.4 渠道多分组拆账 —— MVP 不支持的扩展点设计

PRD 决策：MVP 取 `Channel.Group` CSV 首项。**扩展点（v2 启用）**：

- 新增表 `bill_daily_full_channel_groups(bill_id BIGINT FK, channel_group VARCHAR(64))`，一对多关系。
- v1 schema 中**不创建这张表**，但在 ChannelGroup 字段语义上保留"首项"约定，方便 v2 时仅做"补齐子表 + 调整查询/导出层"，无需重建 bill_daily_full。
- PM 反馈到位若改为 MVP 必要 → 修改点局限于 §5.5 job 写入逻辑（一对多展开为多行）+ §5.3 查询 GROUP BY 维度 +1；预估增量 2 人天，不影响 v1 整体工期。

### 5.5 T+1 离线 Job

#### 5.5.1 调度（ticker 模式）

```go
// service/billing_job.go
const (
    billingJobTickInterval = 1 * time.Minute    // 检查频率
    billingJobTimeZone     = "Asia/Shanghai"
    billingJobCutoffHour   = 2                  // 02:00 起算
    billingJobBatchSize    = 5000               // 单批扫 logs 上限
)

var (
    billingJobOnce    sync.Once
    billingJobRunning atomic.Bool
)

func StartBillingJob() {
    billingJobOnce.Do(func() {
        if !common.IsMasterNode { return }
        gopool.Go(func() {
            ticker := time.NewTicker(billingJobTickInterval)
            defer ticker.Stop()
            for {
                runBillingJobIfDue()
                <-ticker.C
            }
        })
    })
}
```

`runBillingJobIfDue()` 内部：

1. 用 `Asia/Shanghai` 时区计算当前的 `today` 与 `yesterday`。
2. 查 `billing_job_run` 中 `stat_date=yesterday, job_kind='daily_full'` 是否存在 `status='success'`。
3. 若未成功 → 检查 `now >= yesterday + 02:00 Asia/Shanghai` → 启动一次 job。
4. `atomic.Bool` 防同节点并发；`UNIQUE(stat_date, job_kind)` 防数据库层重复插入。

#### 5.5.2 幂等性 —— 单日重跑安全

```
BEGIN
  // 主 DB
  INSERT billing_job_run (stat_date, kind, status='running', started_at=now, attempt++)
    ON CONFLICT (stat_date, kind) DO UPDATE
      SET status='running', started_at=now, attempt=attempt+1
      WHERE billing_job_run.status IN ('pending','failed');
  // 若 ON CONFLICT WHERE 不命中 → 已 success/running，跳过

  // 删除当日已有 bill_daily_full（如果是补跑）
  DELETE FROM bill_daily_full WHERE stat_date = yesterday;

  // 从 LOG_DB 按用户/渠道/模型聚合（批量）
  cursor = 0
  while true:
    rows = SELECT ... FROM logs WHERE created_at BETWEEN [yesterday_start_ts, yesterday_end_ts)
           AND id > cursor ORDER BY id LIMIT 5000
    if len(rows) == 0: break
    cursor = rows[-1].id
    // 在内存中按 (user_id, user_group, channel_id, model_name) 聚合
    // 注意：channel_group 需要在内存 join channels 表的 CSV 首项

  // 对每个聚合分组：
  for each group:
    cost_quota = billingexpr.Eval(channel_pricing[channel_id, model_name].CostExpr, tokens)
    profit_quota = revenue - cost
    INSERT bill_daily_full (...) ON CONFLICT (stat_date, user_id, channel_id, model_name) DO UPDATE ...

  UPDATE billing_job_run SET status='success', finished_at=now, rows_inserted=..., rows_updated=...
COMMIT (失败则 UPDATE billing_job_run SET status='failed', error_msg=...; 不回滚已写入的 bill_daily_full，让下次补跑覆盖)
```

**为什么先 DELETE 再 INSERT**：① 简化幂等语义（避免脏数据残留）；② 单日数据量可控（< 1 万行），DELETE 代价低；③ ON CONFLICT UPDATE 可作为兜底但不依赖。

**跨 LOG_DB / DB 事务**：不依赖跨库事务。失败回滚仅靠"重跑 → DELETE 当日 → 重写"实现。

#### 5.5.3 失败回滚与人工补跑

- **失败自动重试**：ticker 每分钟检查未 success 的最近 7 天 job → 重跑（含 `attempt < 5` 限制，避免死循环）。
- **手动补跑接口**：`POST /api/billing/admin/job/rerun {stat_date}`（RootAuth），重置该日 `billing_job_run.status='pending'`，下一个 ticker tick 重跑。
- **历史回填**：见 §5.5.4。

#### 5.5.4 历史数据回填窗口（闭环 PRD #6）

技术约束分析：

| 约束 | 现状 | 结论 |
| --- | --- | --- |
| logs 表保留期 | 仅手动 DeleteOldLog；无自动清理，最早数据 = 部署后未删除的最早日 | 实际窗口 = "现有 logs 最早日 ~ T-1" |
| logs 字段完整性 | 早期 logs 可能缺 `Group`（用户分组快照） | 缺字段的行 `user_group=''` 记入，不阻断；UI 标注"分组缺失" |
| Channel.Group 历史漂移 | 渠道历史可能改过分组 | 接受现在时态 Channel.Group（PRD 已选 simplified） |
| channel_pricing 历史 | 上线时大概率仅当前定价 | 用**当前 channel_pricing 计算历史成本**（PRD §5 决策 1 默认行为；用户接受"调价不追溯") |
| 性能 | 单日 1 万行 × 90 天 = 90 万行 | 在 SQLite/MySQL/PG 上 < 10 分钟可完成 |

**锁定决策**：

1. **默认仅 T+1 增量**：上线后从次日开始自动生成。
2. **可选历史回填**：管理员可通过 `POST /api/billing/admin/job/backfill {start_date, end_date}`（RootAuth）触发，**单次最多 90 天**（防止误操作锁库；90 天足够 1 个完整季度对账）。
3. **回填范围内**：start_date 不得早于 `logs` 表的 `MIN(created_at)` 对应日；接口校验。
4. **回填顺序**：从最旧日开始顺序补，保证中间结果可用。
5. **回填期间**：在 `billing_job_run` 表中按日插入 `kind='backfill'` 行，不与日常 `daily_full` 冲突。

> 超出 90 天的历史需求 → 分批触发，或直接 SQL 脚本由 DBA 手工跑（不暴露为产品功能）。

### 5.6 日切时区（闭环 PRD #2）

**锁定决策：`Asia/Shanghai`（UTC+8）**。

理由：

1. tcredit 是国内（基于 PRD §1.2 上下文与项目语料）业务，财务对账按中国自然日；UTC 会让月底跨日导致客户困惑。
2. 与现有 billingexpr 中默认 `hour("Asia/Shanghai")` 用法对齐（参见 `pkg/billingexpr/billingexpr_test.go:882,940`）。
3. 实现：在 `service/billing_job.go` 中 `time.LoadLocation("Asia/Shanghai")` 一次，所有 `stat_date` 计算用该时区；`Log.CreatedAt`（Unix ts）转换为 Asia/Shanghai 日期。
4. 跨服务器时区一致性：不依赖 OS TZ；用 `time.LoadLocation`（Go 内嵌 tzdata，运行时不依赖系统）。需要确保 Dockerfile 中包含 tzdata（验证：当前 `Dockerfile` 已使用 alpine 基础镜像 → 上线前确认 tzdata 包安装；如缺失，写入 plan.md TODO）。

**用户侧 UI**：所有日期均以 Asia/Shanghai 显示并加 `(UTC+8)` 注释，避免跨地域用户误读。

### 5.7 APM 选型（闭环 PRD #5）

**锁定决策：OpenTelemetry SDK + Prometheus exporter + Grafana**。

| 选项 | 优点 | 缺点 | 评分 |
| --- | --- | --- | --- |
| **OpenTelemetry + Prom + Grafana**（推荐） | 开源；与 gin/gorm 集成成熟（`otelgin`、`otelgorm`）；指标/链路/日志三合一；与现有部署体系（Docker / K8s）天然契合 | 自建运维成本（Prom + Grafana 各 1 副本即可） | **8.5/10** |
| NewRelic / Datadog | 开箱即用 | 商用授权费用 + 数据出海（GDPR 风险） | 5/10 |
| ELK + APM Server | 已知方案 | 资源占用大；与 Prom 重叠 | 6/10 |

**集成路径（最小可用版）**：

1. `go get go.opentelemetry.io/otel/...`、`otelgin`、`otelgorm`。
2. `main.go` 启动时初始化 OTel SDK，导出到 Prom 端点 `:9464/metrics`。
3. `router/main.go` 注入 `otelgin.Middleware("new-api")`。
4. `model/main.go` 给 `DB`、`LOG_DB` 装 `otelgorm.NewPlugin()`。
5. 在 controller/billing_controller.go 关键路径上加 span（手动埋点 3～5 处即够）。
6. Grafana 看板模板：`bill_query_p95`, `bill_query_qps`, `billing_job_duration`, `billing_job_failures`, `channel_pricing_missing_count`。

**与 PRD §6 护栏指标对接**：P95 指标 = `histogram_quantile(0.95, rate(http_server_duration_seconds_bucket{route=~"/api/billing/.*"}[5m]))`。

### 5.8 性能保障（对应 PRD AC-5, AC-8 / 护栏 P95 < 800ms 与准确性 < 0.1%）

#### 5.8.1 P95 < 800ms

- **预算分解**：DB 查询 < 300ms + 序列化 < 100ms + 网络 < 200ms + middleware（auth、log、cors）< 100ms + 缓冲 100ms。
- **索引保障**：§5.1.2 索引覆盖三类查询（避免 full scan）。
- **行数上限**：单次 API 默认 `LIMIT 1000`，最大 `LIMIT 10000`（CSV 导出走流式分页，单批 1000 行）。
- **基础压测要求**（plan.md 中 v1.0 验收前必跑）：在 100 万行 bill_daily_full 数据集下，30 天范围三类查询 P95 实测 < 800ms（SQLite/MySQL/PG 各跑一次）。

#### 5.8.2 准确性 < 0.1%（AC-8）

- **每日自检脚本**（v1.1 提供，本设计预留接口）：03:00 跑 `SUM(bill_daily_full.revenue_quota) WHERE stat_date=yesterday` vs `SUM(logs.quota) WHERE created_at IN yesterday_range`，差值率 > 0.1% → 邮件/Webhook 报警。
- **浮点取整**：成本计算 `billingexpr.Eval` 输出 float → 转 int64 `math.Round(cost_usd * QuotaPerUnit)`；规则同现有体系（参见 `pkg/billingexpr/run.go` 已有逻辑）。
- **时区一致性**：自检脚本与 job 必须使用同一 `Asia/Shanghai` 边界，避免日切误差。

### 5.9 权限隔离（AC-7）

- **API 层**：`UserAuth` 路由组下的接口强制 `user_id = session.user_id`（controller 拿 session 覆盖 query param）；`AdminAuth` 接口允许 `user_id` query。
- **SQL 层**：所有 `user_id` 来源审计，禁止用户控制的 query 参数直传到 SQL；走 service 层封装函数（如 `GetUserBillDaily(userId int, ...)`）。
- **响应层**：用户视图响应 struct 不包含 `CostQuota / ProfitQuota / CostMissing`（编译时保证，不依赖运行时分支）。
- **前端**：菜单项按 `role` 控制；后端 403 兜底（不依赖前端）。

### 5.10 前端

**MVP 仅在 `web/default/` 主题实现**，理由：

- default 是主推主题（React 19 + Base UI）；classic 用户为存量保留。
- 双主题同步实现 = 工期翻倍（≥ 5 人天），与 v1.0 P0 排期不匹配。
- classic 主题账单 → v1.1 同步（plan.md 中作为单独 issue 拆分）。

**目录结构**（参考 `features/usage-logs/` 风格）：

```
web/default/src/features/billing/
├── routes/
│   ├── UserBillPage.tsx      // 普通用户与管理员都用，按角色切换
│   ├── ChannelBillPage.tsx   // AdminAuth
│   └── FullBillPage.tsx      // AdminAuth
├── components/
│   ├── BillDateRangePicker.tsx
│   ├── BillTable.tsx
│   ├── GranularitySwitch.tsx  // 日/月切换
│   └── CsvExportButton.tsx
├── hooks/
│   └── useBillData.ts
└── lib/
    └── billingApi.ts
```

CSV 导出格式（AC-6）：UTF-8 with BOM、`\r\n` 行分隔、值不带千分符；管理员视图含 cost/profit 列，用户视图不含。

### 5.11 配置项（option）

新增 `model/option.go` 中（或 `setting/billing_setting/`）：

| Key | 默认 | 说明 |
| --- | --- | --- |
| `BillingJobEnabled` | `true` | 是否启动 T+1 job |
| `BillingJobCutoffHour` | `2` | 触发时刻（小时，Asia/Shanghai） |
| `BillingJobTimeZone` | `Asia/Shanghai` | 时区（不建议改） |
| `BillingMaxBackfillDays` | `90` | 单次回填上限 |
| `BillingCostMissingPolicy` | `warn`（默认） | 缺失策略：`warn`=AC-4 兜底；`block`=不写入并报警（暂不启用） |

---

## 6. 风险与回退

### 6.1 风险表

| # | 触发条件 | 影响 | 概率 | 缓解 | 回退方案 |
| --- | --- | --- | --- | --- | --- |
| R1 | LOG_DB 与主 DB 分离部署，T+1 job 跨库读写延迟大 | job 执行时间 > 10 min | 中 | 单次扫描批量 = 5000；并发分批处理；监控 `billing_job_duration` | 拆分为按 user_id 范围的 sub-job |
| R2 | channel_pricing 大面积缺失 → cost_quota=0 → profit 看起来超高 | 管理员误判渠道盈利 | 高（上线初期） | UI 标红"未配置成本"行；首页 dashboard 显示 `channel_pricing_missing_count` 指标 | 运营 SOP：上线前一周内补齐所有现役渠道成本定价 |
| R3 | Docker 镜像缺 tzdata → `time.LoadLocation("Asia/Shanghai")` 失败 | job 起不来 | 低 | plan.md 中 TODO 验证 Dockerfile + 加 `tzdata` apk | 临时改用 `time.FixedZone("CST", 8*3600)`（精度足够） |
| R4 | logs 历史字段缺失（早期 schema） | 部分行 user_group="" | 中 | AC-4 兜底逻辑覆盖；UI 显式标注 | 该行 user_group 显示"未知"，不阻断 |
| R5 | 30 天聚合查询走错索引 | P95 飙升 | 低 | 上线前在三库各跑一次 EXPLAIN | 添加 `FORCE INDEX` hint（仅 MySQL；其他库忽略） |
| R6 | job 失败连续 ≥ 3 天 | 账单无数据 | 低 | `billing_job_run` 状态表 + ticker 重试 + 监控告警 | 手动 `POST /api/billing/admin/job/rerun` |
| R7 | 多节点部署下两个节点都执行 job（IsMasterNode 配置漂移） | 重复写入 | 低 | `UNIQUE(stat_date, kind)` DB 层兜底；DELETE 当日 + ON CONFLICT 语义安全 | 数据自然收敛，无需人工 |
| R8 | billingexpr 编译失败（成本表达式语法错误） | 该渠道当日 cost=0 + warning | 中 | 保存时调用 `billingexpr.Compile` 校验；表达式无效不允许保存 | AC-4 兜底；UI 标记错误表达式 |
| R9 | CSV 导出文件过大（管理员选 90 天 × 全用户） | 内存爆 / 浏览器卡死 | 中 | 流式输出（`encoding/csv` + 分页 Flush）；前端进度提示；硬限制最大行数 1 百万 | 超限提示并强制缩短范围 |
| R10 | GDPR 区客户 | 数据保留法律风险 | PM 闭环 | 不在 design.md 范围 | 由 PM/法务决定 |

### 6.2 整体下线方案

- 前端隐藏 billing 菜单 + 后端路由返回 503；
- T+1 job 通过 `BillingJobEnabled=false` 立即停止（无需重启）；
- 数据表保留（不删 bill_daily_full / channel_pricing）；
- 完全不影响计费扣款（与扣款路径**零耦合**）。

---

## 7. 工期估算

> 估算粒度：人天；假设条件 = ① 1 名熟悉本仓库的后端 + 1 名熟悉 default 主题的前端；② channel_pricing 上线前 1 周内运营完成成本定价配置（不阻塞代码工期）；③ 三库测试环境已就绪。

| # | 组件 | 后端 | 前端 | 子合计 |
| --- | --- | --- | --- | --- |
| 1 | `channel_pricing` 表 + 4 个 CRUD API + billingexpr 集成 | 2 | — | 2 |
| 2 | `bill_daily_full` 表 + 索引 + 三库迁移测试 | 1.5 | — | 1.5 |
| 3 | `billing_job_run` 表 + ticker job + 幂等/补跑/手动回填接口 | 3.5 | — | 3.5 |
| 4 | 6 个查询 API（user/admin × daily/monthly × full）+ 权限分流 + service 层封装 | 3 | — | 3 |
| 5 | CSV 流式导出 + 按角色字段过滤 | 1 | — | 1 |
| 6 | OpenTelemetry 接入 + Prom exporter + Grafana 仪表盘模板 | 1.5 | — | 1.5 |
| 7 | 用户账单页（含日/月切换、筛选、导出） | — | 1.5 | 1.5 |
| 8 | 管理员渠道账单页 + 全流程账单页 + 成本定价配置页 | — | 3 | 3 |
| 9 | 单元测试 + 集成测试（job 幂等、AC-1～AC-8） | 2 | 0.5 | 2.5 |
| 10 | 联调 + 压测（100 万行场景） + 上线前 SOP 文档 | 1.5 | 0.5 | 2 |
| **合计（不含 buffer）** | | **16** | **5.5** | **21.5** |
| **+30% buffer** | | | | **28** |

**周历折算**（5 人天/周）：

- 单人后端：约 4 周；单人前端：约 1.5 周。
- 1 后端 + 1 前端并行：≈ **3.5 周**（约 17 工作日）。
- v1.1（自检脚本 + 性能优化 + classic 主题同步）：再 +1 周。

**关键路径**：组件 #1 → #2 → #3 → #4 → #9 → #10；组件 #6（APM）可并行；组件 #7/#8 在 #4 后启动。

**假设条件不成立的应对**：

- 若 PM 反馈 #3（多分组拆账）要求纳入 MVP → +2 人天，在 #3 与 #4 之间插入。
- 若运营无法在上线前补齐 channel_pricing → 走 AC-4 兜底 + 上线后逐步补齐（不延期上线，但 v1.0 初期 profit 数据将不可信，UI 标红提示）。

---

## 8. 演进路径

| 阶段 | 范围 | 兼容性 |
| --- | --- | --- |
| v1.0（本设计） | 物化日账单 + 按需聚合 + 三类视图 + APM | — |
| v1.1 | 准确性自检脚本 + 性能优化 + classic 主题账单 + 对账时长埋点回看 | 不改 schema |
| v2.0 | 管理员手动调账 + 异常告警 + 月独立流水 + **渠道多分组拆账**（启用 §5.4 扩展表） | +1 关系表，不破坏现有；月独立流水另起 `bill_monthly` 表 |
| v3.0（构想） | 多币种 + 汇率 + 准实时（分钟级聚合） | 准实时需替换 ticker 为流处理（如 Kafka + Flink），不复用现有 job |

**当前方案对 v2 友好性自评**：

- ✅ 调账：`bill_daily_full` + 新增 `bill_adjustment` 关系表即可；
- ✅ 告警：复用 `billing_job_run.error_msg` + `CostMissing` 字段，新增告警规则表；
- ✅ 多分组拆账：§5.4 已预留；
- ⚠️ 月独立流水：需新增 `bill_monthly` 表，但 v1 月账单始终 SUM(daily) 保证数学一致性，v2 可在 `bill_monthly` 上做调账叠加层。

**当前方案对 v3 限制**：

- ⚠️ 准实时账单需重构 job → 流；当前 T+1 设计与准实时差异较大；但 v1 数据模型在准实时方案下仍可保留为"日级快照表"，无破坏。

---

## 9. 验证证据

### 9.1 已完成的代码级核查

| 验证项 | 命令 / 文件 | 结论 |
| --- | --- | --- |
| Log 字段齐全 | `model/log.go:21-41` | UserId / ChannelId / ModelName / PromptTokens / CompletionTokens / Quota / Group ✓ |
| 现有 ticker 模式 | `service/subscription_reset_task.go:1-80` | sync.Once + IsMasterNode + atomic.Bool 模式可复用 ✓ |
| billingexpr 时区支持 | `pkg/billingexpr/billingexpr_test.go:882,940` | `hour("Asia/Shanghai")` 已在测试覆盖 ✓ |
| AutoMigrate 三库 | `model/main.go:258-285` | 新表加入 `migrateDB` 即可，GORM 抽象掉差异 ✓ |
| LOG_DB 分离能力 | `model/main.go:215-226,372` | LOG_DB 可独立 DSN；job 设计已按此假设展开 ✓ |
| Auth middleware | `middleware/auth.go:170-194` | UserAuth / AdminAuth / RootAuth 可直接挂账单路由 ✓ |
| 前端 feature 目录约定 | `web/default/src/features/usage-logs/` | features/billing/ 可参照 ✓ |

### 9.2 待 plan.md 阶段补做的验证

| 待验证项 | 方法 | 通过门槛 |
| --- | --- | --- |
| Dockerfile tzdata | 镜像构建 + `time.LoadLocation` 启动检查 | job 不因时区库缺失而 panic |
| 100 万行场景 P95 | 三库各填充 100 万行 → 三类查询压测 | P95 < 800ms |
| AC-8 准确性 | 模拟 1 天 logs + 跑 job + SUM 比对 | 差异率 < 0.1% |
| billingexpr 表达式兼容 | 用现有 `pricing` 表里的复杂表达式测试 | 编译+求值均成功 |

### 9.3 跑过的代码扫描命令（节选）

```
$ grep -rln "cron\|gocron\|robfig" --include="*.go" .          # 无任何 cron 库 → ticker 路线确认
$ grep -rn "RecordConsumeLog" --include="*.go" . | wc -l         # 9 处调用 → 全部不受本设计影响
$ grep -n "AutoMigrate" model/main.go                            # 258 / 293 / 341 / 362 / 372
$ wc -l model/log.go model/pricing.go model/channel.go           # 539 / 364 / 1077 → 不需重构现有大文件
$ grep -n "Asia/Shanghai" pkg/billingexpr/                       # 在测试中已使用 → 工程化预期良好
```

---

## 10. 待澄清项

> 本节仅列出**本设计阶段确实存在的未闭环项**；PRD §11 中已分给架构师的 3 项已在本文 §1.2 锁定。

| # | 事项 | 由谁 | 何时 | 期望回答 |
| --- | --- | --- | --- | --- |
| D-1 | 渠道多分组拆账是否进入 MVP（PRD §11 #3） | PJM + 运营 | PRD 评审 24h 内（与 PM 既定时间一致） | 是 → 触发 §5.4 扩展，工期 +2 人天；否 → 维持现状 |
| D-2 | LOG_DB 是否在生产部署上与主 DB 分离 | DBA / 运维 | plan.md 启动前 | 若分离 → 重点压测 job 跨库读取耗时；若合一 → 可允许单事务优化（非必须） |
| D-3 | channel_pricing 上线后由谁负责录入存量渠道成本 | PJM 分派给运营 | plan.md 中作为独立任务 | 给出 SOP 文档与 deadline |
| D-4 | Grafana 看板是否已有团队共享实例 | 运维 | plan.md 启动前 | 若有 → 复用；若无 → +0.5 人天部署 |

PRD §11 中 #1（对账时长基线）与 #4（GDPR 适用性）不属于设计阶段闭环范围，由 PM/运营负责，本设计不重复列入。

---

## 11. 反馈 PM 项（如有）

本设计过程中暴露的 PRD 层面问题（建议 PM 在 PRD v2 中修正）：

1. **PRD §3 / §4 "与 pricing 表对称"措辞不准确**：现有 `model/pricing.go` 是派生视图（由 ratio_setting + abilities 派生），不是 DB 表。本设计已修正为"与 ratio_setting 概念对称，独立持久化为 `channel_pricing` 表"。**不影响 PRD 决策的实质内容**，仅建议 PRD v2 措辞微调。
2. **PRD §6 "P95 ≤ 2s 视为可接受"与 "v1.1 必须达标"**：本设计承诺 v1.0 即可达成 < 800ms（基于物化方案 + 索引覆盖 + 行数限制）。若实测达不到，说明设计有问题（而非"留待 v1.1"）；建议 PRD v2 将"短期可接受 2s"删除。
3. 其余无重大歧义。

---

## 附录 A：迁移 DDL 示意（仅说明结构，实际由 GORM AutoMigrate 生成）

```sql
-- channel_pricing
CREATE TABLE channel_pricing (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL,
  model_name VARCHAR(128) NOT NULL,
  cost_expr TEXT NOT NULL DEFAULT '',
  currency VARCHAR(8) DEFAULT 'USD',
  status INTEGER DEFAULT 1,
  remark VARCHAR(255) DEFAULT '',
  created_at BIGINT,
  updated_at BIGINT,
  UNIQUE (channel_id, model_name)
);

-- bill_daily_full
CREATE TABLE bill_daily_full (
  id BIGINT PRIMARY KEY AUTOINCREMENT,
  stat_date VARCHAR(10) NOT NULL,
  user_id INTEGER NOT NULL,
  user_group VARCHAR(64) DEFAULT '',
  channel_id INTEGER NOT NULL,
  channel_group VARCHAR(64) DEFAULT '',
  model_name VARCHAR(128) DEFAULT '',
  request_count BIGINT DEFAULT 0,
  prompt_tokens BIGINT DEFAULT 0,
  completion_tokens BIGINT DEFAULT 0,
  revenue_quota BIGINT DEFAULT 0,
  cost_quota BIGINT DEFAULT 0,
  profit_quota BIGINT DEFAULT 0,
  cost_missing BOOLEAN DEFAULT 0,
  generated_at BIGINT,
  UNIQUE (stat_date, user_id, channel_id, model_name)
);
CREATE INDEX idx_bdf_date         ON bill_daily_full (stat_date);
CREATE INDEX idx_bdf_user_date    ON bill_daily_full (user_id, stat_date);
CREATE INDEX idx_bdf_channel_date ON bill_daily_full (channel_id, stat_date);

-- billing_job_run
CREATE TABLE billing_job_run (
  id BIGINT PRIMARY KEY AUTOINCREMENT,
  stat_date VARCHAR(10) NOT NULL,
  job_kind VARCHAR(32) DEFAULT 'daily_full',
  status VARCHAR(16) DEFAULT 'pending',
  started_at BIGINT DEFAULT 0,
  finished_at BIGINT DEFAULT 0,
  rows_inserted BIGINT DEFAULT 0,
  rows_updated BIGINT DEFAULT 0,
  error_msg VARCHAR(1024) DEFAULT '',
  attempt INTEGER DEFAULT 0,
  UNIQUE (stat_date, job_kind)
);
```

DDL 仅为 SQLite 风格示意；MySQL/PG 由 GORM 同语义生成（如 `AUTOINCREMENT` → `AUTO_INCREMENT` / `BIGSERIAL`，`BOOLEAN` → `TINYINT(1)` / `BOOLEAN`）。

## 附录 B：与 PRD 验收标准的映射

| 验收 | 实现位置 | 备注 |
| --- | --- | --- |
| AC-1 用户日账单 | §5.2 GetUserBillDaily + §5.3.1 | — |
| AC-2 月账单 SUM | §5.3.2 直接 GROUP BY stat_month | 数学一致性天然成立 |
| AC-3 渠道账单成本 | §5.5 job + §5.1.1 channel_pricing + billingexpr | — |
| AC-4 成本缺失兜底 | §5.5.2 cost_missing=true; 第 6.1 R2 | — |
| AC-5 管理员筛选 P95 | §5.8.1 索引 + LIMIT + 压测 | — |
| AC-6 CSV 导出 | §5.10 UTF-8 with BOM + 流式 | — |
| AC-7 权限隔离 | §5.9 三层（API/SQL/响应/前端） | — |
| AC-8 数据准确性 | §5.8.2 v1.1 对账脚本 + §5.5.2 幂等保障 | — |
