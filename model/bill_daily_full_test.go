package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBillDailyFull_UniqueConstraint(t *testing.T) {
	truncateTables(t)

	r1 := &BillDailyFull{
		StatDate:     "2026-05-19",
		UserId:       1,
		ChannelId:    1,
		ModelName:    "gpt-4o",
		RevenueQuota: 100,
		CostQuota:    60,
		ProfitQuota:  40,
	}
	assert.NoError(t, DB.Create(r1).Error)

	r2 := &BillDailyFull{
		StatDate:     "2026-05-19",
		UserId:       1,
		ChannelId:    1,
		ModelName:    "gpt-4o",
		RevenueQuota: 999,
	}
	err := DB.Create(r2).Error
	assert.Error(t, err, "expected UNIQUE(stat_date, user_id, channel_id, model_name)")
}

func TestBillDailyFull_QueryByUserDate(t *testing.T) {
	truncateTables(t)

	rows := []*BillDailyFull{
		{StatDate: "2026-05-19", UserId: 1, ChannelId: 1, ModelName: "m1", RevenueQuota: 50},
		{StatDate: "2026-05-20", UserId: 1, ChannelId: 2, ModelName: "m2", RevenueQuota: 70},
		{StatDate: "2026-04-30", UserId: 1, ChannelId: 1, ModelName: "m1", RevenueQuota: 999},
		{StatDate: "2026-05-19", UserId: 2, ChannelId: 1, ModelName: "m1", RevenueQuota: 999},
	}
	for _, r := range rows {
		assert.NoError(t, DB.Create(r).Error)
	}

	var got []BillDailyFull
	assert.NoError(t, DB.Where("user_id = ? AND stat_date BETWEEN ? AND ?", 1, "2026-05-01", "2026-05-31").Find(&got).Error)
	assert.Equal(t, 2, len(got))
}

func TestBillDailyFull_QueryByChannelDate(t *testing.T) {
	truncateTables(t)

	rows := []*BillDailyFull{
		{StatDate: "2026-05-19", UserId: 1, ChannelId: 7, ModelName: "m", CostQuota: 10},
		{StatDate: "2026-05-19", UserId: 2, ChannelId: 7, ModelName: "m", CostQuota: 20},
		{StatDate: "2026-05-19", UserId: 3, ChannelId: 8, ModelName: "m", CostQuota: 99},
	}
	for _, r := range rows {
		assert.NoError(t, DB.Create(r).Error)
	}

	var got []BillDailyFull
	assert.NoError(t, DB.Where("channel_id = ? AND stat_date = ?", 7, "2026-05-19").Find(&got).Error)
	assert.Equal(t, 2, len(got))
}

func TestBillDailyFull_CostMissingFlagPersists(t *testing.T) {
	truncateTables(t)

	row := &BillDailyFull{
		StatDate:     "2026-05-19",
		UserId:       1,
		ChannelId:    1,
		ModelName:    "no-price-model",
		RevenueQuota: 100,
		CostMissing:  true,
	}
	assert.NoError(t, DB.Create(row).Error)

	var got BillDailyFull
	assert.NoError(t, DB.First(&got, row.Id).Error)
	assert.True(t, got.CostMissing)
}
