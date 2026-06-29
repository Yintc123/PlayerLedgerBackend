.PHONY: help build build-lambda-zip run migrate-up seed lint test test-unit test-integration security fmt clean docker-up docker-down

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Build & Run
# ═══════════════════════════════════════════════════════════════════════════════════════════════

build:
	@echo "Building binary..."
	@mkdir -p bin
	go build \
	  -ldflags "-X main.Version=$$(git describe --tags --always) \
	            -X main.Commit=$$(git rev-parse HEAD)" \
	  -o bin/server ./cmd/server
	@echo "✓ Binary built: bin/server"

run: build
	@echo "Starting server..."
	./bin/server

# 打包成 AWS Lambda ZIP（provided.al2023 + 架構 arm64 + Lambda Web Adapter layer）。
# 不需 Docker、不需 ECR；產出 bin/lambda.zip 直接在 Lambda Console 上傳。
# 執行檔須命名 bootstrap 並置於 zip 根目錄（custom runtime 慣例）。
# 部署步驟見 .aws/README-lambda.md。
build-lambda-zip:
	@echo "Building Lambda ZIP (provided.al2023, arm64, LWA layer)..."
	@mkdir -p bin
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
	  -trimpath -tags=netgo,osusergo \
	  -ldflags "-s -w -X main.Version=$$(git describe --tags --always) \
	            -X main.Commit=$$(git rev-parse HEAD)" \
	  -o bin/bootstrap ./cmd/server
	zip -j -q bin/lambda.zip bin/bootstrap
	@echo "✓ bin/lambda.zip ready — upload in Lambda console"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Database
# ═══════════════════════════════════════════════════════════════════════════════════════════════

# Create .env from .env.example if it doesn't exist
.env:
	@echo "Creating .env from .env.example..."
	@cp .env.example .env
	@echo "✓ .env created (adjust values as needed)"

migrate-up: .env
	@echo "Running migrations..."
	migrate -path ./migrations \
	  -database "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}" \
	  up
	@echo "✓ Migrations completed"

migrate-down:
	@echo "Rolling back last migration..."
	migrate -path ./migrations \
	  -database "postgres://${DB_USER}:${DB_PASSWORD}@${DB_HOST}:${DB_PORT}/${DB_NAME}?sslmode=${DB_SSLMODE}" \
	  down 1
	@echo "✓ Rollback completed"

# 寫入開發假資料：20 筆 members + 50 筆 deposit_records（冪等，可重複執行）。
# 嚴禁在 prod（APP_ENV=prod 會中止）。seed 玩家密碼見程式輸出。
seed: .env
	@echo "Seeding dev fake data (20 members + 50 deposit records)..."
	go run ./cmd/seed
	@echo "✓ Seed completed"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Quality Checks
# ═══════════════════════════════════════════════════════════════════════════════════════════════

lint:
	@echo "Running Go linter..."
	golangci-lint run ./...
	@echo "Linting OpenAPI schema..."
	npx -y @redocly/cli@1.25.0 lint schema/openapi.yaml
	@echo "✓ All linters passed"

fmt:
	@echo "Formatting Go code..."
	go fmt ./...
	@echo "✓ Formatting complete"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Testing
# ═══════════════════════════════════════════════════════════════════════════════════════════════

test-unit:
	@echo "Running unit tests..."
	go test -race -count=1 -cover ./...
	@echo "✓ Unit tests passed"

test-integration: docker-up
	@echo "Running integration tests..."
	go test -race -count=1 -tags integration -cover ./... || \
	  ($(MAKE) docker-down; exit 1)
	@$(MAKE) docker-down
	@echo "✓ Integration tests passed"

test: test-unit test-integration

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Security
# ═══════════════════════════════════════════════════════════════════════════════════════════════

security:
	@echo "Running security scans..."
	go install golang.org/x/vuln/cmd/govulncheck@v1.1.3
	govulncheck ./...
	go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4
	gosec ./...
	@echo "✓ Security checks passed"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Docker (for testing)
# ═══════════════════════════════════════════════════════════════════════════════════════════════

docker-up:
	@echo "Starting test containers (PostgreSQL + Redis)..."
	docker compose -f docker-compose.test.yml up -d
	@sleep 2  # Wait for services to be ready
	@echo "✓ Test containers running"

docker-down:
	@echo "Stopping test containers..."
	docker compose -f docker-compose.test.yml down
	@echo "✓ Test containers stopped"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Utilities
# ═══════════════════════════════════════════════════════════════════════════════════════════════

clean:
	@echo "Cleaning up..."
	rm -rf bin/
	go clean
	@echo "✓ Clean complete"

# ═══════════════════════════════════════════════════════════════════════════════════════════════
# Help
# ═══════════════════════════════════════════════════════════════════════════════════════════════

help:
	@echo "==================================================================="
	@echo "PlayerLedger Backend — Makefile Targets"
	@echo "==================================================================="
	@echo ""
	@echo "Build & Run:"
	@echo "  make build              - Build binary (bin/server)"
	@echo "  make build-lambda-zip   - Build AWS Lambda ZIP (bin/lambda.zip; no Docker/ECR)"
	@echo "  make run                - Build and run server"
	@echo ""
	@echo "Database:"
	@echo "  make migrate-up         - Run pending migrations"
	@echo "  make migrate-down       - Rollback last migration"
	@echo ""
	@echo "Quality Checks:"
	@echo "  make lint               - Run golangci-lint + OpenAPI lint"
	@echo "  make fmt                - Format Go code"
	@echo ""
	@echo "Testing:"
	@echo "  make test-unit          - Run unit tests"
	@echo "  make test-integration   - Run integration tests (with containers)"
	@echo "  make test               - Run all tests (unit + integration)"
	@echo ""
	@echo "Security:"
	@echo "  make security           - Run govulncheck + gosec"
	@echo ""
	@echo "Docker (for testing):"
	@echo "  make docker-up          - Start test containers"
	@echo "  make docker-down        - Stop test containers"
	@echo ""
	@echo "Utilities:"
	@echo "  make clean              - Clean build artifacts"
	@echo "  make help               - Show this help message"
	@echo ""
	@echo "Typical workflow:"
	@echo "  1. make fmt && make lint"
	@echo "  2. make test"
	@echo "  3. make security"
	@echo "  4. make build"
	@echo "  5. make run"
	@echo ""
	@echo "Tips:"
	@echo "  - Copy .env.example → .env and adjust values"
	@echo "  - DB migrations auto-run on server start"
	@echo "  - Use docker-compose.test.yml for integration tests"
	@echo "==================================================================="

.DEFAULT_GOAL := help
