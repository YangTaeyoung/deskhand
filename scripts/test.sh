#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

export CGO_ENABLED=0

go vet ./...
go test ./...

GOOS=windows GOARCH=amd64 go vet ./...
for pkg in $(GOOS=windows GOARCH=amd64 go list ./...); do
  GOOS=windows GOARCH=amd64 go test -c -o /dev/null "$pkg"
done
