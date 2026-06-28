.PHONY: lint test-unit test-integration build security migrate-up help

lint:
	@echo "Running linters..."
	golangci-lint run ./...
	npx -y @redocly/cli@1.25.0 lint schema/openapi.yaml

test-unit:
	@echo "Running unit tests..."
	go test -race -count=1 -cover ./...

test-integration:
	@echo "Running integration tests..."
	docker compose -f docker-compose.test.yml up -d
	go test -race -count=1 -tags integration -cover ./... || (docker compose -f docker-compose.test.yml down; exit 1)
	docker compose -f docker-compose.test.yml down

test: test-unit test-integration

security:
	@echo "Running security scans..."
	go install golang.org/x/vuln/cmd/govulncheck@v1.1.3
	govulncheck ./...
	go install github.com/securego/gosec/v2/cmd/gosec@v2.21.4
	gosec ./...

build:
	@echo "Building binary..."
	go build \
	  -ldflags "-X main.Version=$$(git describe --tags --always) \
	            -X main.Commit=$$(git rev-parse HEAD)" \
	  -o bin/server ./cmd/server

migrate-up:
	@echo "Running migrations..."
	migrate -path ./migrations -database "$(DB_URL)" up

help:
	@echo "Available targets:"
	@echo "  lint              - Run linters"
	@echo "  test-unit         - Run unit tests"
	@echo "  test-integration  - Run integration tests"
	@echo "  test              - Run all tests"
	@echo "  security          - Run security scans"
	@echo "  build             - Build binary"
	@echo "  migrate-up        - Run database migrations"
