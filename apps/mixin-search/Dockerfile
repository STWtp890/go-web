FROM golang:1.26.8-alpine AS builder

ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} GOTOOLCHAIN=local

WORKDIR /src

COPY go.work go.work.sum ./
COPY apps/gin-backend/go.mod apps/gin-backend/go.sum ./apps/gin-backend/
COPY apps/mixin-search/go.mod apps/mixin-search/go.sum ./apps/mixin-search/
COPY packages/gen/go.mod packages/gen/go.sum ./packages/gen/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd apps/mixin-search && go mod download

COPY apps/mixin-search ./apps/mixin-search
COPY packages/gen ./packages/gen
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd apps/mixin-search && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rag-server ./cmd/rag-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rag-healthcheck ./cmd/rag-healthcheck

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/rag-server ./rag-server
COPY --from=builder /out/rag-healthcheck ./rag-healthcheck

USER app
EXPOSE 9090
CMD ["/app/rag-server"]
