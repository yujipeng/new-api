# tcreditaim-账单 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 tcreditaim 平台交付账单模块 MVP（v1.0）：用户账单 / 渠道账单 / 全流程账单 × 日 / 月维度查询、导出、权限隔离、T+1 离线 job、成本定价配置、APM 接入。

**Architecture:** 后端 Go 1.22 + Gin + GORM v2 + 三库兼容（SQLite/MySQL/PG）；新增 3 张主 DB 表（`channel_pricing` / `bill_daily_full` / `billing_job_run`）+ ticker 模式 T+1 job（沿用 `subscription_reset_task` 模板）+ 6 个查询 API + CSV 流式导出 + OpenTelemetry/Prometheus 接入；前端仅在 `web/default/`（React 19 + Base UI）实现，`web/classic/` 留 v1.1。

**Tech Stack:** Go 1.22+ / Gin / GORM v2 / `pkg/billingexpr`（复用）/ React 19 / Base UI / Tailwind / Rsbuild / Bun / OpenTelemetry SDK + Prometheus exporter + Grafana。

**关联产物：**
- PRD v1.1：`docs/superpowers/specs/2026-05-20-billing-prd.md`（commit `6d81214d`）
- design.md v1：`docs/superpowers/specs/2026-05-20-billing-design.md`（commit `1de08263`）
- 关联 issue：[TES-28](mention://issue/26ce481d-8463-42de-ad03-39f6ad59686a)

**关卡门提醒（HARD-GATE）：**
- 每个 Task 完成后**必须**通过 `superpowers:verification-before-completion` 自验，附命令输出证据
- 全部 Task 完成后由 dev 发起两轮代码审查（`superpowers:requesting-code-review`），审查 LGTM 后才能进 QA
- QA 通过后才能推送远程，最终主干合并由项目负责人在 `superpowers:finishing-a-development-branch` 4 选项中决策

---

## 0. 全局约定与前置

**目录规约（设计 §5.2）：**
```
controller/billing_controller.go        ← 新建
service/billing_query.go                ← 新建
service/billing_job.go                  ← 新建
service/billing_calc.go                 ← 新建
service/billing_csv.go                  ← 新建
model/channel_pricing.go                ← 新建
model/bill_daily_full.go                ← 新建
model/billing_job_run.go                ← 新建
web/default/src/features/billing/       ← 新建（前端目录）
docs/operations/billing-sop.md          ← 新建（上线 SOP）
docs/grafana/billing-dashboard.json     ← 新建（仪表盘模板）
```

**任务编号规约：** `Phase X.Y`，X = 主阶段，Y = 阶段内顺序。关键路径见 design.md §7：1 → 2 → 3 → 4 → 9 → 10；Phase 6（APM）可与 1–5 并行；Phase 7（前端）在 Phase 4 完成后启动。

**JSON / DB / i18n 强制规则（CLAUDE.md Rule 1-3）：**
- 业务代码 JSON 必须用 `common.Marshal` / `common.Unmarshal`
- 所有 DB 代码三库（SQLite/MySQL/PG）必须同时通过；新表用 GORM AutoMigrate，避免裸 DDL；`stat_date` 用 `varchar(10)` 而非 DATE 类型
- 前端用 `bun` 跑脚本；新增 i18n key 必须同步 `web/default/src/i18n/locales/{zh,en}.json`

**保护规则（CLAUDE.md Rule 5）：** 不修改任何 `new-api` / `QuantumNous` 品牌标识；本 plan 添加的所有文件 license header 沿用项目既有风格。

**Commit 风格：** `feat(billing): ...` / `test(billing): ...` / `docs(billing): ...`；每个 Task 结尾**必须** commit；遇到 hook 失败则修复后再 commit（不允许 `--no-verify`）。

---

## Phase 1: 后端数据层（关键路径起点）

### Task 1.1: 创建 `channel_pricing` 模型

**Files:**
- Create: `model/channel_pricing.go`
- Create: `model/channel_pricing_test.go`

- [ ] **Step 1: 写失败测试 — 模型字段与唯一约束**

```go
// model/channel_pricing_test.go
package model

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestChannelPricing_CreateAndUniqueConstraint(t *testing.T) {
    setupTestDB(t) // 复用既有测试辅助；若无则 t.Skip 并在 Phase 8 补建
    cp1 := &ChannelPricing{ChannelId: 1, ModelName: "gpt-4o", CostExpr: "v1: p * 0.5 + c * 1.5", Currency: "USD", Status: 1}
    assert.NoError(t, DB.Create(cp1).Error)
    cp2 := &ChannelPricing{ChannelId: 1, ModelName: "gpt-4o", CostExpr: "v1: p * 0.4", Currency: "USD", Status: 1}
    err := DB.Create(cp2).Error
    assert.Error(t, err, "expected UNIQUE(channel_id, model_name) violation")
}
```

- [ ] **Step 2: 运行测试验证失败**

```
go test ./model/ -run TestChannelPricing_CreateAndUniqueConstraint -v
```
Expected: FAIL — `undefined: ChannelPricing`

- [ ] **Step 3: 实现 model/channel_pricing.go**

```go
package model

type ChannelPricing struct {
    Id        int    `json:"id"`
    ChannelId int    `json:"channel_id" gorm:"uniqueIndex:idx_cp_unique;not null"`
    ModelName string `json:"model_name" gorm:"type:varchar(128);uniqueIndex:idx_cp_unique;not null"`
    CostExpr  string `json:"cost_expr"  gorm:"type:text;not null;default:''"`
    Currency  string `json:"currency"   gorm:"type:varchar(8);default:'USD'"`
    Status    int    `json:"status"     gorm:"default:1"` // 1=enabled, 0=disabled
    Remark    string `json:"remark"     gorm:"type:varchar(255);default:''"`
    CreatedAt int64  `json:"created_at" gorm:"bigint;autoCreateTime:milli"`
    UpdatedAt int64  `json:"updated_at" gorm:"bigint;autoUpdateTime:milli"`
}

// GetChannelPricing returns the active pricing row for (channelId, modelName).
// Returns nil ChannelPricing and nil error if not found (caller drives AC-4 fallback).
func GetChannelPricing(channelId int, modelName string) (*ChannelPricing, error) {
    var cp ChannelPricing
    err := DB.Where("channel_id = ? AND model_name = ? AND status = 1", channelId, modelName).First(&cp).Error
    if err == gorm.ErrRecordNotFound {
        return nil, nil
    }
    return &cp, err
}

func ListChannelPricings(channelId int) ([]ChannelPricing, error) {
    var rows []ChannelPricing
    err := DB.Where("channel_id = ?", channelId).Order("model_name").Find(&rows).Error
    return rows, err
}
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./model/ -run TestChannelPricing_CreateAndUniqueConstraint -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add model/channel_pricing.go model/channel_pricing_test.go
git commit -m "feat(billing): add channel_pricing model with unique(channel_id, model_name)"
```

---

### Task 1.2: 创建 `bill_daily_full` 模型与索引

**Files:**
- Create: `model/bill_daily_full.go`
- Create: `model/bill_daily_full_test.go`

- [ ] **Step 1: 写失败测试 — 复合唯一约束 + 三种查询索引**

```go
package model

import "testing"

func TestBillDailyFull_UniqueConstraintAndIndexes(t *testing.T) {
    setupTestDB(t)
    r1 := &BillDailyFull{StatDate: "2026-05-19", UserId: 1, ChannelId: 1, ModelName: "gpt-4o", RevenueQuota: 100, CostQuota: 60, ProfitQuota: 40}
    assert.NoError(t, DB.Create(r1).Error)
    r2 := &BillDailyFull{StatDate: "2026-05-19", UserId: 1, ChannelId: 1, ModelName: "gpt-4o", RevenueQuota: 999}
    err := DB.Create(r2).Error
    assert.Error(t, err, "expected UNIQUE(stat_date, user_id, channel_id, model_name)")
}

func TestBillDailyFull_QueryByUserDate(t *testing.T) {
    setupTestDB(t)
    DB.Create(&BillDailyFull{StatDate: "2026-05-19", UserId: 1, ChannelId: 1, ModelName: "m", RevenueQuota: 50})
    var rows []BillDailyFull
    assert.NoError(t, DB.Where("user_id = ? AND stat_date BETWEEN ? AND ?", 1, "2026-05-01", "2026-05-31").Find(&rows).Error)
    assert.Equal(t, 1, len(rows))
}
```

- [ ] **Step 2: 运行测试验证失败**

```
go test ./model/ -run "TestBillDailyFull_" -v
```
Expected: FAIL — `undefined: BillDailyFull`

- [ ] **Step 3: 实现 model/bill_daily_full.go**

```go
package model

type BillDailyFull struct {
    Id               int64  `json:"id"                gorm:"primaryKey"`
    StatDate         string `json:"stat_date"         gorm:"type:varchar(10);uniqueIndex:idx_bdf_unique,priority:1;index:idx_bdf_date,priority:1;index:idx_bdf_user_date,priority:2;index:idx_bdf_channel_date,priority:2;not null"`
    UserId           int    `json:"user_id"           gorm:"uniqueIndex:idx_bdf_unique,priority:2;index:idx_bdf_user_date,priority:1;not null"`
    UserGroup        string `json:"user_group"        gorm:"type:varchar(64);index;default:''"`
    ChannelId        int    `json:"channel_id"        gorm:"uniqueIndex:idx_bdf_unique,priority:3;index:idx_bdf_channel_date,priority:1;not null"`
    ChannelGroup     string `json:"channel_group"     gorm:"type:varchar(64);index;default:''"`
    ModelName        string `json:"model_name"        gorm:"type:varchar(128);uniqueIndex:idx_bdf_unique,priority:4;index;default:''"`
    RequestCount     int64  `json:"request_count"     gorm:"default:0"`
    PromptTokens     int64  `json:"prompt_tokens"     gorm:"default:0"`
    CompletionTokens int64  `json:"completion_tokens" gorm:"default:0"`
    RevenueQuota     int64  `json:"revenue_quota"     gorm:"default:0"`
    CostQuota        int64  `json:"cost_quota"        gorm:"default:0"`
    ProfitQuota      int64  `json:"profit_quota"      gorm:"default:0"`
    CostMissing      bool   `json:"cost_missing"      gorm:"default:false"`
    GeneratedAt      int64  `json:"generated_at"      gorm:"bigint;autoCreateTime:milli"`
}
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./model/ -run "TestBillDailyFull_" -v
```
Expected: PASS（两个测试用例）

- [ ] **Step 5: Commit**

```
git add model/bill_daily_full.go model/bill_daily_full_test.go
git commit -m "feat(billing): add bill_daily_full materialized table with unique+covering indexes"
```

---

### Task 1.3: 创建 `billing_job_run` 模型

**Files:**
- Create: `model/billing_job_run.go`
- Create: `model/billing_job_run_test.go`

- [ ] **Step 1: 写失败测试 — 复合唯一约束 (stat_date, job_kind)**

```go
func TestBillingJobRun_UniqueByDateAndKind(t *testing.T) {
    setupTestDB(t)
    r1 := &BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full", Status: "running"}
    assert.NoError(t, DB.Create(r1).Error)
    r2 := &BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full", Status: "pending"}
    assert.Error(t, DB.Create(r2).Error)
    r3 := &BillingJobRun{StatDate: "2026-05-19", JobKind: "backfill", Status: "pending"}
    assert.NoError(t, DB.Create(r3).Error)
}
```

- [ ] **Step 2: 运行测试验证失败**

```
go test ./model/ -run TestBillingJobRun_ -v
```
Expected: FAIL

- [ ] **Step 3: 实现 model/billing_job_run.go**

```go
package model

const (
    BillingJobKindDailyFull = "daily_full"
    BillingJobKindBackfill  = "backfill"

    BillingJobStatusPending = "pending"
    BillingJobStatusRunning = "running"
    BillingJobStatusSuccess = "success"
    BillingJobStatusFailed  = "failed"

    BillingJobMaxAttempt = 5
)

type BillingJobRun struct {
    Id           int64  `json:"id"            gorm:"primaryKey"`
    StatDate     string `json:"stat_date"     gorm:"type:varchar(10);uniqueIndex:idx_bjr_date_kind,priority:1;not null"`
    JobKind      string `json:"job_kind"      gorm:"type:varchar(32);uniqueIndex:idx_bjr_date_kind,priority:2;default:'daily_full'"`
    Status       string `json:"status"        gorm:"type:varchar(16);default:'pending'"`
    StartedAt    int64  `json:"started_at"    gorm:"default:0"`
    FinishedAt   int64  `json:"finished_at"   gorm:"default:0"`
    RowsInserted int64  `json:"rows_inserted" gorm:"default:0"`
    RowsUpdated  int64  `json:"rows_updated"  gorm:"default:0"`
    ErrorMsg     string `json:"error_msg"     gorm:"type:varchar(1024);default:''"`
    Attempt      int    `json:"attempt"       gorm:"default:0"`
}
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./model/ -run TestBillingJobRun_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add model/billing_job_run.go model/billing_job_run_test.go
git commit -m "feat(billing): add billing_job_run state table with unique(stat_date, job_kind)"
```

---

### Task 1.4: 注册到 AutoMigrate（三库兼容验证）

**Files:**
- Modify: `model/main.go:258` 附近的 `migrateDB` 调用块

- [ ] **Step 1: 找到现有 AutoMigrate 入口**

```
grep -n "AutoMigrate" model/main.go
```
Expected: 在 line 258 / 293 / 341 / 362 / 372 出现。本任务的目标是把 3 个新模型加入第一个 `DB.AutoMigrate(` 调用块（主 DB 表组）。

- [ ] **Step 2: 在 model/main.go AutoMigrate 主 DB 表组中追加**

定位 `model/main.go` line ~258 的 `err := DB.AutoMigrate(` 块，按字母顺序在尾部加入：

```go
err := DB.AutoMigrate(
    // ... existing entries ...
    &ChannelPricing{},
    &BillDailyFull{},
    &BillingJobRun{},
)
```

- [ ] **Step 3: SQLite 启动迁移自检**

```
go run . --port 0 &  # 仅启动测试；如无 --port 0 支持，写入临时 sqlite 后立即 ctrl-C
# 或者用既有集成测试入口
go test ./model/ -run TestAutoMigrate_BillingTables -v
```
（若项目无 TestAutoMigrate 框架，本步骤改为手动启动 + `sqlite3 ./<dsn>.db ".schema channel_pricing"` 检查表存在与索引）

Expected: 3 张表均存在，索引 `idx_cp_unique`、`idx_bdf_unique`、`idx_bjr_date_kind` 均存在。

- [ ] **Step 4: MySQL 与 PostgreSQL 迁移验证**

参照 `docker-compose.yml` 或 README 中既有的 MySQL / PG 测试环境配置：
```
SQL_DSN="root:pass@tcp(127.0.0.1:3306)/newapi_test?charset=utf8mb4" go run . --port 0
SQL_DSN="postgres://user:pass@127.0.0.1:5432/newapi_test?sslmode=disable" go run . --port 0
```
启动后用 `SHOW INDEX FROM bill_daily_full` (MySQL) / `\d bill_daily_full` (PG) 确认索引生成。

Expected: 三库均生成 3 张表与全部索引；无 DDL 错误。

- [ ] **Step 5: Commit**

```
git add model/main.go
git commit -m "feat(billing): register channel_pricing/bill_daily_full/billing_job_run in AutoMigrate"
```

---

## Phase 2: `channel_pricing` CRUD API

> 所有接口挂在 `AdminAuth` 路由组（design.md §5.2）。响应统一用项目既有 `common.SuccessResponse` / `common.ErrorResponse` 封装。

### Task 2.1: GET 列表 / 详情

**Files:**
- Create: `controller/billing_controller.go`
- Modify: `router/api-router.go`（或对应 admin 路由文件，按既有结构）

- [ ] **Step 1: 写失败测试**

```go
// controller/billing_controller_test.go
func TestListChannelPricing_AdminAuthOnly(t *testing.T) {
    r := setupTestRouter(t)
    // 普通用户 -> 403
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/billing/channel-pricing?channel_id=1", nil)
    setUserAuth(req, /*role=*/ "user")
    r.ServeHTTP(w, req)
    assert.Equal(t, 403, w.Code)
    // 管理员 -> 200 + 空列表
    w2 := httptest.NewRecorder()
    req2, _ := http.NewRequest("GET", "/api/billing/channel-pricing?channel_id=1", nil)
    setUserAuth(req2, /*role=*/ "admin")
    r.ServeHTTP(w2, req2)
    assert.Equal(t, 200, w2.Code)
}
```

- [ ] **Step 2: 运行测试验证失败**

```
go test ./controller/ -run TestListChannelPricing_AdminAuthOnly -v
```
Expected: FAIL — 路由不存在 / 404

- [ ] **Step 3: 实现 controller**

```go
// controller/billing_controller.go
package controller

import (
    "strconv"
    "github.com/QuantumNous/new-api/common"
    "github.com/QuantumNous/new-api/model"
    "github.com/gin-gonic/gin"
)

func ListChannelPricing(c *gin.Context) {
    channelId, _ := strconv.Atoi(c.Query("channel_id"))
    rows, err := model.ListChannelPricings(channelId)
    if err != nil {
        common.ApiError(c, err)
        return
    }
    c.JSON(200, gin.H{"success": true, "data": rows})
}
```

- [ ] **Step 4: 注册路由**

```go
// router/api-router.go 中 admin 路由组下添加
billing := apiAdmin.Group("/billing")
billing.GET("/channel-pricing", controller.ListChannelPricing)
```

- [ ] **Step 5: 运行测试验证通过**

```
go test ./controller/ -run TestListChannelPricing_AdminAuthOnly -v
```
Expected: PASS

- [ ] **Step 6: Commit**

```
git add controller/billing_controller.go controller/billing_controller_test.go router/api-router.go
git commit -m "feat(billing): add GET /api/billing/channel-pricing (admin)"
```

---

### Task 2.2: POST 创建（含 billingexpr 编译校验）

**Files:** `controller/billing_controller.go`、`controller/billing_controller_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestCreateChannelPricing_InvalidExpr_Rejected(t *testing.T) {
    r := setupTestRouter(t)
    body := `{"channel_id":1,"model_name":"gpt-4o","cost_expr":"this is not a valid expr","currency":"USD"}`
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/billing/channel-pricing", strings.NewReader(body))
    setUserAuth(req, "admin")
    req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 400, w.Code, "invalid billingexpr must be rejected before persist")
}

func TestCreateChannelPricing_Success(t *testing.T) {
    setupTestDB(t)
    r := setupTestRouter(t)
    body := `{"channel_id":1,"model_name":"gpt-4o","cost_expr":"v1: p * 0.5 + c * 1.5","currency":"USD","remark":"trial"}`
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/billing/channel-pricing", strings.NewReader(body))
    setUserAuth(req, "admin"); req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var cnt int64
    model.DB.Model(&model.ChannelPricing{}).Where("channel_id=1 AND model_name='gpt-4o'").Count(&cnt)
    assert.Equal(t, int64(1), cnt)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./controller/ -run TestCreateChannelPricing -v
```
Expected: FAIL — 路由 404 / 未做校验

- [ ] **Step 3: 实现（接入 `pkg/billingexpr.Compile`）**

```go
import "github.com/QuantumNous/new-api/pkg/billingexpr"

type createChannelPricingReq struct {
    ChannelId int    `json:"channel_id" binding:"required"`
    ModelName string `json:"model_name" binding:"required"`
    CostExpr  string `json:"cost_expr"  binding:"required"`
    Currency  string `json:"currency"`
    Remark    string `json:"remark"`
}

func CreateChannelPricing(c *gin.Context) {
    var req createChannelPricingReq
    if err := c.ShouldBindJSON(&req); err != nil {
        common.ApiErrorMsg(c, "invalid request: "+err.Error()); return
    }
    if _, err := billingexpr.Compile(req.CostExpr); err != nil {
        common.ApiErrorMsg(c, "invalid cost_expr: "+err.Error()); return
    }
    cp := &model.ChannelPricing{
        ChannelId: req.ChannelId, ModelName: req.ModelName,
        CostExpr:  req.CostExpr, Currency: defaultStr(req.Currency, "USD"),
        Status:    1, Remark: req.Remark,
    }
    if err := model.DB.Create(cp).Error; err != nil {
        common.ApiError(c, err); return
    }
    c.JSON(200, gin.H{"success": true, "data": cp})
}
```

- [ ] **Step 4: 注册路由 + 跑测试**

```
billing.POST("/channel-pricing", controller.CreateChannelPricing)
go test ./controller/ -run TestCreateChannelPricing -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add controller/billing_controller.go controller/billing_controller_test.go router/api-router.go
git commit -m "feat(billing): add POST /api/billing/channel-pricing with billingexpr.Compile validation"
```

---

### Task 2.3: PUT 更新 + DELETE 删除

**Files:** 同上

- [ ] **Step 1: 写失败测试（更新成功 / 删除返回 200 / 未知 id 404）**

```go
func TestUpdateChannelPricing_RecompilesExpr(t *testing.T) {
    setupTestDB(t)
    cp := &model.ChannelPricing{ChannelId: 1, ModelName: "m", CostExpr: "v1: p", Currency: "USD", Status: 1}
    assert.NoError(t, model.DB.Create(cp).Error)
    r := setupTestRouter(t)
    body := `{"channel_id":1,"model_name":"m","cost_expr":"v1: c * 2.0","currency":"USD"}`
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("PUT", "/api/billing/channel-pricing/"+strconv.Itoa(cp.Id), strings.NewReader(body))
    setUserAuth(req, "admin"); req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var updated model.ChannelPricing
    model.DB.First(&updated, cp.Id)
    assert.Equal(t, "v1: c * 2.0", updated.CostExpr)
}

func TestUpdateChannelPricing_InvalidExpr_Rejected(t *testing.T) {
    setupTestDB(t)
    cp := &model.ChannelPricing{ChannelId: 1, ModelName: "m", CostExpr: "v1: p", Status: 1}
    model.DB.Create(cp)
    body := `{"channel_id":1,"model_name":"m","cost_expr":"!!invalid!!","currency":"USD"}`
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("PUT", "/api/billing/channel-pricing/"+strconv.Itoa(cp.Id), strings.NewReader(body))
    setUserAuth(req, "admin"); req.Header.Set("Content-Type", "application/json")
    setupTestRouter(t).ServeHTTP(w, req)
    assert.Equal(t, 400, w.Code)
}

func TestDeleteChannelPricing_Success(t *testing.T) {
    setupTestDB(t)
    cp := &model.ChannelPricing{ChannelId: 1, ModelName: "m", CostExpr: "v1: p", Status: 1}
    model.DB.Create(cp)
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("DELETE", "/api/billing/channel-pricing/"+strconv.Itoa(cp.Id), nil)
    setUserAuth(req, "admin")
    setupTestRouter(t).ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var cnt int64
    model.DB.Model(&model.ChannelPricing{}).Where("id = ?", cp.Id).Count(&cnt)
    assert.Equal(t, int64(0), cnt)
}

func TestDeleteChannelPricing_NotFound_Returns404(t *testing.T) {
    setupTestDB(t)
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("DELETE", "/api/billing/channel-pricing/9999", nil)
    setUserAuth(req, "admin")
    setupTestRouter(t).ServeHTTP(w, req)
    assert.Equal(t, 404, w.Code)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./controller/ -run "TestUpdateChannelPricing_|TestDeleteChannelPricing_" -v
```

- [ ] **Step 3: 实现**

```go
func UpdateChannelPricing(c *gin.Context) {
    id, _ := strconv.Atoi(c.Param("id"))
    var cp model.ChannelPricing
    if err := model.DB.First(&cp, id).Error; err != nil { c.JSON(404, gin.H{"success": false}); return }
    var req createChannelPricingReq
    if err := c.ShouldBindJSON(&req); err != nil { common.ApiErrorMsg(c, err.Error()); return }
    if _, err := billingexpr.Compile(req.CostExpr); err != nil { common.ApiErrorMsg(c, err.Error()); return }
    cp.CostExpr = req.CostExpr; cp.Currency = req.Currency; cp.Remark = req.Remark
    if err := model.DB.Save(&cp).Error; err != nil { common.ApiError(c, err); return }
    c.JSON(200, gin.H{"success": true, "data": cp})
}

func DeleteChannelPricing(c *gin.Context) {
    id, _ := strconv.Atoi(c.Param("id"))
    res := model.DB.Delete(&model.ChannelPricing{}, id)
    if res.Error != nil { common.ApiError(c, res.Error); return }
    if res.RowsAffected == 0 { c.JSON(404, gin.H{"success": false}); return }
    c.JSON(200, gin.H{"success": true})
}
```

注册路由：
```
billing.PUT("/channel-pricing/:id", controller.UpdateChannelPricing)
billing.DELETE("/channel-pricing/:id", controller.DeleteChannelPricing)
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./controller/ -run "TestUpdateChannelPricing_|TestDeleteChannelPricing_" -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git commit -am "feat(billing): add PUT/DELETE /api/billing/channel-pricing/:id"
```

---

## Phase 3: T+1 离线 Job

> 本阶段沿用 `service/subscription_reset_task.go` 模板（已确认：`sync.Once` + `IsMasterNode` + `atomic.Bool` + `time.NewTicker`）。

### Task 3.1: billing_calc.go — 成本计算辅助

**Files:** `service/billing_calc.go`、`service/billing_calc_test.go`

- [ ] **Step 1: 写失败测试**

```go
package service

import (
    "testing"
    "github.com/QuantumNous/new-api/model"
)

func TestCalcCostQuota_MissingPricing_FallsBackToZero(t *testing.T) {
    cost, missing := CalcCostQuota(/*channelId=*/999, "unknown-model", 1000, 500)
    assert.Equal(t, int64(0), cost)
    assert.True(t, missing)
}

func TestCalcCostQuota_HappyPath(t *testing.T) {
    // arrange: 写一条 channel_pricing(channel=1, model=m, cost_expr="v1: p + c")
    setupChannelPricing(t, 1, "m", "v1: p + c")
    cost, missing := CalcCostQuota(1, "m", 100, 50)
    assert.False(t, missing)
    assert.Greater(t, cost, int64(0))
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestCalcCostQuota_ -v
```

- [ ] **Step 3: 实现**

```go
// service/billing_calc.go
package service

import (
    "github.com/QuantumNous/new-api/model"
    "github.com/QuantumNous/new-api/pkg/billingexpr"
)

// CalcCostQuota returns (cost_quota_int64, cost_missing_flag).
// cost_missing=true ⇒ AC-4 fallback path: caller must set cost_quota=0 in bill_daily_full.
func CalcCostQuota(channelId int, modelName string, promptTokens, completionTokens int64) (int64, bool) {
    cp, err := model.GetChannelPricing(channelId, modelName)
    if err != nil || cp == nil || cp.CostExpr == "" {
        return 0, true
    }
    out, err := billingexpr.Eval(cp.CostExpr, billingexpr.Vars{
        "p": float64(promptTokens),
        "c": float64(completionTokens),
    })
    if err != nil {
        return 0, true // AC-4 兜底：表达式运行时错误等同未配置
    }
    // 与现有体系一致：USD * QuotaPerUnit; 由 billingexpr 输出已是 quota 等价单位（见 pkg/billingexpr/run.go）
    return int64(out + 0.5), false
}
```

> 备注：若 `billingexpr.Vars` 字段名与上面假设不一致，按 `pkg/billingexpr/expr.md` 实际签名调整；不允许把"差异"留作 TODO。

- [ ] **Step 4: 运行测试验证通过**

```
go test ./service/ -run TestCalcCostQuota_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add service/billing_calc.go service/billing_calc_test.go
git commit -m "feat(billing): add CalcCostQuota wrapping billingexpr with AC-4 fallback"
```

---

### Task 3.2: billing_job.go — ticker 骨架（沿用 subscription_reset_task 模板）

**Files:** `service/billing_job.go`

- [ ] **Step 1: 写失败测试 — 时区判定与 idempotency**

```go
func TestBillingJob_SkipWhenYesterdaySuccess(t *testing.T) {
    setupTestDB(t)
    // 先插入一条 success 记录
    yest := YesterdayInShanghai(time.Now())
    model.DB.Create(&model.BillingJobRun{StatDate: yest, JobKind: "daily_full", Status: "success"})
    // 调用 runBillingJobIfDue ─ 不应改变记录
    runBillingJobIfDue()
    var r model.BillingJobRun
    model.DB.Where("stat_date = ? AND job_kind = 'daily_full'", yest).First(&r)
    assert.Equal(t, "success", r.Status)
    assert.Equal(t, 0, r.Attempt)
}

func TestBillingJob_RunsWhenPending(t *testing.T) {
    setupTestDB(t)
    yest := YesterdayInShanghai(time.Now())
    runBillingJobIfDue()
    var r model.BillingJobRun
    model.DB.Where("stat_date = ? AND job_kind = 'daily_full'", yest).First(&r)
    assert.Contains(t, []string{"success", "running"}, r.Status)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestBillingJob_ -v
```

- [ ] **Step 3: 实现 ticker 骨架 + 时区辅助**

```go
// service/billing_job.go
package service

import (
    "context"
    "sync"
    "sync/atomic"
    "time"

    "github.com/QuantumNous/new-api/common"
    "github.com/QuantumNous/new-api/logger"
    "github.com/QuantumNous/new-api/model"

    "github.com/bytedance/gopkg/util/gopool"
    "gorm.io/gorm/clause"
)

const (
    billingJobTickInterval = 1 * time.Minute
    billingJobTimeZoneName = "Asia/Shanghai"
    billingJobCutoffHour   = 2
    billingJobBatchSize    = 5000
)

var (
    billingJobOnce     sync.Once
    billingJobRunning  atomic.Bool
    billingJobLocation *time.Location
)

func StartBillingJob() {
    billingJobOnce.Do(func() {
        loc, err := time.LoadLocation(billingJobTimeZoneName)
        if err != nil {
            logger.LogError(context.Background(), "billing job: LoadLocation failed: "+err.Error())
            // 降级：FixedZone(+8h) — 精度与 Asia/Shanghai 等价
            loc = time.FixedZone("CST", 8*3600)
        }
        billingJobLocation = loc
        if !common.IsMasterNode { return }
        gopool.Go(func() {
            ticker := time.NewTicker(billingJobTickInterval)
            defer ticker.Stop()
            runBillingJobIfDue()
            for range ticker.C {
                runBillingJobIfDue()
            }
        })
    })
}

// YesterdayInShanghai returns 'YYYY-MM-DD' for the previous day in Asia/Shanghai.
func YesterdayInShanghai(now time.Time) string {
    if billingJobLocation == nil {
        billingJobLocation, _ = time.LoadLocation(billingJobTimeZoneName)
    }
    return now.In(billingJobLocation).AddDate(0, 0, -1).Format("2006-01-02")
}

func runBillingJobIfDue() {
    if !billingJobRunning.CompareAndSwap(false, true) { return }
    defer billingJobRunning.Store(false)

    now := time.Now()
    if now.In(billingJobLocation).Hour() < billingJobCutoffHour {
        return
    }
    yest := YesterdayInShanghai(now)
    var existing model.BillingJobRun
    err := model.DB.Where("stat_date = ? AND job_kind = ?", yest, model.BillingJobKindDailyFull).First(&existing).Error
    if err == nil && existing.Status == model.BillingJobStatusSuccess {
        return
    }
    if err == nil && existing.Attempt >= model.BillingJobMaxAttempt {
        return // 已达重试上限，等待人工补跑
    }
    runDailyFullJob(yest, &existing)
}
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./service/ -run TestBillingJob_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add service/billing_job.go service/billing_job_test.go
git commit -m "feat(billing): add T+1 billing job ticker scaffold with Asia/Shanghai cutoff"
```

---

### Task 3.3: 实现 `runDailyFullJob` — 扫描 logs + 聚合 + 写入

**Files:** `service/billing_job.go`、`service/billing_job_test.go`

- [ ] **Step 1: 写失败测试 — 单日聚合正确性**

```go
func TestRunDailyFullJob_Aggregates_OneDay(t *testing.T) {
    setupTestDB(t)
    yest := YesterdayInShanghai(time.Now())
    // 准备：3 条 logs（同用户/同渠道/同模型 → 应聚合成 1 行）
    seedLogsForYesterday(t, 1, 1, "gpt-4o", []int64{500, 300, 200}, []int64{100, 50, 50}, []int{60, 40, 30})
    // 准备：channel_pricing 让成本可算
    model.DB.Create(&model.ChannelPricing{ChannelId: 1, ModelName: "gpt-4o", CostExpr: "v1: p + c", Status: 1})

    var run model.BillingJobRun
    model.DB.Where("stat_date = ? AND job_kind = ?", yest, "daily_full").FirstOrCreate(&run)
    runDailyFullJob(yest, &run)

    var rows []model.BillDailyFull
    model.DB.Where("stat_date = ?", yest).Find(&rows)
    assert.Equal(t, 1, len(rows))
    assert.Equal(t, int64(3), rows[0].RequestCount)
    assert.Equal(t, int64(130), rows[0].RevenueQuota) // 60+40+30
    assert.False(t, rows[0].CostMissing)
}

func TestRunDailyFullJob_IdempotentRerun_DeletesOld(t *testing.T) {
    // arrange: 已有一行 bill_daily_full（旧数据，错误成本）
    // act: 跑 runDailyFullJob 第二次
    // assert: 仅一行；cost 已更新为新计算值
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestRunDailyFullJob_ -v
```

- [ ] **Step 3: 实现**

```go
func runDailyFullJob(statDate string, runRec *model.BillingJobRun) {
    ctx := context.Background()
    runRec.StatDate = statDate
    runRec.JobKind = model.BillingJobKindDailyFull
    runRec.Status = model.BillingJobStatusRunning
    runRec.StartedAt = time.Now().UnixMilli()
    runRec.Attempt++
    model.DB.Clauses(clause.OnConflict{
        Columns:   []clause.Column{{Name: "stat_date"}, {Name: "job_kind"}},
        DoUpdates: clause.AssignmentColumns([]string{"status", "started_at", "attempt"}),
    }).Create(runRec)

    // 1) 删除当日所有行（幂等基线）
    if err := model.DB.Where("stat_date = ?", statDate).Delete(&model.BillDailyFull{}).Error; err != nil {
        finishJobFailed(runRec, "delete old rows: "+err.Error()); return
    }

    // 2) 计算日切边界（Asia/Shanghai）→ Unix range
    startTs, endTs := dayBoundsInShanghai(statDate)

    // 3) 按 id cursor 批量扫 logs
    type aggKey struct{ UserId, ChannelId int; UserGroup, ModelName string }
    type aggVal struct{ ReqCount, P, C, Revenue int64 }
    bucket := map[aggKey]*aggVal{}

    cursor := int64(0)
    for {
        var logs []model.Log
        q := model.LOG_DB.Where("created_at >= ? AND created_at < ? AND id > ?", startTs, endTs, cursor).
            Order("id").Limit(billingJobBatchSize)
        if err := q.Find(&logs).Error; err != nil {
            finishJobFailed(runRec, "scan logs: "+err.Error()); return
        }
        if len(logs) == 0 { break }
        cursor = logs[len(logs)-1].Id
        for _, lg := range logs {
            k := aggKey{lg.UserId, lg.ChannelId, lg.Group, lg.ModelName}
            v := bucket[k]
            if v == nil { v = &aggVal{}; bucket[k] = v }
            v.ReqCount++
            v.P += int64(lg.PromptTokens)
            v.C += int64(lg.CompletionTokens)
            v.Revenue += int64(lg.Quota)
        }
        if len(logs) < billingJobBatchSize { break }
    }

    // 4) 取 channel.Group 首项缓存
    channelGroupCache := map[int]string{}
    getChannelGroup := func(channelId int) string {
        if g, ok := channelGroupCache[channelId]; ok { return g }
        var ch model.Channel
        if err := model.DB.Select("`group`").First(&ch, channelId).Error; err != nil {
            channelGroupCache[channelId] = ""
            return ""
        }
        first := firstCSV(ch.Group)
        channelGroupCache[channelId] = first
        return first
    }

    // 5) 计算成本并写入
    inserted := int64(0)
    for k, v := range bucket {
        cost, missing := CalcCostQuota(k.ChannelId, k.ModelName, v.P, v.C)
        row := &model.BillDailyFull{
            StatDate: statDate, UserId: k.UserId, UserGroup: k.UserGroup,
            ChannelId: k.ChannelId, ChannelGroup: getChannelGroup(k.ChannelId),
            ModelName: k.ModelName, RequestCount: v.ReqCount,
            PromptTokens: v.P, CompletionTokens: v.C,
            RevenueQuota: v.Revenue, CostQuota: cost,
            ProfitQuota: v.Revenue - cost, CostMissing: missing,
        }
        if err := model.DB.Create(row).Error; err != nil {
            finishJobFailed(runRec, "insert bill_daily_full: "+err.Error()); return
        }
        inserted++
    }

    runRec.Status = model.BillingJobStatusSuccess
    runRec.FinishedAt = time.Now().UnixMilli()
    runRec.RowsInserted = inserted
    runRec.ErrorMsg = ""
    model.DB.Save(runRec)
    logger.LogInfo(ctx, fmt.Sprintf("billing job %s success: rows=%d", statDate, inserted))
}

func finishJobFailed(r *model.BillingJobRun, msg string) {
    r.Status = model.BillingJobStatusFailed
    r.FinishedAt = time.Now().UnixMilli()
    r.ErrorMsg = msg
    model.DB.Save(r)
    logger.LogError(context.Background(), "billing job failed: "+msg)
}

func dayBoundsInShanghai(statDate string) (int64, int64) {
    loc := billingJobLocation
    if loc == nil { loc, _ = time.LoadLocation(billingJobTimeZoneName) }
    t0, _ := time.ParseInLocation("2006-01-02", statDate, loc)
    t1 := t0.AddDate(0, 0, 1)
    return t0.Unix(), t1.Unix()
}

func firstCSV(s string) string {
    if s == "" { return "" }
    for i, ch := range s {
        if ch == ',' { return s[:i] }
    }
    return s
}
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./service/ -run TestRunDailyFullJob_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git commit -am "feat(billing): implement runDailyFullJob with batched log scan + idempotent rewrite"
```

---

### Task 3.4: 手动补跑接口 `POST /api/billing/admin/job/rerun`

**Files:** `controller/billing_controller.go`、test 文件

- [ ] **Step 1: 写失败测试**

```go
func TestRerunJob_ResetsStatusToPending(t *testing.T) {
    setupTestDB(t)
    model.DB.Create(&model.BillingJobRun{StatDate: "2026-05-18", JobKind: "daily_full", Status: "failed", Attempt: 3, ErrorMsg: "x"})
    r := setupTestRouter(t)
    body := `{"stat_date":"2026-05-18"}`
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/billing/admin/job/rerun", strings.NewReader(body))
    setUserAuth(req, "root"); req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var rec model.BillingJobRun
    model.DB.Where("stat_date='2026-05-18'").First(&rec)
    assert.Equal(t, "pending", rec.Status)
    assert.Equal(t, 0, rec.Attempt)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./controller/ -run TestRerunJob_ -v
```

- [ ] **Step 3: 实现 + 注册（注意：RootAuth 而非 AdminAuth）**

```go
type rerunJobReq struct{ StatDate string `json:"stat_date" binding:"required"` }

func RerunBillingJob(c *gin.Context) {
    var req rerunJobReq
    if err := c.ShouldBindJSON(&req); err != nil { common.ApiErrorMsg(c, err.Error()); return }
    res := model.DB.Model(&model.BillingJobRun{}).
        Where("stat_date = ? AND job_kind = ?", req.StatDate, model.BillingJobKindDailyFull).
        Updates(map[string]any{"status": model.BillingJobStatusPending, "attempt": 0, "error_msg": ""})
    if res.Error != nil { common.ApiError(c, res.Error); return }
    c.JSON(200, gin.H{"success": true, "affected": res.RowsAffected})
}

// router/api-router.go
billingRoot := apiRoot.Group("/billing/admin/job") // RootAuth 组
billingRoot.POST("/rerun", controller.RerunBillingJob)
```

- [ ] **Step 4: 运行测试验证通过**

```
go test ./controller/ -run TestRerunJob_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git commit -am "feat(billing): add POST /api/billing/admin/job/rerun (RootAuth)"
```

---

### Task 3.5: 历史回填接口 `POST /api/billing/admin/job/backfill`（≤ 90 天）

**Files:** 同上

- [ ] **Step 1: 写失败测试**

```go
func TestBackfill_RejectsOver90Days(t *testing.T) {
    r := setupTestRouter(t)
    body := `{"start_date":"2026-01-01","end_date":"2026-05-15"}` // > 90 天
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/billing/admin/job/backfill", strings.NewReader(body))
    setUserAuth(req, "root"); req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 400, w.Code)
}

func TestBackfill_SchedulesPendingRows(t *testing.T) {
    setupTestDB(t)
    r := setupTestRouter(t)
    body := `{"start_date":"2026-05-15","end_date":"2026-05-17"}` // 3 天
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/billing/admin/job/backfill", strings.NewReader(body))
    setUserAuth(req, "root"); req.Header.Set("Content-Type", "application/json")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var cnt int64
    model.DB.Model(&model.BillingJobRun{}).Where("job_kind='backfill' AND status='pending'").Count(&cnt)
    assert.Equal(t, int64(3), cnt)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./controller/ -run TestBackfill_ -v
```

- [ ] **Step 3: 实现**

```go
type backfillReq struct {
    StartDate string `json:"start_date" binding:"required"` // YYYY-MM-DD
    EndDate   string `json:"end_date"   binding:"required"`
}

func BackfillBillingJob(c *gin.Context) {
    var req backfillReq
    if err := c.ShouldBindJSON(&req); err != nil { common.ApiErrorMsg(c, err.Error()); return }
    loc, _ := time.LoadLocation("Asia/Shanghai")
    start, err1 := time.ParseInLocation("2006-01-02", req.StartDate, loc)
    end,   err2 := time.ParseInLocation("2006-01-02", req.EndDate,   loc)
    if err1 != nil || err2 != nil { common.ApiErrorMsg(c, "invalid date"); return }
    days := int(end.Sub(start).Hours()/24) + 1
    if days <= 0 || days > 90 {
        common.ApiErrorMsg(c, "range must be 1..90 days"); return
    }
    for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
        rec := model.BillingJobRun{
            StatDate: d.Format("2006-01-02"),
            JobKind:  model.BillingJobKindBackfill,
            Status:   model.BillingJobStatusPending,
        }
        model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
    }
    c.JSON(200, gin.H{"success": true, "days_scheduled": days})
}
```

> 同时在 `runBillingJobIfDue` 增加一段 backfill 扫描：找到 `kind='backfill' AND status='pending'` 行，按 stat_date 升序逐日执行 `runDailyFullJob(date, &rec)`（修改 runRec.JobKind 区分写入分类）。**注意**：此处对 `runDailyFullJob` 函数签名做调整 — 把 `JobKind` 改为参数；之前 task 中的测试用例需要同步用新签名。

- [ ] **Step 4: 同步调整 runDailyFullJob 接受 kind 入参 + 重跑既有测试**

```
go test ./service/ -run TestRunDailyFullJob_ -v
go test ./controller/ -run TestBackfill_ -v
```
Expected: 全部 PASS

- [ ] **Step 5: 注册路由 + Commit**

```
billingRoot.POST("/backfill", controller.BackfillBillingJob)
git commit -am "feat(billing): add POST /api/billing/admin/job/backfill (RootAuth, ≤90d)"
```

---

### Task 3.6: GET `/api/billing/admin/job/runs` — 状态查询

**Files:** `controller/billing_controller.go`

- [ ] **Step 1: 写失败测试**

```go
func TestListJobRuns_FiltersByDateRange(t *testing.T) {
    setupTestDB(t)
    for _, d := range []string{"2026-05-10", "2026-05-15", "2026-05-20"} {
        model.DB.Create(&model.BillingJobRun{StatDate: d, JobKind: "daily_full", Status: "success"})
    }
    r := setupTestRouter(t)
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/billing/admin/job/runs?start=2026-05-12&end=2026-05-18", nil)
    setUserAuth(req, "admin")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    var resp struct{ Data []model.BillingJobRun `json:"data"` }
    json.Unmarshal(w.Body.Bytes(), &resp)
    assert.Equal(t, 1, len(resp.Data))
    assert.Equal(t, "2026-05-15", resp.Data[0].StatDate)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./controller/ -run TestListJobRuns_ -v
```

- [ ] **Step 3: 实现**

```go
func ListBillingJobRuns(c *gin.Context) {
    start := c.Query("start"); end := c.Query("end")
    q := model.DB.Model(&model.BillingJobRun{})
    if start != "" { q = q.Where("stat_date >= ?", start) }
    if end   != "" { q = q.Where("stat_date <= ?", end) }
    var rows []model.BillingJobRun
    if err := q.Order("stat_date desc").Limit(500).Find(&rows).Error; err != nil { common.ApiError(c, err); return }
    c.JSON(200, gin.H{"success": true, "data": rows})
}

// 注册：admin 组（非 root）
apiAdmin.Group("/billing/admin/job").GET("/runs", controller.ListBillingJobRuns)
```

- [ ] **Step 4: 跑测试**

```
go test ./controller/ -run TestListJobRuns_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git commit -am "feat(billing): add GET /api/billing/admin/job/runs"
```

---

### Task 3.7: 在 `main.go` 注册 `StartBillingJob`

**Files:** `main.go:120` 附近

- [ ] **Step 1: 找到既有任务启动点**

```
grep -n "StartSubscriptionQuotaResetTask\|service\.Start" main.go
```
Expected: line 120 附近有 `service.StartSubscriptionQuotaResetTask()`

- [ ] **Step 2: 在其后追加**

```go
service.StartSubscriptionQuotaResetTask()
service.StartBillingJob()
```

- [ ] **Step 3: 启动自检（手动）**

```
go build -o /tmp/new-api . && /tmp/new-api --port 0 2>&1 | head -50
```
Expected：日志中出现 "billing job ticker started"（沿用 LogInfo 风格）；无 panic。

- [ ] **Step 4: Commit**

```
git commit -am "feat(billing): register StartBillingJob in main.go"
```

---

## Phase 4: 查询 API（6 个端点 + 权限分流）

> **关键编译时保证（AC-7）：** 用户视图响应 struct 与管理员视图响应 struct 分离，确保用户接口编译时即不暴露 cost/profit 字段（design.md §5.9）。

### Task 4.1: service/billing_query.go — 用户日账单聚合

**Files:** `service/billing_query.go`、`service/billing_query_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestQueryUserDailyBill_Aggregates(t *testing.T) {
    setupTestDB(t)
    seed := []model.BillDailyFull{
        {StatDate: "2026-05-19", UserId: 1, UserGroup: "g1", ChannelId: 1, ModelName: "m", RequestCount: 10, RevenueQuota: 100},
        {StatDate: "2026-05-19", UserId: 1, UserGroup: "g1", ChannelId: 2, ModelName: "m", RequestCount: 5,  RevenueQuota: 50},
        {StatDate: "2026-05-19", UserId: 2, UserGroup: "g2", ChannelId: 1, ModelName: "m", RequestCount: 1,  RevenueQuota: 9},
    }
    for _, r := range seed { model.DB.Create(&r) }
    rows, err := QueryUserDailyBill(1, "2026-05-01", "2026-05-31")
    assert.NoError(t, err)
    assert.Equal(t, 1, len(rows)) // 同 user_group + 同 model + 同 stat_date 聚合成 1 行（跨 channel 折叠）
    assert.Equal(t, int64(15), rows[0].RequestCount)
    assert.Equal(t, int64(150), rows[0].RevenueQuota)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestQueryUserDailyBill_ -v
```

- [ ] **Step 3: 实现**

```go
// service/billing_query.go
package service

import "github.com/QuantumNous/new-api/model"

// UserDailyBillRow — 用户视图（编译时无 cost / profit）
type UserDailyBillRow struct {
    StatDate         string `json:"stat_date"`
    UserId           int    `json:"user_id"`
    UserGroup        string `json:"user_group"`
    ModelName        string `json:"model_name"`
    RequestCount     int64  `json:"request_count"`
    PromptTokens     int64  `json:"prompt_tokens"`
    CompletionTokens int64  `json:"completion_tokens"`
    RevenueQuota     int64  `json:"revenue_quota"`
}

func QueryUserDailyBill(userId int, startDate, endDate string) ([]UserDailyBillRow, error) {
    var rows []UserDailyBillRow
    err := model.DB.Table("bill_daily_full").
        Select("stat_date, user_id, user_group, model_name, " +
               "SUM(request_count) AS request_count, " +
               "SUM(prompt_tokens) AS prompt_tokens, " +
               "SUM(completion_tokens) AS completion_tokens, " +
               "SUM(revenue_quota) AS revenue_quota").
        Where("user_id = ? AND stat_date BETWEEN ? AND ?", userId, startDate, endDate).
        Group("stat_date, user_id, user_group, model_name").
        Order("stat_date DESC, model_name").
        Scan(&rows).Error
    return rows, err
}
```

- [ ] **Step 4: 跑测试**

```
go test ./service/ -run TestQueryUserDailyBill_ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git add service/billing_query.go service/billing_query_test.go
git commit -m "feat(billing): add QueryUserDailyBill returning cost-free user view"
```

---

### Task 4.2: 用户月账单 + 管理员变体

**Files:** 同上

- [ ] **Step 1: 写失败测试 — AC-2 月账单 = SUM(日)**

```go
func TestQueryUserMonthlyBill_EqualsSumOfDaily(t *testing.T) {
    setupTestDB(t)
    for d := 1; d <= 5; d++ {
        model.DB.Create(&model.BillDailyFull{
            StatDate: fmt.Sprintf("2026-05-%02d", d),
            UserId:   1, UserGroup: "g", ChannelId: 1, ModelName: "m",
            RequestCount: int64(d), RevenueQuota: int64(d * 10),
        })
    }
    monthly, _ := QueryUserMonthlyBill(1, "2026-05")
    var daily int64
    for _, r := range monthly { daily += r.RevenueQuota }
    assert.Equal(t, int64(10+20+30+40+50), daily)
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestQueryUserMonthlyBill_ -v
```

- [ ] **Step 3: 实现**

```go
// 月账单 = 同 service 文件，按 stat_date LIKE 'YYYY-MM-%' GROUP BY user_group + model_name
func QueryUserMonthlyBill(userId int, month string) ([]UserDailyBillRow, error) {
    var rows []UserDailyBillRow
    err := model.DB.Table("bill_daily_full").
        Select("? AS stat_date, user_id, user_group, model_name, "+
               "SUM(request_count) AS request_count, "+
               "SUM(prompt_tokens) AS prompt_tokens, "+
               "SUM(completion_tokens) AS completion_tokens, "+
               "SUM(revenue_quota) AS revenue_quota", month).
        Where("user_id = ? AND stat_date LIKE ?", userId, month+"-%").
        Group("user_id, user_group, model_name").
        Scan(&rows).Error
    return rows, err
}

// 管理员视图（含 cost/profit）— 与用户视图字段不同 struct
type AdminUserBillRow struct {
    UserDailyBillRow
    CostQuota    int64 `json:"cost_quota"`
    ProfitQuota  int64 `json:"profit_quota"`
    CostMissing  bool  `json:"cost_missing"`
}

func QueryAdminUserBill(userId int, startDate, endDate string) ([]AdminUserBillRow, error) {
    var rows []AdminUserBillRow
    // MAX(cost_missing) 三库通用替代 BOOL_OR；SQLite/MySQL 中 bool 存为 int → MAX 返回 1/0；PG 中 bool MAX 仍返回 true/false
    err := model.DB.Table("bill_daily_full").
        Select("stat_date, user_id, user_group, model_name, " +
               "SUM(request_count) AS request_count, " +
               "SUM(prompt_tokens) AS prompt_tokens, " +
               "SUM(completion_tokens) AS completion_tokens, " +
               "SUM(revenue_quota) AS revenue_quota, " +
               "SUM(cost_quota) AS cost_quota, " +
               "SUM(profit_quota) AS profit_quota, " +
               "MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing").
        Where("user_id = ? AND stat_date BETWEEN ? AND ?", userId, startDate, endDate).
        Group("stat_date, user_id, user_group, model_name").
        Order("stat_date DESC, model_name").
        Scan(&rows).Error
    return rows, err
}

func QueryAdminUserMonthlyBill(userId int, month string) ([]AdminUserBillRow, error) {
    var rows []AdminUserBillRow
    err := model.DB.Table("bill_daily_full").
        Select("? AS stat_date, user_id, user_group, model_name, "+
               "SUM(request_count) AS request_count, "+
               "SUM(prompt_tokens) AS prompt_tokens, "+
               "SUM(completion_tokens) AS completion_tokens, "+
               "SUM(revenue_quota) AS revenue_quota, "+
               "SUM(cost_quota) AS cost_quota, "+
               "SUM(profit_quota) AS profit_quota, "+
               "MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing", month).
        Where("user_id = ? AND stat_date LIKE ?", userId, month+"-%").
        Group("user_id, user_group, model_name").
        Scan(&rows).Error
    return rows, err
}
```

- [ ] **Step 4: 跑测试**

```
go test ./service/ -run "TestQueryUserMonthlyBill_|TestQueryAdminUserBill_" -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```
git commit -am "feat(billing): add monthly/admin user bill queries"
```

---

### Task 4.3: 渠道账单 daily / monthly（service + controller）

**Files:** `service/billing_query.go`、`controller/billing_controller.go`

- [ ] **Step 1: 写失败测试**

```go
func TestQueryChannelDailyBill_Aggregates(t *testing.T) {
    setupTestDB(t)
    model.DB.Create(&model.BillDailyFull{StatDate: "2026-05-19", UserId: 1, ChannelId: 7, ChannelGroup: "gA", ModelName: "m", RequestCount: 3, RevenueQuota: 30, CostQuota: 18, ProfitQuota: 12})
    rows, _ := QueryChannelDailyBill(7, "2026-05-01", "2026-05-31", "")
    assert.Equal(t, 1, len(rows))
    assert.Equal(t, int64(12), rows[0].ProfitQuota)
}
```

- [ ] **Step 2: 验证失败 + 实现 + 跑测试 + Commit**

```
go test ./service/ -run TestQueryChannelDailyBill_ -v   # FAIL
# 实现 QueryChannelDailyBill / QueryChannelMonthlyBill + controller 端点 + 路由
go test ./service/ ./controller/ -run "ChannelDaily|ChannelMonthly" -v   # PASS
git commit -am "feat(billing): add channel daily/monthly bill query endpoints"
```

实现需点（不允许占位符）：
- Struct `ChannelBillRow`：`stat_date, channel_id, channel_group, model_name, request_count, revenue_quota, cost_quota, profit_quota, cost_missing`
- controller `GetChannelBillDaily(c *gin.Context)`：从 query 取 `channel_id / start / end / channel_group`；channel_id 必填
- 路由：`apiAdmin.Group("/billing/admin/channel").GET("/daily", controller.GetChannelBillDaily)` / `.GET("/monthly", ...)`

---

### Task 4.4: 全流程账单 daily / monthly + 分页

**Files:** 同上

- [ ] **Step 1: 写失败测试 — 含分页 + 多维筛选**

```go
func TestQueryFullDailyBill_FiltersAndPagination(t *testing.T) {
    setupTestDB(t)
    for i := 0; i < 50; i++ {
        model.DB.Create(&model.BillDailyFull{
            StatDate: "2026-05-19", UserId: i+1, ChannelId: (i%5)+1,
            ChannelGroup: []string{"gA","gB"}[i%2], ModelName: "m",
            RequestCount: 1, RevenueQuota: int64(i+1),
        })
    }
    rows, total, _ := QueryFullDailyBill(QueryFullParams{Start:"2026-05-19", End:"2026-05-19", ChannelGroup:"gA", Limit:10, Offset:0})
    assert.Equal(t, 10, len(rows))
    assert.Equal(t, int64(25), total) // 50 行中 channel_group=gA 占一半
}
```

- [ ] **Step 2: 验证失败 + 实现 + 跑测试 + Commit**

```
go test ./service/ -run TestQueryFullDailyBill_ -v
# 实现：
#   type QueryFullParams struct { Start, End string; UserId, ChannelId int; ChannelGroup string; Limit, Offset int }
#   func QueryFullDailyBill(p QueryFullParams) ([]AdminFullBillRow, int64, error) { ... 含 COUNT(*) for total ... }
# controller：
#   GET /api/billing/admin/full/daily?start=&end=&user_id=&channel_id=&channel_group=&limit=&offset=
#   GET /api/billing/admin/full/monthly?month=YYYY-MM&...
go test ./controller/ -run TestGetFullBill -v
git commit -am "feat(billing): add full bill daily/monthly endpoints with filter+pagination"
```

实现注意：
- `Limit` 默认 100，最大 10000（design.md §5.8.1）；超出截断为 10000
- 排序：`stat_date DESC, user_id, channel_id, model_name`

---

### Task 4.5: 用户端 daily / monthly API（强制 user_id 来自 session）

**Files:** `controller/billing_controller.go`

- [ ] **Step 1: 写失败测试 — 用户传 user_id 参数应被忽略**

```go
func TestGetUserBillDaily_IgnoresQueryUserId(t *testing.T) {
    r := setupTestRouter(t)
    seed := &model.BillDailyFull{StatDate:"2026-05-19", UserId: 7, UserGroup:"g", ChannelId:1, ModelName:"m", RevenueQuota:100}
    model.DB.Create(seed)
    // 登录用户 id=7；尝试在 query 中指定 user_id=8 → 仍只返回 user 7 的数据
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/billing/user/daily?start=2026-05-01&end=2026-05-31&user_id=8", nil)
    setSessionUserId(req, 7)
    setUserAuth(req, "user")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    body := w.Body.String()
    assert.Contains(t, body, `"revenue_quota":100`)
    assert.NotContains(t, body, `cost_quota`)  // AC-7 用户视图无 cost
}
```

- [ ] **Step 2: 验证失败 + 实现**

```go
func GetUserBillDaily(c *gin.Context) {
    userId := c.GetInt("id") // 项目既有 session userId 读取方式（参考其他 controller）
    if userId <= 0 { c.JSON(401, gin.H{"success": false}); return }
    rows, err := service.QueryUserDailyBill(userId, c.Query("start"), c.Query("end"))
    if err != nil { common.ApiError(c, err); return }
    c.JSON(200, gin.H{"success": true, "data": rows})
}

// 路由
apiUser := apiV1.Group("/billing/user", middleware.UserAuth())
apiUser.GET("/daily",   controller.GetUserBillDaily)
apiUser.GET("/monthly", controller.GetUserBillMonthly)
```

- [ ] **Step 3: 验证 AC-7（响应 struct 编译时无 cost）**

```
grep -n "CostQuota\|cost_quota" service/billing_query.go | grep -i UserDailyBillRow
```
Expected: 无输出（UserDailyBillRow 不包含 CostQuota 字段）

- [ ] **Step 4: 跑测试 + Commit**

```
go test ./controller/ -run TestGetUserBillDaily_ -v
git commit -am "feat(billing): add user-side daily/monthly endpoints with session-bound user_id"
```

---

## Phase 5: CSV 导出（AC-6）

### Task 5.1: 流式 CSV 输出（UTF-8 with BOM）

**Files:** `service/billing_csv.go`、`service/billing_csv_test.go`、`controller/billing_controller.go`

- [ ] **Step 1: 写失败测试**

```go
func TestCsvExport_HasBomAndCorrectHeaders(t *testing.T) {
    setupTestDB(t)
    model.DB.Create(&model.BillDailyFull{StatDate:"2026-05-19", UserId:1, ChannelId:1, ModelName:"m", RequestCount:1, RevenueQuota:5})
    r := setupTestRouter(t)
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/billing/export.csv?scope=admin_full&start=2026-05-01&end=2026-05-31", nil)
    setUserAuth(req, "admin")
    r.ServeHTTP(w, req)
    assert.Equal(t, 200, w.Code)
    body := w.Body.Bytes()
    assert.Equal(t, []byte{0xEF, 0xBB, 0xBF}, body[:3]) // BOM
    assert.Contains(t, string(body), "stat_date,user_id,user_group,channel_id,channel_group,model_name,request_count,revenue_quota,cost_quota,profit_quota")
}
```

- [ ] **Step 2: 验证失败**

```
go test ./service/ -run TestCsvExport_ -v
```

- [ ] **Step 3: 实现 — 流式写**

```go
// service/billing_csv.go
package service

import (
    "encoding/csv"
    "fmt"
    "io"
    "strconv"
)

const (
    csvBatchSize  = 1000
    csvMaxRows    = 1_000_000
    csvUtf8Bom    = "\xEF\xBB\xBF"
)

type CsvScope string

const (
    ScopeUserDaily      CsvScope = "user_daily"
    ScopeUserMonthly    CsvScope = "user_monthly"
    ScopeAdminChannel   CsvScope = "admin_channel"
    ScopeAdminFull      CsvScope = "admin_full"
)

func ExportBillCsv(w io.Writer, scope CsvScope, params QueryFullParams) error {
    // 写 BOM
    if _, err := w.Write([]byte(csvUtf8Bom)); err != nil { return err }
    cw := csv.NewWriter(w)
    cw.UseCRLF = true
    defer cw.Flush()

    header, rowGen, err := newCsvSource(scope, params)
    if err != nil { return err }
    if err := cw.Write(header); err != nil { return err }

    total := 0
    for {
        batch, more, err := rowGen()
        if err != nil { return err }
        for _, row := range batch {
            if total >= csvMaxRows {
                return fmt.Errorf("export exceeds %d rows; narrow your range", csvMaxRows)
            }
            if err := cw.Write(row); err != nil { return err }
            total++
        }
        cw.Flush()
        if err := cw.Error(); err != nil { return err }
        if !more { break }
    }
    return nil
}

// newCsvSource returns (header, rowGenerator) for the given scope.
// rowGenerator returns (batch, hasMore, err). hasMore=false ⇒ exhausted.
func newCsvSource(scope CsvScope, p QueryFullParams) ([]string, func() ([][]string, bool, error), error) {
    switch scope {
    case ScopeAdminFull:
        header := []string{"stat_date","user_id","user_group","channel_id","channel_group","model_name","request_count","revenue_quota","cost_quota","profit_quota"}
        offset := 0
        return header, func() ([][]string, bool, error) {
            page := p; page.Limit = csvBatchSize; page.Offset = offset
            rows, _, err := QueryFullDailyBill(page)
            if err != nil { return nil, false, err }
            out := make([][]string, 0, len(rows))
            for _, r := range rows {
                out = append(out, []string{
                    r.StatDate, strconv.Itoa(r.UserId), r.UserGroup,
                    strconv.Itoa(r.ChannelId), r.ChannelGroup, r.ModelName,
                    strconv.FormatInt(r.RequestCount, 10), strconv.FormatInt(r.RevenueQuota, 10),
                    strconv.FormatInt(r.CostQuota, 10),    strconv.FormatInt(r.ProfitQuota, 10),
                })
            }
            offset += len(rows)
            return out, len(rows) == csvBatchSize, nil
        }, nil
    case ScopeUserDaily:
        header := []string{"stat_date","user_id","user_group","model_name","request_count","prompt_tokens","completion_tokens","revenue_quota"}
        offset := 0
        return header, func() ([][]string, bool, error) {
            rows, err := QueryUserDailyBill(p.UserId, p.Start, p.End)
            if err != nil { return nil, false, err }
            // QueryUserDailyBill 一次返回全量，不分页；超过 csvBatchSize 时切片返回
            start := offset
            end := start + csvBatchSize
            if end > len(rows) { end = len(rows) }
            page := rows[start:end]
            out := make([][]string, 0, len(page))
            for _, r := range page {
                out = append(out, []string{
                    r.StatDate, strconv.Itoa(r.UserId), r.UserGroup, r.ModelName,
                    strconv.FormatInt(r.RequestCount, 10),
                    strconv.FormatInt(r.PromptTokens, 10),
                    strconv.FormatInt(r.CompletionTokens, 10),
                    strconv.FormatInt(r.RevenueQuota, 10),
                })
            }
            offset = end
            return out, end < len(rows), nil
        }, nil
    case ScopeUserMonthly:
        header := []string{"stat_month","user_id","user_group","model_name","request_count","prompt_tokens","completion_tokens","revenue_quota"}
        emitted := false
        return header, func() ([][]string, bool, error) {
            if emitted { return nil, false, nil }
            emitted = true
            rows, err := QueryUserMonthlyBill(p.UserId, p.Month)
            if err != nil { return nil, false, err }
            out := make([][]string, 0, len(rows))
            for _, r := range rows {
                out = append(out, []string{
                    r.StatDate, strconv.Itoa(r.UserId), r.UserGroup, r.ModelName,
                    strconv.FormatInt(r.RequestCount, 10),
                    strconv.FormatInt(r.PromptTokens, 10),
                    strconv.FormatInt(r.CompletionTokens, 10),
                    strconv.FormatInt(r.RevenueQuota, 10),
                })
            }
            return out, false, nil
        }, nil
    case ScopeAdminChannel:
        header := []string{"stat_date","channel_id","channel_group","model_name","request_count","revenue_quota","cost_quota","profit_quota","cost_missing"}
        emitted := false
        return header, func() ([][]string, bool, error) {
            if emitted { return nil, false, nil }
            emitted = true
            rows, err := QueryChannelDailyBill(p.ChannelId, p.Start, p.End, p.ChannelGroup)
            if err != nil { return nil, false, err }
            out := make([][]string, 0, len(rows))
            for _, r := range rows {
                missing := "0"; if r.CostMissing { missing = "1" }
                out = append(out, []string{
                    r.StatDate, strconv.Itoa(r.ChannelId), r.ChannelGroup, r.ModelName,
                    strconv.FormatInt(r.RequestCount, 10),
                    strconv.FormatInt(r.RevenueQuota, 10),
                    strconv.FormatInt(r.CostQuota, 10),
                    strconv.FormatInt(r.ProfitQuota, 10),
                    missing,
                })
            }
            return out, false, nil
        }, nil
    }
    return nil, nil, fmt.Errorf("unknown scope %q", scope)
}
```

> 同时移除原 `> 对四个 scope 都要落实完整 generator` 备注 — 三个 case 已全部实现，无遗留。`csvMaxRows=1_000_000` 与 `csvBatchSize=1000` 防止内存爆。`QueryFullParams` 在 Task 4.4 已定义，本任务为其新增 `UserId int` / `Month string` 字段以支持 user/monthly scope（向后兼容：admin_full 路径不读这两个字段）。

- [ ] **Step 4: controller 端 + 路由**

```go
func ExportBillCsv(c *gin.Context) {
    scope := service.CsvScope(c.Query("scope"))
    isAdminScope := scope == service.ScopeAdminFull || scope == service.ScopeAdminChannel
    if isAdminScope {
        // 由 AdminAuth middleware 保证，已挂在 apiAdmin 组内
    } else {
        // 用户 scope：强制 user_id 来自 session
    }
    c.Header("Content-Type", "text/csv; charset=utf-8")
    c.Header("Content-Disposition", `attachment; filename="billing_`+string(scope)+`.csv"`)
    params := buildCsvParams(c, scope)
    if err := service.ExportBillCsv(c.Writer, scope, params); err != nil {
        // 已开始写 → 不能再 c.JSON；仅日志
        logger.LogError(c, "csv export failed: "+err.Error())
    }
}

// 路由：admin scope 挂 apiAdmin；user scope 挂 apiUser
apiAdmin.GET("/billing/export.csv",  controller.ExportBillCsv)
apiUser.GET("/billing/export.csv",   controller.ExportBillCsv)
```

- [ ] **Step 5: 跑测试 + Commit**

```
go test ./service/ ./controller/ -run TestCsvExport_ -v
git commit -am "feat(billing): add streaming CSV export with UTF-8 BOM (AC-6)"
```

---

## Phase 6: APM 接入（OpenTelemetry + Prometheus）

> 可与 Phase 1–5 并行。本阶段不引入对 ChannelPricing / BillDailyFull 的依赖。

### Task 6.1: 依赖 + SDK 初始化

**Files:** `go.mod`、`common/otel.go`（新建）、`main.go`

- [ ] **Step 1: 添加依赖**

```
go get go.opentelemetry.io/otel \
       go.opentelemetry.io/otel/sdk \
       go.opentelemetry.io/otel/exporters/prometheus \
       go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin \
       go.opentelemetry.io/contrib/instrumentation/gorm.io/gorm/otelgorm
go mod tidy
```

- [ ] **Step 2: 实现 common/otel.go**

```go
package common

import (
    "context"
    "net/http"

    "github.com/prometheus/client_golang/prometheus/promhttp"
    "go.opentelemetry.io/otel/exporters/prometheus"
    "go.opentelemetry.io/otel/sdk/metric"
)

var OtelMeterProvider *metric.MeterProvider

func InitOtel(ctx context.Context, metricsAddr string) error {
    exp, err := prometheus.New()
    if err != nil { return err }
    OtelMeterProvider = metric.NewMeterProvider(metric.WithReader(exp))
    go func() {
        mux := http.NewServeMux()
        mux.Handle("/metrics", promhttp.Handler())
        _ = http.ListenAndServe(metricsAddr, mux) // :9464 默认
    }()
    return nil
}
```

- [ ] **Step 3: 在 main.go 启动时调用 + 注册 middleware**

```go
// main.go
if err := common.InitOtel(context.Background(), ":9464"); err != nil {
    logger.LogWarn(context.Background(), "otel init failed: "+err.Error())
}
// router 初始化处
router.Use(otelgin.Middleware("new-api"))
// model/main.go DB 初始化处
DB.Use(otelgorm.NewPlugin())
LOG_DB.Use(otelgorm.NewPlugin())
```

- [ ] **Step 4: 验证 /metrics 端点**

```
go build -o /tmp/new-api . && /tmp/new-api --port 0 &
sleep 2
curl -s localhost:9464/metrics | head -20
```
Expected: 出现 `go_gc_duration_seconds` / `process_*` 等基础指标。

- [ ] **Step 5: Commit**

```
git add go.mod go.sum common/otel.go main.go router/api-router.go model/main.go
git commit -m "feat(billing): integrate OpenTelemetry + Prometheus /metrics endpoint"
```

---

### Task 6.2: 业务 span（账单 controller）

**Files:** `controller/billing_controller.go`

- [ ] **Step 1: 加 span（5 个关键路径）**

```go
import "go.opentelemetry.io/otel"
var billingTracer = otel.Tracer("billing")

func GetUserBillDaily(c *gin.Context) {
    ctx, span := billingTracer.Start(c.Request.Context(), "GetUserBillDaily")
    defer span.End()
    c.Request = c.Request.WithContext(ctx)

    userId := c.GetInt("id")
    if userId <= 0 { c.JSON(401, gin.H{"success": false}); return }
    rows, err := service.QueryUserDailyBill(userId, c.Query("start"), c.Query("end"))
    if err != nil { common.ApiError(c, err); return }
    c.JSON(200, gin.H{"success": true, "data": rows})
}
```
对 `GetUserBillDaily / GetChannelBillDaily / GetFullBillDaily / ExportBillCsv / RerunBillingJob` 5 处加 span（其余复用 otelgin 自动埋点）。

- [ ] **Step 2: 跑既有 controller 测试**

```
go test ./controller/ -v
```
Expected: 全部 PASS（不应被埋点改变行为）

- [ ] **Step 3: Commit**

```
git commit -am "feat(billing): add manual OTel spans on key billing endpoints"
```

---

### Task 6.3: Grafana 仪表盘模板

**Files:** `docs/grafana/billing-dashboard.json`（新建）

- [ ] **Step 1: 创建仪表盘 JSON**

```
docs/grafana/billing-dashboard.json
```

内容包含 5 个 panel：
1. `bill_query_p95`: `histogram_quantile(0.95, rate(http_server_duration_seconds_bucket{route=~"/api/billing/.*"}[5m]))`
2. `bill_query_qps`: `rate(http_server_request_count_total{route=~"/api/billing/.*"}[1m])`
3. `billing_job_duration`: `billing_job_run_finished_at - billing_job_run_started_at`（自定义 metric，见下方 Task 6.4）
4. `billing_job_failures`: `sum(increase(billing_job_failure_total[1h]))`
5. `channel_pricing_missing_count`: `bill_daily_full_cost_missing_count`

写完整 JSON（panel 定义、targets、布局），不允许占位。

- [ ] **Step 2: Commit**

```
git add docs/grafana/billing-dashboard.json
git commit -m "docs(billing): add Grafana dashboard template for billing observability"
```

---

### Task 6.4: 业务 metrics（job / 缺失计数）

**Files:** `service/billing_metrics.go`、`service/billing_job.go`（少量修改）

- [ ] **Step 1: 写失败测试**

```go
func TestBillingMetrics_JobFailureIncrements(t *testing.T) {
    initBillingMetrics()
    before := readCounter(billingJobFailureCounter)
    incrementJobFailure("test")
    after := readCounter(billingJobFailureCounter)
    assert.Equal(t, before+1, after)
}
```

- [ ] **Step 2: 验证失败 + 实现**

```go
// service/billing_metrics.go
package service

import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/metric"
)

var (
    billingJobFailureCounter metric.Int64Counter
    billingCostMissingCounter metric.Int64Counter
)

func initBillingMetrics() {
    meter := otel.Meter("billing")
    billingJobFailureCounter, _ = meter.Int64Counter("billing_job_failure_total")
    billingCostMissingCounter, _ = meter.Int64Counter("bill_daily_full_cost_missing_count")
}

func incrementJobFailure(date string) {
    if billingJobFailureCounter != nil {
        billingJobFailureCounter.Add(context.Background(), 1)
    }
}
```

修改 `billing_job.go::finishJobFailed` 调用 `incrementJobFailure(r.StatDate)`；在 `runDailyFullJob` 写入 cost_missing=true 的行时调用 `billingCostMissingCounter.Add(ctx, 1)`。

- [ ] **Step 3: 跑测试 + 整体回归**

```
go test ./service/ -v
```
Expected: 全部 PASS

- [ ] **Step 4: Commit**

```
git commit -am "feat(billing): emit billing_job_failure_total + cost_missing_count metrics"
```

---

## Phase 7: 前端（web/default/）

> 仅 `web/default/`（React 19 + Base UI + Tailwind + Rsbuild）；classic 主题 → v1.1。所有命令用 `bun`。

### Task 7.1: 目录骨架 + API 客户端

**Files:**
- Create: `web/default/src/features/billing/lib/billingApi.ts`
- Create: `web/default/src/features/billing/types.ts`

- [ ] **Step 1: 写失败测试（Vitest）**

```ts
// web/default/src/features/billing/lib/billingApi.test.ts
import { describe, it, expect, vi } from 'vitest';
import { fetchUserBillDaily } from './billingApi';

describe('billingApi', () => {
  it('GET /api/billing/user/daily with date range', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ success: true, data: [] }) });
    global.fetch = fetchMock as any;
    await fetchUserBillDaily({ start: '2026-05-01', end: '2026-05-31' });
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/api/billing/user/daily?start=2026-05-01&end=2026-05-31'), expect.anything());
  });
});
```

- [ ] **Step 2: 验证失败**

```
cd web/default && bun test billing
```
Expected: FAIL — 找不到 billingApi

- [ ] **Step 3: 实现**

```ts
// web/default/src/features/billing/types.ts
export interface UserBillRow {
  stat_date: string;
  user_id: number;
  user_group: string;
  model_name: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  revenue_quota: number;
}

export interface AdminBillRow extends UserBillRow {
  channel_id?: number;
  channel_group?: string;
  cost_quota: number;
  profit_quota: number;
  cost_missing: boolean;
}

// web/default/src/features/billing/lib/billingApi.ts
import { apiClient } from '@/lib/apiClient'; // 复用既有 client；按项目实际路径调整

export async function fetchUserBillDaily(params: { start: string; end: string }) {
  return apiClient.get<{ data: UserBillRow[] }>(`/api/billing/user/daily?start=${params.start}&end=${params.end}`);
}
export async function fetchUserBillMonthly(month: string) {
  return apiClient.get<{ data: UserBillRow[] }>(`/api/billing/user/monthly?month=${month}`);
}
export async function fetchChannelBill(params: { channel_id: number; start: string; end: string; channel_group?: string }) {
  return apiClient.get<{ data: AdminBillRow[] }>(`/api/billing/admin/channel/daily?` + new URLSearchParams(params as any));
}
export async function fetchFullBill(params: { start: string; end: string; user_id?: number; channel_id?: number; channel_group?: string; limit?: number; offset?: number }) {
  return apiClient.get<{ data: AdminBillRow[]; total: number }>(`/api/billing/admin/full/daily?` + new URLSearchParams(params as any));
}
// channel pricing CRUD
export async function listChannelPricing(channelId: number) {
  return apiClient.get<{ data: AdminBillRow[] }>(`/api/billing/channel-pricing?channel_id=${channelId}`);
}
export async function createChannelPricing(body: { channel_id: number; model_name: string; cost_expr: string; currency?: string; remark?: string }) {
  return apiClient.post<{ data: any }>(`/api/billing/channel-pricing`, body);
}
export async function updateChannelPricing(id: number, body: { channel_id: number; model_name: string; cost_expr: string; currency?: string; remark?: string }) {
  return apiClient.put<{ data: any }>(`/api/billing/channel-pricing/${id}`, body);
}
export async function deleteChannelPricing(id: number) {
  return apiClient.delete<{ success: boolean }>(`/api/billing/channel-pricing/${id}`);
}
// job 操作
export async function listJobRuns(params: { start?: string; end?: string }) {
  const qs = new URLSearchParams(params as any).toString();
  return apiClient.get<{ data: any[] }>(`/api/billing/admin/job/runs?${qs}`);
}
export async function rerunJob(stat_date: string) {
  return apiClient.post<{ success: boolean; affected: number }>(`/api/billing/admin/job/rerun`, { stat_date });
}
export async function backfillJob(start_date: string, end_date: string) {
  return apiClient.post<{ success: boolean; days_scheduled: number }>(`/api/billing/admin/job/backfill`, { start_date, end_date });
}
```

- [ ] **Step 4: 跑测试 + Commit**

```
cd web/default && bun test billing
```
Expected: PASS

```
git add web/default/src/features/billing/
git commit -m "feat(billing/web): scaffold billing feature module with API client"
```

---

### Task 7.2: 通用组件（GranularitySwitch / DateRangePicker / BillTable / CsvExportButton）

**Files:** `web/default/src/features/billing/components/*.tsx`

- [ ] **Step 1: 写组件测试（React Testing Library）**

```tsx
// BillTable.test.tsx — 验证管理员视图与用户视图字段差异
it('hides cost/profit columns for user role', () => {
  const { queryByText } = render(<BillTable rows={[{ ...userRow }]} role="user" />);
  expect(queryByText('cost_quota')).toBeNull();
  expect(queryByText('profit_quota')).toBeNull();
});
```

- [ ] **Step 2: 验证失败 + 实现 4 个组件**

逐个实现：
- `GranularitySwitch.tsx`：日 / 月 toggle，输出受控 `granularity: 'daily' | 'monthly'`
- `BillDateRangePicker.tsx`：daily 模式输出 `{ start, end }`；monthly 模式输出 `{ month: 'YYYY-MM' }`
- `BillTable.tsx`：列定义按 `role` 切换；空态展示；分页（前端基于 limit/offset 调 hook 拉数据）
- `CsvExportButton.tsx`：拼接 `/api/billing/export.csv?scope=...&...` 触发浏览器下载

每个组件完整 TSX（无占位）：props 类型、Base UI 组件、Tailwind 样式、i18n key 全部定义；新 i18n key 同步加入 `web/default/src/i18n/locales/zh.json` 与 `en.json`。

- [ ] **Step 3: 跑测试 + Commit**

```
cd web/default && bun test components/
git add web/default/src/features/billing/components/ web/default/src/i18n/locales/
git commit -m "feat(billing/web): add shared bill components with role-aware columns (AC-7)"
```

---

### Task 7.3: UserBillPage（普通用户与管理员双模）

**Files:** `web/default/src/features/billing/routes/UserBillPage.tsx`

- [ ] **Step 1: 写测试 — 用户与管理员视图字段差异**

```tsx
it('admin sees cost columns; user does not', () => { /* mock useAuth + render */ });
```

- [ ] **Step 2: 验证失败 + 实现**

完整页面：包含 Date Range / Granularity 控件、数据表（依赖 `useBillData` hook）、错误态、空态、loading skeleton、CsvExportButton。

- [ ] **Step 3: 跑测试 + Commit**

```
cd web/default && bun test UserBillPage
git commit -am "feat(billing/web): add UserBillPage with dual user/admin modes"
```

---

### Task 7.4: ChannelBillPage（管理员）

**Files:** `routes/ChannelBillPage.tsx`

- [ ] **Step 1-5: TDD 流程同 7.3**

实现要点：渠道下拉选择器（拉 `/api/channel/list`）+ 日 / 月切换 + 利润率列（`profit / revenue`）+ 导出按钮。

```
git commit -am "feat(billing/web): add ChannelBillPage with profit margin column"
```

---

### Task 7.5: FullBillPage（全流程账单 + 多维筛选）

**Files:** `routes/FullBillPage.tsx`

- [ ] **Step 1-5: TDD 流程同 7.3**

实现要点：5 个筛选器（日期范围 / 用户 / 渠道 / 渠道分组 / 模型）+ 分页表格 + ⚠️ cost_missing 行红色标记 + 导出。

```
git commit -am "feat(billing/web): add FullBillPage with multi-filter + cost_missing badge"
```

---

### Task 7.6: ChannelPricingPage（成本定价 CRUD UI）

**Files:** `routes/ChannelPricingPage.tsx`、`components/PricingEditor.tsx`

- [ ] **Step 1-5: TDD 流程**

要点：
- 表单字段：channel + model + cost_expr（textarea，提示语法）+ currency + remark
- 客户端先用一个 minimal validator 拦明显错（如空字符串），最终校验靠后端 `billingexpr.Compile`
- 错误回显 inline
- 列表表格 + 行内编辑 / 删除

```
git commit -am "feat(billing/web): add ChannelPricingPage for cost expression CRUD"
```

---

### Task 7.7: 路由注册 + 菜单按 role 控制

**Files:** `web/default/src/router/index.tsx`、菜单配置文件（按既有结构）

- [ ] **Step 1: 注册路由**

```tsx
// router/index.tsx
{
  path: '/billing/me',          element: <UserBillPage role="self" />,
}, {
  path: '/billing/admin/users',     element: <AdminGuard><UserBillPage role="admin" /></AdminGuard>,
}, {
  path: '/billing/admin/channels',  element: <AdminGuard><ChannelBillPage /></AdminGuard>,
}, {
  path: '/billing/admin/full',      element: <AdminGuard><FullBillPage /></AdminGuard>,
}, {
  path: '/billing/admin/pricing',   element: <AdminGuard><ChannelPricingPage /></AdminGuard>,
},
```

- [ ] **Step 2: 菜单**

在既有 sidebar / topbar 配置（按 `web/default/src/components/Sidebar` 既有约定）按 `role` 渲染：用户角色仅显示「我的账单」；管理员显示完整 5 项。

- [ ] **Step 3: 跑前端集成测试 + 类型检查**

```
cd web/default && bun run build
cd web/default && bun test
```
Expected: build 成功；测试全部 PASS。

- [ ] **Step 4: Commit**

```
git add web/default/src/router/ web/default/src/components/Sidebar/
git commit -m "feat(billing/web): register billing routes and role-gated menu items"
```

---

## Phase 8: 测试加固

### Task 8.1: 三库迁移端到端验证

**Files:** `model/billing_migration_test.go`（新建）

- [ ] **Step 1: 写测试 — 在 SQLite/MySQL/PG 环境分别跑**

```go
//go:build integration

func TestBillingMigration_AllThreeDBs(t *testing.T) {
    cases := []struct{ Name, DSN string }{
        {"sqlite", "file::memory:?cache=shared"},
        {"mysql",  os.Getenv("TEST_MYSQL_DSN")},
        {"postgres", os.Getenv("TEST_PG_DSN")},
    }
    for _, tc := range cases {
        t.Run(tc.Name, func(t *testing.T) {
            if tc.DSN == "" { t.Skip("DSN not set") }
            // 初始化 DB → 跑 AutoMigrate → 断言表存在 → 检查唯一索引名
        })
    }
}
```

- [ ] **Step 2-4: 实现 + 跑 + Commit**

```
TEST_MYSQL_DSN=... TEST_PG_DSN=... go test -tags integration ./model/ -run TestBillingMigration_AllThreeDBs -v
git commit -am "test(billing): add 3-DB migration integration test"
```

---

### Task 8.2: AC-1 ~ AC-8 集成测试矩阵

**Files:** `test/integration/billing_ac_test.go`（新建）

- [ ] **Step 1: 写测试 — 对每个 AC 一个测试用例**

```go
func TestAC1_UserDailyBill(t *testing.T) {
    // Given: U1 在 2026-05-19 调用 M1 100 次, Quota=1000
    setupTestDB(t)
    for i := 0; i < 100; i++ {
        model.LOG_DB.Create(&model.Log{
            UserId: 1, ChannelId: 1, ModelName: "M1", Group: "g",
            PromptTokens: 10, CompletionTokens: 0, Quota: 10,
            CreatedAt: dayStartTs("2026-05-19") + int64(i),
        })
    }
    model.DB.Create(&model.ChannelPricing{ChannelId: 1, ModelName: "M1", CostExpr: "v1: p", Status: 1})
    runDailyFullJob("2026-05-19", &model.BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full"})
    rows, _ := service.QueryUserDailyBill(1, "2026-05-19", "2026-05-19")
    var totalReq, totalRevenue int64
    for _, r := range rows { totalReq += r.RequestCount; totalRevenue += r.RevenueQuota }
    assert.Equal(t, int64(100), totalReq)
    assert.Equal(t, int64(1000), totalRevenue)
}

func TestAC2_MonthlyEqualsSumOfDaily(t *testing.T) {
    setupTestDB(t)
    var dailySum int64
    for d := 1; d <= 5; d++ {
        rev := int64(d * 100)
        model.DB.Create(&model.BillDailyFull{
            StatDate: fmt.Sprintf("2026-05-%02d", d),
            UserId: 1, UserGroup: "g", ChannelId: 1, ModelName: "M1",
            RequestCount: 1, RevenueQuota: rev,
        })
        dailySum += rev
    }
    monthly, _ := service.QueryUserMonthlyBill(1, "2026-05")
    var monthlySum int64
    for _, r := range monthly { monthlySum += r.RevenueQuota }
    assert.Equal(t, dailySum, monthlySum)
}

func TestAC3_CostCalculation(t *testing.T) {
    setupTestDB(t)
    model.LOG_DB.Create(&model.Log{
        UserId: 1, ChannelId: 1, ModelName: "M1", Group: "g",
        PromptTokens: 5000, CompletionTokens: 0, Quota: 100,
        CreatedAt: dayStartTs("2026-05-19"),
    })
    model.DB.Create(&model.ChannelPricing{ChannelId: 1, ModelName: "M1", CostExpr: "v1: p * 0.001", Status: 1})
    runDailyFullJob("2026-05-19", &model.BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full"})
    var bdf model.BillDailyFull
    model.DB.Where("stat_date='2026-05-19' AND channel_id=1").First(&bdf)
    assert.Equal(t, int64(5), bdf.CostQuota) // 5000 * 0.001 = 5
    assert.Equal(t, int64(100), bdf.RevenueQuota)
    assert.Equal(t, int64(95), bdf.ProfitQuota)
    assert.False(t, bdf.CostMissing)
}

func TestAC4_CostMissingFallback(t *testing.T) {
    setupTestDB(t)
    model.LOG_DB.Create(&model.Log{UserId: 1, ChannelId: 2, ModelName: "Mx", Quota: 50,
        CreatedAt: dayStartTs("2026-05-19")})
    runDailyFullJob("2026-05-19", &model.BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full"})
    var bdf model.BillDailyFull
    model.DB.Where("channel_id=2").First(&bdf)
    assert.True(t, bdf.CostMissing)
    assert.Equal(t, int64(0), bdf.CostQuota)
    assert.Equal(t, int64(50), bdf.ProfitQuota) // revenue - 0
}

func TestAC5_AdminFilterCorrectness(t *testing.T) {
    setupTestDB(t)
    model.DB.Create(&model.BillDailyFull{StatDate: "2026-05-15", ChannelGroup: "gA", ChannelId: 1, ModelName: "m", RevenueQuota: 100})
    model.DB.Create(&model.BillDailyFull{StatDate: "2026-05-15", ChannelGroup: "gB", ChannelId: 2, ModelName: "m", RevenueQuota: 200})
    rows, _, _ := service.QueryFullDailyBill(service.QueryFullParams{Start: "2026-05-01", End: "2026-05-31", ChannelGroup: "gA"})
    assert.Equal(t, 1, len(rows))
    assert.Equal(t, "gA", rows[0].ChannelGroup)
}

func TestAC6_CsvBomAndHeaders(t *testing.T) {
    setupTestDB(t)
    model.DB.Create(&model.BillDailyFull{StatDate: "2026-05-19", UserId: 1, ChannelId: 1, ModelName: "m", RevenueQuota: 10})
    var buf bytes.Buffer
    err := service.ExportBillCsv(&buf, service.ScopeAdminFull, service.QueryFullParams{Start: "2026-05-01", End: "2026-05-31"})
    assert.NoError(t, err)
    body := buf.Bytes()
    assert.Equal(t, []byte{0xEF, 0xBB, 0xBF}, body[:3])
    assert.Contains(t, string(body), "stat_date,user_id,user_group,channel_id,channel_group,model_name,request_count,revenue_quota,cost_quota,profit_quota")
}

func TestAC7_UserCannotAccessAdminEndpoint(t *testing.T) {
    r := setupTestRouter(t)
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/billing/admin/channel/daily?channel_id=1&start=2026-05-01&end=2026-05-31", nil)
    setUserAuth(req, "user")
    r.ServeHTTP(w, req)
    assert.Equal(t, 403, w.Code)
}

func TestAC8_DataAccuracy(t *testing.T) {
    setupTestDB(t)
    var logRevenue int64
    for i := 0; i < 100; i++ {
        q := int64(7 + i)
        model.LOG_DB.Create(&model.Log{UserId: 1, ChannelId: 1, ModelName: "m", Quota: int(q),
            CreatedAt: dayStartTs("2026-05-19") + int64(i)})
        logRevenue += q
    }
    runDailyFullJob("2026-05-19", &model.BillingJobRun{StatDate: "2026-05-19", JobKind: "daily_full"})
    var bdfRevenue int64
    model.DB.Model(&model.BillDailyFull{}).Where("stat_date='2026-05-19'").
        Select("COALESCE(SUM(revenue_quota),0)").Row().Scan(&bdfRevenue)
    diffRate := math.Abs(float64(bdfRevenue-logRevenue)) / float64(logRevenue)
    assert.Less(t, diffRate, 0.001, "AC-8 准确性: diff rate %f >= 0.1%%", diffRate)
}

// 辅助：把 'YYYY-MM-DD' 转 Asia/Shanghai 当日 00:00 的 Unix 秒
func dayStartTs(d string) int64 {
    loc, _ := time.LoadLocation("Asia/Shanghai")
    t, _ := time.ParseInLocation("2006-01-02", d, loc)
    return t.Unix()
}
```

- [ ] **Step 2-4: 实现 + 跑 + Commit**

```
go test -tags integration ./test/integration/ -run "TestAC[1-8]_" -v
git commit -am "test(billing): integration tests for AC-1..AC-8"
```

---

### Task 8.3: job 幂等 + 失败重试测试

**Files:** `service/billing_job_test.go`

- [ ] **Step 1: 写测试**

```go
func TestJob_RerunPreservesIdempotency(t *testing.T) {
    setupTestDB(t)
    yest := YesterdayInShanghai(time.Now())
    model.LOG_DB.Create(&model.Log{UserId: 1, ChannelId: 1, ModelName: "m", Quota: 50,
        CreatedAt: dayStartTs(yest)})
    rec := &model.BillingJobRun{StatDate: yest, JobKind: "daily_full"}
    runDailyFullJob(yest, rec)
    runDailyFullJob(yest, rec) // 第二次跑
    var rows []model.BillDailyFull
    model.DB.Where("stat_date = ?", yest).Find(&rows)
    assert.Equal(t, 1, len(rows))
    assert.Equal(t, int64(50), rows[0].RevenueQuota)
}

func TestJob_AttemptLimit_StopsAtFive(t *testing.T) {
    setupTestDB(t)
    yest := YesterdayInShanghai(time.Now())
    // 写一条已 attempt=5, status=failed 的记录
    model.DB.Create(&model.BillingJobRun{StatDate: yest, JobKind: "daily_full",
        Status: "failed", Attempt: 5, ErrorMsg: "x"})
    runBillingJobIfDue() // 应直接 skip
    var r model.BillingJobRun
    model.DB.Where("stat_date=?", yest).First(&r)
    assert.Equal(t, 5, r.Attempt, "attempt 不应被再 +1")
    assert.Equal(t, "failed", r.Status)
}

func TestJob_BackfillProcessesAscending(t *testing.T) {
    setupTestDB(t)
    for _, d := range []string{"2026-05-17", "2026-05-15", "2026-05-16"} {
        model.DB.Create(&model.BillingJobRun{StatDate: d, JobKind: "backfill", Status: "pending"})
    }
    // 调用 backfill 扫描入口（在 runBillingJobIfDue 内部，需测试单独走 service 函数 RunBackfillBatch）
    service.RunBackfillBatch()
    var dates []string
    model.DB.Model(&model.BillingJobRun{}).Where("job_kind='backfill' AND status='success'").
        Order("finished_at asc").Pluck("stat_date", &dates)
    assert.Equal(t, []string{"2026-05-15", "2026-05-16", "2026-05-17"}, dates,
        "backfill 应按 stat_date 升序处理")
}
```

> 备注：`RunBackfillBatch` 是 Task 3.5 实现 backfill 调度时新增的 service-level 函数（封装"找 pending → runDailyFullJob(kind='backfill') → 标记 success"循环），用于让测试避开 ticker 等待。Task 3.5 实现完成后此处直接可用。

- [ ] **Step 2-4: 实现 + 跑 + Commit**

```
go test ./service/ -run "TestJob_" -v
git commit -am "test(billing): idempotency + retry limit + backfill ordering tests"
```

---

### Task 8.4: billingexpr 兼容测试

**Files:** `service/billing_calc_test.go`

- [ ] **Step 1: 写测试 — 用既有 ratio_setting 中的复杂表达式**

```go
func TestCalcCost_CompatibleWithExistingPricingExprs(t *testing.T) {
    exprs := []string{
        "v1: p * 0.5 + c * 1.5",
        "v1: tier(p+c, 0:0.001, 10000:0.0008)",
        "v1: hour(\"Asia/Shanghai\") < 8 ? p*0.4+c*1.2 : p*0.5+c*1.5",
    }
    for _, e := range exprs {
        _, err := billingexpr.Compile(e); assert.NoError(t, err, e)
    }
}
```

- [ ] **Step 2-4: 跑 + Commit**

```
go test ./service/ -run TestCalcCost_Compatible -v
git commit -am "test(billing): cost expression compatibility with existing ratio_setting forms"
```

---

## Phase 9: 压测 + 上线前 SOP

### Task 9.1: 数据生成脚本（100 万行）

**Files:** `scripts/billing_seed.go`、`scripts/billing_seed.sh`

- [ ] **Step 1: 写脚本**

```go
// scripts/billing_seed.go — 生成 100 万行 bill_daily_full：
// 365 天 × ~2740 用户/渠道/模型组合 ≈ 100 万行
// 用 batch insert（1000 行/批）
```

- [ ] **Step 2: 执行**

```
go run scripts/billing_seed.go --dsn $TEST_SQLITE_DSN --rows 1000000
go run scripts/billing_seed.go --dsn $TEST_MYSQL_DSN  --rows 1000000
go run scripts/billing_seed.go --dsn $TEST_PG_DSN     --rows 1000000
```

- [ ] **Step 3: Commit**

```
git add scripts/billing_seed.*
git commit -m "test(billing): add seed script for 1M-row performance dataset"
```

---

### Task 9.2: P95 压测 + 验收门

**Files:** `scripts/billing_load_test.sh`、`docs/operations/billing-sop.md`

- [ ] **Step 1: 写压测脚本（vegeta / hey）**

```bash
# scripts/billing_load_test.sh
hey -n 1000 -c 20 -H "Authorization: Bearer $ADMIN_TOKEN" \
    "http://localhost:3000/api/billing/admin/full/daily?start=2026-04-01&end=2026-04-30"
```

- [ ] **Step 2: 三库分别跑，记录 P95**

记录到 `docs/operations/billing-sop.md`：每库一节，附原始 P50 / P90 / P95 / P99 + 通过 / 失败结论。

- [ ] **Step 3: 验收门**

```
P95 (SQLite)     ≤ 800ms ?
P95 (MySQL)      ≤ 800ms ?
P95 (PostgreSQL) ≤ 800ms ?
```
Expected: 三库全部 PASS（design.md §3 承诺 < 500ms，留 60% 余量）。

- [ ] **Step 4: 若不达标 → 提调优 issue（不放行 QA）**

按 design.md §6.1 R5：跑 `EXPLAIN`，确认索引命中；如 MySQL 走错索引 → 加 `FORCE INDEX(idx_bdf_user_date)`；如 PG → 分析 `pg_stat_user_indexes`。

- [ ] **Step 5: Commit**

```
git add scripts/billing_load_test.sh docs/operations/billing-sop.md
git commit -m "test(billing): P95 load test scripts + sop benchmark records"
```

---

### Task 9.3: Dockerfile tzdata 验证（design.md §5.6 R3）

**Files:** `Dockerfile`（如 alpine 基础镜像缺 tzdata → 补 `apk add tzdata`）

- [ ] **Step 1: 检查既有 Dockerfile**

```
grep -in "alpine\|tzdata" Dockerfile
```

- [ ] **Step 2: 如缺失 → 补充**

```dockerfile
RUN apk add --no-cache tzdata
ENV TZ=Asia/Shanghai
```

- [ ] **Step 3: 构建 + 启动镜像 + 自检**

```
docker build -t new-api-tz-check .
docker run --rm new-api-tz-check sh -c 'date; ls /usr/share/zoneinfo/Asia/Shanghai'
```
Expected: 文件存在；时区正确。

- [ ] **Step 4: Commit**

```
git commit -am "fix(billing): ensure tzdata + Asia/Shanghai TZ in container image"
```

---

### Task 9.4: 上线 SOP 文档

**Files:** `docs/operations/billing-sop.md`

- [ ] **Step 1: 完整 SOP 内容**

```markdown
# 账单模块上线 SOP

## 一、上线前 1 周（运营协调）
1. 完成对账时长基线测量：随机抽 3 名财务人员实测当前对账耗时（手工 SQL 路径），记录 P50 → PRD §11.2 #1 数据闭环。
2. 完成存量 channel_pricing 录入（近 30 天 Top 20 模型 × Top 5 渠道，覆盖 ≥ 90% 流水）→ PRD §11.2 #7。
3. 完成 Grafana 实例部署 / 复用决策（D-4 默认新建）。

## 二、上线当日
1. 三库迁移自检（按部署 DB 类型跑一次 AutoMigrate）
2. 启动 StartBillingJob（main.go 已注册，自动启动）
3. 02:00 Asia/Shanghai 等待首日 T+1 job 完成 → 看 /api/billing/admin/job/runs
4. 抽样 3 个用户 + 3 个渠道，肉眼对账：bill_daily_full vs logs

## 三、上线后第 1 周
1. 每日检查 channel_pricing_missing_count；缺失渠道运营补录
2. 上线后 7 天首次跑准确性自检（v1.1 自动；v1.0 临时用 SQL 手动）
3. P95 监控：超 800ms 立刻分析索引命中

## 四、回退
1. 设置 `BillingJobEnabled=false`（option）→ ticker 暂停
2. 前端隐藏菜单（环境变量或 option）
3. bill_daily_full / channel_pricing 数据保留，不影响计费扣款
```

- [ ] **Step 2: Commit**

```
git add docs/operations/billing-sop.md
git commit -m "docs(billing): add on-launch SOP and rollback procedure"
```

---

## Phase 10: 运营 / 跨角色协调（PJM 跟踪，**非代码任务**）

> 本阶段任务由 PJM 在任务面板单独跟踪，不阻塞代码合并；但是 v1.0 上线前必须完成 10.1 与 10.2。

### Task 10.1: 对账时长基线测量

- **责任人**：运营（PJM 协调；squad 内无运营角色 → PJM 出面对接业务侧运营 / 财务）
- **执行**：上线前 1 周内，抽样 3 人 × 各完成 1 次"当前手工 SQL 对账 → 输出 CSV"，记录 P50 时长（分钟）
- **交付**：在 issue [TES-28] 评论中提交基线数据；写入 PRD v1.2（如需）
- **验证**：PJM 在任务面板标记完成 + 附评论链接

### Task 10.2: channel_pricing 存量数据录入

- **责任人**：运营（PJM 协调）
- **范围**：近 30 天调用量 Top 20 模型 × Top 5 渠道（覆盖 ≥ 90% 流水）
- **数据源优先级**：① 现有供应商合同 / 采购单（财务侧） → ② `ratio_setting` 售价倍率作为清单参照 → ③ 公开价目页（OpenAI / Anthropic / Azure / 通义 / 智谱）
- **工期**：1–2 人天（数据收集 + CSV 录入 + 抽检 5 行准确性）
- **执行节点**：后端 `channel_pricing` CRUD API 上线后（Phase 2 完成）、T+1 job 首次跑通前（Phase 3 + Task 7.6 前端 CRUD UI 上线后即可）
- **验证**：PJM 抽样 5 条记录 vs 供应商合同核对

### Task 10.3: LOG_DB 部署形态确认（D-2 默认假设可分离）

- **责任人**：运维 / DBA（PJM 协调）
- **决策路径**：若运维确认生产 LOG_DB 与主 DB 物理分离 → 在 Phase 9.2 压测中**重点观察跨库读取耗时**；若合一 → 允许后续优化（非阻塞 v1.0）
- **执行节点**：plan.md 启动后 ~ Phase 9 之前

### Task 10.4: Grafana 实例（D-4 默认新建）

- **责任人**：运维
- **执行节点**：Phase 6 上线前
- **回退**：若发现既有团队共享实例 → 复用（-0.5 人天），不影响代码工期

---

## 最终验收门（HARD-GATE，dev 通过两轮代码审查 + QA 验收后才能放行推送）

### 后端验收清单

- [ ] 所有 Phase 1–6 + 8–9 单元 / 集成测试 PASS（`go test ./... -v`）
- [ ] `go vet ./...` / `golangci-lint run`（项目既有规则）无新增告警
- [ ] 三库（SQLite/MySQL/PG）迁移与压测全 PASS（P95 < 800ms）
- [ ] AC-1 ~ AC-8 集成测试全 PASS
- [ ] job 单日重跑幂等（DELETE + 重写）验证通过
- [ ] Dockerfile tzdata 验证通过
- [ ] `/metrics` 端点返回 200 + 业务 metric 已注册
- [ ] 已规避所有 `// TODO` / 占位符（`grep -rn "TODO\|FIXME" --include="*.go" service/ controller/ model/`）

### 前端验收清单

- [ ] `cd web/default && bun run build` 成功
- [ ] `cd web/default && bun test` 全 PASS
- [ ] 用户视图编译时无 cost_quota / profit_quota 字段（grep 自验）
- [ ] 5 个页面（UserBill / ChannelBill / FullBill / ChannelPricing / JobRuns）手动 smoke 通过
- [ ] i18n zh / en 两侧 key 完整（`bun run i18n:sync` 无 diff）

### 运营 / 协调清单（Phase 10）

- [ ] 对账时长基线已测量并归档
- [ ] 存量 channel_pricing 已录入（Top 20 × Top 5）
- [ ] LOG_DB / Grafana 部署形态已确认

### 推送 + 合并决策

- 完成上述清单后 dev 调用 `superpowers:finishing-a-development-branch` 呈现 4 选项给 PJM
- PJM 汇总决策摘要 → 提交项目负责人确认主干合并
- 文档维护专家在合并后同步 README / API doc / CHANGELOG

---

## 附录 A：关键路径回顾（design.md §7）

```
Task 1.1 → 1.2 → 1.3 → 1.4          [Phase 1 数据层]
   │
   ▼
Task 2.* (CRUD)                     [Phase 2 — Phase 3 不依赖；可并行]
   │
   ▼ (Phase 3 需要 Phase 1 表 + Task 3.1 calc)
Task 3.1 → 3.2 → 3.3 → 3.4 → 3.5 → 3.6 → 3.7       [Phase 3 job]
   │
   ▼ (Phase 4 不依赖 Phase 3，仅依赖 Phase 1；可并行启动)
Task 4.1 → 4.2 → 4.3 → 4.4 → 4.5    [Phase 4 查询]
   │
   ▼
Task 5.* (CSV)                       [Phase 5]
   │
   ▼ (Phase 7 前端依赖 Phase 2/4/5 接口形态)
Task 7.1 → 7.2 → 7.3 → 7.4 → 7.5 → 7.6 → 7.7
   │
   ▼
Phase 8 测试加固 + Phase 9 压测 + Phase 10 运营协调
   │
   ▼
最终验收门 → finishing-a-development-branch
```

Phase 6（APM）可与 1–5 任意时机并行。

---

## 附录 B：风险登记（design.md §6.1 → 本 plan 应对）


| # | 风险（design.md） | 本 plan 对应任务 |
| --- | --- | --- |
| R1 LOG_DB 跨库延迟 | Task 3.3 批量 5000 + Task 9.2 压测 |
| R2 channel_pricing 大面积缺失 | Task 10.2 录入 + AC-4 兜底测试（8.2） + Task 6.4 缺失计数 metric |
| R3 Docker tzdata 缺失 | Task 9.3 |
| R4 logs 历史字段缺失 | AC-4 兜底（8.2）+ Task 3.3 user_group 空字符串容错 |
| R5 索引走错 | Task 9.2 EXPLAIN |
| R6 job 失败 ≥ 3 天 | Task 3.4 手动 rerun + Task 6.4 失败 metric |
| R7 多节点重复执行 | Task 1.3 UNIQUE 约束 + 3.2 IsMasterNode guard |
| R8 billingexpr 编译错 | Task 2.2 / 2.3 Compile 校验 + 8.4 兼容测试 |
| R9 CSV 过大 | Task 5.1 流式 + csvMaxRows=1M |
| R10 GDPR | PRD §11.1 #4 锁定中国大陆，不在本 plan 范围 |

---

## 附录 C：Phase 10 跟踪项（PRD v1.1 §11.2 同步）


| # | 事项 | 责任方 | 时间节点 | 验证标准 |
| --- | --- | --- | --- | --- |
| 1 | 对账时长基线 ≥ 60min 实测 | 运营 + PJM | 上线前 1 周 | 给出 P50 数值（分钟） |
| 7 | channel_pricing 存量录入 | 运营 + PJM | CRUD API 上线后 ~ T+1 job 首跑前 | Top 20 模型 × Top 5 渠道 ≥ 90% 流水覆盖 |
| 8 | LOG_DB 是否分离 | DBA / 运维 + PJM | plan.md 启动后 ~ Phase 9 前 | 二选一确认；分离则压测重点观察跨库延迟 |
