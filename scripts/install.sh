#!/usr/bin/env bash
set -euo pipefail

REPO="TolgaOk/agentgate"
BINARY="aga"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "${ARCH}" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: ${ARCH}" >&2; exit 1 ;;
esac

VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed 's/.*"v\(.*\)".*/\1/')"
if [ -z "${VERSION}" ]; then
  echo "Failed to fetch latest version" >&2
  exit 1
fi

URL="https://github.com/${REPO}/releases/download/v${VERSION}/${BINARY}-${OS}-${ARCH}"
echo "Installing ${BINARY} v${VERSION} (${OS}/${ARCH})..."

TMP="$(mktemp)"
trap 'rm -f "${TMP}"' EXIT

curl -fsSL -o "${TMP}" "${URL}"
chmod +x "${TMP}"

if [ -w "${INSTALL_DIR}" ]; then
  mv "${TMP}" "${INSTALL_DIR}/${BINARY}"
else
  sudo mv "${TMP}" "${INSTALL_DIR}/${BINARY}"
fi

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
