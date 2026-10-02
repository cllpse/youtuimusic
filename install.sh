#!/bin/sh
# Install youtuimusic from GitHub Releases. Works on linux/darwin, amd64/arm64.
#
# As of the self-contained releases the archive also carries the two runtime
# dependencies — mpv (playback) and yt-dlp (stream URLs) — so this installs
# them too and the result runs with nothing else on the machine. If an archive
# predates that, or a piece is missing, it falls back to the native package
# manager.
#
# Options come from the environment. Piped from curl, they are set on sh:
#
#   curl -fsSL .../install.sh | YOUTUIMUSIC_NO_DEPS=1 sh
#
#   YOUTUIMUSIC_INSTALL_DIR  where the binary goes (default /usr/local/bin)
#   YOUTUIMUSIC_LIBEXEC_DIR  where mpv and yt-dlp go (default
#                            $YOUTUIMUSIC_INSTALL_DIR/../libexec/youtuimusic).
#                            youtuimusic looks for them only beside its own
#                            executable, in libexec/youtuimusic next to it, or
#                            in ../libexec/youtuimusic above it (internal/tool),
#                            so anywhere else installs them out of its sight.
#   YOUTUIMUSIC_NO_DEPS=1    install the binary and nothing else
#
# The older YTMUIMUSIC_* spellings of these are still honoured.
#
# A tag as the first argument pins a release:  ... | sh -s -- v0.2.0
set -eu

REPO=cllpse/youtuimusic
BIN=youtuimusic

have() { command -v "$1" >/dev/null 2>&1; }
die() { echo "install.sh: $*" >&2; exit 1; }

# sha256 prints a file's SHA-256: sha256sum on Linux, shasum on macOS.
sha256() {
    if have sha256sum; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

if [ "${1:-}" = "--version" ]; then
    echo "install.sh for $REPO (pass a tag like v0.1.0 as \$1 to pin)"
    exit 0
fi

VERSION=${1:-}
if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
fi
[ -n "$VERSION" ] || die "could not determine the latest release"

OS=$(uname -s)
case "$OS" in
    Linux*) GOOS=linux ;;
    Darwin*) GOOS=darwin ;;
    *) die "unsupported OS: $OS" ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    *) die "unsupported architecture: $ARCH" ;;
esac

BASE="https://github.com/$REPO/releases/download/$VERSION"
ARCHIVE="${BIN}_${VERSION#v}_${GOOS}_${GOARCH}.tar.gz"
DEST=${YOUTUIMUSIC_INSTALL_DIR:-${YTMUIMUSIC_INSTALL_DIR:-/usr/local/bin}}
LIB=${YOUTUIMUSIC_LIBEXEC_DIR:-${YTMUIMUSIC_LIBEXEC_DIR:-$DEST/../libexec/youtuimusic}}
NO_DEPS=${YOUTUIMUSIC_NO_DEPS:-${YTMUIMUSIC_NO_DEPS:-0}}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "installing $BIN $VERSION ($GOOS/$GOARCH) to $DEST"
curl -fsSL -o "$TMP/archive.tar.gz" "$BASE/$ARCHIVE"

# Check the archive against the checksums.txt GoReleaser publishes beside it.
# A mismatch, or no entry for this archive, stops the install. Only a machine
# with no SHA-256 tool at all goes ahead unchecked, and it says so.
if have sha256sum || have shasum; then
    curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt" ||
        die "could not fetch checksums.txt for $VERSION"
    want=$(awk -v f="$ARCHIVE" '$2 == f {print $1}' "$TMP/checksums.txt")
    [ -n "$want" ] || die "checksums.txt for $VERSION has no entry for $ARCHIVE"
    got=$(sha256 "$TMP/archive.tar.gz")
    [ "$want" = "$got" ] || die "checksum mismatch for $ARCHIVE (expected $want, got $got)"
    echo "verified $ARCHIVE against checksums.txt"
else
    echo "install.sh: warning: neither sha256sum nor shasum found; $ARCHIVE is not verified" >&2
fi

tar -xzf "$TMP/archive.tar.gz" -C "$TMP"
[ -f "$TMP/$BIN" ] || die "archive had no $BIN"

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
# A dependency already on PATH is left alone, so only what is missing gets
# installed. The runtime looks the two up in opposite orders, and skipping is
# right for both: yt-dlp on PATH wins over a bundled copy (tool.System — it
# has to keep up with YouTube, and a packaged one is updated more often than
# this release), so a second copy would never run; mpv takes a bundled copy
# first and only then PATH (tool.Path), so installing one would shadow the mpv
# the user already has.

if [ "$NO_DEPS" = "1" ]; then
    echo "skipping runtime dependencies (YOUTUIMUSIC_NO_DEPS=1)"
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

bundled_mpv=no
bundled_dl=no

if [ -f "$TMP/mpv.tar.gz" ] && have mpv; then
    echo "mpv already on PATH ($(command -v mpv)) — skipping bundled copy"
    # One bundled by an earlier install would still be found first.
    if [ -e "$LIB/mpv" ]; then
        echo "note: $LIB/mpv from an earlier install still takes precedence;" \
            "remove $LIB/mpv and $LIB/mpv.d to use the one on PATH"
    fi
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
# have. A packaged copy installed here is the one that runs: yt-dlp on PATH
# beats a bundle anyway, and no bundled mpv was installed to shadow it.

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
