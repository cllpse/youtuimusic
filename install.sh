#!/bin/sh
# Install youtuimusic from GitHub Releases. Works on linux/darwin, amd64/arm64.
#
# As of the self-contained releases the archive also carries the two runtime
# dependencies — mpv (playback) and yt-dlp (stream URLs) — so this installs
# them too and the result runs with nothing else on the machine. If an archive
# predates that, or a piece is missing, it falls back to the native package
# manager. Skip all dependency handling with YTMUIMUSIC_NO_DEPS=1.
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
LIB=${YTMUIMUSIC_LIBEXEC_DIR:-$DEST/../libexec/youtuimusic}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "installing $BIN $VERSION ($GOOS/$GOARCH) to $DEST"
curl -fsSL "$URL" | tar -xz -C "$TMP"
[ -f "$TMP/$BIN" ] || { echo "install.sh: archive had no $BIN" >&2; exit 1; }

mkdir -p "$DEST" 2>/dev/null || sudo mkdir -p "$DEST"
if [ -w "$DEST" ]; then
    install -m755 "$TMP/$BIN" "$DEST/$BIN"
else
    echo "need permission to write to $DEST — using sudo"
    sudo install -m755 "$TMP/$BIN" "$DEST/$BIN"
fi
echo "installed: $DEST/$BIN"

# ---- bundled runtime dependencies -------------------------------------------
#
# The release carries mpv and yt-dlp. They go under libexec, beside the
# binary, where internal/tool finds them without touching PATH. mpv is stored
# as a directory (mpv.d) plus a small wrapper named "mpv", because on Linux it
# is an AppImage that has to be unpacked and on macOS it is an .app bundle.
#
# A dependency already on PATH is left alone: the runtime prefers it (mpv falls
# back to the bundle, yt-dlp prefers PATH outright), so unpacking a second copy
# would only waste space. Only what is missing gets installed.

if [ "${YTMUIMUSIC_NO_DEPS:-0}" = "1" ]; then
    echo "skipping runtime dependencies (YTMUIMUSIC_NO_DEPS=1)"
    exit 0
fi

mkdir -p "$LIB" 2>/dev/null || sudo mkdir -p "$LIB"
if [ -w "$LIB" ]; then
    lib_install() { install "$@"; }
    as_root() { "$@"; }
else
    lib_install() { sudo install "$@"; }
    as_root() { sudo "$@"; }
fi

have() { command -v "$1" >/dev/null 2>&1; }

bundled_mpv=no
bundled_dl=no

if [ -f "$TMP/mpv.tar.gz" ] && have mpv; then
    echo "mpv already on PATH ($(command -v mpv)) — skipping bundled copy"
elif [ -f "$TMP/mpv.tar.gz" ]; then
    echo "installing bundled mpv to $LIB"
    mkdir -p "$TMP/mpvd"
    tar -xzf "$TMP/mpv.tar.gz" -C "$TMP/mpvd"
    mpv_bin=
    if [ -d "$TMP/mpvd/mpv.app" ]; then
        # macOS: keep the bundle whole, its dylibs are relative to it.
        as_root rm -rf "$LIB/mpv.d" "$LIB/mpv"
        as_root mv "$TMP/mpvd/mpv.app" "$LIB/mpv.d"
        mpv_bin=mpv.d/Contents/MacOS/mpv
    elif ( cd "$TMP/mpvd" && chmod +x mpv.AppImage &&
        ./mpv.AppImage --appimage-extract >/dev/null 2>&1 ); then
        # Linux: unpack the AppImage on the target, where its runtime runs.
        # The uruntime names the real tree AppDir and leaves squashfs-root as a
        # relative symlink to it; resolve before moving or the link dangles.
        extracted=$(cd "$TMP/mpvd/squashfs-root" && pwd -P)
        as_root rm -rf "$LIB/mpv.d" "$LIB/mpv"
        as_root mv "$extracted" "$LIB/mpv.d"
        mpv_bin=mpv.d/AppRun
    else
        echo "could not unpack bundled mpv; falling back to the package manager" >&2
    fi
    if [ -n "$mpv_bin" ]; then
        cat > "$TMP/mpv.wrapper" <<EOF
#!/bin/sh
# Launcher for the mpv bundled with youtuimusic.
here=\$(CDPATH= cd -- "\$(dirname -- "\$0")" && pwd)
exec "\$here/$mpv_bin" "\$@"
EOF
        lib_install -m755 "$TMP/mpv.wrapper" "$LIB/mpv"
        bundled_mpv=yes
    fi
fi

if [ -f "$TMP/yt-dlp" ] && have yt-dlp; then
    echo "yt-dlp already on PATH ($(command -v yt-dlp)) — skipping bundled copy"
elif [ -f "$TMP/yt-dlp" ]; then
    echo "installing bundled yt-dlp to $LIB"
    lib_install -m755 "$TMP/yt-dlp" "$LIB/yt-dlp"
    bundled_dl=yes
fi

# ---- package-manager fallback -----------------------------------------------
#
# Only for what the archive did not carry and the machine does not already
# have. Distro and Homebrew builds track upstream, so when they exist they are
# preferred at runtime anyway (see internal/tool.System).

run_priv() {
    if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi
}

missing=
have mpv || [ "$bundled_mpv" = yes ] || missing="$missing mpv"
have yt-dlp || [ "$bundled_dl" = yes ] || missing="$missing yt-dlp"
[ -z "$missing" ] && exit 0

echo "installing runtime dependencies:$missing"
if [ "$OS" = Darwin ]; then
    if have brew; then
        brew install $missing
        exit 0
    fi
    echo "install:$missing with Homebrew: brew install $missing" >&2
    exit 0
fi

for pm in apt-get dnf pacman zypper apk; do
    have "$pm" || continue
    case "$pm" in
        apt-get)
            run_priv apt-get update -qq || true
            run_priv apt-get install -y $missing
            ;;
        dnf)    run_priv dnf install -y $missing ;;
        pacman) run_priv pacman -S --needed --noconfirm $missing ;;
        zypper) run_priv zypper --non-interactive install $missing ;;
        apk)    run_priv apk add $missing ;;
    esac
    exit 0
done

echo "no package manager detected; install:$missing yourself:" >&2
echo "  debian/ubuntu:  sudo apt install mpv yt-dlp" >&2
echo "  fedora:         sudo dnf install mpv yt-dlp" >&2
echo "  arch:           sudo pacman -S mpv yt-dlp" >&2
echo "  macos:          brew install mpv yt-dlp" >&2
