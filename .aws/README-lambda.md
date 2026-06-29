# AWS 部署 — API Gateway + Lambda（ZIP + LWA layer，全程 Console）

> 試水溫用路線。正式生產建議走 ECS/Fargate（見 [ecs-task-definition.json](./ecs-task-definition.json)）。
> 做法：把現有 Gin HTTP server 編成一顆執行檔,以 **ZIP** 上傳到 Lambda,搭 **Lambda Web Adapter (LWA) layer**
> 當 runtime 代理。**零 Go 程式碼改動、不需 Docker、不需 ECR**。

> 容器版（`Dockerfile.lambda` + `template.yaml`）仍保留作備案,但那條路要 ECR + `docker push`;
> 本文件是不碰 ECR 的 ZIP 版,推薦先用這個。

## 為什麼 ZIP 不用 ECR

Lambda 兩種打包:**ZIP**(直接上傳,本案 ~10MB) vs **容器映像**(必須放 ECR)。選 ZIP 就沒 ECR 的事。
migrations 已 `//go:embed` 進 binary,所以 zip 只要一顆執行檔就完整。

---

## 0. 本機:打包 ZIP（唯一一行終端機指令）

```bash
make build-lambda-zip      # 產出 bin/lambda.zip（arm64、bootstrap 置於 zip 根目錄）
```
> 你是 Apple Silicon,target 已設 `GOARCH=arm64`;Lambda 等下也選 arm64,一致且較便宜。

---

## A. 探測現有資源（EC2 / VPC Console，region: ap-southeast-2）

1. **EC2 → Instances** → 點開 pg/redis VM,記下:**Private IPv4**(當 `DB_HOST`/`REDIS_HOST`)、**VPC ID**、**Security group**(VM 的 SG)。
2. **VPC → Subnets**(篩該 VPC)→ 找 **2 個私有子網**(不同 AZ、Auto-assign public IP = No),記 `subnet-xxxx`。
3. **VPC → NAT Gateways** → 確認該 VPC 有 NAT 且 Available(Lambda 在私有子網要靠它寫 CloudWatch / 連 Secrets Manager API)。

## B. 網路放行（EC2 → Security Groups）

4. **Create security group**:名稱 `playerledger-lambda-sg`、選上面 VPC、Inbound 留空。記下 `<LAMBDA_SG>`。
5. 編輯 **VM 的 SG → Inbound rules**,新增兩條,Source 都選 `<LAMBDA_SG>`:
   - Type **PostgreSQL**(5432)
   - Type **Custom TCP**,Port **6379**(Redis)

## C. Secrets（Secrets Manager Console）

6. 開 `jko_vm_pg_redis_1` → Retrieve,確認含 `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`(缺則補)。
7. 準備好 `JWT_SECRET`(≥32)、`JWT_REFRESH_SECRET`(≥32 且不同)、`ADMIN_PASSWORD`(≥12)。
   （本路線直接把值貼進 Lambda 環境變數;勿寫進 git。）

## D. 建 Lambda（Lambda Console → Create function → Author from scratch）

8. **Author from scratch**。Function name `playerledger-backend`。
   - **Runtime**:`Amazon Linux 2023`（即 `provided.al2023`，custom runtime）
   - **Architecture**:`arm64`
   - Create function。
9. **Code → Upload from → .zip file** → 上傳 `bin/lambda.zip`。
   - **Runtime settings → Handler** 設為 `bootstrap`(custom runtime 慣例;LWA 模式下實際不用它定位程式,填著即可)。
10. **加 LWA layer**:**Code → Layers → Add a layer → Specify an ARN**,填:
    ```
    arn:aws:lambda:ap-southeast-2:753240598075:layer:LambdaAdapterLayerArm64:28
    ```
    > 版本號 `:28` 請對一下 LWA README「Lambda layers」最新值(`753240598075` 是 LWA 官方帳號)。架構務必選 arm64 對應的 `...Arm64` layer。
11. **Configuration → General configuration**:Memory **1024**、Timeout **30 sec**。
12. **Configuration → VPC**:選該 **VPC**、**2 個私有子網**、SG 選 `<LAMBDA_SG>`(存檔時 Console 會自動補執行角色的 ENI 權限)。
13. **Configuration → Environment variables** → 逐筆加入下表。
    > ⚠️ ZIP 沒有 Dockerfile,所以 **LWA 相關 4 個變數必須在這裡設**(容器版是烤在映像裡)。

**LWA / runtime（ZIP 必填）**

| Key | Value |
|---|---|
| AWS_LAMBDA_EXEC_WRAPPER | `/opt/bootstrap` |
| AWS_LWA_PORT | `8080` |
| AWS_LWA_READINESS_CHECK_PATH | `/health` |
| PORT | `8080` |

**App 設定（非機敏）**

| Key | Value |
|---|---|
| APP_ENV | `staging` |
| GIN_MODE | `release` |
| ALLOWED_ORIGINS | 前端 ALB 網址 |
| ALLOW_CREDENTIALS | `true` |
| TRUSTED_PROXIES | `0.0.0.0/0` |
| DB_HOST | VM 私有 IP |
| DB_PORT | `5432` |
| DB_SSLMODE | `disable`（VM 多半沒開 TLS;見注意） |
| DB_MAX_OPEN_CONNS | `2` |
| DB_MAX_IDLE_CONNS | `1` |
| DB_PREPARE_STMT | `false` |
| REDIS_HOST | VM 私有 IP |
| REDIS_PORT | `6379` |
| REDIS_DB | `0` |
| LOG_LEVEL / LOG_FORMAT / LOG_SERVICE | `info` / `json` / `playerledger` |
| RATE_LIMIT_ENABLED | `true` |
| RATE_LIMIT_IP_PERIOD / RATE_LIMIT_IP_MAX | `60` / `60` |
| RATE_LIMIT_USER_PERIOD / RATE_LIMIT_USER_MAX | `60` / `300` |
| METRICS_ENABLED | `false` |
| JWT_ISSUER | `playerledger` |
| JWT_ACCESS_TTL / JWT_GRACE_WINDOW / JWT_CLOCK_SKEW_LEEWAY | `900` / `10` / `30` |
| BCRYPT_COST | `12` |
| ADMIN_USERNAME | `admin` |

**Secrets（直接貼值）**

| Key | 值來源 |
|---|---|
| DB_USER / DB_PASSWORD / DB_NAME | `jko_vm_pg_redis_1` 的 POSTGRES_* |
| REDIS_PASSWORD | `jko_vm_pg_redis_1` 的 REDIS_PASSWORD |
| JWT_SECRET / JWT_REFRESH_SECRET / ADMIN_PASSWORD | C-7 準備的值 |

> JWT client policy（cms-web 等 TTL）程式有內建預設,**不用設**。

## E. 接 API Gateway（API Gateway Console）

14. **Create API → HTTP API → Build**。
15. **Add integration → Lambda** → 選 `playerledger-backend` → 命名 → Next。
16. **Routes**:加 **ANY** `/{proxy+}` 與 **ANY** `/`,都指到該 Lambda integration。
17. Stage 用預設 **`$default`（Auto-deploy）** → Create。複製 **Invoke URL**。

## F. 測試

```
<Invoke URL>/health        → {"status":"ok",...}
<Invoke URL>/health/ready  → DB / Redis / Lua 都通才 200
```
第一次是 cold start(順便跑 migration + admin seed),慢個幾秒正常。

---

## 更新版本(改了程式或設定後)

```bash
make build-lambda-zip
```
Lambda Console → **Code → Upload from → .zip file** 重新上傳 `bin/lambda.zip` 即可(env / layer / VPC 不用重設)。

## ⚠️ 已知限制 / 與 Fargate 的差異

1. **migration / admin seed 仍在 cold start 跑**(idempotent,但並發冷啟動會搶 migrate lock)。正式前建議抽成獨立步驟先跑。
2. **DB 連線**:每個執行環境各開池,已設 `DB_MAX_OPEN_CONNS=2`、`DB_PREPARE_STMT=false`;量大時 `DB_HOST` 應改指 RDS Proxy。
3. **Metrics 關閉**:Prometheus pull 模型在 Lambda 無效,`METRICS_ENABLED=false`。
4. **Secret 以純 env 存在 Lambda**(可被 `lambda:GetFunctionConfiguration` 讀到)。正式環境改用 AWS Parameters and Secrets Lambda Extension。
5. **DB_SSLMODE**:VM 自架 Postgres 沒開 TLS 時用 `disable`(因 `APP_ENV=staging` 才允許;prod 會被 config 擋)。VM 有 TLS 才改 `require`。
6. **client IP / 限流**:`TRUSTED_PROXIES=0.0.0.0/0` 僅試作;正式請收斂為 APIGW 來源,否則 `X-Forwarded-For` 可偽造繞過 IP 限流。
7. **冷啟動**:VPC + 連 DB/Redis + Lua SCRIPT LOAD + migration/seed 檢查,可能數百 ms~數秒;閒置數分鐘後環境被回收→下個請求吃滿冷啟動(要消除須 Provisioned Concurrency)。
8. **`/health/ready` 回 503**:看 Lambda 的 CloudWatch Logs,多半是 SG 沒放行、子網沒 NAT、或 secret 值貼錯。

## 之後想正式化?

改走 in-process adapter(`aws-lambda-go-api-proxy` + `lambda.Start`):把 `main.go` 抽成可重用 bootstrap、
新增 `cmd/lambda` 入口、migration/seed 移出啟動路徑。改動較大、需補測試(TDD),屆時再做。
