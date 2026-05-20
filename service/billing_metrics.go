package service

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var (
	billingMetricsOnce        sync.Once
	billingJobFailureCounter  metric.Int64Counter
	billingCostMissingCounter metric.Int64Counter
)

// InitBillingMetrics binds OTel counters to the global MeterProvider.
// Safe to call multiple times; only the first call wires counters.
func InitBillingMetrics() {
	billingMetricsOnce.Do(func() {
		meter := otel.Meter("billing")
		billingJobFailureCounter, _ = meter.Int64Counter(
			"billing_job_failure_total",
			metric.WithDescription("Total billing job failures, labelled by stat_date."),
		)
		billingCostMissingCounter, _ = meter.Int64Counter(
			"bill_daily_full_cost_missing_count",
			metric.WithDescription("Count of bill_daily_full rows inserted with cost_missing=true."),
		)
	})
}

// resetBillingMetricsForTest resets the once guard so tests can rebind
// counters against a recording MeterProvider. Test-only.
func resetBillingMetricsForTest() {
	billingMetricsOnce = sync.Once{}
	billingJobFailureCounter = nil
	billingCostMissingCounter = nil
}

func incrementJobFailure(ctx context.Context, statDate string) {
	if billingJobFailureCounter == nil {
		return
	}
	billingJobFailureCounter.Add(ctx, 1)
}

func incrementCostMissing(ctx context.Context) {
	if billingCostMissingCounter == nil {
		return
	}
	billingCostMissingCounter.Add(ctx, 1)
}
