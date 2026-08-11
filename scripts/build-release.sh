#!/usr/bin/env bash
#
# Cross-compile every release binary. Requires Go; produces artefacts that
# require nothing at all.
#
# Usage: ./scripts/build-release.sh [version]

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${1:-dev}"
MODULE="github.com/Aryan27-max/bento-box"
LDFLAGS="-s -w -X ${MODULE}/internal/cli.Version=${VERSION}"

rm -rf dist
mkdir -p dist

# CGO_ENABLED=0 is what makes the output a static binary with no libc
# dependency, which is the whole promise of the release artefacts.
export CGO_ENABLED=0

build() {
    local goos="$1" goarch="$2" extension="${3:-}"
    local output="dist/bento-${goos}-${goarch}${extension}"

    printf '  building %-32s' "${goos}/${goarch}"
    GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/bento
    printf 'ok  (%s)\n' "$(du -h "$output" | cut -f1)"
}

echo "Building Bento Box ${VERSION}"
build windows amd64 .exe
build windows arm64 .exe
build linux   amd64
build linux   arm64
build darwin  amd64
build darwin  arm64

echo
echo "Checksums"
(
    cd dist
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum bento-* > checksums.txt
    else
        shasum -a 256 bento-* > checksums.txt
    fi
    cat checksums.txt
)
