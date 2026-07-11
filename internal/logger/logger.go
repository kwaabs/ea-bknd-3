package logger

import (
	"bknd-3/internal/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	*zap.Logger
}

// New creates a zap logger configured by environment.
func New(cfg *config.Config) *Logger {
	var zapCfg zap.Config

	if cfg.Environment == "production" {
		zapCfg = zap.NewProductionConfig()
		zapCfg.EncoderConfig.TimeKey = "timestamp"
		zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.TimeKey = "timestamp"
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	l, err := zapCfg.Build()
	if err != nil {
		panic(err)
	}

	return &Logger{l}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() {
	_ = l.Logger.Sync() // ignore sync errors (often harmless in dev)
}
