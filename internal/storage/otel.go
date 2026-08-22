package storage

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// blobSpan opens a span for one blob-store operation. Attributes carry
// metadata only — the key and byte count, never content, and never anything
// an operator could not already read off the wire (#302). With tracing
// disabled the global provider is the no-op one and this costs nothing.
//
// A negative size means "unknown at call time" and is omitted.
func blobSpan(ctx context.Context, name, key string, size int64) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{attribute.String("blob.key", key)}
	if size >= 0 {
		attrs = append(attrs, attribute.Int64("blob.size_bytes", size))
	}
	return otel.Tracer("nexspence/storage").Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attrs...))
}

// finishSpan closes a blob-store span, recording err when the operation failed.
func finishSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
