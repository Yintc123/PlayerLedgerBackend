# PlayerLedger Backend — 部署指南（§24）

## 目錄

1. [Docker Build](#docker-build)
2. [Kubernetes Deploy](#kubernetes-deploy)
3. [CI/CD Pipeline](#cicd-pipeline)
4. [本地開發](#本地開發)

---

## Docker Build

### 建置映像（指定版本）

```bash
# 本機建置
docker build \
  --build-arg VERSION=$(git describe --tags --always) \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  -t playerledger/api:$(git describe --tags --always) .

# 標記並推到 registry
docker tag playerledger/api:v1.0.0 ghcr.io/example/playerledger:v1.0.0
docker push ghcr.io/example/playerledger:v1.0.0
```

### 映像特性

- **Multi-stage build**：編譯階段 (~800MB) → runtime (~20MB distroless base)
- **Non-root user**：以 UID 65532（distroless nonroot）執行，符合容器安全最佳實踐
- **CGO_ENABLED=0**：純 Go 靜態編譯，避免 glibc 依賴
- **版本/Commit 注入**：透過 `-ldflags` 注入 `main.Version` / `main.Commit`，與 `metrics.BuildInfo` 對齐

### 驗證映像

```bash
# 檢查大小
docker images | grep playerledger

# 運行容器
docker run --rm -p 8080:8080 \
  -e DB_HOST=host.docker.internal \
  -e DB_USER=postgres \
  -e DB_PASSWORD=password \
  -e REDIS_HOST=host.docker.internal \
  -e JWT_SECRET='32-byte-secret-key-here-long!' \
  -e JWT_REFRESH_SECRET='32-byte-secret-key-here-long!' \
  ghcr.io/example/playerledger:v1.0.0
```

---

## Kubernetes Deploy

### 前置作業

```bash
# 創建 secret（包含敏感環境變數）
kubectl create secret generic playerledger-secrets \
  --from-literal=db-user=postgres \
  --from-literal=db-password='<db-password>' \
  --from-literal=redis-password='<redis-password>' \
  --from-literal=jwt-secret='<jwt-secret-32-bytes>' \
  --from-literal=jwt-refresh-secret='<jwt-refresh-secret-32-bytes>' \
  -n playerledger
```

### 部署應用

```bash
# 應用 k8s manifests
kubectl apply -f k8s/deployment.yaml

# 驗證部署
kubectl get deployment -n playerledger
kubectl get pods -n playerledger
kubectl logs -f deployment/playerledger -n playerledger
```

### k8s 資源

| 資源 | 說明 | 重要配置 |
|------|------|--------|
| **Namespace** | playerledger | 隔離應用及其依賴 |
| **Deployment** | 3 replicas | Rolling update，maxSurge=1 maxUnavailable=0 |
| **Service** | ClusterIP | 負載均衡至 3 pods |
| **ConfigMap** | playerledger-config | 非敏感環境變數 |
| **Secret** | playerledger-secrets | DB/JWT 密鑰（kubectl create secret） |
| **NetworkPolicy** | 限制 /metrics 存取 | 僅 monitoring namespace 的 Prometheus 可存取 |
| **ServiceAccount** | playerledger | Pod 身份（含 RBAC 角色） |

### 健康檢查配置

```yaml
livenessProbe:
  httpGet:
    path: /health        # simple status
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /health/ready  # db + redis + lua scripts check
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 2
```

### 安全上下文

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
```

### /metrics 隔離

NetworkPolicy 限制 `/metrics` 只能由 monitoring namespace 的 Prometheus 存取（§18.1）：

```yaml
- from:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: monitoring
    podSelector:
      matchLabels:
        app.kubernetes.io/name: prometheus
  ports:
  - protocol: TCP
    port: 8080
```

---

## CI/CD Pipeline

### GitHub Actions Workflow（§23）

五個 job 平行執行，`build` 依賴全部通過：

```
lint ────────────┐
schema-lint ─────┤
test-unit ───────┼──→ build
test-integration ┤
security ────────┘
```

### Jobs 說明

| Job | 用途 | 工具 |
|-----|------|------|
| **lint** | Go 代碼品質 | golangci-lint |
| **schema-lint** | OpenAPI schema 驗證 | @redocly/cli |
| **test-unit** | 單元測試 + coverage | go test -race |
| **test-integration** | 集成測試（真實 DB/Redis） | docker-compose services |
| **security** | 漏洞掃描 | govulncheck + gosec |
| **build** | 編譯驗證 | go build |

### 本地等效指令（Makefile）

```bash
make lint                  # Run linters
make test-unit            # Run unit tests
make test-integration     # Run integration tests
make test                 # Run all tests
make security             # Run security scans
make build                # Build binary
make help                 # Show all targets
```

---

## 本地開發

### 快速啟動（Docker Compose）

```bash
# 啟動 PostgreSQL + Redis（測試環境）
docker compose -f docker-compose.test.yml up -d

# 執行 integration tests
go test -race -count=1 -tags integration ./...

# 清理
docker compose -f docker-compose.test.yml down
```

### 環境變數示例（.env）

```env
APP_ENV=dev
PORT=8080
GIN_MODE=debug
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=playerledger
REDIS_HOST=localhost
REDIS_PORT=6379
JWT_SECRET=super-secret-key-32-bytes-minimum!!
JWT_REFRESH_SECRET=super-secret-key-32-bytes-minimum!!
LOG_LEVEL=debug
LOG_FORMAT=console
```

### 編譯 + 執行

```bash
# 編譯（注入版本信息）
make build

# 執行
./bin/server

# 驗證
curl http://localhost:8080/health
curl http://localhost:8080/health/ready
curl http://localhost:8080/metrics
```

---

## 常見問題

### Q1: 如何在 k8s 中更新映像？

```bash
# 方案 1：修改 deployment 映像版本
kubectl set image deployment/playerledger \
  api=ghcr.io/example/playerledger:v1.0.1 \
  -n playerledger

# 方案 2：編輯 deployment
kubectl edit deployment playerledger -n playerledger
```

### Q2: 如何查看 Pod 日誌？

```bash
# 查看特定 pod
kubectl logs pod/playerledger-xxx -n playerledger

# 實時跟蹤
kubectl logs -f deployment/playerledger -n playerledger

# 顯示前 50 行
kubectl logs --tail=50 deployment/playerledger -n playerledger
```

### Q3: 如何驗證健康檢查？

```bash
# 進入 pod 並執行 curl
kubectl exec -it pod/playerledger-xxx -n playerledger -- \
  /bin/sh -c "curl http://localhost:8080/health/ready"
```

### Q4: 如何檢查 /metrics 端點？

```bash
# 來自 monitoring namespace
kubectl exec -it pod/prometheus-xxx -n monitoring -- \
  /bin/sh -c "curl http://playerledger.playerledger.svc:80/metrics"
```

---

## 檢查清單

- [ ] Docker 映像編譯成功，大小 < 30MB
- [ ] 映像執行時 `curl /health` 返回 200
- [ ] `docker push` 到 registry（ghcr.io）
- [ ] k8s ConfigMap + Secret 建立
- [ ] `kubectl apply -f k8s/deployment.yaml` 部署
- [ ] 驗證 3 個 pods 皆 Running + Ready
- [ ] `curl /health/ready` 返回 200
- [ ] Prometheus 能抓到 `/metrics`
- [ ] 日誌檢查無錯誤

---

## 下一步

- [ ] 設定 Ingress（讓前端應用能存取）
- [ ] 配置 PostgreSQL StatefulSet + PersistentVolume
- [ ] 配置 Redis StatefulSet + PersistentVolume
- [ ] Prometheus + Grafana 監控
- [ ] CI/CD 自動化推送映像至 registry

