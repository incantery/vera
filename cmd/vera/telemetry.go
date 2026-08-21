// Telemetry, wired for Grafana Cloud's agent observability.
//
// That product is built on OpenTelemetry and reads the gen_ai.*
// semantic conventions, so this emits plain OTel rather than anything
// Grafana-shaped: the same spans and metrics go to their OTLP gateway,
// to a local Alloy, or to a collector on a laptop, and which one is a
// matter of environment variables rather than code.
//
// It is OFF unless an endpoint is configured. The alternative — the
// SDK's default of assuming a collector on localhost:4318 — means a
// binary that spends every exchange retrying a connection that was
// never going to work, and says so in the log until you stop reading
// the log.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	// Must match the SDK's own schema version, or merging this
	// resource with the default one fails outright.
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const serviceName = "vera"

// telemetryConfigured: has somebody actually pointed this somewhere?
//
// Standard OTel variables, so the Grafana Cloud portal's own copy-paste
// block is the entire setup:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp-gateway-<zone>.grafana.net/otlp
//	OTEL_EXPORTER_OTLP_HEADERS=Authorization=Basic <base64 instanceID:token>
//	OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
func telemetryConfigured() bool {
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_TRACES_EXPORTER",
		"OTEL_METRICS_EXPORTER",
	} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// startTelemetry installs the providers and hands back a shutdown.
// Shutdown matters more than usual here: exchanges are seconds apart
// and the batcher holds spans, so quitting without flushing loses the
// exchange you were looking at when you decided to quit.
func startTelemetry(ctx context.Context, version string) (*providers, error) {
	// Export failures happen on a background goroutine, so without
	// this a wrong token means a dashboard that stays empty and a
	// process that never mentions it. Loud is the only useful setting.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		slog.Error("telemetry", "error", err.Error())
	}))

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, err
	}

	spans, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	tracer := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spans),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracer)

	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	meter := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meter)

	return &providers{tracer, meter}, nil
}

type providers struct {
	tracer *sdktrace.TracerProvider
	meter  *sdkmetric.MeterProvider
}

func (p *providers) shutdown(ctx context.Context) {
	_ = p.tracer.Shutdown(ctx)
	_ = p.meter.Shutdown(ctx)
}

// check answers the only question that matters during setup: does this
// configuration actually reach the place it claims to?
//
// ForceFlush exports synchronously and returns what happened, so a bad
// token is a non-zero exit here rather than an empty dashboard an hour
// from now.
func (p *providers) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := p.tracer.ForceFlush(ctx); err != nil {
		return fmt.Errorf("traces did not reach the endpoint: %w", err)
	}
	if err := p.meter.ForceFlush(ctx); err != nil {
		return fmt.Errorf("metrics did not reach the endpoint: %w", err)
	}
	return nil
}

// captureContent: whether the prompt and the reply themselves ride
// along, which the convention makes opt-out because for most services
// it is user data leaving the building.
//
// Here it defaults ON. The whole reason to have this today is to answer
// "why did it say that", the destination is your own Grafana stack, and
// the content is already in the log beside it.
func captureContent() bool {
	switch os.Getenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT") {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}
