#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

version="${DESKHAND_VERSION:-$(git describe --always --dirty)}"
ldflags="-X main.version=${version}"
mkdir -p build

export CGO_ENABLED=0

if [ -d cmd/deskhand ]; then
  GOOS=darwin GOARCH=arm64 go build -ldflags "$ldflags" -o build/deskhand ./cmd/deskhand
fi
if [ -d cmd/deskhand-agent ]; then
  for arch in amd64 arm64; do
    GOOS=windows GOARCH=$arch go build -ldflags "$ldflags" -o "build/deskhand-agent-windows-$arch.exe" ./cmd/deskhand-agent
    GOOS=windows GOARCH=$arch go build -ldflags "$ldflags -H windowsgui" -o "build/deskhand-agentw-windows-$arch.exe" ./cmd/deskhand-agent
    GOOS=windows GOARCH=$arch go build -ldflags "$ldflags" -o "build/deskhand-ssh-relay-windows-$arch.exe" ./scripts/e2e/udprelay
  done
fi
GOOS=windows GOARCH=amd64 go build -ldflags "$ldflags" -o build/deskhand-fixture-windows-amd64.exe ./cmd/deskhand-fixture
