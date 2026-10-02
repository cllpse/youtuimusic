# Releasing

A release is three pushes: the tag builds the GitHub release, a script pushes
the Homebrew formula, and (once the AUR is set up) the same script pushes the
PKGBUILD.

```bash
# 1. the GitHub release — builds binaries for linux/darwin x amd64/arm64,
#    bundles mpv + yt-dlp into each archive, writes checksums.txt, and
#    attaches them to a GitHub release
git tag v0.2.0 && git push origin v0.2.0
#    then watch: https://github.com/cllpse/youtuimusic/actions

# 2. the package managers — generates the formula / PKGBUILD from the
#    release's checksums.txt and pushes each to its channel
scripts/release-channels.sh              # both channels
scripts/release-channels.sh homebrew     # just the tap
scripts/release-channels.sh aur          # just the AUR
scripts/release-channels.sh all v0.2.0   # a given tag rather than the latest
```

Both channels must already be set up once; the script skips a channel and
prints its setup steps when credentials are missing. Each channel stops at its
first failed step, a failure in one does not stop the other, and the script
exits non-zero if either failed. Details below.

## Version string

`youtuimusic --version` reads `main.version`, injected at link time by
GoReleaser (`-X main.version={{.Version}}`). A bare `go build` produces
`dev`. A release tag should look like `v0.2.0` — the leading `v` is stripped
for file names, package versions and what `--version` prints (`0.2.0`).

## Files

| file | role |
|---|---|
| `goreleaser.yml` | build matrix, archive names, checksums, and the per-target `mpv`/`yt-dlp` files. Builds with `CGO_ENABLED=0`: without it the linux/amd64 binary — the one target not cross-compiled on the runner — links glibc dynamically and will not start on musl distributions or NixOS. Note: GoReleaser **v2** schema — it rejected the older `cmd:` field. |
| `.github/workflows/release.yml` | on `v*` tags: setup-go + `goreleaser release --clean` |
| `scripts/bundle-deps.sh` | `before` hook: downloads `mpv` + `yt-dlp` for all four targets into `deps/<os>_<arch>/`. ~300 MB of downloads per release; yt-dlp is checksum-verified (`sha256sum`, or `shasum` on macOS). GitHub API calls use `GITHUB_TOKEN` when it is set, as it is in the workflow, to stay clear of the unauthenticated rate limit. |
| `install.sh` | user-facing curl\|sh installer; detects OS/arch; verifies the archive against the release's `checksums.txt`; installs the binary and unpacks the bundled `mpv`/`yt-dlp` into `libexec`, skipping any dep already on `PATH`. `YOUTUIMUSIC_INSTALL_DIR` overrides the binary destination, `YOUTUIMUSIC_LIBEXEC_DIR` the dependency destination, and `YOUTUIMUSIC_NO_DEPS=1` skips the dependencies; the older `YTMUIMUSIC_*` spellings still work. The dependency destination has to be one the app searches (`internal/tool`): beside the binary, `libexec/youtuimusic` next to it, or `../libexec/youtuimusic` above it. |
| `THIRD_PARTY_NOTICES.md` | licenses of the bundled `mpv` (GPL/LGPL) and `yt-dlp` (Unlicense); ships inside every archive. |
| `scripts/release-channels.sh` | homebrew + aur publisher; `homebrew`/`aur`/`all` subcommands; optional second arg pins a tag instead of latest |

## Homebrew tap (one-time setup)

A tap is just a git repo named `homebrew-tap` — Homebrew itself is not
needed on the maintainer machine. The formula lives at
`Formula/youtuimusic.rb` in [`cllpse/homebrew-tap`](https://github.com/cllpse/homebrew-tap).

1. `gh repo create cllpse/homebrew-tap --public`
2. ssh key on the GitHub account (the script pushes over ssh)
3. `scripts/release-channels.sh homebrew`

Users then run `brew install cllpse/tap/youtuimusic`. `brew audit --strict
youtuimusic` on a machine with Homebrew is worth one run after the first
formula push.

## AUR (one-time setup, pending)

Not yet published — the AUR was down when this was set up. When it is back:

1. register at <https://aur.archlinux.org/register>, add the account's ssh
   public key under My Account, confirm with `ssh aur@aur.archlinux.org`
   (it greets you by name and closes the connection)
2. create the package: `git clone ssh://aur@aur.archlinux.org/youtuimusic.git`
   (empty clone is fine — first push of `PKGBUILD` + `.SRCINFO` creates it)
3. `scripts/release-channels.sh aur` — needs `makepkg` locally (Arch, or
   `pacman` on other distros) to generate `.SRCINFO`

Arch users then run `paru -S youtuimusic` (or their AUR helper of choice).

## Gotchas hit on the first release

- The tag-triggered workflow can silently not run on the first push — delete
  and re-push the tag to force it (`git push origin :refs/tags/vX.Y.Z`).
- GoReleaser v2 rejected `cmd: go build` in the build stanza; plain `main:`
  is all that is needed.
- The archives are large (~75-90 MB; darwin/amd64 is the biggest): the
  bundled dependencies dominate. If a local `goreleaser build` is all that is
  needed, skip the fetch with
  `goreleaser build --skip=before` (after running `scripts/bundle-deps.sh`
  once, or not at all when only the Go binary matters).
- `bundle-deps.sh` runs on the x86_64 CI runner but must fetch arm64 payloads
  too. The Linux AppImage is therefore shipped un-extracted and unpacked by
  `install.sh` on the target, where the architecture matches.
