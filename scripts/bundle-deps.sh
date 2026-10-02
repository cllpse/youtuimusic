#!/bin/sh
# Bundle youtuimusic's runtime dependencies into deps/<os>_<arch>/ so that
# goreleaser can drop them into the release archive. The app then runs with no
# packages installed: it finds them beside itself.
#
# Usage:
#   scripts/bundle-deps.sh                 # all four release targets
#   scripts/bundle-deps.sh linux_amd64     # just one
#
# Output per target:
#   deps/<os>_<arch>/yt-dlp      standalone binary, no Python needed
#   deps/<os>_<arch>/mpv.tar.gz  mpv payload:
#                                  mpv.AppImage  (linux — extracted on install)
#                                  mpv.app/...   (darwin — official build)
#
# Sources and why:
#   yt-dlp  github.com/yt-dlp/yt-dlp   Unlicense. The PyInstaller "one file"
#           build, so a machine without a usable Python still gets a working
#           downloader. Verified against the release's SHA2-256SUMS.
#   mpv     github.com/mpv-player/mpv  GPLv2+/LGPL. Official macOS builds since
#           v0.41.0; arm64 uses the oldest macOS target for reach, amd64 the
#           only Intel build published.
#           github.com/pkgforge-dev/mpv-AppImage  Linux, because mpv ships no
#           Linux binary and a distro one cannot be relocated. The AppImage is
#           self-contained and is unpacked by install.sh on the target, so no
#           cross-architecture execution is needed at build time.
#
# Run from anywhere; paths are resolved relative to the repo root.
set -eu

cd "$(dirname "$0")/.."

YTDLP_REPO=yt-dlp/yt-dlp
MPV_REPO=mpv-player/mpv
APPIMAGE_REPO=pkgforge-dev/mpv-AppImage

say() { echo "bundle-deps: $*" >&2; }
die() { echo "bundle-deps: $*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need curl
need python3
need tar
need unzip

# sha256sum is GNU coreutils; stock macOS has shasum instead. Without either
# the yt-dlp check below would compare against nothing and report a
# mismatch, which is the wrong thing to go looking for.
if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    die "sha256sum or shasum is required"
fi

# Scratch space for the downloads, removed on exit — a failed fetch included.
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# The release workflow has GITHUB_TOKEN in the environment. Using it lifts the
# API's unauthenticated rate limit, which runners sharing an address hit.
api() {
    if [ -n "${GITHUB_TOKEN:-}" ]; then
        curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
            "https://api.github.com/repos/$1/releases/latest"
    else
        curl -fsSL "https://api.github.com/repos/$1/releases/latest"
    fi
}

# ---- yt-dlp ----------------------------------------------------------------

ytdlp_asset() {
    case "$1-$2" in
        linux-amd64)  echo yt-dlp_linux ;;
        linux-arm64)  echo yt-dlp_linux_aarch64 ;;
        darwin-*)     echo yt-dlp_macos ;;
        *) die "no yt-dlp build for $1/$2" ;;
    esac
}

fetch_ytdlp() {
    os=$1 arch=$2 dir=$3
    asset=$(ytdlp_asset "$os" "$arch")
    say "$os/$arch: yt-dlp ($asset)"
    base="https://github.com/$YTDLP_REPO/releases/latest/download"
    curl -fsSL -o "$dir/yt-dlp" "$base/$asset"

    # Verify against the published sums. A mismatch is fatal — better to fail
    # the release than to ship a downloader nobody checked.
    want=$(curl -fsSL "$base/SHA2-256SUMS" | awk -v a="$asset" '$2 == a {print $1}')
    [ -n "$want" ] || die "yt-dlp: no checksum for $asset"
    got=$(sha256 "$dir/yt-dlp")
    [ "$want" = "$got" ] || die "yt-dlp: checksum mismatch for $asset"
    chmod 0755 "$dir/yt-dlp"
}

# ---- mpv -------------------------------------------------------------------

fetch_mpv_darwin() {
    arch=$1 dir=$2
    case "$arch" in
        arm64) variant=arm ;;
        amd64) variant=intel ;;
        *) die "no mpv build for darwin/$arch" ;;
    esac
    json=$(api "$MPV_REPO") || die "mpv: GitHub API request for $MPV_REPO failed"
    url=$(printf '%s' "$json" | python3 -c '
import json, re, sys
assets = json.load(sys.stdin)["assets"]
variant = sys.argv[1]
best = None
for a in assets:
    m = re.search(r"-macos-(\d+)-" + variant + r"\.zip$", a["name"])
    if m:
        v = int(m.group(1))
        if best is None or v < best[0]:
            best = (v, a["browser_download_url"])
print(best[1] if best else "")
' "$variant")
    [ -n "$url" ] || die "mpv: no darwin/$variant asset found"

    say "darwin/$arch: mpv ($(basename "$url"))"
    tmp=$(mktemp -d "$WORK/mpv.XXXXXX")
    curl -fsSL -o "$tmp/mpv.zip" "$url"
    unzip -q -o "$tmp/mpv.zip" -d "$tmp"
    [ -f "$tmp/mpv.tar.gz" ] || die "mpv: $url did not contain mpv.tar.gz"
    mv "$tmp/mpv.tar.gz" "$dir/mpv.tar.gz"
    rm -rf "$tmp"
}

fetch_mpv_linux() {
    arch=$1 dir=$2
    case "$arch" in
        amd64) pat=anylinux-x86_64.AppImage ;;
        arm64) pat=anylinux-aarch64.AppImage ;;
        *) die "no mpv build for linux/$arch" ;;
    esac
    json=$(api "$APPIMAGE_REPO") || die "mpv: GitHub API request for $APPIMAGE_REPO failed"
    url=$(printf '%s' "$json" | python3 -c '
import json, sys
pat = sys.argv[1]
for a in json.load(sys.stdin)["assets"]:
    if a["name"].endswith(pat):
        print(a["browser_download_url"])
        break
' "$pat")
    [ -n "$url" ] || die "mpv: no linux/$arch asset found"

    say "linux/$arch: mpv ($(basename "$url"))"
    tmp=$(mktemp -d "$WORK/mpv.XXXXXX")
    curl -fsSL -o "$tmp/mpv.AppImage" "$url"
    # Not extracted here: the payload only runs on its own architecture, and
    # install.sh unpacks it on the target anyway. Tar it so every target ships
    # the same filename.
    tar -czf "$dir/mpv.tar.gz" -C "$tmp" mpv.AppImage
    rm -rf "$tmp"
}

# ---- main ------------------------------------------------------------------

targets=${*:-"linux_amd64 linux_arm64 darwin_amd64 darwin_arm64"}

for t in $targets; do
    os=${t%%_*}
    arch=${t#*_}
    dir=deps/$t
    mkdir -p "$dir"
    fetch_ytdlp "$os" "$arch" "$dir"
    case "$os" in
        linux)  fetch_mpv_linux  "$arch" "$dir" ;;
        darwin) fetch_mpv_darwin "$arch" "$dir" ;;
        *) die "unsupported os: $os" ;;
    esac
done

say "done: $(du -sh deps 2>/dev/null | cut -f1) under deps/"
