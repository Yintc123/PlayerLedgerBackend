# ADR 003 — 雙使用者系統設計

**狀態**：已採用（**§3「Member 資料隔離」實作位置已修訂為 middleware 層**，見下方修訂註）
**日期**：2026-06-27

> **修訂註（2026-06-28，infrastructure.md v1.8+）**：
> 原文 §3 寫「在 **service 層**強制驗證 `claims.UserID == targetID`」。
> 實際落地時改為 **`pkg/jwt.RequireOwnership` middleware 層**統一執行（infrastructure.md §8.6）。
>
> 理由：
> 1. middleware 攔截後可避免 service 入口被遺漏（service 入口會增加，少寫一處即出 forbidden bypass）
> 2. CMS 自動放行邏輯（CMS 不受 ownership 限制）集中一處，避免每個 service 重複判斷 `UserType`
> 3. middleware fail-closed：path param 名稱拼錯時 warn 後 403，比 service 層忘記檢查更安全
>
> 決策不變（型別區分、enum、雙表分離），只是實作位置調整。

> **修訂註（2026-06-28，infrastructure.md v1.9+）**：
> 原文 §1 `AccessClaims` 寫獨立 `UserID string` `json:"uid"` 欄位、§2 `SignAccess` 簽章為位置參數。實際落地改為：
> - **User ID 攜帶於 `RegisteredClaims.Subject`（標準 `sub` claim）**，遵循 JWT RFC 7519 慣例；移除原獨立 `UserID` / `uid` 欄位。
> - **新增 `(c *AccessClaims) UserID() string` helper**（即 `c.Subject` 的具名 alias）。所有 middleware / service / audit 一律呼叫此 method，**禁止直接讀 `Subject`**。
> - **`SignAccess` 改為 struct param**：`SignAccess(p SignAccessParams)`，避免位置參數膨脹。
>
> 理由：對齊 JWT 標準，未來接 OAuth / OIDC / 第三方 SSO 不需轉換 claim 名稱；helper alias 讓未來改 carriage（複合 ID 等）只動一處。決策不變（雙表分離 / `utype` 路由），只是 claim 命名與簽章 ergonomics 調整。下方原文 §1 / §2 / §3 程式碼已同步更新。

---

## 背景

系統有兩類使用者：

- **CMS 內部人員**（admin / user / viewer）：使用後台管理介面
- **一般玩家**（member）：使用前台查詢自己的儲值紀錄

兩類使用者存在不同資料表，但共用同一套 JWT 認證流程。

## 問題

若 CMS 人員與玩家的 ID 只用 UUID 儲存於 `sub` 欄位，無法區分來自哪張表，且兩張表的 UUID 可能碰撞，導致查詢錯誤的資料。

## 決策

### 1. UserType 欄位

在 JWT Claims 加入 `utype` 欄位區分使用者來源：

```go
type AccessClaims struct {
    jwt.RegisteredClaims              // user ID 在 Subject；caller 透過 UserID() helper 取（infrastructure.md §8.3）
    UserType UserType `json:"utype"`  // "cms" | "member"
    Role     Role     `json:"role"`
}

// UserID 是 RegisteredClaims.Subject 的具名 alias。
func (c *AccessClaims) UserID() string { return c.Subject }
```

`utype` 用於**資料表路由**，`role` 用於**權限控制**，職責不重疊。

### 2. Enum 型別定義

`Role` 與 `UserType` 皆定義為 Go 慣用的 enum 模式，提供型別安全與 `IsValid()` 驗證：

```go
type UserType string

const (
    UserTypeCMS    UserType = "cms"
    UserTypeMember UserType = "member"
)

func (u UserType) IsValid() bool { ... }
```

`Manager` 介面使用強型別，傳錯型別編譯即報錯：

```go
SignAccess(p SignAccessParams) (token string, err error)  // SignAccessParams 詳見 infrastructure.md §8.3
```

### 3. Member 資料隔離

`utype == "member"` 的請求，由 **`pkg/jwt.RequireOwnership` middleware**（見上方 v1.8 修訂註）統一比對 `claims.UserID() == c.Param(<paramName>)`，不符回 403 `forbidden`；CMS 自動放行。

### 4. 角色清單

| Role | UserType | 說明 |
|------|----------|------|
| `admin` | `cms` | 系統管理員，最高權限 |
| `user` | `cms` | 一般操作人員 |
| `viewer` | `cms` | 唯讀檢視者 |
| `member` | `member` | 一般玩家，只能查詢自己的資料 |

## 影響

- **正面**：明確的型別區分，避免 ID 碰撞；編譯期型別安全；Member 資料隔離規範清晰
- **負面**：所有簽發 token 的地方都需傳入 `userType`，增加少量參數
- **規範**：新增使用者類型時，必須同步更新 `UserType` 常數與 `IsValid()`
