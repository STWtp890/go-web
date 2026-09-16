# Build the Go application separately so the runtime image contains only what
# is needed to start the service.
FROM golang:1.26.8-alpine AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local

WORKDIR /src

COPY go.work go.work.sum ./
COPY apps/gin-backend/go.mod apps/gin-backend/go.sum ./apps/gin-backend/
COPY apps/mixin-search/go.mod apps/mixin-search/go.sum ./apps/mixin-search/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/gin-backend && go mod download

COPY apps/gin-backend ./apps/gin-backend
COPY packages/gen ./packages/gen
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/gin-backend && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gin-backend ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-index-worker ./cmd/document-index-worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-index-admin ./cmd/document-index-admin

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/gin-backend ./gin-backend
COPY --from=builder /out/document-index-worker ./document-index-worker
COPY --from=builder /out/document-index-admin ./document-index-admin
COPY --chown=app:app apps/gin-backend/configs ./configs
RUN mkdir -p storage/uploads storage/logs \
    && chown -R app:app storage

# Set GIN_CONFIG_PATH to a mounted configuration file in container deployments
# to supply the PostgreSQL/Redis service addresses and production JWT keys.
ENV GIN_CONFIG_PATH=/app/configs/config.yaml

USER app

EXPOSE 8080

CMD ["/app/gin-backend"]
