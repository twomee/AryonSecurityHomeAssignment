#!/bin/sh

set -eu

go_image='golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc'
gitleaks_image='zricethezav/gitleaks@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f'

case "${1:-}" in
  gofmt)
    files="$(docker run --rm \
      -v "$PWD/go-server:/src" \
      -w /src \
      "$go_image" \
      gofmt -l .)"
    test -z "$files"
    ;;
  test)
    docker run --rm \
      -v "$PWD/go-server:/src" \
      -w /src \
      "$go_image" \
      go test ./...
    ;;
  gitleaks)
    docker run --rm \
      -v "$PWD:/repo" \
      "$gitleaks_image" \
      git /repo --pre-commit --staged --redact --no-banner
    ;;
  *)
    echo "unknown pre-commit command" >&2
    exit 2
    ;;
esac
