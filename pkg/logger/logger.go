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
var (
	global      atomic.Pointer[zap.Logger]
	initialized atomic.Bool
)

func init() {
	global.Store(zap.NewNop())
}

// Init 用 LogConfig + env 初始化全域 logger（§5.2）。
// 規格 §5.2 要求：重複呼叫為 no-op + warn，不允許 runtime 替換 logger instance。
// （silent 替換會讓 init order bug 無法察覺）
func Init(logCfg config.LogConfig, env string) error {
	if !initialized.CompareAndSwap(false, true) {
		L().Warn("logger.Init called more than once; ignoring (logger 不允許 runtime 替換，見 §5.2)")
		return nil
	}
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

// ResetForTesting 將 logger 還原為未初始化狀態。
// **僅供測試使用** —— production code 永遠不該呼叫。
// Production 啟動時 Init 應只呼叫一次（§5.2）。
func ResetForTesting() {
	initialized.Store(false)
	global.Store(zap.NewNop())
}
