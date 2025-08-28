package telemetry

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	lambdadetector "go.opentelemetry.io/contrib/detectors/aws/lambda"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"

	"github.com/Finatext/belldog/internal/appconfig"
)

type Flusher struct {
	inner func(ctx context.Context) error
}

func (f Flusher) ForceFlush(ctx context.Context) error {
	return f.inner(ctx)
}

// SetupOTel initializes and setup global state for metrics collection.
// If config.NoOTel is true, this will setup the No-Op.
func SetupOTel(ctx context.Context, config appconfig.Config) (func(), error) {
	// We don't need flusher in non-Lambda environments.
	_, cleanup, err := setupOTelInner(ctx, nil, config)
	return cleanup, err
}

// SetupOTelLambda initializes OTel resources and global state for metrics collection.
func SetupOTelLambda(ctx context.Context, config appconfig.Config) (Flusher, func(), error) {
	// Stripped down Lambda collector does not support resource detection, so inject them in application layer.
	lambdaResource, err := lambdadetector.NewResourceDetector().Detect(ctx)
	if err != nil {
		return Flusher{}, nil, errors.WithStack(err)
	}

	return setupOTelInner(ctx, lambdaResource, config)
}

func setupOTelInner(ctx context.Context, base *resource.Resource, config appconfig.Config) (Flusher, func(), error) {
	if config.NoOTel {
		// If nothing is set to otel.Meter, the SDK will use the no-op implementation.
		return Flusher{inner: func(ctx context.Context) error {
			return nil
		}}, func() {}, nil
	}

	if base == nil {
		base = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog"))
	} else {
		b, err := resource.Merge(base, resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog")))
		if err != nil {
			return Flusher{}, nil, errors.WithStack(err)
		}
		base = b
	}

	rsc, err := resource.Merge(base, resource.Environment())
	if err != nil {
		return Flusher{}, nil, errors.WithStack(err)
	}

	httpExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return Flusher{}, nil, errors.WithStack(err)
	}
	meterProvider := sdk.NewMeterProvider(
		sdk.WithResource(rsc),
		// To overwrite this, use `OTEL_METRIC_EXPORT_INTERVAL`.
		sdk.WithReader(sdk.NewPeriodicReader(httpExporter, sdk.WithInterval(10*time.Second))),
	)
	otel.SetMeterProvider(meterProvider)

	flusher := func(ctx context.Context) error {
		if err := meterProvider.ForceFlush(ctx); err != nil {
			return errors.WithStack(err)
		}
		return nil
	}

	cleanup := func() {
		slog.Info("shutting down meter provider")
		if err := meterProvider.Shutdown(ctx); err != nil {
			slog.Error("failed to shutdown meter provider", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}
	return Flusher{inner: flusher}, cleanup, nil
}
