# PlayerLedger Backend — 基礎架構規格書

版本：v1.11
日期：2026-06-28

> v1.11：新增 §7.5 `UserRevocationStore`（user-level revoke watermark）、§8.5 AuthMiddleware
> 補步驟 3.5 檢查 `claims.iat < RevokedAfter(claims.Subject)`；§12.4 補對應 401 `session_revoked` 條目；
> §18.2 新增 metric `auth_user_revoke_errors_total`。
> 用途：admin 對單一 user 強制踢人（不持有 target jti 的場景，例如 cms_user role 變更、軟刪除）。
> 與 §7.3 `AccessTokenBlacklist`（per-jti）互補；對應 cms-users-api.md v1.1 §4.3/§4.4。
>
> v1.10：基礎架構完整度補強 — 補齊 audit `EventRegisterSuccess` / `EventRegisterFailed` 常數
> （§18.3.2 與 §18.3.4 整合點對齊）；釐清 `FamilyStore` 的 Lua script 載入時機改為
> `NewFamilyStore` constructor 內自動執行，介面只暴露 `ScriptsLoaded()` 給 `/health/ready`
> （§7.4 / §9.1 / §11.3 / §22 一致）；合併 `LogConfig.AuditPath` 至 §4.2（消除 §18.3.5
> 重複定義）；新增 §3.5 Auth Endpoint OpenAPI schema 完整範例（7 個 endpoint，含
> securitySchemes、共用 envelope、request / response、錯誤碼對應）；補強 `HandleError`
> 對 JSON parsing error 的處理（§12.3 / §12.4），避免 `ShouldBindJSON` 失敗誤走 500；
> 補強 `Validate()` 對 prod `GIN_MODE` 的強制檢查（§4.2），避免 debug 模式洩漏 stack trace。
>
> v1.9：基礎架構最佳實踐修正 — 補齊缺漏介面（`FamilyStore.ScriptsLoaded/PreloadScripts`、
> `logger.GetRequestIDFromCtx`、`pkg/auth/hasher`、`Manager.VerifyAccess` 的 `aud` 白名單）；
> 解開 `pkg/logger ↔ pkg/httpx` import cycle（`GinRecovery` 改置 `pkg/httpx`，所有中介層統一走 `httpx.WriteError`）；
> 補強 migration DSN（`statement_timeout` + 密碼 redact）、HSTS 環境感知、CORS `ExposeHeaders` 加 `Retry-After`、
> audit logger 生命週期（`Sync()` + graceful shutdown 整合）、audit log 檔案 rotation 責任歸屬釐清、
> `JWT_*_PREVIOUS` secret 強度驗證、`UserMiddleware` 配置錯誤 metric、`HTTPRequestDuration` 加 `status` 標籤、
> `/health/ready` 範例補欄位、Go 版本三處統一至 1.25。
>
> v1.8：補完 ADR 007 落地細節 — HS256 簽署、純 JWT 傳輸（不走 cookie）、`iss` claim、`FamilyState.AbsoluteExp`、
> GraceHit handler 實作、audit log、metrics、UA parser、auth domain errors。新增 `schema/openapi.yaml`。
>
> v1.7：JWT / Refresh Token 設計改為 Family-based Rotation + 重放偵測（ADR 007，取代 ADR 002）。

---

## 1. 技術選型

### 1.1 執行環境最低需求

| | 版本 | 理由 |
|---|---|---|
| Go | **1.25** | 鎖定固定 minor 版本，避免 CI / Dockerfile / 本機漂移；§23.3 CI、§24.2 Dockerfile 必須同步此版本 |
| PostgreSQL | **15+** | 內建 `gen_random_uuid()`、`pg_advisory_lock`、`statement_timeout` |
| Redis | **7+** | Lua script 行為與 `noeviction` 政策；FamilyStore 依賴 |

### 1.2 套件版本

> **版本策略**：欄位列的版本為**最低相容版本**（semver minor 視為相容），不是鎖死版本；
> 跟隨 dependabot 升 minor / patch 即可，跨 major 才需評估 breaking changes。
> CI 以 `go.mod` 為單一真實來源；表格僅作初始選型參考。

| 模組 | 套件 | 最低版本 | 說明 |
|------|------|----------|------|
| HTTP 框架 | `gin-gonic/gin` | ≥ v1.10 | 高效能 HTTP router，中介層生態完整 |
| CORS | `gin-contrib/cors` | ≥ v1.7 | Gin CORS middleware |
| 安全標頭 | `unrolled/secure` | ≥ v1.16 | HSTS / X-Content-Type-Options / X-Frame-Options |
| ORM | `gorm.io/gorm` | ≥ v1.25 | Go 主流 ORM，支援 migration、hooks、association |
| GORM Zap 整合 | `moul.io/zapgorm2` | ≥ v1.3 | 將 GORM 日誌接到全域 zap |
| DB Driver | `gorm.io/driver/postgres` | ≥ v1.5 | PostgreSQL driver |
| DB Migration | `golang-migrate/migrate` | ≥ v4.18 | 版本化 migration 腳本管理 |
| Config | `spf13/viper` | ≥ v1.19 | 多源設定載入，優先順序可控 |
| Logging | `uber-go/zap` | ≥ v1.27 | 結構化日誌，效能卓越 |
| Metrics | `prometheus/client_golang` | ≥ v1.20 | Prometheus exporter |
| JWT | `golang-jwt/jwt` | ≥ v5.2 | 業界標準（本階段用 HS256，未來多服務時升級 RS256） |
| Bcrypt | `golang.org/x/crypto` | ≥ v0.27 | 密碼雜湊 |
| Redis | `redis/go-redis` | ≥ v9.6 | 官方 client，Pipeline / Pub-Sub / Cluster 支援 |
| Rate Limiting | `ulule/limiter` | ≥ v3.11 | 支援 Redis 分散式限流 |
| Validation | `go-playground/validator` | ≥ v10.22 | struct tag 驗證，Gin 內建整合 |
| UUID | `google/uuid` | ≥ v1.6 | UUID v4 產生 |
| User-Agent 解析 | `mileusna/useragent` | ≥ v1.3 | 解析 UA → device label（family metadata 顯示用） |
| Test 斷言 | `testify/assert` + `testify/require` | ≥ v1.9 | 斷言函式庫 |
| Test 容器 | `testcontainers-go` | ≥ v0.33 | Integration test 動態啟動容器（CI 用，見 §19） |
| Schema 驗證 | `getkin/kin-openapi` | ≥ v0.127 | E2E test / CI 驗證 OpenAPI 文件結構 |

> **為何選 GORM 而非 ent / sqlc？** 詳見 ADR-001。
> GORM 上手成本低，適合本階段快速迭代；複雜查詢可搭配 `db.Raw()` 使用原生 SQL。

> **分散式追蹤預留**
> OpenTelemetry (`go.opentelemetry.io/otel`) 暫不導入，但所有 service / repository 簽章必須接收 `context.Context`，未來導入時可直接掛載 span 而不改介面。

---

## 2. 目錄結構

```
PlayerLedgerBackend/
├── cmd/
│   └── server/
│       └── main.go              # 程式進入點（啟動 Gin server）
├── config/
│   ├── config.go                # Config 結構定義與載入邏輯
│   ├── config_test.go
│   └── config.yaml.example      # 範例設定檔（不含機敏資料）
├── internal/
│   ├── handler/                 # HTTP handler（E2E 測試層）
│   │   ├── health_handler.go    # GET /health, GET /health/ready
│   │   ├── error_handler.go     # 統一錯誤轉 HTTP 回應
│   │   └── response.go          # 統一回應格式
│   ├── service/                 # 業務邏輯（Unit 測試層）
│   ├── repository/              # 資料存取（Integration 測試層）
│   ├── apperr/                  # Domain error 定義
│   ├── model/                   # DB 實體（對應資料表）
│   ├── dto/                     # 對前端的資料傳輸物件（含 Model→DTO 轉換）
│   └── pagination/              # 分頁參數解析與 GORM scope
├── pkg/
│   ├── ctxkey/                  # context.Context typed keys 與 helper（最底層，無 import 任何 pkg）
│   │   └── ctxkey.go            # SetRequestID(ctx,id) / RequestID(ctx) / RequestIDHeader 常數
│   ├── logger/                  # Zap 封裝，全域 logger（不含 HTTP 中介層以外的 panic 處理）
│   │   ├── logger.go            # Init / L() / With()
│   │   ├── middleware.go        # GinLogger（GinRecovery 已移至 pkg/httpx，見 §9.3）
│   │   └── requestid.go         # RequestID middleware + GetRequestID（gin 版）；ctx 版見 pkg/ctxkey
│   ├── auth/
│   │   └── hasher/              # 密碼雜湊抽象層（bcrypt 預設實作；未來可換 argon2id）
│   │       ├── hasher.go        # Hasher interface
│   │       └── bcrypt.go        # bcryptHasher 實作（cost 從 JWTConfig.BcryptCost 取）
│   ├── jwt/                     # JWT 簽發與驗證
│   │   ├── jwt.go               # Manager interface 與 Claims 定義
│   │   ├── role.go              # Role / UserType 常數
│   │   ├── context.go           # typed context key（避免字串散落）
│   │   └── middleware.go        # AuthMiddleware / RequireRole / RequireOwnership
│   ├── database/                # GORM 連線管理 + zap logger 整合 + migration runner
│   ├── redis/                   # go-redis 連線管理 + Lua script（blacklist / FamilyStore）
│   ├── ratelimit/               # Rate limiting middleware（IP + User 兩層）
│   ├── metrics/                 # Prometheus exporter 與 middleware
│   ├── audit/                   # Audit logger（獨立 zap instance）
│   ├── ua/                      # User-Agent parser（mileusna/useragent 封裝；§7.4 FamilyState.DeviceLabel 用）
│   └── httpx/                   # 共用 HTTP 中介層與 helper
│       ├── body_limit.go        # MaxBodyBytes
│       ├── secure_headers.go    # SecureHeaders（依 APP_ENV 切 HSTS）
│       ├── recovery.go          # GinRecovery（從 pkg/logger 搬來，解開 import cycle）
│       └── error.go             # WriteError（所有中介層共用錯誤回應）
├── migrations/                  # golang-migrate SQL 腳本（專案根；embed 透過 embed.go 匯出）
│   ├── embed.go                 # //go:embed *.sql var FS embed.FS
│   ├── 000001_create_cms_users.up.sql      # auth 必需，見 §13.5
│   ├── 000001_create_cms_users.down.sql
│   ├── 000002_create_members.up.sql
│   └── 000002_create_members.down.sql
│   # 註：admin 不再用 SQL seed；改由 ADMIN_USERNAME/ADMIN_PASSWORD env + service.EnsureAdminFromConfig 注入（§13.5）
├── schema/                      # OpenAPI 3.1 契約（SDD 唯一真實來源）
│   ├── openapi.yaml             # 主 schema 檔
│   └── components/              # 共用 schema 元件
├── docs/
│   ├── adr/                     # Architecture Decision Records
│   └── specs/                   # 規格書（本文件所在）
├── .github/
│   └── workflows/
│       └── ci.yml               # GitHub Actions CI pipeline
├── docker-compose.test.yml      # 本地 integration test 用 DB / Redis 容器
├── Dockerfile                   # 生產映像建置（multi-stage）
├── Makefile                     # 常見指令統一入口（test / lint / build / migrate）
├── go.mod
├── go.sum
├── .golangci.yml                # golangci-lint 設定
├── .env.example                 # 環境變數範例
└── README.md
```

### 2.1 依賴方向

模組間 import 邊明文鎖定，避免 cycle 與不可預期的耦合。CI 由 `golangci-lint` 的 `depguard` rule 強制（§23.4）。

```
internal/handler ─→ internal/service ─→ internal/repository ─→ internal/model
internal/handler ─→ pkg/httpx   ─→ pkg/logger ─→ pkg/ctxkey
internal/handler ─→ internal/apperr
internal/service ─→ pkg/jwt     ─→ pkg/redis ─→ pkg/logger ─→ pkg/ctxkey
internal/service ─→ pkg/auth/hasher
internal/service ─→ pkg/audit   ─→ pkg/ctxkey                  ← audit 不經 pkg/logger
pkg/ratelimit    ─→ pkg/jwt     ─→ pkg/redis ─→ pkg/logger
pkg/database     ─→ pkg/logger（zapgorm2 整合）
pkg/redis        ─→ pkg/logger
pkg/metrics      —— 葉節點，無 internal/* 依賴
pkg/ctxkey       —— 最底層，無 import 任何 pkg（純 context helper）
```

永遠單向、禁止反向的規則：

| 規則 | 違反後果 |
|---|---|
| `pkg/* → internal/*` 永遠禁止 | pkg 是 reusable 元件，反向 import 立即 cycle |
| `pkg/redis` 禁止 import `pkg/jwt` / `pkg/ratelimit` | 與 `pkg/ratelimit → pkg/jwt → pkg/redis` 形成 cycle |
| `pkg/logger` 禁止 import `pkg/httpx` / `pkg/jwt` / `pkg/redis` | logger 是最底層，反向 import 立即 cycle（v1.9 已修 `pkg/httpx`） |
| `pkg/audit` 禁止 import `pkg/logger` / `pkg/jwt` / `pkg/redis` | audit 是獨立 sink，import 應用層 package 會把 LOG_LEVEL / package 問題傳染到 audit；用 `pkg/ctxkey` 取 request_id 即可 |
| `internal/model` 不得 import 任何其他 internal 子目錄 | model 是純資料結構，反向會綁死 service 改動 |
| `internal/repository` 不得 import `internal/service` | repository 是純資料存取，反向耦合會讓 fake 失效 |
| `internal/service` 不得 import `internal/handler` | service 不知 HTTP；handler 透過 ctx / DTO 傳值 |

> **OpenTelemetry 預留**：所有 service / repository 簽章第一參數均接 `context.Context`，未來導入 OTel 直接掛 span 不改 interface（見 §1）。

---

## 3. SDD — OpenAPI 契約

### 3.1 設計原則

- **Schema 為唯一契約**：所有 HTTP API 的 request / response 結構必先定義於 `schema/openapi.yaml`，再實作 handler。
- **變更流程**：
  1. 修改 `schema/openapi.yaml`（PR 中獨立 commit，方便 review）
  2. 調整 handler 的 E2E test，以 schema 為斷言依據
  3. 調整 handler / service 實作直到測試通過
- **破壞性變更**：移除欄位、改型別、改 status code 須在 PR 說明中標註 `BREAKING CHANGE` 並通知前端。

### 3.2 工具鏈

| 用途 | 工具 |
|------|------|
| Schema lint | `redocly/cli`（CI 中執行） |
| Schema → 文件 | `redoc-cli` 產生靜態 HTML，部署為 `/docs` 端點 |
| Runtime 驗證 | E2E test 用 `getkin/kin-openapi` 驗證 response 是否符合 schema |
| Schema → Go 型別 | 目前手寫 handler；schema 數量超過 30 個端點時評估 `oapi-codegen` |

### 3.3 CI 整合

`lint` job 中加入：

```bash
redocly lint schema/openapi.yaml
```

Schema 不符規範直接擋下 PR。E2E test 必須在每個 handler 至少一個 happy path 與一個 error path 中驗證 response 符合 schema。

### 3.4 Auth Endpoint 清單

以下端點由 `schema/openapi.yaml` 明確定義，handler 一律對齊 schema：

| Method | Path | Auth | Service | 說明 |
|---|---|---|---|---|
| POST | `/auth/register` | 公開 | `AuthService.Register` | **僅 `client_id == "cms-web"` 放行**；CMS 自註冊，預設 `role = user`；成功回 201 無 body |
| POST | `/auth/login` | 公開 | `AuthService.Login` | 帳密登入，依 `client_id` 路由表（見 §8.9） |
| POST | `/auth/refresh` | refresh token 自我認證 | `AuthService.Refresh` | Family-based rotation；行為見 §8.2 |
| POST | `/auth/logout` | access token | `AuthService.Logout` | 廢當前 family + access JTI 入黑名單 |
| GET | `/auth/sessions` | access token | `AuthService.ListSessions` | 列我所有 family（lazy cleanup） |
| DELETE | `/auth/sessions/{fid}` | access token | `AuthService.RevokeSession` | 撤銷指定 family；不可撤自己當前 family |
| POST | `/auth/sessions/revoke-all` | access token | `AuthService.RevokeAll` | 全裝置登出 + 當前 access JTI 入黑名單 |

> `/health`、`/health/ready`、`/metrics` 屬 ops 端點，**不在 OpenAPI 契約範圍**（不對外公布、不保證版本相容），規格定義見 §11、§18.1。

### 3.5 Auth Endpoint Schema 範例

下方為 `schema/openapi.yaml` 中 Auth endpoint 的完整契約片段，TDD 寫 E2E test 時 `kin-openapi`
直接以此驗證 response。**所有錯誤回應一律走共用 `ErrorResponse` envelope**（對應 §10.2 / §12.4）；
成功回應走 `Response<T>` 或 `204 No Content`。

#### 3.5.1 共用安全與 envelope

```yaml
openapi: 3.1.0
info:
  title: PlayerLedger Backend API
  version: 1.0.0

components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT

  schemas:
    # ─── 共用 envelope ───────────────────────────────────────────────
    SuccessEnvelope:
      type: object
      required: [success, request_id, data]
      properties:
        success:    { type: boolean, enum: [true] }
        request_id: { type: string, description: "X-Request-ID 對應值" }
        data:       {}                                         # endpoint 各自 override
        meta:       { $ref: "#/components/schemas/PageMeta" }  # 僅 list 端點出現

    ErrorResponse:
      type: object
      required: [success, request_id, error]
      properties:
        success:    { type: boolean, enum: [false] }
        request_id: { type: string }
        error:      { type: string, description: "snake_case error code（見 §12.4）" }
        details:
          type: array
          description: "僅 validation 錯誤時出現"
          items: { $ref: "#/components/schemas/FieldError" }

    PageMeta:
      type: object
      required: [page, page_size, total]
      properties:
        page:      { type: integer, minimum: 1 }
        page_size: { type: integer, minimum: 1, maximum: 100 }
        total:     { type: integer, format: int64, minimum: 0 }

    FieldError:
      type: object
      required: [field, message]
      properties:
        field:   { type: string }
        message: { type: string }

    # ─── Auth domain schemas ────────────────────────────────────────
    ClientID:
      type: string
      enum: [cms-web, public-web, ios-app, android-app]
      description: "Login / Register 必填；對應 JWTConfig.ClientPolicies 的 key（見 §4.2 / §8.1）"

    TokenPair:
      type: object
      required: [access_token, refresh_token, token_type, expires_in, refresh_expires_in]
      properties:
        access_token:       { type: string, description: "JWT，純 stateless 驗證（§8.1）" }
        refresh_token:      { type: string, description: "JWT，每次 rotation 換新 jti（§8.2）" }
        token_type:         { type: string, enum: [Bearer] }
        expires_in:         { type: integer, description: "access TTL（秒）" }
        refresh_expires_in: { type: integer, description: "refresh TTL（秒）；rotation 重設" }

    SessionInfo:
      type: object
      required: [fid, client_id, device_label, ip_at_login, created_at, last_rotated_at, is_current]
      properties:
        fid:             { type: string, format: uuid }
        client_id:       { $ref: "#/components/schemas/ClientID" }
        device_label:    { type: string, description: "UA parser 產生，例：Chrome 120 on macOS" }
        ip_at_login:     { type: string }
        created_at:      { type: string, format: date-time }
        last_rotated_at: { type: string, format: date-time }
        is_current:      { type: boolean, description: "當前 access token 所屬 family 為 true" }
```

#### 3.5.2 共用錯誤回應參考（避免每個 endpoint 重複展開）

```yaml
components:
  responses:
    Error400InvalidInput:
      description: "request body 解析失敗 / validation 失敗（含 details）"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            invalid_input:    { value: { success: false, request_id: "...", error: "invalid input" } }
    Error400InvalidClient:
      description: "client_id 不在白名單"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            invalid_client:   { value: { success: false, request_id: "...", error: "invalid_client" } }
    Error401Auth:
      description: "access token 驗證失敗（多種子型別）"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            unauthorized:     { value: { success: false, request_id: "...", error: "unauthorized" } }
            token_expired:    { value: { success: false, request_id: "...", error: "token_expired" } }
            invalid_token:    { value: { success: false, request_id: "...", error: "invalid_token" } }
            session_revoked:  { value: { success: false, request_id: "...", error: "session_revoked" } }
    Error401Refresh:
      description: "refresh token 驗證失敗（多種子型別）"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            token_expired:     { value: { success: false, request_id: "...", error: "token_expired" } }
            absolute_expired:  { value: { success: false, request_id: "...", error: "absolute_expired" } }
            invalid_token:     { value: { success: false, request_id: "...", error: "invalid_token" } }
            replay_detected:   { value: { success: false, request_id: "...", error: "replay_detected" } }
            session_not_found: { value: { success: false, request_id: "...", error: "session_not_found" } }
    Error403Forbidden:
      description: "權限不足（RequireRole / RequireOwnership）"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
    Error404NotFound:
      description: "目標 family / 資源不存在"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
    Error409UsernameTaken:
      description: "Register 時 cms_users 已存在同名"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            username_taken: { value: { success: false, request_id: "...", error: "username_taken" } }
    Error422WeakPassword:
      description: "弱密碼（規則見 §8.9）"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
          examples:
            weak_password: { value: { success: false, request_id: "...", error: "weak_password" } }
    Error429TooMany:
      description: "限流（IP 層或 user 層）"
      headers:
        Retry-After:
          schema: { type: integer, minimum: 1 }
          description: "建議重試秒數"
      content:
        application/json:
          schema: { $ref: "#/components/schemas/ErrorResponse" }
```

#### 3.5.3 Auth endpoints

```yaml
paths:
  /auth/register:
    post:
      summary: CMS 自註冊（僅 client_id=cms-web）
      description: |
        建立 CMS user，預設 role=user。不簽 token；caller 須另打 /auth/login。
        詳見 §8.9 AuthService.Register。
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [username, password, client_id]
              properties:
                username:  { type: string, minLength: 3, maxLength: 64 }
                password:  { type: string, minLength: 8, maxLength: 256 }
                client_id: { $ref: "#/components/schemas/ClientID" }
      responses:
        "201":
          description: "註冊成功（無 body）"
        "400": { $ref: "#/components/responses/Error400InvalidInput" }
        "409": { $ref: "#/components/responses/Error409UsernameTaken" }
        "422": { $ref: "#/components/responses/Error422WeakPassword" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/login:
    post:
      summary: 帳密登入
      description: 依 client_id 路由到 cms_users / members；成功回 token pair（見 §8.2）。
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [username, password, client_id]
              properties:
                username:  { type: string, minLength: 1, maxLength: 128 }
                password:  { type: string, minLength: 1, maxLength: 256 }
                client_id: { $ref: "#/components/schemas/ClientID" }
      responses:
        "200":
          description: "登入成功"
          content:
            application/json:
              schema:
                allOf:
                  - $ref: "#/components/schemas/SuccessEnvelope"
                  - type: object
                    properties:
                      data: { $ref: "#/components/schemas/TokenPair" }
        "400": { $ref: "#/components/responses/Error400InvalidInput" }
        "401": { $ref: "#/components/responses/Error401Auth" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/refresh:
    post:
      summary: Token rotation（family-based + replay detection）
      description: |
        依 ADR-007 family rotation 流程：
        Rotated / GraceHit 回 200 新 token pair；ReplayDetected / FamilyNotFound 回 401。
        詳見 §8.2 / §8.3.1。
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [refresh_token]
              properties:
                refresh_token: { type: string }
      responses:
        "200":
          description: "rotation 成功（含 GraceHit）"
          content:
            application/json:
              schema:
                allOf:
                  - $ref: "#/components/schemas/SuccessEnvelope"
                  - type: object
                    properties:
                      data: { $ref: "#/components/schemas/TokenPair" }
        "400": { $ref: "#/components/responses/Error400InvalidInput" }
        "401": { $ref: "#/components/responses/Error401Refresh" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/logout:
    post:
      summary: 廢當前 family + 黑名單當前 access JTI
      security: [{ bearerAuth: [] }]
      requestBody:
        required: false
        description: "optional；非空時 server 驗 fid 與 access claims 一致（§8.2）"
        content:
          application/json:
            schema:
              type: object
              properties:
                refresh_token: { type: string }
      responses:
        "204": { description: "登出成功（無 body）" }
        "400": { $ref: "#/components/responses/Error400InvalidInput" }
        "401": { $ref: "#/components/responses/Error401Auth" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/sessions:
    get:
      summary: 列出當前 user 全部裝置 session
      description: "lazy cleanup 孤兒 fid；is_current=true 為當前裝置（§8.9 ListSessions）"
      security: [{ bearerAuth: [] }]
      responses:
        "200":
          description: "session 清單"
          content:
            application/json:
              schema:
                allOf:
                  - $ref: "#/components/schemas/SuccessEnvelope"
                  - type: object
                    properties:
                      data:
                        type: array
                        items: { $ref: "#/components/schemas/SessionInfo" }
                      meta: { $ref: "#/components/schemas/PageMeta" }
        "401": { $ref: "#/components/responses/Error401Auth" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/sessions/{fid}:
    delete:
      summary: 撤銷指定 family
      description: "不可撤自己當前 family（請改打 /auth/logout）；對應 §8.9 RevokeSession"
      security: [{ bearerAuth: [] }]
      parameters:
        - name: fid
          in: path
          required: true
          schema: { type: string, format: uuid }
      responses:
        "204": { description: "撤銷成功" }
        "401": { $ref: "#/components/responses/Error401Auth" }
        "403":
          description: "嘗試撤自己當前 family（use_logout_instead）"
          content:
            application/json:
              schema: { $ref: "#/components/schemas/ErrorResponse" }
              examples:
                use_logout_instead:
                  value: { success: false, request_id: "...", error: "forbidden" }
        "404": { $ref: "#/components/responses/Error404NotFound" }
        "429": { $ref: "#/components/responses/Error429TooMany" }

  /auth/sessions/revoke-all:
    post:
      summary: 全裝置登出（含當前 access JTI 入黑名單）
      description: |
        對應 §8.9 RevokeAll；同步把當前 access JTI 加入黑名單（ttl=剩餘 exp），
        否則當前 access token 仍可用到自然過期（最長 15 分鐘）。
      security: [{ bearerAuth: [] }]
      responses:
        "204": { description: "全裝置登出成功" }
        "401": { $ref: "#/components/responses/Error401Auth" }
        "429": { $ref: "#/components/responses/Error429TooMany" }
```

#### 3.5.4 錯誤碼對應快查（與 §12.4 同步）

| Endpoint | 可能 error 字串 |
|---|---|
| `POST /auth/register` | `invalid input`、`invalid_client`、`username_taken`、`weak_password`、`too many requests` |
| `POST /auth/login` | `invalid input`、`invalid_client`、`unauthorized`、`too many requests` |
| `POST /auth/refresh` | `invalid input`、`token_expired`、`absolute_expired`、`invalid_token`、`replay_detected`、`session_not_found`、`too many requests` |
| `POST /auth/logout` | `invalid input`、`unauthorized`、`token_expired`、`invalid_token`、`session_revoked`、`too many requests` |
| `GET /auth/sessions` | `unauthorized`、`token_expired`、`invalid_token`、`too many requests` |
| `DELETE /auth/sessions/{fid}` | 上列 + `forbidden`、`resource not found` |
| `POST /auth/sessions/revoke-all` | `unauthorized`、`token_expired`、`invalid_token`、`too many requests` |

> **schema 變更流程**：依 §3.1 — 先改 `schema/openapi.yaml`，再改 handler E2E test，最後改實作。
> handler 引用的 `schema.RegisterRequest` / `schema.LoginRequest` / `schema.LogoutRequest` 等 Go 型別
> 由本節 yaml 對應（手寫；§3.2 註：endpoint > 30 個時改用 `oapi-codegen`）。

---

## 4. Config 模組

### 4.1 設計原則

- 單一 `Config` struct，所有模組從此取值，禁止全域變數散落各處。
- 讀取優先順序：**環境變數 > `.env` 檔 > `config.yaml` > `SetDefault` 預設值**。
- 啟動時若必填欄位缺失或值不合法，立即 `fatal` 退出，禁止以錯誤預設值繼續執行。
- 跨欄位邏輯約束（如 CORS 萬用字元 + AllowCredentials 互斥）以 `Validate()` 方法檢查。

### 4.2 Config 結構

```go
// config/config.go
//
// 巢狀 struct 一律使用 `mapstructure:",squash"`，
// 讓所有欄位攤平到根層，env 變數可直接以 PORT、DB_HOST 等扁平名稱寫入。
// 若不 squash，viper 會把 cfg.Server.Port 對應到 "Server.PORT"，
// 環境變數 PORT 不會被 Unmarshal 讀到。
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
    Env string `mapstructure:"APP_ENV" validate:"oneof=dev staging prod"` // 用於選擇 config.{env}.yaml
}

type ServerConfig struct {
    Port              int           `mapstructure:"PORT" validate:"required,min=1,max=65535"`
    GinMode           string        `mapstructure:"GIN_MODE" validate:"oneof=debug release test"`
    AllowedOrigins    []string      `mapstructure:"ALLOWED_ORIGINS" validate:"required,dive,required"`
    AllowCredentials  bool          `mapstructure:"ALLOW_CREDENTIALS"`
    TrustedProxies    []string      `mapstructure:"TRUSTED_PROXIES"`    // CIDR；空陣列代表完全不信任 proxy header
    ShutdownTimeout   time.Duration `mapstructure:"SHUTDOWN_TIMEOUT"    validate:"required,min=1s"`   // 預設 10s；0 會讓 graceful shutdown 立即 cancel
    ReadHeaderTimeout time.Duration `mapstructure:"READ_HEADER_TIMEOUT" validate:"required,min=1s"`   // 預設 10s，防 Slowloris
    ReadTimeout       time.Duration `mapstructure:"READ_TIMEOUT"        validate:"required,min=1s"`   // 預設 30s
    WriteTimeout      time.Duration `mapstructure:"WRITE_TIMEOUT"       validate:"required,min=1s"`   // 預設 30s
    IdleTimeout       time.Duration `mapstructure:"IDLE_TIMEOUT"        validate:"required,min=1s"`   // 預設 120s
    MaxRequestBody    int64         `mapstructure:"MAX_REQUEST_BODY"    validate:"required,min=1024"` // bytes，預設 1MB；0 會讓 MaxBytesReader 把所有 body 視為超限 (413)
}

type DatabaseConfig struct {
    Host             string        `mapstructure:"DB_HOST"     validate:"required"`
    Port             int           `mapstructure:"DB_PORT"     validate:"required,min=1,max=65535"`
    User             string        `mapstructure:"DB_USER"     validate:"required"`
    Password         string        `mapstructure:"DB_PASSWORD" validate:"required"`
    Name             string        `mapstructure:"DB_NAME"     validate:"required"`
    SSLMode          string        `mapstructure:"DB_SSLMODE"  validate:"oneof=disable require verify-ca verify-full"`
    MaxOpenConns     int           `mapstructure:"DB_MAX_OPEN_CONNS"`
    MaxIdleConns     int           `mapstructure:"DB_MAX_IDLE_CONNS"`
    ConnMaxLifetime  time.Duration `mapstructure:"DB_CONN_MAX_LIFETIME"`
    ConnectTimeout   time.Duration `mapstructure:"DB_CONNECT_TIMEOUT"`    // PG connect_timeout
    StatementTimeout time.Duration `mapstructure:"DB_STATEMENT_TIMEOUT"`  // 單 query 上限，防慢查打爆連線池
    PrepareStmt      bool          `mapstructure:"DB_PREPARE_STMT"`       // 直連 PG = true；走 PgBouncer transaction mode 必須 false（見 §6.1）
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

// JWTConfig — 詳細設計見 ADR 007（Refresh Token Rotation 與重放偵測）
//
// 簽署演算法：HS256（兩組秘鑰互相獨立，未來多服務時升級 RS256）。
// 每個 client（cms-web / public-web / ios-app …）有獨立的 refresh / absolute TTL 政策。
// Login request 攜帶 client_id，server 對照 ClientPolicies 取出對應 policy。
//
// ⚠️ 環境變數命名規範：
// shell 環境變數不允許包含 "." 或 "-"，因此 client policy env 一律以底線 + 全大寫表示，
// 例如 cms-web 的 refresh ttl 寫為 JWT_CLIENT_POLICIES_CMS_WEB_REFRESH_TTL。
// 載入時使用 viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))，
// 同時保留 ClientPolicies 內部 key 仍為「cms-web」這種 client_id 字面值。
type JWTConfig struct {
    Issuer                string                  `mapstructure:"JWT_ISSUER" validate:"required"`              // 寫入 token iss claim；驗證時必須相符
    Secret                string                  `mapstructure:"JWT_SECRET" validate:"required,min=32"`       // access token HS256 主 secret
    PreviousSecret        string                  `mapstructure:"JWT_SECRET_PREVIOUS" validate:"omitempty,min=32,nefield=Secret"` // 上一把 access secret；rotation grace 期間用於 verify fallback。若設定，強度必須等同主 secret 且不可與主 secret 相同
    RefreshSecret         string                  `mapstructure:"JWT_REFRESH_SECRET" validate:"required,min=32,nefield=Secret"`
    PreviousRefreshSecret string                  `mapstructure:"JWT_REFRESH_SECRET_PREVIOUS" validate:"omitempty,min=32,nefield=RefreshSecret"` // 上一把 refresh secret；同上
    AccessTTL             time.Duration           `mapstructure:"JWT_ACCESS_TTL"          validate:"required,min=1m"`   // 預設 15m（短期 access token）
    GraceWindow           time.Duration           `mapstructure:"JWT_GRACE_WINDOW"        validate:"min=0,max=1m"`      // 預設 10s；0 = 停用 grace（重試一律觸發 replay）；上限 1m 避免攻擊窗過長
    ClockSkewLeeway       time.Duration           `mapstructure:"JWT_CLOCK_SKEW_LEEWAY" validate:"min=0,max=2m"` // 預設 30s。Verify 對 exp / nbf / iat / abs_exp 含此 leeway，容忍多副本部署的時鐘漂移
    ClientPolicies        map[string]ClientPolicy `mapstructure:"JWT_CLIENT_POLICIES"`   // key = client_id（cms-web / public-web / ios-app）
    BcryptCost            int                     `mapstructure:"BCRYPT_COST" validate:"min=10,max=15"` // 預設 12
}

// ClientPolicy — 不同 client 套不同 refresh / 絕對上限。
// RefreshTTL：refresh token 的滑動 exp，rotation 重新計算。
// AbsoluteTTL：family 的絕對最長壽命，rotation 不會延長。
type ClientPolicy struct {
    RefreshTTL  time.Duration `mapstructure:"REFRESH_TTL"  validate:"required,min=1m"`
    AbsoluteTTL time.Duration `mapstructure:"ABSOLUTE_TTL" validate:"required,gtfield=RefreshTTL"`
}

type LogConfig struct {
    Level     string `mapstructure:"LOG_LEVEL" validate:"oneof=debug info warn error"`
    Format    string `mapstructure:"LOG_FORMAT" validate:"oneof=json console"`
    Service   string `mapstructure:"LOG_SERVICE"`     // 注入每筆日誌的 service 欄位
    AuditPath string `mapstructure:"LOG_AUDIT_PATH"`  // audit logger sink；空 → 共用 stdout；非空 → 寫該檔案（詳見 §18.3.1）
}

type RateLimitConfig struct {
    Enabled  bool          `mapstructure:"RATE_LIMIT_ENABLED"`
    // IP 限流：所有人都受限（含未登入）
    IPPeriod time.Duration `mapstructure:"RATE_LIMIT_IP_PERIOD" validate:"omitempty,min=1s"`
    IPLimit  int64         `mapstructure:"RATE_LIMIT_IP_MAX"    validate:"omitempty,min=1"`
    // User 限流：登入後額外套用，額度可較寬鬆
    UserPeriod time.Duration `mapstructure:"RATE_LIMIT_USER_PERIOD" validate:"omitempty,min=1s"`
    UserLimit  int64         `mapstructure:"RATE_LIMIT_USER_MAX"    validate:"omitempty,min=1"`
}
// Enabled=true 時，IPPeriod / IPLimit / UserPeriod / UserLimit 全部必填且 > 0；由 Validate() 跨欄位檢查（struct tag 無法在 Enabled=true 條件下要求其他欄位 required）。
// 對應的 ratelimit.IPMiddleware / UserMiddleware 各取一組 (Period, Limit) 使用，見 §15

type MetricsConfig struct {
    Enabled bool   `mapstructure:"METRICS_ENABLED"`
    Path    string `mapstructure:"METRICS_PATH"` // 預設 /metrics
}

// Validate 處理 struct tag 無法表達的跨欄位約束。
// 所有規則一律 fail-fast — 啟動立即退出，禁止以「降級設定」繼續執行。
func (c *Config) Validate() error {
    for _, origin := range c.Server.AllowedOrigins {
        if origin == "*" && c.Server.AllowCredentials {
            return errors.New("ALLOW_CREDENTIALS=true 時 ALLOWED_ORIGINS 不可為 *（瀏覽器規範禁止）")
        }
    }
    // Production 禁止 sslmode=disable（明文連線 DB；對齊 §6.1）
    if c.App.Env == "prod" && c.Database.SSLMode == "disable" {
        return errors.New("APP_ENV=prod 禁止 DB_SSLMODE=disable，必須 require / verify-ca / verify-full")
    }
    // Production 必須 GIN_MODE=release（debug / test 模式會洩漏 stack trace 與 panic 細節）
    if c.App.Env == "prod" && c.Server.GinMode != "release" {
        return errors.New("APP_ENV=prod 必須 GIN_MODE=release，禁止 debug / test")
    }
    // RateLimit 啟用時，四個參數必須齊備且 > 0
    if c.RateLimit.Enabled {
        if c.RateLimit.IPPeriod < time.Second || c.RateLimit.IPLimit < 1 {
            return errors.New("RATE_LIMIT_ENABLED=true 時 RATE_LIMIT_IP_PERIOD ≥ 1s 且 RATE_LIMIT_IP_MAX ≥ 1")
        }
        if c.RateLimit.UserPeriod < time.Second || c.RateLimit.UserLimit < 1 {
            return errors.New("RATE_LIMIT_ENABLED=true 時 RATE_LIMIT_USER_PERIOD ≥ 1s 且 RATE_LIMIT_USER_MAX ≥ 1")
        }
    }
    return nil
}
```

### 4.3 載入流程

```go
// config/config.go
func Load() (*Config, error) {
    v := viper.New()

    // 1. 預設值（最低優先序）
    setDefaults(v)

    // 2. .env（可選；本地開發用）
    //    godotenv 將 KEY=VALUE 直接注入 os.Environ；後續 AutomaticEnv 即可讀取。
    //    CI / production 無 .env 檔屬正常情況，errors.Is(err, os.ErrNotExist) 直接放行。
    if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
        return nil, fmt.Errorf("load .env: %w", err)
    }

    // 3. config.yaml — 依 APP_ENV 切換不同檔（config.yaml / config.staging.yaml / config.prod.yaml）
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

    // 4. 環境變數（最高優先序）
    //    搭配 ",squash" 標籤後，env 名即為扁平的 mapstructure key（PORT、DB_HOST...）
    //
    //    SetEnvKeyReplacer 把 mapstructure key 中的 "." 與 "-" 都換成 "_"，
    //    才能讓 ClientPolicies 這種巢狀 map 透過合法的 shell env 名稱注入。
    //    例：mapstructure key "JWT_CLIENT_POLICIES.cms-web.REFRESH_TTL"
    //        對應 shell env JWT_CLIENT_POLICIES_CMS_WEB_REFRESH_TTL
    v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
    v.AutomaticEnv()

    // 4.1 顯式 BindEnv（**必要**，Viper 已知限制）
    //    AutomaticEnv() 在 Unmarshal() 時不會自動讀環境變數（只在 Get*() 系列生效），
    //    必須對每個 key 呼叫 BindEnv 才能讓 Unmarshal 路徑感知到 env 注入的值。
    //    所有 mapstructure key 集中在 bindEnvVars(v) 顯式宣告（保持單一來源）。
    bindEnvVars(v)

    // 5. Unmarshal + struct tag 驗證
    //    intSecondsToTimeDurationHookFunc 先攔截純整數（.env 慣例：所有 duration 用秒）；
    //    StringToTimeDurationHookFunc 接手帶單位字串（"15m"），供 YAML 設定檔使用。
    var cfg Config
    if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
        intSecondsToTimeDurationHookFunc(),
        mapstructure.StringToTimeDurationHookFunc(),
        mapstructure.StringToSliceHookFunc(","),
    ))); err != nil {
        return nil, fmt.Errorf("unmarshal config: %w", err)
    }
    // NewValidator() 是專案共用的 validator instance（封裝 validator.New() + 自訂 tag
    // 註冊），避免每處呼叫各自重複設定；tests 也共用同一份規則。
    if err := NewValidator().Struct(&cfg); err != nil {
        return nil, fmt.Errorf("validate config: %w", err)
    }
    // 6. 跨欄位驗證
    if err := cfg.Validate(); err != nil {
        return nil, err
    }
    return &cfg, nil
}

// duration 慣例：所有 duration 預設值與 .env 值一律用整數秒（不帶單位）。
// intSecondsToTimeDurationHookFunc decode hook 負責將整數轉為 time.Duration。
// YAML 設定檔仍可使用帶單位字串（"15m"），由 StringToTimeDurationHookFunc 接手。
func setDefaults(v *viper.Viper) {
    v.SetDefault("APP_ENV", "dev")
    v.SetDefault("GIN_MODE", "release")
    v.SetDefault("SHUTDOWN_TIMEOUT", 10)
    v.SetDefault("READ_HEADER_TIMEOUT", 10)
    v.SetDefault("READ_TIMEOUT", 30)
    v.SetDefault("WRITE_TIMEOUT", 30)
    v.SetDefault("IDLE_TIMEOUT", 120)
    v.SetDefault("MAX_REQUEST_BODY", 1<<20) // 1MB
    v.SetDefault("DB_PORT", 5432)
    v.SetDefault("DB_SSLMODE", "disable")
    v.SetDefault("DB_MAX_OPEN_CONNS", 25)
    v.SetDefault("DB_MAX_IDLE_CONNS", 5)
    v.SetDefault("DB_CONN_MAX_LIFETIME", 300)  // 5m
    v.SetDefault("DB_CONNECT_TIMEOUT", 5)
    v.SetDefault("DB_STATEMENT_TIMEOUT", 10)
    v.SetDefault("DB_PREPARE_STMT", true)
    v.SetDefault("REDIS_PORT", 6379)
    v.SetDefault("REDIS_DIAL_TIMEOUT", 5)
    v.SetDefault("REDIS_READ_TIMEOUT", 3)
    v.SetDefault("REDIS_WRITE_TIMEOUT", 3)
    v.SetDefault("REDIS_POOL_SIZE", 10)
    v.SetDefault("JWT_ISSUER", "playerledger")
    v.SetDefault("JWT_ACCESS_TTL", 900)   // 15m
    v.SetDefault("JWT_GRACE_WINDOW", 10)
    v.SetDefault("JWT_CLOCK_SKEW_LEEWAY", 30)
    // ClientPolicies 預設值。
    // mapstructure key 用 "." / "-"（給 viper 解析巢狀 map 與保留原始 client_id 字面值）；
    // shell env 對應名稱為 JWT_CLIENT_POLICIES_<CLIENT_ID>_REFRESH_TTL 等（"." / "-" → "_"）。
    v.SetDefault("JWT_CLIENT_POLICIES.cms-web.REFRESH_TTL", 3600)     // 1h
    v.SetDefault("JWT_CLIENT_POLICIES.cms-web.ABSOLUTE_TTL", 28800)   // 8h
    v.SetDefault("JWT_CLIENT_POLICIES.public-web.REFRESH_TTL", 3600)
    v.SetDefault("JWT_CLIENT_POLICIES.public-web.ABSOLUTE_TTL", 86400)   // 24h
    v.SetDefault("JWT_CLIENT_POLICIES.ios-app.REFRESH_TTL", 2592000)     // 720h = 30d
    v.SetDefault("JWT_CLIENT_POLICIES.ios-app.ABSOLUTE_TTL", 15552000)   // 4320h = 180d
    v.SetDefault("JWT_CLIENT_POLICIES.android-app.REFRESH_TTL", 2592000)
    v.SetDefault("JWT_CLIENT_POLICIES.android-app.ABSOLUTE_TTL", 15552000)
    v.SetDefault("BCRYPT_COST", 12)
    v.SetDefault("LOG_LEVEL", "info")
    v.SetDefault("LOG_FORMAT", "json")
    v.SetDefault("METRICS_PATH", "/metrics")
}

// intSecondsToTimeDurationHookFunc converts bare integer values (and plain integer
// strings as written in .env) to time.Duration by treating them as seconds.
// Strings with unit suffixes ("15m", "10s") are passed through unchanged for
// StringToTimeDurationHookFunc to handle.
func intSecondsToTimeDurationHookFunc() mapstructure.DecodeHookFuncType {
    return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
        if to != reflect.TypeOf(time.Duration(0)) {
            return data, nil
        }
        switch v := data.(type) {
        case string:
            n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
            if err != nil {
                return data, nil
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
```

> **環境分層**：`APP_ENV=dev` 讀 `config.yaml`；`APP_ENV=staging` 讀 `config.staging.yaml`；`APP_ENV=prod` 讀 `config.prod.yaml`。由 deployment manifest（k8s ConfigMap / docker env）注入。本機開發 `.env` 與 yaml 互補。

---

## 5. Logger 模組

### 5.1 設計原則

- 全域單例 `*zap.Logger`，透過 `logger.L()` 取得，避免依賴注入冗餘。
- 結構化日誌（預設 JSON），每條日誌自動帶 `request_id`、`service`、`env` 欄位。
- 健康檢查路徑（`/health`、`/health/ready`、`/metrics`）排除於 access log，避免污染。

### 5.2 初始化

```go
// pkg/logger/logger.go

// 全域 logger 內部以 atomic.Pointer[zap.Logger] 儲存，**Init 之前 L() 回 zap.NewNop()**，
// 避免 init 順序錯誤（例如某 pkg.Connect 在 logger.Init 之前 capture L()）導致 nil deref。
// Init 後以 atomic.Store 切換，呼叫端 capture 後仍指到舊 instance（zap.Logger 本身 thread-safe）。

// Init 用 service + env 兩個 baseFields 初始化全域 logger。
// env 由 cfg.App.Env 提供（單一真實來源，不另開 LOG_ENV）。
// 重複呼叫 Init 為 no-op + warn，不允許 runtime 替換 logger instance。
func Init(logCfg config.LogConfig, env string) error

// L 取得全域 logger（含 service / env baseFields）。
// Init 之前回 zap.NewNop()（safe no-op）；Init 後回真實 logger。
func L() *zap.Logger

// With 在全域 logger 上加 fields；同樣 Init 之前回 nop logger.With(...)。
func With(fields ...zap.Field) *zap.Logger
```

### 5.3 Gin 中介層

```go
// pkg/logger/middleware.go

// GinLogger 記錄每個 request 的 access log。
// skipPaths 中的路徑不會寫入（健康檢查、metrics）。
//
// 寫入欄位（固定，TDD 可直接以此為斷言）：
//   - method        string  — c.Request.Method
//   - path          string  — c.FullPath()（已模板化，避免 path param 高基數爆炸 metrics 與 log）
//   - status        int     — c.Writer.Status()
//   - latency_ms    int64   — time.Since(start).Milliseconds()
//   - client_ip     string  — c.ClientIP()（依 TrustedProxies §9.2）
//   - user_agent    string  — c.Request.UserAgent()
//   - bytes_in      int64   — c.Request.ContentLength（unknown 時為 -1）
//   - bytes_out     int     — c.Writer.Size()
//   - request_id    string  — logger.GetRequestID(c)
//   - errors        string  — c.Errors.ByType(gin.ErrorTypePrivate).String()（非空才寫）
//
// 不寫的欄位（刻意省略）：
//   - query string：可能含 token / 密碼等 sensitive value，**預設不寫**；若需 debug，由 caller 顯式 enable
//   - request body：access log 不該帶 body（量大、易洩漏 PII）
//   - response body：同上
//
// log level 依 status：5xx → Error；4xx → Warn；其他 → Info。
func GinLogger(skipPaths ...string) gin.HandlerFunc
```

> **GinRecovery 不在此**：v1.9 起 `GinRecovery` 移至 `pkg/httpx`（見 §9.3）。
> 原因：recovery 要寫回應，必須依賴統一的 `httpx.WriteError`；而 `httpx` 自身又依賴 `logger.GetRequestID`，
> 若把 recovery 留在 `pkg/logger` 內呼叫 `httpx.WriteError` 會形成 `logger ↔ httpx` 循環依賴。
> 解法：保持單向依賴 `httpx → logger`，把所有 HTTP 中介層（recovery / secure headers / body limit / error helper）
> 統一聚集在 `pkg/httpx`，`pkg/logger` 只剩跟「日誌本體」相關的東西（含 `GinLogger` 與 `RequestID`）。

掛載順序見 §9.2。

### 5.4 Request ID 模組

跨 gin / context.Context 的 request_id 傳遞，分成兩個 package：

- **`pkg/ctxkey`**：純 context.Context typed key 與 Get / Set helper（最底層、無 import）。讓 `pkg/audit` 等不該依賴 `pkg/logger` 的模組也能取 request_id（§2.1）。
- **`pkg/logger`**：RequestID middleware（負責產生 / 注入）+ gin.Context 版的 `GetRequestID`。

```go
// pkg/ctxkey/ctxkey.go
package ctxkey

import "context"

const RequestIDHeader = "X-Request-ID"

// requestIDKey 為 context.Context 的 typed key（unexported，避免外部誤造同型 key 碰撞）。
type requestIDKey struct{}

// SetRequestID 注入 request_id 到 ctx；由 RequestID middleware 在 request 入口呼叫。
func SetRequestID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID 從 ctx 取 request_id；未注入（例如 background goroutine）回空字串。
// 給下游純 context.Context 介面用（service / audit logger / repository）。
func RequestID(ctx context.Context) string {
    if ctx == nil {
        return ""
    }
    s, _ := ctx.Value(requestIDKey{}).(string)
    return s
}
```

```go
// pkg/logger/requestid.go
package logger

import (
    "github.com/<org>/playerledger/pkg/ctxkey"
    // ...
)

const RequestIDKey = "request_id"   // gin.Context map key（gin 內部用 map[string]any，必須字串）

// RequestID 為每個 request 注入唯一 ID。
// 優先沿用上游或客戶端傳入的合法 X-Request-ID，否則自行產生 UUID v4。
// 不合法（超長、含控制字元）視同未提供，靜默產生新 ID。
//
// 同時把 ID 注入 gin.Context（給 c.Get 用）與 c.Request.Context()
// （給下游純 context.Context 介面用；透過 pkg/ctxkey 的 typed key）。
func RequestID() gin.HandlerFunc {
    return func(c *gin.Context) {
        id := c.GetHeader(ctxkey.RequestIDHeader)
        if !isValidRequestID(id) {
            id = uuid.New().String()
        }
        c.Set(RequestIDKey, id)
        c.Request = c.Request.WithContext(ctxkey.SetRequestID(c.Request.Context(), id))
        c.Header(ctxkey.RequestIDHeader, id) // 回寫，方便 debug
        c.Next()
    }
}

// isValidRequestID：非空、長度 ≤ 128、僅含可印 ASCII（0x21–0x7E），避免 log injection
func isValidRequestID(id string) bool {
    if id == "" || len(id) > 128 {
        return false
    }
    for _, r := range id {
        if r < 0x21 || r > 0x7E {
            return false
        }
    }
    return true
}

// GetRequestID 從 gin.Context 取 request_id。Handler / middleware 用（gin context 版）。
// 純 context.Context 版本請用 ctxkey.RequestID(ctx)。
func GetRequestID(c *gin.Context) string {
    id, _ := c.Get(RequestIDKey)
    s, _ := id.(string)
    return s
}
```

傳遞規則：

| 情境 | 行為 |
|------|------|
| 上游 / 客戶端傳入合法 `X-Request-ID` | 沿用該值 |
| 無 header | 自動產生 UUID v4 |
| header 不合法（超長 / 含控制字元） | 靜默忽略，自動產生 UUID v4 |
| 任何情況 | 寫入 response header、`gin.Context` key `"request_id"` |
| `GinLogger` | 自動加入 zap fields |
| `Response[T]` | 填入 `request_id` 欄位回傳給前端（見 §10） |

---

## 6. Database 模組（GORM）

### 6.1 設計原則

- `*gorm.DB` 透過依賴注入傳入 Repository，禁止全域直接存取。
- 使用 GORM v2，Model 定義在 `internal/model/`。
- **禁止使用 GORM AutoMigrate**，所有結構變更透過 golang-migrate 版本化腳本。
- `PrepareStmt` 由 `DB_PREPARE_STMT` 控制，預設 `true` 提升重複查詢效能；走 PgBouncer transaction mode 時必須設為 `false`（見下方警告）。
- DSN 內含 `connect_timeout` 與 `statement_timeout`，避免慢查與卡死。
- 生產環境 `SSLMode` 必須為 `require` 以上，`disable` 僅限本機開發。

> ⚠️ **與 PgBouncer 的相容性**：`PrepareStmt: true` 在 PgBouncer `pool_mode=transaction`（最常見的高併發模式）下會炸 `prepared statement "stmtcache_xxx" does not exist`，因為每個 transaction 隨機分配後端連線。
> 兩種解法擇一：
> 1. PgBouncer 改用 `pool_mode=session`（每連線綁定，連線池效益較差）
> 2. 應用層 `PrepareStmt: false`（每次查詢重新 parse，輕微 CPU 成本）
>
> 直連 PostgreSQL 或用 pgx 內建連線池則不受影響。

### 6.2 連線管理

```go
// pkg/database/database.go
func Connect(cfg config.DatabaseConfig) (*gorm.DB, error) {
    dsn := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s connect_timeout=%d statement_timeout=%d",
        cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode,
        int(cfg.ConnectTimeout.Seconds()),
        int(cfg.StatementTimeout.Milliseconds()),
    )

    db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
        PrepareStmt: cfg.PrepareStmt, // 直連 PG = true；走 PgBouncer transaction mode 必須 false
        Logger:      newGormLogger(),  // zapgorm2 包裝全域 zap logger
    })
    if err != nil {
        return nil, fmt.Errorf("gorm open: %w", err)
    }

    sqlDB, err := db.DB()
    if err != nil {
        return nil, fmt.Errorf("get sql.DB: %w", err)
    }
    sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
    sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
    sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
    return db, nil
}

// newGormLogger 整合 zap：將 GORM 內部日誌轉至全域 zap
func newGormLogger() gormlogger.Interface {
    return zapgorm2.New(logger.L()).LogMode(gormlogger.Warn)
}
```

### 6.3 Model 範例

```go
// internal/model/base.go
type Base struct {
    ID        uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
    CreatedAt time.Time
    UpdatedAt time.Time
    DeletedAt gorm.DeletedAt `gorm:"index"` // soft delete
}
```

> **UUID v4 vs v7**：本階段採 PG 內建 `gen_random_uuid()`（v4）以求最低依賴。2026 業界推 UUID v7（時間排序、B-tree 友善），可在 transaction、audit_log 等高 insert 量資料表評估改用 v7（PG 17+ 內建 `uuidv7()`，或應用層產生）。若評估改用，請另起 ADR 決定全表一致或部分採用。

### 6.4 Repository 介面規範

```go
// internal/repository/player_repository.go
type PlayerRepository interface {
    FindByID(ctx context.Context, id uuid.UUID) (*model.Player, error)
    Create(ctx context.Context, player *model.Player) error
    // ...
}

// 測試時用 FakePlayerRepository 替換，不 mock *gorm.DB
```

### 6.5 Auth 相關 Model / Repository

Auth 必需的兩張表 — `cms_users`（CMS 內部人員）與 `members`（一般玩家）— 雖屬業務 model，但 `AuthService.Login` / `Register`（見 §8.9）直接依賴；為避免實作時找不到 contract，定義納入基礎架構規格。

> **設計取捨**：
> - 兩張表結構刻意對齊（id、username、password_hash + 標準 timestamps），便於 `Hasher`（§8.3.2）與 audit log（§18.3）統一處理。
> - `members` **不放 `role` 欄位** — `utype=member ⇒ role=member` 是規則，由程式碼 enforce，避免 DB 重複資料。
> - **Member 註冊現階段不開放**，dev/test 透過 SQL 手動 seed；正式註冊機制（自註冊 / 邀請 / 外部 sync）待後續業務 spec 決定。`MemberRepository` 因此只暴露 `FindByUsername`，不提供 `Create`。
> - **CMS 開放自註冊**（見 §8.2 / §8.9 Register 流程），預設 role 為 `user`。`role` 升級／降級走 admin-only endpoint，留給後續業務 spec。

```go
// internal/model/cms_user.go
type CMSUser struct {
    Base                                   // id, created_at, updated_at, deleted_at（見 §6.3）
    Username     string `gorm:"size:64;not null;uniqueIndex:uq_cms_users_username,where:deleted_at IS NULL"`
    PasswordHash string `gorm:"size:72;not null"`  // bcrypt 輸出 60 byte + margin
    Role         string `gorm:"size:16;not null"`  // admin / user / viewer
}

func (CMSUser) TableName() string { return "cms_users" }

// internal/model/member.go
type Member struct {
    Base
    Username     string `gorm:"size:64;not null;uniqueIndex:uq_members_username,where:deleted_at IS NULL"`
    PasswordHash string `gorm:"size:72;not null"`
}

func (Member) TableName() string { return "members" }
```

```go
// internal/repository/cms_user_repository.go
type CMSUserRepository interface {
    // FindByUsername：找不到回 apperr.ErrNotFound；DB 錯誤一律 fmt.Errorf("find cms user: %w", err)。
    FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)

    // Create：username 已存在回 apperr.ErrConflict（依 §12.5 unique constraint 23505 包裝）。
    // 由 AuthService 在 Hasher.Hash(password) 後呼叫，禁止直接傳明文密碼。
    Create(ctx context.Context, u *model.CMSUser) error
}

// internal/repository/member_repository.go
type MemberRepository interface {
    // FindByUsername：找不到回 apperr.ErrNotFound。
    // 不提供 Create — Member 註冊流程現階段不開放（見上方設計取捨）。
    FindByUsername(ctx context.Context, username string) (*model.Member, error)
}
```

> **Username 唯一性**：採**各表獨立 unique**（partial unique index 排除 soft-delete 列）。同一個 username 可同時是 CMS user 與 member（不同人），application 層**不做**跨表檢查。
>
> **Partial unique index**：`WHERE deleted_at IS NULL` 子句讓「軟刪後同 username 可重註冊」成立，是 PostgreSQL 慣用做法。GORM v2 透過 `uniqueIndex:<name>,where:<expr>` tag 表達；對應 migration SQL 必須一致（見 §13.2 範例）。

---

## 7. Redis 模組

### 7.1 設計原則

- 用途：JWT 短期黑名單、**Refresh Token Family Store**（見 ADR 007）、Rate Limiting、短期快取。
- `redis.Client` 透過依賴注入傳入需要的模組。
- **Key 命名與 hash tag**：跨 key 操作（Lua script）必須讓所有 key 落在同一個 Redis Cluster slot，採用 `{<hash-tag>}` 鎖定。
  - 同 user 的 family 相關 key：`auth:family:{<userID>}:<fid>`、`auth:user_families:{<userID>}`
  - 同 user 的 rate limit：`ratelimit:user:{<userID>}`
  - 單 key 操作可不加 tag：`auth:blacklist:<accessJTI>`
  - 即使目前部署單機 Redis，也預先採用此規範，未來水平擴展至 Cluster 不用改 key 設計。
- **涉及多 key 的寫入必須以 Lua script 確保原子性**，禁止以 Pipeline 替代（Pipeline 僅批次送出，不保證原子）。
- **Auth 用 Redis 必須設 `maxmemory-policy noeviction`**：family key 被誤砍 = 合法使用者被踢，必須避免。建議 auth 使用獨立 Redis instance 或專屬 DB index。

### 7.2 連線管理

```go
// pkg/redis/redis.go
func Connect(cfg config.RedisConfig) (*redis.Client, error) {
    client := redis.NewClient(&redis.Options{
        Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
        Password:     cfg.Password,
        DB:           cfg.DB,
        DialTimeout:  cfg.DialTimeout,
        ReadTimeout:  cfg.ReadTimeout,
        WriteTimeout: cfg.WriteTimeout,
        PoolSize:     cfg.PoolSize,
    })
    ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
    defer cancel()
    if _, err := client.Ping(ctx).Result(); err != nil {
        return nil, fmt.Errorf("redis ping: %w", err)
    }
    return client, nil
}
```

### 7.3 Access Token 短期黑名單介面

僅用於「強制踢人」場景（管理員手動踢、改密碼、family revoked 連帶）。日常 access token 驗證**不查黑名單**以維持 stateless 性能；只在需要立刻撤銷時加入，TTL = access token 剩餘 exp 秒數。

```go
// pkg/redis/blacklist.go

type AccessTokenBlacklist interface {
    // Add 將 jti 加入黑名單，存活 ttl 秒。
    //   - ttl <= 0：視為「token 已自然過期」，直接 no-op 不寫入。
    //   - Redis 寫入失敗：回包裝 error。caller（Logout / RevokeAll）應 log + metric，
    //     但不影響「廢 family」此一更關鍵步驟（family 已廢 = refresh 路徑已斷）。
    Add(ctx context.Context, jti string, ttl time.Duration) error

    // IsBlacklisted 查詢 jti 是否在黑名單。
    //   - 找不到 key                 → (false, nil)
    //   - 命中                       → (true, nil)
    //   - **Redis 故障（連線錯 / timeout）→ (false, err)**：由 caller fail-open。
    //     AuthMiddleware 收到 err 後 log warn + metrics.AuthBlacklistErrors.Inc() + 放行
    //     （hot path 不能因 Redis 抖動全打 401；強制踢人是稀有事件，short-term miss 可接受）。
    IsBlacklisted(ctx context.Context, jti string) (bool, error)
}

// NewAccessTokenBlacklist 由 *redis.Client 構造預設實作。
// 使用 SETEX + EXISTS 兩條原生命令，無 Lua（單 key 操作）。
func NewAccessTokenBlacklist(client *redis.Client) AccessTokenBlacklist
```

> Key 命名：`auth:blacklist:<accessJTI>`（單 key 操作，不需 hash tag）。
> 對應 metric `auth_blacklist_errors_total` 見 §18.2。

### 7.4 Refresh Token Family Store 介面

對應 ADR 007 的 family-based rotation + replay detection 設計。**Family 為登入 session 的單位**，多裝置 = 多 family，互不影響。

**Redis key 結構**（皆以 `{<userID>}` 為 hash tag）：

```
auth:family:{<userID>}:<fid>     → JSON FamilyState   TTL = abs_exp 剩餘秒數
auth:user_families:{<userID>}    → SET<fid>           無 TTL（手動清理）
```

```go
// pkg/redis/family_store.go

// FamilyState 完整描述一個 login session 的 server 端狀態。
// 序列化為 JSON 後存入 auth:family:{userID}:<fid>。
//
// AbsoluteExp 是 server 信任的 abs_exp 來源：rotation / grace 重簽 refresh token
// 時必須從這裡取，不能信任 client presented JWT，避免攻擊者改 token 內容延長 session。
// Lua 也用此值計算 Redis key TTL，無需 caller 額外傳入。
//
// UserType / Role 在 login 時隨 family 一起寫入，rotation 與 GraceHit 重簽
// access token 時直接讀 state，不必再打 DB（hot path 維持 stateless）。
// 取捨：使用者 role 變更後，最遲下一次 login 才會生效；舊 family 仍持有舊 role，
// 直到該 family 因 logout / refresh exp / abs_exp 自然結束。
// 若需立即生效，呼叫 RevokeAll 強制全裝置重登。
type FamilyState struct {
    UserID                string `json:"user_id"`
    FamilyID              string `json:"fid"`
    ClientID              string `json:"client_id"`               // = aud claim
    UserType              string `json:"utype"`                   // login 時固化；GraceHit / Rotated 重簽 access 用
    Role                  string `json:"role"`                    // login 時固化；同上
    CurrentJTI            string `json:"current_jti"`
    PreviousJTI           string `json:"previous_jti,omitempty"`             // grace window 用
    PreviousResponseUntil int64  `json:"previous_response_until,omitempty"`  // unix seconds；grace 截止
    AbsoluteExp           int64  `json:"abs_exp"`                            // unix seconds；rotation 不延長
    DeviceLabel           string `json:"device_label"`                       // 從 User-Agent 解析
    IPAtLogin             string `json:"ip_at_login"`
    CreatedAt             int64  `json:"created_at"`                         // unix seconds
    LastRotatedAt         int64  `json:"last_rotated_at"`                    // unix seconds
}

// RotateResult — Rotate Lua script 三種結果：
//   - Rotated      : 正常 rotation，已寫入新 jti
//   - GraceHit     : grace window 命中（網路重試），handler 用 state.CurrentJTI 重簽 refresh
//   - ReplayDetected: 觸發重放，family 已被刪除，handler 須回 401 + 寫 audit log
type RotateResult int

const (
    Rotated RotateResult = iota + 1
    GraceHit
    ReplayDetected
    FamilyNotFound // family key 不存在（已過期 / 已被廢）；handler 同樣回 401
)

// FamilyStore — Refresh Token Family 的原子操作介面。
// Save / Rotate / Revoke / RevokeAll 涉及多 key，皆以 Lua script 一次原子執行。
// ListByUser 採 lazy cleanup：讀取時順手 SREM 已過期的孤兒 fid。
type FamilyStore interface {
    // Save：login 時建立新 family（同時 SADD 入 user_families 索引）
    Save(ctx context.Context, state FamilyState) error

    // Rotate：原子 CAS — 驗證 presented_jti、更新 current/previous、設定 grace window。
    // Lua 從 state 內部讀 AbsoluteExp 計算 Redis TTL；觸發重放時 Lua 自動 DEL family + SREM 索引。
    //
    // 回傳 invariant：
    //   - Rotated         → (Rotated, *FamilyState non-nil, nil)
    //   - GraceHit        → (GraceHit, *FamilyState non-nil, nil)
    //   - ReplayDetected  → (ReplayDetected, nil, nil)
    //   - FamilyNotFound  → (FamilyNotFound, nil, nil)
    //   - 其他 Redis error → (0, nil, err)
    // 若 Rotated / GraceHit 回傳 nil state（Lua bug / state_json 為空），caller **必須** fail-closed
    // 視為 ErrFamilyNotFound，禁止對 nil 解參考造成 panic。
    Rotate(ctx context.Context, userID, fid, presentedJTI, newJTI string,
        graceWindow time.Duration) (RotateResult, *FamilyState, error)

    // Revoke：登出單一 family
    Revoke(ctx context.Context, userID, fid string) error

    // RevokeAll：登出該 user 所有 family（改密碼 / 強制全裝置登出）
    RevokeAll(ctx context.Context, userID string) error

    // ListByUser：列出該 user 所有 family（含 lazy cleanup 孤兒 fid）。
    // 用於 GET /auth/sessions 頁面。
    //
    // Redis SMEMBERS 出來無序；本實作須在 Go layer 對結果 **sort by LastRotatedAt desc**，
    // 讓「最近活躍的裝置」排在前面（UX 預期）。caller 不需再 sort。
    ListByUser(ctx context.Context, userID string) ([]FamilyState, error)

    // ScriptsLoaded 回報 NewFamilyStore constructor 內的 SCRIPT LOAD 是否已成功完成，
    // 供 /health/ready 探測（§11.3）。constructor 內載入成功前回 false；成功後恆為 true
    // （process 生命週期內不重置）。
    //
    // 為何不公開 PreloadScripts：避免 caller 在 constructor 之外 lazy-load 而忘了檢查
    // 回傳 error；NewFamilyStore 拿到的 instance 必為「已 ready」狀態，否則 constructor
    // 直接回 error，由 main fatal 退出，避免冷啟動首次 refresh 才踩到 NOSCRIPT 重試 latency。
    ScriptsLoaded() bool
}

// NewFamilyStore — constructor 內自動 SCRIPT LOAD 所有 Lua script，
// 失敗回 error；caller（main）應 fatal 退出。
// ctx 用於 SCRIPT LOAD 的 timeout / cancel，建議從 main 傳入帶 timeout 的 ctx
// （例如 5s）避免 Redis hang 卡住啟動。
func NewFamilyStore(ctx context.Context, client *redis.Client, cfg config.JWTConfig) (FamilyStore, error)
```

**Lua script** — 皆放於 `pkg/redis/scripts/`，以 `redis.NewScript()` + `EvalSha` 呼叫：

```lua
-- save.lua — login 時建立 family + 更新索引
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = family_state_json（內含 abs_exp）
-- ARGV[2] = abs_ttl_seconds（= abs_exp - now，由 caller 計算傳入）
-- ARGV[3] = fid
redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[2])
redis.call("SADD", KEYS[2], ARGV[3])
return 1
```

```lua
-- rotate.lua — refresh 的核心 CAS。回傳 {code, state_json}
--   code: 1=rotated / 2=grace_hit / 3=replay / 4=family_not_found
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = presented_jti
-- ARGV[2] = new_jti
-- ARGV[3] = now_unix
-- ARGV[4] = grace_window_seconds
-- ARGV[5] = fid（給 replay 路徑 SREM 索引用）
local raw = redis.call("GET", KEYS[1])
if not raw then return {4, ""} end

local f = cjson.decode(raw)
local now = tonumber(ARGV[3])
local grace_until = tonumber(ARGV[4]) + now
local abs_ttl_remaining = tonumber(f.abs_exp) - now

-- abs_exp 過期 → 視同 family 不存在（理論上 Redis TTL 已先到，但雙保險）
if abs_ttl_remaining <= 0 then
    redis.call("DEL", KEYS[1])
    redis.call("SREM", KEYS[2], ARGV[5])
    return {4, ""}
end

-- 正常 rotation
if f.current_jti == ARGV[1] then
    f.previous_jti = f.current_jti
    f.previous_response_until = grace_until
    f.current_jti = ARGV[2]
    f.last_rotated_at = now
    redis.call("SET", KEYS[1], cjson.encode(f), "EX", abs_ttl_remaining)
    return {1, cjson.encode(f)}
end

-- Grace window 命中（網路重試）
if f.previous_jti and f.previous_jti == ARGV[1]
   and tonumber(f.previous_response_until or 0) > now then
    return {2, cjson.encode(f)}
end

-- 重放偵測 → 廢掉 family + 同步清掉索引
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[5])
return {3, cjson.encode(f)}
```

```lua
-- revoke.lua — 單一 family 登出
-- KEYS[1] = auth:family:{userID}:fid
-- KEYS[2] = auth:user_families:{userID}
-- ARGV[1] = fid
redis.call("DEL", KEYS[1])
redis.call("SREM", KEYS[2], ARGV[1])
return 1
```

```lua
-- revoke_all.lua — 全裝置登出
-- KEYS[1] = auth:user_families:{userID}
-- ARGV[1] = user_id（用來組 family key 字串；hash tag 必須跟 KEYS[1] 同 slot）
local fids = redis.call("SMEMBERS", KEYS[1])
for _, fid in ipairs(fids) do
    redis.call("DEL", "auth:family:{" .. ARGV[1] .. "}:" .. fid)
end
redis.call("DEL", KEYS[1])
return #fids
```

```lua
-- list_with_cleanup.lua — ListByUser 的 lazy cleanup：回傳所有存活 family，順手 SREM 孤兒 fid
-- KEYS[1] = auth:user_families:{userID}
-- ARGV[1] = user_id
local fids = redis.call("SMEMBERS", KEYS[1])
local result = {}
for _, fid in ipairs(fids) do
    local raw = redis.call("GET", "auth:family:{" .. ARGV[1] .. "}:" .. fid)
    if raw then
        table.insert(result, raw)
    else
        redis.call("SREM", KEYS[1], fid)
    end
end
return result
```

> **效能優化**：所有 script 透過 `redis.NewScript()` 預載，後續 `EvalSha` 不傳 body。第一次以 `EvalSha` 呼叫若 `NOSCRIPT` 自動 fallback `Eval`（go-redis 已內建）。
>
> **`list_with_cleanup` 容量考量**：N = 該使用者的活躍 + 孤兒 family 數，一般情境 < 100，Lua 內 `SMEMBERS` + N 次 `GET` 完全可接受。若預期單一 user 會累積大量孤兒（罕見），可改成限制每次只掃 X 個。

### 7.5 User-Level Revocation Store 介面

**v1.11 新增**——用於「對單一 user 一次性廢掉所有當下還活著的 access token」。
與 §7.3 `AccessTokenBlacklist`（per-`jti`）的差異：

| 機制 | 粒度 | 觸發時機 | 寫入端知不知道 jti |
|---|---|---|---|
| `AccessTokenBlacklist` (§7.3) | 單 jti | Logout / RevokeAll 流程中 caller 持有自己當下的 access jti | 知道 |
| `UserRevocationStore` (§7.5) | 整個 userID | Admin 強制踢人（role 變更、軟刪除等）；caller **不持有** target 的任何 jti | **不知道** |

策略：寫入 `auth:user_revoked_after:{<userID>} = unix_ts`；AuthMiddleware verify 成功後
比對 `claims.iat < unix_ts` 即視為 invalid（見 §8.5 步驟 3.5）。

```go
// pkg/redis/user_revocation.go

type UserRevocationStore interface {
    // Revoke 設定 userID 的 revocation watermark = now() unix seconds。
    //   - ttl：key 存活時間；建議設為「系統最長 abs_exp」+ 安全餘量（例如 ios refresh ttl = 30d）。
    //     ttl 過後資料自然清理；此時 user 對應的所有 access token 早已過期，不影響正確性。
    //   - Redis 寫入失敗：回包裝 error。caller（cms_user_service.Update / SoftDelete）應 log + metric，
    //     但不影響「廢 family」此一更關鍵步驟（family 已廢 = refresh 路徑已斷）。
    Revoke(ctx context.Context, userID string, ttl time.Duration) error

    // RevokedAfter 查詢 userID 的 revocation watermark unix seconds。
    //   - 找不到 key                 → (0, nil)：表示「從未被 revoke」
    //   - 命中                       → (unix_ts, nil)
    //   - **Redis 故障（連線錯 / timeout）→ (0, err)**：由 caller fail-open。
    //     AuthMiddleware 收到 err 後 log warn + metrics.AuthUserRevokeErrors.Inc() + 放行
    //     （與 §7.3 黑名單同個 fail-open 邏輯）。
    RevokedAfter(ctx context.Context, userID string) (int64, error)
}

// NewUserRevocationStore 由 *redis.Client 構造預設實作。
// 使用 SETEX + GET 兩條原生命令，無 Lua（單 key 操作）。
func NewUserRevocationStore(client *redis.Client) UserRevocationStore
```

> Key 命名：`auth:user_revoked_after:{<userID>}`（`{<userID>}` hash tag 與 §7.4 family
> store 一致，未來 Cluster 環境下同一 user 的所有 auth key 落在同一 slot，避免 cross-slot 限制）。
>
> 對應 metric `auth_user_revoke_errors_total`，需補入 §18.2。
>
> **語意 watermark vs blacklist**：本介面只記「最後一次被踢的時間」，
> 同一 user 多次被踢只覆寫不累積；查詢時用 `claims.iat < watermark` 判斷即可。
> 比起「每被踢一次塞一筆」省 redis 容量也省查詢成本（O(1) GET vs O(n) match）。

---

## 8. JWT 模組

### 8.1 設計原則

> 完整設計動機與替代方案比較見 [ADR 007](../adr/007-refresh-token-rotation-and-replay-detection.md)。本節描述實作規範。

- **Access / Refresh 使用不同 secret**（config validate 強制 `nefield`），互相獨立。
- **Access token：純 stateless 驗證**，15 分鐘短效，帶完整 claims（含 `fid`、`aud`）。**hot path 不打 Redis**；僅在「強制踢人」場景才查短期黑名單。
- **Refresh token：每次 rotation 必須換新 jti**，舊 jti 立即失效，再次出現視為重放。
- **Family 為 session 單位**：每次 login 開新 `fid`，多裝置 → 多 family，互相隔離。
- **三層 TTL 控制**：
  - Access token `exp` = 15 分鐘（絕對）
  - Refresh token `exp` = client policy 的 RefreshTTL（滑動，rotation 重設）
  - Refresh token `abs_exp` = login 時間 + client policy 的 AbsoluteTTL（絕對上限，rotation 不延長）
- **Grace window**：rotation 後 N 秒（預設 10s）內舊 jti 可重複命中，回傳上次等價回應，吸收網路重試而不誤觸重放。
- **不同 client 不同 policy**：login request 攜帶 `client_id`，server 對照 `JWTConfig.ClientPolicies` 取 TTL；refresh token JWT `aud = client_id`，跨 client 拿 token 也被擋。
- **`UserType` 區分資料來源**：CMS 人員（`cms`）與一般玩家（`member`）來自不同表，透過 `utype` claim 路由，避免 ID 碰撞。
- **Member 資料隔離**：以 `RequireOwnership` middleware 統一處理（見 §8.6）。
- **密碼雜湊**：使用 `pkg/auth/hasher.Hasher` 介面（§8.3.2），預設 bcrypt 實作，cost 從 `JWTConfig.BcryptCost` 讀取（預設 12）。**禁止 service / repository 直接呼叫 `golang.org/x/crypto/bcrypt`**，便於未來換 argon2id（OWASP 2026 推薦，見 ADR 007 未來工作）。
- **前端配合（CMS 15 分鐘閒置）**：前端負責 idle timer 與 auto-logout（UX 層）；後端透過 refresh token 滑動 1 小時保證「無 API 請求 1 小時自動失效」。詳見 ADR 007。

### 8.2 Token 流程

```
Login（POST /auth/login）
  ├─ 驗證帳密 → user_id, utype, role
  ├─ 從 request 取 client_id（cms-web / public-web / ios-app …）
  ├─ 對照 ClientPolicies 取 refresh_ttl, abs_ttl
  ├─ fid = uuid.New() ；jti = uuid.New() ；abs_exp = now + abs_ttl
  ├─ 簽 access  ({sub, utype, role, fid, aud=client_id, exp=now+15m})
  ├─ 簽 refresh ({sub, utype, jti, fid, aud=client_id, exp=now+refresh_ttl, abs_exp})
  └─ FamilyStore.Save → auth:family:{user_id}:fid + auth:user_families:{user_id}

API 請求
  └─ Authorization: Bearer <access_token>
       └─ 純 stateless：驗簽章 + exp + aud → SetClaims → next
            （僅特殊情況查 access blacklist）

Refresh（POST /auth/refresh）
  ├─ 從 request body 取 refresh_token
  ├─ JWT 驗簽 + iss + aud + exp + abs_exp
  │    exp 過 → ErrTokenExpired ；abs_exp 過 → ErrAbsoluteExpired
  ├─ 取出 user_id, fid, presented_jti
  ├─ new_jti = uuid.New()
  └─ FamilyStore.Rotate Lua CAS：
       ├─ Rotated         → 簽新 access + 新 refresh（refresh.jti=new_jti），回 200
       ├─ GraceHit        → 用 state.CurrentJTI 重簽 refresh、簽新 access，回 200
       │                    （詳見 §8.3.1）
       ├─ ReplayDetected  → Lua 已 DEL family+SREM index → audit.Log(replay_detected)
       │                    → metrics.AuthReplayDetected.Inc → ErrReplayDetected (401)
       └─ FamilyNotFound  → ErrFamilyNotFound (401)

Logout（POST /auth/logout）— 僅需 access token；可選帶 refresh_token
  ├─ AuthMiddleware → 取 user_id, fid, access_jti
  ├─ 若 body 帶 refresh_token：驗簽 + 比對 fid 與 access claims 一致；不一致 → ErrInvalidInput（400 `invalid input`，見 §12.4 / §8.9 LogoutInput）
  ├─ FamilyStore.Revoke(user_id, fid)
  ├─ AccessTokenBlacklist.Add(access_jti, ttl=remaining_exp)
  ├─ audit.Log(logout)
  └─ 204 No Content（前端清掉記憶體 token，跳登入頁）

全裝置登出（POST /auth/sessions/revoke-all）
  └─ FamilyStore.RevokeAll(user_id) + audit.Log(revoke_all) + 204
```

### 8.2.1 重放偵測行為

| 情境 | 結果 |
|---|---|
| 合法 rotation | Rotated，舊 jti 失效 |
| 攻擊者偷 token 後比合法者先 refresh | 合法者下次 refresh → ReplayDetected → family 廢，雙方都被踢，使用者重登並收到「異常登入」提示 |
| 合法者先 refresh，攻擊者再用舊 token | 攻擊者 refresh → ReplayDetected → family 廢，合法者也被踢 |
| 同 client 因網路重試 10 秒內用同 jti 兩次 | GraceHit，不觸發重放，回傳等價回應 |
| 攻擊者拿過期 token | JWT 簽章/exp 驗證階段就拒絕，根本進不到 Lua |
| 同瀏覽器多分頁並發 refresh | 前端必須以 BroadcastChannel / Web Lock 協調；超出 grace window 仍會觸發重放 |

### 8.3 Context Key 與 Claims 定義

```go
// pkg/jwt/context.go
//
// SetClaims / GetClaims 為唯一存取入口，避免 "jwt_claims" 字串散落各處。
// 注意：gin.Context 內部以 map[string]any 儲存，無法使用 unexported type 做 key
// （context.Context 才能），但用未匯出的 string 常數已足以避免外部誤用。
const claimsCtxKey = "jwt_claims"

func SetClaims(c *gin.Context, claims *AccessClaims) {
    c.Set(claimsCtxKey, claims)
}

func GetClaims(c *gin.Context) (*AccessClaims, bool) {
    v, ok := c.Get(claimsCtxKey)
    if !ok {
        return nil, false
    }
    claims, ok := v.(*AccessClaims)
    return claims, ok
}
```

```go
// pkg/jwt/jwt.go
//
// 簽署演算法：HS256（access / refresh 各自獨立 secret）。
// jwt.RegisteredClaims 提供 iss, sub, aud, exp, iat, jti 等標準欄位。
// AccessClaims / RefreshClaims 額外加上系統內部欄位（utype, role, fid, abs_exp）。
//
// Verify 流程必須檢查（詳述見 NewManager 文件）：
//   0. alg 鎖定 HS256（防 alg=none / alg confusion）
//   1. 簽章正確
//   2. iss == JWTConfig.Issuer
//   3. aud ∈ ClientPolicies 已知 client_id
//   4. exp / nbf / iat 含 ClockSkewLeeway 容忍
//   5. (Refresh only) abs_exp 含 leeway → 否則 ErrAbsoluteExpired

type AccessClaims struct {
    jwt.RegisteredClaims              // iss, sub=userID, aud=client_id, exp, iat, jti=access_jti
    UserType UserType `json:"utype"`
    Role     Role     `json:"role"`
    FamilyID string   `json:"fid"`    // 對應後端 family，供管理員「廢掉 family 連帶 access」用
}

type RefreshClaims struct {
    jwt.RegisteredClaims              // iss, sub=userID, aud=client_id, exp, iat, jti=refresh_jti
    UserType    UserType `json:"utype"`
    FamilyID    string   `json:"fid"`
    AbsoluteExp int64    `json:"abs_exp"` // unix seconds，rotation 不延長，超過則 ErrAbsoluteExpired
}

// UserID 是 RegisteredClaims.Subject 的具名 alias，遵循 JWT RFC 7519「sub claim 即 user identifier」慣例。
// 所有 middleware / service / audit 取 user ID 一律呼叫此 method，**禁止直接讀 c.Subject**。
// 理由：未來若改用複合 ID carriage（例如 base64(user_type + user_uuid)）只動 helper，
// caller 介面不變；且讀者看到 claims.UserID() 比 claims.Subject 直覺。
func (c *AccessClaims) UserID() string  { return c.Subject }
func (c *RefreshClaims) UserID() string { return c.Subject }

// SignAccessParams / SignRefreshParams：用 struct 包裝避免長串位置參數。
// Issuer 由 jwtManager 內部以 JWTConfig.Issuer 自動填入，不放 Params。
type SignAccessParams struct {
    UserID   string        // 寫入 JWT 的 sub（RegisteredClaims.Subject）；對應 cms_users.id 或 members.id 的 UUID 字串
    UserType UserType
    Role     Role
    FamilyID string
    ClientID string        // 寫入 aud
    JTI      string        // 呼叫端產生（每次都新）
    TTL      time.Duration // 預設取 JWTConfig.AccessTTL
}

type SignRefreshParams struct {
    UserID      string        // 寫入 sub（同上）
    UserType    UserType
    FamilyID    string
    ClientID    string
    JTI         string        // Login=新；Rotated=新；GraceHit=取 state.CurrentJTI
    TTL         time.Duration // 來自 client policy 的 RefreshTTL
    AbsoluteExp time.Time     // 必須從 server-side state 取（FamilyState.AbsoluteExp），rotation/grace 不改
}

// Manager 所有 method 第一參數均為 context.Context，對齊 §1「所有 service / repository 簽章必接 ctx」原則。
// Sign / Verify 本身為 in-memory HMAC 計算，不會 cancel；ctx 主要保留給未來 OTel span 與測試
// （fake manager 可從 ctx 取注入值），降低介面演進成本。
type Manager interface {
    SignAccess(ctx context.Context, p SignAccessParams) (token string, err error)
    VerifyAccess(ctx context.Context, token string) (*AccessClaims, error)
    SignRefresh(ctx context.Context, p SignRefreshParams) (token string, err error)
    VerifyRefresh(ctx context.Context, token string) (*RefreshClaims, error)

    // PolicyOf 從 client_id 取對應 ClientPolicy；找不到回 ErrInvalidClient。
    PolicyOf(ctx context.Context, clientID string) (config.ClientPolicy, error)
}

// NewManager 由 cfg 構造 Manager。
//
// 簽章：一律使用 cfg.Secret / cfg.RefreshSecret，演算法固定 HS256。
//
// 驗證：
//   0. **Keyfunc 鎖定演算法**：進入簽章驗證之前，先檢查 `token.Method.(*jwt.SigningMethodHMAC)` 且
//      `token.Method.Alg() == "HS256"`，不符直接回 ErrInvalidToken。
//      此步驟必要，防兩種攻擊：
//      - `alg=none`：攻擊者偽造 `{"alg":"none"}` header 與任意 payload；若 keyfunc 不檢查 alg，
//        部分 jwt lib 會視為「無需簽章」直接通過。
//      - **alg confusion**：攻擊者用 server 的 HMAC secret 當「公鑰」配 RS256 簽 token；
//        若 keyfunc 不檢查 alg，server 會誤走 HMAC 路徑用 secret 字串當 key 驗 RS256 通過。
//   1. 簽章先試主 secret，失敗再試 PreviousSecret / PreviousRefreshSecret（若已設定）→ 都失敗回 ErrInvalidToken
//   2. iss 必須 == cfg.Issuer → 不符回 ErrInvalidToken
//   3. aud 必須 ∈ 已知 client_id 白名單 → 不符回 ErrInvalidToken
//      白名單由 NewManager 構造時從 cfg.ClientPolicies 的 keys 捕捉並存入 manager 內部 set；
//      未知 client_id 在 login 早就被擋（ErrInvalidClient），不會出現在已簽出的 token 內，
//      此檢查為防禦性深度檢查（例如 ClientPolicies 設定縮減後，舊 token aud 已失效）。
//   4. **時間 claim 含 leeway**：以 `cfg.ClockSkewLeeway`（預設 30s，見 §4.2）容忍多副本部署的時鐘漂移：
//      - `exp + leeway > now`           → 過則 ErrTokenExpired
//      - `nbf - leeway ≤ now`（NotBefore 不可在未來） → 違反則 ErrInvalidToken
//      - `iat - leeway ≤ now`（IssuedAt 不可在未來） → 違反則 ErrInvalidToken
//      無 leeway 時，NTP 抖動 1–2 秒會隨機讓部分 request 收到 401，難以追蹤。
//   5. (Refresh only) `abs_exp + leeway > now` → 過則 ErrAbsoluteExpired
//
// Secret rotation grace 期間（部署新 secret 到 JWT_SECRET，把舊值搬到 JWT_SECRET_PREVIOUS，
// 等所有舊 token 自然過期，最後清掉 JWT_SECRET_PREVIOUS）詳見 ADR 007「Secret Rotation」段。
// **iss / aud 永遠以最新 cfg 為準，不隨 secret rotation 寬鬆** — rotation 只涵蓋 secret，
// 不涵蓋 issuer / audience。
func NewManager(cfg config.JWTConfig) Manager
```

### 8.3.1 GraceHit handler 行為（refresh endpoint 實作關鍵）

當 `FamilyStore.Rotate` 回傳 `GraceHit` 時，handler 必須完全依 `state` 重簽，**禁止打 DB 重查使用者**：

```go
// state = FamilyStore.Rotate 回傳的 *FamilyState（GraceHit 時為當前 state，未變更）

// 防禦性 nil check：依 §7.4 invariant，Rotated / GraceHit 必非 nil；
// 若收到 nil（Lua bug / state_json 空），視為 ErrFamilyNotFound 走 login，禁止 panic。
if state == nil {
    return ErrFamilyNotFound
}

accessJWT, err := jwtManager.SignAccess(ctx, jwt.SignAccessParams{
    UserID:   state.UserID,
    UserType: jwt.UserType(state.UserType),  // 直接從 state 取，login 時已固化
    Role:     jwt.Role(state.Role),          // 同上
    FamilyID: state.FamilyID,
    ClientID: state.ClientID,
    JTI:      uuid.NewString(),              // access token 每次都新
    TTL:      cfg.JWT.AccessTTL,
})
if err != nil {
    return err
}

policy, err := jwtManager.PolicyOf(ctx, state.ClientID)
if err != nil {
    // ClientPolicies 設定縮減後 state.ClientID 可能已失效（ErrInvalidClient）；
    // fail-closed 走 login，禁止用 zero-value policy 簽出 TTL=0 的 refresh。
    return ErrInvalidToken
}
refreshJWT, err := jwtManager.SignRefresh(ctx, jwt.SignRefreshParams{
    UserID:      state.UserID,
    UserType:    jwt.UserType(state.UserType),
    FamilyID:    state.FamilyID,
    ClientID:    state.ClientID,
    JTI:         state.CurrentJTI,                          // 沿用 state 內 current_jti，不換新
    TTL:         policy.RefreshTTL,                         // 重新計算
    AbsoluteExp: time.Unix(state.AbsoluteExp, 0),           // 從 state 取，不可信任 client JWT
})
if err != nil {
    return err
}

// 回 200，response shape 與 Rotated 完全相同
```

關鍵不變式：
- `access_token.jti` 永遠新；`refresh_token.jti` 在 GraceHit 沿用 state.CurrentJTI
- `refresh_token.abs_exp` 永遠從 state 取
- `utype` / `role` 永遠從 state 取（login 時固化）；hot path 不打 DB
- Lua state 沒有變更 → 不會把 grace window 推遠 → 攻擊者無法刷 grace 拉長攻擊窗

> Rotated 路徑的重簽程序與此完全相同，差別在 `refresh_token.jti` 改用 `uuid.NewString()`。

### 8.3.2 Password Hasher（`pkg/auth/hasher`）

密碼雜湊抽象成 `Hasher` interface，預設 bcrypt 實作，未來可換 argon2id（OWASP 2026 推薦，見 ADR 007 未來工作）。
**禁止在 service / repository 內直接呼叫 `golang.org/x/crypto/bcrypt`**，必須走此介面，避免日後升級散落改動。

```go
// pkg/auth/hasher/hasher.go
//
// Hasher 為密碼雜湊抽象。實作必須：
//   - Hash() 回傳的字串可被同一實作的 Compare() 還原驗證
//   - Compare(hash, plain) 在密碼錯誤時回 ErrMismatch（sentinel），其他錯誤照原樣回傳
//   - 對相同密碼每次 Hash() 結果不同（含 salt）
type Hasher interface {
    Hash(plain string) (string, error)
    Compare(hash, plain string) error
}

var ErrMismatch = errors.New("password mismatch")
```

```go
// pkg/auth/hasher/bcrypt.go

type bcryptHasher struct{ cost int }

// NewBcrypt 從 JWTConfig.BcryptCost 取 cost（規格 10–15，預設 12）。
func NewBcrypt(cost int) Hasher { return &bcryptHasher{cost: cost} }

func (h *bcryptHasher) Hash(plain string) (string, error) {
    b, err := bcrypt.GenerateFromPassword([]byte(plain), h.cost)
    if err != nil {
        return "", fmt.Errorf("bcrypt hash: %w", err)
    }
    return string(b), nil
}

func (h *bcryptHasher) Compare(hash, plain string) error {
    err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
    if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
        return ErrMismatch
    }
    return err
}
```

> **為何不重用 `bcrypt.ErrMismatchedHashAndPassword`**：service 層不該耦合特定演算法的 error 型別。
> 統一回 `hasher.ErrMismatch`，service 層用 `errors.Is(err, hasher.ErrMismatch)` 判斷後轉成 `apperr.ErrUnauthorized`。

### 8.4 Role 定義

```go
// pkg/jwt/role.go
type Role string

const (
    // CMS 內部人員
    RoleAdmin  Role = "admin"  // 系統管理員，擁有最高權限
    RoleUser   Role = "user"   // CMS 一般操作人員，基本查詢權限
    RoleViewer Role = "viewer" // CMS 唯讀檢視者，僅能查看特定資料

    // 非 CMS 使用者
    RoleMember Role = "member" // 一般玩家，只能查詢自己的資料
)

func (r Role) IsValid() bool {
    switch r {
    case RoleAdmin, RoleUser, RoleViewer, RoleMember:
        return true
    }
    return false
}

type UserType string

const (
    UserTypeCMS    UserType = "cms"    // CMS 內部人員
    UserTypeMember UserType = "member" // 一般玩家
)

func (u UserType) IsValid() bool {
    switch u {
    case UserTypeCMS, UserTypeMember:
        return true
    }
    return false
}
```

### 8.5 Auth Middleware

```go
// pkg/jwt/middleware.go

// AuthMiddleware 驗證 access token，將 claims 注入 context（透過 SetClaims）。
// blacklist 為「強制踢人」用的短期黑名單，非常態查詢；hot path 預期黑名單 miss。
//
// 處理流程：
//   1. 從 Authorization header 取 "Bearer <token>"
//      - 規範化：去除前後空白；prefix 比對「Bearer 」case-insensitive，僅接受單一空白
//      - header 缺 / 前綴錯 / token 部分為空 → 401 `unauthorized`
//   2. jwtManager.VerifyAccess(token) — 依 pkg/jwt sentinel 對應 HTTP error code：
//      - jwt.ErrTokenExpired                       → 401 `token_expired`   （前端 retry refresh）
//      - jwt.ErrInvalidToken（含 alg/iss/aud/簽章/nbf/iat）→ 401 `invalid_token`   （前端走 login）
//      - 其他不預期 error                          → 401 `unauthorized`
//
//      ⚠️ 為何 pkg/jwt 不直接回 apperr.ErrXxx：§2.1 禁止 pkg/* → internal/* 依賴。
//      pkg/jwt 定義自己的 sentinel（jwt.ErrTokenExpired / jwt.ErrInvalidToken /
//      jwt.ErrAbsoluteExpired / jwt.ErrInvalidClient）；service 層用 errors.Is
//      轉譯為 internal/apperr 的 domain error（與 §8.3.2 hasher.ErrMismatch →
//      apperr.ErrUnauthorized 的 pattern 對稱，見 service/auth_service.transitJWTError）。
//      middleware 直接寫 HTTP error code，不過 apperr 一層。
//   3. blacklist.IsBlacklisted(ctx, claims.ID):
//      - (true, nil)  → 401 `session_revoked`（middleware 內直接寫 error code，不過 HandleError；見 §12.4）
//      - (false, nil) → 通過
//      - (false, err) → **fail-open**：log warn + metrics.AuthBlacklistErrors.Inc() + 通過
//   3.5. userRevoke.RevokedAfter(claims.Subject):  ← v1.11 新增（見 §7.5）
//      - (0, nil)                                  → 通過（user 從未被 revoke）
//      - (ts, nil) and claims.IssuedAt < ts        → 401 `session_revoked`（admin 強制踢人後簽出的舊 token）
//      - (ts, nil) and claims.IssuedAt >= ts       → 通過（revoke 之後簽的 token，視為合法）
//      - (0, err)                                  → **fail-open**：log warn + metrics.AuthUserRevokeErrors.Inc() + 通過
//   4. SetClaims(c, claims) + c.Next()
//
// 設計權衡（fail-open vs fail-closed）：
//   黑名單 hit 是稀有事件（管理員手動踢、改密碼、family revoke）；
//   Redis 抖動更常見。fail-open 容忍 short-term miss，避免「Redis 抖一下整個 API 全打 401」。
//   若安全模型要求 fail-closed，改為 (false, err) → 401 + 切換 metric 即可，介面不變。
//
// 步驟 3 與 3.5 為何同時保留：
//   步驟 3 是「caller 知道目標 jti」的精準踢人（自己 logout、refresh rotation 連帶廢舊 access）；
//   步驟 3.5 是「caller 不知道任何 jti」的整 user 廢票（admin 改別人 role/軟刪等）。
//   兩條互補，不重複。
func AuthMiddleware(
    jwtManager Manager,
    blacklist redis.AccessTokenBlacklist,
    userRevoke redis.UserRevocationStore,
) gin.HandlerFunc

// RequireRole 驗證 token role 是否符合，需接在 AuthMiddleware 之後。
//
// 注意：
// 1. 不呼叫 HandleError（internal/handler），以避免 pkg/jwt ↔ internal/handler 循環依賴。
//    直接內嵌 401 / 403 回應格式。
// 2. 使用 GetClaims（typed accessor），避免字串 key 散落。
func RequireRole(roles ...Role) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, ok := GetClaims(c)
        if !ok {
            abortJSON(c, http.StatusUnauthorized, "unauthorized")
            return
        }
        for _, r := range roles {
            if claims.Role == r {
                c.Next()
                return
            }
        }
        abortJSON(c, http.StatusForbidden, "forbidden")
    }
}

func abortJSON(c *gin.Context, status int, msg string) {
    c.AbortWithStatusJSON(status, gin.H{
        "success":    false,
        "request_id": logger.GetRequestID(c),
        "error":      msg,
    })
}
```

`AuthMiddleware` 驗證流程（純 stateless 為主，黑名單與 user-revoke 僅強制踢人時用）：
1. 從 `Authorization: Bearer <token>` 取得 access token。
2. 呼叫 `jwtManager.VerifyAccess()` 驗證簽章、`exp`、`aud`。
3. 檢查 `jti` 是否在短期黑名單（§7.3；caller 知道 jti 時用）。
4. 檢查 user 是否在 user-revocation watermark 之後簽發（§7.5；caller 不知 jti 時用）。
5. 呼叫 `SetClaims(c, claims)` 注入 context。

> 為何不查 `fid` 是否還在 family store：access token 只活 15 分鐘，付出 hot path Redis 查詢成本不划算。若管理員需要立刻撤銷某 family，由 logout/revoke 流程**順手把仍有效的 access jti 加入黑名單**（§7.3）或寫 user-revoke watermark（§7.5）即可。

### 8.6 Ownership Middleware（Member 資料隔離）

Member 隔離邏輯統一抽成 middleware，**禁止散落於各 service**。

```go
// pkg/jwt/middleware.go

// RequireOwnership 確保 URL param 指定的目標 ID 屬於當前登入者。
// 對 UserType == cms 自動放行（由 RequireRole 控制權限）。
// 對 UserType == member 嚴格比對 claims.UserID() == c.Param(paramName)，不符回 403。
//
// Sanity check：path param 若不存在（拼錯 paramName 或 router 沒掛對應 :param），
// c.Param 回 ""，與任何 UserID 比較都不等 → 永遠 403，會造成「靜默全 forbidden」的詭異 bug。
// 故啟動期間記 warn log 提醒（不 panic，避免 init 失敗無法上線）。
func RequireOwnership(paramName string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, ok := GetClaims(c)
        if !ok {
            abortJSON(c, http.StatusUnauthorized, "unauthorized")
            return
        }
        if claims.UserType == UserTypeCMS {
            c.Next()
            return
        }
        target := c.Param(paramName)
        if target == "" {
            // 配置錯誤：router 沒對應 :paramName。記 error 並 403 拒絕（fail-closed）。
            logger.L().Error("RequireOwnership misconfigured — path param missing",
                zap.String("request_id", logger.GetRequestID(c)),
                zap.String("path", c.FullPath()),
                zap.String("param_name", paramName),
            )
            abortJSON(c, http.StatusForbidden, "forbidden")
            return
        }
        if claims.UserID() != target {
            abortJSON(c, http.StatusForbidden, "forbidden")
            return
        }
        c.Next()
    }
}
```

### 8.7 Router 角色權限範例

```go
auth := jwt.AuthMiddleware(jwtManager, blacklist)

// 所有已登入使用者皆可存取，但 member 僅能查自己的資料
players := api.Group("/players", auth)
players.GET("/:id/transactions", jwt.RequireOwnership("id"), playerHandler.ListTransactions)

// 僅 admin 可存取
adminGroup := api.Group("/admin", auth, jwt.RequireRole(jwt.RoleAdmin))
adminGroup.GET("/users", adminHandler.ListUsers)

// admin 與 user 可存取，viewer 不行
manageGroup := api.Group("/manage", auth, jwt.RequireRole(jwt.RoleAdmin, jwt.RoleUser))
manageGroup.GET("/reports", reportHandler.Get)
```

### 8.8 對前端的契約要求

下列為「前端配合契約」（詳見 ADR 007 §前端配合契約），違反任一條會破壞安全模型，必須在前端團隊規範文件中明文要求：

| 項目 | 要求 |
|---|---|
| Access token 儲存 | In-memory variable，不可進 storage |
| Refresh token 儲存 | In-memory（首選）/ sessionStorage（次選）；**禁 localStorage** |
| Refresh 失敗 | **不可自動重試**；一律走 login 流程 |
| 多分頁 / 多 worker | 以 `BroadcastChannel` 或 `navigator.locks.request()` 協調 refresh，避免並發送同 jti |
| CSP | `Content-Security-Policy` 嚴格 nonce-based，禁 inline script、禁 `unsafe-eval` |
| 輸出跳脫 | 嚴禁 `dangerouslySetInnerHTML` / `v-html` 接未過濾資料 |
| CMS idle timer | 監聽 mouse/keyboard，15 分鐘無互動 → 呼叫 `/auth/logout` + 清掉記憶體 token + 跳登入頁 |
| Login 必填 | `client_id`（`cms-web` / `public-web` / `ios-app` / `android-app`） |

### 8.9 AuthService 介面

Auth 業務邏輯統一入口，由 `internal/handler/auth` 呼叫。**禁止 handler 直接呼叫 `FamilyStore` / `Hasher` / `Manager` / `Blacklist`**，避免邏輯散落於各 endpoint。

> **設計取捨**：
> - **Register 僅開放給 CMS user**（`client_id == "cms-web"` 才放行，其餘 → `ErrInvalidClient`），新註冊預設 `role = user`。Member 註冊機制待後續業務 spec（見 §6.5）。`role` 升降權留給後續業務 spec。
> - **Register 成功回 201 + 空 body，不直接簽 token**；前端註冊完必須再打一次 `/auth/login`。理由：把「建立帳號」與「建立 session」職責分離，未來加入「註冊後須 email 驗證才能登入」等流程不必改 service 簽章。
> - **Login 依 `client_id` 路由表**：`cms-web` → `cms_users`、`public-web` / `ios-app` / `android-app` → `members`。未來若 CMS 也需手機 App，再評估改為 `LoginInput.UserType` 顯式傳入。
> - 所有 method 第一參數均接 `context.Context`，對齊 §1「所有 service / repository 簽章必接 ctx」原則。
> - **`RevokeAll` 必須把當前 access JTI 加入黑名單**（ttl = 剩餘 exp，與 Logout 一致），否則「全裝置登出」後當前 access token 仍可用到自然過期（最長 15 分鐘）。

#### 介面定義

```go
// internal/service/auth/auth_service.go

type AuthService interface {
    // Register：建立 CMS user，預設 role = "user"。
    //   ErrInvalidClient  — client_id != "cms-web"
    //   ErrWeakPassword   — 弱密碼（見下方規則）
    //   ErrUsernameTaken  — username 已被 cms_users 占用（依 §12.5 unique 23505 包裝）
    // 不簽 token、不建立 session；caller 另打 /auth/login。
    Register(ctx context.Context, in RegisterInput) error

    // Login：依 client_id 路由到 cms_users / members，驗帳密 → 開新 family → 簽 token pair。
    //   ErrInvalidClient  — client_id 不在白名單
    //   ErrUnauthorized   — 帳密錯（含 user 不存在；不洩漏哪個錯）
    Login(ctx context.Context, in LoginInput) (*TokenPair, error)

    // Refresh：依 §8.2 / §8.3.1 做 rotation；錯誤對應 §12.4 sentinel：
    //   ErrTokenExpired / ErrAbsoluteExpired / ErrInvalidToken / ErrReplayDetected / ErrFamilyNotFound
    Refresh(ctx context.Context, in RefreshInput) (*TokenPair, error)

    // Logout：廢當前 family + 把當前 access JTI 加入黑名單。
    //   ErrInvalidInput — in.RefreshToken 非空且 fid 與 access claims 不一致
    Logout(ctx context.Context, in LogoutInput) error

    // ListSessions：回當前 user 全部 family，currentFID 命中者 IsCurrent=true；
    // 發現孤兒 fid 順手清（lazy cleanup）。
    ListSessions(ctx context.Context, userID, currentFID string) ([]SessionInfo, error)

    // RevokeSession：撤銷指定 fid。
    //   ErrUseLogoutInstead — targetFID == currentFID（請改打 /auth/logout）
    //   ErrNotFound         — family 不存在
    RevokeSession(ctx context.Context, userID, currentFID, targetFID string) error

    // RevokeAll：撤銷當前 user 所有 family，並把當前 access JTI 加入黑名單
    // （ttl = currentAccessRemaining，與 Logout 一致）。
    RevokeAll(ctx context.Context, userID, currentAccessJTI string, currentAccessRemaining time.Duration) error
}
```

#### Input / Output 型別

```go
type RegisterInput struct {
    Username string `validate:"required,min=3,max=64"`
    Password string `validate:"required,min=8,max=256"`  // 額外 weak password 檢查見下
    ClientID string `validate:"required,oneof=cms-web public-web ios-app android-app"`
}

type LoginInput struct {
    Username  string `validate:"required,min=1,max=128"`
    Password  string `validate:"required,min=1,max=256"`
    ClientID  string `validate:"required,oneof=cms-web public-web ios-app android-app"`
    IP        string  // handler 從 c.ClientIP() 取（依 TrustedProxies §9.2）
    UserAgent string  // handler 從 c.Request.UserAgent() 取
}

type RefreshInput struct {
    RefreshToken string `validate:"required"`
    IP           string
    UserAgent    string
}

type LogoutInput struct {
    UserID       string
    FamilyID     string
    AccessJTI    string
    AccessRemain time.Duration  // 加入黑名單 ttl
    RefreshToken string         // optional；非空時驗 fid 與 access claims 一致
}

type TokenPair struct {
    AccessToken      string
    RefreshToken     string
    TokenType        string  // "Bearer"
    ExpiresIn        int     // access TTL 秒
    RefreshExpiresIn int     // refresh TTL 秒
}

type SessionInfo struct {
    FID           string
    ClientID      string
    DeviceLabel   string    // UA parser 產生（"Chrome 120 on macOS"）
    IPAtLogin     string
    CreatedAt     time.Time
    LastRotatedAt time.Time
    IsCurrent     bool
}
```

#### 弱密碼規則

`Register` 在 `Hasher.Hash` 之前先過：

| 規則 | 拒絕條件 |
|---|---|
| 最小長度 | `len(password) < 8` |
| 字符多樣性 | 不含任何字母 **或** 不含任何數字 |

不符 → `ErrWeakPassword`（HTTP 422 `weak_password`，見 §12.4）。

> **demo 刻意寬鬆**：未加大小寫混合、特殊字元、字典檢查、洩漏密碼比對。後續導入 zxcvbn / NIST SP 800-63B 時 replace 規則即可，介面不變。
> **Seed admin（§13.5）不走此檢查**（migration SQL 直接 INSERT bcrypt hash，bypass application validation）；預設密碼 `admin123` 達到「8 字元 + 字母 + 數字」最低底線，剛好通過。

#### Constructor 與依賴

```go
func NewAuthService(
    cmsUserRepo repository.CMSUserRepository,
    memberRepo  repository.MemberRepository,
    hasher      hasher.Hasher,
    jwtManager  jwt.Manager,
    familyStore redis.FamilyStore,
    blacklist   redis.AccessTokenBlacklist,
    auditLogger audit.Logger,
) AuthService
```

UA 解析（`mileusna/useragent`）為 AuthService 內部實作細節，不在 constructor signature。

> Audit log 對應事件詳見 §18.3.4（已涵蓋 Register / Login / Refresh / Logout / Sessions 完整事件清單）。

---

## 9. Gin Server 組裝

### 9.1 啟動流程

```
main()
  ├── cfg, _ := config.Load()
  ├── logger.Init(cfg.Log, cfg.App.Env)
  ├── db, _  := database.Connect(cfg.Database)
  ├── database.RunMigrations(cfg.Database)       ← golang-migrate + embed.FS
  ├── rdb, _ := redis.Connect(cfg.Redis)
  ├── sqlDB, _ := db.DB()
  ├── metrics.Init(sqlDB, Version, Commit)       ← 含 DBStatsCollector / BuildInfo
  ├── auditLogger, _ := audit.NewZapLogger(cfg.Log.AuditPath)   ← 獨立 sink；Shutdown 時 Sync（§14.2）
  ├── 建立共享 auth 元件：
  │      jwtManager     := jwt.NewManager(cfg.JWT)
  │      hasher         := hasher.NewBcrypt(cfg.JWT.BcryptCost)
  │      preloadCtx, _  := context.WithTimeout(context.Background(), 5*time.Second)
  │      familyStore, _ := redis.NewFamilyStore(preloadCtx, rdb, cfg.JWT)  ← constructor 內自動 SCRIPT LOAD（§7.4），失敗 fatal
  │      blacklist      := redis.NewAccessTokenBlacklist(rdb)              ← §7.3
  │      rlStore, _  := ratelimit.NewRedisStore(rdb, "ratelimit") ← §15.4
  ├── wire up repositories → services（AuthService 注入 auditLogger 等）→ handlers
  ├── router.Setup(...)（rlStore 注入 IPMiddleware / UserMiddleware）
  └── server.RunWithGracefulShutdown(...)        ← shutdown 順序見 §14.2
```

> `Version` / `Commit` 由 `go build -ldflags "-X main.Version=v1.2.3 -X main.Commit=$(git rev-parse HEAD)"` 注入。
> Shutdown 順序與 audit `Sync()`、blacklist / familyStore / rdb / db close 的處理見 §14.2。

### 9.2 Router 結構

```go
r := gin.New()

// 信任的 proxy 來源 — 必須明確設定，否則 c.ClientIP() 可被 X-Forwarded-For 偽造。
// nil / 空 slice 代表完全不信任（直接連線 IP 才採用）。
if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
    return nil, err
}

// 順序原則：
//   1. RequestID 第一個 — 後續所有 layer 才能讀到 ID
//   2. Recovery 緊接其後 — 最大化 panic 覆蓋（包含 Logger 自己 panic）
//   3. Logger 在 Recovery 之後 — access log 已能讀到 ID
//   4. SecureHeaders / CORS / MaxBodyBytes — 對所有路由生效
//   5. Metrics — 排最後，c.FullPath() 已就緒
//
// 注意：`Retry-After` 必須在 CORS ExposeHeaders 列出，否則跨域場景下
// 前端 JS 讀不到限流 endpoint 的重試時間（§15.2 寫入此 header）。
r.Use(
    logger.RequestID(),
    httpx.GinRecovery(),                                         // v1.9：從 pkg/logger 移至 pkg/httpx
    logger.GinLogger("/health", "/health/ready", cfg.Metrics.Path),
    httpx.SecureHeaders(cfg.App.Env),                            // v1.9：依環境決定是否送 HSTS
    cors.New(cors.Config{
        AllowOrigins:     cfg.Server.AllowedOrigins,
        AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Accept-Language", "Authorization", "X-Request-ID"},
        ExposeHeaders:    []string{"X-Request-ID", "Retry-After"},
        AllowCredentials: cfg.Server.AllowCredentials,
        MaxAge:           12 * time.Hour,
    }),
    httpx.MaxBodyBytes(cfg.Server.MaxRequestBody),
    metrics.GinMiddleware(),
)

// Health check / metrics — 不進 api group，不套 auth、不套限流
r.GET("/health", healthHandler.Live)
r.GET("/health/ready", healthHandler.Ready)
if cfg.Metrics.Enabled {
    r.GET(cfg.Metrics.Path, gin.WrapH(promhttp.Handler()))
}

// API schema HTML（由 `redocly build-docs schema/openapi.yaml -o docs/api.html` 預先產生）
// — 僅在非 prod 環境暴露；prod 不對外公開 schema，避免攻擊者免費取得 endpoint inventory（§3.2）
if cfg.App.Env != "prod" {
    r.StaticFile("/docs", "./docs/api.html")
}

api := r.Group("/api/v1")
//
// Rate limit 分兩層：
//   外層 IP 限流：所有人都受限，擋匿名濫用（含登入暴力破解）
//   內層 User 限流：登入後改用 user key，避免共享 IP（NAT、辦公室）誤傷合法使用者
//
if cfg.RateLimit.Enabled {
    api.Use(ratelimit.IPMiddleware(cfg.RateLimit.IPPeriod, cfg.RateLimit.IPLimit, rlStore))
}

// 認證後群組：先驗 token、再套 user 限流
authed := api.Group("/")
authed.Use(jwt.AuthMiddleware(jwtManager, blacklist))
if cfg.RateLimit.Enabled {
    authed.Use(ratelimit.UserMiddleware(cfg.RateLimit.UserPeriod, cfg.RateLimit.UserLimit, rlStore))
}
{
    players := authed.Group("/players")
    {
        players.GET("/:id/transactions", jwt.RequireOwnership("id"), playerHandler.ListTransactions)
    }
}

// 非認證群組：只有 IP 限流；需要 access token 的端點各自掛 AuthMiddleware
// （不掛 UserMiddleware 避免 session 管理本身被限流卡住，例如：使用者察覺異常想全裝置登出時）
auth := api.Group("/auth")
{
    // 公開
    auth.POST("/login", authHandler.Login)
    auth.POST("/refresh", authHandler.Refresh)

    // 需要 access token
    authedAuth := auth.Group("/", jwt.AuthMiddleware(jwtManager, blacklist))
    {
        authedAuth.POST("/logout", authHandler.Logout)

        // Session 管理（裝置清單 / 撤銷指定 / 全裝置登出）
        // 路徑設計注意：DELETE /sessions/:fid 與 POST /sessions/revoke-all 雖然 method 不同，
        // 但都掛在 /sessions 下；若日後新增 GET /sessions/revoke-all 必須改路徑避免 trie 衝突。
        authedAuth.GET   ("/sessions",            authHandler.ListSessions)
        authedAuth.DELETE("/sessions/:fid",       authHandler.RevokeSession)
        authedAuth.POST  ("/sessions/revoke-all", authHandler.RevokeAllSessions)
    }
}
```

### 9.3 共用 HTTP Middleware

```go
// pkg/httpx/body_limit.go
func MaxBodyBytes(n int64) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
        c.Next()
    }
}

// pkg/httpx/secure_headers.go
//
// SecureHeaders 包裝 unrolled/secure。
//
// 預設套用於**所有路由**（API 為純 JSON，本來就不該載入子資源、被嵌入 iframe、或被瀏覽器
// 自動取用 camera/mic 等 sensor），故走 default-deny CSP 與全關 PermissionsPolicy。
// 若日後新增 /docs 等需 render HTML 的端點，**必須**用獨立 middleware 套寬鬆 CSP 覆寫
// （例如 `default-src 'self'; script-src 'self' 'nonce-...'`），不要全域放寬。
//
// HSTS 僅在 staging / prod 啟用 — dev 啟用會強迫瀏覽器把 localhost 升 HTTPS，
// 一次踩到就要清整個 STS 快取（每個開發環境瀏覽器都要清），體驗極差。
func SecureHeaders(env string) gin.HandlerFunc {
    opts := secure.Options{
        FrameDeny:             true,
        ContentTypeNosniff:    true,
        ReferrerPolicy:        "no-referrer",
        // CSP：JSON API 預設不該載任何資源；frame-ancestors 'none' 取代 X-Frame-Options（同 FrameDeny 重複，現代瀏覽器優先讀 CSP）
        ContentSecurityPolicy: "default-src 'none'; frame-ancestors 'none'",
        // 全關 sensor / payment / fullscreen 等 Permissions API；XSS 注入也無法觸發
        PermissionsPolicy:     "camera=(), microphone=(), geolocation=(), payment=(), fullscreen=()",
    }
    if env == "staging" || env == "prod" {
        opts.STSSeconds = 31536000
        opts.STSIncludeSubdomains = true
    }
    s := secure.New(opts)
    return func(c *gin.Context) {
        if err := s.Process(c.Writer, c.Request); err != nil {
            c.AbortWithStatus(http.StatusInternalServerError)
            return
        }
        c.Next()
    }
}

// pkg/httpx/recovery.go
//
// GinRecovery 攔截 panic，記錄 stack 後以 §10 ErrorResponse shape 回 500。
// v1.9 起此中介層居於 pkg/httpx；解開了 pkg/logger ↔ pkg/httpx 的循環依賴
// （recovery 寫回應需要 WriteError，WriteError 需要 logger.GetRequestID，
//  若 recovery 留在 logger 內呼叫 WriteError 即成 cycle）。
func GinRecovery() gin.HandlerFunc {
    return gin.CustomRecovery(func(c *gin.Context, recovered any) {
        logger.L().Error("panic recovered",
            zap.Any("panic", recovered),
            zap.String("request_id", logger.GetRequestID(c)),
            zap.Stack("stack"),
        )
        WriteError(c, http.StatusInternalServerError, "internal server error")
    })
}
```

### 9.4 共用錯誤回應 helper

中介層（`pkg/jwt`、`pkg/ratelimit`、`pkg/logger`）需要寫錯誤回應，但不能反向依賴 `internal/handler.ErrorResponse`（否則 import cycle）。為避免每個 middleware 自己 `gin.H{...}` 內嵌一份 envelope（一旦 envelope 欄位變動就要散落同步），統一抽到 `pkg/httpx`：

```go
// pkg/httpx/error.go
//
// WriteError 寫入符合 §10 ErrorResponse shape 的 JSON 並 Abort。
// 不引用 internal/handler，避免 pkg → internal 反向依賴；
// shape 與 internal/handler.ErrorResponse 必須維持一致（CI 由 OpenAPI lint 把關）。
//
// code 應為 §12.4 列舉的 snake_case error code（如 "unauthorized" / "forbidden"）。
func WriteError(c *gin.Context, status int, code string) {
    c.AbortWithStatusJSON(status, gin.H{
        "success":    false,
        "request_id": logger.GetRequestID(c),
        "error":      code,
    })
}
```

所有中介層改用 `httpx.WriteError(c, http.StatusUnauthorized, "unauthorized")`，取代各自手寫 `c.AbortWithStatusJSON(gin.H{...})` 與 `abortJSON()`。包含：
- `pkg/jwt.RequireRole` / `RequireOwnership` / `AuthMiddleware`
- `pkg/ratelimit.{IP,User}Middleware`
- `pkg/httpx.GinRecovery`（v1.9 已從 `pkg/logger` 搬入 `pkg/httpx`）

### 9.5 Auth Handler 實作骨架

7 個 endpoint 依 §3.4 對齊 OpenAPI；service 層 input / output 結構詳見 §8.9；錯誤映射由 `handler.HandleError`（§12.3）統一處理。本節給最關鍵的 3 個完整骨架，其餘 4 個以表格摘要重點。

```go
// internal/handler/auth/handler.go

type Handler struct {
    svc service.AuthService
}

func NewHandler(svc service.AuthService) *Handler { return &Handler{svc: svc} }

// Register — POST /auth/register（公開；§8.9）
func (h *Handler) Register(c *gin.Context) {
    var body schema.RegisterRequest
    if err := c.ShouldBindJSON(&body); err != nil {
        handler.HandleError(c, err)
        return
    }
    if err := h.svc.Register(c.Request.Context(), service.RegisterInput{
        Username: body.Username,
        Password: body.Password,
        ClientID: body.ClientID,
    }); err != nil {
        handler.HandleError(c, err)
        return
    }
    c.Status(http.StatusCreated)  // 201 無 body；caller 須另打 /auth/login
}

// Login — POST /auth/login（公開）
func (h *Handler) Login(c *gin.Context) {
    var body schema.LoginRequest
    if err := c.ShouldBindJSON(&body); err != nil {
        handler.HandleError(c, err)
        return
    }
    pair, err := h.svc.Login(c.Request.Context(), service.LoginInput{
        Username:  body.Username,
        Password:  body.Password,
        ClientID:  body.ClientID,
        IP:        c.ClientIP(),                  // 依 TrustedProxies（§9.2）
        UserAgent: c.Request.UserAgent(),
    })
    if err != nil {
        handler.HandleError(c, err)
        return
    }
    handler.OK(c, dto.FromTokenPair(pair))
}

// Logout — POST /auth/logout（需 access token；body optional）
func (h *Handler) Logout(c *gin.Context) {
    claims, _ := jwt.GetClaims(c)  // AuthMiddleware 保證存在
    var body schema.LogoutRequest
    _ = c.ShouldBindJSON(&body)    // optional body；解析失敗忽略

    err := h.svc.Logout(c.Request.Context(), service.LogoutInput{
        UserID:       claims.UserID(),
        FamilyID:     claims.FamilyID,
        AccessJTI:    claims.ID,                            // RegisteredClaims.ID = jti
        AccessRemain: time.Until(claims.ExpiresAt.Time),    // 加入黑名單 ttl
        RefreshToken: body.RefreshToken,
    })
    if err != nil {
        handler.HandleError(c, err)
        return
    }
    c.Status(http.StatusNoContent)
}
```

#### 其他 4 個 endpoint 重點

| Endpoint | 輸入解析 | Service 呼叫 | 成功回應 |
|---|---|---|---|
| `Refresh` (POST `/auth/refresh`) | `body.refresh_token` + `c.ClientIP()` + UA | `svc.Refresh(ctx, RefreshInput{...})` | `handler.OK(c, dto.FromTokenPair(pair))` |
| `ListSessions` (GET `/auth/sessions`) | `claims.UserID()` + `claims.FamilyID`（當前 fid） | `svc.ListSessions(ctx, userID, currentFID)` | `handler.OKList(c, dto.FromSessionInfoList(infos), nil)` |
| `RevokeSession` (DELETE `/auth/sessions/:fid`) | `c.Param("fid")` + `claims.UserID()` + `claims.FamilyID` | `svc.RevokeSession(ctx, userID, currentFID, targetFID)` | `c.Status(204)` |
| `RevokeAllSessions` (POST `/auth/sessions/revoke-all`) | `claims.UserID()` + `claims.ID` + `time.Until(claims.ExpiresAt.Time)` | `svc.RevokeAll(ctx, userID, jti, remain)` | `c.Status(204)` |

#### 共用注意事項

- **Bearer 解析**：handler **不**自己解析 `Authorization` header；`AuthMiddleware`（§8.5）已驗證並 `SetClaims`，handler 直接 `jwt.GetClaims(c)`。
- **IP / UA 取得**：一律 `c.ClientIP()`（依 `TrustedProxies`，見 §9.2）與 `c.Request.UserAgent()`；**禁止**直接讀 `c.GetHeader("X-Forwarded-For")` 等 raw header。
- **ctx 傳遞**：所有 service 呼叫第一參數均為 `c.Request.Context()`；**禁止用 `context.Background()`**（會丟失 request_id、cancellation、deadline）。
- **錯誤回應**：一律 `handler.HandleError(c, err)`，由 §12.3 統一映射；handler 內**禁止**直接 `c.JSON(401, ...)`。
- **path collision 警示**：`POST /auth/sessions/revoke-all` 與 `DELETE /auth/sessions/:fid` 共存於 gin trie；method 不同不衝突（§9.2 註）。若日後新增 `GET /auth/sessions/revoke-all` 會與 `:fid` 衝突，務必先重命名（建議 `_actions/revoke-all`）。

---

## 10. Response

### 10.1 設計原則

- 定義在 `internal/handler/response.go`，分為**成功**與**錯誤**兩個獨立型別。
- 使用 Go generics（`Response[T any]`），`Data` 欄位型別安全。
- `RequestID` 出現在所有回應中，由 `HandleError` 與各 handler 從 context 提取後填入。
- `Meta` 僅在 list 端點使用，單筆查詢時省略。
- **不能用單一型別 + omitempty 同時承載成功與錯誤**：empty slice 加 omitempty 會被序列化為「欄位消失」而非 `[]`，破壞前端契約。

### 10.2 結構定義

```go
// internal/handler/response.go

// Response 用於所有成功回應。Data 無 omitempty —
// 空 slice 序列化為 [] 而非消失；單筆查詢用 *Type 傳遞，nil 序列化為 null（避免歧義）。
type Response[T any] struct {
    Success   bool   `json:"success"`        // 恆為 true
    RequestID string `json:"request_id"`
    Data      T      `json:"data"`
    Meta      *Meta  `json:"meta,omitempty"` // 僅 list 端點填入
}

// ErrorResponse 用於所有錯誤回應。無 Data 欄位，避免 omitempty 陷阱。
type ErrorResponse struct {
    Success   bool         `json:"success"`           // 恆為 false
    RequestID string       `json:"request_id"`
    Error     string       `json:"error"`
    Details   []FieldError `json:"details,omitempty"` // 僅 validation 錯誤時出現
}

// Meta 對應 OpenAPI 的 PageMeta schema（schema/openapi.yaml）。
// Go 端使用短名 Meta；OpenAPI 為避免與其他 envelope meta 概念衝突，命名 PageMeta。
// 兩邊 JSON wire format 皆為小寫 "meta"（envelope 欄位名），確保 kin-openapi runtime 驗證通過。
type Meta struct {
    Page     int   `json:"page"`
    PageSize int   `json:"page_size"`
    Total    int64 `json:"total"` // 使用 int64 對齊 SQL COUNT(*)
}

type FieldError struct {
    Field   string `json:"field"`
    Message string `json:"message"`
}

// OK 成功單筆回應的 constructor，省略每個 handler 重複的樣板
func OK[T any](c *gin.Context, data T) Response[T] {
    return Response[T]{
        Success:   true,
        RequestID: logger.GetRequestID(c),
        Data:      data,
    }
}

// OKList 成功列表回應的 constructor（含分頁 meta）
func OKList[T any](c *gin.Context, data []T, page, pageSize int, total int64) Response[[]T] {
    return Response[[]T]{
        Success:   true,
        RequestID: logger.GetRequestID(c),
        Data:      data,
        Meta:      &Meta{Page: page, PageSize: pageSize, Total: total},
    }
}
```

### 10.3 使用範例

```go
// 成功 — 單筆
c.JSON(http.StatusOK, handler.OK(c, dto.FromPlayer(player)))

// 成功 — 列表含分頁（FromXxxList 保證非 nil slice，序列化為 []）
c.JSON(http.StatusOK,
    handler.OKList(c, dto.FromTransactionList(txs), page.Page, page.PageSize, total))

// 失敗 — 由 HandleError 統一處理（見 §12）
// { "success": false, "request_id": "...", "error": "resource not found" }
```

---

## 11. Health Check

### 11.1 設計原則

- 不需要獨立模組，實作於 `internal/handler/health_handler.go`。
- 兩個端點分別服務不同用途，皆不需要 JWT auth、不套限流。
- `/health` 不打 DB / Redis，供 load balancer 高頻輪詢；`/health/ready` 做深度連線檢查。
- 兩端點均在 `GinLogger` 的 skipPaths 中（見 §5.3），避免污染日誌。
- **`/health/ready` 必須驗證 FamilyStore Lua script 已預載**：避免冷啟動首次 refresh 才踩到 `NOSCRIPT` 重試 latency。`NewFamilyStore` constructor 內自動 `SCRIPT LOAD`（§7.4），完成後 `ScriptsLoaded()` 回 true；`/health/ready` 探測此旗標。
- **回應 shape 刻意不走 §10 envelope**：health 為 ops 端點（§3.4，不在 OpenAPI 契約範圍），k8s probe 只看 HTTP status code；body 為人類 debug 用，free-form `{"status": <map>}` 比強塞 `{success, request_id, error, ...}` 直觀。強行對齊會把 ops 端點拉進業務契約，與 §3.4 的設計相違。

### 11.2 端點定義

| 方法 | 路徑 | 用途 | 依賴 |
|------|------|------|------|
| GET | `/health` | 服務存活確認 | 無 |
| GET | `/health/ready` | 深度就緒檢查（DB + Redis） | `*gorm.DB`、`*redis.Client` |

### 11.3 實作

```go
// internal/handler/health_handler.go
type HealthHandler struct {
    db          *gorm.DB
    redis       *redis.Client
    familyStore redis.FamilyStore // 暴露 ScriptsLoaded() bool 供 ready 探測
}

func (h *HealthHandler) Live(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HealthHandler) Ready(c *gin.Context) {
    ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
    defer cancel()

    checks := map[string]string{}
    allOK := true

    sqlDB, err := h.db.DB()
    if err != nil || sqlDB.PingContext(ctx) != nil {
        checks["database"] = "unhealthy"
        allOK = false
    } else {
        checks["database"] = "ok"
    }

    if err := h.redis.Ping(ctx).Err(); err != nil {
        checks["redis"] = "unhealthy"
        allOK = false
    } else {
        checks["redis"] = "ok"
    }

    // FamilyStore Lua scripts 由 NewFamilyStore constructor 自動載入；未載入 =
    // constructor 應該已 fatal 退出（防禦性檢查；正常路徑此分支不會觸發）
    if !h.familyStore.ScriptsLoaded() {
        checks["family_store_scripts"] = "unhealthy"
        allOK = false
    } else {
        checks["family_store_scripts"] = "ok"
    }

    status := http.StatusOK
    if !allOK {
        status = http.StatusServiceUnavailable
    }
    c.JSON(status, gin.H{"status": checks})
}
```

> **預載時機**：`pkg/redis/family_store.go` 的 `NewFamilyStore(ctx, client, cfg)`（介面與簽章定義見 §7.4）在 constructor 內逐一以 `SCRIPT LOAD` 寫入 Redis script cache，成功後將 `ScriptsLoaded` 旗標翻為 `true`。任一支載入失敗整個 constructor 回 error，main 直接 fatal 退出（避免冷啟動首次 refresh 才踩到 `NOSCRIPT` 重試 latency）。`/health/ready` 透過 `ScriptsLoaded()` 探測該旗標，**caller 不可從 instance 之外重複 lazy-load**。

### 11.4 回應範例

```json
// GET /health → 200
{ "status": "ok" }

// GET /health/ready → 200（全部正常）
{
  "status": {
    "database": "ok",
    "redis": "ok",
    "family_store_scripts": "ok"
  }
}

// GET /health/ready → 503（DB 異常）
{
  "status": {
    "database": "unhealthy",
    "redis": "ok",
    "family_store_scripts": "ok"
  }
}

// GET /health/ready → 503（FamilyStore Lua script 未預載）
{
  "status": {
    "database": "ok",
    "redis": "ok",
    "family_store_scripts": "unhealthy"
  }
}
```

---

## 12. 錯誤處理（Error Handler）

### 12.1 三層職責分工

| 層級 | 職責 | 做法 |
|------|------|------|
| Repository | 將 DB error 包裝成 domain error | `ErrNotFound`、`ErrConflict` 等 sentinel errors |
| Service | 業務邏輯錯誤 | 回傳 domain error |
| Handler | 將 error 轉為 HTTP 回應 | 呼叫 `HandleError()`，集中對應 status code |

### 12.2 Domain Errors

```go
// internal/apperr/errors.go
var (
    ErrNotFound          = errors.New("resource not found")
    ErrUnauthorized      = errors.New("unauthorized")
    ErrForbidden         = errors.New("forbidden")
    ErrConflict          = errors.New("resource already exists")
    ErrInvalidInput      = errors.New("invalid input")
    ErrTooManyRequests   = errors.New("too many requests")

    // Auth 相關 — 細分以利前端區分行為（重登 vs retry vs 顯示訊息）
    ErrTokenExpired      = errors.New("token expired")              // 401，access exp / refresh exp 超過
    ErrAbsoluteExpired   = errors.New("absolute session expired")   // 401，refresh.abs_exp 超過 → 強制重登
    ErrInvalidToken      = errors.New("invalid token")              // 401，簽章 / iss / aud 等驗證失敗
    ErrReplayDetected    = errors.New("token replay detected")      // 401，重放偵測 → family 已廢
    ErrFamilyNotFound    = errors.New("session not found")          // 401，family key 不存在
    ErrInvalidClient     = errors.New("invalid client")             // 400，未知 client_id
)
```

### 12.3 HandleError

所有 handler 統一呼叫 `HandleError()`，禁止在各 handler 內自行判斷 HTTP status code。

```go
// internal/handler/error_handler.go
func HandleError(c *gin.Context, err error) {
    reqID := logger.GetRequestID(c)

    // c.ShouldBindJSON 失敗：JSON 語法錯 / 型別不符 / body 為空。
    // 不應走 default 變 500；統一回 400 invalid input（不洩漏 parser 細節）。
    var syntaxErr *json.SyntaxError
    var typeErr *json.UnmarshalTypeError
    if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || errors.Is(err, io.EOF) {
        c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{
            Success:   false,
            RequestID: reqID,
            Error:     "invalid input",
        })
        return
    }

    // validation error 優先處理
    var ve validator.ValidationErrors
    if errors.As(err, &ve) {
        details := make([]FieldError, len(ve))
        for i, fe := range ve {
            details[i] = FieldError{
                Field:   fe.Field(),
                Message: validationMessage(fe),
            }
        }
        c.AbortWithStatusJSON(http.StatusBadRequest, ErrorResponse{
            Success:   false,
            RequestID: reqID,
            Error:     "invalid input",
            Details:   details,
        })
        return
    }

    // domain error
    var (
        status  int
        message string
    )
    switch {
    case errors.Is(err, apperr.ErrNotFound):
        status, message = http.StatusNotFound, apperr.ErrNotFound.Error()
    case errors.Is(err, apperr.ErrUnauthorized):
        status, message = http.StatusUnauthorized, apperr.ErrUnauthorized.Error()
    case errors.Is(err, apperr.ErrTokenExpired):
        status, message = http.StatusUnauthorized, "token_expired"
    case errors.Is(err, apperr.ErrAbsoluteExpired):
        status, message = http.StatusUnauthorized, "absolute_expired"
    case errors.Is(err, apperr.ErrInvalidToken):
        status, message = http.StatusUnauthorized, "invalid_token"
    case errors.Is(err, apperr.ErrReplayDetected):
        status, message = http.StatusUnauthorized, "replay_detected"
    case errors.Is(err, apperr.ErrFamilyNotFound):
        status, message = http.StatusUnauthorized, "session_not_found"
    case errors.Is(err, apperr.ErrForbidden):
        status, message = http.StatusForbidden, apperr.ErrForbidden.Error()
    case errors.Is(err, apperr.ErrConflict):
        status, message = http.StatusConflict, apperr.ErrConflict.Error()
    case errors.Is(err, apperr.ErrInvalidInput):
        status, message = http.StatusBadRequest, apperr.ErrInvalidInput.Error()
    case errors.Is(err, apperr.ErrInvalidClient):
        status, message = http.StatusBadRequest, "invalid_client"
    case errors.Is(err, apperr.ErrTooManyRequests):
        status, message = http.StatusTooManyRequests, apperr.ErrTooManyRequests.Error()
    default:
        status, message = http.StatusInternalServerError, "internal server error"
        logger.L().Error("unhandled error",
            zap.String("request_id", reqID),
            zap.Error(err),
        )
    }

    c.AbortWithStatusJSON(status, ErrorResponse{
        Success:   false,
        RequestID: reqID,
        Error:     message,
    })
}

// validationMessage 將 validator tag 轉為人類可讀訊息
func validationMessage(fe validator.FieldError) string {
    switch fe.Tag() {
    case "required":
        return fmt.Sprintf("%s 為必填", fe.Field())
    case "email":
        return "必須為有效的 email 格式"
    case "min":
        return fmt.Sprintf("最小長度為 %s", fe.Param())
    case "max":
        return fmt.Sprintf("最大長度為 %s", fe.Param())
    case "gte":
        return fmt.Sprintf("最小值為 %s", fe.Param())
    case "lte":
        return fmt.Sprintf("最大值為 %s", fe.Param())
    default:
        return "格式不正確"
    }
}
```

> **避免訊息洩漏**：domain error 對應的 `message` 一律使用 sentinel error 自身的字串，**不採用 `err.Error()`**，避免上層包裝細節（例如 `fmt.Errorf("user %d not found in shard %d: %w", id, shard, ErrNotFound)`）洩漏給客戶端。原始錯誤透過 logger 紀錄即可。

### 12.4 錯誤對應總表

| 來源 | 偵測方式 | HTTP Status | `error` 字串 | 回應差異 |
|------|---------|-------------|------|---------|
| `json.SyntaxError` / `json.UnmarshalTypeError` / `io.EOF` | `errors.As` / `errors.Is` | 400 | `invalid input` | request body 解析失敗（語法錯 / 型別不符 / 空 body）；不洩漏 parser 細節 |
| `validator.ValidationErrors` | `errors.As` | 400 | `invalid input` | 含 `details[]` 欄位錯誤清單 |
| `apperr.ErrInvalidInput` | `errors.Is` | 400 | `invalid input` | |
| `apperr.ErrInvalidClient` | `errors.Is` | 400 | `invalid_client` | 未知 / 不被支援的 `client_id` |
| `apperr.ErrUnauthorized` | `errors.Is` | 401 | `unauthorized` | |
| `apperr.ErrTokenExpired` | `errors.Is` | 401 | `token_expired` | access / refresh exp 超過；前端可 retry refresh |
| `apperr.ErrAbsoluteExpired` | `errors.Is` | 401 | `absolute_expired` | refresh.abs_exp 超過；前端必須走 login，**不可** retry |
| `apperr.ErrInvalidToken` | `errors.Is` | 401 | `invalid_token` | 簽章 / iss / aud 失敗；走 login |
| `apperr.ErrReplayDetected` | `errors.Is` | 401 | `replay_detected` | 重放偵測；前端應 UI 提示「異常登入」並走 login |
| `apperr.ErrFamilyNotFound` | `errors.Is` | 401 | `session_not_found` | family 已被廢 / 過期 |
| AuthMiddleware（blacklist hit） | `IsBlacklisted == true` | 401 | `session_revoked` | 強制踢人（管理員、改密碼、family revoke）；middleware 內直接寫，不過 HandleError |
| AuthMiddleware（user-revoke hit）| `claims.iat < RevokedAfter` | 401 | `session_revoked` | Admin 對整 user 強制踢人（§7.5）；同 error code 不區分原因，避免洩漏內部判斷 |
| `apperr.ErrForbidden` | `errors.Is` | 403 | `forbidden` | |
| `apperr.ErrNotFound` | `errors.Is` | 404 | `resource not found` | |
| `apperr.ErrConflict` | `errors.Is` | 409 | `resource already exists` | |
| `apperr.ErrUsernameTaken` | `errors.Is` | 409 | `username_taken` | Register 時 cms_users 已存在同名（依 §12.5 unique 23505 包裝為此 sentinel；獨立於 generic conflict） |
| `apperr.ErrWeakPassword` | `errors.Is` | 422 | `weak_password` | Register 弱密碼檢查不通過（規則見 §8.9） |
| `apperr.ErrTooManyRequests` | `errors.Is` | 429 | `too many requests` | |
| 其他 | default | 500 | `internal server error` | 記錄至 logger |

### 12.5 Repository 錯誤包裝規範

```go
// GORM ErrRecordNotFound → domain ErrNotFound
if errors.Is(err, gorm.ErrRecordNotFound) {
    return nil, apperr.ErrNotFound
}
// unique constraint violation → domain ErrConflict（依 pgError code 判斷）
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    return nil, apperr.ErrConflict
}
```

---

## 13. DB Migration

### 13.1 設計原則

- 使用 `golang-migrate/migrate/v4`，migration 腳本版本化，所有 schema 變更有歷史紀錄。
- 禁止在 production 使用 GORM AutoMigrate。
- 腳本存放於 `migrations/`，檔名格式：`{version}_{description}.{up|down}.sql`。
- **腳本以 `embed.FS` 編入 binary**，runtime 不依賴外部檔案系統 — 避免容器化部署忘記 COPY、相對路徑錯誤、binary 從其他工作目錄啟動等問題。
- 啟動時自動執行 pending migrations（`migrate.Up()`），失敗則 fatal 退出。
- 多 instance 同時啟動時，golang-migrate 自動透過 PostgreSQL advisory lock (`pg_advisory_lock`) 序列化執行：先取得鎖的 instance 跑 migration，其他 instance 阻塞等候直到鎖釋放後讀到「無 pending」狀態。無需應用層額外重試。

> **多應用共用同一 DB 的注意事項**（本專案目前**一個應用獨佔一個 DB**，不會遇到；但 spec 留註以防共用 DB 重構時被忽略）：
> golang-migrate 預設以 search_path 第一個 schema（通常是 `public`）的名稱 hash 作為 advisory lock id，並以 `schema_migrations` 作為紀錄表。兩個應用共用同一 DB + schema 會：
> 1. **lock id 相同 → 啟動互卡**（lock 等待 timeout 由 migrationStatementTimeout 5m 約束，仍會看到明顯延遲）
> 2. **共用 schema_migrations → 版本號互踩**（A 的 000005 跟 B 的 000005 互蓋）
>
> 解決方式（依嚴重度由低到高）：
> - 各應用 query string 加 `x-migrations-table=<app>_schema_migrations` — 解決紀錄表衝突，但 lock 仍互卡
> - **推薦：各應用獨立 schema**（`CREATE SCHEMA playerledger; SET search_path=playerledger,public;`），lock id 自然不同
> - 各應用獨立 DB — 最徹底，本專案目前採此方案

> **建議 integration test**（§19）：spawn 兩個 process 同時啟動，驗證只有一個跑 migration、另一個阻塞後讀到「無 pending」狀態，且 schema_migrations 內版本號沒有重複套用。

### 13.2 腳本命名範例

```
migrations/
├── 000001_create_cms_users.up.sql
├── 000001_create_cms_users.down.sql
├── 000002_create_members.up.sql
├── 000002_create_members.down.sql
├── 000003_seed_initial_admin.up.sql
└── 000003_seed_initial_admin.down.sql
```

> Auth 必需的兩張表（`cms_users` / `members`）與 dev/demo 預設 admin（`admin / admin123`）的完整 SQL 範例見 §13.5。業務表（如 `players`、`transactions`）從 `000004` 開始編號。

### 13.3 執行方式

**檔案配置**：`migrations/` 放專案根（便於 CLI 工具直接使用），由 `migrations/embed.go` 匯出 `embed.FS`：

```go
// migrations/embed.go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

`pkg/database/migrate.go` 引用該 FS：

```go
// pkg/database/migrate.go

import "github.com/<org>/playerledger/migrations"

// migrationStatementTimeout：單一 migration statement 上限。
// 設較長（5 分鐘）容納大型 ALTER / CREATE INDEX；但有限以避免 deadlock 或 advisory lock 等待
// 把整個 process 卡死，便於監控偵測異常。
const migrationStatementTimeout = 5 * time.Minute

func RunMigrations(cfg config.DatabaseConfig) error {
    src, err := iofs.New(migrations.FS, ".")
    if err != nil {
        return fmt.Errorf("migration source: %w", err)
    }
    // statement_timeout 用 query string 傳給 PG（毫秒）。
    // 密碼用 url.UserPassword 包裝以正確處理特殊字元。
    dsnURL := &url.URL{
        Scheme: "postgres",
        User:   url.UserPassword(cfg.User, cfg.Password),
        Host:   fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
        Path:   cfg.Name,
        RawQuery: url.Values{
            "sslmode":           {cfg.SSLMode},
            "statement_timeout": {strconv.Itoa(int(migrationStatementTimeout.Milliseconds()))},
        }.Encode(),
    }
    m, err := migrate.NewWithSourceInstance("iofs", src, dsnURL.String())
    if err != nil {
        // %w 會把 dsn 含密碼一起寫到 log；用 redactedDSN 取代。
        return fmt.Errorf("migrate new (dsn=%s): %w", redactedDSN(dsnURL), err)
    }
    defer func() {
        srcErr, dbErr := m.Close()
        if srcErr != nil {
            logger.L().Warn("migrate source close error", zap.Error(srcErr))
        }
        if dbErr != nil {
            logger.L().Warn("migrate db close error", zap.Error(dbErr))
        }
    }()
    if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
        return fmt.Errorf("migrate up: %w", err)
    }
    return nil
}

// redactedDSN 把 password 替換成 "***" 後序列化，給 error / log 用。
func redactedDSN(u *url.URL) string {
    redacted := *u
    if u.User != nil {
        redacted.User = url.UserPassword(u.User.Username(), "***")
    }
    return redacted.String()
}
```

> **為何用 `migrations/embed.go` 而不直接 embed 於 `pkg/database`**：
> `//go:embed` 路徑相對於 .go 檔位置；若放在 `pkg/database/migrate.go` 直接 embed 需要把 SQL 搬到 `pkg/database/migrations/`，與 CLI（`migrate -path ./migrations`）路徑分歧。本規格選擇「SQL 放專案根 + 同目錄一支 embed.go」，CLI 與 binary 共用同一份檔案。

> **密碼安全**：DSN 中的密碼必須以 `url.QueryEscape` 編碼，避免特殊字元（`@`、`/`、`:` 等）破壞 URL 結構。

### 13.4 手動操作（CLI）

```bash
# 套用所有 pending migrations
migrate -path ./migrations \
  -database "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}" \
  up

# 回滾最後一版
migrate -path ./migrations \
  -database "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}" \
  down 1

# 建立新的 migration 檔
migrate create -ext sql -dir ./migrations -seq create_transactions
```

### 13.5 Auth 表 migration 與 seed admin

對應 §6.5 model 與 §8.9 AuthService 的 SQL；放入 `migrations/` 即由 §13.3 的 embed.FS 自動套用。

#### 結構 migration

```sql
-- migrations/000001_create_cms_users.up.sql
CREATE TABLE cms_users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64) NOT NULL,
    password_hash VARCHAR(72) NOT NULL,
    role          VARCHAR(16) NOT NULL CHECK (role IN ('admin','user','viewer')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_cms_users_username ON cms_users(username) WHERE deleted_at IS NULL;

-- migrations/000001_create_cms_users.down.sql
DROP TABLE IF EXISTS cms_users;
```

```sql
-- migrations/000002_create_members.up.sql
CREATE TABLE members (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64) NOT NULL,
    password_hash VARCHAR(72) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX uq_members_username ON members(username) WHERE deleted_at IS NULL;

-- migrations/000002_create_members.down.sql
DROP TABLE IF EXISTS members;
```

#### Seed initial admin（由 .env 注入，非 SQL migration）

雞生蛋問題：CMS 自註冊預設 role 為 `user`，沒有任何路徑能產生第一個 `admin`。

**v1.11 起改用 env 配置 + 應用層 idempotent seed**（取代原 SQL migration `000003_seed_initial_admin`）；
避免把任何形式的密碼或 placeholder 寫進版控的 migration SQL。

```bash
# .env / .env.example
ADMIN_USERNAME=admin
ADMIN_PASSWORD=change-me-min-12-chars-strong-pw
```

對應 `config.AdminConfig`（§4.2）。Validate() 跨欄位規則：
- 兩欄同時留空 → 跳過 seed（dev 友善，無 admin 即可開機探索）
- 任一非空 → 兩個都必填、密碼 ≥ 12 字元
- `APP_ENV=prod` → 兩欄都必填（避免 prod 上線忘了設）

啟動流程（`cmd/server/main.go` 在 repository 建立後、HTTP server 啟動前呼叫）：

```go
// internal/service/admin_seed.go
//
// 行為：
//   - 帳號不存在 → bcrypt(password) → 建立（role=admin）
//   - 帳號已存在 → log info「skipping」，**不主動覆寫密碼**
//     （避免無聲改密碼造成稽核盲點；旋密走 CMS API 或 ops 手動 update）
//
// 多副本同時啟動：第二個 instance 會看到帳號已存在 → 跳過，天然 idempotent。
func EnsureAdminFromConfig(
    ctx context.Context,
    repo repository.CMSUserRepository,
    h hasher.Hasher,
    username, password string,
) (created bool, err error)
```

> **為何不用 SQL seed migration**：
> - SQL migration 的密碼欄位避不開「寫死進版控」或「placeholder + CI guard」兩種次優解。
> - 應用層 seed 讓密碼留在 env / secret manager（k8s Secret / Vault），與其他敏感配置同一處理管道。
> - bcrypt cost 直接走 `JWTConfig.BcryptCost`，與後續 CMS 註冊使用者一致；無需「migration 用 cost=10，runtime 用 cost=12」的特例。

> **為何不主動覆寫已存在 admin 的密碼**：
> 在啟動時無聲覆寫會造成兩個問題：(1) 任何重啟都可能改密碼，運維難以追蹤；(2) audit log 缺少「誰改了密碼」紀錄（不像走 CMS API 有 actor）。改密碼一律走 CMS API 或運維手動 SQL，留下稽核軌跡。

---

## 14. Graceful Shutdown

### 14.1 設計原則

- 收到 `SIGTERM` / `SIGINT` 時，停止接受新連線，等待進行中的 request 處理完畢再關閉。
- Shutdown timeout 從 `ServerConfig.ShutdownTimeout` 讀取（預設 10s）。
- 依序關閉：HTTP Server → DB 連線池 → Redis 連線 → flush audit logger → flush app logger。
- **Audit logger 必須在 app logger 之前 Sync**：audit 事件容忍度為零（安全相關），不能因 process 結束而丟失 buffer 內事件。
- 所有關閉錯誤都要 log，不可靜默忽略（audit logger 的 Sync 失敗額外寫入 stderr，避免依賴正要關閉的 app logger）。

### 14.2 實作

```go
// cmd/server/main.go
srv := &http.Server{
    Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
    Handler:           router,
    ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, // 防 Slowloris，必設
    ReadTimeout:       cfg.Server.ReadTimeout,
    WriteTimeout:      cfg.Server.WriteTimeout,
    IdleTimeout:       cfg.Server.IdleTimeout,
}

go func() {
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        logger.L().Fatal("server error", zap.Error(err))
    }
}()

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
defer cancel()

if err := srv.Shutdown(ctx); err != nil {
    logger.L().Error("http shutdown error", zap.Error(err))
}

if sqlDB, err := db.DB(); err != nil {
    logger.L().Error("get sql.DB error", zap.Error(err))
} else if err := sqlDB.Close(); err != nil {
    logger.L().Error("close sql.DB error", zap.Error(err))
}

if err := redisClient.Close(); err != nil {
    logger.L().Error("close redis error", zap.Error(err))
}

// Audit logger 先 Sync — 安全事件不能漏；失敗時直寫 stderr，避免依賴正要 Sync 的 app logger
if err := auditLogger.Sync(); err != nil {
    fmt.Fprintf(os.Stderr, "audit logger sync error: %v\n", err)
}

logger.L().Info("server exited")
_ = logger.L().Sync() // flush buffered log entries
```

---

## 15. Rate Limiting

### 15.1 設計原則

- 使用 `ulule/limiter` + Redis store，支援分散式多節點限流。
- **兩層限流**（見 §9.2 router）：
  - 外層 `IPMiddleware` 掛在 `/api/v1`：以 `c.ClientIP()` 為 key，匿名與已認證請求都受限，擋暴力濫用
  - 內層 `UserMiddleware` 掛在 `AuthMiddleware` 之後：以 `claims.UserID()` 為 key，避免共享 IP（NAT、辦公室）誤傷合法使用者；額度可較寬鬆
- 超出限制回傳 `429 Too Many Requests`，`Retry-After` header 告知重試時間。
- 限流開關與參數由 `RateLimitConfig.IP` / `RateLimitConfig.User` 分別控制。
- **Fail-open 策略**：limiter 本身故障（Redis 不可用）時放行請求，避免限流元件成為單點故障；故障時記 warn 並由 metrics 追蹤。
- **IP 偽造防護**：必須先在 router 設定 `SetTrustedProxies`（見 §9.2），否則 `c.ClientIP()` 可被偽造繞過。

### 15.2 實作

```go
// pkg/ratelimit/middleware.go

// IPMiddleware 以 c.ClientIP() 為限流 key，掛在 /api/v1，所有人都受限
func IPMiddleware(period time.Duration, limit int64, store limiter.Store) gin.HandlerFunc {
    return newMiddleware(period, limit, store, func(c *gin.Context) string {
        return "ratelimit:ip:" + c.ClientIP()
    })
}

// UserMiddleware 以 claims.UserID() 為限流 key，必須掛在 AuthMiddleware 之後
// 若無 claims（middleware 順序錯）視為 misconfiguration，記 error + metric 後 fail-open。
// metric `ratelimit_misconfigured_total` 應在告警系統設「> 0 即 page」規則，
// 避免靜默 bypass 導致登入後流量無 user-level 限流保護。
func UserMiddleware(period time.Duration, limit int64, store limiter.Store) gin.HandlerFunc {
    return newMiddleware(period, limit, store, func(c *gin.Context) string {
        claims, ok := jwt.GetClaims(c)
        if !ok {
            logger.L().Error("UserMiddleware without claims — middleware order misconfigured",
                zap.String("request_id", logger.GetRequestID(c)),
                zap.String("path", c.FullPath()),
            )
            metrics.RateLimitMisconfigured.WithLabelValues(c.FullPath()).Inc()
            return "" // 空 key → newMiddleware 視為 fail-open
        }
        // hash tag {userID} 對齊 §7.1 規範
        return "ratelimit:user:{" + claims.UserID() + "}"
    })
}

func newMiddleware(period time.Duration, limit int64, store limiter.Store,
    keyFn func(*gin.Context) string) gin.HandlerFunc {

    rate := limiter.Rate{Period: period, Limit: limit}
    lim := limiter.New(store, rate)

    return func(c *gin.Context) {
        key := keyFn(c)
        if key == "" {
            c.Next()
            return
        }
        ctx, err := lim.Get(c.Request.Context(), key)
        if err != nil {
            // Fail-open：limiter 故障時放行，記 warn 供監控
            logger.L().Warn("rate limiter failed, allowing request",
                zap.String("request_id", logger.GetRequestID(c)),
                zap.Error(err),
            )
            metrics.RateLimiterErrors.Inc()
            c.Next()
            return
        }
        if ctx.Reached {
            // ulule/limiter 的 ctx.Reset 是「window 重置時的 unix timestamp（秒）」，
            // 不是 HTTP `Retry-After` header 規範的「秒數」。直接寫 ctx.Reset 客戶端會
            // 把時戳當秒數，誤等數十年。下方計算實際剩餘秒數，下限 1 保證至少 1 秒。
            retryAfter := ctx.Reset - time.Now().Unix()
            if retryAfter < 1 {
                retryAfter = 1
            }
            c.Header("Retry-After", strconv.FormatInt(retryAfter, 10))
            // 不引用 internal/handler.ErrorResponse 避免反向依賴，內嵌等價結構
            c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
                "success":    false,
                "request_id": logger.GetRequestID(c),
                "error":      "too many requests",
            })
            return
        }
        c.Next()
    }
}
```

### 15.3 Redis Key 命名

```
ratelimit:ip:<ip>                # 單 key 操作，無需 hash tag
ratelimit:user:{<userID>}        # hash tag 對齊 §7.1，與 refresh token 同 slot
```

> **為何 IP key 不加 hash tag**：rate limiter 對單一 key 做 `INCR + EXPIRE`，不跨 key 操作，cluster 模式下單一節點自己處理即可，分散更均勻；同 user 限流則為了與其他 `{userID}` 操作（family store）共享 slot 才加 hash tag。**規則：只有需要跨 key 原子操作的場景才加 hash tag**。

### 15.4 Limiter Store 建構

`pkg/ratelimit` 需要 `ulule/limiter.Store` 實例承載計數狀態；本專案統一以 Redis store（多副本部署共享狀態），由獨立 constructor 提供，避免每個 middleware 各自建構。

```go
// pkg/ratelimit/store.go

import (
    sredis "github.com/ulule/limiter/v3/drivers/store/redis"
    "github.com/redis/go-redis/v9"
    "github.com/ulule/limiter/v3"
)

// NewRedisStore 以同一個 *redis.Client 建構 limiter store。
// prefix 預設 "ratelimit"；對應 §15.3 / §7.1 key 命名。
// 構造失敗（sredis 內部 ping 等）→ 回 error，由 main fatal 退出。
func NewRedisStore(client *redis.Client, prefix string) (limiter.Store, error) {
    if prefix == "" {
        prefix = "ratelimit"
    }
    return sredis.NewStoreWithOptions(client, limiter.StoreOptions{
        Prefix:   prefix,
        MaxRetry: 3,
    })
}
```

> **與 §15.3 key 命名的關係**：`sredis` 會把 prefix 與 caller 傳入的 key 串接成 `<prefix>:<key>`。本專案 middleware 傳入的 key 已含完整命名（`ratelimit:ip:<ip>` / `ratelimit:user:{<userID>}`），prefix 設成空字串或 `ratelimit` 都不致衝突；統一用 `ratelimit` 避免 sredis 預設值在升級時悄悄改變。
>
> **多副本一致性**：所有副本必須用同一個 Redis instance + 同一個 prefix，否則限流計數會被分散到不同 key 而失效。
>
> **§22 對齊**：step 15（`pkg/ratelimit`）的第一個 sub-step 為建立 `NewRedisStore`；router 組裝（step 24，`main.go`）注入此 store 給 `IPMiddleware` / `UserMiddleware`。

---

## 16. Pagination Helper

### 16.1 設計原則

- 所有 list 端點統一使用 `PageRequest` 解析分頁參數，不重複寫解析邏輯。
- 預設 `page=1`、`page_size=20`，`page_size` 上限 100，防止一次撈取過多資料。
- `Total` 採用 `int64`（與 SQL `COUNT(*)` 對齊），handler 不再強轉。
- 提供 GORM Scope 函式，直接注入查詢。
- **大資料集（資料量 > 10 萬筆）建議改用 cursor-based pagination**：以 `(created_at, id)` 複合 cursor，避免深翻頁的 OFFSET 效能塌陷。
  - 本階段先以 offset 滿足需求，但 **transaction list 端點預期單 player 累積快**，建議從 v1 就直接走 cursor，避免日後 schema 變動破壞前端契約。
  - Cursor 介面建議：`?cursor=<opaque>&limit=20`，cursor 為 base64(`<created_at>|<id>`)；回應帶 `next_cursor` 與 `has_more`，**不回 total**（cursor 模式下 total 無法 cheaply 取得）。
  - OpenAPI 共用 envelope 需另定 `CursorMeta`（`next_cursor`、`has_more`），與 §10 `PageMeta` 並存。

### 16.2 實作

```go
// internal/pagination/pagination.go

type PageRequest struct {
    Page     int `form:"page"      validate:"omitempty,min=1,max=10000"` // 上限防深翻頁 abuse
    PageSize int `form:"page_size" validate:"omitempty,min=1,max=100"`
}

func (p *PageRequest) SetDefaults() {
    if p.Page == 0 {
        p.Page = 1
    }
    if p.PageSize == 0 {
        p.PageSize = 20
    }
}

func (p *PageRequest) Offset() int {
    return (p.Page - 1) * p.PageSize
}

// GORM Scope
func (p *PageRequest) Scope() func(*gorm.DB) *gorm.DB {
    return func(db *gorm.DB) *gorm.DB {
        return db.Offset(p.Offset()).Limit(p.PageSize)
    }
}
```

### 16.3 Handler 使用範例

```go
func (h *PlayerHandler) ListTransactions(c *gin.Context) {
    var page pagination.PageRequest
    if err := c.ShouldBindQuery(&page); err != nil {
        HandleError(c, err)
        return
    }
    page.SetDefaults()

    txs, total, err := h.service.ListTransactions(c.Request.Context(), id, &page)
    if err != nil {
        HandleError(c, err)
        return
    }

    c.JSON(http.StatusOK,
        OKList(c, dto.FromTransactionList(txs), page.Page, page.PageSize, total))
}
```

---

## 17. DTO（Data Transfer Object）

### 17.1 設計原則

- DTO 是專門傳輸給前端的資料結構，與 DB Model **嚴格分離**，禁止直接將 Model 回傳給前端。
- 定義在 `internal/dto/`，每個資源一個檔案（如 `player_dto.go`、`transaction_dto.go`）。
- 轉換函式（`FromXxx` / `FromXxxList`）與 DTO 定義放同一檔，避免 handler 肥大：
  ```go
  // internal/dto/player_dto.go
  func FromPlayer(p *model.Player) *PlayerDTO          { ... }
  func FromPlayerList(ps []model.Player) []PlayerDTO   { ... }  // 保證回傳非 nil slice
  ```
- DTO 只包含前端需要的欄位，不含密碼雜湊、GORM 內部欄位（`DeletedAt`）、外鍵等。
- 同一 Model 可對應多個 DTO（例如 `PlayerListItemDTO` vs `PlayerDetailDTO`），按使用情境拆分。

### 17.2 Model 與 DTO 的分工

```
DB → Repository → Model → Service → Handler → DTO → 前端
                                        ↑
                              dto.FromXxx() 在此轉換
```

| | Model | DTO |
|---|---|---|
| 位置 | `internal/model/` | `internal/dto/` |
| 用途 | 對應資料表，GORM 操作 | 對應 API 回應，給前端 |
| 欄位 | 含所有 DB 欄位 | 只含前端需要的欄位 |
| Tag | `gorm:"..."` | `json:"..."` |

### 17.3 範例

```go
// internal/model/player.go
type Player struct {
    Base
    Name         string `gorm:"not null"`
    Email        string `gorm:"uniqueIndex;not null"`
    PasswordHash string `gorm:"not null"`
}

// internal/dto/player_dto.go
type PlayerDTO struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}

func FromPlayer(p *model.Player) *PlayerDTO {
    return &PlayerDTO{
        ID:    p.ID.String(),
        Name:  p.Name,
        Email: p.Email,
        // PasswordHash 刻意不包含
    }
}

// FromPlayerList 保證回傳非 nil slice，handler 可直接放入 Response.Data
// 避免每個 handler 重複 if result == nil { ... } 的樣板程式碼
func FromPlayerList(ps []model.Player) []PlayerDTO {
    out := make([]PlayerDTO, len(ps))
    for i := range ps {
        // 取 ps[i] 的位址而非 range 變數 p — 規避 Go 1.21 以前的 loop-var alias bug，
        // 也讓 linter 不警告；Go 1.22+ 修了 range alias，但此 pattern 更顯式且任何版本都正確。
        out[i] = *FromPlayer(&ps[i])
    }
    return out
}

// internal/handler/player_handler.go
func (h *PlayerHandler) Get(c *gin.Context) {
    player, err := h.service.GetPlayer(c.Request.Context(), id)
    if err != nil {
        HandleError(c, err)
        return
    }
    c.JSON(http.StatusOK, OK(c, dto.FromPlayer(player)))
}
```

---

## 18. 可觀測性 — Metrics & Tracing

### 18.1 設計原則

- 至少導入 **Metrics**（Prometheus），Tracing 預留未來接入 OpenTelemetry。
- Metrics 端點 `/metrics`：純明文 Prometheus 格式，無 auth、無限流；**端點本身會洩漏 `build_info`（版本 / commit）與所有業務 metric 名稱、label**（含 `client_id`、`role` 等），對外暴露形同 reconnaissance gift。**生產環境必須由網路層隔離**：
  - **推薦**（k8s）：以 `NetworkPolicy` 限制只能由 `monitoring` namespace 的 Prometheus pod 抓取，YAML 範例見 §24.3
  - **替代方案**：未來若需更嚴格，可在 `MetricsConfig` 增 `BindAddr`（獨立 listener bind 內部介面如 `127.0.0.1:9090`），與主應用 listener 物理隔離；目前 demo 階段未實作此選項
  - **禁止用 basic auth 等應用層認證**：增加 scrape 失敗風險與 Prometheus 配置複雜度，不如網路層隔離乾淨
- 業務指標（login 次數、refresh 換發次數）由 service 層自行記錄；HTTP 指標由 middleware 統一收。

### 18.2 內建指標

```go
// pkg/metrics/metrics.go

var (
    HTTPRequestsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "http_requests_total",
            Help: "Total HTTP requests by method, path, status",
        },
        []string{"method", "path", "status"},
    )

    // 含 status 標籤，便於切「200 P95 vs 5xx P95」儀表板。
    // status 的基數受限於 HTTP 規範（< 60 種），不會爆 Prometheus。
    HTTPRequestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "http_request_duration_seconds",
            Help:    "HTTP request latency by method, path, and status",
            Buckets: prometheus.DefBuckets,
        },
        []string{"method", "path", "status"},
    )

    RateLimiterErrors = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "ratelimit_errors_total",
            Help: "Rate limiter backend errors (Redis unavailable, etc.)",
        },
    )

    // UserMiddleware 在 claims 缺失時 fail-open；此計數讓告警系統能偵測中介層順序錯誤。
    // 規格上 > 0 即代表 production bug；告警閾值建議「5 分鐘內 > 0 即 page」。
    RateLimitMisconfigured = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "ratelimit_misconfigured_total",
            Help: "UserMiddleware invoked without claims — middleware order misconfigured",
        },
        []string{"path"},
    )

    AuthLoginAttempts = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "auth_login_attempts_total",
            Help: "Login attempts by result",
        },
        []string{"result"}, // success | invalid_credentials | invalid_client | error
    )

    AuthRotations = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "auth_token_rotations_total",
            Help: "Refresh token rotations by result",
        },
        []string{"result", "client_id"}, // rotated | grace_hit | replay_detected | family_not_found
    )

    // 安全告警指標：replay 通常代表帳號被盯上，運維可設長期高頻告警
    AuthReplayDetected = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "auth_replay_detected_total",
            Help: "Refresh token replay attacks detected",
        },
        []string{"client_id"},
    )

    // AuthMiddleware 查 blacklist 時 Redis 故障的次數（fail-open，見 §7.3 / §8.5）。
    // 短期上升 → Redis 抖動；持續上升 → blacklist 可能完全失效，應 page。
    AuthBlacklistErrors = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "auth_blacklist_errors_total",
            Help: "Errors querying access-token blacklist in AuthMiddleware (fail-open path)",
        },
    )

    // AuthMiddleware 查 user-revocation watermark 時 Redis 故障的次數（fail-open，見 §7.5 / §8.5）。
    // 與 AuthBlacklistErrors 同等告警語意（持續上升 → user-level 強制踢人可能失效）。
    AuthUserRevokeErrors = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "auth_user_revoke_errors_total",
            Help: "Errors querying user-revocation watermark in AuthMiddleware (fail-open path)",
        },
    )
)

// Init 註冊應用指標。平台指標（goroutine、process、go runtime）由 client_golang
// 預設 collector 自動暴露，無需手動註冊。
//
// 此函式接受 sqlDB 以註冊連線池指標 — 啟動順序必須在 database.Connect() 之後呼叫。
func Init(sqlDB *sql.DB, version, commit string) {
    prometheus.MustRegister(
        // 應用業務指標
        HTTPRequestsTotal,
        HTTPRequestDuration,
        RateLimiterErrors,
        RateLimitMisconfigured,
        AuthLoginAttempts,
        AuthRotations,
        AuthReplayDetected,
        AuthBlacklistErrors,
        AuthUserRevokeErrors,
        BuildInfo,
        // 平台指標
        collectors.NewBuildInfoCollector(),                    // build_info{...} 標出版本
        collectors.NewDBStatsCollector(sqlDB, "main"),         // sql_dbstats_* 連線池狀態
    )
    // 自定 build label（version / commit）— 透過 ldflags 注入：
    // go build -ldflags "-X main.Version=v1.2.3 -X main.Commit=$(git rev-parse HEAD)"
    BuildInfo.WithLabelValues(version, commit).Set(1)
}

// 額外的 build label gauge（讓 alerting 可依版本分組）
var BuildInfo = prometheus.NewGaugeVec(
    prometheus.GaugeOpts{
        Name: "app_build_info",
        Help: "Application build info; value always 1, version/commit as labels",
    },
    []string{"version", "commit"},
)

// GinMiddleware 記錄每個請求的指標。
// 注意：path 必須使用 c.FullPath()（含參數佔位符 :id），避免高基數爆 Prometheus。
func GinMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        c.Next()
        path := c.FullPath()
        if path == "" {
            path = "unknown"
        }
        status := strconv.Itoa(c.Writer.Status())
        HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
        HTTPRequestDuration.WithLabelValues(c.Request.Method, path, status).Observe(time.Since(start).Seconds())
    }
}
```

> **高基數陷阱**：Prometheus label value 數量爆炸會打爆 metrics 後端。`path` 一律用 `c.FullPath()`（例 `/players/:id`），絕不可用 `c.Request.URL.Path`。

### 18.3 Audit Log（`pkg/audit`）

#### 18.3.1 設計原則

Audit log 用於記錄**安全相關事件**：登入成功 / 失敗、token rotation、refresh 重放偵測、登出、session 撤銷。
與一般 application log 在用途與保存要求上不同，必須分離：

- **獨立 logger instance**：不受 `LOG_LEVEL` 影響，audit 永遠 `Info` 以上等級全部寫入，避免被 `LOG_LEVEL=warn` 過濾。
- **強制 JSON format**：不論主 logger 設定為 `console` 或 `json`，audit 一律 JSON，便於外部 SIEM ingest。
- **獨立 sink（可配置）**：預設與 application log 同 stdout（最簡單部署）；可透過 `LOG_AUDIT_PATH` 寫入獨立檔案 / 獨立 stream，未來可改接專用 SIEM / 安全資料庫而不改 caller。
- **檔案 rotation 責任**：本 module **不內建 lumberjack 等 rotation 套件**。若 `LOG_AUDIT_PATH` 指向檔案，rotation 必須由外部處理（**生產**：k8s sidecar 或 systemd journal；**本機開發**：直接 stdout 或外部 logrotate）。內建 rotation 會增加 module 體積與運維灰色地帶（檔案描述符、信號處理），但生產通常已有 sidecar 處理 — 此責任分工明確化。
- **不可被應用層覆寫**：audit logger interface 不公開 `SetLevel` 等可降級方法；只能寫入與 Sync。
- **每筆事件必填**：`event_type`、`timestamp`、`request_id`、`actor`（user_id），其餘為事件特有欄位。
- **生命週期**：必須在 graceful shutdown 中**先於 app logger** 呼叫 `Sync()`，避免 process 結束時 buffer 內安全事件遺失。
- **寫入錯誤處理**：`Log()` **不回傳 error**（caller 不該被 audit 寫失敗拖累業務邏輯）；實作必須內部 fallback — 若主 sink（檔案 / stdout）寫失敗，自動寫 `os.Stderr` 作為最後兜底，**永不吞錯**。對應「audit 不可被應用層覆寫」哲學：caller 寫 audit 後就視為「至少有一處記錄」，不需自行處理失敗。

#### 18.3.2 介面定義

```go
// pkg/audit/audit.go

type EventType string

const (
    EventRegisterSuccess EventType = "auth.register_success"
    EventRegisterFailed  EventType = "auth.register_failed"
    EventLoginSuccess    EventType = "auth.login_success"
    EventLoginFailed     EventType = "auth.login_failed"
    EventTokenRotated    EventType = "auth.token_rotated"
    EventReplayDetected  EventType = "auth.replay_detected"  // ⚠️ 觸發告警
    EventLogout          EventType = "auth.logout"
    EventSessionRevoked  EventType = "auth.session_revoked"
    EventRevokeAll       EventType = "auth.revoke_all"
)

// AuthEvent — 安全事件的結構化內容。Extra 給事件特有欄位用，
// 例如 replay_detected 帶 {presented_jti, current_jti, delta_sec}。
type AuthEvent struct {
    Type      EventType
    UserID    string         // actor；未登入事件（如 login_failed）可空
    FamilyID  string         // 事件涉及的 family（若有）
    ClientID  string
    IP        string
    UserAgent string
    Extra     map[string]any
}

// Logger — audit logger 唯一介面。實作必須 thread-safe，
// 內部從 context 自動帶出 request_id，呼叫端不需手動傳。
//
// Log **不回傳 error**：caller 寫 audit 後即視為「已記錄」，不該被 audit sink 寫失敗
// 拖累業務邏輯。實作必須內部 fallback — 主 sink 寫失敗時自動寫 os.Stderr（最後兜底），
// 永不吞錯。詳見 §18.3.1「寫入錯誤處理」。
//
// Sync 在 graceful shutdown 必須呼叫（且早於 app logger Sync），
// 確保 buffer 內安全事件落地；失敗時應寫 stderr 而非依賴 app logger。
type Logger interface {
    Log(ctx context.Context, event AuthEvent)
    Sync() error
}
```

#### 18.3.3 預設實作（zap）

```go
// pkg/audit/zap_logger.go

type zapAuditLogger struct {
    z *zap.Logger // 獨立 instance，core 來自 audit sink
}

// NewZapLogger 建立獨立 audit logger。
//   - path == ""  → 共用 stdout（最簡單部署，本機開發預設）
//   - path != ""  → 寫該檔案；rotation 必須由外部處理（§18.3.1）
//                  開檔失敗（父目錄不存在 / 權限不足 / 檔案被佔用）→ 回 error；
//                  **caller（main）必須 fatal 退出**，禁止 fallback 到 stdout 矇混（會讓 prod
//                  以為 audit 寫入檔案但實際進 stdout，破壞 SIEM ingest 假設）。
// 本 constructor 不接受 io.Writer，避免呼叫端誤把 app logger 的 sink 共用導致 LOG_LEVEL 污染 audit。
func NewZapLogger(path string) (Logger, error) {
    core, err := newAuditCore(path) // 內部固定 JSON encoder、InfoLevel、寫指定 sink；開檔失敗回 error
    if err != nil {
        return nil, fmt.Errorf("audit core: %w", err)
    }
    return &zapAuditLogger{z: zap.New(core)}, nil
}

// Log 寫入單筆 audit event。
// 內部 fallback：若 zap core 寫主 sink 失敗，writeSyncer 會自動降級寫 os.Stderr，
// 確保「永不吞錯」（§18.3.1 / Logger interface 註解）。
func (l *zapAuditLogger) Log(ctx context.Context, e AuthEvent) {
    l.z.Info(string(e.Type),
        zap.String("event_type", string(e.Type)),
        zap.String("request_id", ctxkey.RequestID(ctx)),  // 從 pkg/ctxkey 取，不 import pkg/logger（§2.1）
        zap.String("user_id", e.UserID),
        zap.String("fid", e.FamilyID),
        zap.String("client_id", e.ClientID),
        zap.String("ip", e.IP),
        zap.String("user_agent", e.UserAgent),
        zap.Any("extra", e.Extra),
    )
}

// Sync flush 內部 buffer。graceful shutdown 必呼叫。
// 對 stdout sink 通常無 op；對檔案 sink 強制 fsync。
func (l *zapAuditLogger) Sync() error {
    return l.z.Sync()
}
```

#### 18.3.4 整合點

| 觸發位置 | 寫入事件 | 必填 Extra |
|---|---|---|
| `AuthService.Register` CMS 自註冊成功 | `auth.register_success` | `{username, role: "user"}` |
| `AuthService.Register` CMS 自註冊失敗 | `auth.register_failed` | `{reason: "weak_password" \| "username_taken" \| "invalid_client", username}` |
| `AuthService.Login` 帳密驗證成功 | `auth.login_success` | — |
| `AuthService.Login` 帳密驗證失敗 | `auth.login_failed` | `{reason: "invalid_credentials" \| "invalid_client" \| ...}` |
| `AuthService.Refresh` rotation 成功 | `auth.token_rotated` | `{old_jti, new_jti}` |
| `AuthService.Refresh` Lua 回傳 ReplayDetected | `auth.replay_detected` | `{presented_jti, current_jti, delta_sec}` |
| `AuthService.Logout` | `auth.logout` | — |
| `DELETE /auth/sessions/:fid` | `auth.session_revoked` | `{revoked_fid, operator: user_id}` |
| `POST /auth/sessions/revoke-all` | `auth.revoke_all` | `{family_count}` |

#### 18.3.5 配置

對應 `LogConfig.AuditPath`（定義見 §4.2，env 範本見 §20）：

```env
LOG_AUDIT_PATH=                 # 空 → 共用 stdout；非空 → 寫入此檔案路徑
```

> Audit logger constructor 簽章為 `NewZapLogger(path string)`（見 §18.3.3），由 main 從
> `cfg.Log.AuditPath` 取值；開檔失敗 main 必須 fatal 退出，禁止 fallback 至 stdout。

#### 18.3.6 告警配對

`auth.replay_detected` 必須同步 `metrics.AuthReplayDetected.Inc()`。建議 PromQL 範本：

```promql
# 單一使用者 5 分鐘內重放偵測 > 3 次 → page
sum by (client_id) (increase(auth_replay_detected_total[5m])) > 3
```

> **未來工作**：當稽核需求成熟（PCI / ISO），把 audit sink 從 zap 切到專用 stream（Kafka / SQS）或安全資料庫，
> 不影響 caller — caller 只看 `audit.Logger` interface。

### 18.4 Tracing 預留

- 暫不導入 OpenTelemetry SDK。
- 所有 service / repository 簽章必須接收 `context.Context`，未來導入時直接掛 span。
- HTTP middleware 若日後加入 OTEL，可放在 `RequestID` 之後、`GinLogger` 之前。

---

## 19. 測試策略（配合 TDD）

| 模組 | 測試層級 | 工具 |
|------|---------|------|
| Config | Unit | `testify`，測試各優先順序、validate 失敗、跨欄位約束 |
| Logger | Unit | 驗證 output 欄位格式 |
| Request ID Middleware | Unit | 無 header 產生 UUID；合法 header 沿用；不合法 header 忽略並產生新 UUID |
| JWT Manager | Unit | SignAccess / VerifyAccess / SignRefresh / VerifyRefresh / iss 不符 / aud 不符 / exp 過期 / abs_exp 過期 / PolicyOf |
| Audit Logger | Unit | 各 event_type 寫入時帶齊 fields；zap 結構化驗證 |
| Ownership Middleware | Unit | CMS 放行；member 不符 403；無 claims 401 |
| DB Migration | Integration | 真實 PostgreSQL，驗證 up / down 冪等 |
| Repository | Integration | 真實 PostgreSQL |
| Service | Unit | Fake Repository（實作 interface） |
| Error Handler | Unit | 驗證各 domain error 對應 HTTP status / body / request_id；訊息不洩漏細節 |
| Health Handler | Unit + Integration | `/health` 純回應；`/health/ready` 搭配真實 DB + Redis |
| Rate Limiter | Unit + Integration | Fake store 驗證 429 邏輯；真實 Redis 驗證分散式行為 |
| Pagination | Unit | 驗證 Offset、預設值、上限邊界 |
| DTO 轉換 | Unit | 驗證 Model → DTO 欄位正確、敏感欄位不外漏 |
| Handler | E2E | `net/http/httptest`，搭配 `kin-openapi` 驗證 schema 對應 |
| Redis Blacklist / FamilyStore | Integration | 真實 Redis，驗證 Lua script 原子性、rotation CAS、grace window、replay 偵測 |
| Metrics | Unit | 驗證 middleware 正確 increment counter |

### 19.1 Build Tag 規範

Unit test 與 Integration test 以 Go build tag 分離：

```go
//go:build integration

// 放在 integration test 檔案第一行，unit test 檔案不加任何 tag
```

| 指令 | 執行範圍 |
|------|---------|
| `go test ./...` | 僅 unit tests（無外部依賴） |
| `go test -tags integration ./...` | 僅 integration tests |

Integration test 檔案命名規範：`xxx_integration_test.go`，同時加上 build tag。

### 19.2 測試環境

主路徑：`docker-compose.test.yml` 提供本地與 CI 一致的容器：

```yaml
# docker-compose.test.yml
services:
  postgres-test:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: playerledger_test
      POSTGRES_USER: test
      POSTGRES_PASSWORD: test
    ports: ["5433:5432"]

  redis-test:
    image: redis:7-alpine
    ports: ["6380:6379"]
```

本地執行 integration test：

```bash
docker compose -f docker-compose.test.yml up -d
go test -race -count=1 -tags integration ./...
docker compose -f docker-compose.test.yml down
```

> **替代方案 — testcontainers-go**
> 若不想手動管理容器生命週期，可改用 `testcontainers-go` 於測試碼內動態啟停容器。權衡：DX 較好但首次啟動較慢、需要 Docker socket 權限。目前以 docker-compose 為主路徑。

### 19.3 並行測試

- Unit test 預設 `t.Parallel()`，加速整體執行。
- Integration test 涉及共用 DB 時禁止 `t.Parallel()`，或使用獨立 schema 隔離。

### 19.4 Integration Test 資料隔離（DB 清理約定）

**預設採用「每測 Transaction Rollback」**，每個 test 在自己的 transaction 內讀寫，結束時 rollback；資料永遠不會落地。Repository test 必須接受 `*gorm.DB` 注入，由 helper 包成 transaction：

```go
// internal/repository/testhelper_integration_test.go

//go:build integration

// WithTx 為 test 提供 transaction 化的 *gorm.DB。
// test 結束時自動 ROLLBACK，下一個 test 看到的是乾淨狀態。
func WithTx(t *testing.T) *gorm.DB {
    t.Helper()
    tx := testDB.Begin()
    t.Cleanup(func() {
        tx.Rollback()
    })
    return tx
}

// 使用範例
func TestPlayerRepo_FindByID_存在_回傳資料(t *testing.T) {
    db := WithTx(t)
    repo := NewPlayerRepo(db)
    // ... 寫測試
}
```

例外情況：

| 情境 | 用法 |
|------|------|
| 預設 | **Transaction Rollback**（上述 `WithTx`） |
| 測試需驗證 commit 後行為（trigger、generated column 等） | `t.Cleanup` 中 `TRUNCATE TABLE foo CASCADE` |
| 測試需與其他 connection 隔離（例如測 advisory lock） | `CREATE SCHEMA test_<rand>` + 結束 DROP |

**禁止跨測試共用「種子資料」**：每個 test 自己建立所需的最小資料集；fixture 共用會造成隱性耦合。

### 19.5 Test 命名

對齊 CLAUDE.md 規範 `TestXxx_條件_預期結果`：

```
TestAuthService_Login_密碼正確_回傳兩個token
TestAuthService_Login_密碼錯誤_回傳ErrUnauthorized
TestAuthService_Login_未知client_id_回傳ErrInvalidClient
TestAuthService_Refresh_jti已被換發_觸發重放偵測並廢family
TestAuthService_Refresh_jti在GraceWindow內重送_用current_jti重簽refresh
TestAuthService_Refresh_abs_exp已過_回傳ErrAbsoluteExpired
TestAuthService_Refresh_iss不符_回傳ErrInvalidToken
TestAuthService_Refresh_aud不符_回傳ErrInvalidToken
TestAuthService_Sessions_多裝置_每個login產生獨立family
TestAuthService_RevokeAll_廢掉所有family並登出當前裝置
```

每個 test 只驗證一件事，看到名稱就知道測什麼，無需讀程式碼。

---

## 20. 環境變數清單

```env
# .env.example

# App
APP_ENV=dev                               # dev | staging | prod；決定載入哪份 config.yaml

# Server
PORT=8080
GIN_MODE=release                          # debug | release | test
ALLOWED_ORIGINS=http://localhost:3000     # 逗號分隔多個來源；ALLOW_CREDENTIALS=true 時不可為 *
ALLOW_CREDENTIALS=true
TRUSTED_PROXIES=                          # 逗號分隔 CIDR；空 = 完全不信任 proxy header
SHUTDOWN_TIMEOUT=10s
READ_HEADER_TIMEOUT=10s                   # 防 Slowloris
READ_TIMEOUT=30s
WRITE_TIMEOUT=30s
IDLE_TIMEOUT=120s
MAX_REQUEST_BODY=1048576                  # bytes，預設 1MB

# Database (PostgreSQL)
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=playerledger
DB_SSLMODE=disable                        # 生產環境必須 require 以上
DB_MAX_OPEN_CONNS=25
DB_MAX_IDLE_CONNS=5
DB_CONN_MAX_LIFETIME=5m
DB_CONNECT_TIMEOUT=5s
DB_STATEMENT_TIMEOUT=10s
DB_PREPARE_STMT=true                      # 直連 PG 設 true；走 PgBouncer transaction mode 必須 false（見 §6.1）

# Redis
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_PASSWORD=
REDIS_DB=0
REDIS_DIAL_TIMEOUT=5s
REDIS_READ_TIMEOUT=3s
REDIS_WRITE_TIMEOUT=3s
REDIS_POOL_SIZE=10

# JWT — 詳見 ADR 007（HS256；未來多服務時升級 RS256）
JWT_ISSUER=playerledger                   # 寫入 iss claim
JWT_SECRET=please-rotate-me-min-32-chars-long
JWT_SECRET_PREVIOUS=                      # 上一把 access secret；rotation grace 期間用於 verify fallback（空 = 無 grace）
                                          # 若設定：長度仍須 ≥32 且不可等於主 secret（v1.9 起 validate）
JWT_REFRESH_SECRET=must-differ-from-access-secret-32+
JWT_REFRESH_SECRET_PREVIOUS=              # 上一把 refresh secret；同上規則
JWT_ACCESS_TTL=15m                        # access token 絕對 TTL
JWT_GRACE_WINDOW=10s                      # rotation 後同 jti 可重送的窗口（吸收網路重試）
JWT_CLOCK_SKEW_LEEWAY=30s                 # Verify 時 exp/nbf/iat/abs_exp 容忍的時鐘漂移（多副本部署必備）

# 每個 client 的 refresh / absolute TTL（key = client_id；ABSOLUTE_TTL 必須 > REFRESH_TTL）
#
# Shell 環境變數名稱不允許 "." 或 "-"，因此一律以 "_" 取代 + 全大寫表示；
# Viper 載入時透過 SetEnvKeyReplacer 將 "." / "-" 換成 "_"，
# 內部 ClientPolicies map 的 key 仍為 client_id 字面值（cms-web / ios-app …）。
#
# 對應規則：
#   mapstructure key                                       → shell env
#   JWT_CLIENT_POLICIES.cms-web.REFRESH_TTL                → JWT_CLIENT_POLICIES_CMS_WEB_REFRESH_TTL
#   JWT_CLIENT_POLICIES.public-web.ABSOLUTE_TTL            → JWT_CLIENT_POLICIES_PUBLIC_WEB_ABSOLUTE_TTL
JWT_CLIENT_POLICIES_CMS_WEB_REFRESH_TTL=1h
JWT_CLIENT_POLICIES_CMS_WEB_ABSOLUTE_TTL=8h
JWT_CLIENT_POLICIES_PUBLIC_WEB_REFRESH_TTL=1h
JWT_CLIENT_POLICIES_PUBLIC_WEB_ABSOLUTE_TTL=24h
JWT_CLIENT_POLICIES_IOS_APP_REFRESH_TTL=720h       # 30d
JWT_CLIENT_POLICIES_IOS_APP_ABSOLUTE_TTL=4320h     # 180d
JWT_CLIENT_POLICIES_ANDROID_APP_REFRESH_TTL=720h
JWT_CLIENT_POLICIES_ANDROID_APP_ABSOLUTE_TTL=4320h

BCRYPT_COST=12                            # 10-15，2026 推薦 12

# Logging
LOG_LEVEL=info                            # debug | info | warn | error
LOG_FORMAT=json                           # json | console
LOG_SERVICE=playerledger                  # 注入每筆日誌的 service 欄位（env 取自 APP_ENV）
LOG_AUDIT_PATH=                           # 空 → audit 共用 stdout；非空 → 寫入該檔案路徑（詳見 §18.3）

# Rate Limiting — IP 與 User 兩層獨立配置（見 §15）
RATE_LIMIT_ENABLED=true
RATE_LIMIT_IP_PERIOD=1m                   # 外層：所有人都受限
RATE_LIMIT_IP_MAX=60
RATE_LIMIT_USER_PERIOD=1m                 # 內層：登入後改用 user key，額度可較寬鬆
RATE_LIMIT_USER_MAX=300

# Metrics
METRICS_ENABLED=true
METRICS_PATH=/metrics
```

### 20.1 config.yaml.example

YAML 與 env 採**同一份扁平命名**（對齊 §4.2 mapstructure tag）。Viper 載入優先順序：env > .env > `config.{APP_ENV}.yaml` > SetDefault。**dev 環境用 yaml 可保留註解，prod 一律走 env / secret manager**。

```yaml
# config.dev.yaml.example
# 不放任何密碼 / token；prod 一律走 env 或 secret manager。

APP_ENV: dev

# Server
PORT: 8080
GIN_MODE: debug
ALLOWED_ORIGINS: http://localhost:3000
ALLOW_CREDENTIALS: true
TRUSTED_PROXIES: ""
SHUTDOWN_TIMEOUT: 10s
READ_HEADER_TIMEOUT: 10s
READ_TIMEOUT: 30s
WRITE_TIMEOUT: 30s
IDLE_TIMEOUT: 120s
MAX_REQUEST_BODY: 1048576

# Database
DB_HOST: localhost
DB_PORT: 5432
DB_USER: postgres
# DB_PASSWORD: 由 env 提供
DB_NAME: playerledger
DB_SSLMODE: disable
DB_MAX_OPEN_CONNS: 25
DB_MAX_IDLE_CONNS: 5
DB_CONN_MAX_LIFETIME: 5m
DB_CONNECT_TIMEOUT: 5s
DB_STATEMENT_TIMEOUT: 10s
DB_PREPARE_STMT: true

# Redis
REDIS_HOST: localhost
REDIS_PORT: 6379
# REDIS_PASSWORD: 由 env 提供
REDIS_DB: 0
REDIS_DIAL_TIMEOUT: 5s
REDIS_READ_TIMEOUT: 3s
REDIS_WRITE_TIMEOUT: 3s
REDIS_POOL_SIZE: 10

# JWT — 詳見 ADR 007
JWT_ISSUER: playerledger
# JWT_SECRET / JWT_REFRESH_SECRET / 對應 PREVIOUS：一律 env，禁止寫 yaml
JWT_ACCESS_TTL: 15m
JWT_GRACE_WINDOW: 10s
JWT_CLOCK_SKEW_LEEWAY: 30s

# ClientPolicies — yaml 用巢狀 map，key 直接用原 client_id 字面值（"-" 不需轉 "_"）
JWT_CLIENT_POLICIES:
  cms-web:
    REFRESH_TTL: 1h
    ABSOLUTE_TTL: 8h
  public-web:
    REFRESH_TTL: 1h
    ABSOLUTE_TTL: 24h
  ios-app:
    REFRESH_TTL: 720h       # 30d
    ABSOLUTE_TTL: 4320h     # 180d
  android-app:
    REFRESH_TTL: 720h
    ABSOLUTE_TTL: 4320h

BCRYPT_COST: 12

# Logging
LOG_LEVEL: debug
LOG_FORMAT: console
LOG_SERVICE: playerledger
LOG_AUDIT_PATH: ""

# Rate Limiting
RATE_LIMIT_ENABLED: true
RATE_LIMIT_IP_PERIOD: 1m
RATE_LIMIT_IP_MAX: 60
RATE_LIMIT_USER_PERIOD: 1m
RATE_LIMIT_USER_MAX: 300

# Metrics
METRICS_ENABLED: true
METRICS_PATH: /metrics
```

> **與 env 命名的對齊**：yaml key 等同 env 變數名（全大寫 + 底線），唯一差異是 `JWT_CLIENT_POLICIES`：yaml 用巢狀 map（client_id 保留原字面值如 `cms-web`），env 用扁平 `JWT_CLIENT_POLICIES_CMS_WEB_*`（"_" 取代 "-"），由 viper `SetEnvKeyReplacer` 對齊。
>
> **為何選擇全大寫扁平命名而非 yaml 慣例的小寫 nested**：
> 1. yaml / env 命名一致，跨格式切換無心智成本
> 2. `mapstructure:",squash"`（見 §4.2）攤平到根層，不需額外一層 key 對應
> 3. 避免「yaml 寫 `server.port`，env 寫 `PORT`」載入時對不上
>
> **Secret 不進 yaml**：`JWT_SECRET` / `JWT_REFRESH_SECRET` / `DB_PASSWORD` / `REDIS_PASSWORD` 等敏感欄位**絕不**寫進 yaml（即便範本也不示範），免得有人 copy-paste 進 prod。一律走 env 或 secret manager。

---

## 21. 依賴套件清單（go.mod）

```
require (
    github.com/gin-gonic/gin               v1.10.x
    github.com/gin-contrib/cors            v1.7.x
    github.com/unrolled/secure             v1.16.x
    github.com/spf13/viper                 v1.19.x
    github.com/joho/godotenv               v1.5.x   // .env 讀取
    github.com/mitchellh/mapstructure      v1.5.x   // viper decode hook
    go.uber.org/zap                        v1.27.x
    gorm.io/gorm                           v1.25.x
    gorm.io/driver/postgres                v1.5.x
    moul.io/zapgorm2                       v1.3.x
    github.com/jackc/pgx/v5                v5.7.x   // pgconn.PgError 判別
    github.com/golang-migrate/migrate/v4   v4.18.x   // 含 source/iofs 子套件供 embed.FS 用
    github.com/golang-jwt/jwt/v5           v5.2.x
    github.com/redis/go-redis/v9           v9.6.x
    github.com/ulule/limiter/v3            v3.11.x
    github.com/go-playground/validator/v10 v10.22.x
    github.com/google/uuid                 v1.6.x
    github.com/mileusna/useragent          v1.3.x   // family device_label 解析
    github.com/prometheus/client_golang    v1.20.x
    golang.org/x/crypto                    v0.27.x  // bcrypt
    github.com/getkin/kin-openapi          v0.127.x // schema 驗證（test 用）
    github.com/stretchr/testify            v1.9.x
)
```

---

## 22. 實作順序建議

依 TDD 流程，每步先寫測試：

1. `config` — 載入、優先順序、validate、跨欄位驗證
2. `pkg/ctxkey` — context.Context typed key + `SetRequestID` / `RequestID` helper（最底層，無依賴；§5.4）
3. `pkg/logger` — `Init` / `L()`（init-safe nop fallback）/ `With` / `GinLogger`（含完整 zap.Field 清單）/ `RequestID` middleware / `GetRequestID`（gin 版）
4. `pkg/metrics` — Prometheus registry + Gin middleware（含 status label）+ `RateLimitMisconfigured` + `AuthBlacklistErrors` + 平台指標（BuildInfo / DBStatsCollector）
5. `pkg/httpx` — `MaxBodyBytes` / `SecureHeaders`（依 env 切 HSTS）/ `WriteError` / **`GinRecovery`（v1.9 從 logger 搬入）**
6. `pkg/database` — 連線管理（zapgorm2 整合）+ **embed.FS migration**（DSN 含 `statement_timeout` + 密碼 redact）
7. `migrations/` — 第一版 migration 腳本（`000001_create_cms_users` / `000002_create_members` / `000003_seed_initial_admin`，見 §13.5）+ `RunMigrations()`
8. `pkg/redis` — 連線管理、`AccessTokenBlacklist`（含 fail-open IsBlacklisted）、**FamilyStore（Lua CAS + grace window + replay 偵測 + lazy cleanup；`NewFamilyStore(ctx, client, cfg)` constructor 內自動 SCRIPT LOAD，介面只暴露 `ScriptsLoaded()` 供 `/health/ready`）**
9. `pkg/auth/hasher` — `Hasher` interface + bcrypt 實作（service / repository 禁止直接呼叫 bcrypt）
10. `pkg/jwt` — Sign / Verify（HS256；alg 鎖定 / iss / aud 白名單 / exp / nbf / iat 含 leeway / abs_exp，見 §8.3）/ `context.go`（typed key）/ `UserID()` helper / `PolicyOf` / `AuthMiddleware`（含 blacklist fail-open）/ `RequireRole` / `RequireOwnership`
11. `pkg/audit` — `Logger` interface（`Log` 無 error；內部 stderr fallback；`Sync()`）+ zap 實作（獨立 sink、JSON 強制、event_type 列表，詳見 §18.3）；**不 import `pkg/logger`**，request_id 取自 `pkg/ctxkey`
12. `internal/apperr` — domain errors 定義（含 `ErrTokenExpired` / `ErrAbsoluteExpired` / `ErrReplayDetected` / `ErrInvalidClient` / `ErrUsernameTaken` / `ErrWeakPassword` 等）
13. `internal/handler/response` — `Response[T]`、`ErrorResponse`、`OK` / `OKList` constructor、`Meta`、`FieldError`
14. `internal/handler/error_handler` — 錯誤對應 HTTP status（用 ErrorResponse；不洩漏細節）
15. `internal/handler/health_handler` — `/health` 與 `/health/ready`（含 `family_store_scripts` 檢查）
16. `pkg/ratelimit` — `NewRedisStore`（§15.4）+ **`IPMiddleware` + `UserMiddleware` 兩層**，fail-open + metrics（含 `RateLimitMisconfigured`）
17. `internal/pagination` — 分頁 helper
18. `internal/model` — DB 實體（先做 `CMSUser` / `Member`，見 §6.5；其他業務 model 之後加）
19. `internal/dto` — DTO 定義 + `FromXxx` / `FromXxxList` 轉換函式
20. `internal/repository` — 資料存取（integration test，採 transaction rollback 隔離）；先做 `CMSUserRepository`（`FindByUsername` + `Create`）與 `MemberRepository`（只 `FindByUsername`），見 §6.5
21. `internal/service/auth` — **`AuthService` 介面（§8.9）完整 7 個 method**：Register / Login / Refresh / Logout / ListSessions / RevokeSession / RevokeAll；含弱密碼檢查、依 client_id 路由表、GraceHit 重簽流程（含 nil state / PolicyOf err 處理）、UA 解析、audit log 串接、`RevokeAll` 加當前 access JTI 入黑名單
22. `internal/service/<domain>` — 業務邏輯（unit test with fake repo）
23. `internal/handler/auth` — **7 個 auth endpoint**（含 `POST /auth/register`，依 `schema/openapi.yaml`，E2E test with httptest + kin-openapi 驗證；handler 骨架見 §9.5）
24. `internal/handler/<domain>` — 其他 HTTP handler
25. `cmd/server/main.go` — 組裝所有模組（HTTP timeouts、TrustedProxies、兩層 rate limit、Graceful Shutdown 含 audit Sync）

---

## 23. GitHub Actions CI

### 23.1 設計原則

- 觸發時機：**PR 開啟 / 更新**與 **push 到 main**。
- 五個 job：`lint`、`schema-lint`、`test-unit`、`test-integration`、`security` 平行；`build` 等待全部通過。
- 全程開啟 `-race` detector，及早發現 data race。
- Integration test 透過 GitHub Actions `services` 啟動 PostgreSQL 與 Redis。
- `go test -count=1` 禁止測試快取。
- Coverage 上傳至 Codecov，PR 中顯示變化。
- 漏洞掃描：`govulncheck`（Go 官方）+ `gosec`（SAST）。

### 23.2 Job 相依關係

```
lint ────────────┐
schema-lint ─────┤
test-unit ───────┼──→ build
test-integration ┤
security ────────┘
```

### 23.3 Workflow 設定

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

env:
  GO_VERSION: '1.25'
  GOLANGCI_LINT_VERSION: v1.61.0
  REDOCLY_VERSION: 1.25.0
  GOVULNCHECK_VERSION: v1.1.3
  GOSEC_VERSION: v2.21.4

jobs:
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          # cache: true 預設以 go.sum hash 作為 cache key（actions/setup-go v3+），
          # 需 repo 已 commit go.sum（CI 不會自動產生）。go.sum 缺 → cache miss 每次重抓 deps。
          cache: true
      - uses: golangci/golangci-lint-action@v6
        with:
          version: ${{ env.GOLANGCI_LINT_VERSION }}

  schema-lint:
    name: OpenAPI Schema Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npx -y @redocly/cli@${{ env.REDOCLY_VERSION }} lint schema/openapi.yaml

  test-unit:
    name: Unit Tests
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: Run unit tests
        run: go test -race -count=1 -coverprofile=coverage.out ./...
      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: coverage.out
          flags: unit

  test-integration:
    name: Integration Tests
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_DB: playerledger_test
          POSTGRES_USER: test
          POSTGRES_PASSWORD: test
        ports:
          - 5433:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
      redis:
        image: redis:7-alpine
        ports:
          - 6380:6379
        options: >-
          --health-cmd "redis-cli ping"
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
    env:
      DB_HOST: localhost
      DB_PORT: 5433
      DB_USER: test
      DB_PASSWORD: test
      DB_NAME: playerledger_test
      DB_SSLMODE: disable
      REDIS_HOST: localhost
      REDIS_PORT: 6380
      REDIS_PASSWORD: ""
      REDIS_DB: 0
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: Run integration tests
        run: go test -race -count=1 -tags integration -coverprofile=coverage.out ./...
      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: coverage.out
          flags: integration

  security:
    name: Security Scan
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: govulncheck
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@${{ env.GOVULNCHECK_VERSION }}
          govulncheck ./...
      - name: gosec
        uses: securego/gosec@${{ env.GOSEC_VERSION }}
        with:
          args: ./...

  build:
    name: Build
    runs-on: ubuntu-latest
    needs: [lint, schema-lint, test-unit, test-integration, security]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: Build binary
        run: go build ./cmd/server/...
```

### 23.4 golangci-lint 設定

```yaml
# .golangci.yml
linters:
  enable:
    - errcheck
    - gosimple
    - govet
    - ineffassign
    - staticcheck
    - unused
    - gofmt
    - goimports
    - misspell
    - bodyclose       # 確保 HTTP response body 關閉
    - noctx           # http 請求必須帶 context
    - revive          # 風格檢查
    - gocritic        # 額外靜態分析

linters-settings:
  errcheck:
    check-type-assertions: true

issues:
  exclude-rules:
    - path: "_test.go"
      linters: [errcheck, bodyclose]
```

### 23.5 自動化補件

- **Dependabot**：`.github/dependabot.yml` 每週掃描 `gomod` 與 `github-actions` 升級 PR。
- **CodeQL**：`.github/workflows/codeql.yml` 每週掃描程式碼安全性。

### 23.6 本地等效指令（Makefile）

```makefile
# Makefile
.PHONY: lint test-unit test-integration build security migrate-up

lint:
	golangci-lint run ./...
	npx -y @redocly/cli@1.25.0 lint schema/openapi.yaml

test-unit:
	go test -race -count=1 -cover ./...

test-integration:
	docker compose -f docker-compose.test.yml up -d
	go test -race -count=1 -tags integration ./... || (docker compose -f docker-compose.test.yml down; exit 1)
	docker compose -f docker-compose.test.yml down

security:
	govulncheck ./...
	gosec ./...

build:
	go build \
	  -ldflags "-X main.Version=$(shell git describe --tags --always) \
	            -X main.Commit=$(shell git rev-parse HEAD)" \
	  -o bin/server ./cmd/server

migrate-up:
	migrate -path ./migrations -database "$(DB_URL)" up
```

---

## 24. 部署 — Dockerfile / Container

### 24.1 設計原則

- **Multi-stage build**：第一階段編譯，第二階段用 distroless base 跑 binary，最終映像 < 30 MB。
- **Non-root user**：以 UID 65532 執行（distroless `nonroot` user），符合容器最佳實踐與 k8s `runAsNonRoot: true` policy。
- **HEALTHCHECK**：指 `/health/ready`，失敗即被 orchestrator 重啟。
- **Build args 注入版本**：`VERSION` / `COMMIT` 透過 `ldflags` 注入 binary，與 `metrics.BuildInfo` 對齊（見 §18.2）。
- **CGO 關閉**：`CGO_ENABLED=0` + `-tags=netgo,osusergo`，避免 distroless 缺 glibc。
- **時區 / 憑證**：distroless `:nonroot` 已內含 ca-certificates 與 tzdata；若用 `:static-nonroot` 需自行 COPY。

### 24.2 Dockerfile

```dockerfile
# syntax=docker/dockerfile:1.7

# ----- builder -----
FROM golang:1.25-alpine AS builder
WORKDIR /src

# 利用 build cache：先放 go.mod / go.sum
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# 再放原始碼
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ENV CGO_ENABLED=0 GOOS=linux

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
      -tags=netgo,osusergo \
      -o /out/server ./cmd/server

# ----- runtime -----
# Production 強烈建議改用 sha256 digest 鎖死（防 supply-chain image swap），由 dependabot 自動升版：
#   FROM gcr.io/distroless/static-debian12:nonroot@sha256:<pin-via-dependabot>
# Dev/CI 用 tag 即可；prod 部署前 CI 可加 check：若 image 仍含 `:nonroot` 不含 digest 則 fail。
FROM gcr.io/distroless/static-debian12:nonroot

# distroless 已有 /etc/passwd 的 nonroot user，UID 65532
USER nonroot:nonroot

WORKDIR /app
COPY --from=builder --chown=nonroot:nonroot /out/server /app/server

# Migrations 放映像內，啟動時跑 RunMigrations()（見 §13）
COPY --from=builder --chown=nonroot:nonroot /src/migrations /app/migrations

EXPOSE 8080

# Healthcheck 由 orchestrator 觸發；Docker 自帶的 HEALTHCHECK 需要 shell，distroless 沒有，
# 因此交給 k8s livenessProbe / readinessProbe 配置（見 §24.3）
ENTRYPOINT ["/app/server"]
```

### 24.3 Kubernetes 探針配置範例

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /health/ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 2

securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
```

**`/metrics` 網路層隔離**（對齊 §18.1）— 限制只能由 `monitoring` namespace 的 Prometheus pod 抓取，避免端點對外暴露洩漏 build_info 與業務 label：

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: playerledger-allow-metrics-from-monitoring
spec:
  podSelector:
    matchLabels:
      app: playerledger
  policyTypes: [Ingress]
  ingress:
    # 業務流量（從 ingress controller / api gateway 進來）
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
      ports:
        - protocol: TCP
          port: 8080
    # /metrics 限定 monitoring namespace 的 Prometheus
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - protocol: TCP
          port: 8080
```

> 上述兩條 `from` 都允許 port 8080，是因為本專案 `/metrics` 與業務 endpoint 共用同一 listener（§18.1 替代方案尚未啟用）；以 NetworkPolicy 限制「誰能連」即可。若日後啟用獨立 listener，把第二條 `port` 改為 `9090` 即可。

### 24.4 建置與發布

```bash
# 本機建置（注入 git tag + commit）
docker build \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  -t playerledger/api:$(git describe --tags --always) .

# CI 推 registry（範例）
docker tag playerledger/api:v1.2.3 ghcr.io/<org>/playerledger:v1.2.3
docker push ghcr.io/<org>/playerledger:v1.2.3
```

### 24.5 環境變數注入

容器**禁止 baking secret 進映像**；secret 一律透過 k8s Secret / Docker Compose env_file / cloud secret manager 注入。
所有支援的環境變數見 §20。

