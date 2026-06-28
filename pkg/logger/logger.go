package logger

import (
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// 全局 logger 实例，使用 atomic.Pointer 保证线程安全
var global atomic.Pointer[zap.Logger]

func init() {
	// 初始化为 nop logger，避免 init 之前调用 L() 时 panic
	global.Store(zap.NewNop())
}

// Init 初始化全局 logger
func Init(format string, level string, service string, env string) error {
	cfg := zap.NewProductionConfig()

	// 设置日志格式
	switch format {
	case "console":
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	case "json":
		cfg = zap.NewProductionConfig()
	default:
		return fmt.Errorf("unsupported log format: %s", format)
	}

	// 设置日志级别
	switch level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	default:
		return fmt.Errorf("unsupported log level: %s", level)
	}

	// 添加基础字段
	fields := []zap.Field{
		zap.String("service", service),
		zap.String("env", env),
	}

	logger, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}

	logger = logger.With(fields...)
	global.Store(logger)

	return nil
}

// L 获取全局 logger 实例
func L() *zap.Logger {
	return global.Load()
}

// With 在全局 logger 上添加字段
func With(fields ...zap.Field) *zap.Logger {
	return L().With(fields...)
}

// Sync 刷新日志缓冲
func Sync() error {
	return L().Sync()
}
