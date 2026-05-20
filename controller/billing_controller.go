package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/gin-gonic/gin"
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
