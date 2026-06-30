# 玩家儲值彙總（Player Deposit Summary）— API 規格書

版本：v1.2
日期：2026-06-30
狀態：**契約已定案，後端實作待排程**（OpenAPI 已定義；handler / service / repository 尚未實作）

> v1.2：第二輪 review（對照原始碼）——
> §3.4 / §4 `lifetime_days` 定案為 **UTC 日曆日差**並改於 SQL 算（`AT TIME ZONE 'UTC'`，避免 session 時區誤差）；
> §4 改回直白兩查詢 + 唯讀交易（去除過度設計的 `json_agg`）；
> §8 修正：**移除自訂 metric**（後端 players.search/read 並無 per-view counter，僅靠 Gin HTTP 指標 + audit）、
> 稽核改用**型別化事件** `EventPlayerDepositSummary`（與 `EventPlayerRead` 同形）；修正 §5 範例退款率 0.0461 → 0.0462。
>
> v1.1：最佳實踐 review 修正——
> 端點命名由 `topup-summary` 改為 **`deposit-summary`**（對齊後端 `deposit-records` / `DepositRecord` 房規）；
> §3.6 補軟刪除玩家可見性、§4 改**單一往返 + 快照一致性**、§4.3 補退款率捨入模式、§8 新增可觀測性與稽核。
>
> v1.0：首版。新增 CMS 玩家儲值彙總端點，供前端「玩家詳情頁」儲值彙總卡使用，取代前端 mock。

範圍：玩家層級的 `deposit_records` 聚合查詢（CMS 管理端，唯讀）
對應規格：`players-api.md`、`deposit-records-model.md`、`deposit-records-api.md`、`infrastructure.md` §10/§12/§18
前端對應：`PlayerLedgerFrontend/docs/specs/06-topup-records-domain.md §7`、`09-screen-player-detail.md §4.3`
（前端以「topup / 儲值」為產品用語，BFF 函式 `getPlayerTopupSummary` 呼叫本後端 `deposit-summary` 端點——
與既有 `listDeposits` → `/cms/deposit-records` 同樣的 topup↔deposit 對映模式。）

> **欄位命名取捨（刻意）**：API 介面識別子（路徑 / operationId / schema 名）一律用 `deposit` 房規；
> 但回應欄位 `first_topup_at` / `last_topup_at` 保留 `topup` 字樣，使前端**機械式 snake→camel** 轉換
> 直接得到 `firstTopupAt` / `lastTopupAt`（前端產品用語），避免在 transform 之外多一層語意改名。
> 其餘欄位（`completed_*` / `refunded_*` / `failed_*` / `refund_rate` / `lifetime_days`）皆為 status / 中性詞，無此議題。

---

## 1. 設計原則

- **SDD**：端點先在 `schema/openapi.yaml` 定義（已完成），再寫 handler 與測試。
- **命名對齊房規**：後端資源一律用 `deposit`（`deposit-records` / `DepositRecord` / `createDepositRecord`），故本端點為 `deposit-summary`、operationId `getPlayerDepositSummary`、schema `PlayerDepositSummary`。「topup」僅為前端產品用語。
- **回應 envelope**：成功走 `Response<T>`（`OK()`，**無 `meta`**，非分頁）；錯誤走共用 `ErrorResponse`。
- **彙總在 DB 層完成**：以聚合 SQL（`GROUP BY currency`）計算，不把整批明細拉到應用層再算，避免大玩家 N+1 / 記憶體壓力。
- **金額為整數最小貨幣單位**：沿用 `deposit_records.amount` 的 int64 最小單位，**禁止** float。
- **退款率後端算好**：`refund_rate` 由後端以整數金額計算後輸出 double，前端不在 client 做除法（避免浮點誤差與口徑分歧）。
- **不含 PII**：彙總僅金額與計數，無 email / phone / 玩家姓名，故**不分角色遮罩**，viewer 亦回完整值。

---

## 2. 端點

```
GET /api/cms/players/{id}/deposit-summary        # 全 CMS staff（admin / user / viewer）
```

- **資源設計**：彙總為**單一玩家**範疇的唯讀資料，巢狀於 `players`（與同頁 `GET /cms/players/{id}` 一致）。
  與「跨玩家扁平資源」`GET /cms/deposit-records?player_id=`（列表）為**不同用途**：列表回明細列，彙總回統計。
- **OpenAPI tags**：`[players, deposit-records]`（橫跨兩域：路徑屬 players、資料源屬 deposit-records）。
- **路徑參數**：`id`（UUID）= `members.id` = `deposit_records.player_id`。
- **權限**：`claims.utype == cms`（由 `cmsGroup` 中介層保證），全 CMS staff 可讀；彙總無 PII，viewer 不受限、不遮罩。無需 `RequireRole`。
- **路由註冊建議**：`cmsGroup.GET("/players/:id/deposit-summary", playerHandler.DepositSummary)`，
  置於 `cmsGroup.GET("/players/:id", ...)` 之後（Gin 同前綴不同尾段，無路由衝突）。

---

## 3. 聚合語意（精確定義）

設目標玩家為 `P`（`deposit_records.player_id = P`）。

### 3.1 分桶（依當前 `status`，互斥）

每筆紀錄依其**當前 status** 歸入單一桶；同一筆不重複計入。

| 桶 | 條件 | 計入欄位 |
|----|------|---------|
| completed | `status = 'completed'` | `completed_count`、`completed_amount` |
| refunded | `status = 'refunded'` | `refunded_count`、`refunded_amount` |
| failed | `status = 'failed'` | `failed_count` |
| （不計入） | `status IN ('pending','cancelled')` | 不出現在 `totals_by_currency` 統計 |

> **為何 completed 與 refunded 互斥**：狀態機為 `completed → refunded`（單向終態，見 `deposit-records-model.md §4`）。
> 一筆退款紀錄當前 status 為 `refunded`，故計入退款桶、**不**重複計入成功桶。
> 「曾經成功的總額」= `completed_amount + refunded_amount`（退款率分母即此）。

### 3.2 幣別分組

- 依 `currency` `GROUP BY`，每個**含 completed / refunded / failed 任一紀錄**的幣別輸出一筆 `CurrencyTotals`。
- 僅有 `pending` / `cancelled` 的幣別**不輸出**（無可彙總統計）。
- `totals_by_currency` 依 `currency` **ASC** 排序（輸出穩定，前端 UI 不跳動）。
- 目前 DB CHECK 僅允許 `TWD`，實務上 ≤ 1 筆；多幣別開放後此設計自然擴展。

### 3.3 退款率

```
refund_rate(currency) = refunded_amount / (completed_amount + refunded_amount)
```

- **以整數金額計算**（非筆數），再輸出 double。
- 分母為 0（該幣別無 completed 也無 refunded，例如只有 failed）→ `refund_rate = 0`。
- 四捨五入至**小數 4 位**，捨入模式 **round half away from zero**（前端以 `(refund_rate*100).toFixed(2)` 顯示，4 位足夠穩定）。

### 3.4 首末次儲值與生涯天數

「儲值」採**成功口徑**：僅 completed ∪ refunded 紀錄視為一次儲值事件（pending / failed / cancelled 不算）。

- `first_topup_at` = `MIN(created_at)`，`last_topup_at` = `MAX(created_at)`，範圍為 `status IN ('completed','refunded')`。
- 無任何成功紀錄 → 兩者皆 `null`。
- `lifetime_days` = `DATE(last_topup_at) − DATE(first_topup_at)`（**UTC 日曆日差**，PM 定案 2026-06-30）：
  - 無成功紀錄 → `null`；同一 UTC 日期（含單筆）→ `0`；隔日 → `1`；以此類推
  - 跨幣別**合併**計算（生涯天數是玩家層級，不分幣別）

> **為何取 `created_at` 而非退款時間**：`created_at` 是該筆儲值「發生」的時間；退款（`updated_at`）是後續事件。
> 生涯定義為「玩家實際儲值的時間跨度」，故用儲值發生時間。
> **為何用首末次之間而非至今**：可重現、不受查詢時間影響（PM 決議，2026-06-30）。
> **為何用日曆日差而非 ceil/floor 原始時長**：「首次到末次儲值跨幾天」對運營最直觀（同日 = 0、隔日 = 1），
> 不受同日多筆的小時數影響。UTC 計算（與時間欄位口徑一致）；近午夜時可能與使用者本地時區差一天，可接受。

### 3.5 空狀態

玩家存在（`members` 有此 id 且未軟刪）但無任何可彙總紀錄 → `200`：

```json
{ "player_id": "...", "totals_by_currency": [], "first_topup_at": null, "last_topup_at": null, "lifetime_days": null }
```

### 3.6 玩家存在性與可見性

- 聚合查詢回 0 列**不**代表玩家不存在（可能只是沒儲值），故**必須**另確認 `player_id` 存在。
- 可見性與 `GET /cms/players/{id}` **一致**：玩家不存在**或已軟刪除**（`members.deleted_at IS NOT NULL`）→ `404 resource not found`。
  避免「詳情頁 404、彙總卻回 200」的不一致。
- 建議：先做 members 存在性 / 未軟刪查詢，再跑聚合（見 §4）。

---

## 4. 參考聚合查詢

於**同一唯讀交易**（GORM `db.Transaction(func(tx)...)`，`READ ONLY`）內跑兩條直白的聚合查詢，
確保各幣別統計與首末次時間取自**同一快照**（避免兩查詢之間有寫入造成不一致）。回 grouped rows 直接 scan，
比 `json_agg`/單語句更貼合 GORM `Raw().Scan()` 慣例。

```sql
-- (1) per-currency 分桶統計
SELECT
  currency,
  COUNT(*)                          FILTER (WHERE status = 'completed') AS completed_count,
  COALESCE(SUM(amount) FILTER (WHERE status = 'completed'), 0)          AS completed_amount,
  COUNT(*)                          FILTER (WHERE status = 'refunded')  AS refunded_count,
  COALESCE(SUM(amount) FILTER (WHERE status = 'refunded'), 0)           AS refunded_amount,
  COUNT(*)                          FILTER (WHERE status = 'failed')    AS failed_count
FROM deposit_records
WHERE player_id = $1
  AND status IN ('completed', 'refunded', 'failed')
GROUP BY currency
ORDER BY currency ASC;

-- (2) 玩家層級首末次（成功口徑）＋ 生涯天數（UTC 日曆日差）
SELECT
  MIN(created_at) AS first_topup_at,
  MAX(created_at) AS last_topup_at,
  ( (MAX(created_at) AT TIME ZONE 'UTC')::date
  - (MIN(created_at) AT TIME ZONE 'UTC')::date ) AS lifetime_days
FROM deposit_records
WHERE player_id = $1
  AND status IN ('completed', 'refunded');
```

- **`lifetime_days` 在 SQL 算完**：`(max::date − min::date)` 回整數天。**務必 `AT TIME ZONE 'UTC'`**——`timestamptz::date`
  會用 session 時區轉換（本專案 DSN 未固定 TimeZone，見 `pkg/database`），不加會得到非 UTC 日期、與決議口徑不符。
  無成功紀錄時 MIN/MAX 為 NULL → `lifetime_days` = `null`。
- **`refund_rate` 在 service 算**：以查詢(1) 的整數金額計算（DB 不做浮點除法，口徑集中於 Go）。
- **玩家存在性**（§3.6）：先用既有、soft-delete-aware 的 `memberRepo`（同 `GET /cms/players/{id}` 路徑）確認玩家存在，
  不存在 / 軟刪 → 404；再跑上述聚合。三者可同置一個唯讀交易。
- **溢位**：`SUM(amount)` 在 Postgres 回 `numeric`；單一玩家金額加總遠不及 int64 上限，service 轉 int64 安全。
- **索引**：現有 `idx_deposit_records_player_created (player_id, created_at DESC)` 已覆蓋 `player_id` 過濾；
  單玩家紀錄量有限，聚合掃描成本可接受。單玩家紀錄量大時可評估 `(player_id, currency, status)` 部分索引。

---

## 5. 回應 schema（已定義於 `schema/openapi.yaml`）

```yaml
PlayerDepositSummary:
  required: [player_id, totals_by_currency, first_topup_at, last_topup_at, lifetime_days]
  properties:
    player_id:          { type: string, format: uuid }
    totals_by_currency: { type: array, items: { $ref: CurrencyTotals } }
    first_topup_at:     { type: [string, 'null'], format: date-time }
    last_topup_at:      { type: [string, 'null'], format: date-time }
    lifetime_days:      { type: [integer, 'null'], minimum: 0 }

CurrencyTotals:
  required: [currency, completed_count, completed_amount, refunded_count, refunded_amount, failed_count, refund_rate]
  properties:
    currency:         { type: string }
    completed_count:  { type: integer, format: int64, minimum: 0 }
    completed_amount: { type: integer, format: int64, minimum: 0 }
    refunded_count:   { type: integer, format: int64, minimum: 0 }
    refunded_amount:  { type: integer, format: int64, minimum: 0 }
    failed_count:     { type: integer, format: int64, minimum: 0 }
    refund_rate:      { type: number,  format: double, minimum: 0, maximum: 1 }
```

成功回應範例（200）：

```json
{
  "success": true,
  "request_id": "0193b3f4-1234-7abc-9def-0123456789ab",
  "data": {
    "player_id": "0193b3f4-1234-7abc-9def-000000000002",
    "totals_by_currency": [
      { "currency": "TWD", "completed_count": 12, "completed_amount": 24800,
        "refunded_count": 1, "refunded_amount": 1200, "failed_count": 2, "refund_rate": 0.0462 }
    ],
    "first_topup_at": "2026-01-04T09:12:00Z",
    "last_topup_at": "2026-06-20T03:11:22Z",
    "lifetime_days": 167
  }
}
```

時間欄位一律 RFC3339 **UTC**（與既有 DTO 慣例一致，輸出 `Z`；見 player / cms_user DTO 的 `.UTC().Format`）。

---

## 6. 錯誤

| 條件 | HTTP | error |
|------|------|-------|
| `id` 非 UUID 格式 | 400 | `invalid input` |
| 未帶 / 無效 access token | 401 | `unauthorized` |
| 非 CMS staff（`utype != cms`） | 403 | `forbidden` |
| `player_id` 不存在 / 已軟刪除 | 404 | `resource not found` |
| 限流 | 429 | `too many requests`（含 `Retry-After`） |

- 沿用 `infrastructure.md` §12.4 / 既有 `error_handler.go` sentinel 映射，**無新增 error code**。

---

## 7. 實作分層（待實作；TDD）

| 層 | 檔案（建議） | 職責 |
|----|------|------|
| handler | `internal/handler/player_handler.go`（新增 `DepositSummary`） | 解析並驗證 `id`（UUID）；呼叫 service；`OK()` 輸出 DTO；錯誤交 `HandleError` |
| service | `internal/service/player_service.go`（新增 `DepositSummary`） | 確認玩家存在且未軟刪（否則 `ErrNotFound`）；呼叫 repo 聚合；計算 `refund_rate`；組 DTO；寫 audit（§8） |
| repository | `internal/repository/deposit_record_repository.go`（新增 `AggregateByPlayer`） | 於唯讀交易跑 §4 兩條查詢，回 `[]CurrencyAggregate` 與 `(firstTopupAt, lastTopupAt, lifetimeDays)`（`lifetime_days` 由 SQL UTC 日曆日差算出） |
| dto | `internal/dto/deposit_summary_dto.go`（新） | `PlayerDepositSummaryDTO` / `CurrencyTotalsDTO`；顯式 null、時間 `.UTC().Format` |

> 聚合放 `deposit_record_repository`（資料源為 `deposit_records`），對外端點掛在 player 路由 + `player_service`
> 編排（玩家存在性 + 統計）。`player_service` 需能存取 deposit 聚合 repo（建構子注入）。

### 7.1 測試清單（TDD）

**repository（整合，真實 DB）— `deposit_record_repository_test.go`**

```
should aggregate completed/refunded/failed counts and amounts grouped by currency
should exclude pending and cancelled from totals
should return empty result when player has no completed/refunded/failed records
should compute first/last topup over completed ∪ refunded created_at only
should return nil first/last when no successful records
should order totals_by_currency by currency ASC
should read aggregation and first/last within a single consistent snapshot
```

**service（單元，fake repo）— `player_service_test.go`**

```
should return 404 (ErrNotFound) when player_id not in members
should return 404 when player is soft-deleted
should compute refund_rate = refunded_amount/(completed+refunded) per currency
should return refund_rate=0 when denominator is 0 (only failed records)
should round refund_rate to 4 decimal places (half away from zero)
should compute lifetime_days as UTC calendar-day diff DATE(last)-DATE(first); 0 for same day; null for none
should return empty summary (200) when player exists but has no records
should write a players.deposit_summary audit log (actor + target_player_id, no amounts) on success
should not write audit log when player not found
```

**handler（e2e，httptest）— `player_handler_test.go`**

```
should return 200 with PlayerDepositSummary envelope (no meta)
should return 400 when id is not a valid UUID
should return 404 when service reports player not found
should allow viewer role (no masking, full values)
should return 401 when unauthenticated
```

---

## 8. 可觀測性與稽核

- **Metric（沿用既有，無自訂 counter）**：HTTP 層指標由 `metrics.GinMiddleware()` 依 route template 自動產生
  （含 status code）。**不**新增 per-view 自訂 counter——與 `players.search` / `players.read` 一致（兩者亦僅靠
  Gin HTTP 指標 + audit，無業務 counter）。專案的自訂 metric 僅保留給安全事件（如 `AuthReplayDetected`），彙總讀取不屬此類。
- **稽核（定案：記錄，2026-06-30）**：彙總為**財務聚合資料的讀取**，service 於成功時寫一筆 audit，
  與 `EventPlayerRead`（`players.read`）**完全同形**：
  - 新增事件型別 `EventPlayerDepositSummary EventType = "players.deposit_summary"`（`pkg/audit/audit.go`，置於 `EventPlayerSearch` / `EventPlayerRead` 旁）
  - `s.audit.Log(ctx, audit.AuthEvent{ Type: audit.EventPlayerDepositSummary, UserID: actor.UserID, Extra: {"role": actor.Role, "target_player_id": id} })`
  - **僅記識別碼**（actor / target_player_id / role），**不**記金額或統計明細。
- **Redact**：金額 / 統計值非個資，不 redact；log 不得輸出 access token。

---

## 9. 開放問題

- [x] **生涯天數捨入**：~~待定~~ → **UTC 日曆日差** `DATE(last) − DATE(first)`，同日 = 0（PM 定案 2026-06-30，§3.4）。
- [x] **彙總讀取是否稽核**：~~待定~~ → **記錄 audit log**（PM 定案 2026-06-30，§8）。
- [x] **退款率口徑**：金額比（決議，§3.3）；風控若另需「筆數退款率」可再加欄位（不取代金額比）。
- [x] **多幣別生涯天數**：跨幣別合併（決議，§3.4）；若要求「分幣別生涯」需把欄位移入 `CurrencyTotals`。
- [ ] **是否納入 `pending` / `cancelled` 統計**：目前不輸出；客服若需「待處理筆數」可加 `pending_count`（不影響成功/退款口徑）。
- [ ] **快取**：讀多寫少；詳情頁流量高時可評估短 TTL 快取（key = player_id），須處理建立 / 退款後失效。v1 不做。
