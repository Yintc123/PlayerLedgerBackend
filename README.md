# PlayerLedger Backend

玩家儲值紀錄管理系統的 API 伺服器，使用 Go + Gin 建置。提供 CMS 後台（人員管理、玩家查詢、儲值紀錄管理）與玩家自助查詢，採純 JWT 認證（refresh token rotation + replay detection）。

## 開發方法

本專案採用 **SDD + TDD** 開發流程：

- **SDD（Schema-Driven Development）**：以 OpenAPI Schema 為唯一契約（`schema/openapi.yaml`），前後端依此並行開發；handler 的 request/response 結構必須與 Schema 嚴格對應。
- **TDD（Test-Driven Development）**：先寫失敗測試再實作（Red → Green → Refactor），repository 以 interface 注入、測試替換為 fake。詳見 [`CLAUDE.md`](./CLAUDE.md)。

## 技術棧

| 領域 | 選用 |
|------|------|
| 語言 / 框架 | Go 1.25、[Gin](https://github.com/gin-gonic/gin) |
| 資料庫 | PostgreSQL + [GORM v2](https://gorm.io/) |
| 認證 / Session | [golang-jwt v5](https://github.com/golang-jwt/jwt)（HS256）、[Redis](https://github.com/redis/go-redis)（family store / 黑名單 / 限流） |
| 密碼雜湊 | bcrypt |
| 設定 | [Viper](https://github.com/spf13/viper)（`.env` + `config.yaml`，依 `APP_ENV` 載入） |
| 日誌 / 稽核 | [Zap](https://github.com/uber-go/zap)（+ zapgorm2） |
| 可觀測性 | [Prometheus](https://github.com/prometheus/client_golang) `/metrics` |
| 部署 | 容器（Dockerfile）/ AWS Lambda（Lambda Web Adapter）/ Kubernetes |

認證模型與 token rotation 設計見 `docs/adr/007-*` 與 `docs/specs/infrastructure.md §8`。

## 專案結構

```
PlayerLedgerBackend/
├── cmd/
│   ├── server/         # HTTP 伺服器進入點（main）
│   └── seed/           # 開發用假資料產生器
├── internal/
│   ├── handler/        # HTTP handler（對齊 OpenAPI 的 request/response）
│   ├── service/        # 業務邏輯（不變量、狀態機、權限）
│   ├── repository/     # 資料存取層（GORM；以 interface 注入）
│   ├── model/          # GORM model（cms_user / member / deposit_record）
│   ├── dto/            # API 輸入/輸出 DTO（含 PII 遮罩）
│   ├── apperr/         # 領域 sentinel 錯誤
│   └── pagination/     # 分頁工具
├── pkg/                # 可重用基礎設施
│   ├── jwt/            # JWT 簽發/驗證、AuthMiddleware、RequireRole
│   ├── auth/           # 密碼 hasher（bcrypt）
│   ├── redis/          # Redis client
│   ├── database/       # GORM/PostgreSQL 連線
│   ├── audit/          # 稽核日誌
│   ├── logger/         # Zap logger
│   ├── metrics/        # Prometheus 指標
│   ├── ratelimit/      # 兩層（IP / User）限流
│   ├── httpx/          # 統一成功/錯誤回應
│   ├── ctxkey/         # context key
│   └── ua/             # User-Agent 解析
├── migrations/         # golang-migrate SQL（啟動時自動套用）
├── schema/             # OpenAPI 契約（唯一 API 來源）+ schema 驗證測試
├── config/             # Viper 設定載入與驗證
├── docs/specs/         # 規格書（infrastructure / *-api / *-model）
├── k8s/                # Kubernetes manifests
├── Dockerfile          # 容器映像
├── Dockerfile.lambda   # AWS Lambda 容器映像
├── template.yaml       # AWS SAM 範本
└── Makefile            # 建置 / 測試 / 部署任務
```

## 快速開始

### 前置需求

- Go 1.25+
- Docker（跑本地 PostgreSQL + Redis）
- [`golang-migrate`](https://github.com/golang-migrate/migrate) CLI（手動跑 migration 時）

### 1. 設定環境變數

```bash
cp .env.example .env   # 依需求調整 DB / Redis / JWT secret 等值
```

JWT 兩組 secret 必須各 ≥ 32 字元；`APP_ENV=prod` 時 config 會強制驗證 secret 強度、SSL mode、admin seed 等（見 `.env.example` 註解）。

### 2. 啟動相依服務

```bash
make docker-up         # 啟動測試用 PostgreSQL + Redis 容器
go mod download
```

### 3. 啟動伺服器

```bash
make run               # = go build ./cmd/server 後執行 bin/server
```

API 預設監聽 `http://localhost:8080`，base path 為 `/api`。**Migration 於伺服器啟動時自動套用**。

### 4.（選用）灌入開發假資料

```bash
make seed              # 20 筆 members + 50 筆 deposit records（冪等；prod 會中止）
```

## API 端點

所有業務端點掛在 `/api` 之下；完整 request/response、錯誤碼與權限定義以 [`schema/openapi.yaml`](./schema/openapi.yaml) 為準。

### 認證（`/api/auth`）

| 方法 | 路徑 | 說明 |
|------|------|------|
| POST | `/auth/register` | CMS 自註冊（限 `client_id=cms-web`，預設 role=user） |
| POST | `/auth/login` | 帳密登入，簽發 access + refresh token |
| POST | `/auth/refresh` | 以 refresh token 換新 token（rotation + replay 偵測） |
| POST | `/auth/logout` | 登出當前裝置 |
| GET | `/auth/sessions` | 列出我所有登入裝置 |
| DELETE | `/auth/sessions/{fid}` | 撤銷指定裝置 |
| POST | `/auth/sessions/revoke-all` | 全裝置登出 |

### CMS 後台（`/api/cms`，需 `utype=cms`）

| 方法 | 路徑 | 權限 | 說明 |
|------|------|------|------|
| POST | `/cms/deposit-records` | admin / user | 建立儲值紀錄（初始 `pending`） |
| GET | `/cms/deposit-records` | 全 CMS staff | 列出儲值紀錄（分頁 + 篩選） |
| GET | `/cms/deposit-records/{id}` | 全 CMS staff | 取單筆儲值紀錄 |
| PATCH | `/cms/deposit-records/{id}` | admin | 更新狀態 / 備註（狀態機驗證） |
| GET | `/cms/players` | 全 CMS staff | 搜尋玩家（keyset cursor；viewer PII 遮罩） |
| GET | `/cms/players/{id}` | 全 CMS staff | 玩家詳情（viewer PII 遮罩） |
| GET | `/cms/users` | 全 CMS staff | 列出 CMS users |
| GET | `/cms/users/{id}` | 全 CMS staff | 取單筆 CMS user |
| PATCH | `/cms/users/{id}` | admin | 更新 username / role |
| DELETE | `/cms/users/{id}` | admin | 軟刪除 CMS user |
| PATCH | `/cms/users/me` | 本人 | 自助改 username / 密碼 |

### 玩家自助（`/api/me`，需 `utype=member`）

| 方法 | 路徑 | 說明 |
|------|------|------|
| GET | `/me/deposit-records` | 查自己的儲值紀錄（`player_id` 取自 token） |

### 維運

| 方法 | 路徑 | 說明 |
|------|------|------|
| GET | `/health` | liveness |
| GET | `/health/ready` | readiness（DB / Redis） |
| GET | `/metrics` | Prometheus 指標（須由網路層隔離） |

## 開發任務（Makefile）

```bash
make fmt               # go fmt
make lint              # golangci-lint + OpenAPI lint（@redocly/cli）
make test-unit         # 單元測試（-race -cover）
make test-integration  # 整合測試（自動起停測試容器）
make test              # unit + integration
make security          # govulncheck + gosec
make build             # 建置 bin/server（注入 Version/Commit）
make migrate-up        # 手動套用 migration
make migrate-down      # 回滾最後一筆 migration
make help              # 列出所有任務
```

直接跑測試：

```bash
go test ./...                 # 全部
go test ./... -run TestXxx    # 指定測試
go test ./... -cover          # 覆蓋率
```

## 部署

- **容器**：以 `Dockerfile` 建置映像，環境變數見 `.env.example`。
- **AWS Lambda**：`make build-lambda-zip` 產出 `bin/lambda.zip`（provided.al2023 + arm64 + Lambda Web Adapter），或用 `Dockerfile.lambda` / `template.yaml`（SAM）。
- **Kubernetes**：manifests 於 `k8s/`。

詳細部署步驟見 [`DEPLOYMENT.md`](./DEPLOYMENT.md)。

## 文件

- API 契約：[`schema/openapi.yaml`](./schema/openapi.yaml)
- 基礎設施與認證設計：`docs/specs/infrastructure.md`
- 各領域 API / model 規格：`docs/specs/*-api.md`、`docs/specs/*-model.md`
- 開發規範（TDD / SDD）：[`CLAUDE.md`](./CLAUDE.md)
