# 玩家查詢（Players）— 資料模型規格書

版本：v1.0
日期：2026-06-29

> v1.2：補齊精度缺口——§5 email / phone 比對語意由「待確認」改為**定案**
> （email 前綴 / phone 精確，附 pg_trgm 替代方案）、新增搜尋條件「空字串視為未提供」規則。
>
> v1.1：最佳實踐修正——
> §4/§5 `display_name` 前綴索引改 `lower(...) text_pattern_ops` 配 `LIKE`（原 `ILIKE` 無法用 `text_pattern_ops` 索引）；
> §6 cursor 由 offset 編碼改為 **keyset**（`created_at, id`），消除並發插入下的重複 / 漏列；
> §8 釐清 `limit+1` 由 service 負責（repository 忠實照 `Limit` 查）；
> §10 補 migration 鎖與資料改寫的注意事項；§5 補 phone canonical E.164 假設。
>
> v1.0：首版。為支援 CMS 玩家查詢（搜尋 + 詳情）擴充 `members` 表，
> 新增 `external_id`、`display_name`、`email`、`phone`、`status`、`last_active_at`
> 六個欄位與 `member_status` enum；新增搜尋用 index 與 `MemberRepository.Search`。
> 對應前端規格 `PlayerLedgerFrontend/docs/specs/05-player-query-domain.md`、`08`、`09`。

範圍：`members` 資料表的欄位擴充、`member_status` enum、搜尋 index、Go model、repository interface
對應規格：`players-api.md`、`deposit-records-model.md`（FK `player_id → members.id`）、`infrastructure.md` §12/§16

---

## 1. 設計原則

- **沿用既有 `members` 表**：玩家查詢操作的對象就是現有 `members`（會員）表。
  本規格以**擴充欄位**方式補齊前端查詢所需資料，不另開 `member_profiles` 表，
  使搜尋與詳情查詢保持單表、免 JOIN（決策見 §9 落差說明）。
- **欄位漸進擴充、向後相容**：新增欄位皆 nullable 或帶 DEFAULT，既有列（seed/註冊產生）
  不需停機即可升級；`display_name` 以 `username` 回填後再設 NOT NULL。
- **查詢欄位與機密欄位分離**：`password_hash` 永不出現在任何查詢 DTO；
  `email`、`phone` 為 PII，遮罩規則由 **API 層依角色**處理（見 `players-api.md` §5），
  model / repository 一律回傳原始值。
- **狀態於 DB 層強制**：`status` 使用 `member_status` enum，於 DB 層強制合法值。
- **`registered_at` 不新增欄位**：直接沿用既有 `created_at`，由 DTO 映射為 `registered_at`，
  避免重複語意欄位。
- **`last_active_at` 暫無寫入來源**：本期僅建立欄位（nullable，預設 NULL），
  待未來導入玩家活動追蹤（登入 / 下注事件）後再回填；查詢端容忍 null（見 §9）。
- **軟刪除一致性**：沿用 `members.deleted_at`；搜尋與詳情一律加 `deleted_at IS NULL` 條件，
  已刪除玩家不出現在任何查詢結果。
- **金額 / 帳務無關**：本表不涉及金流；玩家的儲值彙總與紀錄由 `deposit_records` 提供
  （見 `deposit-records-model.md`）。

---

## 2. Enum 定義

### 2.1 `member_status`

| 值 | 說明 |
|---|---|
| `active` | 正常帳號（預設值；新建與既有玩家皆為此狀態）|
| `frozen` | 凍結（暫停存取，可復原）|
| `closed` | 關閉（永久停用）|

> 本期 **不提供** 變更 `status` 的 API 端點（玩家查詢為唯讀，見 `players-api.md` §2）。
> `status` 一律為 DEFAULT `active`；凍結 / 關閉的操作端點與狀態機屬未來範圍。
> 前端 `PlayerStatus` 對應同三值（`active` / `frozen` / `closed`），可直接 render。

---

## 3. 資料表 Schema（擴充後）

```sql
-- members 表擴充後完整樣貌（粗體為 v1.0 新增欄位）
CREATE TABLE members (
    id            UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64)   NOT NULL,
    password_hash VARCHAR(72)   NOT NULL,
    external_id   VARCHAR(64),                                   -- 新增：外部遊戲系統識別碼
    display_name  VARCHAR(64)   NOT NULL,                        -- 新增：顯示暱稱（回填自 username 後設 NOT NULL）
    email         VARCHAR(255),                                  -- 新增：電子郵件（PII，API 層依角色遮罩）
    phone         VARCHAR(32),                                   -- 新增：電話 E.164（PII，API 層依角色遮罩）
    status        member_status NOT NULL DEFAULT 'active',       -- 新增：帳號狀態
    last_active_at TIMESTAMPTZ,                                  -- 新增：最後活動時間（本期無寫入來源，預設 NULL）
    created_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
```

### 3.1 新增欄位說明

| 欄位 | 型別 | 限制 | 說明 |
|---|---|---|---|
| `external_id` | VARCHAR(64) | nullable, unique（partial）| 外部遊戲系統的玩家識別碼；搜尋採**精確比對** |
| `display_name` | VARCHAR(64) | NOT NULL | 顯示暱稱；搜尋採**前綴模糊**（NFC 正規化）；migration 以 `username` 回填 |
| `email` | VARCHAR(255) | nullable | PII；搜尋採**前綴比對**（lowercase）；API 對 user / viewer 遮罩（僅 admin 完整）|
| `phone` | VARCHAR(32) | nullable | E.164 格式；搜尋採**正規化後精確比對**；API 對 user / viewer 遮罩（僅 admin 完整）|
| `status` | member_status | NOT NULL, DEFAULT `active` | 帳號狀態；見 §2.1 |
| `last_active_at` | TIMESTAMPTZ | nullable | 最後活動時間；本期恆為 NULL，待未來事件回填 |

> 既有欄位 `id`、`username`、`created_at`、`deleted_at` 在查詢 DTO 中分別對應
> `player_id`、（不外露）、`registered_at`、（過濾條件）。`username` 為登入帳號，
> **不**在玩家查詢 DTO 中輸出（前端以 `display_name` 呈現）。

---

## 4. Index 策略

```sql
-- external_id 精確查詢 + 唯一性（僅在有值且未刪除時）
CREATE UNIQUE INDEX uq_members_external_id
    ON members (external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;

-- display_name 大小寫不敏感前綴查詢：以 lower() 函式索引支援 LIKE 'prefix%' 前綴掃描。
-- 注意：text_pattern_ops 僅支援 LIKE（大小寫敏感），不支援 ILIKE；故兩側皆 lower() 後用 LIKE。
CREATE INDEX idx_members_display_name_lower
    ON members (lower(display_name) text_pattern_ops)
    WHERE deleted_at IS NULL;

-- email 前綴查詢（同理：lower(email) 函式索引 + LIKE）
CREATE INDEX idx_members_email_lower
    ON members (lower(email) text_pattern_ops)
    WHERE email IS NOT NULL AND deleted_at IS NULL;

-- phone 正規化後精確查詢
CREATE INDEX idx_members_phone
    ON members (phone)
    WHERE phone IS NOT NULL AND deleted_at IS NULL;
```

> `player_id`（= `id`）查詢走 PK，無需額外 index。
> 搜尋一律附帶 `deleted_at IS NULL`（GORM 對 `gorm.DeletedAt` 自動加入），predicate 與 partial index
> 條件相符，PostgreSQL 可使用上述 partial index。
> **排序 / keyset**：搜尋固定 `ORDER BY created_at DESC, id DESC`（見 §6）。實務上各搜尋條件
> （exact `external_id` / `player_id`、prefix `display_name`）已大幅縮小結果集，排序成本低，
> 故不另為 `(created_at, id)` 建複合 index；若未來無條件全表瀏覽成為需求再評估。

---

## 5. 搜尋語意（repository 實作依據）

對應前端 `05-player-query-domain.md` §4：

| 條件欄位 | 比對方式 | 正規化 | SQL 片段（示意）|
|---|---|---|---|
| `player_id` | 精確 | trim | `id = $1`（先 parse UUID，格式錯回 400）|
| `external_id` | 精確 | trim | `external_id = $1` |
| `display_name` | 前綴 | trim + NFC | `lower(display_name) LIKE lower($1) \|\| '%'` |
| `email` | 前綴 | trim + lowercase | `lower(email) LIKE $1 \|\| '%'` |
| `phone` | 精確 | 去除空白 / `-` / `(` / `)` | `phone = $1` |

> **email / phone 比對語意（定案）**：採 **email 前綴 / phone 精確**，據此選 btree 前綴索引（§4）。
> 理由與替代方案見 `players-api.md` §4.1（若產品確需 email 子字串搜尋，改 `pg_trgm` 擴充 +
> GIN 索引 `USING gin (lower(email) gin_trgm_ops)`，本 model 其餘不變；見 §10 備註）。
>
> - **phone canonical 假設**：精確比對要求 `members.phone` 儲存為 canonical **E.164**
>   （`+886912345678`）。使用者輸入本地格式（`0912...`）不會命中；如需容錯，
>   應在寫入端統一正規化為 E.164。

- **空字串視為未提供**：handler 將任一搜尋條件正規化後為空字串者忽略（不傳入 filter）；
  故 repository 收到的 `PlayerSearchFilter` 各條件非 nil 即代表「有效且已正規化」（見 `players-api.md` §4.1）。
- **組合邏輯**：所有「有提供」的條件以 **AND** 串接；至少需提供一個有效條件
  （全空由 API 層回 `400 invalid input`，repository 不接受空 filter）。
- **排序**：固定 `created_at DESC, id DESC`（最新註冊優先；`id` 為 tie-breaker，供 keyset 分頁穩定）；本期不開放 `sort` 控制。
- **分頁**：keyset cursor（見 §6）。

---

## 6. Cursor 分頁機制（keyset）

前端契約使用 opaque cursor（`cursor` 進、`next_cursor` 出），與 `deposit_records`
的 `page/page_size` 不同（決策見 `players-api.md` §1）。本期採 **keyset（seek）分頁**，
而非 offset——因結果按 `created_at DESC` 排序，新玩家註冊會插入最前，offset 分頁在並發下
會造成跨頁**重複 / 漏列**；keyset 以「上一頁最後一列的位置」續查，分頁穩定且無 deep-offset 成本。

- **cursor 內容**：編碼上一頁最後一列的 keyset 位置 `(created_at, id)`，
  以 `base64url(JSON{"t": <created_at RFC3339Nano>, "id": <uuid>})` 表示（對前端 opaque）。
- **續查條件**（`ORDER BY created_at DESC, id DESC`）：
  ```sql
  WHERE <filter conditions>
    AND ( $cursor IS NULL
          OR (created_at, id) < ($cursor_created_at, $cursor_id) )  -- row-value 比較，配合 DESC
  ORDER BY created_at DESC, id DESC
  LIMIT $limit
  ```
  缺省 cursor → 從第一頁開始（無 keyset 條件）。
- **是否有下一頁**：由 **service** 將 `Limit` 設為 `pageSize + 1` 傳入 repository（repository
  忠實照 `Limit` 查，見 §8）；service 取回後若筆數 > `pageSize` → 砍到 `pageSize` 筆，
  以「保留的最後一列」之 `(created_at, id)` 編碼為 `next_cursor`；否則 `next_cursor = nil`（最後一頁）。
- `pageSize` 範圍 `1..50`，預設 `20`（超出由 API 層回 400）。
- cursor 解析失敗（非合法 base64 / JSON / 欄位缺漏）由 API 層回 `400 invalid input`。

> keyset 對前端維持 opaque cursor 契約不變。`(created_at, id)` 的 `id` 為 tie-breaker，
> 確保同一 `created_at` 的多列也有全序、不漏不重。

---

## 7. Go Model（擴充後）

```go
package model

import "time"

type MemberStatus string

const (
    MemberStatusActive MemberStatus = "active"
    MemberStatusFrozen MemberStatus = "frozen"
    MemberStatusClosed MemberStatus = "closed"
)

// Member 一般玩家（会员）。v1.0 擴充玩家查詢所需的 profile 欄位。
type Member struct {
    Base
    Username     string `gorm:"size:64;not null;uniqueIndex:uq_members_username,where:deleted_at IS NULL"`
    PasswordHash string `gorm:"size:72;not null"`

    ExternalID   *string      `gorm:"size:64"`
    DisplayName  string       `gorm:"size:64;not null"`
    Email        *string      `gorm:"size:255"`
    Phone        *string      `gorm:"size:32"`
    Status       MemberStatus `gorm:"type:member_status;not null;default:active"`
    LastActiveAt *time.Time   `gorm:""`
}

func (Member) TableName() string {
    return "members"
}
```

> `Base` 已含 `ID`、`CreatedAt`、`UpdatedAt`、`DeletedAt`（見 `internal/model/base.go`）。
> `CreatedAt` 即 DTO 的 `registered_at` 來源。

---

## 8. Repository Interface（擴充）

於既有 `MemberRepository`（`FindByUsername` / `FindByID`）新增 `Search`：

```go
package repository

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/yintengching/playerledger/internal/model"
)

// Keyset 表示 keyset 分頁的續查位置（上一頁最後一列）。
type Keyset struct {
    CreatedAt time.Time
    ID        uuid.UUID
}

// PlayerSearchFilter 玩家搜尋條件。
// 所有指標欄位 nil 表示「未提供該條件」；至少需一個搜尋條件非 nil（API 層保證，repository 不接受全空）。
// 各字串欄位已由 API 層完成 trim / lowercase / 正規化（見 model spec §5），repository 直接使用。
type PlayerSearchFilter struct {
    PlayerID          *uuid.UUID // 精確
    ExternalID        *string    // 精確
    DisplayNamePrefix *string    // 前綴（lower + LIKE）
    EmailPrefix       *string    // 前綴（已 lowercase）
    Phone             *string    // 精確（已正規化）

    After *Keyset // nil = 第一頁；非 nil = 續查 (created_at, id) < After（見 §6）
    Limit int     // repository 忠實照此值；service 已設為 pageSize+1 以判斷 hasMore
}

type MemberRepository interface {
    FindByUsername(ctx context.Context, username string) (*model.Member, error)
    FindByID(ctx context.Context, id uuid.UUID) (*model.Member, error)

    // Search 依 filter 查詢：AND 組合、ORDER BY created_at DESC, id DESC、deleted_at IS NULL、
    // 套用 After keyset 條件、LIMIT filter.Limit。repository 不感知分頁語意，
    // 僅忠實回傳 ≤ filter.Limit 筆；hasMore 判斷與 next_cursor 編碼由 service 處理（見 players-api.md §6）。
    Search(ctx context.Context, f PlayerSearchFilter) ([]*model.Member, error)
}
```

> `FindByID` 已存在且已含 `deleted_at IS NULL` 條件，玩家詳情直接複用。
> `FakeMemberRepository` 須同步補 `Search` 實作以支援 handler / service 測試（見 §10 checklist）。

---

## 9. 與前端契約的落差說明

前端 `Player` 模型（`05 §2.1`）與後端 `members` 既有欄位的對照與處置：

| 前端欄位 | 後端來源 | 處置 |
|---|---|---|
| `playerId` | `members.id` | 直接映射 |
| `externalId` | `members.external_id` | **本期新增欄位** |
| `displayName` | `members.display_name` | **本期新增欄位**，migration 以 `username` 回填 |
| `email` | `members.email` | **本期新增欄位**，API 依角色遮罩 |
| `phone` | `members.phone` | **本期新增欄位**，API 依角色遮罩 |
| `status` | `members.status` | **本期新增欄位**，DEFAULT `active`；無變更端點 |
| `registeredAt` | `members.created_at` | 既有欄位映射 |
| `lastActiveAt` | `members.last_active_at` | **本期新增欄位**，恆為 NULL（無寫入來源，待未來回填）|

> 決策（經確認）：採「擴充 `members` 表」而非另開 profile 表；
> `status` / `last_active_at` 採「預設值 + 預留」策略，先讓前端可正常 render，
> 寫入來源後續補。

---

## 10. Migration

```sql
-- migrations/000004_extend_members_player_profile.up.sql

-- 鎖與資料改寫注意事項（目前 members 資料量小，以下皆可接受）：
--  1. ADD COLUMN ... DEFAULT 'active'：PostgreSQL 11+ 為 metadata-only，快速、不改寫整表。
--  2. UPDATE 回填 display_name 會改寫每一列；ALTER COLUMN SET NOT NULL 需全表掃描 + 短暫
--     ACCESS EXCLUSIVE 鎖。若未來 members 成長至大表，應改「分批回填 + 先加 NOT VALID CHECK
--     後 VALIDATE」的線上模式，避免長鎖。本期表小，直接執行。

CREATE TYPE member_status AS ENUM ('active', 'frozen', 'closed');

ALTER TABLE members
    ADD COLUMN external_id    VARCHAR(64),
    ADD COLUMN display_name   VARCHAR(64),
    ADD COLUMN email          VARCHAR(255),
    ADD COLUMN phone          VARCHAR(32),
    ADD COLUMN status         member_status NOT NULL DEFAULT 'active',
    ADD COLUMN last_active_at TIMESTAMPTZ;

-- display_name 回填既有列後設 NOT NULL（避免既有資料違反約束）
UPDATE members SET display_name = username WHERE display_name IS NULL;
ALTER TABLE members ALTER COLUMN display_name SET NOT NULL;

CREATE UNIQUE INDEX uq_members_external_id
    ON members (external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;

-- 大小寫不敏感前綴：lower() 函式索引 + text_pattern_ops（配 LIKE，非 ILIKE）
CREATE INDEX idx_members_display_name_lower
    ON members (lower(display_name) text_pattern_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_members_email_lower
    ON members (lower(email) text_pattern_ops)
    WHERE email IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX idx_members_phone
    ON members (phone)
    WHERE phone IS NOT NULL AND deleted_at IS NULL;
```

> 若 §5「email / phone 比對語意」最終定為**子字串搜尋**，本 migration 需改用
> `CREATE EXTENSION IF NOT EXISTS pg_trgm;` + GIN trigram 索引取代對應 btree 索引。

```sql
-- migrations/000004_extend_members_player_profile.down.sql

DROP INDEX IF EXISTS idx_members_phone;
DROP INDEX IF EXISTS idx_members_email_lower;
DROP INDEX IF EXISTS idx_members_display_name_lower;
DROP INDEX IF EXISTS uq_members_external_id;

ALTER TABLE members
    DROP COLUMN IF EXISTS last_active_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS phone,
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS external_id;

DROP TYPE IF EXISTS member_status;
```

---

## 11. 實作 Checklist

- [ ] 建立 migration（`000004_extend_members_player_profile.up/down.sql`）
- [ ] 擴充 `internal/model/member.go`（`MemberStatus` enum + 新欄位）
- [ ] 擴充 `internal/repository/member_repository.go`（`Keyset` + `PlayerSearchFilter` + `Search` GORM 實作；AND 組合、`ORDER BY created_at DESC, id DESC`、`After` keyset 條件、`LIMIT filter.Limit`、`deleted_at IS NULL`）
- [ ] 補 `FakeMemberRepository.Search`（測試用；需正確模擬 keyset 排序與 `After` 過濾）
- [ ] `internal/repository/member_repository_integration_test.go` 新增 `Search` 案例（真實 DB：各條件、AND 組合、lower 前綴、軟刪除排除、keyset 翻頁不重不漏、同 `created_at` 由 `id` tie-break）
- [ ] 更新 `cmd/seed`：產生玩家時**務必填入 `display_name`**（NOT NULL 無 DEFAULT），並填 `external_id` / `email`（canonical lowercase）/ `phone`（canonical E.164），供前端整合測試有資料可查
</invoke>
