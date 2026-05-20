package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBillingJobRun_UniqueByDateAndKind(t *testing.T) {
	truncateTables(t)

	r1 := &BillingJobRun{StatDate: "2026-05-19", JobKind: BillingJobKindDailyFull, Status: BillingJobStatusRunning}
	assert.NoError(t, DB.Create(r1).Error)

	r2 := &BillingJobRun{StatDate: "2026-05-19", JobKind: BillingJobKindDailyFull, Status: BillingJobStatusPending}
	assert.Error(t, DB.Create(r2).Error, "expected UNIQUE(stat_date, job_kind) violation for same kind+date")

	r3 := &BillingJobRun{StatDate: "2026-05-19", JobKind: BillingJobKindBackfill, Status: BillingJobStatusPending}
	assert.NoError(t, DB.Create(r3).Error, "different job_kind on same date must be allowed")

	r4 := &BillingJobRun{StatDate: "2026-05-20", JobKind: BillingJobKindDailyFull, Status: BillingJobStatusPending}
	assert.NoError(t, DB.Create(r4).Error, "different stat_date with same kind must be allowed")
}

func TestBillingJobRun_DefaultStatusPending(t *testing.T) {
	truncateTables(t)

	r := &BillingJobRun{StatDate: "2026-05-19", JobKind: BillingJobKindDailyFull}
	assert.NoError(t, DB.Create(r).Error)

	var got BillingJobRun
	assert.NoError(t, DB.First(&got, r.Id).Error)
	assert.Equal(t, BillingJobStatusPending, got.Status)
}

func TestBillingJobRun_MaxAttemptConstant(t *testing.T) {
	assert.GreaterOrEqual(t, BillingJobMaxAttempt, 3, "design §5.3 mandates at least 3 retries")
}
