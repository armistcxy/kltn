package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	l *zap.Logger
}

func NewLogger(env string) (*Logger, error) {
	var (
		zl  *zap.Logger
		err error
	)

	switch env {
	case "prod":
		zl, err = zap.NewProduction()
	default:
		cfg := zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		cfg.DisableStacktrace = true
		cfg.Development = true
		zl, err = cfg.Build(zap.AddCaller(), zap.AddCallerSkip(1))
	}

	if err != nil {
		return nil, err
	}

	return &Logger{l: zl}, nil
}

func (lg *Logger) Debug(msg string, fields ...zap.Field) {
	lg.l.Debug(msg, fields...)
}

func (lg *Logger) Info(msg string, fields ...zap.Field) {
	lg.l.Info(msg, fields...)
}

func (lg *Logger) Warn(msg string, fields ...zap.Field) {
	lg.l.Warn(msg, fields...)
}

func (lg *Logger) Error(msg string, fields ...zap.Field) {
	lg.l.Error(msg, fields...)
}

func (lg *Logger) Fatal(msg string, fields ...zap.Field) {
	lg.l.Fatal(msg, fields...)
}

func (lg *Logger) With(fields ...zap.Field) *Logger {
	return &Logger{
		l: lg.l.With(fields...),
	}
}
