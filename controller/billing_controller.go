package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
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
