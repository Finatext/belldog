package telemetry

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"

	"github.com/Finatext/belldog/internal/appconfig"
)

// SetupOTel initializes and setup global state for metrics collection.
// If config.NoOTel is true, this will setup the No-Op.
func SetupOTel(ctx context.Context, base *resource.Resource, config *appconfig.Config) (func(), error) {
	if config != nil && config.NoOTel {
		// If nothing is set to otel.Meter, the SDK will use the no-op implementation.
		return func() {}, nil
	}

	if base == nil {
		base = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog"))
	} else {
		b, err := resource.Merge(base, resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog")))
		if err != nil {
			return func() {}, errors.WithStack(err)
		}
		base = b
	}

	rsc, err := resource.Merge(base, resource.Environment())
	if err != nil {
		return func() {}, errors.WithStack(err)
	}

	httpExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return func() {}, errors.WithStack(err)
	}
	meterProvider := sdk.NewMeterProvider(
		sdk.WithResource(rsc),
		// To overwrite this, use `OTEL_METRIC_EXPORT_INTERVAL`.
		sdk.WithReader(sdk.NewPeriodicReader(httpExporter, sdk.WithInterval(10*time.Second))),
	)
	otel.SetMeterProvider(meterProvider)

	return func() {
		slog.Info("shutting down meter provider")
		if err := meterProvider.Shutdown(ctx); err != nil {
			slog.Error("failed to shutdown meter provider", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}, nil
}
