# ADR 006 — 兩種 400 錯誤的區別

**狀態**：已採用  
**日期**：2026-06-27

---

## 背景

系統中有兩種情況都應該回傳 **400 Bad Request**，但來源、語意與回應格式不同。若混淆處理，會導致：

- Service 層的業務邏輯錯誤因 `HandleError` 識別不到而誤回 500
- 前端無法區分「欄位格式錯」和「業務邏輯不合法」，難以給出正確提示

---

## 兩種錯誤的比較

| | `validator.ValidationErrors` | `apperr.ErrInvalidInput` |
|---|---|---|
| **來源** | Handler 層，`ShouldBindJSON` / `ShouldBindQuery` | Service 層，業務邏輯主動回傳 |
| **觸發時機** | struct tag 驗證失敗（格式、必填、長度） | 欄位格式合法但業務邏輯不合理 |
| **偵測方式** | `errors.As(err, &ve)` | `errors.Is(err, apperr.ErrInvalidInput)` |
| **HTTP Status** | 400 | 400 |
| **回應格式** | 含 `details[]` 欄位錯誤清單 | 僅 `error` 字串 |

---

## 情境範例

### `validator.ValidationErrors`（欄位格式問題）

```go
type LoginRequest struct {
    Email    string `json:"email"    validate:"required,email"`
    Password string `json:"password" validate:"required,min=8"`
}

// email 格式錯誤 → validator/v10 自動偵測 → ValidationErrors
```

回應：
```json
{
  "success": false,
  "error": "invalid input",
  "details": [
    { "field": "Email",    "message": "必須為有效的 email 格式" },
    { "field": "Password", "message": "最小長度為 8" }
  ]
}
```

### `apperr.ErrInvalidInput`（業務邏輯不合法）

```go
// 欄位格式都正確，但結束時間早於開始時間
func (s *TransactionService) List(ctx context.Context, req *ListRequest) {
    if req.EndDate.Before(req.StartDate) {
        return nil, apperr.ErrInvalidInput
    }
}
```

回應：
```json
{
  "success": false,
  "error": "invalid input"
}
```

---

## HandleError 處理順序

```go
// 1. 先用 errors.As 偵測 validator.ValidationErrors（具體型別）
var ve validator.ValidationErrors
if errors.As(err, &ve) {
    // → 400 + details[]
    return
}

// 2. 再用 errors.Is 偵測 domain errors（sentinel errors）
switch {
case errors.Is(err, apperr.ErrInvalidInput):
    // → 400，無 details
case errors.Is(err, apperr.ErrNotFound):
    // → 404
// ...
}
```

`errors.As` 必須在 `errors.Is` 之前，因為 `validator.ValidationErrors` 不是 sentinel error，無法用 `errors.Is` 識別。

---

## 決策

- **保留 `apperr.ErrInvalidInput`**：不因 `validator.ValidationErrors` 的存在而刪除，兩者處理不同層的問題
- **`HandleError` 兩條分支並存**：`errors.As` 處理格式錯誤，`errors.Is` 處理業務邏輯錯誤
- **前端識別方式**：同樣是 400，但有無 `details` 欄位可區分來源

## 影響

- **正面**：錯誤語意清晰，前端可依 `details` 是否存在決定顯示方式
- **規範**：Service 層只能回傳 `apperr.ErrInvalidInput`，不能直接建立 `validator.ValidationErrors`；Handler 層不自行判斷業務邏輯，業務邏輯錯誤由 service 回傳
