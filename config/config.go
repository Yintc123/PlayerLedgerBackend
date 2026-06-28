package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config 單一配置結構，所有模組從此取值。
type Config struct {
	App       AppConfig       `mapstructure:",squash"`
	Server    ServerConfig    `mapstructure:",squash"`
	Database  DatabaseConfig  `mapstructure:",squash"`
	Redis     RedisConfig     `mapstructure:",squash"`
	JWT       JWTConfig       `mapstructure:",squash"`
	Log       LogConfig       `mapstructure:",squash"`
	RateLimit RateLimitConfig `mapstructure:",squash"`
	Metrics   MetricsConfig   `mapstructure:",squash"`
	Admin     AdminConfig     `mapstructure:",squash"`
}

type AppConfig struct {
	Env string `mapstructure:"APP_ENV" validate:"oneof=dev staging prod"`
}

type ServerConfig struct {
	Port              int           `mapstructure:"PORT" validate:"required,min=1,max=65535"`
	GinMode           string        `mapstructure:"GIN_MODE" validate:"oneof=debug release test"`
	AllowedOrigins    []string      `mapstructure:"ALLOWED_ORIGINS" validate:"required,dive,required"`
	AllowCredentials  bool          `mapstructure:"ALLOW_CREDENTIALS"`
	TrustedProxies    []string      `mapstructure:"TRUSTED_PROXIES"`
	ShutdownTimeout   time.Duration `mapstructure:"SHUTDOWN_TIMEOUT" validate:"required,min=1s"`
	ReadHeaderTimeout time.Duration `mapstructure:"READ_HEADER_TIMEOUT" validate:"required,min=1s"`
	ReadTimeout       time.Duration `mapstructure:"READ_TIMEOUT" validate:"required,min=1s"`
	WriteTimeout      time.Duration `mapstructure:"WRITE_TIMEOUT" validate:"required,min=1s"`
	IdleTimeout       time.Duration `mapstructure:"IDLE_TIMEOUT" validate:"required,min=1s"`
	MaxRequestBody    int64         `mapstructure:"MAX_REQUEST_BODY" validate:"required,min=1024"`
}

type DatabaseConfig struct {
	Host             string        `mapstructure:"DB_HOST" validate:"required"`
	Port             int           `mapstructure:"DB_PORT" validate:"required,min=1,max=65535"`
	User             string        `mapstructure:"DB_USER" validate:"required"`
	Password         string        `mapstructure:"DB_PASSWORD" validate:"required"`
	Name             string        `mapstructure:"DB_NAME" validate:"required"`
	SSLMode          string        `mapstructure:"DB_SSLMODE" validate:"oneof=disable require verify-ca verify-full"`
	MaxOpenConns     int           `mapstructure:"DB_MAX_OPEN_CONNS"`
	MaxIdleConns     int           `mapstructure:"DB_MAX_IDLE_CONNS"`
	ConnMaxLifetime  time.Duration `mapstructure:"DB_CONN_MAX_LIFETIME"`
	ConnectTimeout   time.Duration `mapstructure:"DB_CONNECT_TIMEOUT"`
	StatementTimeout time.Duration `mapstructure:"DB_STATEMENT_TIMEOUT"`
	PrepareStmt      bool          `mapstructure:"DB_PREPARE_STMT"`
}

type RedisConfig struct {
	Host         string        `mapstructure:"REDIS_HOST" validate:"required"`
	Port         int           `mapstructure:"REDIS_PORT" validate:"required,min=1,max=65535"`
	Password     string        `mapstructure:"REDIS_PASSWORD"`
	DB           int           `mapstructure:"REDIS_DB"`
	DialTimeout  time.Duration `mapstructure:"REDIS_DIAL_TIMEOUT"`
	ReadTimeout  time.Duration `mapstructure:"REDIS_READ_TIMEOUT"`
	WriteTimeout time.Duration `mapstructure:"REDIS_WRITE_TIMEOUT"`
	PoolSize     int           `mapstructure:"REDIS_POOL_SIZE"`
}

type JWTConfig struct {
	Issuer                string                  `mapstructure:"JWT_ISSUER" validate:"required"`
	Secret                string                  `mapstructure:"JWT_SECRET" validate:"required,min=32"`
	PreviousSecret        string                  `mapstructure:"JWT_SECRET_PREVIOUS" validate:"omitempty,min=32,nefield=Secret"`
	RefreshSecret         string                  `mapstructure:"JWT_REFRESH_SECRET" validate:"required,min=32,nefield=Secret"`
	PreviousRefreshSecret string                  `mapstructure:"JWT_REFRESH_SECRET_PREVIOUS" validate:"omitempty,min=32,nefield=RefreshSecret"`
	AccessTTL             time.Duration           `mapstructure:"JWT_ACCESS_TTL" validate:"required,min=1m"`
	GraceWindow           time.Duration           `mapstructure:"JWT_GRACE_WINDOW" validate:"min=0,max=1m"`
	ClockSkewLeeway       time.Duration           `mapstructure:"JWT_CLOCK_SKEW_LEEWAY" validate:"min=0,max=2m"`
	ClientPolicies        map[string]ClientPolicy `mapstructure:"JWT_CLIENT_POLICIES"`
	BcryptCost            int                     `mapstructure:"BCRYPT_COST" validate:"min=10,max=15"`
}

type ClientPolicy struct {
	RefreshTTL  time.Duration `mapstructure:"REFRESH_TTL" validate:"required,min=1m"`
	AbsoluteTTL time.Duration `mapstructure:"ABSOLUTE_TTL" validate:"required,gtfield=RefreshTTL"`
}

type LogConfig struct {
	Level     string `mapstructure:"LOG_LEVEL" validate:"oneof=debug info warn error"`
	Format    string `mapstructure:"LOG_FORMAT" validate:"oneof=json console"`
	Service   string `mapstructure:"LOG_SERVICE"`
	AuditPath string `mapstructure:"LOG_AUDIT_PATH"`
}

type RateLimitConfig struct {
	Enabled    bool          `mapstructure:"RATE_LIMIT_ENABLED"`
	IPPeriod   time.Duration `mapstructure:"RATE_LIMIT_IP_PERIOD" validate:"omitempty,min=1s"`
	IPLimit    int64         `mapstructure:"RATE_LIMIT_IP_MAX" validate:"omitempty,min=1"`
	UserPeriod time.Duration `mapstructure:"RATE_LIMIT_USER_PERIOD" validate:"omitempty,min=1s"`
	UserLimit  int64         `mapstructure:"RATE_LIMIT_USER_MAX" validate:"omitempty,min=1"`
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"METRICS_ENABLED"`
	Path    string `mapstructure:"METRICS_PATH"`
}

// AdminConfig — super admin seed（取代規格 §13.5 原 SQL seed migration）。
// 啟動時依此設定 idempotent 確保 admin 帳號存在：帳號不存在則建立、
// 已存在則跳過（不主動覆寫密碼，避免無聲改密碼造成稽核盲點）。
// 兩個欄位皆空 → 跳過 seed（dev 友善）；任一非空 → 兩個都必填且密碼 ≥ 12 字元。
// 跨欄位驗證見 Validate()。
type AdminConfig struct {
	Username string `mapstructure:"ADMIN_USERNAME"`
	Password string `mapstructure:"ADMIN_PASSWORD"`
}

// Validate 處理 struct tag 無法表達的跨欄位約束。
func (c *Config) Validate() error {
	for _, origin := range c.Server.AllowedOrigins {
		if origin == "*" && c.Server.AllowCredentials {
			return errors.New("ALLOW_CREDENTIALS=true 時 ALLOWED_ORIGINS 不可為 *（瀏覽器規範禁止）")
		}
	}

	if c.App.Env == "prod" && c.Database.SSLMode == "disable" {
		return errors.New("APP_ENV=prod 禁止 DB_SSLMODE=disable，必須 require / verify-ca / verify-full")
	}

	if c.App.Env == "prod" && c.Server.GinMode != "release" {
		return errors.New("APP_ENV=prod 必須 GIN_MODE=release，禁止 debug / test")
	}

	if c.RateLimit.Enabled {
		if c.RateLimit.IPPeriod < time.Second || c.RateLimit.IPLimit < 1 {
			return errors.New("RATE_LIMIT_ENABLED=true 時 RATE_LIMIT_IP_PERIOD ≥ 1s 且 RATE_LIMIT_IP_MAX ≥ 1")
		}
		if c.RateLimit.UserPeriod < time.Second || c.RateLimit.UserLimit < 1 {
			return errors.New("RATE_LIMIT_ENABLED=true 時 RATE_LIMIT_USER_PERIOD ≥ 1s 且 RATE_LIMIT_USER_MAX ≥ 1")
		}
	}

	// Admin seed：兩欄同時空 = 跳過 seed；任一非空則兩個都必填，密碼 ≥ 12 字元
	hasU, hasP := c.Admin.Username != "", c.Admin.Password != ""
	if hasU != hasP {
		return errors.New("ADMIN_USERNAME 與 ADMIN_PASSWORD 必須同時設定（或同時留空跳過 seed）")
	}
	if hasP && len(c.Admin.Password) < 12 {
		return errors.New("ADMIN_PASSWORD 至少 12 字元")
	}
	// Prod 禁止跳過 seed（避免 prod 上線忘了設）
	if c.App.Env == "prod" && !hasU {
		return errors.New("APP_ENV=prod 必須設定 ADMIN_USERNAME 與 ADMIN_PASSWORD")
	}

	return nil
}

// Load 从環境变量、.env 文件、config.yaml 中加载配置。
func Load() (*Config, error) {
	v := viper.New()

	// 1. 預設值（最低优先级）
	setDefaults(v)

	// 2. .env 文件（可选）
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	// 3. YAML 配置文件（根据 APP_ENV 选择）
	appEnv := strings.ToLower(os.Getenv("APP_ENV"))
	if appEnv == "" {
		appEnv = "dev"
	}

	cfgName := "config"
	if appEnv != "dev" {
		cfgName = "config." + appEnv
	}

	v.SetConfigName(cfgName)
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("read %s.yaml: %w", cfgName, err)
		}
	}

	// 4. 環境变量（最高优先级）
	// 注意：AutomaticEnv() 对 Unmarshal 不生效（Viper 已知限制），需显式 BindEnv（§4.3）
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	bindEnvVars(v)

	// 5. Unmarshal + 驗證
	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		intSecondsToTimeDurationHookFunc(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// 基础 struct tag 驗證
	val := NewValidator()
	if err := val.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("validate config struct: %w", err)
	}

	// 跨字段驗證
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("APP_ENV", "dev")
	v.SetDefault("GIN_MODE", "release")
	v.SetDefault("SHUTDOWN_TIMEOUT", 10)
	v.SetDefault("READ_HEADER_TIMEOUT", 10)
	v.SetDefault("READ_TIMEOUT", 30)
	v.SetDefault("WRITE_TIMEOUT", 30)
	v.SetDefault("IDLE_TIMEOUT", 120)
	v.SetDefault("MAX_REQUEST_BODY", int64(1<<20))

	v.SetDefault("DB_PORT", 5432)
	v.SetDefault("DB_SSLMODE", "disable")
	v.SetDefault("DB_MAX_OPEN_CONNS", 25)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME", 300) // 5m
	v.SetDefault("DB_CONNECT_TIMEOUT", 5)
	v.SetDefault("DB_STATEMENT_TIMEOUT", 10)
	v.SetDefault("DB_PREPARE_STMT", true)

	v.SetDefault("REDIS_PORT", 6379)
	v.SetDefault("REDIS_DIAL_TIMEOUT", 5)
	v.SetDefault("REDIS_READ_TIMEOUT", 3)
	v.SetDefault("REDIS_WRITE_TIMEOUT", 3)
	v.SetDefault("REDIS_POOL_SIZE", 10)

	v.SetDefault("JWT_ISSUER", "playerledger")
	v.SetDefault("JWT_ACCESS_TTL", 900) // 15m
	v.SetDefault("JWT_GRACE_WINDOW", 10)
	v.SetDefault("JWT_CLOCK_SKEW_LEEWAY", 30)
	v.SetDefault("JWT_CLIENT_POLICIES.cms-web.REFRESH_TTL", 3600)   // 1h
	v.SetDefault("JWT_CLIENT_POLICIES.cms-web.ABSOLUTE_TTL", 28800) // 8h
	v.SetDefault("JWT_CLIENT_POLICIES.public-web.REFRESH_TTL", 3600)
	v.SetDefault("JWT_CLIENT_POLICIES.public-web.ABSOLUTE_TTL", 86400) // 24h
	v.SetDefault("JWT_CLIENT_POLICIES.ios-app.REFRESH_TTL", 2592000)   // 720h = 30d
	v.SetDefault("JWT_CLIENT_POLICIES.ios-app.ABSOLUTE_TTL", 15552000) // 4320h = 180d
	v.SetDefault("JWT_CLIENT_POLICIES.android-app.REFRESH_TTL", 2592000)
	v.SetDefault("JWT_CLIENT_POLICIES.android-app.ABSOLUTE_TTL", 15552000)
	v.SetDefault("BCRYPT_COST", 12)

	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_FORMAT", "json")
	v.SetDefault("LOG_SERVICE", "playerledger")
	v.SetDefault("METRICS_PATH", "/metrics")
}

// intSecondsToTimeDurationHookFunc converts bare integer values (and plain integer
// strings as written in .env) to time.Duration by treating them as seconds.
// Strings that already carry a unit suffix ("15m", "10s") are passed through
// unchanged for StringToTimeDurationHookFunc to handle.
func intSecondsToTimeDurationHookFunc() mapstructure.DecodeHookFuncType {
	return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		if to != reflect.TypeOf(time.Duration(0)) {
			return data, nil
		}
		switch v := data.(type) {
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return data, nil // let StringToTimeDurationHookFunc handle "15m" etc.
			}
			return time.Duration(n) * time.Second, nil
		case int:
			return time.Duration(v) * time.Second, nil
		case int64:
			return time.Duration(v) * time.Second, nil
		case float64:
			return time.Duration(int64(v)) * time.Second, nil
		}
		return data, nil
	}
}

// bindEnvVars 顯式綁定所有 env var 到 Viper key（§4.3）。
// 必要原因：Viper 的 AutomaticEnv() 在 Unmarshal() 時不自動讀取 env，
// 只有顯式 BindEnv 才能確保 unmarshal 時能讀到 env 的值。
func bindEnvVars(v *viper.Viper) {
	keys := []string{
		// App
		"APP_ENV",
		// Server
		"PORT", "GIN_MODE", "ALLOWED_ORIGINS", "ALLOW_CREDENTIALS",
		"TRUSTED_PROXIES", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
		"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "MAX_REQUEST_BODY",
		// Database
		"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME",
		"DB_SSLMODE", "DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME", "DB_CONNECT_TIMEOUT", "DB_STATEMENT_TIMEOUT", "DB_PREPARE_STMT",
		// Redis
		"REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB",
		"REDIS_DIAL_TIMEOUT", "REDIS_READ_TIMEOUT", "REDIS_WRITE_TIMEOUT", "REDIS_POOL_SIZE",
		// JWT
		"JWT_ISSUER", "JWT_SECRET", "JWT_SECRET_PREVIOUS",
		"JWT_REFRESH_SECRET", "JWT_REFRESH_SECRET_PREVIOUS",
		"JWT_ACCESS_TTL", "JWT_GRACE_WINDOW", "JWT_CLOCK_SKEW_LEEWAY", "BCRYPT_COST",
		// Log
		"LOG_LEVEL", "LOG_FORMAT", "LOG_SERVICE", "LOG_AUDIT_PATH",
		// Rate Limit
		"RATE_LIMIT_ENABLED", "RATE_LIMIT_IP_PERIOD", "RATE_LIMIT_IP_MAX",
		"RATE_LIMIT_USER_PERIOD", "RATE_LIMIT_USER_MAX",
		// Metrics
		"METRICS_ENABLED", "METRICS_PATH",
		// Admin
		"ADMIN_USERNAME", "ADMIN_PASSWORD",
	}
	for _, key := range keys {
		v.BindEnv(key) //nolint:errcheck
	}
}
