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

COPY apps/document-search/go.mod apps/document-search/go.sum ./apps/document-search/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
COPY packages/serviceauth/go.mod packages/serviceauth/go.sum ./packages/serviceauth/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/document-search && go mod download

COPY apps/document-search ./apps/document-search
COPY packages/gen ./packages/gen
COPY packages/serviceauth ./packages/serviceauth
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/document-search && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-search ./cmd/document-search && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-search-healthcheck ./cmd/document-search-healthcheck

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/document-search ./document-search
COPY --from=builder /out/document-search-healthcheck ./document-search-healthcheck
COPY --chown=app:app apps/document-search/configs ./configs

ENV DOCUMENT_SEARCH_CONFIG=/app/configs/config.yaml

USER app

EXPOSE 18082 18092

CMD ["/app/document-search"]
