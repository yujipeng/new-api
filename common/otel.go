package common

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	otelDefaultMetricsAddr = ":9464"
	otelDefaultServiceName = "new-api"
)

var (
	otelMu             sync.Mutex
	otelMeterProvider  *sdkmetric.MeterProvider
	otelMetricsServer  *http.Server
	otelMetricsStarted bool
	otelMetricsAddr    string
)

func resolveOtelAddr() string {
	if v := strings.TrimSpace(os.Getenv("OTEL_METRICS_ADDR")); v != "" {
		return v
	}
	return otelDefaultMetricsAddr
}

func resolveOtelServiceName() string {
	if v := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME")); v != "" {
		return v
	}
	return otelDefaultServiceName
}

// InitOtel sets the global MeterProvider with a Prometheus exporter and
// starts an HTTP server exposing /metrics on the configured address.
//
// Subsequent calls are idempotent; the first successful initialization wins.
// Errors during exporter setup are returned; HTTP server bind failures are
// surfaced via SysError so the main process can continue.
func InitOtel(ctx context.Context) error {
	otelMu.Lock()
	defer otelMu.Unlock()

	if otelMetricsStarted {
		return nil
	}

	exp, err := otelprom.New()
	if err != nil {
		return err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", resolveOtelServiceName()),
		),
	)
	if err != nil {
		return err
	}

	otelMeterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exp),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(otelMeterProvider)

	otelMetricsAddr = resolveOtelAddr()
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	otelMetricsServer = &http.Server{
		Addr:              otelMetricsAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", otelMetricsAddr)
	if err != nil {
		SysError("otel metrics listen failed on " + otelMetricsAddr + ": " + err.Error())
		otelMeterProvider = nil
		otel.SetMeterProvider(nil)
		otelMetricsServer = nil
		otelMetricsAddr = ""
		return err
	}

	otelMetricsStarted = true
	srv := otelMetricsServer
	go func() {
		if serveErr := srv.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			SysError("otel metrics server stopped: " + serveErr.Error())
		}
	}()
	SysLog("otel metrics endpoint listening on " + otelMetricsAddr + "/metrics")
	return nil
}

// ShutdownOtel gracefully stops the metrics HTTP server and flushes the
// MeterProvider. Safe to call multiple times.
func ShutdownOtel(ctx context.Context) {
	otelMu.Lock()
	defer otelMu.Unlock()

	if !otelMetricsStarted {
		return
	}

	if otelMetricsServer != nil {
		_ = otelMetricsServer.Shutdown(ctx)
		otelMetricsServer = nil
	}
	if otelMeterProvider != nil {
		_ = otelMeterProvider.Shutdown(ctx)
		otelMeterProvider = nil
	}
	otel.SetMeterProvider(nil)
	otelMetricsAddr = ""
	otelMetricsStarted = false
}

// OtelMetricsAddr returns the bound metrics address (after InitOtel succeeds);
// empty string before init or after shutdown.
func OtelMetricsAddr() string {
	otelMu.Lock()
	defer otelMu.Unlock()
	return otelMetricsAddr
}
