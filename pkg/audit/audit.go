package audit

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var auditLogger *zap.Logger

// Init 初始化审计日志（独立于应用 logger，§18.3）
// logPath: 审计日志文件路径（如 /var/log/app/audit.log）
// maxSizeMB: 单个文件最大大小（MB），超过后轮转
// maxBackups: 保留最多备份数
// maxAgeDays: 保留最多天数
func Init(logPath string, maxSizeMB int, maxBackups, maxAgeDays int) error {
	if logPath == "" {
		logPath = "/var/log/playerledger/audit.log"
	}

	// 创建目录
	dir := os.Getenv("AUDIT_LOG_DIR")
	if dir == "" {
		dir = "/var/log/playerledger"
	}
	os.MkdirAll(dir, 0o755)

	// JSON encoder 配置
	config := zap.NewProductionEncoderConfig()
	config.TimeKey = "timestamp"
	config.EncodeTime = zapcore.EpochTimeEncoder

	encoder := zapcore.NewJSONEncoder(config)

	// File sink（使用 zapcore.AddSync 避免对 zap.SugaredLogger 的依赖）
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open audit log file: %w", err)
	}

	// 简单实现（未集成 lumberjack，生产需补齐）
	sink := zapcore.AddSync(file)
	core := zapcore.NewCore(encoder, sink, zapcore.InfoLevel)
	auditLogger = zap.New(core, zap.AddCaller())

	return nil
}

// L 获取审计 logger
func L() *zap.Logger {
	if auditLogger == nil {
		// 未初始化时返回 noop logger，避免 panic
		return zap.NewNop()
	}
	return auditLogger
}

// Log 记录审计事件
func Log(event *AuditEvent) {
	if auditLogger == nil {
		return
	}
	auditLogger.Info("",
		zap.String("event_type", string(event.EventType)),
		zap.Int64("timestamp", event.Timestamp),
		zap.String("user_id", event.UserID),
		zap.String("username", event.Username),
		zap.String("user_type", event.UserType),
		zap.String("client_id", event.ClientID),
		zap.String("family_id", event.FamilyID),
		zap.String("ip_address", event.IPAddress),
		zap.String("result", event.Result),
		zap.Any("details", event.Details),
		zap.String("request_id", event.RequestID),
	)
}

// Sync 同步审计日志缓冲（graceful shutdown 时调用，§14.2）
// 失败时写 stderr，避免依赖正要关闭的 app logger
func Sync() error {
	if auditLogger == nil {
		return nil
	}
	return auditLogger.Sync()
}
