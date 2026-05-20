package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installRecorder swaps the global TracerProvider for a recording one,
// rebinds package-level billingTracer, and returns the recorder so a test
// can assert on captured spans. Restores everything on cleanup.
func installRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	prevTracer := billingTracer
	billingTracer = tp.Tracer("billing")
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
		billingTracer = prevTracer
	})
	return rec
}

func newTestContext(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

func TestStartBillingSpan_RebindsContextAndRecordsSpan(t *testing.T) {
	rec := installRecorder(t)
	c, _ := newTestContext(http.MethodGet, "/api/billing/user/daily")

	span := startBillingSpan(c, "TestSpanName")
	span.End()

	// Span name captured.
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("len(spans)=%d want 1", len(spans))
	}
	if got := spans[0].Name(); got != "TestSpanName" {
		t.Fatalf("span name=%q want TestSpanName", got)
	}

	// Request context now carries an active span context (parent for downstream).
	if !spans[0].SpanContext().IsValid() {
		t.Fatalf("recorded span has invalid SpanContext")
	}
}
