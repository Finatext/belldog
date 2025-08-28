package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const OTEL_SHUTDOWN_TIMEOUT_ENV = "OTEL_SHUTDOWN_TIMEOUT"
const OTEL_SHUTDOWN_TIMEOUT_DEFAULT = "10s"

type Flusher struct {
	inner func(ctx context.Context) error
}

func (f Flusher) ForceFlush(ctx context.Context) error {
	return f.inner(ctx)
}

// SetupOTel initializes OTel resources and global state for metrics collection.
func SetupOTel(ctx context.Context) (Flusher, func() error, error) {
	httpExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return Flusher{}, nil, fmt.Errorf("failed to create OTLP HTTP exporter: %w", err) // nolint:wrapcheck
	}
	meterProvider := sdk.NewMeterProvider(
		sdk.WithResource(resource.Environment()),
		// To overwrite this, use `OTEL_METRIC_EXPORT_INTERVAL`.
		sdk.WithReader(sdk.NewPeriodicReader(httpExporter, sdk.WithInterval(10*time.Second))),
	)
	otel.SetMeterProvider(meterProvider)

	flusher := func(ctx context.Context) error {
		if err := meterProvider.ForceFlush(ctx); err != nil {
			return fmt.Errorf("failed to force flush meter provider: %w", err)
		}
		return nil
	}

	timeoutEnv := os.Getenv(OTEL_SHUTDOWN_TIMEOUT_ENV)
	if timeoutEnv == "" {
		timeoutEnv = OTEL_SHUTDOWN_TIMEOUT_DEFAULT
	}
	timeout, err := time.ParseDuration(timeoutEnv)
	if err != nil {
		return Flusher{}, nil, fmt.Errorf("failed to parse env var `%s`: %w", OTEL_SHUTDOWN_TIMEOUT_ENV, err) // nolint:wrapcheck
	}

	cleanup := func() error { // nolint:contextcheck
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := meterProvider.Shutdown(ctx); err != nil {
			return err // nolint:wrapcheck
		}
		return nil
	}
	return Flusher{inner: flusher}, cleanup, nil
}
