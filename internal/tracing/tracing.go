// Package tracing wires OpenTelemetry distributed tracing: an OTLP exporter,
// the W3C trace-context propagator, and the tracers the instrumented layers
// (HTTP, pgx, blob store, background jobs) hang their spans on.
//
// Disabled (the default) means the global TracerProvider stays the no-op one,
// so every span the code starts is free — instrumentation call sites do not
// branch on the config themselves.
package tracing

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config mirrors config.TracingConfig without importing the config package
// (tracing sits below config in the dependency order of cmd/server).
type Config struct {
	Enabled      bool
	OTLPEndpoint string  // host:port of the OTLP receiver
	OTLPProtocol string  // "grpc" (default) or "http"
	OTLPInsecure bool    // plaintext instead of TLS
	SampleRatio  float64 // head-sampling ratio for root spans, 0..1
	ServiceName  string
	Environment  string // deployment.environment resource attribute
}

// Init installs the global TracerProvider and W3C propagator from cfg and
// returns a shutdown function that flushes buffered spans. When cfg.Enabled is
// false it installs nothing and returns a no-op shutdown.
//
// Sampling is ParentBased(TraceIDRatioBased): a sampled caller keeps its whole
// trace, an unsampled one stays unsampled, and only root spans roll the dice.
// "Always keep error traces" is deliberately NOT attempted here — a head
// sampler runs at span creation, before any handler has produced a status, so
// it cannot see errors (#302); that guarantee belongs to tail-sampling in the
// collector.
func Init(ctx context.Context, cfg Config, version string) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	var client otlptrace.Client
	switch cfg.OTLPProtocol {
	case "", "grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		client = otlptracegrpc.NewClient(opts...)
	case "http":
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.OTLPEndpoint)}
		if cfg.OTLPInsecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		client = otlptracehttp.NewClient(opts...)
	default:
		return nil, fmt.Errorf("tracing: unknown otlp_protocol %q (use grpc or http)", cfg.OTLPProtocol)
	}
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "nexspence"
	}
	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(version),
	}
	if cfg.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironment(cfg.Environment))
	}
	res, err := resource.New(ctx, resource.WithAttributes(attrs...))
	if err != nil {
		return nil, fmt.Errorf("tracing: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(ctx)
	}, nil
}

// Tracer returns a named tracer from the global provider — the no-op provider
// until Init has installed a real one.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

// StartRoot opens a root span for a background job that runs with no HTTP
// request behind it (cleanup, GC, blob-store migration, Nexus import,
// replication). Without such a root the job's DB and blob-store spans either
// vanish or attach to whatever unrelated context the goroutine inherited —
// and outgoing trace propagation has nothing to propagate (#302).
func StartRoot(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return Tracer("nexspence/jobs").Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
}

// Inject writes the current trace context into an outbound request's headers
// (W3C traceparent). Effective only when ctx carries a live span — for
// replication that is RunRule's root span; with no span in context the
// propagator silently writes nothing (#302).
func Inject(ctx context.Context, h http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(h))
}
