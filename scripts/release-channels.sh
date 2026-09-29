#!/bin/sh
# Publish youtuimusic to the Homebrew tap and the AUR after a GitHub release.
#
# Usage:
#   scripts/release-channels.sh            # both channels
#   scripts/release-channels.sh homebrew   # just the tap
#   scripts/release-channels.sh aur        # just the AUR
#   scripts/release-channels.sh all v0.1.0 # pin a tag instead of latest
#
# Prerequisites per channel:
#
#   homebrew — a GitHub repo named homebrew-tap under your account, and push
#     access to it (git with ssh, or gh CLI). One-time setup:
#       gh repo create cllpse/homebrew-tap --public
#     (no local Homebrew install needed; the formula is pushed as a plain file)
#
#   aur — an account at https://aur.archlinux.org, and the account's SSH key
#     in ~/.ssh. One-time setup:
#       # create the account in the web UI, then add your key in My Account
#       ssh aur@aur.archlinux.org            # should greet you
#
# Neither channel is touched unless its prerequisites are satisfied; the
# script prints what is missing and what to do about it.

set -eu

REPO=cllpse/youtuimusic
TAP_REPO=cllpse/homebrew-tap
PKG=youtuimusic
CHANNEL=${1:-all}
VERSION=${2:-}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

say() { echo "release-channels: $*"; }
die() { echo "release-channels: $*" >&2; exit 1; }

# ---- resolve the release ---------------------------------------------------

if [ -z "$VERSION" ]; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
fi
[ -n "$VERSION" ] || die "no release found; pass a tag: $0 v0.1.0"
VER=${VERSION#v}
say "release: $VERSION"

curl -fsSL "https://github.com/$REPO/releases/download/$VERSION/checksums.txt" \
    -o "$WORK/checksums.txt" ||
    die "cannot fetch checksums.txt for $VERSION — was it released by goreleaser?"

sha() { awk -v f="$1" '$2 ~ f {print $1}' "$WORK/checksums.txt"; }
DARWIN_AMD64=$(sha "youtuimusic_${VER}_darwin_amd64.tar.gz")
DARWIN_ARM64=$(sha "youtuimusic_${VER}_darwin_arm64.tar.gz")
LINUX_AMD64=$(sha "youtuimusic_${VER}_linux_amd64.tar.gz")
LINUX_ARM64=$(sha "youtuimusic_${VER}_linux_arm64.tar.gz")
[ -n "$DARWIN_AMD64" ] && [ -n "$LINUX_AMD64" ] ||
    die "checksums.txt is missing expected archives"

# ---- homebrew --------------------------------------------------------------

do_homebrew() {
    say "homebrew: checking push access to $TAP_REPO"
    if ! git ls-remote "git@github.com:$TAP_REPO.git" HEAD >/dev/null 2>&1; then
        cat >&2 <<EOF
homebrew: cannot reach git@github.com:$TAP_REPO.git (missing repo or ssh key)

  one-time setup:
    gh repo create ${TAP_REPO#*/} --public   # or create it on github.com
    # make sure this machine's ssh key is on your GitHub account

  then rerun this script.
EOF
        return 1
    fi

    git clone -q "git@github.com:$TAP_REPO.git" "$WORK/tap"
    mkdir -p "$WORK/tap/Formula"
    cat > "$WORK/tap/Formula/$PKG.rb" <<EOF
class Youtuimusic < Formula
  desc "YouTube Music TUI: playlists as tabs, tracks below, progress bar"
  homepage "https://github.com/$REPO"
  version "$VER"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/$REPO/releases/download/$VERSION/${PKG}_#{version}_darwin_arm64.tar.gz"
      sha256 "$DARWIN_ARM64"
    else
      url "https://github.com/$REPO/releases/download/$VERSION/${PKG}_#{version}_darwin_amd64.tar.gz"
      sha256 "$DARWIN_AMD64"
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/$REPO/releases/download/$VERSION/${PKG}_#{version}_linux_arm64.tar.gz"
      sha256 "$LINUX_ARM64"
    else
      url "https://github.com/$REPO/releases/download/$VERSION/${PKG}_#{version}_linux_amd64.tar.gz"
      sha256 "$LINUX_AMD64"
    end
  end

  def install
    bin "$PKG"
  end

  def caveats
    <<~EOS
      Requires mpv and yt-dlp on PATH.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/$PKG --version")
  end
end
EOF

    git -C "$WORK/tap" add "Formula/$PKG.rb"
    if git -C "$WORK/tap" diff --cached --quiet; then
        say "homebrew: formula already at $VER — nothing to do"
    else
        git -C "$WORK/tap" commit -q -m "$PKG $VER"
        git -C "$WORK/tap" push -q origin HEAD
        say "homebrew: pushed — now installable with: brew install ${TAP_REPO#*/}/$PKG"
    fi
}

# ---- aur -------------------------------------------------------------------

do_aur() {
    say "aur: checking ssh access to aur.archlinux.org"
    if ! ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new aur@aur.archlinux.org 2>&1 |
        grep -q "Hello"; then
        cat >&2 <<EOF
aur: ssh to aur@aur.archlinux.org failed (no account or ssh key not registered)

  one-time setup:
    1. create an account at https://aur.archlinux.org/register
    2. in My Account, paste the public half of your ssh key (~/.ssh/id_*.pub)
    3. ssh aur@aur.archlinux.org   # should print "Hello <username>!"

  then rerun this script.
EOF
        return 1
    fi

    git clone -q "ssh://aur@aur.archlinux.org/$PKG.git" "$WORK/aur" ||
        die "cannot clone the $PKG AUR package — create it first with: git init + push an initial PKGBUILD"

    cd "$WORK/aur"
    cat > PKGBUILD <<EOF
# Maintainer: cllpse <https://github.com/cllpse>
pkgname=$PKG
pkgver=$VER
pkgrel=1
pkgdesc="YouTube Music TUI: playlists as tabs, tracks below, progress bar"
arch=('x86_64' 'aarch64')
url="https://github.com/$REPO"
license=('MIT')
depends=('mpv' 'yt-dlp')
source_x86_64=("\$pkgname-\$pkgver.tar.gz::\$url/releases/download/$VERSION/${PKG}_\${pkgver}_linux_amd64.tar.gz")
source_aarch64=("\$pkgname-\$pkgver.tar.gz::\$url/releases/download/$VERSION/${PKG}_\${pkgver}_linux_arm64.tar.gz")
sha256sums_x86_64=('$LINUX_AMD64')
sha256sums_aarch64=('$LINUX_ARM64')
package() {
    install -Dm755 "\$srcdir/$PKG" "\$pkgdir/usr/bin/$PKG"
}
EOF
    makepkg --printsrcinfo > .SRCINFO
    git add PKGBUILD .SRCINFO
    if git diff --cached --quiet; then
        say "aur: PKGBUILD already at $VER — nothing to do"
    else
        git commit -q -m "$PKG $VER-1"
        git push -q origin master
        say "aur: pushed — installable with: paru -S $PKG"
    fi
    cd - >/dev/null
}

# ---- run -------------------------------------------------------------------

RET=0
case "$CHANNEL" in
    homebrew) do_homebrew || RET=1 ;;
    aur) do_aur || RET=1 ;;
    all)
        do_homebrew || RET=1
        do_aur || RET=1
        ;;
    *) die "unknown channel: $2 (use homebrew, aur or all)" ;;
esac
exit $RET
