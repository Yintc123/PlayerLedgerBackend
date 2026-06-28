# CMS Users API 規格書

版本：v1.3（未實作）
範圍：CMS 內部人員（`cms_users` 表）的管理 API
對應規格：`infrastructure.md` §3（OpenAPI）、§7.5（UserRevocationStore）、§8（Auth）、§10（Response）、§12（Errors）、§17（DTO）、§18.2（Metrics）
對應 ADR：暫無

---

## 1. 設計原則

- **規格驅動（SDD）**：所有 endpoint 必須先在 `schema/openapi.yaml` 定義（含完整 request / response / error schema，見 §5.2），再寫 handler 與測試（與 `infrastructure.md` §3 對齊）。
- **回應 envelope 一致性**：成功走 `Response<T>` / `204`，錯誤走共用 `ErrorResponse`（`infrastructure.md` §10.2 / §12.4）。
- **DTO 隔離**：絕不回傳 `password_hash`、任何 token 欄位；`deleted_at` 僅在 `include_deleted=true` 結果中出現。
- **不變量強制**：lock-out 防護（最後一個 admin 不可降級或刪除）在 service 層集中檢查，**不靠 caller、不靠前端**。
- **稽核敏感操作**：role 變更、刪除、自助改密碼皆寫 audit log（`infrastructure.md` §18.3）。
- **稽核失敗不阻塞主操作**：audit logger 寫入失敗（disk / sink IO）僅 log warn + metric，**不 rollback** 主操作。
  理由：主操作已完成（DB 已寫入），rollback 反而造成「DB 與 audit 不一致」更嚴重；故走「best-effort audit + alert」模式。
- **HTTP idempotency**：DELETE 對已軟刪除帳號回 `404 not_found`（**非** 204）——以「資源不在當前可見狀態」為語意，
  與 GET / PATCH 對已刪除帳號的回應一致；前端據此可寫「retry → 看到 404 = 對方已刪 = 成功」邏輯。
  PATCH 對未變欄位（idempotent body）仍回 `200 + 當前狀態`，不額外處理。

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
| `role` | string | — | `admin` / `user` / `viewer`；**可重複出現做 OR 篩選**（handler 用 `c.QueryArray("role")`）|
| `username_like` | string | — | 子字串模糊比對，最少 2 字元 |
| `include_deleted` | bool | `false` | `true` 才回傳 `deleted_at IS NOT NULL` 的紀錄；**僅 admin 可用**，non-admin 傳了**靜默忽略**（避免洩漏「軟刪 vs 不存在」差異） |
| `sort` | string | `-created_at` | 白名單見下；`-` = desc |

**參數解析規則（service 層責任）**：

| 參數 | 解析 / 驗證 | 違反處理 |
|---|---|---|
| `page_size` | 上限 100，<1 → 預設 20 | 上限以上 → fail-fast 400 |
| `role[]` | 每個值必須在 `[admin, user, viewer]` 白名單；空陣列 = 不篩 | 任一不在白名單 → 400 `invalid input` |
| `sort` | 必須在 `[created_at, -created_at, username, -username]` 白名單 | 不在白名單 → 400 `invalid input` |
| `username_like` | service 層**必須 escape SQL LIKE 的 `\`、`%`、`_`**（避免 caller 用 `%` 全表掃描或 `_` 單字元匹配繞過 minLength），再前後加 `%` 餵給 PostgreSQL ILIKE。`\` 用 `ESCAPE '\'` 子句配套。 | minLength < 2 → 400 |
| `include_deleted` | 若 caller `role != admin`，無論傳值一律當 `false`，不報錯 | 不報錯 |

**回應**：`Response<[]CMSUserDTO>` + `meta` 分頁資訊。

**錯誤**：
- 400 `invalid input` — page < 1、page_size > 100、role 不在白名單、sort 不在白名單、username_like < 2 字元
- 401 `unauthorized` — 無 token
- 403 `forbidden` — `utype != cms`

### 4.2 `GET /cms/users/{id}` — Get

**Path 參數**：`id` UUID v4。

**Query 參數**（optional）：

| 名稱 | 型別 | 預設 | 說明 |
|---|---|---|---|
| `include_deleted` | bool | `false` | `true` 時 admin 可看到已軟刪除帳號；non-admin 傳了**靜默忽略**（同 §4.1）|

**回應**：`Response<CMSUserDTO>`。

**錯誤**：
- 400 `invalid input` — `id` 非 UUID 格式
- 401 `unauthorized` / 403 `forbidden` 同 §4.1
- 404 `not_found` — id 不存在；或已軟刪除且未指定 `?include_deleted=true`；或 non-admin 對軟刪除帳號查詢（不洩漏其存在）

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

**執行流程（service 層保證的順序）**：

```
┌─ DB transaction ─────────────────────────────────────┐
│ 1. SELECT … FOR UPDATE 鎖住 target user 列          │
│ 2. INV-1 / INV-3 / INV-5 檢查                       │
│ 3. UPDATE cms_users SET username/role WHERE id=?    │
│ 4. role 變動時：再 SELECT count(*) FROM admins 確認 │
│    fail-safe（避免兩個 caller 並發降級彼此）          │
└──────────────────────────────────────────────────────┘
        │ commit
        ▼
┌─ post-commit（best-effort，失敗 log + alert）────────┐
│ 5. role 變動時：familyStore.RevokeAll(targetID)     │
│ 6. role 變動時：userRevocationStore.Revoke(...)     │
│ 7. audit.Log(updated / role_changed / force_revoked)│
└──────────────────────────────────────────────────────┘
```

**Side effects 細節 — 僅當 `role` 在請求中變動**：

1. `familyStore.RevokeAll(ctx, targetID)` — 廢掉 target 所有 refresh token family（`infrastructure.md` §7.4）
2. `userRevocationStore.Revoke(ctx, targetID, ttl)` — 寫 user-level revoke watermark（`infrastructure.md` §7.5）
   - **TTL 來源**：`max(p.AbsoluteTTL for p in cfg.JWT.ClientPolicies) + leeway`，service 啟動時計算後快取。
     例如當前最長 absolute TTL 是 ios-app 的 180 天，watermark TTL = 180d + 1d 安全餘量 = 181d。
     超過 TTL 後 redis 自動清理，此時 target 對應的所有可能 access token 早已 abs_exp 失效。
   - AuthMiddleware 於下次驗 token 時自動比對 `claims.iat < watermark`，視為失效（`infrastructure.md` §8.5 步驟 3.5）

**為何 redis 操作放 commit 後**（不放 transaction 內）：
DB 與 redis 是兩個 store，無分散式 transaction。採「DB commit 成功 → 才動 redis」順序，
最壞情況 redis revoke 失敗時帳號 role 已改但 session 沒被踢——15 分鐘內舊 access token 仍以舊 role 操作。
此情境由 `cms_user.sessions_force_revoked` 缺漏 + `auth_user_revoke_errors_total` 觸發告警，
運維可手動呼叫 `/auth/sessions/revoke-all` 補刀。若顛倒順序（先 redis 後 DB），DB 失敗會誤踢還活著的人。

**為何強制 revoke**：避免 demote 後被降級者在 access token 剩餘 TTL（最多 15 分鐘）
內仍以舊 role 操作；upgrade（user→admin）也一併 revoke 以維持「role 變更必須重登」的單一規則，
行為一致便於稽核與測試。

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

**執行流程**：

```
┌─ DB transaction ─────────────────────────────────────┐
│ 1. SELECT … FOR UPDATE 鎖住 target user 列          │
│ 2. INV-1 / INV-2 檢查（最後 admin / 刪自己）         │
│ 3. UPDATE cms_users SET deleted_at = now()          │
│    WHERE id = ? AND deleted_at IS NULL              │
│ 4. 受影響列 = 0 → 拋 ErrNotFound（已被別人刪）       │
└──────────────────────────────────────────────────────┘
        │ commit
        ▼
┌─ post-commit（best-effort，失敗 log + alert）────────┐
│ 5. familyStore.RevokeAll(targetID)                  │
│ 6. userRevocationStore.Revoke(targetID, ttl)        │
│    （TTL 來源同 §4.3）                                │
│ 7. audit.Log(deleted, sessions_force_revoked)       │
└──────────────────────────────────────────────────────┘
```

**回應**：`204 No Content`。

**錯誤**：
- 400 `invalid input` — `id` 非 UUID 格式
- 401 `unauthorized` / 403 `forbidden`（同 §4.3）
- 404 `not_found` — id 不存在 **或已軟刪除**
- 422 `last_admin_lockout` — 試圖刪最後一個 admin
- 422 `cannot_delete_self` — caller 試圖刪自己

**HTTP idempotency 行為**：
- 第 1 次 DELETE 成功 → 204
- 第 2 次 DELETE（同一 id）→ 404 `not_found`
  - 不選 204 的理由：與 GET / PATCH 對已刪除帳號的行為一致（皆 404），前端無需區分「我剛刪的」vs「別人刪的」；
    並符合 §1 設計原則「DELETE 對已軟刪帳號回 404」。
  - HTTP 規範允許此行為：RFC 7231 §4.2.2 對 idempotency 的定義是「重複請求的效果等價」，
    並未強制重複回應的 status code 必須相同。

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

完整契約如下（5 個 endpoint），直接複製進 `schema/openapi.yaml`。
所有錯誤回應走 `infrastructure.md` §3.5.1 已定義的 `Unauthorized` / `Forbidden` / `BadRequest` /
`Conflict` / `UnprocessableEntity` / `NotFound` 共用 `components.responses`。

```yaml
# schema/openapi.yaml（節錄）

paths:
  /cms/users:
    get:
      operationId: cmsListUsers
      tags: [cms-users]
      security: [{ bearerAuth: [] }]
      parameters:
        - { name: page,            in: query, schema: { type: integer, minimum: 1,  default: 1 } }
        - { name: page_size,       in: query, schema: { type: integer, minimum: 1, maximum: 100, default: 20 } }
        - name: role
          in: query
          description: 可重複；OR 篩選
          schema: { type: array, items: { type: string, enum: [admin, user, viewer] } }
          style: form
          explode: true
        - { name: username_like,   in: query, schema: { type: string, minLength: 2 } }
        - { name: include_deleted, in: query, description: "僅 admin 有效；non-admin 傳了靜默忽略", schema: { type: boolean, default: false } }
        - { name: sort,            in: query, schema: { type: string, enum: ["created_at","-created_at","username","-username"], default: "-created_at" } }
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                allOf:
                  - $ref: '#/components/schemas/SuccessEnvelope'
                  - type: object
                    properties:
                      data: { type: array, items: { $ref: '#/components/schemas/CMSUserDTO' } }
                      meta: { $ref: '#/components/schemas/PageMeta' }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }

  /cms/users/{id}:
    parameters:
      - { name: id, in: path, required: true, schema: { type: string, format: uuid } }

    get:
      operationId: cmsGetUser
      tags: [cms-users]
      security: [{ bearerAuth: [] }]
      parameters:
        - { name: include_deleted, in: query, schema: { type: boolean, default: false } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/CMSUserResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }

    patch:
      operationId: cmsUpdateUser
      tags: [cms-users]
      security: [{ bearerAuth: [] }]
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: false   # 嚴格擋未知欄位
              minProperties: 1              # 至少改一個欄位
              properties:
                username: { type: string, minLength: 3, maxLength: 64 }
                role:     { type: string, enum: [admin, user, viewer] }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/CMSUserResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/Conflict' }              # username_taken
        '422': { $ref: '#/components/responses/UnprocessableEntity' }   # last_admin_lockout / cannot_change_own_role

    delete:
      operationId: cmsDeleteUser
      tags: [cms-users]
      security: [{ bearerAuth: [] }]
      responses:
        '204': { description: Deleted }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '422': { $ref: '#/components/responses/UnprocessableEntity' }   # last_admin_lockout / cannot_delete_self

  /cms/users/me:
    patch:
      operationId: cmsUpdateSelf
      tags: [cms-users]
      security: [{ bearerAuth: [] }]
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: false   # 嚴格擋未知欄位（特別擋 role：INV-4）
              minProperties: 1
              properties:
                username:         { type: string, minLength: 3, maxLength: 64 }
                current_password: { type: string, minLength: 1, maxLength: 256, format: password }
                new_password:     { type: string, minLength: 8, maxLength: 256, format: password }
              # current_password 與 new_password 配對在 service 層檢查
              # （OpenAPI 無法表達「A required iff B present」）
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/CMSUserResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401':
          description: 未登入 / 改密碼但 current_password 錯
          content: { application/json: { schema: { $ref: '#/components/schemas/ErrorResponse' } } }
        '403': { $ref: '#/components/responses/Forbidden' }
        '409': { $ref: '#/components/responses/Conflict' }
        '422': { $ref: '#/components/responses/UnprocessableEntity' }   # weak_password

components:
  schemas:
    CMSUserDTO:
      type: object
      required: [id, username, role, created_at, updated_at]
      properties:
        id:         { type: string, format: uuid }
        username:   { type: string }
        role:       { type: string, enum: [admin, user, viewer] }
        created_at: { type: string, format: date-time, description: "RFC3339 UTC" }
        updated_at: { type: string, format: date-time, description: "RFC3339 UTC" }
        deleted_at: { type: string, format: date-time, nullable: true, description: "僅 include_deleted=true 時可能出現" }

    CMSUserResponse:
      allOf:
        - $ref: '#/components/schemas/SuccessEnvelope'
        - type: object
          properties:
            data: { $ref: '#/components/schemas/CMSUserDTO' }
```

### 5.3 前端整合範例（curl + JSON）

```bash
# 列出所有 admin（admin 視角，看軟刪除）
curl -H "Authorization: Bearer $TOKEN" \
  "https://api.example.com/api/v1/cms/users?role=admin&include_deleted=true&sort=-created_at"

# 降級 alice 為 viewer（admin only；觸發 alice 全裝置強制重登）
curl -X PATCH -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"role":"viewer"}' \
  "https://api.example.com/api/v1/cms/users/0193b3f4-1234-7abc-9def-0123456789ab"

# 自助改密碼
curl -X PATCH -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"current_password":"old","new_password":"new-pw-strong-123"}' \
  "https://api.example.com/api/v1/cms/users/me"
```

```json
// 422 last_admin_lockout
{
  "success": false,
  "request_id": "0193b3f4-1234-7abc-9def-0123456789ab",
  "error": "last_admin_lockout"
}
```

---

## 6. 不變量（Invariants）

集中在 service 層檢查，回 `apperr.ErrXxx` 由 handler 轉 HTTP code。

| ID | 規則 | 觸發點 | 錯誤碼 |
|---|---|---|---|
| INV-1 | 至少存在一個未刪除的 `role=admin` | PATCH demote admin、DELETE admin | 422 `last_admin_lockout` |
| INV-2 | Caller 不能刪自己 | DELETE `/:id` 當 `id == caller.UserID()` | 422 `cannot_delete_self` |
| INV-3 | Caller 不能改自己的 role | PATCH `/:id` 當 `id == caller.UserID()` 且 body 含 `role` | 422 `cannot_change_own_role` |
| INV-4 | PATCH /me 不能含 `role` 欄位 | OpenAPI `additionalProperties: false` 第一層擋；service 層也擋（深度防禦）| 400 `invalid input` |
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
- `cms_user.sessions_force_revoked` 缺漏（DB 已寫但 audit 沒對應） → 與 `auth_user_revoke_errors_total` 一起告警，提示運維補刀

**Audit 失敗策略**（§1 設計原則延伸）：

| 階段 | 失敗時行為 |
|---|---|
| DB transaction 內 | rollback 整個操作；caller 看到 500 `internal server error` |
| post-commit redis revoke | log warn + `auth_user_revoke_errors_total` +1，**不影響** API 回應；運維據告警補刀 |
| audit.Log 寫入 | log warn + `audit_write_errors_total` +1（需在 §18.2 metric 補入），**不影響** API 回應，**不 rollback DB**（理由：DB 已 commit，rollback 反而造成「DB 與 audit 不一致」更嚴重） |

理由：安全性 vs 可用性 tradeoff——audit 是「best-effort 完整性」而非「事務性正確性」；
若改密碼 commit 後 audit 失敗就 rollback，會讓 user 因 audit 暫掛而無法改密碼，可用性傷害大於 audit 缺漏。


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

**Constructor 依賴**：

```go
func NewCMSUserService(
    repo               repository.CMSUserRepository,
    hasher             hasher.Hasher,                 // self-update 改密碼用
    familyStore        redis.FamilyStore,             // role 變更 / 軟刪除 → RevokeAll
    userRevocation     redis.UserRevocationStore,     // 同上 → 寫 user-level watermark
    userRevocationTTL  time.Duration,                 // §4.3 公式算出：max(ClientPolicies.AbsoluteTTL) + leeway
    audit              audit.Logger,
) CMSUserService
```

`userRevocationTTL` 由 `cmd/server/main.go` 啟動時計算後注入：

```go
var maxAbs time.Duration
for _, p := range cfg.JWT.ClientPolicies {
    if p.AbsoluteTTL > maxAbs { maxAbs = p.AbsoluteTTL }
}
userRevocationTTL := maxAbs + 24*time.Hour  // 1 天安全餘量
```

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

> 既有 `Create` 由 `auth_service.Register` 使用，本規格不新增 create 流程。

### 10.1 Transaction 邊界與 WithTx pattern

INV-1 / INV-3 檢查與 `UPDATE` / `soft delete` 必須在**同一 DB transaction** 內，
否則「先查 count = 2 → demote 一個 → 另一個 admin 同時被別 caller 改」的並發競態會繞過 lock-out 防護。

當前 codebase 的 `internal/repository/cms_user_repository.go` 還沒 transaction 抽象，本規格落地時需引入。
推薦 pattern：

```go
// internal/repository/transactor.go（新檔，所有 repo 共用）

// Transactor 抽象 DB transaction 控制；GORM 實作直接包 db.Transaction(fn)。
// Service 層用法：
//   err := tx.WithTx(ctx, func(ctx context.Context) error {
//       count, _ := r.CountActiveAdmins(ctx)
//       if count <= 1 && targetIsAdmin { return apperr.ErrLastAdminLockout }
//       return r.Update(ctx, id, patch)
//   })
//
// 實作關鍵：fn 內呼叫的 repo method 必須**讀同一 ctx**，
// 才能透過 `ctx.Value(txKey{})` 拿到 transaction `*gorm.DB`；否則仍走主連線。
type Transactor interface {
    WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// 每個 repo method 內：
func (r *cmsUserRepository) Update(ctx context.Context, id string, patch CMSUserPatch) error {
    db := r.dbFromCtx(ctx)  // 若 ctx 有 tx → 用 tx，否則 r.db
    return db.WithContext(ctx).Model(&model.CMSUser{}).Where("id = ?", id).Updates(...).Error
}
```

### 10.2 Service 內使用 transaction

```go
func (s *cmsUserService) Update(ctx context.Context, callerID, targetID string, in UpdateCMSUserInput) (*model.CMSUser, error) {
    var updated *model.CMSUser

    err := s.tx.WithTx(ctx, func(ctx context.Context) error {
        target, err := s.repo.FindByID(ctx, targetID, false)  // SELECT … FOR UPDATE
        if err != nil { return err }

        if in.Role != nil {
            // INV-1
            if target.Role == "admin" && *in.Role != "admin" {
                count, _ := s.repo.CountActiveAdmins(ctx)
                if count <= 1 { return apperr.ErrLastAdminLockout }
            }
            // INV-3
            if callerID == targetID { return apperr.ErrCannotChangeOwnRole }
        }
        if err := s.repo.Update(ctx, targetID, toPatch(in)); err != nil { return err }
        updated, err = s.repo.FindByID(ctx, targetID, false)
        return err
    })
    if err != nil { return nil, err }

    // post-commit side effects（best-effort，§4.3 流程）
    if in.Role != nil {
        if err := s.familyStore.RevokeAll(ctx, targetID); err != nil { /* log+metric, continue */ }
        if err := s.userRevocation.Revoke(ctx, targetID, s.revocationTTL); err != nil { /* log+metric, continue */ }
    }
    s.audit.Log(ctx, ...)  // §7 audit fail 不阻塞

    return updated, nil
}
```

> `FindByID` 對 transaction 內呼叫應加 `SELECT … FOR UPDATE`（GORM `Clauses(clause.Locking{Strength: "UPDATE"})`），
> 避免 concurrent reader 同時讀到舊 row 後各自 demote。

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

**Infra（依賴前置，必先做）**：
- [ ] `pkg/redis/user_revocation.go`：實作 `UserRevocationStore`（規格見 `infrastructure.md` §7.5）
- [ ] `pkg/jwt/middleware.go`：AuthMiddleware 加 step 3.5 user-revoke 檢查（規格見 `infrastructure.md` §8.5）；signature 加第 3 個參數 `userRevoke UserRevocationStore`
- [ ] `pkg/metrics/metrics.go`：新增 `AuthUserRevokeErrors` + `AuditWriteErrors` counter（規格見 `infrastructure.md` §18.2 與本檔 §7）
- [ ] `internal/repository/transactor.go`（新檔）：`Transactor` 介面 + GORM 實作 + `ctx` 注入 tx 的 pattern（本檔 §10.1）
- [ ] 既有 repositories 改為 `dbFromCtx(ctx)` 取連線（兼容無 tx 場景，行為不變）

**Domain（本規格）**：
- [ ] `internal/apperr/errors.go` 加 4 個新 sentinel（§8）
- [ ] `internal/handler/error_handler.go` 加 4 個 case 映射（§8）
- [ ] `pkg/audit/audit.go` 加 5 個 EventType 常數（§7）
- [ ] `internal/repository/cms_user_repository.go` 擴充 5 個 method：FindByID / List / Update / SoftDelete / CountActiveAdmins（§10）
- [ ] `internal/dto/cms_user_dto.go` 新檔（§5.1）
- [ ] `internal/service/cms_user_service.go` 新檔：constructor 含 6 個 dependency（§9）；5 個 method 含 INV / transaction / revoke / audit（§4、§6、§10.2）
- [ ] `internal/handler/cms_user_handler.go` 新檔：5 個 handler + role/sort 白名單驗證 + `username_like` SQL escape（§4.1）
- [ ] `schema/openapi.yaml` 貼入 §5.2 完整 schema（5 endpoint + DTO）
- [ ] `cmd/server/main.go`：
  - 計算 `userRevocationTTL = max(ClientPolicies.AbsoluteTTL) + 24h` 並注入（§9）
  - wire CMSUserService 與 handler
  - **路由註冊順序**：`/me` 必須先於 `/:id`（Gin tree 採 longest static prefix 優先，順序錯會 panic）

  ```go
  g := r.Group("/api/v1/cms/users",
      jwt.AuthMiddleware(jwtMgr, blacklist, userRevoke),
      jwt.RequireUserType(jwt.UserTypeCMS),
  )
  g.PATCH("/me",   h.UpdateSelf)                                          // ← 先註冊
  g.GET("",        h.List)
  g.GET("/:id",    h.Get)
  g.PATCH("/:id",  jwt.RequireRole(jwt.RoleAdmin), h.Update)
  g.DELETE("/:id", jwt.RequireRole(jwt.RoleAdmin), h.Delete)
  ```

**測試**：
- [ ] Unit tests：service 層 INV 規則、role 強制、revoke 觸發、audit fail 不阻塞主操作（§12 共 10 條）
- [ ] Integration tests：repository List 篩選（含 username_like escape）、軟刪除、unique 衝突、transaction race（兩 admin 並發 demote）
- [ ] E2E tests：5 endpoint × 權限矩陣 × OpenAPI schema 驗證（用 kin-openapi）

**Migration（可選，建議加）**：
- [ ] role + deleted_at 複合 index（§11）
- [ ] username pg_trgm GIN index（§11）

**規格同步**：
- [ ] 更新 `infrastructure.md` §3.4 endpoint 清單，加入這 5 個

---

## 15. 變更紀錄

| 版本 | 日期 | 變更 |
|---|---|---|
| v1.3 | 2026-06-28 | 最佳實踐補強，目標：規格能 1:1 落地、實作不再回頭問。重點：§5.2 補完整 5 endpoint OpenAPI + 新增 §5.3 curl/JSON 範例；§4.1 加 List 解析規則（role 多值、username_like SQL escape、include_deleted 權限、sort 白名單）；§4.2 補 query 參數表；§4.3/§4.4 加 transaction 流程圖 + post-commit best-effort 順序 + UserRevocationStore TTL 計算公式；§4.4 補 DELETE idempotency 行為與 RFC7231 引用；§6 INV-4 補「OpenAPI additionalProperties:false + service 深度防禦」；§7 加 audit fail policy（含 `AuditWriteErrors` metric）；§9 補 6 個 constructor dependency + TTL 計算範例；§10 新增 §10.1 Transactor pattern + §10.2 service 使用範例（SELECT FOR UPDATE 子句）；§14 重組為 Infra/Domain/測試/Migration/規格同步 五區，加路由註冊順序 Go 範例 |
| v1.2 | 2026-06-28 | 把 user-level revoke 機制正式落地到 `infrastructure.md` v1.11（§7.5 `UserRevocationStore` + §8.5 step 3.5 + §12.4 + §18.2 metric）；本檔 §4.3/§4.4/§14 改為直接引用 §7.5 而非散描述 |
| v1.1 | 2026-06-28 | 依使用者回饋調整：移除 `POST /cms/users`（改由 `/auth/register` 公開註冊提供）；§4.3/§4.4 加入 role 變更與軟刪除的「自動 revoke target sessions」流程；新增 `cms_user.sessions_force_revoked` audit event；引入 user-level revoke 機制概念 |
| v1.0 | 2026-06-28 | 初版草案 |
