#!/bin/sh
# Run from any directory. No credentials, network calls or deployment.
set -eu
cd "$(dirname "$0")/.."
version=${1:-$(git describe --tags --always --dirty)}
mkdir -p dist
for target_os in darwin linux windows; do
  for target_arch in amd64 arm64; do
    suffix=
    if [ "$target_os" = windows ]; then suffix=.exe; fi
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath \
      -ldflags "-X main.version=$version" -o "dist/lolad-$target_os-$target_arch$suffix" .
  done
done
