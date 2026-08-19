FROM golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406 AS builder

RUN apk upgrade --no-cache

RUN CGO_ENABLED=0 go install \
    -tags postgres \
    github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40

RUN apk upgrade --no-cache \
    && addgroup -S migrator \
    && adduser -S -G migrator migrator

COPY --from=builder /go/bin/migrate /usr/local/bin/migrate

USER migrator

ENTRYPOINT ["/usr/local/bin/migrate"]
