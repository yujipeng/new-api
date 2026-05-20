package service

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
)

func CalcCostQuota(channelId int, modelName string, promptTokens, completionTokens int64) (int64, bool) {
	cp, err := model.GetChannelPricing(channelId, modelName)
	if err != nil || cp == nil || cp.CostExpr == "" {
		return 0, true
	}
	params := billingexpr.TokenParams{
		P: float64(promptTokens),
		C: float64(completionTokens),
	}
	cost, _, err := billingexpr.RunExpr(cp.CostExpr, params)
	if err != nil {
		return 0, true
	}
	return int64(cost + 0.5), false
}
