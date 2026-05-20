package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

func ListChannelPricing(c *gin.Context) {
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	rows, err := model.ListChannelPricings(channelId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetChannelPricingById(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	var cp model.ChannelPricing
	if err := model.DB.First(&cp, id).Error; err != nil {
		common.ApiErrorMsg(c, "not found")
		return
	}
	common.ApiSuccess(c, cp)
}

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
		common.ApiErrorMsg(c, "invalid request: "+err.Error())
		return
	}
	if _, err := billingexpr.CompileFromCache(req.CostExpr); err != nil {
		common.ApiErrorMsg(c, "invalid cost_expr: "+err.Error())
		return
	}
	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}
	cp := &model.ChannelPricing{
		ChannelId: req.ChannelId,
		ModelName: req.ModelName,
		CostExpr:  req.CostExpr,
		Currency:  currency,
		Status:    1,
		Remark:    req.Remark,
	}
	if err := model.DB.Create(cp).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, cp)
}

type updateChannelPricingReq struct {
	CostExpr string `json:"cost_expr"`
	Currency string `json:"currency"`
	Remark   string `json:"remark"`
	Status   *int   `json:"status"`
}

func UpdateChannelPricing(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	var cp model.ChannelPricing
	if err := model.DB.First(&cp, id).Error; err != nil {
		common.ApiErrorMsg(c, "not found")
		return
	}
	var req updateChannelPricingReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "invalid request: "+err.Error())
		return
	}
	if req.CostExpr != "" {
		if _, err := billingexpr.CompileFromCache(req.CostExpr); err != nil {
			common.ApiErrorMsg(c, "invalid cost_expr: "+err.Error())
			return
		}
		cp.CostExpr = req.CostExpr
	}
	if req.Currency != "" {
		cp.Currency = req.Currency
	}
	if req.Remark != "" {
		cp.Remark = req.Remark
	}
	if req.Status != nil {
		cp.Status = *req.Status
	}
	if err := model.DB.Save(&cp).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, cp)
}

func DeleteChannelPricing(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	if err := model.DB.Delete(&model.ChannelPricing{}, id).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// --- Billing Job Admin Endpoints ---

type rerunJobReq struct {
	StatDate string `json:"stat_date" binding:"required"`
}

func RerunBillingJob(c *gin.Context) {
	var req rerunJobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	res := model.DB.Model(&model.BillingJobRun{}).
		Where("stat_date = ? AND job_kind = ?", req.StatDate, model.BillingJobKindDailyFull).
		Updates(map[string]any{"status": model.BillingJobStatusPending, "attempt": 0, "error_msg": ""})
	if res.Error != nil {
		common.ApiError(c, res.Error)
		return
	}
	common.ApiSuccess(c, gin.H{"affected": res.RowsAffected})
}

type backfillReq struct {
	StartDate string `json:"start_date" binding:"required"`
	EndDate   string `json:"end_date"   binding:"required"`
}

func BackfillBillingJob(c *gin.Context) {
	var req backfillReq
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	start, err1 := time.ParseInLocation("2006-01-02", req.StartDate, loc)
	end, err2 := time.ParseInLocation("2006-01-02", req.EndDate, loc)
	if err1 != nil || err2 != nil {
		common.ApiErrorMsg(c, "invalid date format, use YYYY-MM-DD")
		return
	}
	days := int(end.Sub(start).Hours()/24) + 1
	if days <= 0 || days > 90 {
		common.ApiErrorMsg(c, "range must be 1..90 days")
		return
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		rec := model.BillingJobRun{
			StatDate: d.Format("2006-01-02"),
			JobKind:  model.BillingJobKindBackfill,
			Status:   model.BillingJobStatusPending,
		}
		model.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&rec)
	}
	common.ApiSuccess(c, gin.H{"days_scheduled": days})
}

func ListBillingJobRuns(c *gin.Context) {
	start := c.Query("start")
	end := c.Query("end")
	q := model.DB.Model(&model.BillingJobRun{})
	if start != "" {
		q = q.Where("stat_date >= ?", start)
	}
	if end != "" {
		q = q.Where("stat_date <= ?", end)
	}
	var rows []model.BillingJobRun
	if err := q.Order("stat_date desc").Limit(500).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

// --- Bill Query Endpoints ---

// GetUserBillDaily — current user's daily bill rows.
// user_id is bound to the session; any query-string user_id is ignored (AC-7).
func GetUserBillDaily(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
		return
	}
	rows, err := service.QueryUserDailyBill(userId, c.Query("start"), c.Query("end"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetUserBillMonthly(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
		return
	}
	rows, err := service.QueryUserMonthlyBill(userId, c.Query("month"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

// GetAdminUserBill — admin can query any user's daily bill (includes cost/profit).
func GetAdminUserBillDaily(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "user_id required")
		return
	}
	rows, err := service.QueryAdminUserBill(userId, c.Query("start"), c.Query("end"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetAdminUserBillMonthly(c *gin.Context) {
	userId, _ := strconv.Atoi(c.Query("user_id"))
	if userId <= 0 {
		common.ApiErrorMsg(c, "user_id required")
		return
	}
	rows, err := service.QueryAdminUserMonthlyBill(userId, c.Query("month"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetChannelBillDaily(c *gin.Context) {
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	if channelId <= 0 {
		common.ApiErrorMsg(c, "channel_id required")
		return
	}
	rows, err := service.QueryChannelDailyBill(channelId, c.Query("start"), c.Query("end"), c.Query("channel_group"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func GetChannelBillMonthly(c *gin.Context) {
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	if channelId <= 0 {
		common.ApiErrorMsg(c, "channel_id required")
		return
	}
	rows, err := service.QueryChannelMonthlyBill(channelId, c.Query("month"), c.Query("channel_group"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, rows)
}

func parseFullParams(c *gin.Context) service.QueryFullParams {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	userId, _ := strconv.Atoi(c.Query("user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	return service.QueryFullParams{
		Start:        c.Query("start"),
		End:          c.Query("end"),
		UserId:       userId,
		ChannelId:    channelId,
		ChannelGroup: c.Query("channel_group"),
		UserGroup:    c.Query("user_group"),
		ModelName:    c.Query("model_name"),
		Limit:        limit,
		Offset:       offset,
	}
}

func GetFullBillDaily(c *gin.Context) {
	p := parseFullParams(c)
	rows, total, err := service.QueryFullDailyBill(p)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total})
}

func GetFullBillMonthly(c *gin.Context) {
	p := parseFullParams(c)
	p.Start = c.Query("month")
	rows, total, err := service.QueryFullMonthlyBill(p)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total})
}

// --- CSV export ---

func ExportBillCSV(c *gin.Context) {
	scope := c.Query("scope")
	if scope == "" {
		scope = service.CSVScopeAdminFull
	}
	if scope != service.CSVScopeUser {
		// Admin-only scopes — gate at handler since the route accepts both.
		role := c.GetInt("role")
		if role < common.RoleAdminUser {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "admin required for this scope"})
			return
		}
	}
	start := c.Query("start")
	end := c.Query("end")
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+service.CSVFilename(scope, start, end)+`"`)
	c.Header("Cache-Control", "no-store")

	var writeErr error
	switch scope {
	case service.CSVScopeUser:
		userId := c.GetInt("id")
		if userId <= 0 {
			c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
			return
		}
		rows, err := service.QueryUserDailyBill(userId, start, end)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		writeErr = service.WriteUserBillCSV(c.Writer, rows)
	case service.CSVScopeAdminUser:
		userId, _ := strconv.Atoi(c.Query("user_id"))
		if userId <= 0 {
			common.ApiErrorMsg(c, "user_id required")
			return
		}
		rows, err := service.QueryAdminUserBill(userId, start, end)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		writeErr = service.WriteAdminUserBillCSV(c.Writer, rows)
	case service.CSVScopeChannel:
		channelId, _ := strconv.Atoi(c.Query("channel_id"))
		if channelId <= 0 {
			common.ApiErrorMsg(c, "channel_id required")
			return
		}
		rows, err := service.QueryChannelDailyBill(channelId, start, end, c.Query("channel_group"))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		writeErr = service.WriteChannelBillCSV(c.Writer, rows)
	case service.CSVScopeAdminFull:
		p := parseFullParams(c)
		// CSV export ignores pagination; cap at max limit
		p.Limit = 10000
		p.Offset = 0
		rows, _, err := service.QueryFullDailyBill(p)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		writeErr = service.WriteAdminFullBillCSV(c.Writer, rows)
	default:
		common.ApiErrorMsg(c, "unknown scope: "+scope)
		return
	}

	if writeErr != nil {
		// Response may have been partially written — log only.
		_ = writeErr
	}
}
