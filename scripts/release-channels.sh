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
#       ssh aur@aur.archlinux.org            # should greet you by name
#     and makepkg locally, to write .SRCINFO.
#
# Neither channel is touched unless its prerequisites are satisfied; the
# script prints what is missing and what to do about it.

set -eu

REPO=cllpse/youtuimusic
TAP_REPO=cllpse/homebrew-tap
PKG=youtuimusic
CHANNEL=${1:-all}
VERSION=${2:-}

say() { echo "release-channels: $*"; }
die() { echo "release-channels: $*" >&2; exit 1; }

# Checked before anything is fetched, so a typo costs nothing.
case "$CHANNEL" in
    homebrew|aur|all) ;;
    *) die "unknown channel: $CHANNEL (use homebrew, aur or all)" ;;
esac

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

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

sha() { awk -v f="$1" '$2 == f {print $1}' "$WORK/checksums.txt"; }
DARWIN_AMD64=$(sha "${PKG}_${VER}_darwin_amd64.tar.gz")
DARWIN_ARM64=$(sha "${PKG}_${VER}_darwin_arm64.tar.gz")
LINUX_AMD64=$(sha "${PKG}_${VER}_linux_amd64.tar.gz")
LINUX_ARM64=$(sha "${PKG}_${VER}_linux_arm64.tar.gz")
# All four, or the formula and PKGBUILD would carry an empty sha256 for a
# platform and fail there at install time instead of here.
[ -n "$DARWIN_AMD64" ] && [ -n "$DARWIN_ARM64" ] &&
    [ -n "$LINUX_AMD64" ] && [ -n "$LINUX_ARM64" ] ||
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

  depends_on "mpv"
  depends_on "yt-dlp"

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
    bin.install "$PKG"
  end

  def caveats
    <<~EOS
      mpv and yt-dlp are installed as dependencies.
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
    if ! command -v makepkg >/dev/null 2>&1; then
        echo "aur: makepkg is required to write .SRCINFO (Arch, or pacman on other distros)" >&2
        return 1
    fi

    say "aur: checking ssh access to aur.archlinux.org"
    # A registered key is greeted by name before the connection closes. The
    # greeting is matched loosely — Hello or Welcome — rather than on one
    # exact wording.
    if ! ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new aur@aur.archlinux.org 2>&1 |
        grep -q -e "Hello" -e "Welcome"; then
        cat >&2 <<EOF
aur: ssh to aur@aur.archlinux.org failed (no account or ssh key not registered)

  one-time setup:
    1. create an account at https://aur.archlinux.org/register
    2. in My Account, paste the public half of your ssh key (~/.ssh/id_*.pub)
    3. ssh aur@aur.archlinux.org   # should greet you by name

  then rerun this script.
EOF
        return 1
    fi

    # A package that does not exist yet clones as an empty repository, and
    # the first push creates it, so a failure here is access, not absence.
    git clone -q "ssh://aur@aur.archlinux.org/$PKG.git" "$WORK/aur" ||
        die "cannot clone ssh://aur@aur.archlinux.org/$PKG.git"

    # This runs in a subshell (see run_channel), so the cd does not leak.
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
        # HEAD:master, because an empty clone names its branch after the
        # local init.defaultBranch, and the AUR only accepts master.
        git push -q origin HEAD:master
        say "aur: pushed — installable with: paru -S $PKG"
    fi
}

# ---- run -------------------------------------------------------------------

# run_channel runs one channel in a subshell with -e in force. Called the
# obvious way, as `do_aur || RET=1`, the shell ignores -e for the whole
# function body, so a failed clone, makepkg or push would carry on and
# print "pushed". A failure in one channel still lets the other run.
RET=0
run_channel() {
    set +e
    (set -e; "$1")
    rc=$?
    set -e
    [ "$rc" -eq 0 ] || RET=1
}

case "$CHANNEL" in
    homebrew) run_channel do_homebrew ;;
    aur) run_channel do_aur ;;
    all)
        run_channel do_homebrew
        run_channel do_aur
        ;;
esac
exit $RET
