# ADR 001 — ORM 選型

**狀態**：已採用  
**日期**：2026-06-27

---

## 背景

專案需要一個 ORM 來管理 PostgreSQL 的資料存取。Go 生態中主要有三個選擇：GORM、ent、sqlc。

## 各方案評估

| 方案 | 優點 | 缺點 |
|------|------|------|
| **GORM** | 上手成本低、文件豐富、社群最大、支援 auto-migrate | 有 ORM 魔法，複雜查詢需搭配 `db.Raw()` |
| **ent** | 型別安全、code-gen、適合複雜 schema | code-gen 流程較重，學習曲線陡 |
| **sqlc** | SQL-first、無魔法、效能極致 | 不算 ORM，需手寫 SQL，維護成本高 |

## 決策

選擇 **GORM v2**（`gorm.io/gorm` + `gorm.io/driver/postgres`）。

理由：
- 本專案為查詢工具，schema 相對簡單，不需要 ent 的 code-gen 複雜度
- 快速迭代階段，GORM 的低上手成本優先於極致效能
- 複雜查詢可搭配 `db.Raw()` 逃脫 ORM 限制，不受鎖死

## 影響

- **正面**：開發速度快，repository 層實作簡潔
- **負面**：若未來 schema 大幅複雜化，可能需評估遷移至 ent
- **規範**：禁止使用 GORM AutoMigrate 於 production，所有 schema 變更透過 golang-migrate 版本化腳本管理
