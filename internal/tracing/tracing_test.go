package tracing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Disabled tracing must install nothing: the global provider stays whatever it
// was and the returned shutdown is callable.
func TestInit_DisabledIsNoop(t *testing.T) {
	before := otel.GetTracerProvider()
	shutdown, err := Init(context.Background(), Config{Enabled: false}, "test")
	require.NoError(t, err)
	assert.Same(t, before, otel.GetTracerProvider())
	assert.NoError(t, shutdown(context.Background()))
}

func TestInit_UnknownProtocolRefused(t *testing.T) {
	_, err := Init(context.Background(), Config{Enabled: true, OTLPEndpoint: "localhost:4317", OTLPProtocol: "udp"}, "test")
	require.ErrorContains(t, err, "otlp_protocol")
}

// Enabled tracing installs a provider and the W3C propagator; the OTLP client
// is lazy, so no collector is needed to construct and shut down.
func TestInit_EnabledInstallsProviderAndPropagator(t *testing.T) {
	prev := otel.GetTracerProvider()
	defer otel.SetTracerProvider(prev)

	shutdown, err := Init(context.Background(), Config{
		Enabled: true, OTLPEndpoint: "localhost:1", OTLPProtocol: "grpc",
		OTLPInsecure: true, SampleRatio: 1, ServiceName: "nexspence-test",
	}, "vtest")
	require.NoError(t, err)
	assert.NotSame(t, prev, otel.GetTracerProvider())
	fields := otel.GetTextMapPropagator().Fields()
	assert.Contains(t, fields, "traceparent")
	_ = shutdown(context.Background())
}

// Inject writes traceparent only when the context carries a live span — the
// exact failure mode #302's verification caught in replication: no root span,
// no propagation, silently.
func TestInject_RequiresLiveSpan(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	defer otel.SetTracerProvider(prevTP)
	prevProp := otel.GetTextMapPropagator()
	defer otel.SetTextMapPropagator(prevProp)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// No span in context → nothing to inject.
	h := map[string][]string{}
	Inject(context.Background(), h)
	assert.Empty(t, h)

	// A live root span → traceparent present.
	ctx, span := StartRoot(context.Background(), "replication.run_rule")
	defer span.End()
	h2 := map[string][]string{}
	Inject(ctx, h2)
	assert.NotEmpty(t, h2["Traceparent"])
}
