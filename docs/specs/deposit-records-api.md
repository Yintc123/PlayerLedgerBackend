# 儲值紀錄（Deposit Records）— API 規格書

版本：v1.6
日期：2026-06-29

> v1.6：移除 API 版本前綴——玩家自助端點 `/api/v1/me/deposit-records` 改為 `/api/me/deposit-records`
> （§1 路由前綴、§2 權限矩陣、§3 endpoint 清單、§4.5 同步更新）。所有端點統一無版本號，
> 對齊 `infrastructure.md` 與 `schema/openapi.yaml`（global servers 改為 `/api`）。
>
> v1.5：補齊四項實作模糊點——
> §4.4 補充並發策略（last-write-wins，見 model §1）；
> §4.5 明確說明 member token `claims.sub` = `members.id`；
> §9 新增 `reference_no_conflict` 錯誤碼（對應 model `ErrReferenceNoConflict`）；
> `sort` 白名單已在 model §7 `DepositRecordFilter.Sort` 定義，handler 驗證後傳入。
>
> v1.4：§3 修正 Service 欄位命名——`DepositService.UpdateStatus` 改為 `DepositService.Update`，
> 與 model spec §7.5 interface 定義一致。
>
> v1.3：§4.4 PATCH 備註欄位改為三態語意（nil=不動 / null=清空 / value=設值），
> 對應 model `UpdateDepositInput` 的 `**string` 設計；
> §10 測試 checklist 修正「pending → cancelled → 204」為正確的 200。
>
> v1.2：PATCH 新增 `cancelled` 為合法目標狀態（pending → cancelled）；
> §4.1 說明 `amount` 為幣別最小單位；
> §8 Audit Log 拆分為 `deposit.status_changed`（status 實際改變）與
> `deposit.note_updated`（純備註更新），消除語意錯誤；
> §7 路由補 `X-Forwarded-For` 信任模型說明。
>
> v1.1：`player_name` / `operator_ip` 改由 server 填入；`note` 拆雙軌；
> PATCH 語意明確為覆蓋；cmsGroup 共用說明；`DepositRecordPublicDTO` 改用 `display_note`；
> `UpdateStatus` 回傳更新後 record。

範圍：儲值紀錄的 HTTP API（CMS 管理端 + 玩家自助查詢）
對應規格：`deposit-records-model.md`、`infrastructure.md` §3/§10/§12/§15/§17/§18

---

## 1. 設計原則

- **SDD**：所有 endpoint 必須先在 `schema/openapi.yaml` 定義，再寫 handler 與測試。
- **回應 envelope**：成功走 `Response<T>` / `204`，錯誤走共用 `ErrorResponse`（`infrastructure.md` §10.2 / §12.4）。
- **不可變欄位保護**：`amount`、`currency`、`player_id`、`payment_method` 建立後不得修改；
  PATCH endpoint 僅接受 `status`、`internal_note`、`display_note`。
- **狀態轉換強制**：非法 status 轉換在 service 層以 `model.CanTransition` 驗證，回傳 `422 invalid_transition`。
- **備註覆蓋語意**：PATCH 更新備註為**覆蓋**（replace）；歷史備註由 audit log 保存，不在 DB 追加。
- **server 自動填入欄位**：`player_name`（從 members.display_name 查）、`operator_id`（從 token）、`operator_ip`
  （從 HTTP request via `c.ClientIP()`）均由 server 填入，caller 無法指定，防止偽造。
- **路由前綴**：所有端點統一無版本號（v1.6 起移除 `/api/v1`）。
  - CMS 管理端：`/api/cms/`
  - 玩家自助查詢：`/api/me/deposit-records`

---

## 2. 權限模型

| 動作 | admin | user | viewer | member（玩家）| 未登入 |
|---|---|---|---|---|---|
| `POST /api/cms/deposit-records` 建立 | ✅ | ✅ | ❌ | ❌ | ❌ |
| `GET /api/cms/deposit-records` 列表 | ✅ | ✅ | ✅ | ❌ | ❌ |
| `GET /api/cms/deposit-records/:id` 單筆 | ✅ | ✅ | ✅ | ❌ | ❌ |
| `PATCH /api/cms/deposit-records/:id` 改狀態／備註 | ✅ | ❌ | ❌ | ❌ | ❌ |
| `GET /api/me/deposit-records` 查自己 | ❌ | ❌ | ❌ | ✅ | ❌ |

- CMS 端點：`Authorization: Bearer <access_token>`，`claims.utype == "cms"`；非 CMS 回 `403 forbidden`。
- 玩家端點：`Authorization: Bearer <access_token>`，`claims.utype == "member"`；非 member 回 `403 forbidden`。

---

## 3. Endpoint 清單

| 方法 | 路徑 | Auth | 權限 | Service | 摘要 |
|---|---|---|---|---|---|
| POST | `/api/cms/deposit-records` | access token | admin, user | `DepositService.Create` | 建立儲值紀錄 |
| GET | `/api/cms/deposit-records` | access token | CMS staff | `DepositService.List` | 列出紀錄（分頁、篩選）|
| GET | `/api/cms/deposit-records/:id` | access token | CMS staff | `DepositService.Get` | 取單筆 |
| PATCH | `/api/cms/deposit-records/:id` | access token | admin only | `DepositService.Update` | 更新 status / 備註 |
| GET | `/api/me/deposit-records` | access token | member | `DepositService.ListByPlayer` | 玩家查自己的紀錄 |

---

## 4. Endpoint 細節

### 4.1 `POST /api/cms/deposit-records` — 建立

**Request Body**：

```json
{
  "player_id":      "0193b3f4-1234-7abc-9def-000000000001",
  "amount":         1000,
  "currency":       "TWD",
  "payment_method": "bank_transfer",
  "internal_note":  "客服補單，玩家提供匯款收據",
  "display_note":   "銀行轉帳儲值",
  "reference_no":   "TXN-20260629-001"
}
```

| 欄位 | 型別 | 必填 | 說明 |
|---|---|---|---|
| `player_id` | UUID | ✅ | 必須存在於 `members` 表 |
| `amount` | integer | ✅ | 正整數；單位為幣別最小單位（TWD：元，USD：cents）|
| `currency` | string | ❌ | 預設 `TWD`；必須為 3 字元 ISO 4217 |
| `payment_method` | string | ✅ | 見 model §2.2 enum 白名單 |
| `internal_note` | string | ❌ | staff 內部備註，上限 2000 字元，**不對玩家顯示** |
| `display_note` | string | ❌ | 對玩家顯示的說明，上限 500 字元 |
| `reference_no` | string | ❌ | 上限 128 字元；若提供，系統檢查唯一性 |

**Server 自動填入（caller 不得指定）**：

| 欄位 | 來源 |
|---|---|
| `player_name` | 從 `members.display_name`（顯示暱稱）查詢（驗證 player_id 存在後）；非 `username` 登入帳號 |
| `operator_id` | access token `claims.sub` |
| `operator_ip` | `c.ClientIP()`（見 §7 信任模型說明） |
| `status` | 固定 `pending` |

**回應**：`201 Created`，body 為 `Response<DepositRecordDTO>`。

**錯誤**：
- `400 invalid input` — 欄位格式錯誤、amount ≤ 0 或非整數、currency 非 3 字元、payment_method 不在白名單
- `401 unauthorized` / `403 forbidden` — 同 §2
- `404 not_found` — player_id 不存在於 members 表
- `409 resource already exists` — reference_no 已被其他紀錄使用（DB unique constraint 兜底）
- `429 too many requests` — 觸發限流

---

### 4.2 `GET /api/cms/deposit-records` — 列表

**Query 參數**（皆 optional）：

| 名稱 | 型別 | 預設 | 說明 |
|---|---|---|---|
| `page` | int | 1 | 1-based |
| `page_size` | int | 20 | 上限 100 |
| `player_id` | UUID | — | 篩特定玩家 |
| `status` | string | — | 可重複出現做 OR 篩選（`?status=pending&status=failed`）|
| `payment_method` | string | — | 可重複出現做 OR 篩選 |
| `start_date` | date（`YYYY-MM-DD`）| — | `created_at >= start_date 00:00:00 UTC` |
| `end_date` | date（`YYYY-MM-DD`）| — | `created_at <= end_date 23:59:59 UTC` |
| `sort` | string | `-created_at` | 白名單：`created_at`、`-created_at`、`amount`、`-amount` |

**參數驗證**：

| 參數 | 驗證 | 違反處理 |
|---|---|---|
| `page_size` | ≤ 100；< 1 → 預設 20 | > 100 → 400 |
| `status[]` | 每個值必須在 enum 白名單 | 400 `invalid input` |
| `payment_method[]` | 每個值必須在 enum 白名單 | 400 `invalid input` |
| `start_date` / `end_date` | 合法日期格式；end_date 不可早於 start_date | 400 `invalid input` |
| `sort` | 必須在白名單 | 400 `invalid input` |

**回應**：`200 OK`，body 為 `Response<[]DepositRecordDTO>` + `meta` 分頁。

**錯誤**：`400 invalid input`、`401`、`403`、`429`。

---

### 4.3 `GET /api/cms/deposit-records/:id` — 單筆

**Path 參數**：`id` UUID v4。

**回應**：`200 OK`，body 為 `Response<DepositRecordDTO>`。

**錯誤**：
- `400 invalid input` — id 非 UUID 格式
- `401`、`403`、`429` — 同前
- `404 not_found` — id 不存在

---

### 4.4 `PATCH /api/cms/deposit-records/:id` — 更新狀態 / 備註

**Request Body**：

```json
{
  "status":        "completed",
  "internal_note": "金流商已確認入帳，單號 ABC-123",
  "display_note":  "儲值已完成"
}
```

| 欄位 | 型別 | 必填 | 說明 |
|---|---|---|---|
| `status` | string | ❌ | 目標狀態；合法轉換見 model §4 |
| `internal_note` | string \| null \| 缺席 | ❌ | **三態語意**（見下方說明）；上限 2000 字元 |
| `display_note` | string \| null \| 缺席 | ❌ | **三態語意**（見下方說明）；上限 500 字元 |

**備註欄位三態語意**：

| JSON 表示 | 語意 | repository 收到 |
|---|---|---|
| `"internal_note": "新備註"` | 設定新值 | `**string`，outer non-nil，inner non-nil |
| `"internal_note": null` | 清空（設為 NULL）| `**string`，outer non-nil，inner nil |
| 欄位缺席（不傳）| 不修改現有值 | `**string`，outer nil |

Go 標準 JSON decode 到 `*string` 無法區分 null 與缺席，handler 需使用 `**string` 或自訂
`json.Unmarshaler` 實現三態解析，再映射至 `repository.UpdateDepositInput`（見 model §7）。

**合法目標 status**（由 `model.CanTransition` 決定，與當前 status 有關）：

| 當前狀態 | 可轉換至 |
|---|---|
| `pending` | `completed`、`failed`、`cancelled` |
| `completed` | `refunded` |
| `failed` | —（終態）|
| `cancelled` | —（終態）|
| `refunded` | —（終態）|

- 至少提供三個欄位其中一個，否則 `400 invalid input`。
- 合法狀態轉換由 service 層呼叫 `model.CanTransition` 驗證，非法轉換回 `422 invalid_transition`。
- 備註更新語意為**覆蓋**；歷史備註由 audit log 保存。
- **並發更新**：採 last-write-wins，後到的請求直接覆蓋前一次結果（見 model §1）。

**回應**：`200 OK`，body 為 `Response<DepositRecordDTO>`（service 直接回傳更新後 record）。

**錯誤**：
- `400 invalid input` — 格式錯誤、body 為空
- `401`、`403`、`429` — 同前
- `404 not_found` — id 不存在
- `422 invalid_transition` — 非法狀態轉換

---

### 4.5 `GET /api/me/deposit-records` — 玩家查自己的紀錄

**Query 參數**（皆 optional）：

| 名稱 | 型別 | 預設 | 說明 |
|---|---|---|---|
| `page` | int | 1 | 1-based |
| `page_size` | int | 20 | 上限 50（玩家端低於 CMS 端，降低大量拉取風險）|
| `start_date` | date | — | 同 §4.2 |
| `end_date` | date | — | 同 §4.2 |

- `player_id` 從 access token `claims.sub` 自動取得，caller 不可指定（防止查別人的紀錄）。
  member token 的 `claims.sub` 為 `members.id`（UUID）；與 CMS token 的 `claims.sub`（`cms_users.id`）不同型，
  middleware 已透過 `claims.utype == "member"` 確保 token 來源正確。
- 固定按 `-created_at` 排序，不開放 sort 控制。

**回應**：`200 OK`，body 為 `Response<[]DepositRecordPublicDTO>` + `meta` 分頁。

**錯誤**：`400 invalid input`、`401`、`403`、`429`。

---

## 5. DTO 定義

### 5.1 `DepositRecordDTO`（CMS 端，含完整欄位）

```go
type DepositRecordDTO struct {
    ID            string  `json:"id"`
    PlayerID      string  `json:"player_id"`
    PlayerName    string  `json:"player_name"`
    Amount        int64   `json:"amount"`           // 幣別最小單位
    Currency      string  `json:"currency"`
    Status        string  `json:"status"`
    PaymentMethod string  `json:"payment_method"`
    OperatorID    *string `json:"operator_id,omitempty"`
    OperatorIP    *string `json:"operator_ip,omitempty"`
    InternalNote  *string `json:"internal_note,omitempty"`
    DisplayNote   *string `json:"display_note,omitempty"`
    ReferenceNo   *string `json:"reference_no,omitempty"`
    CreatedAt     string  `json:"created_at"`       // RFC 3339
    UpdatedAt     string  `json:"updated_at"`       // RFC 3339
}
```

### 5.2 `DepositRecordPublicDTO`（玩家端，隱藏敏感欄位）

```go
type DepositRecordPublicDTO struct {
    ID            string  `json:"id"`
    Amount        int64   `json:"amount"`
    Currency      string  `json:"currency"`
    Status        string  `json:"status"`
    PaymentMethod string  `json:"payment_method"`
    DisplayNote   *string `json:"display_note,omitempty"` // 不含 internal_note
    CreatedAt     string  `json:"created_at"`
}
```

> 玩家端完全不暴露：`operator_id`、`operator_ip`、`internal_note`、`reference_no`、`player_id`、`player_name`。

---

## 6. OpenAPI Schema 片段

待 handler 實作時，以下路徑需完整展開並貼入 `schema/openapi.yaml`：

```yaml
tags:
  - name: deposit-records
    description: 儲值紀錄管理

# CMS endpoints — path-level servers 覆寫（無版本號，見 openapi.yaml servers 說明）
/cms/deposit-records:
  servers:
    - url: http://localhost:8080/api
    - url: https://api.example.com/api
  post:
    tags: [deposit-records]
    summary: 建立儲值紀錄
    operationId: createDepositRecord

  get:
    tags: [deposit-records]
    summary: 列出儲值紀錄（CMS）
    operationId: listDepositRecords

/cms/deposit-records/{id}:
  servers:
    - url: http://localhost:8080/api
    - url: https://api.example.com/api
  get:
    tags: [deposit-records]
    summary: 取單筆儲值紀錄
    operationId: getDepositRecord

  patch:
    tags: [deposit-records]
    summary: 更新儲值狀態 / 備註
    operationId: updateDepositRecord

# 玩家端（走 /api global servers）
/me/deposit-records:
  get:
    tags: [deposit-records]
    summary: 玩家查自己的儲值紀錄
    operationId: listMyDepositRecords
```

---

## 7. 路由註冊（main.go 參考）

```go
// X-Forwarded-For 信任模型：只信任已知 proxy，防止 client 偽造 IP。
// 需在 router 初始化後配置：
//   router.SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"})
// handler 透過 c.ClientIP() 取得可信 IP；若無可信 proxy，c.ClientIP() 回傳 RemoteAddr。

// cmsGroup：若 cms-users 路由已建立此 group，共用同一個，不重複掛 middleware。
// cmsGroup := router.Group("/api/cms",
//     jwt.AuthMiddleware(jwtMgr, blacklist, userRevoke),
//     jwt.RequireUserType(jwt.UserTypeCMS),
// )

depositH := handler.NewDepositHandler(depositService)
{
    cmsGroup.POST("/deposit-records",      jwt.RequireRole(jwt.RoleAdmin, jwt.RoleUser), depositH.Create)
    cmsGroup.GET("/deposit-records",       depositH.List)
    cmsGroup.GET("/deposit-records/:id",   depositH.Get)
    cmsGroup.PATCH("/deposit-records/:id", jwt.RequireRole(jwt.RoleAdmin), depositH.UpdateStatus)
}

// 玩家端（/api，走既有 apiGroup + RequireUserType member）
memberGroup := apiGroup.Group("").Use(
    jwt.AuthMiddleware(jwtMgr, blacklist, userRevoke),
    jwt.RequireUserType(jwt.UserTypeMember),
)
{
    memberGroup.GET("/me/deposit-records", depositH.ListMine)
}
```

---

## 8. Audit Log 需求

金融操作必須寫 audit log（對應 `infrastructure.md` §18.3），使用 best-effort 模式（寫入失敗僅 warn + metric，不 rollback 主操作）。

| EventType | 觸發條件 | 關鍵欄位 |
|---|---|---|
| `deposit.created` | POST 建立成功後 | `deposit_id`、`player_id`、`amount`、`currency`、`payment_method`、`operator_id` |
| `deposit.status_changed` | PATCH 且 `status` 實際發生改變 | `deposit_id`、`from_status`、`to_status`、`operator_id` |
| `deposit.note_updated` | PATCH 且 status **未改變**，僅更新備註 | `deposit_id`、`operator_id` |

> `deposit.status_changed` 與 `deposit.note_updated` 的判斷在 service 層進行：
> 若 request body 同時帶 `status` 與備註，且 status 確實改變，僅觸發 `deposit.status_changed`（不重複觸發 note_updated）。

---

## 9. Error Code 補充

除 `infrastructure.md` §12.4 現有 error code 外，新增：

| error | HTTP | 場景 |
|---|---|---|
| `invalid_transition` | 422 | 非法 status 轉換（如 `failed → completed`、`cancelled → completed`） |
| `resource already exists` | 409 | `reference_no` 與現有紀錄重複（service 偵測 pgconn code `23505`，回傳 `ErrReferenceNoConflict`）|

---

## 10. 實作 Checklist

**Schema（SDD 第一步）**：
- [ ] 將 §6 OpenAPI 片段完整展開並貼入 `schema/openapi.yaml`

**後端**：
- [ ] `internal/service/deposit_service.go`
  - Create：查 members 取 `player_name`、`c.ClientIP()` 取 `operator_ip`、固定 `status=pending`
  - UpdateStatus：`model.CanTransition` 驗證 → 更新 → 判斷觸發 `status_changed` 或 `note_updated` audit event
- [ ] `internal/service/deposit_service_test.go`（unit test：8 種非法轉換、player 不存在、reference_no 重複、audit event 分支）
- [ ] `internal/handler/deposit_handler.go`（5 個 handler）
- [ ] `internal/handler/deposit_handler_test.go`（E2E：httptest，golden path + 非法轉換 + 403）
- [ ] `cmd/server/main.go`：`SetTrustedProxies` 配置、wire service / handler、共用 `cmsGroup`
- [ ] `internal/dto/deposit_record_dto.go`（`DepositRecordDTO` + `DepositRecordPublicDTO`）

**測試**：
- [ ] 建立：player_id 不存在 → 404、reference_no 重複 → 409、player_name 快照正確、operator_ip 自動填入
- [ ] 列表：status + payment_method 組合篩選、page_size > 100 → 400、cancelled 可出現在篩選結果
- [ ] 更新：8 種非法轉換 → 422、pending → cancelled → 200 + DepositRecordDTO、viewer 呼叫 → 403
- [ ] Audit：status 改變觸發 `status_changed`，純備註更新觸發 `note_updated`，不重複觸發
- [ ] 玩家端：player_id 由 token 決定，資料隔離驗證（不可查他人紀錄）
