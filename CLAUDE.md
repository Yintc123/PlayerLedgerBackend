# PlayerLedger Backend — Claude Guidelines

## 開發方法：TDD

本專案嚴格遵守 **Test-Driven Development**，開發任何功能前必須先寫測試。

### TDD 流程

```
1. Red   — 寫一個失敗的測試，明確描述預期行為
2. Green — 寫最少量的程式碼讓測試通過
3. Refactor — 在測試保護下重構，不改變行為
```

### 規則

- **不允許在沒有對應測試的情況下新增功能程式碼**
- 測試檔案與實作檔案放在同一目錄，命名為 `*_test.go`
- 每個測試函式只驗證一件事，名稱格式為 `TestXxx_條件_預期結果`
- 不 mock 內部模組；repository 層使用 interface，測試時替換為 fake 實作
- 禁止使用 `t.Skip()`跳過測試，修好或刪除

### 測試分層

| 層級 | 套件 | 涵蓋範圍 |
|------|------|---------|
| Unit | `testing` + `testify` | service、純邏輯、工具函式 |
| Integration | `testing` + 真實 DB | repository 層，使用測試資料庫 |
| E2E | `net/http/httptest` | handler 層，完整 request/response 流程 |

### 執行測試

```bash
go test ./...                    # 執行所有測試
go test ./... -v                 # 詳細輸出
go test ./... -run TestXxx       # 執行特定測試
go test ./... -cover             # 顯示覆蓋率
```

## 開發方法：SDD

API 以 OpenAPI Schema 為唯一契約：

- Schema 定義在 `schema/` 目錄，為前後端共同依據
- Schema 變更需先修改 `schema/`，再調整 handler 與測試，最後更新實作
- handler 的 request/response 結構必須與 Schema 嚴格對應
