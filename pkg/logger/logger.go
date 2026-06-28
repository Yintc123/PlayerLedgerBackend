package logger

import (
	"fmt"
	"sync/atomic"

	"github.com/yintengching/playerledger/config"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// global logger instance（atomic 保證 thread-safe）。
// Init 之前 L() 回 zap.NewNop()，避免 init 順序錯誤導致 nil deref。
var global atomic.Pointer[zap.Logger]

func init() {
	global.Store(zap.NewNop())
}

// Init 用 LogConfig + env 初始化全域 logger（§5.2）。
// 重複呼叫視為 no-op 且仍正常初始化（測試用途）。
func Init(logCfg config.LogConfig, env string) error {
	var cfg zap.Config

	switch logCfg.Format {
	case "console":
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	case "json":
		cfg = zap.NewProductionConfig()
	default:
		return fmt.Errorf("unsupported log format: %s", logCfg.Format)
	}

	switch logCfg.Level {
	case "debug":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	default:
		return fmt.Errorf("unsupported log level: %s", logCfg.Level)
	}

	l, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}

	l = l.With(
		zap.String("service", logCfg.Service),
		zap.String("env", env),
	)
	global.Store(l)

	return nil
}

// L 取得全域 logger（Init 之前回 zap.NewNop()）。
func L() *zap.Logger {
	return global.Load()
}

// With 在全域 logger 上加 fields。
func With(fields ...zap.Field) *zap.Logger {
	return L().With(fields...)
}

// Sync 刷新日誌緩衝。
func Sync() error {
	return L().Sync()
}
