# syntax=docker/dockerfile:1.7

# ===== builder =====
FROM golang:1.25-alpine AS builder
WORKDIR /src

# 利用 build cache：先放 go.mod / go.sum
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# 再放原始码
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ENV CGO_ENABLED=0 GOOS=linux

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
      -tags=netgo,osusergo \
      -o /out/server ./cmd/server && \
    go build \
      -trimpath \
      -ldflags="-s -w" \
      -tags=netgo,osusergo \
      -o /out/seed ./cmd/seed

# ===== runtime =====
FROM gcr.io/distroless/static-debian12:nonroot

USER nonroot:nonroot
WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /out/server /app/server
# /app/seed：一次性 seed 工具（CI 以 ECS run-task override command 執行，見 ci.yml seed-db job）
COPY --from=builder --chown=nonroot:nonroot /out/seed /app/seed
COPY --from=builder --chown=nonroot:nonroot /src/migrations /app/migrations

EXPOSE 8080
# 用 CMD（非 ENTRYPOINT）放預設執行檔：service 跑 /app/server；
# 一次性 seed 由 ECS run-task 以 containerOverrides.command=["/app/seed"] 取代 CMD。
# （ECS override 只能改 command=Docker CMD，不能改 ENTRYPOINT，故二進位不可寫死在 ENTRYPOINT。）
CMD ["/app/server"]
