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
