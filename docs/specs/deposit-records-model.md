# 儲值紀錄（Deposit Records）— 資料模型規格書

版本：v1.6
日期：2026-06-29

> v1.6：補齊四項實作模糊點——
> §1 新增「並發更新策略」（last-write-wins）；
> §7 `Sort` 欄位補白名單映射說明（防止 SQL injection）；
> §7.5 新增 `ErrReferenceNoConflict` sentinel（pgconn code 23505 → 409）；
> §7.5 import 補 `"errors"`。
>
> v1.5：修正 §7.5 架構問題——新增 `UpdateDepositInput` 至 service 包，
> `DepositService.Update` 改接受 `UpdateDepositInput`（service 包），
> handler 無需 import repository 包；service 內部映射至 `repository.UpdateDepositInput`。
>
> v1.4：修正 §7 架構問題——`CreateDepositInput` 移至 service 包（§7.5），
> repository `Create` 改回接受 `*model.DepositRecord`；
> repository 只關心儲存，service 負責組裝 entity（查 player_name、填 operator 資訊）。
>
> v1.3：§1 補「金融不可刪除」設計決策；§3 `currency` 加 CHECK 白名單；
> §7 `UpdateDepositInput` 引入 `**string` 三態語意；新增 §7.5 Service Interface；
> §8 Migration 補 `updated_at` trigger 與 currency CHECK。
>
> v1.2：`amount` 釐清為幣別最小單位；新增 `cancelled` 終態；ON DELETE RESTRICT；
> Repository interface 補 `time` import。
>
> v1.1：`amount` 改 BIGINT/int64；`player_name` server 自動填入；`ip_address` 改 `operator_ip`；
> `note` 拆雙軌；`CanTransition` accessor；`UpdateStatus` 回傳 record；`DepositRecordFilter` 補 PaymentMethod；
> index 升級為 `(status, created_at DESC)`。

範圍：`deposit_records` 資料表的 schema、enum、index、狀態機、Go model、repository/service interface

---

## 1. 設計原則

- **金融不可變性**：紀錄一旦建立，`amount`、`currency`、`player_id`、`payment_method` 不允許修改；
  狀態變更走 `status` 欄位，確保帳務歷史可稽核。
- **金融不可刪除**：`deposit_records` **不設 `deleted_at`**，與 `cms_users`、`members` 的軟刪除模式不同。
  `cancelled` 是唯一的作廢路徑；帳務紀錄永久保存，確保對帳完整性。實作者不應仿照其他表加入軟刪除。
- **快照反正規化**：`player_name` 由 **server 建立時自動從 `members.display_name`（顯示暱稱）讀取**，儲存儲值當下的快照；
  caller 不得自行提供，防止偽造。採 `display_name` 而非 `username`（登入帳號），對齊 players-model §6「username 不外露、前端以 display_name 呈現」。
- **最小單位金額**：`amount` 以 `BIGINT` 儲存，單位為**該幣別的最小單位**：
  TWD → 元（1000 元 = 1000），USD → cents（$10.50 = 1050），JPY → 円（500 円 = 500）。
  `currency` 欄位決定如何解讀 `amount`；新增幣別時需同步更新 `currency` CHECK 約束。
- **IP 由 server 擷取**：`operator_ip` 記錄操作人員 IP，透過 `gin.ClientIP()`（需搭配
  `SetTrustedProxies` 配置）自動取得，不信任 caller 提供。
- **備註雙軌**：`internal_note` 僅供 CMS staff 閱讀（可含稽核語言）；`display_note` 可對玩家暴露。
- **nullable 最小化**：僅對業務上確實可選的欄位使用 nullable；其餘一律 NOT NULL。
- **Enum 型別於 DB 層強制**：`status`、`payment_method`、`currency` 在 DB 層強制合法值，
  不依賴應用層驗證。
- **規模考量**：資料量預期可成長至數十萬筆；index 策略以「玩家帳務查詢」與「後台狀態篩選」為
  兩大主軸設計，避免全表掃描。
- **並發更新策略**：PATCH 採 **last-write-wins**；CMS 後台並發 PATCH 同一筆紀錄的機率極低，
  不引入 optimistic lock 以降低實作複雜度。若未來需要並發保護，可加 `version BIGINT` 欄位配合
  `WHERE version = $n` 的條件更新。

---

## 2. Enum 定義

### 2.1 `deposit_status`

| 值 | 說明 |
|---|---|
| `pending` | 建立後等待確認入帳（初始狀態） |
| `completed` | 入帳成功 |
| `failed` | 入帳失敗（金流商回報）|
| `cancelled` | 人工作廢（僅 pending 可取消，用於更正建立錯誤）|
| `refunded` | 已退款（從 completed 轉入） |

### 2.2 `payment_method`

| 值 | 說明 |
|---|---|
| `bank_transfer` | 銀行轉帳 |
| `credit_card` | 信用卡 |
| `manual` | 人工入帳（CMS staff 建立） |
| `convenience_store` | 超商繳費 |
| `e_wallet` | 電子錢包 |

---

## 3. 資料表 Schema

```sql
CREATE TYPE deposit_status AS ENUM (
    'pending', 'completed', 'failed', 'cancelled', 'refunded'
);
CREATE TYPE payment_method AS ENUM (
    'bank_transfer', 'credit_card', 'manual', 'convenience_store', 'e_wallet'
);

CREATE TABLE deposit_records (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id      UUID           NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    player_name    VARCHAR(64)    NOT NULL,
    amount         BIGINT         NOT NULL CHECK (amount > 0),
    -- currency 白名單在 DB 層強制；擴充幣種時同步修改此 CHECK
    currency       CHAR(3)        NOT NULL DEFAULT 'TWD' CHECK (currency IN ('TWD')),
    status         deposit_status NOT NULL DEFAULT 'pending',
    payment_method payment_method NOT NULL,
    operator_id    UUID           REFERENCES cms_users(id),
    operator_ip    INET,
    internal_note  TEXT,
    display_note   TEXT,
    reference_no   VARCHAR(128),
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now()
);
```

### 3.1 欄位說明

| 欄位 | 型別 | 限制 | 說明 |
|---|---|---|---|
| `id` | UUID | PK, default gen | 紀錄唯一識別碼 |
| `player_id` | UUID | NOT NULL, FK → members, ON DELETE RESTRICT | 玩家代號；限制玩家刪除，保留帳務紀錄 |
| `player_name` | VARCHAR(64) | NOT NULL | 儲值當下快照；server 建立時自動從 members.display_name（顯示暱稱）填入 |
| `amount` | BIGINT | NOT NULL, > 0 | 儲值金額，單位為幣別最小單位（見 §1） |
| `currency` | CHAR(3) | NOT NULL, CHECK whitelist | ISO 4217 幣別代碼；擴充時更新 CHECK 約束 |
| `status` | deposit_status | NOT NULL | 處理狀態；見 §4 狀態機 |
| `payment_method` | payment_method | NOT NULL | 付款方式 |
| `operator_id` | UUID | FK → cms_users, nullable | 建立此筆的 CMS staff；預留 NULL 給未來自動入帳 |
| `operator_ip` | INET | nullable | CMS staff 建立時的 IP，server 自動擷取 |
| `internal_note` | TEXT | nullable | staff 內部備註，**不對玩家暴露** |
| `display_note` | TEXT | nullable | 可對玩家顯示的說明文字 |
| `reference_no` | VARCHAR(128) | nullable, unique | 金流商回傳的外部交易號，用於對帳 |
| `created_at` | TIMESTAMPTZ | NOT NULL | 建立時間 |
| `updated_at` | TIMESTAMPTZ | NOT NULL | 最後異動時間；DB trigger 保證任何更新均自動維護 |

---

## 4. 狀態機

```
              建立
               │
            pending
          /    |    \
    completed  |   failed
        │   cancelled
     refunded
```

| 從 \ 到 | pending | completed | failed | cancelled | refunded |
|---|---|---|---|---|---|
| pending | — | ✅ | ✅ | ✅ | ❌ |
| completed | ❌ | — | ❌ | ❌ | ✅ |
| failed | ❌ | ❌ | — | ❌ | ❌ |
| cancelled | ❌ | ❌ | ❌ | — | ❌ |
| refunded | ❌ | ❌ | ❌ | ❌ | — |

**規則**：
- 合法轉換以外的 `status` 變更，service 層必須拒絕並回傳 `invalid_transition` 錯誤。
- `failed`、`cancelled`、`refunded` 均為**終態**，不得再轉換。
- `cancelled` 用於更正建立錯誤（如金額或玩家填錯），**僅 admin 可操作**（同 PATCH 權限）。
- `amount`、`currency`、`player_id`、`payment_method` 在任何 status 下皆**不可修改**。
- `internal_note` 與 `display_note` 更新語意為**覆蓋**（replace）；歷史備註由 audit log 保存。

---

## 5. Index 策略

```sql
-- 玩家帳務歷史（最常見查詢：我的儲值紀錄，按時間倒序分頁）
-- player_id 單欄查詢也可複用此 index 的 leading column
CREATE INDEX idx_deposit_records_player_created
    ON deposit_records (player_id, created_at DESC);

-- 後台狀態篩選 + 時間排序（例：列出所有 pending，最新優先）
CREATE INDEX idx_deposit_records_status_created
    ON deposit_records (status, created_at DESC);

-- 對帳查詢（金流商外部交易號，僅在有值時建 unique index）
CREATE UNIQUE INDEX uq_deposit_records_reference_no
    ON deposit_records (reference_no)
    WHERE reference_no IS NOT NULL;
```

---

## 6. Go Model

```go
package model

import (
    "time"

    "github.com/google/uuid"
)

type DepositStatus string

const (
    DepositStatusPending   DepositStatus = "pending"
    DepositStatusCompleted DepositStatus = "completed"
    DepositStatusFailed    DepositStatus = "failed"
    DepositStatusCancelled DepositStatus = "cancelled"
    DepositStatusRefunded  DepositStatus = "refunded"
)

type PaymentMethod string

const (
    PaymentMethodBankTransfer     PaymentMethod = "bank_transfer"
    PaymentMethodCreditCard       PaymentMethod = "credit_card"
    PaymentMethodManual           PaymentMethod = "manual"
    PaymentMethodConvenienceStore PaymentMethod = "convenience_store"
    PaymentMethodEWallet          PaymentMethod = "e_wallet"
)

// validStatusTransitions 不可從外部修改；查詢走 CanTransition。
var validStatusTransitions = map[DepositStatus]map[DepositStatus]bool{
    DepositStatusPending:   {DepositStatusCompleted: true, DepositStatusFailed: true, DepositStatusCancelled: true},
    DepositStatusCompleted: {DepositStatusRefunded: true},
    DepositStatusFailed:    {},
    DepositStatusCancelled: {},
    DepositStatusRefunded:  {},
}

// CanTransition 回報從 from 轉到 to 是否合法。
func CanTransition(from, to DepositStatus) bool {
    return validStatusTransitions[from][to]
}

type DepositRecord struct {
    ID            uuid.UUID     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
    PlayerID      uuid.UUID     `gorm:"type:uuid;not null"`
    PlayerName    string        `gorm:"type:varchar(64);not null"`
    Amount        int64         `gorm:"type:bigint;not null"`
    Currency      string        `gorm:"type:char(3);not null;default:TWD"`
    Status        DepositStatus `gorm:"type:deposit_status;not null;default:pending"`
    PaymentMethod PaymentMethod `gorm:"type:payment_method;not null"`
    OperatorID    *uuid.UUID    `gorm:"type:uuid"`
    OperatorIP    *string       `gorm:"type:inet"`
    InternalNote  *string       `gorm:"type:text"`
    DisplayNote   *string       `gorm:"type:text"`
    ReferenceNo   *string       `gorm:"type:varchar(128)"`
    CreatedAt     time.Time     `gorm:"not null;autoCreateTime"`
    UpdatedAt     time.Time     `gorm:"not null;autoUpdateTime"`
}
```

---

## 7. Repository Interface

Repository 只關心儲存操作，接受 domain entity 或純儲存語意的 input type。
業務組裝（查 player_name、填 operator 資訊）由 service 層負責。

```go
package repository

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/yourorg/playerledger/internal/model"
)

// UpdateDepositInput 採三態語意處理可空備註欄位（純儲存關切）：
//   nil ptr     = 欄位未提供，不修改現有值
//   &nil        = 明確傳 null，清空該欄位
//   &"text"     = 設定新值
type UpdateDepositInput struct {
    NewStatus    *model.DepositStatus
    InternalNote **string
    DisplayNote  **string
}

type DepositRecordFilter struct {
    PlayerID      *uuid.UUID
    Status        []model.DepositStatus
    PaymentMethod []model.PaymentMethod
    StartDate     *time.Time
    EndDate       *time.Time
    // Sort 白名單（handler 驗證後傳入，repository 直接映射至 ORDER BY，不做二次驗證）：
    //   "-created_at" → ORDER BY created_at DESC（預設）
    //   "created_at"  → ORDER BY created_at ASC
    //   "-amount"     → ORDER BY amount DESC
    //   "amount"      → ORDER BY amount ASC
    // 非白名單值由 handler 在解析 query 時以 400 拒絕，repository 信任此欄位已驗證。
    Sort string
    Page          int
    PageSize      int
}

// PlayerDepositFilter 玩家端查詢；player_id 由 service 從 token 取得後傳入，不由 caller 提供。
type PlayerDepositFilter struct {
    StartDate *time.Time
    EndDate   *time.Time
    Page      int
    PageSize  int
}

type DepositRecordRepository interface {
    // Create 接受已組裝完整的 entity；player_name、operator 資訊由 service 層填妥後傳入。
    Create(ctx context.Context, r *model.DepositRecord) error
    FindByID(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error)
    List(ctx context.Context, f DepositRecordFilter) ([]*model.DepositRecord, int64, error)
    // Update 回傳更新後的 record，避免 handler 需要再查一次 DB。
    Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error)
    ListByPlayer(ctx context.Context, playerID uuid.UUID, f PlayerDepositFilter) ([]*model.DepositRecord, int64, error)
}
```

---

## 7.5 Service Interface

Service 負責業務組裝與規則驗證；handler 測試透過此 interface 注入 fake，
符合 CLAUDE.md 的 TDD 分層要求。

```go
package service

import (
    "context"
    "errors"

    "github.com/google/uuid"
    "github.com/yourorg/playerledger/internal/model"
    "github.com/yourorg/playerledger/internal/repository"
)

// ErrReferenceNoConflict reference_no 與現有紀錄衝突；handler 映射至 409。
// service 層在 repo.Create 回傳錯誤時，透過以下方式偵測：
//
//	var pgErr *pgconn.PgError
//	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
//	    return nil, ErrReferenceNoConflict
//	}
var ErrReferenceNoConflict = errors.New("reference_no conflict")

// CreateDepositInput handler 提供的原始輸入；PlayerName 不在此，由 service 查詢後填入 entity。
type CreateDepositInput struct {
    PlayerID      uuid.UUID
    Amount        int64
    Currency      string
    PaymentMethod model.PaymentMethod
    InternalNote  *string
    DisplayNote   *string
    ReferenceNo   *string
    OperatorID    uuid.UUID  // 從 access token claims.sub 取得
    OperatorIP    *string    // 從 c.ClientIP() 取得
}

// UpdateDepositInput handler 提供的更新輸入；三態語意同 repository.UpdateDepositInput。
// service 內部將此型別映射至 repository.UpdateDepositInput，handler 無需 import repository 包。
type UpdateDepositInput struct {
    NewStatus    *model.DepositStatus
    InternalNote **string
    DisplayNote  **string
}

type DepositService interface {
    // Create 從 members 查 PlayerName 快照，組裝完整 entity 後呼叫 repo.Create。
    Create(ctx context.Context, input CreateDepositInput) (*model.DepositRecord, error)

    Get(ctx context.Context, id uuid.UUID) (*model.DepositRecord, error)

    List(ctx context.Context, f repository.DepositRecordFilter) ([]*model.DepositRecord, int64, error)

    // Update 驗證 CanTransition，更新後依結果寫 audit log（status_changed 或 note_updated）。
    // input 為 service 包內的 UpdateDepositInput；service 負責映射至 repository.UpdateDepositInput。
    Update(ctx context.Context, id uuid.UUID, input UpdateDepositInput) (*model.DepositRecord, error)

    // ListByPlayer player_id 由 caller 從 token 取得後傳入，service 不自行讀 token。
    ListByPlayer(ctx context.Context, playerID uuid.UUID, f repository.PlayerDepositFilter) ([]*model.DepositRecord, int64, error)
}
```

---

## 8. Migration

```sql
-- migrations/000003_create_deposit_records.up.sql

CREATE TYPE deposit_status AS ENUM (
    'pending', 'completed', 'failed', 'cancelled', 'refunded'
);
CREATE TYPE payment_method AS ENUM (
    'bank_transfer', 'credit_card', 'manual', 'convenience_store', 'e_wallet'
);

CREATE TABLE deposit_records (
    id             UUID           PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id      UUID           NOT NULL REFERENCES members(id) ON DELETE RESTRICT,
    player_name    VARCHAR(64)    NOT NULL,
    amount         BIGINT         NOT NULL CHECK (amount > 0),
    currency       CHAR(3)        NOT NULL DEFAULT 'TWD' CHECK (currency IN ('TWD')),
    status         deposit_status NOT NULL DEFAULT 'pending',
    payment_method payment_method NOT NULL,
    operator_id    UUID           REFERENCES cms_users(id),
    operator_ip    INET,
    internal_note  TEXT,
    display_note   TEXT,
    reference_no   VARCHAR(128),
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ    NOT NULL DEFAULT now()
);

CREATE INDEX idx_deposit_records_player_created
    ON deposit_records (player_id, created_at DESC);

CREATE INDEX idx_deposit_records_status_created
    ON deposit_records (status, created_at DESC);

CREATE UNIQUE INDEX uq_deposit_records_reference_no
    ON deposit_records (reference_no)
    WHERE reference_no IS NOT NULL;

-- updated_at trigger：確保直接 DB 操作（migration fix、緊急補單）也會維護此欄位。
-- 若 set_updated_at() 已由其他 migration 定義，可省略 CREATE FUNCTION 段落。
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_deposit_records_updated_at
    BEFORE UPDATE ON deposit_records
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

```sql
-- migrations/000003_create_deposit_records.down.sql

DROP TRIGGER IF EXISTS trg_deposit_records_updated_at ON deposit_records;
DROP TABLE IF EXISTS deposit_records;
DROP TYPE IF EXISTS payment_method;
DROP TYPE IF EXISTS deposit_status;
-- set_updated_at() 為共用函式，不在此 down migration 刪除
```

---

## 9. 實作 Checklist

- [ ] 建立 migration 檔（`000003_create_deposit_records.up/down.sql`）
- [ ] 建立 `internal/model/deposit_record.go`（含 enum 常數、`CanTransition`，涵蓋 `cancelled`）
- [ ] 建立 `internal/repository/deposit_record_repository.go`（interface + GORM 實作；`Update` 處理 `**string` 三態）
- [ ] 建立 `internal/repository/deposit_record_repository_test.go`（integration test，真實 DB）
- [ ] 建立 `internal/service/deposit_service.go`（實作 `DepositService` interface）
- [ ] 建立 `internal/service/deposit_service_test.go`（unit test：8 種非法轉換、player 不存在、audit event 分支）
- [ ] service 層建立時：從 members 查 `player_name`；handler 從 `c.ClientIP()` 取 `operator_ip` 後傳入
- [ ] service 層 Update：呼叫 `model.CanTransition` 驗證，違反回 `invalid_transition`
- [ ] `gin.SetTrustedProxies` 於 main.go 正確配置，確保 `ClientIP()` 可信
