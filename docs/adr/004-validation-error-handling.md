# ADR 004 — Validation 錯誤處理

**狀態**：已採用（**回應形狀已更新**，見下方修訂註）
**日期**：2026-06-27

> **修訂註（2026-06-28，infrastructure.md v1.8+）**：
> 原文範例使用單一 `Response[any]` + `omitempty` 包成功與錯誤。
> 實際落地時改為 **成功與錯誤拆成兩個獨立型別**：
>
> - 成功：`Response[T]`（含 `RequestID`、`Data`、可選 `Meta`）
> - 錯誤：`ErrorResponse`（含 `RequestID`、`Error`、可選 `Details`）
>
> 理由：單一型別 + `omitempty` 在「empty slice」會被序列化成「欄位消失」而非 `[]`，破壞前端契約。
> 決策的核心（`errors.As` 偵測 `validator.ValidationErrors`、回 400 + `details[]`、handler 零負擔）不變，
> 僅承載結構改名。詳見 infrastructure.md §10.2。

---

## 背景

Gin 使用 `validator/v10` 進行 request 資料驗證。當 `c.ShouldBindJSON()` 或 `c.ShouldBindQuery()` 驗證失敗時，回傳的是 `validator.ValidationErrors` 型別，不是 domain error。

## 問題

### 發生時機

驗證錯誤發生在 **handler 函式內**，不是 middleware。流程如下：

```
請求 → CORS → Logger → RateLimit → Auth → Handler
                                              └─→ c.ShouldBindJSON(&req)
                                                      └─→ validator/v10 執行
                                                              └─→ 失敗回傳 ValidationErrors
```

### 原有設計的問題

`HandleError` 只處理 domain errors，無法識別 `validator.ValidationErrors`，會落入 `default` 分支回傳 **500 Internal Server Error**。

使用者帶錯參數（client 的問題）→ server 回 500 → 前端誤以為是 server 故障。

正確的 HTTP 狀態碼應為 **400 Bad Request**（請求資料有問題）。

注意與 **403 Forbidden** 的區別：
- 400 = 請求格式或資料有問題
- 403 = 已認證但沒有權限執行此操作

## 決策

在 `HandleError` 最前面加入 `errors.As` 偵測分支，優先處理 validation errors：

```go
var ve validator.ValidationErrors
if errors.As(err, &ve) {
    details := make([]FieldError, len(ve))
    for i, fe := range ve {
        details[i] = FieldError{
            Field:   fe.Field(),
            Message: validationMessage(fe),
        }
    }
    c.AbortWithStatusJSON(http.StatusBadRequest, Response[any]{
        Success: false,
        Error:   "invalid input",
        Details: details,
    })
    return
}
```

使用 `errors.As`（非 `errors.Is`）的原因：`validator.ValidationErrors` 是具體型別，不是 sentinel error，需用 `errors.As` 做型別斷言。

### 回應格式

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

`details` 欄位僅在 validation 錯誤時出現（`omitempty`），其他錯誤回應不含此欄位。

### Handler 不需要改寫

handler 直接把 `ShouldBindJSON` 的 error 傳給 `HandleError` 即可：

```go
if err := c.ShouldBindJSON(&req); err != nil {
    HandleError(c, err)  // 自動識別 ValidationErrors
    return
}
```

## 影響

- **正面**：集中處理，handler 零負擔；回應 400 而非 500，語意正確；欄位級錯誤訊息對前端友善
- **負面**：`validationMessage` 需手動維護 tag → 訊息對應表，新增自訂 validator tag 時要記得更新
