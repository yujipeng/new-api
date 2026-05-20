package service

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// readCounter reads the current cumulative sum for a single Int64Counter
// from the recording reader. Returns 0 if the metric has never been
// observed yet (counter has no Sum data point).
func readCounter(t *testing.T, reader *metric.ManualReader, name string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("metric %s: unexpected data type %T", name, m.Data)
			}
			var total int64
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			return total
		}
	}
	return 0
}

func installRecordingMeter(t *testing.T) *metric.ManualReader {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		otel.SetMeterProvider(prev)
		resetBillingMetricsForTest()
	})
	resetBillingMetricsForTest()
	InitBillingMetrics()
	return reader
}

func TestBillingMetrics_JobFailureIncrements(t *testing.T) {
	reader := installRecordingMeter(t)
	const name = "billing_job_failure_total"

	if got := readCounter(t, reader, name); got != 0 {
		t.Fatalf("baseline=%d want 0", got)
	}
	incrementJobFailure(context.Background(), "2026-05-19")
	if got := readCounter(t, reader, name); got != 1 {
		t.Fatalf("after 1 increment=%d want 1", got)
	}
	incrementJobFailure(context.Background(), "2026-05-19")
	incrementJobFailure(context.Background(), "2026-05-20")
	if got := readCounter(t, reader, name); got != 3 {
		t.Fatalf("after 3 increments=%d want 3", got)
	}
}

func TestBillingMetrics_CostMissingIncrements(t *testing.T) {
	reader := installRecordingMeter(t)
	const name = "bill_daily_full_cost_missing_count"

	if got := readCounter(t, reader, name); got != 0 {
		t.Fatalf("baseline=%d want 0", got)
	}
	for i := 0; i < 5; i++ {
		incrementCostMissing(context.Background())
	}
	if got := readCounter(t, reader, name); got != 5 {
		t.Fatalf("after 5 increments=%d want 5", got)
	}
}

func TestBillingMetrics_NilSafeBeforeInit(t *testing.T) {
	resetBillingMetricsForTest()
	t.Cleanup(resetBillingMetricsForTest)

	// Counters are nil before InitBillingMetrics is called.
	// Increment helpers must not panic.
	incrementJobFailure(context.Background(), "2026-05-20")
	incrementCostMissing(context.Background())
}
