# Build the Go application separately so the runtime image contains only what
# is needed to start the service.
FROM golang:1.26.8-alpine AS builder

ARG GOPROXY=https://proxy.golang.org,direct
# GOWORK=off is deliberate. The module declares packages/gen and
# packages/serviceauth through relative replace directives and CI proves it
# builds standalone, so the image does not need the workspace file - and must not
# use it: go.work lists every module in the repository, and a build context that
# contains only this application would fail while loading the others.
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local GOWORK=off

WORKDIR /src

COPY apps/gin-backend/go.mod apps/gin-backend/go.sum ./apps/gin-backend/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
COPY packages/serviceauth/go.mod packages/serviceauth/go.sum ./packages/serviceauth/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/gin-backend && go mod download

COPY apps/gin-backend ./apps/gin-backend
COPY packages/gen ./packages/gen
COPY packages/serviceauth ./packages/serviceauth
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/gin-backend && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gin-backend ./cmd/server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/gin-backend ./gin-backend
COPY --chown=app:app apps/gin-backend/configs ./configs
RUN mkdir -p storage/uploads storage/logs \
    && chown -R app:app storage

# Set GIN_CONFIG_PATH to a mounted configuration file in container deployments
# to supply the PostgreSQL/Redis service addresses and production JWT keys.
ENV GIN_CONFIG_PATH=/app/configs/config.yaml

USER app

EXPOSE 8080

CMD ["/app/gin-backend"]
