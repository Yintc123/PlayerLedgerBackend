# Architecture Decision Records

本目錄記錄 PlayerLedger Backend 的架構決策。每份 ADR 描述一個技術抉擇的背景、選項評估、決策內容與影響。

## 格式

- 檔名：`NNN-kebab-case-title.md`，三位數編號遞增，禁止重用編號。
- 狀態：`已採用` / `已取代`（被新 ADR 取代）/ `草案`。
- 取代既有決策時，新 ADR 必須在標題下方註明 `**取代**：[ADR XXX](./XXX-...md)`，被取代的 ADR 同步標記 `**狀態**：⚠️ 已被 [ADR YYY](./YYY-...md) 取代`。

## 索引

| ADR | 標題 | 狀態 | 日期 |
|---|---|---|---|
| [001](./001-orm-selection.md) | ORM 選型（GORM v2） | 已採用 | 2026-06-27 |
| [002](./002-jwt-token-design.md) | JWT 雙 Token 設計 | ⚠️ 已被 ADR 007 取代 | 2026-06-27 |
| [003](./003-dual-user-system.md) | 雙使用者系統設計（CMS / Member） | 已採用 | 2026-06-27 |
| [004](./004-validation-error-handling.md) | Validation 錯誤處理 | 已採用 | 2026-06-27 |
| [005](./005-response-error-handler-separation.md) | Response 與 Error Handler 職責分離 | 已採用 | 2026-06-27 |
| [006](./006-invalid-input-error-types.md) | 兩種 400 錯誤的區別 | 已採用 | 2026-06-27 |
| [007](./007-refresh-token-rotation-and-replay-detection.md) | Refresh Token Rotation 與重放偵測 | 已採用（取代 ADR 002） | 2026-06-28 |

## 撰寫指南

新 ADR 須包含以下段落：

1. **背景**：為什麼這個決策需要做？目前狀態與痛點。
2. **各方案評估**：列出至少 2 個方案的優缺點（含「不做」選項）。
3. **決策**：明確選定的方案與配套規則。
4. **影響**：正面、負面、依賴、未來工作。

避免事後合理化：ADR 寫於決策當下，不為已實作的程式碼補理由。
