package common

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// TestOtelGinMiddleware_NoPanic ensures the otelgin middleware composes
// correctly with gin.New() after InitOtel has registered the global
// MeterProvider, mirroring the wiring done in main.go.
func TestOtelGinMiddleware_NoPanic(t *testing.T) {
	addr := freePort(t)
	t.Setenv("OTEL_METRICS_ADDR", addr)
	resetOtelForTest(t)
	if err := InitOtel(context.Background()); err != nil {
		t.Fatalf("InitOtel: %v", err)
	}
	t.Cleanup(func() { ShutdownOtel(context.Background()) })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(otelgin.Middleware("new-api-test"))
	engine.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "pong" {
		t.Fatalf("body=%q want pong", got)
	}

	// /metrics still serves runtime metrics after a request flowed through otelgin.
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("metrics get: %v", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(buf), "go_") {
		t.Fatalf("metrics body unhealthy: status=%d body=%.200s", resp.StatusCode, string(buf))
	}
}
