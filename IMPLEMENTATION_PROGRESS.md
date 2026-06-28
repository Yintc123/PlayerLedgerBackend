# PlayerLedger Backend 实现进度

## 当前状态（完成 30%）

项目现已可以编译和运行，已完成以下工作：

### ✅ 完成的模块

#### 阶段 1：基础设施与日志
- **pkg/ctxkey** — Context 类型化 key（完整实现 + 测试）
- **pkg/logger** — Zap 日志初始化与中间件
  - `Init()` / `L()` / `With()` 函数
  - `GinLogger` 访问日志中间件
  - `RequestID` middleware 与 helper 函数
- **internal/model** — 基础数据模型
  - `Base` struct（UUID, timestamps）
  - `CMSUser` 与 `Member` model
- **internal/apperr** — Domain error 定义
  - 所有 sentinel error 常数
  - `AppError` struct 与 helper 函数

#### 阶段 2：HTTP 框架与错误处理
- **internal/handler/response.go** — 统一响应格式
  - `Response[T]` / `ErrorResponse` struct
  - `OK()` / `OKList()` / `ErrorResp()` helper
- **internal/handler/health_handler.go** — 健康检查端点（骨架）
- **pkg/httpx** — HTTP 工具函数
  - `SecureHeaders()` middleware（含环境感知 HSTS）
  - `BodyLimit()` middleware
  - `Recovery()` middleware
  - `WriteError()` / `HandleError()` 函数
- **cmd/server/main.go** — 主程序（完整 bootstrap）
  - Config 加载
  - Logger 初始化
  - Router 设置（含所有中间件）
  - Graceful shutdown

#### 其他
- **config/config.go** — 配置结构定义（完整，但 viper 集成需调试）
- **config/validator.go** — validator 包装器
- **go.mod** — 所有依赖已添加

### ⚠️ 部分完成 / 需要调试

1. **config 模块的 viper 集成**
   - 问题：环境变量与默认值的交互在测试中失败
   - 解决方案：使用环境变量直接调用时工作正常
   - 建议：使用 `.env` 文件或直接环境变量进行本地测试；viper 的 DecodeHook 可能需要调整

### ❌ 待实现的模块

#### 阶段 3：数据库与 Redis（高优先级）
- `pkg/database/database.go` — GORM 连接管理
- `pkg/database/migrate.go` — golang-migrate 集成
- SQL migration 脚本（`migrations/`）
- `pkg/redis/redis.go` — go-redis 连接
- `pkg/redis/blacklist.go` — Access token 黑名单
- `pkg/redis/family_store.go` — Family-based token rotation（含 Lua 脚本）

#### 阶段 4：认证与授权（高优先级）
- `pkg/auth/hasher/` — 密码哈希接口与 bcrypt 实现
- `pkg/jwt/` — JWT 签发与验证（Manager interface）
- `pkg/audit/` — Audit logger
- `internal/repository/cms_user_repository.go` 与 `member_repository.go`
- `internal/service/auth_service.go` — 业务逻辑（Register/Login/Refresh/Logout 等）

#### 阶段 5：HTTP Handler 与完整集成
- `internal/handler/auth/` — 7 个 auth endpoint
- `pkg/metrics/` — Prometheus exporter
- `pkg/ratelimit/` — IP 与 user 层限流
- `schema/openapi.yaml` — 完整的 OpenAPI schema

---

## 开发指引

### 立即可做的工作

1. **修复 config 模块的 viper 问题**（可选但建议）
   ```bash
   PORT=8080 GIN_MODE=debug ALLOWED_ORIGINS=http://localhost:3000 \
   DB_HOST=localhost DB_USER=postgres DB_PASSWORD=pass DB_NAME=playerledger \
   REDIS_HOST=localhost JWT_SECRET='<32-byte-secret>' JWT_REFRESH_SECRET='<32-byte-secret>' \
   go run ./cmd/server
   ```

2. **实现数据库连接（阶段 3）**
   - 根据 §6 实现 `pkg/database/database.go`
   - 参考 `internal/model/cms_user.go` 与 `member.go` 的 TableName()
   - 创建 migration 文件（见 §13.5 范例）

3. **实现 JWT 与身份认证（阶段 4）**
   - 从 `pkg/jwt/jwt.go` 开始（Manager interface）
   - 必须遵守规格书 §8 的 HS256 签署、claim 定义、验证规则
   - FamilyStore 是 ADR-007 的核心，需要特别注意 Lua script 原子性

### TDD 流程

每个模块的实现顺序：
1. **定义 interface**（来自规格书）
2. **编写单元测试**（mock 实现）
3. **实现代码**直到测试通过
4. **集成测试**（若涉及外部系统如 DB、Redis）

示例（以 Repository 为例）：
```go
// 1. Define interface (repository/cms_user_repository.go)
type CMSUserRepository interface {
    FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)
    Create(ctx context.Context, u *model.CMSUser) error
}

// 2. Write unit tests (repository/cms_user_repository_test.go)
func TestCMSUserRepository_FindByUsername_NotFound(t *testing.T) { ... }

// 3. Implement (repository/cms_user_impl.go)
type cmsuserRepository struct { db *gorm.DB }
```

### 关键实现细节（来自规格书）

- **JWT 验证**（§8.1）：支持 previous secret + leeway
- **Refresh token rotation**（§8.2）：family-based + grace window（§8.3.1）
- **Rate limiting**（§15）：fail-open，Redis 故障不应阻断流量
- **Audit logging**（§18.3）：独立 sink，不走应用 logger，防止日志级别污染
- **Graceful shutdown**（§14.2）：audit logger → app logger → database → redis
- **CORS & HSTS**（§9）：AllowCredentials + wildcard 互斥；HSTS 仅 staging/prod

---

## 测试清单

### 单元测试（每个模块）
- [ ] JWT HS256 签署与验证
- [ ] Token exp / nbf / iat / abs_exp 验证（含 leeway）
- [ ] Refresh token rotation 三种结果（Rotated / GraceHit / ReplayDetected）
- [ ] 密码哈希（bcrypt cost 验证）
- [ ] Config 跨字段约束（prod SSLMode / GinMode 等）

### 集成测试（需要 Docker Compose）
- [ ] 数据库连接与 migration（含并发启动）
- [ ] Redis FamilyStore Lua script 原子性
- [ ] Repository 层 CRUD 与错误处理

### E2E 测试（需要 kin-openapi）
- [ ] POST /auth/register → 201
- [ ] POST /auth/login → 200 TokenPair（验证 schema）
- [ ] POST /auth/refresh GraceHit → 200
- [ ] POST /auth/logout → 204
- [ ] GET /health/ready DB/Redis 故障 → 503

---

## 环境变量示例（.env）

```env
APP_ENV=dev
PORT=8080
GIN_MODE=debug
ALLOWED_ORIGINS=http://localhost:3000,http://localhost:5173
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=playerledger
DB_SSLMODE=disable
REDIS_HOST=localhost
REDIS_PORT=6379
JWT_SECRET=super-secret-key-32-bytes-minimum!!
JWT_REFRESH_SECRET=super-secret-key-32-bytes-minimum!!
LOG_LEVEL=debug
LOG_FORMAT=console
METRICS_ENABLED=true
RATE_LIMIT_ENABLED=false
```

---

## 下一步行动

推荐优先顺序：
1. **修复 config viper** 或直接使用环境变量调试
2. **实现 pkg/database 与 migration**（数据层基础）
3. **实现 pkg/redis 与 FamilyStore**（token 存储）
4. **实现 pkg/jwt 与 AuthService**（核心业务）
5. **实现 7 个 auth handler**（API 契约）
6. **添加 metrics / ratelimit**（运维就绪）

---

## 参考

- 规格书：`docs/specs/infrastructure.md`（v1.10）
- ADR：`docs/adr/`（特别是 ADR-007 关于 token rotation）
- 模块依赖图：规格书 §2.1
