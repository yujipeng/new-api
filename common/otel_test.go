package common

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func resetOtelForTest(t *testing.T) {
	t.Helper()
	ShutdownOtel(context.Background())
}

func TestInitOtel_ExposesMetricsEndpoint(t *testing.T) {
	addr := freePort(t)
	t.Setenv("OTEL_METRICS_ADDR", addr)
	t.Setenv("OTEL_SERVICE_NAME", "new-api-test")
	resetOtelForTest(t)

	if err := InitOtel(context.Background()); err != nil {
		t.Fatalf("InitOtel: %v", err)
	}
	t.Cleanup(func() { ShutdownOtel(context.Background()) })

	url := "http://" + addr + "/metrics"
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	var body string
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		buf, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			body = string(buf)
			break
		}
		lastErr = nil
		time.Sleep(50 * time.Millisecond)
	}
	if body == "" {
		t.Fatalf("metrics endpoint never returned 200; lastErr=%v", lastErr)
	}
	// promhttp default handler always exposes go_* runtime metrics.
	if !strings.Contains(body, "go_") {
		t.Fatalf("metrics body missing go_* runtime metrics; got: %.200s", body)
	}
}

func TestInitOtel_Idempotent(t *testing.T) {
	addr := freePort(t)
	t.Setenv("OTEL_METRICS_ADDR", addr)
	resetOtelForTest(t)

	if err := InitOtel(context.Background()); err != nil {
		t.Fatalf("InitOtel #1: %v", err)
	}
	t.Cleanup(func() { ShutdownOtel(context.Background()) })

	// Second call must not fail with EADDRINUSE; it should be a no-op.
	if err := InitOtel(context.Background()); err != nil {
		t.Fatalf("InitOtel #2: %v", err)
	}
	if got := OtelMetricsAddr(); got != addr {
		t.Fatalf("OtelMetricsAddr=%q want %q", got, addr)
	}
}

func TestInitOtel_BindFailureLeavesCleanState(t *testing.T) {
	// Occupy a port to force a bind failure.
	occupy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupy.Close()

	t.Setenv("OTEL_METRICS_ADDR", occupy.Addr().String())
	resetOtelForTest(t)

	if err := InitOtel(context.Background()); err == nil {
		t.Fatalf("InitOtel expected bind error, got nil")
	}
	if got := OtelMetricsAddr(); got != "" {
		t.Fatalf("OtelMetricsAddr=%q after failure, want empty", got)
	}
	// A second attempt on a free port must succeed (state was cleaned).
	free := freePort(t)
	t.Setenv("OTEL_METRICS_ADDR", free)
	if err := InitOtel(context.Background()); err != nil {
		t.Fatalf("InitOtel after recovery: %v", err)
	}
	t.Cleanup(func() { ShutdownOtel(context.Background()) })
}
