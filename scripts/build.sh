#!/bin/sh
# Cross-compile release binaries into dist/ with a SHA256SUMS file.
set -eu
VERSION="${1:-dev}"
rm -rf dist && mkdir -p dist
for t in linux/amd64 linux/arm64 linux/arm linux/386 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64; do
  os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
  out="dist/sabr_${os}_${arch}${ext}"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=7 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$out" ./cmd/sabr
done
(cd dist && sha256sum sabr_* > SHA256SUMS)
ls -la dist
