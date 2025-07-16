package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"time"

	"github.com/cockroachdb/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"

	"github.com/Finatext/belldog/internal/appconfig"
)

type Flusher func(ctx context.Context) error

// SetupOTel initializes and setup global state for metrics collection.
// If config.NoOTel is true, this will setup the No-Op.
func SetupOTel(ctx context.Context, base *resource.Resource, config *appconfig.Config) (Flusher, func(), error) {
	if config != nil && config.NoOTel {
		// If nothing is set to otel.Meter, the SDK will use the no-op implementation.
		return func(ctx context.Context) error {
			return nil
		}, func() {}, nil
	}

	if base == nil {
		base = resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog"))
	} else {
		b, err := resource.Merge(base, resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("belldog")))
		if err != nil {
			return nil, nil, errors.WithStack(err)
		}
		base = b
	}

	rsc, err := resource.Merge(base, resource.Environment())
	if err != nil {
		return nil, nil, errors.WithStack(err)
	}

	httpExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, errors.WithStack(err)
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
	return flusher, cleanup, nil
}

// Take lambda handler function returning a function that flushes the metrics with defer:
func WithFlush(handler any, flusher func(ctx context.Context) error) any {
	fv := reflect.ValueOf(handler)
	ft := fv.Type()

	// Find context.Context parameter if it exists
	var ctxIndex = -1
	for i := 0; i < ft.NumIn(); i++ {
		if ft.In(i) == reflect.TypeOf((*context.Context)(nil)).Elem() {
			ctxIndex = i
			break
		}
	}

	wrapper := reflect.MakeFunc(ft, func(in []reflect.Value) []reflect.Value {
		// Extract context if found, otherwise use background context
		var ctx context.Context
		if ctxIndex >= 0 && ctxIndex < len(in) && !in[ctxIndex].IsNil() {
			ctx = in[ctxIndex].Interface().(context.Context)
		} else {
			ctx = context.Background()
		}

		defer func() {
			if err := flusher(ctx); err != nil {
				slog.Error("error flushing data", slog.String("error", fmt.Sprintf("%+v", err)))
			}
		}()
		return fv.Call(in)
	})

	return wrapper.Interface()
}
