FROM golang:1.27.2-alpine3.23@sha256:9e45f0eb4a63ed37ad6e604950407558b7c738e34e015ae77a5d4ad369cde079 AS builder

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
