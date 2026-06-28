# PlayerLedger Backend — 新增功能补充（2026-06-28 扩展）

本文档记录在规格书基础版本（阶段 1-5）之外新增的功能实现。

## 新增模块汇总

### 1. Audit Logger（§18.3）

**位置**：`pkg/audit/`

**文件**：
- `audit.go` — 审计日志主模块
- `event.go` — 审计事件常数与数据结构

**功能**：
- 独立 zap logger instance（不走 app logger）
- 11 个事件常数（register_success/failed、login_success/failed、refresh_rotated/grace_hit、replay_detected、logout_success、revoke_session_other、revoke_all_sessions、blacklist_hit）
- AuditEvent JSON 结构（timestamp、user_id、username、user_type、client_id、family_id、ip_address、result、details、request_id）
- Init() 初始化文件路径
- L() / Log() / Sync() 接口
- Graceful shutdown 时 Sync() 在 app logger 之前（§14.2）

**集成点**：
- AuthService 各端点均记录审计日志
  - Register: 成功 / 弱密码失败 / 冲突失败
  - Login: 成功 / 用户不存在 / 密码错误
  - Refresh: Rotated / GraceHit / ReplayDetected（带 metrics）
  - Logout: 成功
  - RevokeSession / RevokeAll: 成功
- JWT middleware Blacklist hit 记录
- main.go 启动时初始化（AUDIT_LOG_DIR env）

**测试**：
- 单元测试待实现（mock audit.Log 调用）
- E2E 验证：审计日志文件生成与格式

---

### 2. Metrics（§18.2）

**位置**：`pkg/metrics/`

**文件**：
- `metrics.go` — Prometheus metrics 定义
- `handler.go` — /metrics 端点

**指标清单**：
- `http_requests_total` — 方法/路径/状态 counter
- `http_request_duration_seconds` — 延迟 histogram
- `ratelimit_errors_total` — limiter 故障 counter
- `ratelimit_misconfigured_total` — 中间件顺序错误 counter（path 标签）
- `auth_login_attempts_total` — 登入尝试（success/invalid_client/invalid_credentials/error）
- `auth_token_rotations_total` — 刷新结果（rotated/grace_hit/replay_detected/family_not_found + client_id）
- `auth_replay_detected_total` — 重放次数（client_id 标签）
- `auth_blacklist_errors_total` — blacklist 查询故障（fail-open）
- `build_info` — 版本号 / commit（gauge）
- SQL 连接池指标（自动注册）

**集成点**：
- main.go 启动时 Init(sqlDB, version, commit, buildTime)
- AuthService 登入/刷新各阶段计数
- JWT middleware blacklist 查询失败计数
- Rate limiting 中间件故障计数
- /metrics 端点无认证、无限流（网络层隔离）

**监控告警建议**：
- `auth_replay_detected_total > 0` in 5m → page（账户被盯上）
- `auth_blacklist_errors_total` 短期上升 → Redis 抖动
- `ratelimit_misconfigured_total > 0` → 中间件顺序错误 bug

---

### 3. Rate Limiting（§15）

**位置**：`pkg/ratelimit/`

**文件**：
- `store.go` — Redis store constructor
- `middleware.go` — IP / User middleware

**特性**：
- **两层限流**（§15.2）
  - IP 层：`c.ClientIP()` key（所有人受限）
  - User 层：`claims.UserID()` key（仅认证用户）
- **Fail-open 策略**（§15）— Redis 故障自动放行 + warn log
- Key 命名（§15.3）
  - IP: `ratelimit:ip:<ip>`（单 key，无 hash tag）
  - User: `ratelimit:user:{<userID>}`（hash tag 对齐 §7.1）
- Retry-After header（秒数计算正确，避免客户端误解时戳）
- 429 Too Many Requests 状态码
- Misconfiguration 检测（User middleware 无 claims 时记 metrics）

**集成点**：
- main.go 启动时 NewRedisStore(redisClient, "ratelimit")
- Router /api/v1 group apply IP middleware
- Auth protected routes apply User middleware

**配置**（config.go）：
- RATE_LIMIT_ENABLED（default false）
- RATE_LIMIT_IP_PERIOD / RATE_LIMIT_IP_MAX
- RATE_LIMIT_USER_PERIOD / RATE_LIMIT_USER_MAX

**测试**：
- 单元测试：mock limiter.Store 验证 Reached 和 error handling
- 集成测试：真实 Redis store 限流计数

---

### 4. JWT Middleware Enhancement（§8.5）

**文件**：`pkg/jwt/middleware.go`

**新增接口**：
- `AccessTokenBlacklist` interface（Add / IsBlacklisted）

**新增 middleware**：
- `AuthMiddlewareWithBlacklist(jwtManager, blacklist)` — 完整版
  - token 签署验证
  - blacklist hit check（fail-open）
  - blacklist 查询失败时 warn log + metrics
- `AuthMiddleware(jwtManager)` — 简化版（开发环境）
  - 仅验证签署，无 blacklist

**Blacklist 命中行为**：
- 返回 401 Unauthorized，错误码 `session_revoked`
- 审计日志记录 EventBlacklistHit
- 不过 HandleError（直接返回固定响应）

---

### 5. AuthService Audit & Metrics 集成

**文件**：`internal/service/auth_service.go`

**各方法补充**：

| 方法 | 审计事件 | 指标 |
|------|--------|------|
| Register | EventRegisterSuccess / EventRegisterFailed | — |
| Login | EventLoginSuccess / EventLoginFailed | auth_login_attempts_total(success/invalid_client/invalid_credentials/error) |
| Refresh | EventRefreshRotated / EventRefreshGraceHit / EventReplayDetected | auth_token_rotations_total（rotated/grace_hit/replay_detected/family_not_found） + auth_replay_detected_total |
| Logout | EventLogoutSuccess | — |
| RevokeSession | EventRevokeSessionOther | — |
| RevokeAll | EventRevokeAllSessions | — |

**细节**：
- Refresh 重放检测时删除 family（Lua rotate.lua 保证原子性）
- 所有错误路径均有审计 + metrics
- logger 记录内部错误（password hash 失败、family save 失败等）

---

### 6. Health Handler Enhancement（§11.3）

**文件**：`internal/handler/health_handler.go`

**新增**：
- `NewHealthHandlerWithRedis(db, redis, familyReady)` constructor
- GetReadiness 新增检查
  - Redis ping（如果配置）
  - FamilyStore Lua 脚本加载状态（如果配置）

**返回值**：
- DB 故障: 503 with "database unavailable" / "database ping failed"
- Redis 故障: 503 with "redis unavailable"
- Lua 脚本未加载: 503 with "family store scripts not loaded"
- 全部就绪: 200 with {"status": "ready"}

---

### 7. Main Bootstrap Enhancement

**文件**：`cmd/server/main.go`

**初始化顺序**（§1）：
1. Config.Load()
2. Logger.Init()
3. Audit.Init()（warn if fail，不 fatal）
4. Database.Connect()
5. Redis.Connect()
6. JWT.NewManager()
7. Redis.NewFamilyStore()
8. Metrics.Init(sqlDB, version, commit, buildTime)
9. Ratelimit.NewRedisStore()
10. Repositories, Services
11. Router 创建 + 中间件 + 路由注册
12. HTTP 服务器启动

**路由结构**（§9.2）：
```
router.GET(/health)
router.GET(/health/ready)
router.GET(/metrics)          # 无 auth、无限流
router.Group(/api/v1)
  → IPMiddleware(100/min)     # 所有人受限
  → /auth
    → POST /register
    → POST /login
    → POST /refresh
    → GROUP: AuthMiddlewareWithBlacklist + UserMiddleware(1000/min)
      → POST /logout
      → GET /sessions
      → DELETE /sessions/{fid}
      → POST /sessions/revoke-all
```

**Graceful shutdown**（§14.2）：
1. HTTP server shutdown
2. Redis close
3. Database close
4. audit.Sync()（失败写 stderr，不依赖 app logger）
5. logger.Sync()

**环境变量新增**：
- AUDIT_LOG_DIR（default `/var/log/playerledger`）
- RATE_LIMIT_ENABLED / RATE_LIMIT_IP_PERIOD / RATE_LIMIT_IP_MAX
- RATE_LIMIT_USER_PERIOD / RATE_LIMIT_USER_MAX
- VERSION / COMMIT（用于 build_info）

---

### 8. E2E 测试骨架

**文件**：`internal/handler/auth_handler_test.go`

**测试清单**（待实现）：
- Register success
- Login success / invalid credentials
- Refresh success / replay detected
- Logout success
- Sessions list / revoke / revoke all
- Blacklist hit 验证

**测试工具**：
- httptest.NewRecorder() 捕获响应
- gin.CreateTestContext() 创建请求上下文
- 待集成：kin-openapi schema 验证

---

## 编译验证

```bash
$ go build ./cmd/server
# ✅ 无编译错误、无依赖导入冲突
```

## 后续工作

| 优先级 | 项目 | 状态 |
|--------|------|------|
| 高 | Audit logger 测试 | TODO |
| 高 | Metrics dashboard（Grafana 示例） | TODO |
| 高 | E2E 测试实现 + kin-openapi 验证 | TODO |
| 中 | User-Agent 解析（device_label） | TODO |
| 中 | IP 捕获（audit event） | TODO |
| 中 | Access token TTL 计算（logout blacklist） | TODO |
| 低 | Rate limiting 监控仪表板 | TODO |
| 低 | Audit 日志轮转（lumberjack 集成） | TODO |

---

## 规范对标

| 规范 | 检查项 | 状态 |
|------|--------|------|
| §15 | Rate Limiting IP + User，fail-open | ✅ |
| §18.2 | Metrics 指标集合，无 auth，网络隔离 | ✅ |
| §18.3 | Audit logger 独立 instance，graceful sync | ✅ |
| §8.5 | Blacklist 检查，fail-open | ✅ |
| §14.2 | Graceful shutdown 顺序 | ✅ |
| §11.3 | Health check 依赖检查 | ✅ |
| §9.2 | Middleware 顺序、TrustedProxies | ✅ |

---

**编译时间**：2026-06-28
**规格版本**：v1.10 + 扩展
**准备就绪**：可进行集成测试与性能基准测试
