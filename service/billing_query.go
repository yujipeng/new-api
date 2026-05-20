package service

import (
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// UserDailyBillRow is the user-facing view — compile-time guarantee (AC-7):
// no cost/profit fields are present, so even a JSON marshal of the struct
// will never expose them to a non-admin caller.
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

type AdminUserBillRow struct {
	UserDailyBillRow
	CostQuota   int64 `json:"cost_quota"`
	ProfitQuota int64 `json:"profit_quota"`
	CostMissing int   `json:"cost_missing"`
}

type ChannelBillRow struct {
	StatDate     string `json:"stat_date"`
	ChannelId    int    `json:"channel_id"`
	ChannelGroup string `json:"channel_group"`
	ModelName    string `json:"model_name"`
	RequestCount int64  `json:"request_count"`
	RevenueQuota int64  `json:"revenue_quota"`
	CostQuota    int64  `json:"cost_quota"`
	ProfitQuota  int64  `json:"profit_quota"`
	CostMissing  int    `json:"cost_missing"`
}

type AdminFullBillRow struct {
	StatDate         string `json:"stat_date"`
	UserId           int    `json:"user_id"`
	UserGroup        string `json:"user_group"`
	ChannelId        int    `json:"channel_id"`
	ChannelGroup     string `json:"channel_group"`
	ModelName        string `json:"model_name"`
	RequestCount     int64  `json:"request_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	RevenueQuota     int64  `json:"revenue_quota"`
	CostQuota        int64  `json:"cost_quota"`
	ProfitQuota      int64  `json:"profit_quota"`
	CostMissing      bool   `json:"cost_missing"`
}

type QueryFullParams struct {
	Start        string
	End          string
	UserId       int
	ChannelId    int
	ChannelGroup string
	UserGroup    string
	ModelName    string
	Limit        int
	Offset       int
}

const (
	fullQueryDefaultLimit = 100
	fullQueryMaxLimit     = 10000
)

func clampLimit(limit int) int {
	if limit <= 0 {
		return fullQueryDefaultLimit
	}
	if limit > fullQueryMaxLimit {
		return fullQueryMaxLimit
	}
	return limit
}

// --- User-facing aggregations (no cost / profit exposure) ---

func QueryUserDailyBill(userId int, startDate, endDate string) ([]UserDailyBillRow, error) {
	var rows []UserDailyBillRow
	err := model.DB.Table("bill_daily_fulls").
		Select(`stat_date, user_id, user_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(prompt_tokens) AS prompt_tokens,
		        SUM(completion_tokens) AS completion_tokens,
		        SUM(revenue_quota) AS revenue_quota`).
		Where("user_id = ? AND stat_date BETWEEN ? AND ?", userId, startDate, endDate).
		Group("stat_date, user_id, user_group, model_name").
		Order("stat_date DESC, model_name").
		Scan(&rows).Error
	return rows, err
}

func QueryUserMonthlyBill(userId int, month string) ([]UserDailyBillRow, error) {
	var rows []UserDailyBillRow
	err := model.DB.Table("bill_daily_fulls").
		Select(`? AS stat_date, user_id, user_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(prompt_tokens) AS prompt_tokens,
		        SUM(completion_tokens) AS completion_tokens,
		        SUM(revenue_quota) AS revenue_quota`, month).
		Where("user_id = ? AND stat_date LIKE ?", userId, month+"-%").
		Group("user_id, user_group, model_name").
		Order("model_name").
		Scan(&rows).Error
	return rows, err
}

// --- Admin variants (include cost / profit) ---
// MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) is cross-DB safe (SQLite/MySQL
// store bool as int, PostgreSQL evaluates the CASE to int as well).

func QueryAdminUserBill(userId int, startDate, endDate string) ([]AdminUserBillRow, error) {
	var rows []AdminUserBillRow
	err := model.DB.Table("bill_daily_fulls").
		Select(`stat_date, user_id, user_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(prompt_tokens) AS prompt_tokens,
		        SUM(completion_tokens) AS completion_tokens,
		        SUM(revenue_quota) AS revenue_quota,
		        SUM(cost_quota) AS cost_quota,
		        SUM(profit_quota) AS profit_quota,
		        MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing`).
		Where("user_id = ? AND stat_date BETWEEN ? AND ?", userId, startDate, endDate).
		Group("stat_date, user_id, user_group, model_name").
		Order("stat_date DESC, model_name").
		Scan(&rows).Error
	return rows, err
}

func QueryAdminUserMonthlyBill(userId int, month string) ([]AdminUserBillRow, error) {
	var rows []AdminUserBillRow
	err := model.DB.Table("bill_daily_fulls").
		Select(`? AS stat_date, user_id, user_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(prompt_tokens) AS prompt_tokens,
		        SUM(completion_tokens) AS completion_tokens,
		        SUM(revenue_quota) AS revenue_quota,
		        SUM(cost_quota) AS cost_quota,
		        SUM(profit_quota) AS profit_quota,
		        MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing`, month).
		Where("user_id = ? AND stat_date LIKE ?", userId, month+"-%").
		Group("user_id, user_group, model_name").
		Order("model_name").
		Scan(&rows).Error
	return rows, err
}

// --- Channel bills (admin) ---

func QueryChannelDailyBill(channelId int, startDate, endDate, channelGroup string) ([]ChannelBillRow, error) {
	var rows []ChannelBillRow
	q := model.DB.Table("bill_daily_fulls").
		Select(`stat_date, channel_id, channel_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(revenue_quota) AS revenue_quota,
		        SUM(cost_quota) AS cost_quota,
		        SUM(profit_quota) AS profit_quota,
		        MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing`).
		Where("channel_id = ? AND stat_date BETWEEN ? AND ?", channelId, startDate, endDate)
	if channelGroup != "" {
		q = q.Where("channel_group = ?", channelGroup)
	}
	err := q.Group("stat_date, channel_id, channel_group, model_name").
		Order("stat_date DESC, model_name").
		Scan(&rows).Error
	return rows, err
}

func QueryChannelMonthlyBill(channelId int, month, channelGroup string) ([]ChannelBillRow, error) {
	var rows []ChannelBillRow
	q := model.DB.Table("bill_daily_fulls").
		Select(`? AS stat_date, channel_id, channel_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(revenue_quota) AS revenue_quota,
		        SUM(cost_quota) AS cost_quota,
		        SUM(profit_quota) AS profit_quota,
		        MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing`, month).
		Where("channel_id = ? AND stat_date LIKE ?", channelId, month+"-%")
	if channelGroup != "" {
		q = q.Where("channel_group = ?", channelGroup)
	}
	err := q.Group("channel_id, channel_group, model_name").
		Order("model_name").
		Scan(&rows).Error
	return rows, err
}

// --- Full bill (admin) ---

func QueryFullDailyBill(p QueryFullParams) ([]AdminFullBillRow, int64, error) {
	limit := clampLimit(p.Limit)
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	base := model.DB.Table("bill_daily_fulls").
		Where("stat_date BETWEEN ? AND ?", p.Start, p.End)
	if p.UserId > 0 {
		base = base.Where("user_id = ?", p.UserId)
	}
	if p.ChannelId > 0 {
		base = base.Where("channel_id = ?", p.ChannelId)
	}
	if p.ChannelGroup != "" {
		base = base.Where("channel_group = ?", p.ChannelGroup)
	}
	if p.UserGroup != "" {
		base = base.Where("user_group = ?", p.UserGroup)
	}
	if p.ModelName != "" {
		base = base.Where("model_name = ?", p.ModelName)
	}

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []AdminFullBillRow
	err := base.Session(&gorm.Session{}).
		Order("stat_date DESC, user_id, channel_id, model_name").
		Limit(limit).
		Offset(offset).
		Scan(&rows).Error
	return rows, total, err
}

func QueryFullMonthlyBill(p QueryFullParams) ([]AdminFullBillRow, int64, error) {
	limit := clampLimit(p.Limit)
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	month := p.Start
	build := func() *gorm.DB {
		q := model.DB.Table("bill_daily_fulls").
			Where("stat_date LIKE ?", month+"-%")
		if p.UserId > 0 {
			q = q.Where("user_id = ?", p.UserId)
		}
		if p.ChannelId > 0 {
			q = q.Where("channel_id = ?", p.ChannelId)
		}
		if p.ChannelGroup != "" {
			q = q.Where("channel_group = ?", p.ChannelGroup)
		}
		if p.UserGroup != "" {
			q = q.Where("user_group = ?", p.UserGroup)
		}
		if p.ModelName != "" {
			q = q.Where("model_name = ?", p.ModelName)
		}
		return q
	}

	// total = number of unique (user, channel, model) groups in the month
	var total int64
	if err := build().
		Distinct("user_id, channel_id, model_name").
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []AdminFullBillRow
	err := build().
		Select(`? AS stat_date, user_id, user_group, channel_id, channel_group, model_name,
		        SUM(request_count) AS request_count,
		        SUM(prompt_tokens) AS prompt_tokens,
		        SUM(completion_tokens) AS completion_tokens,
		        SUM(revenue_quota) AS revenue_quota,
		        SUM(cost_quota) AS cost_quota,
		        SUM(profit_quota) AS profit_quota,
		        MAX(CASE WHEN cost_missing THEN 1 ELSE 0 END) AS cost_missing`, month).
		Group("user_id, user_group, channel_id, channel_group, model_name").
		Order("user_id, channel_id, model_name").
		Limit(limit).
		Offset(offset).
		Scan(&rows).Error
	return rows, total, err
}
