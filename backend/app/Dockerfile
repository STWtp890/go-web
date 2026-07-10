FROM golang:1.26.4-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o service .

FROM gcr.io/distroless/static-debian13
WORKDIR /app
COPY --from=builder /app/service .
COPY etc/api.yaml ./etc/api.yaml
EXPOSE 8888
CMD ["./service", "-f", "etc/api.yaml"]