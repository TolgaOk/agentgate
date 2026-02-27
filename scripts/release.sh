#!/usr/bin/env bash
set -euo pipefail

VERSION=$(grep 'Version:' cmd/aga/main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')
TAG="v${VERSION}"
DIST="dist"

echo "Building ${TAG}..."
rm -rf "${DIST}"
mkdir -p "${DIST}"

platforms=(
  "darwin/arm64"
  "darwin/amd64"
  "linux/amd64"
  "linux/arm64"
)

for platform in "${platforms[@]}"; do
  GOOS="${platform%/*}"
  GOARCH="${platform#*/}"
  output="${DIST}/aga-${GOOS}-${GOARCH}"
  echo "  ${GOOS}/${GOARCH}"
  GOOS="${GOOS}" GOARCH="${GOARCH}" go build -o "${output}" ./cmd/aga/
done

echo "Creating release ${TAG}..."
gh release create "${TAG}" "${DIST}"/* \
  --title "${TAG}" \
  --notes "Release ${TAG}" \
  --draft

echo "Done. Draft release created: ${TAG}"
