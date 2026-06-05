package logger

import (
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
