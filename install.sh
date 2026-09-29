#!/bin/sh
# Install youtuimusic from GitHub Releases. Works on linux/darwin, amd64/arm64.
set -eu

REPO=cllpse/youtuimusic
BIN=youtuimusic

if [ "${1:-}" = "--version" ]; then
    echo "install.sh for $REPO (pass a tag like v0.1.0 as \$1 to pin)"
    exit 0
fi

VERSION=${1:-}
if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
fi
if [ -z "$VERSION" ]; then
    echo "install.sh: could not determine the latest release" >&2
    exit 1
fi

OS=$(uname -s)
case "$OS" in
    Linux*) GOOS=linux ;;
    Darwin*) GOOS=darwin ;;
    *) echo "install.sh: unsupported OS: $OS" >&2; exit 1 ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    *) echo "install.sh: unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

URL="https://github.com/$REPO/releases/download/$VERSION/${BIN}_${VERSION#v}_${GOOS}_${GOARCH}.tar.gz"
DEST=${YTMUIMUSIC_INSTALL_DIR:-/usr/local/bin}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "installing $BIN $VERSION ($GOOS/$GOARCH) to $DEST"
curl -fsSL "$URL" | tar -xz -C "$TMP" "$BIN"

if [ -w "$DEST" ] || mkdir -p "$DEST" 2>/dev/null; then
    :
else
    echo "need permission to write to $DEST — using sudo"
    sudo install -m755 "$TMP/$BIN" "$DEST/$BIN"
fi
install -m755 "$TMP/$BIN" "$DEST/$BIN" 2>/dev/null || sudo install -m755 "$TMP/$BIN" "$DEST/$BIN"

echo "installed: $DEST/$BIN"
echo "still needs on PATH: mpv, yt-dlp"
