# 玩家查詢（Players）— API 規格書

版本：v1.0
日期：2026-06-29

> v1.2：補齊精度缺口並定案待確認項——
> §1 路徑 `/api/cms/players` 由「待確認」改為**定案**（理由 + 前端對齊說明）；
> §4.1 email / phone 比對語意**定案**（email 前綴 / phone 精確，附替代方案）、
> 新增「參數正規化與驗證」表（空字串視為未提供、maxLength、NFC 正規化位置）、cursor 與 filter 耦合規則；
> §5.2 明確「僅遮罩 email / phone，其餘欄位不遮罩」。
>
> v1.1：最佳實踐修正——
> §1 補「PII 走 GET query string」的暴露面與緩解（含 enumeration 風險）；
> §4.1 補 email / phone 比對語意待前端確認的標注；§5.2 補 phone canonical E.164 假設；
> §6 service 改 keyset cursor、明確 `limit+1` 由 service 負責；
> §8 OpenAPI nullable 改 3.1 寫法 `type: [string, 'null']`。
>
> v1.0：首版。新增 CMS 玩家查詢端點——`GET /api/cms/players`（搜尋）與
> `GET /api/cms/players/:id`（詳情）。對應前端規格
> `PlayerLedgerFrontend/docs/specs/05-player-query-domain.md`、`07-admin-rbac-audit.md`、`08`、`09`。

範圍：玩家查詢的 HTTP API（CMS 管理端，唯讀）
對應規格：`players-model.md`、`infrastructure.md` §3/§10/§12/§16/§18、`deposit-records-api.md`（玩家儲值紀錄沿用 `GET /api/cms/deposit-records?player_id=`）

---

## 1. 設計原則

- **SDD**：所有 endpoint 必須先在 `schema/openapi.yaml` 定義，再寫 handler 與測試。
- **回應 envelope**：成功走 `Response<T>`，錯誤走共用 `ErrorResponse`（`infrastructure.md` §10.2 / §12.4）。
- **唯讀**：本期僅提供搜尋與詳情查詢；無建立 / 修改 / 刪除玩家的端點。
- **路徑前綴採後端 CMS 慣例**：玩家查詢為 CMS staff 專屬（需 `utype=cms`），
  故掛在 `/api/cms/` 下，與 `deposit-records`、`users` 一致，共用 `cmsGroup`。
  > **定案路徑 `/api/cms/players`（含與前端 spec 05 的偏離說明）**：
  > 前端 `05 §4` 將端點寫為 `/api/players/search` 與 `/api/players/{id}`（無 `cms` 前綴、
  > 搜尋帶 `/search` 子路徑），但其 BFF proxy 測試（`app/api/proxy-handler.test.ts`）實際使用
  > `/api/cms/players`。本規格**定案**採後端統一慣例：
  > **`GET /api/cms/players`（集合根帶 query 即為搜尋）+ `GET /api/cms/players/:id`（詳情）**。
  > 理由：(1) 此端點要求 `utype=cms`，與 `deposit-records`、`users` 同屬 CMS staff 資源，
  > 掛 `/api/cms/` 下可共用 `cmsGroup` 的 auth / user-type middleware；(2) 與既有集合資源
  > 「GET 集合帶 query 篩選」慣例一致（對比 `GET /api/cms/deposit-records`），不另立 `/search` 動詞路徑；
  > (3) proxy 測試已採此路徑。
  > **前端對齊**：BFF 為透明 proxy（`app/api/[...path]/route.ts`），`lib/players/{search,get}.ts`
  > 目前為 mock，後端就緒後僅需將其內部 fetch 目標改為 `/api/cms/players`，呼叫端（Server/Client Component）不需改。
  > 此為單行改動，非阻塞；通知前端據此調整即可。
- **PII 角色遮罩**：`email`、`phone` 對 `user` / `viewer` 角色遮罩，**僅 `admin` 回完整值**
  （見 §5）。遮罩在 handler 組裝 DTO 時依 token `claims.role` 套用。
- **Cursor 分頁（keyset）**：搜尋採 opaque cursor（`cursor` / `next_cursor`），非 `page/page_size`
  （對齊前端契約）；底層為 keyset（`created_at, id`）以避免並發插入造成跨頁重複 / 漏列，
  機制見 `players-model.md` §6。
- **軟刪除隔離**：已刪除玩家（`deleted_at` 非空）不出現在搜尋結果，詳情查詢回 `404`。
- **PII 安全（GET query string 暴露面）**：`email` / `phone` 為 PII，經 GET query 傳遞。
  本服務 request logger 以 `c.FullPath()`（模板路徑 `/cms/players`）記錄，**不含 query string**，
  應用層日誌安全。但 PII 仍可能外洩至**基礎設施層存取日誌**（ALB / nginx / CloudWatch 記錄完整 URL）、
  **瀏覽器歷史**與 **Referer**。緩解要求：
  - 部署層確保負載平衡 / 反向代理的 access log **不記錄 query string**（或對 `email` / `phone` 遮罩）；
  - 若上述無法保證，評估改 `POST /api/cms/players/search` 帶 request body（代價：偏離前端 GET 契約，須與前端確認）。
  - **Enumeration 風險**：精確 `email` / `phone` 查詢可被用來探測帳號是否存在（含 viewer）；
    依賴既有 per-user rate limit（`infrastructure.md` §17）兜底，並由 §9 audit log 留痕。

---

## 2. 權限模型

| 動作 | admin | user | viewer | member（玩家）| 未登入 |
|---|---|---|---|---|---|
| `GET /api/cms/players` 搜尋 | ✅ | ✅（遮罩 PII）| ✅（遮罩 PII）| ❌ | ❌ |
| `GET /api/cms/players/:id` 詳情 | ✅ | ✅（遮罩 PII）| ✅（遮罩 PII）| ❌ | ❌ |

- 皆需 `Authorization: Bearer <access_token>`，`claims.utype == "cms"`；非 CMS 回 `403 forbidden`。
- 三種 CMS 角色皆可查；差異僅在 `user` / `viewer` 的 `email` / `phone` 被遮罩，**僅 `admin` 回完整值**（見 §5）。

---

## 3. Endpoint 清單

| 方法 | 路徑 | Auth | 權限 | Service | 摘要 |
|---|---|---|---|---|---|
| GET | `/api/cms/players` | access token | CMS staff | `PlayerService.Search` | 依條件搜尋玩家（cursor 分頁）|
| GET | `/api/cms/players/:id` | access token | CMS staff | `PlayerService.Get` | 取單一玩家詳情 |

> 玩家的儲值彙總（畫面 09）與單玩家儲值列表（畫面 10）**不新增端點**：
> 沿用既有 `GET /api/cms/deposit-records?player_id=<id>`（見 `deposit-records-api.md` §4.2）。
> 儲值彙總（`TopupSummary` 聚合）後端尚無端點，前端暫以 mock 呈現，屬未來範圍（見 §8）。

---

## 4. Endpoint 細節

### 4.1 `GET /api/cms/players` — 搜尋

**Query 參數**：

| 名稱 | 型別 | 必填 | 說明 |
|---|---|---|---|
| `player_id` | UUID | 條件* | 精確比對；非 UUID 格式 → 400 |
| `external_id` | string | 條件* | 精確比對 |
| `display_name` | string | 條件* | 前綴（大小寫不敏感）|
| `email` | string | 條件* | 前綴比對（lowercase）|
| `phone` | string | 條件* | 正規化後精確比對 |
| `cursor` | string | ❌ | opaque keyset cursor（後端編碼 `(created_at, id)`）；缺省為第一頁 |
| `limit` | int | ❌ | `1..50`，預設 `20`；超出 → 400 |

\* **至少需提供一個「有效」搜尋條件**（見下方正規化表的「空字串處理」）；
無任何有效條件 → `400 invalid input`。多個有效條件以 **AND** 組合。

**參數正規化與驗證**（由 handler 處理後再傳入 service）：

| 參數 | 正規化（依序）| 驗證 | 違反處理 |
|---|---|---|---|
| `player_id` | `strings.TrimSpace` | 須為合法 UUID | 400 `invalid input` |
| `external_id` | trim | maxLength 64 | 400 |
| `display_name` | trim → **NFC**（`golang.org/x/text/unicode/norm` `norm.NFC`）| maxLength 64 | 400 |
| `email` | trim → `strings.ToLower` | maxLength 255 | 400 |
| `phone` | trim → 去除空白 / `-` / `(` / `)`（保留前導 `+`）| maxLength 32 | 400 |
| `limit` | — | `1..50`；缺省 20 | 超出 → 400 |
| `cursor` | — | 可成功 base64url-decode 為合法 keyset JSON | 解析失敗 → 400 |

- **空字串處理**：任一搜尋條件參數經 trim / 正規化後為**空字串**，一律**視為未提供**（忽略該條件），
  不視為「比對空字串」。例：`?email=` 或 `?display_name=%20` 等同未帶該參數。
- **有效條件判定**：上述五個搜尋參數經正規化後**全部為空** → `400 invalid input`（不允許無條件全表瀏覽）。
- **AND 組合**：所有「有效」條件以 AND 串接。

> **email / phone 比對語意（定案）**：採 **email 前綴 / phone 精確（canonical E.164）**。
> 理由：(1) btree 前綴索引效率佳、無需額外擴充；(2) 降低以子字串掃描造成的 enumeration / 效能風險；
> (3) 對 back-office「輸入開頭即查」的查找情境已足夠。
> 前端 mock（`lib/players/search.ts`）目前用子字串（contains），屬 mock 便利寫法，整合時改打真實端點即對齊本契約。
> **替代方案**：若產品確需 email **子字串**搜尋，改用 `pg_trgm` 擴充 + GIN trigram 索引
> （見 `players-model.md` §4/§10），本契約其餘不變。

> **cursor 與 filter 的耦合**：keyset `cursor` 綁定「產生它的那組搜尋條件 + 排序」。
> 翻頁時呼叫端**必須保持搜尋條件不變**，只更新 `cursor`；一旦變更任一搜尋參數，
> 必須**重置**（移除 `cursor`）重新從第一頁查起。v1 後端不在 cursor 內嵌 filter 指紋、
> 不主動偵測不一致（cursor 對前端 opaque、由前端負責）；若 cursor 對應的列已被軟刪除，
> keyset 條件仍以其 `(created_at, id)` 續查，結果正確（該列本就不在結果集）。

**回應**：`200 OK`，body 為 `Response<PlayerSearchResult>`：

```json
{
  "success": true,
  "request_id": "01J...",
  "data": {
    "players": [
      {
        "player_id": "0193b3f4-1234-7abc-9def-000000000001",
        "external_id": "EXT-10001",
        "display_name": "王小明",
        "email": "w***@example.com",
        "phone": "+886***5678",
        "status": "active",
        "registered_at": "2026-05-01T08:30:00Z",
        "last_active_at": null
      }
    ],
    "next_cursor": "MjA"
  }
}
```

> 上例 `email` / `phone` 為 `user` / `viewer` 視角（已遮罩）；僅 `admin` 視角為完整值。
> `next_cursor` 為 null 表示最後一頁。此端點 **不含** `meta`（非 page 分頁）。

**錯誤**：
- `400 invalid input` — 未提供任何搜尋條件、`player_id` 非 UUID、`limit` 超出範圍、`cursor` 解析失敗
- `401 unauthorized` / `403 forbidden` — 同 §2
- `429 too many requests` — 觸發限流

---

### 4.2 `GET /api/cms/players/:id` — 詳情

**Path 參數**：`id` UUID（玩家 `player_id`）。

**回應**：`200 OK`，body 為 `Response<PlayerDTO>`（單筆，欄位同 §5 表；`user` / `viewer` 遮罩 PII）。

**錯誤**：
- `400 invalid input` — `id` 非 UUID 格式
- `401`、`403`、`429` — 同前
- `404 resource not found` — 玩家不存在或已軟刪除

---

## 5. DTO 定義與角色遮罩

### 5.1 `PlayerDTO`

```go
type PlayerDTO struct {
    PlayerID     string  `json:"player_id"`
    ExternalID   *string `json:"external_id"`              // 無值為 null
    DisplayName  string  `json:"display_name"`
    Email        *string `json:"email"`                   // user / viewer 遮罩；無值為 null
    Phone        *string `json:"phone"`                   // user / viewer 遮罩；無值為 null
    Status       string  `json:"status"`                  // active | frozen | closed
    RegisteredAt string  `json:"registered_at"`           // RFC 3339（= members.created_at）
    LastActiveAt *string `json:"last_active_at"`           // 本期恆為 null
}

type PlayerSearchResult struct {
    Players    []PlayerDTO `json:"players"`
    NextCursor *string     `json:"next_cursor"`            // 最後一頁為 null
}
```

> 前端 BFF 將 snake_case 轉為 camelCase（`05 §3`）。`username` / `password_hash` **不**外露。
> 欄位皆顯式輸出（含 null），不用 `omitempty`，確保前端型別穩定。

### 5.2 角色遮罩規則

於 handler 組裝 DTO 時，依 `claims.role` 套用；**僅 `admin` 不遮罩，`user` / `viewer` 皆遮罩**。
**僅 `email`、`phone` 兩欄遮罩**；`player_id` / `external_id` / `display_name` / `status` /
`registered_at` / `last_active_at` 對所有 CMS 角色皆回完整值（不遮罩）。

| 欄位 | user / viewer 遮罩格式 | 範例 | 規則 |
|---|---|---|---|
| `email` | 本地部分保留首字 + `***`，保留 `@domain` | `wang@example.com` → `w***@example.com` | 本地長度 ≤ 1 或格式異常 → 整段 `***` |
| `phone` | 保留前 4 碼 + `***` + 末 4 碼 | `+886912345678` → `+886***5678` | 長度 < 8 → 整段 `***` |

- `null` 值不遮罩（仍為 `null`）。
- 遮罩為**輸出層**處理；搜尋條件比對（§4.1）一律以**原始值**進行，
  即 user / viewer 仍可用完整 email/phone 查詢，只是回傳被遮罩。
- `phone` 遮罩假設儲存值為 canonical **E.164**（`+886912345678`）；非標準格式時遮罩規則
  退化為「整段 `***`」（見上表長度 < 8 規則），避免誤露。

---

## 6. Service Interface

```go
package service

import (
    "context"

    "github.com/google/uuid"
    "github.com/yintengching/playerledger/internal/model"
)

// PlayerSearchInput handler 解析 query 後傳入；字串已完成 trim / lowercase / 正規化。
// 至少一個搜尋條件非 nil（handler 保證）；Limit 為 pageSize（已套預設 20 與上限 50 驗證）。
type PlayerSearchInput struct {
    PlayerID    *uuid.UUID
    ExternalID  *string
    DisplayName *string // 前綴
    Email       *string // 前綴（lowercase）
    Phone       *string // 正規化
    Cursor      *string // opaque keyset cursor；service 負責解碼為 repository.Keyset
    Limit       int     // = pageSize（1..50）
}

type PlayerSearchOutput struct {
    Players    []*model.Member // 已砍至 pageSize 筆
    NextCursor *string         // 最後一頁為 nil
}

type PlayerService interface {
    // Search 解碼 cursor → repository.Keyset，組 repository.PlayerSearchFilter 並
    // 將 filter.Limit 設為 in.Limit+1（多取一筆判斷 hasMore；repository 忠實照 Limit 查）。
    // 取回後若筆數 > pageSize → 砍至 pageSize 筆、以保留的最後一列 (created_at, id) 編碼 NextCursor；
    // 否則 NextCursor = nil。最後寫 players.search audit log。
    // cursor 解碼失敗回 apperr.ErrInvalidInput（handler 映射 400）。
    Search(ctx context.Context, in PlayerSearchInput) (PlayerSearchOutput, error)

    // Get 回傳玩家；不存在 / 已軟刪除回 apperr.ErrNotFound（handler 映射 404）。
    // 寫 players.read audit log。
    Get(ctx context.Context, id uuid.UUID) (*model.Member, error)
}
```

> 角色遮罩**不**在 service 進行（service 不感知角色）；由 handler 取 `claims.role` 後套用至 DTO。

---

## 7. 路由註冊（main.go 參考）

```go
playerH := handler.NewPlayerHandler(playerService)
{
    // cmsGroup 已掛 AuthMiddleware + RequireUserType(CMS)，三角色皆可讀，無需 RequireRole
    cmsGroup.GET("/players",     playerH.Search)
    cmsGroup.GET("/players/:id", playerH.Get)
}
```

---

## 8. OpenAPI Schema 片段

待 handler 實作時，以下展開並貼入 `schema/openapi.yaml`：

```yaml
tags:
  - name: players
    description: 玩家查詢（CMS）

/cms/players:
  servers:
    - url: http://localhost:8080/api
    - url: https://api.example.com/api
  get:
    tags: [players]
    summary: 搜尋玩家（CMS）
    operationId: searchPlayers
    # query: player_id, external_id, display_name, email, phone, cursor, limit
    # 200 → Response<PlayerSearchResult>；400/401/403/429 → ErrorResponse

/cms/players/{id}:
  servers:
    - url: http://localhost:8080/api
    - url: https://api.example.com/api
  get:
    tags: [players]
    summary: 取玩家詳情（CMS）
    operationId: getPlayer
    # 200 → Response<PlayerDTO>；400/401/403/404/429 → ErrorResponse
```

> `PlayerDTO` 與 `PlayerSearchResult` 需於 `components/schemas` 定義。
> schema 為 **OpenAPI 3.1.0**，nullable 一律用 `type: [<type>, 'null']`（**非** 3.0 的 `nullable: true`），
> 與既有 `schema/openapi.yaml` 慣例一致。例：
>
> ```yaml
> PlayerDTO:
>   type: object
>   required: [player_id, display_name, status, registered_at, external_id, email, phone, last_active_at]
>   properties:
>     player_id:      { type: string, format: uuid }
>     external_id:    { type: [string, 'null'] }
>     display_name:   { type: string }
>     email:          { type: [string, 'null'], description: "viewer 角色遮罩" }
>     phone:          { type: [string, 'null'], description: "viewer 角色遮罩" }
>     status:         { type: string, enum: [active, frozen, closed] }
>     registered_at:  { type: string, format: date-time }
>     last_active_at: { type: [string, 'null'], format: date-time }
> PlayerSearchResult:
>   type: object
>   required: [players, next_cursor]
>   properties:
>     players:     { type: array, items: { $ref: "#/components/schemas/PlayerDTO" } }
>     next_cursor: { type: [string, 'null'], description: "opaque keyset cursor；最後一頁為 null" }
> ```

---

## 9. Audit Log 需求

對應前端 `07-admin-rbac-audit.md` §8 與 `infrastructure.md` §18.3，best-effort（寫入失敗僅 warn + metric）。

| EventType | 觸發條件 | 關鍵欄位 |
|---|---|---|
| `players.search` | 搜尋成功後 | `operator_id`、`role`、`query`（去敏：欄位名 + 是否提供，**不記** email/phone 原值）、`result_count`、`request_id` |
| `players.read` | 詳情查詢成功後 | `operator_id`、`role`、`target_player_id`、`request_id` |

> `query` 寫入需避免記錄完整 PII 搜尋值（email/phone）；以「提供了哪些欄位」概要為主。

---

## 10. Error Code 對應

沿用 `infrastructure.md` §12.4，無新增 error code：

| error | HTTP | 場景 |
|---|---|---|
| `invalid input` | 400 | 無搜尋條件、`player_id`/`id` 非 UUID、`limit` 超範圍、`cursor` 解析失敗 |
| `forbidden` | 403 | 非 CMS（`utype != cms`）|
| `resource not found` | 404 | 詳情查詢玩家不存在 / 已軟刪除 |
| `too many requests` | 429 | 限流 |

> 前端 BFF 容忍 `resource not found` 與 `resource_not_found` 兩種寫法（`05 §4`）；
> 後端依既有慣例輸出空格形式。

---

## 11. 實作 Checklist

**Schema（SDD 第一步）**：
- [ ] §8 OpenAPI 片段展開並貼入 `schema/openapi.yaml`（含 `PlayerDTO` / `PlayerSearchResult` schema）

**後端**：
- [ ] `internal/dto/player_dto.go`（`PlayerDTO` + `PlayerSearchResult` + 角色遮罩 helper）
- [ ] `internal/service/player_service.go`（`PlayerService`：keyset cursor 解碼 / 編碼、`filter.Limit=pageSize+1` → hasMore → 砍尾、audit）
- [ ] `internal/service/player_service_test.go`（unit：AND 組合、空條件防呆、keyset cursor 編解碼往返、hasMore 邊界、cursor 解碼失敗→ErrInvalidInput、not found）
- [ ] `internal/handler/player_handler.go`（`Search` / `Get`；query 解析與正規化：trim / NFC（`norm.NFC`）/ lowercase / phone 正規化、空字串視為未提供、maxLength 驗證、UUID 驗證、依 `claims.role` 遮罩）
- [ ] `internal/handler/player_handler_test.go`（E2E httptest：happy path、viewer 遮罩、admin 不遮罩、**僅 email/phone 遮罩其餘不遮**、空字串參數視為未提供、全空條件 → 400、超 maxLength → 400、非法 cursor → 400、404、403 非 CMS）
- [ ] `cmd/server/main.go`：wire `PlayerService` / `PlayerHandler`，共用 `cmsGroup`
- [ ] model / repository 變更見 `players-model.md` §11

**測試重點**：
- [ ] 搜尋：單條件、多條件 AND、display_name 前綴、email 前綴（lowercase）、phone 正規化、軟刪除排除
- [ ] 分頁（keyset）：`pageSize+1` 計算 `next_cursor`、最後一頁回 null、`cursor` 往返翻頁**不重不漏**（含翻頁間插入新列）、非法 cursor → 400、`limit` 超界 → 400
- [ ] 遮罩：viewer email/phone 遮罩格式正確、null 不遮罩、admin/user 完整值、搜尋條件仍以原值比對
- [ ] 權限：member token → 403、未登入 → 401
- [ ] Audit：`players.search` 記 result_count 且不洩漏 PII、`players.read` 記 target_player_id
