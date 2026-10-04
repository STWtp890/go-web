# Build the Go application separately so the runtime image contains only what is
# needed to start the service.
FROM golang:1.26.8-alpine AS builder

ARG GOPROXY=https://proxy.golang.org,direct
# GOWORK=off: the module resolves packages/gen and packages/serviceauth through
# relative replace directives, so it builds without the workspace file. Using
# go.work here would make the build depend on every other module in the
# repository being present in the build context.
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local GOWORK=off

WORKDIR /src

COPY apps/qq-search/go.mod apps/qq-search/go.sum ./apps/qq-search/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
COPY packages/serviceauth/go.mod packages/serviceauth/go.sum ./packages/serviceauth/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/qq-search && go mod download

COPY apps/qq-search ./apps/qq-search
COPY packages/gen ./packages/gen
COPY packages/serviceauth ./packages/serviceauth
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/qq-search && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/qq-search ./cmd/qq-search && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/qq-search-healthcheck ./cmd/qq-search-healthcheck

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/qq-search ./qq-search
COPY --from=builder /out/qq-search-healthcheck ./qq-search-healthcheck
COPY --chown=app:app apps/qq-search/configs ./configs

ENV QQ_SEARCH_CONFIG=/app/configs/config.yaml

USER app

EXPOSE 18083 18093

CMD ["/app/qq-search"]
