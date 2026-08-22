package logger

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the structured logger used across Nexspence, aliasing zap's sugared logger.
type Logger = *zap.SugaredLogger

// New builds a Logger at the given level ("debug", "info", ...) and format ("text" or "json").
func New(level, format string) Logger {
	lvl := zapcore.InfoLevel
	_ = lvl.UnmarshalText([]byte(level))

	var cfg zap.Config
	if format == "text" {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)

	log, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	return log.Sugar()
}

// WithTraceContext returns log with trace_id/span_id fields when ctx carries a
// valid span, and log unchanged otherwise (tracing disabled, or no span in
// context). This is the bridge between the two observability systems: an
// error log line can be jumped to its full trace and back — which matters
// most under sampling, where only some requests have a trace at all and the
// log line is the only place that says whether this one did (#321).
func WithTraceContext(ctx context.Context, log Logger) Logger {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return log
	}
	return log.With("trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
}
