#!/bin/sh
# Builds release archives for every supported platform.
#   ./build.sh [VERSION]   (default: dev)
set -e

VERSION="${1:-dev}"
COMMIT="$(git rev-parse HEAD 2>/dev/null || true)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/jackjakarta/gocron/internal/version"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.Date=${DATE}"
TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

if command -v sha256sum >/dev/null 2>&1; then
    SHA="sha256sum"
else
    SHA="shasum -a 256"
fi

echo "Building gocron ${VERSION}..."
echo ""

rm -f gocron-*.tar.gz checksums.txt
n=0
for target in $TARGETS; do
    n=$((n + 1))
    os="${target%/*}"
    arch="${target#*/}"
    echo "[${n}/4] Building ${os}/${arch}..."
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "${LDFLAGS}" -o gocron ./cmd/gocron
    tar -czf "gocron-${os}-${arch}.tar.gz" gocron
done
rm -f gocron

$SHA gocron-*.tar.gz > checksums.txt

echo ""
echo "Build complete!"
echo ""
echo "Checksums:"
cat checksums.txt
echo ""
echo "Checksums written to checksums.txt"
