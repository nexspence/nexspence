package logger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/nexspence-oss/nexspence/internal/logger"
)

// Without a span in context the logger comes back unchanged — no empty
// trace_id fields polluting every line when tracing is off.
func TestWithTraceContext_NoSpan_Unchanged(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core).Sugar()

	got := logger.WithTraceContext(context.Background(), log)
	got.Infow("hello")

	require.Equal(t, 1, logs.Len())
	assert.Empty(t, logs.All()[0].ContextMap()["trace_id"])
}

// With a live span the line carries the span's exact ids.
func TestWithTraceContext_AttachesIDs(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	ctx, span := tp.Tracer("t").Start(context.Background(), "job")
	defer span.End()

	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core).Sugar()

	logger.WithTraceContext(ctx, log).Infow("working")

	require.Equal(t, 1, logs.Len())
	fields := logs.All()[0].ContextMap()
	assert.Equal(t, span.SpanContext().TraceID().String(), fields["trace_id"])
	assert.Equal(t, span.SpanContext().SpanID().String(), fields["span_id"])
}
