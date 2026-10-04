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

COPY apps/document-service/go.mod apps/document-service/go.sum ./apps/document-service/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
COPY packages/serviceauth/go.mod packages/serviceauth/go.sum ./packages/serviceauth/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/document-service && go mod download

COPY apps/document-service ./apps/document-service
COPY packages/gen ./packages/gen
COPY packages/serviceauth ./packages/serviceauth
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/document-service && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-service ./cmd/document-service && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/document-service-healthcheck ./cmd/document-service-healthcheck

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/document-service ./document-service
COPY --from=builder /out/document-service-healthcheck ./document-service-healthcheck
COPY --chown=app:app apps/document-service/configs ./configs

ENV DOCUMENT_SERVICE_CONFIG=/app/configs/config.yaml

USER app

EXPOSE 18081 18091

CMD ["/app/document-service"]
