# ADR 005 — Response 與 Error Handler 職責分離

**狀態**：已採用（**Response[T] 與 ErrorResponse 已拆成兩個型別**，見下方修訂註）
**日期**：2026-06-27

> **修訂註（2026-06-28，infrastructure.md v1.8+）**：
> 原文 `Response[T any]` 同時承載成功與錯誤（`Error` / `Details` 用 `omitempty`）。
> 實際落地時拆成 **`Response[T]`（成功）** 與 **`ErrorResponse`（錯誤）** 兩個獨立型別，
> 並補上 `RequestID` 欄位。
>
> 理由：
> 1. empty slice + `omitempty` 會被 JSON 序列化為「欄位消失」而非 `[]`，破壞前端契約
> 2. 成功與錯誤資料形狀差太多，硬擠進同一型別讓 generic 失去意義
> 3. 加上 `RequestID` 後，客服回報可直接對應 server log
>
> 「**response 定義形狀、error handler 負責轉換**」的職責分離與本 ADR 完全一致，
> 僅承載結構從一個型別變兩個。詳見 infrastructure.md §10.2。

---

## 背景

Handler 層需要統一的回應格式與錯誤處理機制。初始設計將回應結構定義在 handler 各處，導致格式不一致。

## 決策

將 handler 層的共用程式碼拆分為兩個職責明確的檔案：

### `response.go` — 定義形狀

定義所有對前端的回應結構，包含成功與錯誤的資料形狀：

```go
type Response[T any] struct {
    Success bool         `json:"success"`
    Data    T            `json:"data,omitempty"`
    Error   string       `json:"error,omitempty"`
    Details []FieldError `json:"details,omitempty"`
    Meta    *Meta        `json:"meta,omitempty"`
}

type FieldError struct {
    Field   string `json:"field"`
    Message string `json:"message"`
}
```

### `error_handler.go` — 負責轉換

將所有 error 轉為對應的 `Response`，使用 `response.go` 定義的型別：

```go
// 任何 error
//   └─→ HandleError
//         ├─ validator.ValidationErrors → 400 + Details
//         ├─ apperr.ErrNotFound        → 404
//         ├─ apperr.ErrUnauthorized    → 401
//         ├─ apperr.ErrForbidden       → 403
//         └─ 其他                      → 500
```

### 資料流

```
任何 error
    └─→ error_handler.go（HandleError）
            └─→ 選擇對應 status + message
                    └─→ Response[any]  ← 型別定義在 response.go
                            └─→ 前端
```

## 為何 FieldError 放在 response.go

`FieldError` 是回應給前端的資料結構，屬於「回應的形狀」，不是「錯誤處理的邏輯」。`error_handler.go` 使用它來組裝回應，但不定義它，保持單一職責。

## 影響

- **正面**：職責清晰，修改回應格式只動 `response.go`，修改錯誤對應邏輯只動 `error_handler.go`
- **正面**：新增欄位（如未來加 `requestId`）只需改 `response.go`，不影響錯誤邏輯
- **規範**：禁止在各 handler 內自行定義回應結構或判斷 HTTP status code，一律透過 `Response` 與 `HandleError`
