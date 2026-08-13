# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go test ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dashboard .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S dashboard && adduser -S -G dashboard dashboard

WORKDIR /app
COPY --from=builder /out/dashboard /app/dashboard
RUN chown -R dashboard:dashboard /app

USER dashboard
ENV TZ=Asia/Yekaterinburg
EXPOSE 1987

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:1987/completion-plan/healthz || exit 1

ENTRYPOINT ["/app/dashboard"]
