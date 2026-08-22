# Build the Go application separately so the runtime image contains only what
# is needed to start the service.
FROM golang:1.26.5-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gin-backend .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app

WORKDIR /app

COPY --from=builder /out/gin-backend ./gin-backend
COPY --chown=app:app configs ./configs
RUN mkdir -p storage/uploads storage/logs \
    && chown -R app:app storage

# Set GIN_CONFIG_PATH to a mounted configuration file in container deployments
# to supply the PostgreSQL/Redis service addresses and production JWT keys.
ENV GIN_CONFIG_PATH=/app/configs/config.yaml

USER app

EXPOSE 8080

ENTRYPOINT ["./gin-backend"]
