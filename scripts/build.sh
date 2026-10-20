#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

version="$(git describe --always --dirty)"
ldflags="-X main.version=${version}"
mkdir -p build

export CGO_ENABLED=0

if [ -d cmd/deskhand ]; then
  GOOS=darwin GOARCH=arm64 go build -ldflags "$ldflags" -o build/deskhand ./cmd/deskhand
fi
if [ -d cmd/deskhand-agent ]; then
  GOOS=windows GOARCH=amd64 go build -ldflags "$ldflags -H windowsgui" -o build/deskhand-agent-windows-amd64.exe ./cmd/deskhand-agent
  GOOS=windows GOARCH=arm64 go build -ldflags "$ldflags -H windowsgui" -o build/deskhand-agent-windows-arm64.exe ./cmd/deskhand-agent
fi
GOOS=windows GOARCH=amd64 go build -ldflags "$ldflags" -o build/deskhand-fixture-windows-amd64.exe ./cmd/deskhand-fixture
