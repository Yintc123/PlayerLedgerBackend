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
      -o /out/server ./cmd/server

# ===== runtime =====
FROM gcr.io/distroless/static-debian12:nonroot

USER nonroot:nonroot
WORKDIR /app

COPY --from=builder --chown=nonroot:nonroot /out/server /app/server
COPY --from=builder --chown=nonroot:nonroot /src/migrations /app/migrations

EXPOSE 8080
ENTRYPOINT ["/app/server"]
