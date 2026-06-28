package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config 单一配置结构，所有模块从此取值。
type Config struct {
	App       AppConfig       `mapstructure:",squash"`
	Server    ServerConfig    `mapstructure:",squash"`
	Database  DatabaseConfig  `mapstructure:",squash"`
	Redis     RedisConfig     `mapstructure:",squash"`
	JWT       JWTConfig       `mapstructure:",squash"`
	Log       LogConfig       `mapstructure:",squash"`
	RateLimit RateLimitConfig `mapstructure:",squash"`
	Metrics   MetricsConfig   `mapstructure:",squash"`
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

// Validate 处理 struct tag 无法表达的跨字段约束。
func (c *Config) Validate() error {
	for _, origin := range c.Server.AllowedOrigins {
		if origin == "*" && c.Server.AllowCredentials {
			return errors.New("ALLOW_CREDENTIALS=true 时 ALLOWED_ORIGINS 不可为 *（浏览器规范禁止）")
		}
	}

	if c.App.Env == "prod" && c.Database.SSLMode == "disable" {
		return errors.New("APP_ENV=prod 禁止 DB_SSLMODE=disable，必须 require / verify-ca / verify-full")
	}

	if c.App.Env == "prod" && c.Server.GinMode != "release" {
		return errors.New("APP_ENV=prod 必须 GIN_MODE=release，禁止 debug / test")
	}

	if c.RateLimit.Enabled {
		if c.RateLimit.IPPeriod < time.Second || c.RateLimit.IPLimit < 1 {
			return errors.New("RATE_LIMIT_ENABLED=true 时 RATE_LIMIT_IP_PERIOD ≥ 1s 且 RATE_LIMIT_IP_MAX ≥ 1")
		}
		if c.RateLimit.UserPeriod < time.Second || c.RateLimit.UserLimit < 1 {
			return errors.New("RATE_LIMIT_ENABLED=true 时 RATE_LIMIT_USER_PERIOD ≥ 1s 且 RATE_LIMIT_USER_MAX ≥ 1")
		}
	}

	return nil
}

// Load 从环境变量、.env 文件、config.yaml 中加载配置。
func Load() (*Config, error) {
	v := viper.New()

	// 1. 预设默认值（最低优先级）
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

	// 4. 环境变量（最高优先级）
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// 5. Unmarshal + 验证
	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// 基础 struct tag 验证
	val := NewValidator()
	if err := val.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("validate config struct: %w", err)
	}

	// 跨字段验证
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("APP_ENV", "dev")
	v.SetDefault("GIN_MODE", "release")
	v.SetDefault("SHUTDOWN_TIMEOUT", "10s")
	v.SetDefault("READ_HEADER_TIMEOUT", "10s")
	v.SetDefault("READ_TIMEOUT", "30s")
	v.SetDefault("WRITE_TIMEOUT", "30s")
	v.SetDefault("IDLE_TIMEOUT", "120s")
	v.SetDefault("MAX_REQUEST_BODY", int64(1<<20))

	v.SetDefault("DB_PORT", 5432)
	v.SetDefault("DB_SSLMODE", "disable")
	v.SetDefault("DB_MAX_OPEN_CONNS", 25)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME", "5m")
	v.SetDefault("DB_CONNECT_TIMEOUT", "5s")
	v.SetDefault("DB_STATEMENT_TIMEOUT", "10s")
	v.SetDefault("DB_PREPARE_STMT", true)

	v.SetDefault("REDIS_PORT", 6379)
	v.SetDefault("REDIS_DIAL_TIMEOUT", "5s")
	v.SetDefault("REDIS_READ_TIMEOUT", "3s")
	v.SetDefault("REDIS_WRITE_TIMEOUT", "3s")
	v.SetDefault("REDIS_POOL_SIZE", 10)

	v.SetDefault("JWT_ISSUER", "playerledger")
	v.SetDefault("JWT_ACCESS_TTL", "15m")
	v.SetDefault("JWT_GRACE_WINDOW", "10s")
	v.SetDefault("JWT_CLOCK_SKEW_LEEWAY", "30s")
	v.SetDefault("JWT_CLIENT_POLICIES.cms-web.REFRESH_TTL", "1h")
	v.SetDefault("JWT_CLIENT_POLICIES.cms-web.ABSOLUTE_TTL", "8h")
	v.SetDefault("JWT_CLIENT_POLICIES.public-web.REFRESH_TTL", "1h")
	v.SetDefault("JWT_CLIENT_POLICIES.public-web.ABSOLUTE_TTL", "24h")
	v.SetDefault("JWT_CLIENT_POLICIES.ios-app.REFRESH_TTL", "720h")
	v.SetDefault("JWT_CLIENT_POLICIES.ios-app.ABSOLUTE_TTL", "4320h")
	v.SetDefault("JWT_CLIENT_POLICIES.android-app.REFRESH_TTL", "720h")
	v.SetDefault("JWT_CLIENT_POLICIES.android-app.ABSOLUTE_TTL", "4320h")
	v.SetDefault("BCRYPT_COST", 12)

	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_FORMAT", "json")
	v.SetDefault("METRICS_PATH", "/metrics")
}
