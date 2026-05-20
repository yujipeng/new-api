package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelPricing_CreateAndUniqueConstraint(t *testing.T) {
	truncateTables(t)

	cp1 := &ChannelPricing{
		ChannelId: 1,
		ModelName: "gpt-4o",
		CostExpr:  "v1: p * 0.5 + c * 1.5",
		Currency:  "USD",
		Status:    1,
	}
	assert.NoError(t, DB.Create(cp1).Error)

	cp2 := &ChannelPricing{
		ChannelId: 1,
		ModelName: "gpt-4o",
		CostExpr:  "v1: p * 0.4",
		Currency:  "USD",
		Status:    1,
	}
	err := DB.Create(cp2).Error
	assert.Error(t, err, "expected UNIQUE(channel_id, model_name) violation")
}

func TestGetChannelPricing_ReturnsNilWhenAbsent(t *testing.T) {
	truncateTables(t)

	cp, err := GetChannelPricing(999, "no-such-model")
	assert.NoError(t, err)
	assert.Nil(t, cp)
}

func TestGetChannelPricing_ReturnsActiveRow(t *testing.T) {
	truncateTables(t)

	row := &ChannelPricing{
		ChannelId: 7,
		ModelName: "gpt-4o-mini",
		CostExpr:  "v1: p * 0.15 + c * 0.6",
		Currency:  "USD",
		Status:    1,
	}
	assert.NoError(t, DB.Create(row).Error)

	cp, err := GetChannelPricing(7, "gpt-4o-mini")
	assert.NoError(t, err)
	if assert.NotNil(t, cp) {
		assert.Equal(t, "v1: p * 0.15 + c * 0.6", cp.CostExpr)
	}
}

func TestListChannelPricings_OrdersByModelName(t *testing.T) {
	truncateTables(t)

	rows := []*ChannelPricing{
		{ChannelId: 2, ModelName: "z-model", CostExpr: "v1: p", Currency: "USD", Status: 1},
		{ChannelId: 2, ModelName: "a-model", CostExpr: "v1: p", Currency: "USD", Status: 1},
		{ChannelId: 2, ModelName: "m-model", CostExpr: "v1: p", Currency: "USD", Status: 1},
	}
	for _, r := range rows {
		assert.NoError(t, DB.Create(r).Error)
	}

	list, err := ListChannelPricings(2)
	assert.NoError(t, err)
	if assert.Len(t, list, 3) {
		assert.Equal(t, "a-model", list[0].ModelName)
		assert.Equal(t, "m-model", list[1].ModelName)
		assert.Equal(t, "z-model", list[2].ModelName)
	}
}
