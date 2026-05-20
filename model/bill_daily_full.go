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
	GeneratedAt      int64  `json:"generated_at"      gorm:"autoCreateTime:milli"`
}
