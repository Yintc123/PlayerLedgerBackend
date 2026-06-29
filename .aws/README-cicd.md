# CI/CD — GitHub Actions（對齊前端專案）

合併成**單一** `.github/workflows/ci.yml`（名稱 `CI/CD`），沿用前端
`PlayerLedgerFrontend/.github/workflows/` 的慣例（OIDC、secrets/vars 命名、cluster、sed 替換 task def）。

| Workflow | 觸發 | 做什麼 |
|---|---|---|
| `ci.yml`（`CI/CD`） | push/PR main、手動 | **CI**：lint / schema-lint / unit / integration / security / build；**CD**（僅 main push）：`build-image` → `deploy-staging` |
| `bootstrap.yml` | 手動 | 一次性註冊 task def revision 1（service 還沒建時用），同前端 |

## 與前端共用的設定

| 名稱 | 類型 | 值 / 說明 |
|---|---|---|
| `AWS_DEPLOY_ROLE_ARN` | **Secret** | GitHub OIDC assume 的 IAM role ARN |
| `AWS_ACCOUNT_ID` | **Secret** | 12 位帳號 ID（sed 填入 task def 的 `ACCOUNT`） |
| `EC2_PRIVATE_IP` | **Variable** | pg/redis VM 私有 IP `172.31.6.98`（填入 `DB_HOST`/`REDIS_HOST` 的 `EC2_PRIVATE_IP`） |
| cluster | 寫死在 `ci.yml` env | `warmhearted-crocodile-18gqvo-jko-cluster`（前端同一個） |
| service | 寫死在 `ci.yml` env | `playerledger-backend`（你建 service 時用此名） |
| region | — | `ap-southeast-2` |

> 這三個 `secrets` / `vars` 若前端是設在 **organization 層級**，後端 repo 可直接沿用；
> 若是 repo 層級，需在後端 repo 的 **Settings → Secrets and variables → Actions** 各設一份。

## 部署流程

```
push main → CI 全綠
  → build-image：OIDC assume role → buildx(linux/amd64) → push ECR
                  （tag = commit SHA + latest，含 gha cache / SBOM / provenance，比照前端）
  → deploy-staging：sed 填 task def placeholder → render 換 image
                  → register 新 revision → update service（等 service 穩定）
```

後端**內部 only（無公網 ALB）**，故不做前端那種 public health check，改以
`wait-for-service-stability` 判定上線健康。

## 一次性設定

### 1. IAM OIDC role（若前端已建好且 trust 涵蓋本 repo，可略過）

- IAM → Identity providers：確認已有 `token.actions.githubusercontent.com`（OIDC，audience `sts.amazonaws.com`）。
- role `AWS_DEPLOY_ROLE_ARN` 的 **trust policy** 需涵蓋本 repo：
  ```json
  "StringLike": { "token.actions.githubusercontent.com:sub": "repo:<GH_ORG>/<BACKEND_REPO>:*" }
  ```
  （前端的 role 若 trust 只寫了前端 repo，要把後端 repo 也加進去，或另建一個 role。）
- **權限**需含：ECR push（`igs_backend` repo）、`ecs:RegisterTaskDefinition` / `DescribeTaskDefinition` /
  `UpdateService` / `DescribeServices`、`iam:PassRole`（指向 task def 的 `executionRoleArn` = `jko_ecs`，
  condition `iam:PassedToService=ecs-tasks.amazonaws.com`）。

### 2. 基礎設施（Console 建一次，CI 才有對象可更新）

1. ECR repo `igs_backend`。
2. Cloud Map namespace `playerledger.local`。
3. **task def revision 1**：跑 **`bootstrap.yml`**（Actions → Bootstrap Task Definition → Run workflow）。
4. 建 ECS service `playerledger-backend`（指向 `igs-backend:1`，含 Service Connect server 設定，
   見 `.aws/README.md`「Service Connect」）。
5. 之後 `git push main` 就自動部署。

## 注意

- **build 平台**：對齊前端用 `linux/amd64`（task def 已移除 ARM64 設定，預設 x86_64）。
- **`DB_SSLMODE=disable` / `APP_ENV=staging`**：因連自架 VM（無 TLS）且為 staging；prod 會被 config 擋下 disable。
- **DB migration**：目前跟 server 啟動跑（idempotent）。要更穩可在 deploy 後加一次性 migration task。
- 不想用 OIDC 的替代（access key）見 git 歷史或前端做法；建議仍走 OIDC。
