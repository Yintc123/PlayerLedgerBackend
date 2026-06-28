# ADR 002 — JWT 雙 Token 設計

**狀態**：⚠️ **已被 [ADR 007](./007-refresh-token-rotation-and-replay-detection.md) 取代**（2026-06-28）
**日期**：2026-06-27

> **取代原因**：本 ADR 的「單一 session、新登入踢掉舊裝置」設計無法支援多裝置同時登入；
> 亦未處理 refresh token 重放偵測。詳見 ADR 007 的「背景」與「為何不選其他方案」段落。

---

## 背景

專案有自建登入流程，需要定義 token 的簽發、驗證、換發、撤銷機制。

## 決策

採用 **Access Token + Refresh Token 雙 token** 設計，搭配以下規則：

### Token 規格

| | Access Token | Refresh Token |
|---|---|---|
| 用途 | API 請求驗證 | 換發新 access token |
| 有效期 | 1 小時 | 7 天 |
| Secret | `JWT_SECRET` | `JWT_REFRESH_SECRET`（獨立） |
| 儲存位置 | 客戶端 | 客戶端 + Redis |
| Claims | `uid`、`utype`、`role`、`jti` | `uid`、`utype`、`jti` |

兩個 token 使用**不同 secret**，一把洩漏不影響另一把。

### Refresh Token Rotation

每次換發時廢棄舊 refresh token、簽發新的，防止竊取後長期有效。

### 單一 Session 設計

同一帳號同時只有一組有效 token。Redis 維護兩組 key：

```
auth:refresh:{jti}         → userID   TTL = refresh token 有效期
auth:user:{userID}:refresh → jti      TTL = refresh token 有效期
```

新登入會覆蓋舊的 refresh token，舊裝置自動失效。

### 登出不需要帶 refresh token

透過 access token 內的 `userID` 反查 `auth:user:{userID}:refresh` 得到 refresh jti，再一併清除，客戶端只需帶 access token 即可完成登出。

### Token 流程

```
登入     → 簽發 access token + refresh token，寫入兩組 Redis key
API 請求 → 帶 access token，過期回 401
換發     → POST /auth/refresh，驗簽 + 確認 Redis，rotation 後回傳新 token
登出     → 從 access token 取 userID → 反查 refresh jti → 清除 Redis + 黑名單
```

## 影響

- **正面**：access token 短效降低竊取風險；refresh token rotation 防止長期濫用；登出流程對客戶端友善
- **負面**：單一 Session 設計不支援多裝置同時登入，未來若需多 Session 需重新設計 Redis 結構
- **依賴**：Redis 是 refresh token 的信任來源，Redis 不可用時換發與登出功能受影響
