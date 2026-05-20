package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm/clause"
)

const (
	billingJobTickInterval = 1 * time.Minute
	billingJobTimeZoneName = "Asia/Shanghai"
	billingJobCutoffHour   = 2
	billingJobBatchSize    = 5000
)

var (
	billingJobOnce     sync.Once
	billingJobRunning  atomic.Bool
	billingJobLocation *time.Location
)

func StartBillingJob() {
	billingJobOnce.Do(func() {
		loc, err := time.LoadLocation(billingJobTimeZoneName)
		if err != nil {
			loc = time.FixedZone("CST", 8*3600)
		}
		billingJobLocation = loc
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), "billing T+1 job started")
			ticker := time.NewTicker(billingJobTickInterval)
			defer ticker.Stop()
			runBillingJobIfDue()
			for range ticker.C {
				runBillingJobIfDue()
			}
		})
	})
}

func YesterdayInShanghai(now time.Time) string {
	if billingJobLocation == nil {
		billingJobLocation, _ = time.LoadLocation(billingJobTimeZoneName)
	}
	return now.In(billingJobLocation).AddDate(0, 0, -1).Format("2006-01-02")
}

func runBillingJobIfDue() {
	if !billingJobRunning.CompareAndSwap(false, true) {
		return
	}
	defer billingJobRunning.Store(false)

	now := time.Now()
	if now.In(billingJobLocation).Hour() < billingJobCutoffHour {
		return
	}
	yest := YesterdayInShanghai(now)
	var existing model.BillingJobRun
	err := model.DB.Where("stat_date = ? AND job_kind = ?", yest, model.BillingJobKindDailyFull).First(&existing).Error
	switch {
	case err != nil:
		runDailyFullJob(yest, &existing, model.BillingJobKindDailyFull)
	case existing.Status == model.BillingJobStatusSuccess:
		// nothing to do for daily_full
	case existing.Attempt >= model.BillingJobMaxAttempt:
		// give up daily_full; manual rerun required
	default:
		runDailyFullJob(yest, &existing, model.BillingJobKindDailyFull)
	}

	// Drain pending backfill rows oldest first.
	var pending []model.BillingJobRun
	if err := model.DB.Where("job_kind = ? AND status = ?", model.BillingJobKindBackfill, model.BillingJobStatusPending).
		Order("stat_date asc").Limit(31).Find(&pending).Error; err != nil {
		logger.LogWarn(context.Background(), "billing job: list backfill pending: "+err.Error())
		return
	}
	for i := range pending {
		runDailyFullJob(pending[i].StatDate, &pending[i], model.BillingJobKindBackfill)
	}
}

func runDailyFullJob(statDate string, runRec *model.BillingJobRun, kind string) {
	ctx := context.Background()
	runRec.StatDate = statDate
	runRec.JobKind = kind
	runRec.Status = model.BillingJobStatusRunning
	runRec.StartedAt = time.Now().UnixMilli()
	runRec.Attempt++
	model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "stat_date"}, {Name: "job_kind"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "started_at", "attempt"}),
	}).Create(runRec)

	if err := model.DB.Where("stat_date = ?", statDate).Delete(&model.BillDailyFull{}).Error; err != nil {
		finishJobFailed(runRec, "delete old rows: "+err.Error())
		return
	}

	startTs, endTs := dayBoundsInShanghai(statDate)

	type aggKey struct {
		UserId    int
		ChannelId int
		UserGroup string
		ModelName string
	}
	type aggVal struct {
		ReqCount int64
		P        int64
		C        int64
		Revenue  int64
	}
	bucket := map[aggKey]*aggVal{}

	cursor := 0
	for {
		var logs []model.Log
		q := model.LOG_DB.Where("created_at >= ? AND created_at < ? AND id > ? AND type = ?", startTs, endTs, cursor, model.LogTypeConsume).
			Order("id").Limit(billingJobBatchSize)
		if err := q.Find(&logs).Error; err != nil {
			finishJobFailed(runRec, "scan logs: "+err.Error())
			return
		}
		if len(logs) == 0 {
			break
		}
		cursor = logs[len(logs)-1].Id
		for i := range logs {
			lg := &logs[i]
			k := aggKey{lg.UserId, lg.ChannelId, lg.Group, lg.ModelName}
			v := bucket[k]
			if v == nil {
				v = &aggVal{}
				bucket[k] = v
			}
			v.ReqCount++
			v.P += int64(lg.PromptTokens)
			v.C += int64(lg.CompletionTokens)
			v.Revenue += int64(lg.Quota)
		}
		if len(logs) < billingJobBatchSize {
			break
		}
	}

	channelGroupCache := map[int]string{}
	getChannelGroup := func(channelId int) string {
		if g, ok := channelGroupCache[channelId]; ok {
			return g
		}
		var ch model.Channel
		if err := model.DB.Select("id", "group").First(&ch, channelId).Error; err != nil {
			channelGroupCache[channelId] = ""
			return ""
		}
		first := firstCSV(ch.Group)
		channelGroupCache[channelId] = first
		return first
	}

	inserted := int64(0)
	for k, v := range bucket {
		cost, missing := CalcCostQuota(k.ChannelId, k.ModelName, v.P, v.C)
		row := &model.BillDailyFull{
			StatDate:         statDate,
			UserId:           k.UserId,
			UserGroup:        k.UserGroup,
			ChannelId:        k.ChannelId,
			ChannelGroup:     getChannelGroup(k.ChannelId),
			ModelName:        k.ModelName,
			RequestCount:     v.ReqCount,
			PromptTokens:     v.P,
			CompletionTokens: v.C,
			RevenueQuota:     v.Revenue,
			CostQuota:        cost,
			ProfitQuota:      v.Revenue - cost,
			CostMissing:      missing,
		}
		if err := model.DB.Create(row).Error; err != nil {
			finishJobFailed(runRec, "insert bill_daily_full: "+err.Error())
			return
		}
		if missing {
			incrementCostMissing(ctx)
		}
		inserted++
	}

	runRec.Status = model.BillingJobStatusSuccess
	runRec.FinishedAt = time.Now().UnixMilli()
	runRec.RowsInserted = inserted
	runRec.ErrorMsg = ""
	model.DB.Save(runRec)
	logger.LogInfo(ctx, fmt.Sprintf("billing job %s success: rows=%d", statDate, inserted))
}

func finishJobFailed(r *model.BillingJobRun, msg string) {
	r.Status = model.BillingJobStatusFailed
	r.FinishedAt = time.Now().UnixMilli()
	r.ErrorMsg = msg
	model.DB.Save(r)
	incrementJobFailure(context.Background(), r.StatDate)
	logger.LogError(context.Background(), "billing job failed: "+msg)
}

func dayBoundsInShanghai(statDate string) (int64, int64) {
	loc := billingJobLocation
	if loc == nil {
		loc, _ = time.LoadLocation(billingJobTimeZoneName)
	}
	t0, _ := time.ParseInLocation("2006-01-02", statDate, loc)
	t1 := t0.AddDate(0, 0, 1)
	return t0.Unix(), t1.Unix()
}

func firstCSV(s string) string {
	if s == "" {
		return ""
	}
	for i, ch := range s {
		if ch == ',' {
			return s[:i]
		}
	}
	return s
}
