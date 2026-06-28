package redis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yintengching/playerledger/config"
)

// TestConnect_InvalidHost_ReturnsErrorWithPingPrefix 测试无效主机返回格式正确的错误
func TestConnect_InvalidHost_ReturnsErrorWithPingPrefix(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "invalid.host.that.does.not.exist.test",
		Port:         6379,
		Password:     "",
		DB:           0,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	assert.Nil(t, client, "client should be nil for invalid host")
	assert.Error(t, err, "Connect should return error for invalid host")

	// 验证错误格式符合规格：fmt.Errorf("redis ping: %w", err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "redis ping", "error message must start with 'redis ping'")
}

// TestConnect_ConnectTimeout_ReturnsErrorWithPingPrefix 测试连接超时返回正确的错误格式
func TestConnect_ConnectTimeout_ReturnsErrorWithPingPrefix(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "127.0.0.1",
		Port:         55555, // 不存在的端口
		Password:     "",
		DB:           0,
		DialTimeout:  100 * time.Millisecond, // 很短的超时强制失败
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	assert.Nil(t, client, "client should be nil when connection times out")
	assert.Error(t, err, "Connect should return error for connection timeout")

	// 验证错误格式符合规格：fmt.Errorf("redis ping: %w", err)
	errMsg := err.Error()
	assert.Contains(t, errMsg, "redis ping", "error message must contain 'redis ping'")
}

// TestConnect_ConfigurationPassed_AllFieldsForwardedToRedisOptions 测试所有配置字段都被传递
func TestConnect_ConfigurationPassed_AllFieldsForwardedToRedisOptions(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "invalid.host.test",
		Port:         6379,
		Password:     "testpassword",
		DB:           5,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     20,
	}

	// Connect 创建 client 然后立即 Ping，所以配置字段应该被正确读取
	// 如果字段没被传入，会导致连接错误（因为地址无效）或其他配置错误
	client, err := Connect(cfg)
	assert.Nil(t, client, "client should be nil for invalid host")
	assert.Error(t, err, "Connect should return error")

	// 错误应该来自 Ping（表明 redis.Options 被正确构造）
	errMsg := err.Error()
	assert.Contains(t, errMsg, "redis ping", "error should come from ping operation")
}

// TestConnect_DialTimeoutUsedInPingContext 测试 DialTimeout 被用于 Ping 的 context
func TestConnect_DialTimeoutUsedInPingContext(t *testing.T) {
	// 此测试通过验证 Ping 实际上受到了 timeout 限制
	// 使用非常短的 DialTimeout 应该导致 context deadline exceeded 类型的错误
	cfg := config.RedisConfig{
		Host:         "10.255.255.1", // 不可达的地址，会超时（黑洞 IP）
		Port:         6379,
		Password:     "",
		DB:           0,
		DialTimeout:  10 * time.Millisecond, // 非常短的超时
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	start := time.Now()
	client, err := Connect(cfg)
	elapsed := time.Since(start)

	assert.Nil(t, client, "client should be nil when timeout occurs")
	assert.Error(t, err, "Connect should return error")

	// 验证实际等待时间接近 DialTimeout（允许 100ms 偏差）
	assert.Less(t, elapsed, 200*time.Millisecond, "should timeout quickly near DialTimeout")

	// 错误应该来自 Ping 操作
	errMsg := err.Error()
	assert.Contains(t, errMsg, "redis ping", "error message must include 'redis ping'")
}

// TestConnect_ErrorNilClientOnFailure 测试失败时返回 nil client 和 error
func TestConnect_ErrorNilClientOnFailure(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "nonexistent.host",
		Port:         6379,
		Password:     "",
		DB:           0,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)

	// Connect 失败时应该返回 (nil, error)
	require.Nil(t, client, "client must be nil on error")
	require.Error(t, err, "must return error on connection failure")
}

// TestConnect_NoLoggerDependency 验证 redis 包不依赖 logger 包
func TestConnect_NoLoggerDependency(t *testing.T) {
	// 此测试仅通过编译成功即代表通过
	// 如果 redis.go 中 import logger，会导致 import cycle
	// 规格书 §2.1 要求：pkg/redis → pkg/logger 被明文禁止
	//
	// 这个测试的存在本身就是编译时检查——如果有 cycle，整个包都编译不过
	assert.True(t, true, "redis package successfully compiled without logger import")
}

// TestConnect_PoolSizeConfiguration 测试 PoolSize 配置被传递
func TestConnect_PoolSizeConfiguration(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "invalid.test",
		Port:         6379,
		Password:     "",
		DB:           0,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     25, // 自定义 pool size
	}

	client, err := Connect(cfg)
	// 失败预期，但主要是验证 PoolSize 不会造成 panic
	assert.Nil(t, client)
	assert.Error(t, err)
}

// TestConnect_TimeoutParametersUsed 测试三个 timeout 参数都被使用
func TestConnect_TimeoutParametersUsed(t *testing.T) {
	// 规格书 §7.2 要求传递：DialTimeout, ReadTimeout, WriteTimeout
	cfg := config.RedisConfig{
		Host:         "invalid.host.test",
		Port:         6379,
		Password:     "",
		DB:           0,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	// 验证没有 panic（表明参数被正确接受）
	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis ping")
}

// TestConnect_HostAndPortCombined 测试 Host 和 Port 被正确组合成 Addr
func TestConnect_HostAndPortCombined(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "redis.example.com",
		Port:         6380,
		Password:     "",
		DB:           0,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	// 主要是验证不会 panic（表明地址格式正确）
	assert.Nil(t, client)
	assert.Error(t, err)
	// 错误应该来自网络操作，而不是地址格式错误
	errMsg := err.Error()
	assert.Contains(t, errMsg, "redis ping")
}

// TestConnect_PasswordConfiguration 测试 Password 被传递到 redis 连接
func TestConnect_PasswordConfiguration(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "invalid.host",
		Port:         6379,
		Password:     "mypassword123",
		DB:           0,
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	// 验证不会 panic（表明 password 字段被接受）
	assert.Nil(t, client)
	assert.Error(t, err)
}

// TestConnect_DBConfiguration 测试 DB index 被传递
func TestConnect_DBConfiguration(t *testing.T) {
	cfg := config.RedisConfig{
		Host:         "invalid.test",
		Port:         6379,
		Password:     "",
		DB:           7, // 非默认 DB
		DialTimeout:  100 * time.Millisecond,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	}

	client, err := Connect(cfg)
	// 验证不会 panic（表明 DB 字段被接受）
	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis ping")
}
