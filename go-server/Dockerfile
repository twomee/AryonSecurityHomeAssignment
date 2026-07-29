FROM golang:1.26.5-alpine3.23 AS builder

RUN apk upgrade --no-cache

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/cloudstore \
    ./cmd/api

FROM alpine:3.23

RUN apk upgrade --no-cache \
    && addgroup -S cloudstore \
    && adduser -S -G cloudstore cloudstore

COPY --from=builder /out/cloudstore /usr/local/bin/cloudstore

USER cloudstore
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/cloudstore"]
