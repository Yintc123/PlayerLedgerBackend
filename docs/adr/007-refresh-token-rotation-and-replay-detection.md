# ADR 007 — Refresh Token Rotation 與重放偵測（取代 ADR 002）

**狀態**：已採用
**日期**：2026-06-28
**取代**：[ADR 002](./002-jwt-token-design.md)

---

## 背景

API server 服務多種 client（CMS 後台、一般使用者 web、未來的行動裝置 app 等），需同時滿足：

1. **多裝置支援**：使用者可在手機、桌機、平板等多裝置同時保持登入，互不踢除
2. **CMS 閒置安全**：
   - 前端完全無互動 15 分鐘 → 強制登出（UX 體驗，由前端負責）
   - 後端 1 小時無任何 API 請求 → 強制登出（即便使用者仍在前端編輯，沒打 API 就視為離線）
3. **不同 client 套不同 token 政策**（CMS 偏嚴格、行動裝置可較長效）
4. **防 refresh token 重放攻擊**：rotation 後的舊 token 必須被偵測、被偷的 family 整串失效
5. **stateless 為主**：access token 驗證走純 stateless（hot path 不打 Redis / DB）
6. **純 JWT API，不走 cookie**：API server 對所有 client 提供一致契約，避免 cookie state、CSRF token、SameSite 等瀏覽器邊界處理

ADR 002 原本的「單一 session、refresh 在 Redis 反查」設計無法支援多裝置，且未處理重放偵測；本 ADR 重新設計並取代之。

---

## 決策摘要

採用 **Refresh Token Rotation + Family-based Reuse Detection**（OAuth 2.0 Security BCP 推薦模式）：

- 每次 login 開一個全新的 **family**（UUID），多裝置 = 多 family，互相獨立
- 每次 refresh 旋轉新的 `jti`，舊 jti 立即失效，舊 token 再次出現視為重放 → 整 family 廢棄
- 加上 **grace window**（10 秒）容忍網路重試造成的同 jti 重送
- Refresh token JWT 內帶 **`abs_exp`** 強制單一 family 的絕對最長壽命（每個 client 政策不同）
- Family 狀態存 Redis（極小、僅 refresh 時讀寫），key 命名與 ADR 002 一致採 `{userID}` hash tag
- **簽署演算法**：**HS256**（單體服務階段足夠；未來拆服務時再升級至 RS256，見「未來工作」）
- **Token 傳輸**：**純 JWT**，access 走 `Authorization: Bearer`，refresh 走 request body。**不發 cookie**

---

## 為何不選其他方案

### A. 純前端 idle timer

**否決**：使用者可關閉 JS、繞過 timer；只要 JWT 還沒過期，攻擊者就能繼續打 API。前端 timer **只是 UX**，不能當安全邊界。

### B. 把 access token 設成 30 分、refresh token 1 小時，閒置交給前端

**否決**：
- Token TTL 是絕對時間，跟「閒置」語意不同 → 會出現「剛操作完卻被登出」或「明明沒動還能用」
- 1 小時 refresh 對活躍使用者太短（CMS 編輯文章常超過 1 小時），體驗差
- 後端沒在強制 15 分鐘閒置 → 攻擊者拿到 token 可用滿 30 分鐘

### C. 純 stateless JWT（無任何 server state）

**否決**：純 stateless 與重放偵測**本質上互斥**。

JWT 簽出去後到 `exp` 之前無法提前失效。若不存任何 state，rotation 也只能「發新 token」，老 token 仍有效到 exp，攻擊者拿走的 token 在 1 小時內隨便用，零防護。

### D. 將 used jti 全部存進 denylist 並 TTL 6 個月

**否決**：

- 6 個月遠超過 refresh token 自己的 `exp`（1 小時），過期後 JWT 簽章驗證階段就拒絕，不需要額外記憶
- Storage 隨 rotation 次數線性增長（`O(rotation 次數)`），90 天每天 96 次 = 8640 筆/人，浪費
- 真正只需記憶**「這個 family 目前唯一有效的 jti」**（`O(active family 數)`，每個 login 一筆）

### E. DB 儲存 family

**否決於本階段**：DB 提供更好的稽核與耐久性，但本系統已有 Redis、family 存活時間 ≤ abs_exp（8 小時量級）、容量極小（每 family ≈ 100 bytes），Redis 已足夠。

**Redis 失效時的安全性**：所有 family 蒸發 → 合法者與攻擊者皆無法 refresh → 全員被踢回 login。**fail-safe，不是 fail-open**。

重要事件（replay detection、強制登出）另外寫 audit log，補回稽核紀錄。

### F. Refresh token 用 HttpOnly Cookie 傳輸

**否決於本階段**：HttpOnly cookie 是防 XSS 偷 refresh token 的最強手段，但：

- API server 同時服務 web SPA 跟 native app（iOS / Android）；native 沒有 cookie jar，要為 web 特例化會讓契約分歧
- Cookie 需處理 CSRF token、SameSite 邊界（跨域、subdomain、preflight）等瀏覽器規則，使後端契約複雜
- 統一純 JWT 契約簡潔，責任邊界清楚：**後端負責簽章與 rotation；前端負責安全儲存**

**接受的取捨**：前端必須以 in-memory（首選）或 sessionStorage（次選）儲存 refresh token，**禁用 localStorage**。XSS 風險靠下列配套壓制：

- 強制 CSP（nonce-based，禁 inline script、禁 `unsafe-eval`）
- 嚴格輸出跳脫（React/Vue 預設機制；禁用 `dangerouslySetInnerHTML`）
- 第三方 npm 依賴供應鏈審查（Dependabot + 定期人工 review）
- 嚴格的絕對最大 session（abs_exp）限制傷害時間
- Replay detection（本 ADR 主要設計）作為第一道防線

> **未來若引入跨域 cookie 場景**（例：SSO、第三方嵌入），可開新 ADR 評估改用 cookie。本階段不必為此付契約複雜化的代價。

---

## 設計細節

### Token 規格

| | Access Token | Refresh Token |
|---|---|---|
| 用途 | API 請求驗證 | 換發新 access + refresh token |
| 演算法 | **HS256**（HMAC-SHA256） | 同（**不同 secret**） |
| 有效期 | 15 分鐘（純 stateless 驗證） | 滑動依 client policy（CMS 1 小時），絕對上限依 client policy |
| Claims | `iss`、`sub`、`utype`、`role`、`fid`、`aud`、`exp`、`iat`、`jti` | `iss`、`sub`、`utype`、`jti`、`fid`、`aud`、`exp`、`abs_exp`、`iat` |
| 傳輸（client → server） | `Authorization: Bearer <jwt>` | Request body `{ "refresh_token": "<jwt>" }` |
| Client 儲存 | In-memory variable | In-memory（首選）/ sessionStorage（次選）/ **禁 localStorage** |
| 驗證 | 純 JWT 簽章 + iss + aud + exp（不打 Redis） | JWT 簽章 + iss + aud + exp + abs_exp + 查 family.current_jti |

> **`fid` 放進 access token 的用途**：方便管理員「廢掉某個 family」時連帶讓該 family 已簽發的 access token 進短期黑名單（access token 通常 15 分鐘內自然過期，黑名單只需短 TTL）。
>
> **`iss` claim**：固定為 `JWTConfig.Issuer`（例如 `playerledger`）。多服務時可用來判定 token 來源。
>
> **`aud` claim**：=`client_id`（`cms-web` / `public-web` / `ios-app` / `android-app`）。跨 client 拿 token 直接被驗證階段擋下。

### Client Policy

不同 client 套不同 TTL 與絕對上限。Login request body 帶 `client_id`，server 對照 `JWTConfig.ClientPolicies`：

| `client_id` | Refresh TTL（滑動） | 絕對上限 `abs_exp` | 適用情境 |
|---|---|---|---|
| `cms-web` | 1 小時 | 8 小時 | CMS 後台，工作日結束強制重登 |
| `public-web` | 1 小時 | 24 小時 | 一般使用者 web |
| `ios-app` / `android-app` | 30 天 | 180 天 | 行動裝置「保持登入」體驗 |

> Policy 表寫在 server config（infrastructure §4），新增 client 不需改 code。

**未知 `client_id`**：server 回 `400 Bad Request`，error code `invalid_client`（對應 `apperr.ErrInvalidClient`）。**不套用 default policy**，避免攻擊者用未知 client_id 繞過嚴格 client 的限制。

### Family 狀態（Redis）

```
Key:   auth:family:{<userID>}:<fid>      # hash tag = userID，與其他 auth key 同 slot
Value: JSON FamilyState
TTL:   abs_exp 剩餘秒數（family 與其絕對上限同生死）
```

```go
type FamilyState struct {
    UserID                string    // 同 hash tag
    FamilyID              string
    ClientID              string    // = aud claim
    UserType              string    // login 時固化；rotation / GraceHit 重簽 access 用
    Role                  string    // login 時固化；同上
    CurrentJTI            string    // 目前唯一合法 refresh jti
    PreviousJTI           string    // grace window 用：上一次的 jti
    PreviousResponseUntil int64     // unix seconds；超過此時刻 previous_jti 也失效
    AbsoluteExp           int64     // unix seconds；rotation 不延長，由 server 信任的 state 持有
    DeviceLabel           string    // 從 User-Agent 解析（UI 顯示用）
    IPAtLogin             string
    CreatedAt             int64     // unix seconds
    LastRotatedAt         int64     // unix seconds
}
```

> **為何 state 需要 `AbsoluteExp`**：rotation / grace 流程要重新簽 refresh token，新 refresh JWT 的 `abs_exp` 不能信任 client presented JWT（即便驗過簽章，把 abs_exp 維持在 server-side state 是最簡單的安全模型）。Lua 也用此值計算 Redis key TTL。

> **為何 state 需要 `UserType` / `Role`**：rotation 與 GraceHit 都會重簽 access token，新 access JWT 必須帶完整 claims。若不存入 state，每次 refresh 都要打 DB 查使用者，破壞「hot path 純 stateless」的設計目標。
>
> **取捨**：使用者 role 變更（例如 admin 降權）只在下次 login 才會生效，舊 family 仍持有舊 role 直到自然結束或被廢；若需立即生效，呼叫 `RevokeAll` 強制全裝置重登。此取捨換來的是 refresh hot path 完全不打 DB，符合本 ADR「stateless 為主」的設計原則。

**索引（必要）**：用於「列出該使用者所有登入裝置」與「全裝置登出」：

```
Key: auth:user_families:{<userID>}       # SET，內含所有 fid
TTL: 不設
```

**索引清理策略：lazy cleanup**

family key 有 TTL 會自動消失，但 SET 內的 fid 不會。為避免累積垃圾：

1. **`ListByUser`**：`SMEMBERS` 取 fid → `MGET` 各 family key → 過濾 nil → `SREM` 過期的 fid → 回傳實際存活的 family
2. **`Revoke`**：Lua 同時 `DEL family` + `SREM index`
3. **`RevokeAll`**：Lua `SMEMBERS` → 逐一 `DEL family` → `DEL index`
4. **`Save`**：`SADD` 即可，不需清理（自然垃圾累積由步驟 1 處理）

不採用 Redis keyspace notifications（要開啟 `notify-keyspace-events`，運維成本不值得）。

---

### 流程

#### Login

```
POST /auth/login
Body: { "username": "...", "password": "...", "client_id": "cms-web" }

1. 驗證 client_id ∈ ClientPolicies；不在 → 400 invalid_client
2. 驗證帳密 → 拿到 user_id、utype、role
3. 從 client_id 取出 policy（refresh_ttl, abs_ttl）
4. fid = uuid.New() ；refresh_jti = uuid.New() ；access_jti = uuid.New()
5. now = time.Now() ；abs_exp = now + abs_ttl
6. 解析 User-Agent → device_label
7. 簽 access  { iss, sub=user_id, utype, role, fid, aud=client_id,
                exp=now+15min, iat=now, jti=access_jti }
8. 簽 refresh { iss, sub=user_id, utype, jti=refresh_jti, fid, aud=client_id,
                exp=now+refresh_ttl, abs_exp, iat=now }
9. FamilyStore.Save → auth:family:{user_id}:fid（state 含 utype/role/abs_exp 等所有重簽必要欄位）
   + SADD auth:user_families:{user_id}
10. AuditLogger.Log(login_success, user_id, fid, client_id, ip, ua)
11. Response:
    {
      "access_token": "...",
      "refresh_token": "...",
      "token_type": "Bearer",
      "expires_in": 900,
      "refresh_expires_in": 3600
    }
```

#### Access token 驗證（hot path，純 stateless）

```
1. 從 Authorization: Bearer <token> 取 access token
2. JWT 簽章驗證
3. 檢查 iss、aud、exp
4. 檢查 access_jti 是否在短期黑名單（強制踢人才會命中；大多情況為 miss）
5. SetClaims(c, claims)
```

#### Refresh（POST /auth/refresh）

```
POST /auth/refresh
Body: { "refresh_token": "..." }

1. JWT 簽章驗證 + iss + aud + exp + abs_exp > now
   - exp 已過 → 401 token_expired
   - abs_exp 已過 → 401 absolute_expired
   - 其他驗證失敗 → 401 invalid_token
2. 取出 user_id, fid, presented_jti
3. new_jti = uuid.New()
4. 呼叫 FamilyStore.Rotate Lua CAS：
     輸入: user_id, fid, presented_jti, new_jti, grace_window
     讀取 Redis state，根據 state.AbsoluteExp 計算剩餘 TTL
     ├─ Rotated         → 寫入新 state（current=new_jti, previous=presented_jti,
     │                     previous_response_until=now+grace, last_rotated_at=now）
     │                   → handler 簽兩個新 token，回 200
     ├─ GraceHit        → 不變更 state，回傳目前 state
     │                   → handler 用 state.CurrentJTI 重簽 refresh、簽新 access、回 200
     └─ ReplayDetected  → DEL family + SREM index（Lua 內完成）
                        → AuditLogger.Log(replay_detected, user_id, fid, presented_jti,
                                          state.current_jti, ip, ua)
                        → 回 401 replay_detected
```

##### GraceHit 的 handler 行為

> 這是 grace window 設計的關鍵實作細節，避免實作者各自猜「等價回應」的意思。

```
state = FamilyStore.Rotate 回傳的 FamilyState（GraceHit 時 state 未變更）

new_access_jwt = sign access claims with:
  jti = uuid.New()                       # access token 每次都新 jti
  iat = now
  exp = now + access_ttl                 # 15 min
  fid = state.FamilyID
  sub, utype, role, aud, iss = 取自 state / config

new_refresh_jwt = sign refresh claims with:
  jti = state.CurrentJTI                 # 關鍵：沿用 state.current_jti，不換新
  iat = now
  exp = now + client_policy.RefreshTTL   # 重新計算
  abs_exp = state.AbsoluteExp            # 必須從 state 拿，不能信任 client presented JWT
  fid = state.FamilyID
  sub, utype, aud, iss = 取自 state / config

回傳 200 與 Rotated 完全相同的 response shape
```

**為什麼這樣設計**：
- 客戶端因為網路失敗沒收到 T1 的回應 → 拿著舊 jti 重試
- T2 時 server 重簽一個跟 T1 「邏輯等價」的 token 對：access 任意 jti（無狀態），refresh 沿用 `state.current_jti` 以確保下次 refresh 能命中 family state
- Lua state 不變更 → 不會把 grace window 推遠（避免攻擊者刷 grace 拉長攻擊窗）

#### Logout（POST /auth/logout）

```
Headers: Authorization: Bearer <access_token>
Body:    { "refresh_token": "..." }   # 可選，提供時一併撤銷對應 family

1. AuthMiddleware 驗證 access token → 取出 user_id, fid (from access claims)
2. 若 body 帶 refresh_token：驗簽 + 比對 fid 一致；不一致 → 400
3. FamilyStore.Revoke(user_id, fid)
4. AccessTokenBlacklist.Add(access_jti, ttl=remaining_exp)
5. AuditLogger.Log(logout, user_id, fid, ip)
6. 回 204 No Content
```

#### 撤銷指定裝置（DELETE /auth/sessions/:fid）

```
1. AuthMiddleware → 取出 user_id, current_fid
2. fid = path param
3. 不允許撤銷自己（current_fid == fid）→ 400 use_logout_instead
4. FamilyStore.Revoke(user_id, fid)
5. AuditLogger.Log(session_revoked, user_id, fid, operator=user_id)
6. 回 204
```

#### 全裝置登出（POST /auth/sessions/revoke-all）

```
1. AuthMiddleware → 取出 user_id, current_fid
2. FamilyStore.RevokeAll(user_id)    # 包含自己
3. AccessTokenBlacklist.Add(current_access_jti, ttl=remaining_exp)
4. AuditLogger.Log(revoke_all, user_id)
5. 回 204
```

#### 列出登入裝置（GET /auth/sessions）

```
1. AuthMiddleware → 取出 user_id, current_fid
2. FamilyStore.ListByUser(user_id)   # 內含 lazy cleanup
3. 回傳 [{
     fid, client_id, device_label, ip_at_login,
     created_at, last_rotated_at,
     is_current: (fid == current_fid)
   }]
```

### 重放偵測的行為

| 情境 | 結果 |
|---|---|
| 合法 rotation | Rotated，舊 jti 失效 |
| 攻擊者偷 token 後比合法者先 refresh | 合法者下次 refresh → ReplayDetected → family 廢，雙方都被踢 |
| 合法者先 refresh，攻擊者再用舊 token | 攻擊者 refresh → ReplayDetected → family 廢，合法者也被踢 |
| 同 client 因網路重試 10 秒內用同 jti 兩次 | GraceHit，不觸發重放，回傳等價回應 |
| 攻擊者拿過期 token | JWT 簽章/exp 驗證階段就拒絕，根本進不到 Lua |
| 同瀏覽器多分頁並發 refresh | 前端必須以 BroadcastChannel / Web Lock 協調；超出 grace window 仍會觸發重放 |

> **權衡**：重放偵測一旦觸發，合法使用者也會被連帶踢出。代價：使用者需重新登入一次。換來的是攻擊者最多只能拿到一個 access token cycle（≤ 15 分鐘）的存取權。相較於攻擊者無限續期，這個權衡完全划算。

### 前端配合契約（必要）

下列為**對前端的硬性契約**，違反任一條會破壞安全模型：

| 項目 | 要求 |
|---|---|
| 儲存 access token | In-memory variable；不可進 storage |
| 儲存 refresh token | In-memory（首選）/ sessionStorage（次選）；**禁 localStorage** |
| Refresh 失敗 | **不可自動重試**；一律走 login 流程 |
| 多分頁 / 多 worker | 必須以 `BroadcastChannel` 或 `navigator.locks.request()` 協調 refresh，避免並發送同 jti |
| CSP | `Content-Security-Policy` 嚴格 nonce-based，禁 inline script、禁 `unsafe-eval` |
| 輸出跳脫 | 嚴禁 `dangerouslySetInnerHTML` / `v-html` 接未過濾資料 |
| CMS idle timer | 監聽 mouse/keyboard，15 分鐘無互動 → 呼叫 `/auth/logout` + 清掉記憶體 token + 跳登入頁 |
| Login 必填 | `client_id`（`cms-web` / `public-web` / `ios-app` / `android-app`） |

> 後端 1 小時無 API 自動失效：靠 refresh token sliding TTL = 1 小時（CMS policy）。活躍使用者每 15 分鐘 auto-refresh 一次延長 TTL；若真的 1 小時沒打任何 API，refresh token JWT exp 自然失效。

### 多裝置與多 client 隔離

- 同一帳號每次 login → 不同 `fid`，互相不影響
- 不同 client（CMS / web / app）→ 不同 `aud`，跨 client 偷 token 也會被擋
- `device_label` 從 `User-Agent` 解析（純 UI 標籤，非安全控制）
- 「目前登入裝置」管理頁：`GET /auth/sessions` 即可
- 「登出其他裝置」：`DELETE /auth/sessions/:fid`
- 「全裝置登出」：`POST /auth/sessions/revoke-all`

### Audit Log

新增 `pkg/audit` 模組，初版以 zap 結構化日誌實作（共用日誌通道，以 `event_type` 區分），未來可改為專用 stream / DB 表而不改 caller。

```go
// pkg/audit/audit.go
type EventType string

const (
    EventLoginSuccess     EventType = "auth.login_success"
    EventLoginFailed      EventType = "auth.login_failed"
    EventTokenRotated     EventType = "auth.token_rotated"
    EventReplayDetected   EventType = "auth.replay_detected"     // ⚠️ 觸發告警
    EventLogout           EventType = "auth.logout"
    EventSessionRevoked   EventType = "auth.session_revoked"
    EventRevokeAll        EventType = "auth.revoke_all"
)

type AuthEvent struct {
    Type        EventType
    UserID      string
    FamilyID    string
    ClientID    string
    IP          string
    UserAgent   string
    Extra       map[string]any // 例: replay 事件帶 {presented_jti, current_jti, delta_sec}
}

type Logger interface {
    Log(ctx context.Context, event AuthEvent)
}
```

**Replay 事件必須觸發告警**：搭配 metrics `auth_replay_detected_total` counter，運維可設長期高頻告警規則（單一使用者頻繁 replay 通常代表帳號被盯上）。

---

## 影響

### 正面

- 真正支援多裝置同時登入，符合現代使用者預期
- 重放偵測符合 OAuth 2.0 Security BCP（RFC 6749 / draft-ietf-oauth-security-topics）
- Hot path（access token 驗證）保持 stateless，Redis 故障不影響 99% 流量
- 不同 client 政策獨立，未來加新 client 不改 code
- Family + abs_exp 雙重控制：滑動 idle timeout + 絕對最長 session
- Audit log 滿足合規需求
- 純 JWT 契約對所有 client 一致，少一層 cookie/CSRF 處理複雜度

### 負面

- 比 ADR 002 複雜：多了 family 概念、grace window、policy 表、audit log
- Redis 是 refresh 操作的單點：故障時所有人需重登（fail-safe，但 UX 衝擊）
- 合法者與攻擊者搶 rotation 時，合法者也會被踢；需要前端配合「不要自動重試 refresh」並 UI 引導重登
- 同瀏覽器多分頁並發需前端做協調（Web Lock / BroadcastChannel）
- **XSS 風險集中在前端**：refresh token 在 JS 可讀範圍，必須以 CSP + 嚴格輸出跳脫 + 依賴審查綜合壓制

### 依賴

- Redis 必須設 `maxmemory-policy noeviction`（建議獨立 instance 或專用 DB index）
- 前端必須履行「前端配合契約」全部要求
- 後端 audit log sink 至少能接受 zap 結構化輸出（後續可移專用 stream）

### 未來工作

1. 多服務拆解時升級至 RS256：開新 ADR，定義 key rotation 流程與 JWKS endpoint
2. 若引入 SSO / 第三方嵌入場景，重新評估改用 HttpOnly cookie 傳輸 refresh token
3. Audit log 由 zap 共用通道移至專用 SIEM / 安全資料庫
4. 評估從 bcrypt 升級至 argon2id（OWASP 2026 推薦）；目前以 bcrypt 取通用性與 Go 生態成熟度

---

## Secret Rotation Runbook（HS256 階段）

本節定義 HS256 對稱 secret 的安全替換程序。RS256 升級後流程不同，屆時另起 ADR。

### 觸發時機

- **計畫性**：每 6 個月例行 rotation。
- **緊急性**：疑似 secret 洩漏（CI artifact 含 token、員工離職、第三方滲透事件）。

### Manager 驗證行為

`pkg/jwt.NewManager` 支援雙 secret：

- **簽章**：永遠用 `JWT_SECRET` / `JWT_REFRESH_SECRET`（主 secret）。
- **驗證**：先試主 secret；失敗再試 `JWT_SECRET_PREVIOUS` / `JWT_REFRESH_SECRET_PREVIOUS`（若有設定）。兩把都失敗才回 `ErrInvalidToken`。

副作用：rotation 期間驗證成本變 2 倍（最差情況），但對 hot path 只是兩次 HMAC，可忽略。

### 程序

1. **準備新 secret**：產生 ≥ 32 字元高熵亂數（如 `openssl rand -base64 48`）。secret 必須與現有 `JWT_SECRET` / `JWT_REFRESH_SECRET` 不同（`nefield=Secret` 仍然強制）。
2. **部署 grace 期間**：
   - `JWT_SECRET_PREVIOUS` ← 舊 `JWT_SECRET`
   - `JWT_SECRET` ← 新 secret
   - `JWT_REFRESH_SECRET_PREVIOUS` ← 舊 `JWT_REFRESH_SECRET`
   - `JWT_REFRESH_SECRET` ← 新 secret
   - 滾動部署所有節點。
3. **等待舊 token 自然過期**：
   - Access token 最多 `JWT_ACCESS_TTL`（15 分鐘）。
   - Refresh token 最多 client policy 的 `ABSOLUTE_TTL`（CMS 8h / public-web 24h / mobile 180d）。
   - **計畫性 rotation 等到最長 client policy `abs_exp` 過完**（保守做法，使用者完全無感）。
   - **緊急 rotation 不等**：跳到步驟 4 + 配套（見下）。
4. **清空舊 secret**：移除 `JWT_SECRET_PREVIOUS` / `JWT_REFRESH_SECRET_PREVIOUS`，再次滾動部署。

### 緊急 rotation 的配套

若懷疑 secret 已洩漏，不能等 mobile 180 天 abs_exp，必須：

1. 立刻執行步驟 2（部署新 secret），同時：
2. 呼叫所有使用者的 `FamilyStore.RevokeAll`（或直接 `FLUSHDB` 對應 Redis instance），**全員強制重登**。
3. 寫一筆 `auth.revoke_all` audit event 標 `reason=secret_compromise`，運維信箱通知。
4. 一週後執行步驟 4，期間 grace 用於最後遊離的 refresh 重試。

### 驗證

Rotation 完成後，CI 或維運腳本驗證：

```bash
# 應該存在
test -n "$JWT_SECRET"
# 應該不存在（已清空）
test -z "$JWT_SECRET_PREVIOUS"
```

並查看 `auth_login_attempts_total{result="success"}` 在 rotation 視窗內無顯著下降。

### 後續工作（實作）

1. 實作 `pkg/jwt` token signer / verifier（HS256；驗 iss / aud / exp / abs_exp）
2. 實作 `pkg/redis/family_store.go`（含 Lua scripts、lazy cleanup）
3. 實作 `pkg/audit`（zap 實作）
4. 實作 `/auth/login`、`/auth/refresh`、`/auth/logout`、`/auth/sessions`、`DELETE /auth/sessions/:fid`、`/auth/sessions/revoke-all` handler
5. 前端：BroadcastChannel 協調、idle timer、refresh-不重試規範、CSP 設定
