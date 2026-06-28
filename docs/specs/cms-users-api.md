# CMS Users API 規格書

版本：v1.0（草案，未實作）
範圍：CMS 內部人員（`cms_users` 表）的管理 API
對應規格：`infrastructure.md` §3（OpenAPI）、§8（Auth）、§10（Response）、§12（Errors）、§17（DTO）
對應 ADR：暫無

---

## 1. 設計原則

- **規格驅動（SDD）**：所有 endpoint 必須先在 `schema/openapi.yaml` 定義，再寫 handler 與測試（與 §3 對齊）。
- **回應 envelope 一致性**：成功走 `Response<T>` / `204`，錯誤走共用 `ErrorResponse`（§10.2 / §12.4）。
- **DTO 隔離**：絕不回傳 `password_hash`、`deleted_at`（除非 explicitly 列出已刪除）等內部欄位。
- **不變量強制**：lock-out 防護（最後一個 admin 不可降級或刪除）在 service 層集中檢查，不靠 caller。
- **稽核敏感操作**：role 變更、刪除、自助改密碼皆寫 audit log（§18.3）。
- **冪等**：DELETE 對已刪除帳號回 404；CREATE 對重複 username 回 409。

---

## 2. 權限模型

CMS users 表內所有人（admin / user / viewer）統一稱「CMS staff」。

| 動作 | admin | user | viewer | member | 未登入 |
|---|---|---|---|---|---|
| `GET /cms/users` list | ✅ | ✅ | ✅ | ❌ | ❌ |
| `GET /cms/users/:id` | ✅ | ✅ | ✅ | ❌ | ❌ |
| `PATCH /cms/users/:id` 改別人 | ✅ | ❌ | ❌ | ❌ | ❌ |
| `DELETE /cms/users/:id` | ✅ | ❌ | ❌ | ❌ | ❌ |
| `PATCH /cms/users/me` 改自己 | ✅ | ✅ | ✅ | ❌ | ❌ |

### 2.1 為何**不**在這份規格定義 `POST /cms/users`

新帳號建立走**既有公開端點** `/auth/register`（見 `infrastructure.md` §3.5.2 與 `auth_service.Register`）——
前端有專屬註冊頁面，任何人皆可註冊，新帳號 role 永遠 `user`。
本規格只負責「已存在帳號」的管理（讀、改、刪）。

升級為 admin 必須走 `PATCH /cms/users/:id`（admin only），這是唯一晉升路徑。

### 2.2 為何不分 super admin vs admin

當前需求簡單，避免提早引入多層 RBAC。Seed admin（透過 `ADMIN_USERNAME/PASSWORD` env 建立）
與後續 admin 在資料庫無差別；要防止 lock-out 靠 §6 的「最後一個 admin 不可動」不變量。

### 2.3 為何 member 完全擋掉

`member` 是前端玩家，永遠不該碰 CMS 表；middleware 層用 `RequireUserType(UserTypeCMS)` 一次擋掉。

---

## 3. Endpoint 清單

| 方法 | 路徑 | Auth | 權限 | Service | 摘要 |
|---|---|---|---|---|---|
| GET | `/cms/users` | access token | CMS staff | `CMSUserService.List` | 列出 CMS users，分頁、可篩 role / include_deleted |
| GET | `/cms/users/{id}` | access token | CMS staff | `CMSUserService.Get` | 取單筆 |
| PATCH | `/cms/users/{id}` | access token | admin only | `CMSUserService.Update` | 改 username / role；**role 變更時自動 revoke target sessions**（§4.4）|
| DELETE | `/cms/users/{id}` | access token | admin only | `CMSUserService.SoftDelete` | 軟刪除 + **自動 revoke target sessions**（§4.5）|
| PATCH | `/cms/users/me` | access token | 自己 | `CMSUserService.UpdateSelf` | 改自己 username / password；**不能改 role** |

> 註冊（建新帳號）走既有 `POST /auth/register`，見 `infrastructure.md` §3.5.2。
> 路由前綴：`/api/v1`（與 §3 OpenAPI servers 一致）。
> 所有端點皆需 `Authorization: Bearer <access_token>`，且 `claims.utype == "cms"` 才能進入；
> 非 CMS 一律 `403 forbidden`（不是 404，因為對 CMS user 來說資源存在）。

---

## 4. Endpoint 細節

### 4.1 `GET /cms/users` — List

**Query 參數**（皆 optional）：

| 名稱 | 型別 | 預設 | 說明 |
|---|---|---|---|
| `page` | int | 1 | 1-based |
| `page_size` | int | 20 | 上限 100 |
| `role` | string | — | `admin` / `user` / `viewer`，可重複出現做 OR 篩選 |
| `username_like` | string | — | ILIKE 模糊比對（前後加 `%`），最少 2 字元 |
| `include_deleted` | bool | `false` | `true` 才回傳 `deleted_at IS NOT NULL` 的紀錄 |
| `sort` | string | `-created_at` | 允許 `created_at` / `-created_at` / `username` / `-username`（`-` = desc）|

**回應**：`Response<[]CMSUserDTO>` + `meta` 分頁資訊。

**錯誤**：
- 400 `invalid input` — page < 1、page_size > 100、role 不認得、sort 不在白名單
- 401 `unauthorized` — 無 token
- 403 `forbidden` — `utype != cms`

### 4.2 `GET /cms/users/{id}` — Get

**Path 參數**：`id` UUID v4。

**回應**：`Response<CMSUserDTO>`。

**錯誤**：
- 404 `not_found` — id 不存在或已軟刪除（除非 query `?include_deleted=true`）
- 401 / 403 同 4.1

### 4.3 `PATCH /cms/users/{id}` — Update（admin only）

**Path**：`id` UUID v4。

**Request body**（部分更新；缺欄位代表不改）：

```json
{
  "username": "alice2",
  "role": "viewer"
}
```

| 欄位 | 型別 | 驗證 | 說明 |
|---|---|---|---|
| `username` | string | optional, 3–64 字元 | 改 username（unique 衝突 → 409）|
| `role` | string | optional, enum `admin`/`user`/`viewer` | 改 role |

**Side effects — role 變更時自動踢人（§3 表所述）**：

當且僅當 `role` 在請求中變動，service 在 username/role update 成功後**同 transaction
之後**執行：
1. `familyStore.RevokeAll(ctx, targetID)` — 廢掉 target 所有 refresh token family（`infrastructure.md` §7.4）
2. `userRevocationStore.Revoke(ctx, targetID, ttl)` — 寫 user-level revoke watermark（`infrastructure.md` §7.5）
   - TTL 建議：系統最長 abs_exp（例如 ios refresh 30 天）+ 安全餘量
   - AuthMiddleware 於下次驗 token 時自動比對 `claims.iat < watermark`，視為失效（`infrastructure.md` §8.5 步驟 3.5）

> **為何強制 revoke**：避免 demote 後被降級者在 access token 剩餘 TTL（最多 15 分鐘）
> 內仍以舊 role 操作；upgrade（user→admin）也一併 revoke 以維持「role 變更必須重登」的單一規則，
> 行為一致便於稽核與測試。

**回應**：`200` + `Response<CMSUserDTO>`。

**錯誤**：
- 400 `invalid input` — username 長度違規 / role 不認得
- 401 `unauthorized` — 無 token
- 403 `forbidden` — caller 非 admin
- 404 `not_found` — id 不存在
- 409 `username_taken` — username 衝突
- 422 `last_admin_lockout` — 試圖把最後一個 admin 降級（見 §6）
- 422 `cannot_change_own_role` — caller 試圖改自己的 role

**Audit**：
- `cms_user.updated`（一般欄位變更）
- `cms_user.role_changed`（**僅當 role 改變時額外寫一筆**，security 相關高優先級）
- `cms_user.sessions_force_revoked`（role 變更時 revoke 動作）

### 4.4 `DELETE /cms/users/{id}` — Soft delete（admin only）

**Path**：`id` UUID v4。

**行為（按順序）**：
1. `UPDATE cms_users SET deleted_at = now() WHERE id = ? AND deleted_at IS NULL`
2. `familyStore.RevokeAll(ctx, targetID)` — 廢掉 target 所有 refresh token family（`infrastructure.md` §7.4）
3. `userRevocationStore.Revoke(ctx, targetID, ttl)` — 寫 user-level revoke watermark（`infrastructure.md` §7.5）

> **為何同 transaction 邏輯但 redis 操作放 transaction 外**：
> DB 軟刪除與 redis revoke 是兩個 store，無分散式 transaction；採「DB 成功 → 才動 redis」
> 順序，最壞情況 redis revoke 失敗時帳號已軟刪但 session 沒被踢——可由 audit alert
> 觸發運維補刀。順序不可顛倒（若 redis 先成功 DB 失敗，會誤踢一個還活著的 user）。

**回應**：`204 No Content`。

**錯誤**：
- 401 `unauthorized` / 403 `forbidden`（同 4.3）
- 404 `not_found` — id 不存在或**已軟刪除**（避免 idempotent 失敗：第二次 DELETE 回 404）
- 422 `last_admin_lockout` — 試圖刪最後一個 admin
- 422 `cannot_delete_self` — caller 試圖刪自己

**Audit**：
- `cms_user.deleted`（actor, target）
- `cms_user.sessions_force_revoked`（revoke 動作）

### 4.5 `PATCH /cms/users/me` — Self-update

**行為**：caller 改自己 username / password；**禁止改 role**（避免越權）。

**Request body**（部分更新）：

```json
{
  "username": "alice2",
  "current_password": "old-pw",
  "new_password": "new-pw-min-8-with-letter-and-digit"
}
```

| 欄位 | 型別 | 驗證 | 說明 |
|---|---|---|---|
| `username` | string | optional, 3–64 字元 | 改自己 username |
| `current_password` | string | required if `new_password` set | 改密碼前必須驗舊密碼 |
| `new_password` | string | optional, 同 §3.5 weak password 規則 | 設新密碼 |

**回應**：`200` + `Response<CMSUserDTO>`。

**錯誤**：
- 400 `invalid input`
- 401 `unauthorized` — 無 token
- 401 `current_password_mismatch` — 改密碼但 current_password 錯
- 403 `forbidden` — `utype != cms`
- 409 `username_taken`
- 422 `weak_password`

**Audit**：`cms_user.self_updated`（actor = self；若改密碼，extra 標記 `password_changed=true`）

**注意**：
- 改密碼**不**自動 revoke 既有 refresh token family（沿用即可，方便其他裝置續用）。
  運維若懷疑帳密外洩，應另呼叫 `/auth/sessions/revoke-all`。

---

## 5. DTO / Schema

### 5.1 `CMSUserDTO`

```go
// internal/dto/cms_user_dto.go
type CMSUserDTO struct {
    ID        string    `json:"id"`
    Username  string    `json:"username"`
    Role      string    `json:"role"`                  // admin / user / viewer
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    DeletedAt *time.Time `json:"deleted_at,omitempty"` // 僅 include_deleted=true 時可能出現
}

func FromCMSUser(u *model.CMSUser) *CMSUserDTO { ... }
func FromCMSUserList(us []model.CMSUser) []CMSUserDTO { ... }
```

**不洩漏欄位**：`password_hash`、任何含 token 的欄位。

### 5.2 OpenAPI schema 範例

```yaml
# schema/openapi.yaml（節錄；完整契約見後續實作 PR）

paths:
  /cms/users:
    get:
      operationId: cmsListUsers
      tags: [cms-users]
      security: [bearerAuth: []]
      parameters:
        - { name: page, in: query, schema: { type: integer, minimum: 1, default: 1 } }
        - { name: page_size, in: query, schema: { type: integer, minimum: 1, maximum: 100, default: 20 } }
        - { name: role, in: query, schema: { type: string, enum: [admin, user, viewer] } }
        - { name: username_like, in: query, schema: { type: string, minLength: 2 } }
        - { name: include_deleted, in: query, schema: { type: boolean, default: false } }
        - { name: sort, in: query, schema: { type: string, enum: ["created_at","-created_at","username","-username"], default: "-created_at" } }
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                allOf:
                  - $ref: '#/components/schemas/SuccessEnvelope'
                  - properties:
                      data: { type: array, items: { $ref: '#/components/schemas/CMSUserDTO' } }
                      meta: { $ref: '#/components/schemas/PageMeta' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }

  # PATCH/DELETE/me 端點 schema 略；命名與結構規律可參考 List。
  # 註冊（POST）由既有 /auth/register 提供，本檔不重複定義。

components:
  schemas:
    CMSUserDTO:
      type: object
      required: [id, username, role, created_at, updated_at]
      properties:
        id:         { type: string, format: uuid }
        username:   { type: string }
        role:       { type: string, enum: [admin, user, viewer] }
        created_at: { type: string, format: date-time }
        updated_at: { type: string, format: date-time }
        deleted_at: { type: string, format: date-time, nullable: true }

    CMSUserResponse:
      allOf:
        - $ref: '#/components/schemas/SuccessEnvelope'
        - properties:
            data: { $ref: '#/components/schemas/CMSUserDTO' }
```

> `Unauthorized` / `Forbidden` / `BadRequest` / `Conflict` / `UnprocessableEntity`
> 為 `components.responses` 中重用的 `ErrorResponse` 包裝（已在 §3.5.1 定義）。

---

## 6. 不變量（Invariants）

集中在 service 層檢查，回 `apperr.ErrXxx` 由 handler 轉 HTTP code。

| ID | 規則 | 觸發點 | 錯誤碼 |
|---|---|---|---|
| INV-1 | 至少存在一個未刪除的 `role=admin` | PATCH demote admin、DELETE admin | 422 `last_admin_lockout` |
| INV-2 | Caller 不能刪自己 | DELETE `/:id` 當 `id == caller.UserID()` | 422 `cannot_delete_self` |
| INV-3 | Caller 不能改自己的 role | PATCH `/:id` 當 `id == caller.UserID()` 且 body 含 `role` | 422 `cannot_change_own_role` |
| INV-4 | PATCH /me 不能含 `role` 欄位 | OpenAPI schema 排除；service 層也擋 | 400 `invalid input` |
| INV-5 | username partial unique（軟刪除排除）| INSERT / UPDATE 時 PostgreSQL 約束 | 409 `username_taken` |

> **INV-1 的實作**：先查當前 admin 總數，若 `count == 1` 且操作對象就是這個 admin 則拒絕。
> 並發競態（兩個 admin 同時降級彼此）由 transaction + `SELECT ... FOR UPDATE` 或 advisory lock 處理；
> 設計選 transaction（簡單、可靠）。

> **註**：新帳號 role 規則由 `/auth/register` 自行控管（既有 service 已寫死 `user`），
> 不在本規格不變量內。

---

## 7. Audit 事件

新增 5 個 event type（補入 `pkg/audit/audit.go`）：

```go
EventCMSUserUpdated              EventType = "cms_user.updated"
EventCMSUserRoleChanged          EventType = "cms_user.role_changed"          // ⚠️ 高優先級告警
EventCMSUserDeleted              EventType = "cms_user.deleted"
EventCMSUserSelfUpdated          EventType = "cms_user.self_updated"
EventCMSUserSessionsForceRevoked EventType = "cms_user.sessions_force_revoked" // role 變更 / 刪除附帶
```

> `cms_user.created` 屬 `/auth/register` 端職責（已存在 `EventRegisterSuccess`），本規格不重複定義。

**統一欄位**（基於現有 `AuthEvent` 結構擴充或新增 `AdminEvent`）：

| 欄位 | 說明 |
|---|---|
| `type` | event type |
| `actor_user_id` | caller userID（從 access token claims）|
| `target_user_id` | 操作對象（self_updated 與 created 時 = actor）|
| `request_id` | 從 ctx 取 |
| `ip` | caller IP |
| `extra` | role 變更時：`{"from": "user", "to": "admin"}`；改密碼時：`{"password_changed": true}` |

**告警建議**（規格 §18.3 既有 audit alert 機制延伸）：
- `cms_user.role_changed` to=`admin` → 立刻發告警（升級為 admin 是高權限變更）
- `cms_user.deleted` → 5 分鐘內 > 3 次 → 發告警（疑似批次清帳號）

---

## 8. 錯誤對應

新增至 `internal/apperr/errors.go`：

```go
var (
    ErrLastAdminLockout    = errors.New("last admin lockout")
    ErrCannotDeleteSelf    = errors.New("cannot delete self")
    ErrCannotChangeOwnRole = errors.New("cannot change own role")
    ErrCurrentPasswordMismatch = errors.New("current password mismatch")
)
```

`internal/handler/error_handler.go` 新增 case：

| sentinel | HTTP | error code |
|---|---|---|
| `ErrLastAdminLockout` | 422 | `last_admin_lockout` |
| `ErrCannotDeleteSelf` | 422 | `cannot_delete_self` |
| `ErrCannotChangeOwnRole` | 422 | `cannot_change_own_role` |
| `ErrCurrentPasswordMismatch` | 401 | `current_password_mismatch` |

---

## 9. Service 介面

```go
// internal/service/cms_user_service.go

type CMSUserService interface {
    List(ctx context.Context, opts ListCMSUsersOptions) ([]model.CMSUser, int64, error)
    Get(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error)
    Update(ctx context.Context, callerID, targetID string, in UpdateCMSUserInput) (*model.CMSUser, error)
    SoftDelete(ctx context.Context, callerID, targetID string) error
    UpdateSelf(ctx context.Context, callerID string, in UpdateSelfInput) (*model.CMSUser, error)
}

type ListCMSUsersOptions struct {
    Page, PageSize  int
    RoleFilter      []string  // empty = no filter
    UsernameLike    string    // empty = no filter
    IncludeDeleted  bool
    Sort            string    // whitelisted: created_at / -created_at / username / -username
}

type UpdateCMSUserInput struct {
    Username *string  // nil = 不改
    Role     *string  // nil = 不改；非 nil 時 service 檢 INV-1 / INV-3，並觸發 §4.3 強制 revoke
}

type UpdateSelfInput struct {
    Username        *string
    CurrentPassword *string  // 若 NewPassword 非 nil 則必填
    NewPassword     *string
    // 刻意不暴露 Role（INV-4）
}
```

> `CMSUserService` 構造時需注入 `FamilyStore` 與「user-level revoke」工具（§4.3 提到的
> `auth:user_revoked_after:{userID}` 機制），以支援 Update / SoftDelete 的強制踢人。

---

## 10. Repository 變更

`internal/repository/cms_user_repository.go` 擴充：

```go
type CMSUserRepository interface {
    // 既有（由 /auth/register 與 /auth/login 使用）
    FindByUsername(ctx context.Context, username string) (*model.CMSUser, error)
    Create(ctx context.Context, u *model.CMSUser) error

    // 新增（本規格用）
    FindByID(ctx context.Context, id string, includeDeleted bool) (*model.CMSUser, error)
    List(ctx context.Context, opts ListCMSUsersOptions) ([]model.CMSUser, int64, error)
    Update(ctx context.Context, id string, patch CMSUserPatch) error
    SoftDelete(ctx context.Context, id string) error
    CountActiveAdmins(ctx context.Context) (int64, error)  // 用於 INV-1
}

type CMSUserPatch struct {
    Username     *string
    Role         *string
    PasswordHash *string  // self-update 改密碼用
}
```

> **CountActiveAdmins** 在 INV-1 檢查時用，必須與 Update/SoftDelete 在同一 transaction
> （避免「先查 count = 2 → demote 一個 → 另一個 admin 同時被別 caller 改」的競態）。
> 實作：用 `repo.WithTx(func(tx) error { ... })` 一次性執行查詢與寫入。

> 既有 `Create` 由 `auth_service.Register` 使用，本規格不新增 create 流程。

---

## 11. Migration 變更

**不需新增 migration**——`cms_users` 表既有結構（§13.5）已涵蓋所有欄位：
- `id`, `username`, `password_hash`, `role`, `created_at`, `updated_at`, `deleted_at`

可選改善（建議但非必要）：

```sql
-- 加 index 加速 List + role filter
CREATE INDEX IF NOT EXISTS idx_cms_users_role_deleted
ON cms_users(role, deleted_at)
WHERE deleted_at IS NULL;

-- 加 index 加速 username_like 模糊搜尋（pg_trgm 擴充）
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_cms_users_username_trgm
ON cms_users USING gin(username gin_trgm_ops)
WHERE deleted_at IS NULL;
```

---

## 12. 測試策略（對齊 §19 TDD）

| 層級 | 工具 | 覆蓋範圍 |
|---|---|---|
| Unit | testify + fake repo | service 內所有 INV 規則、role 強制、權限分支 |
| Integration | testify + testcontainers / docker-compose | repository List 篩選、軟刪除、unique 衝突 |
| E2E | httptest | 7 個 endpoint × 權限矩陣 × 邊界條件，含 OpenAPI schema 驗證 |

**關鍵 regression test 必加**：

1. `TestCMSUserService_Update_LastAdminLockout` — 預埋僅 1 個 admin，PATCH 改 role 必須回 `ErrLastAdminLockout`
2. `TestCMSUserService_Update_RoleChange_TriggersRevoke` — role 變動時必呼叫 `FamilyStore.RevokeAll` 並寫 `auth:user_revoked_after:{userID}` key
3. `TestCMSUserService_Update_UsernameOnly_NoRevoke` — 只改 username（role 未動）不該觸發 revoke
4. `TestCMSUserService_SoftDelete_CannotDeleteSelf` — caller 刪自己回 `ErrCannotDeleteSelf`
5. `TestCMSUserService_SoftDelete_TriggersRevoke` — 軟刪後必觸發 revoke 流程
6. `TestCMSUserService_UpdateSelf_NewPasswordRequiresCurrent` — 提供 new_password 但缺 current_password → 400
7. `TestCMSUserService_UpdateSelf_UsernameNoCurrentPassword` — 只改 username 不需 current_password（§4.5 規則）
8. `TestCMSUserHandler_Forbidden_NonAdmin_Update` — user role 呼叫 PATCH 回 403
9. `TestCMSUserHandler_NoPasswordHashInResponse` — JSON response 不含 `password_hash` 欄位
10. `TestCMSUserService_Update_RaceTwoAdmins` — 並發 demote 兩個僅有的 admin，只能成功一個（transaction 保護）

---

## 13. Open Questions / 未來擴充

| 議題 | 現況 | 未來方向 |
|---|---|---|
| Admin 重設**他人**密碼 | 暫不提供，user 自己改；忘記密碼走「軟刪後重新註冊」walk-around | 加 `POST /cms/users/:id/reset-password`，產生臨時密碼或寄 reset link |
| 軟刪除回復 | 暫不提供 | 加 `POST /cms/users/:id/restore`（admin only），復原 `deleted_at = NULL` |
| 帳號鎖定 / 失敗次數限制 | 暫不在 cms_users，靠 §15 rate limit | 加 `locked_until` 欄位 + 累計失敗自動鎖定 |
| 多 admin 區分（super vs normal）| 暫無區分 | 引入 `is_super` 旗標或多層 role |
| 變更歷史 | audit log 即足夠 | 若需查詢介面，加 `cms_users_audit` 表或從 audit sink 索引 |
| `GET /cms/users/me` 取自己 profile | 暫不提供，可從 access token claims 取基本資訊 | 補一個便利端點供前端取完整 user 物件 |
| 離職自助刪除（DELETE /me） | 暫不提供，由其他 admin 操作 | 視運維流程決定是否開放 |

---

## 14. 待辦清單（給實作 PR）

- [ ] `internal/apperr/errors.go` 加 4 個新 sentinel
- [ ] `internal/handler/error_handler.go` 加 4 個 case 映射
- [ ] `pkg/audit/audit.go` 加 5 個 EventType 常數
- [ ] `pkg/redis/user_revocation.go`：實作 `UserRevocationStore` 介面（規格見 `infrastructure.md` §7.5）
- [ ] `pkg/jwt/middleware.go`：AuthMiddleware 加 step 3.5 user-revoke 檢查（規格見 `infrastructure.md` §8.5）；signature 加第 3 個參數 `userRevoke UserRevocationStore`
- [ ] `pkg/metrics/metrics.go`：新增 `AuthUserRevokeErrors` counter（規格見 `infrastructure.md` §18.2）
- [ ] `internal/repository/cms_user_repository.go` 擴充 5 個 method + transaction helper
- [ ] `internal/service/cms_user_service.go` 新檔 + 5 個 method（List/Get/Update/SoftDelete/UpdateSelf）+ INV 檢查 + revoke 整合
- [ ] `internal/handler/cms_user_handler.go` 新檔 + 5 個 handler
- [ ] `internal/dto/cms_user_dto.go` 新檔
- [ ] `cmd/server/main.go` wire CMSUserService、註冊路由群組 `/api/v1/cms/users`（注意：`/me` 必須先於 `/:id` 註冊以避免 path 衝突）
- [ ] `schema/openapi.yaml` 加 5 個 endpoint + CMSUserDTO + 重用 ErrorResponse
- [ ] Unit tests（service 層所有 INV 與強制 revoke 觸發）
- [ ] Integration tests（repository List 篩選與軟刪除）
- [ ] E2E tests（handler 5 個 endpoint × 權限矩陣 × revoke side effect）
- [ ] 可選 migration：role + username GIN index（§11）
- [ ] 更新 `infrastructure.md` §3.4 endpoint 清單，加入這 5 個 + 補入 §8 user-level revoke 機制設計

---

## 15. 變更紀錄

| 版本 | 日期 | 變更 |
|---|---|---|
| v1.2 | 2026-06-28 | 把 user-level revoke 機制正式落地到 `infrastructure.md` v1.11（§7.5 `UserRevocationStore` + §8.5 step 3.5 + §12.4 + §18.2 metric）；本檔 §4.3/§4.4/§14 改為直接引用 §7.5 而非散描述 |
| v1.1 | 2026-06-28 | 依使用者回饋調整：移除 `POST /cms/users`（改由 `/auth/register` 公開註冊提供）；§4.3/§4.4 加入 role 變更與軟刪除的「自動 revoke target sessions」流程；新增 `cms_user.sessions_force_revoked` audit event；引入 user-level revoke 機制概念 |
| v1.0 | 2026-06-28 | 初版草案 |
