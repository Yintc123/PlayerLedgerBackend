# AWS 部署 — ECS Fargate

對齊前端做法（`PlayerLedgerFrontend/.aws/ecs-task-definition.json` 與 ADR 018 — AWS Secrets Manager
為 secret 儲存）：**secret 走 Secrets Manager，非 secret 走 ECS `environment[]`**。
程式碼不感知 secret backend——ECS agent 啟動時把 secret 注入成純環境變數，`config.Load()`
照舊讀 env（見 `docs/specs/infrastructure.md` §4.3），Go 不引入 AWS SDK。

## Secret 設定來源（單一共用 secret）

所有機敏值統一取自**與前端共用**的單一 secret `jko_vm_pg_redis_1`（pg + redis + JWT + admin 全在內）。
task definition `secrets[].name` 是注入容器的環境變數名，`valueFrom` ARN 結尾 `:<JSON_KEY>::`
指向該 secret 的 JSON key。注意**環境變數名與 JSON key 不一定同名**（如 `JWT_SECRET` 取自 `JWT_ACCESS_SECRET`）：

| 容器環境變數 (`name`) | 來源 secret | JSON key |
|---|---|---|
| `DB_PASSWORD`        | `jko_vm_pg_redis_1` | `POSTGRES_PASSWORD` |
| `REDIS_PASSWORD`     | `jko_vm_pg_redis_1` | `REDIS_PASSWORD` |
| `JWT_SECRET`         | `jko_vm_pg_redis_1` | `JWT_ACCESS_SECRET` |
| `JWT_REFRESH_SECRET` | `jko_vm_pg_redis_1` | `JWT_REFRESH_SECRET` |
| `ADMIN_PASSWORD`     | `jko_vm_pg_redis_1` | `ADMIN_PASSWORD` |

secret 內的 `SESSION_SECRET` 是前端用的，後端不引用。

`DB_USER` / `DB_NAME` **非機敏**，連同 `DB_HOST` / `DB_PORT` / `REDIS_HOST` / `REDIS_PORT` 一起留在
`environment[]` 純文字（`DB_USER=jko_vm`、`DB_NAME=playerledger`）。

> `JWT_ACCESS_SECRET` / `JWT_REFRESH_SECRET` 須 ≥ 32 字元且兩者不同；`ADMIN_PASSWORD` ≥ 12 字元
> （否則 `config.Validate()` 啟動即 fail-fast）。

## IAM

ECS **task execution role**（`executionRoleArn`，本檔為 `jko_ecs`）需要對 secret `jko_vm_pg_redis_1` ARN 的
`secretsmanager:GetSecretValue` 權限，ECS agent 才能在啟動時拉 secret 注入容器。若 secret 用了
自訂 KMS CMK，還需該 key 的 `kms:Decrypt`。

## Service Connect（前端 → 後端內網呼叫）

後端**內部 only、不開公網 ALB**，由前端 Next.js BFF 透過 **ECS Service Connect** 在共用
namespace 內呼叫。架構：

```
   公網 ─ ALB ─→ frontend service ──(Service Connect)──→ backend service
                 都掛 namespace: playerledger.local      （無公網入口）
                 frontend 用 http://backend:8080 呼叫
```

ECS 的「namespace」= **AWS Cloud Map namespace**（region 級資源，非屬某 cluster）。
**同 cluster 可有多個 namespace**（每個 service 各自於 `serviceConnectConfiguration` 指定），
但**要互相發現必須在同一個 namespace**。本案兩個 service 共用 `playerledger.local`。

### 一次性：建 namespace

```bash
aws servicediscovery create-http-namespace --name playerledger.local
# 或在 ECS Console → Cluster → 設定預設 namespace 時一併建立
```

### 後端 service（沿用前端 cluster）

1. **port 命名**：`ecs-task-definition.json` 的 `portMappings[].name = "http"` + `appProtocol = "http"`
   —— Service Connect 必需（無 name 的 port 無法被 advertise）。
2. **註冊 task def + 建 service**（`service-backend.json` 已含 `serviceConnectConfiguration`，
   advertise `discoveryName=backend`、`dnsName=backend:8080`）：
   ```bash
   aws ecs register-task-definition --cli-input-json file://.aws/ecs-task-definition.json
   # 先把 service-backend.json 的 <FRONTEND_CLUSTER_NAME> / <SUBNET_ID> / <BACKEND_SG> 填好
   aws ecs create-service --cli-input-json file://.aws/service-backend.json
   ```
   `<SUBNET_ID>` 用前端同一個（公有）子網；`assignPublicIp=ENABLED`（公有子網無 NAT 時，
   Fargate 要靠 public IP 拉 ECR image）。

### 前端 service（client 端，同 namespace）

前端的 ECS service 需**啟用 Service Connect 並掛同一個 namespace**（只當 client，不必 advertise
自己 → `serviceConnectConfiguration` 不需 `services` 區段）：

```json
"serviceConnectConfiguration": { "enabled": true, "namespace": "playerledger.local" }
```

並把前端 env **`API_BASE_URL` 改成 `http://backend:8080`**（取代現有 `https://httpbin.org` placeholder）。
此為前端 repo / 部署設定的變更。

### Security Group

後端 SG（`<BACKEND_SG>`）需放行 **inbound TCP 8080，Source = 前端 service 的 SG**。
（後端對 VM 的 DB/Redis 連線另循 `EC2_PRIVATE_IP`，由 VM 的 SG 放行 5432/6379。）

> 前端 BFF 是 **server 端**呼叫後端，**CORS 不適用**（CORS 只在瀏覽器強制），該路徑不依賴 `ALLOWED_ORIGINS`。

## 健康檢查

後端內部 only、**無 ALB target group**，故無 LB 層健康檢查；ECS 以 task 是否 running 判定。
runtime image 是 `distroless/static`（無 shell），也不能用前端那種 `CMD-SHELL` container
health check。若要更精準的健康判定，日後可加一顆 Go 健康檢查小程式當 `healthCheck.command`。
（`/health`、`/health/ready`、`/metrics` 為 ops 端點，不在 OpenAPI 契約內，見 infrastructure.md §11。）

## 套用前須替換的 placeholder

- `ecs-task-definition.json`：`REGION`、`ACCOUNT`、`REGISTRY`、`LATEST`、`EC2_PRIVATE_IP`（= `172.31.6.98`）、
  `ALLOWED_ORIGINS`（內網路徑可不理）。
- `service-backend.json`：`<FRONTEND_CLUSTER_NAME>`、`<SUBNET_ID>`、`<BACKEND_SG>`。
