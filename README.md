# youtuimusic

A small YouTube Music TUI: playlists as tabs, tracks below them, a progress
bar at the bottom. Search, and thumbs up/down. That is the whole scope,
deliberately.

## Running

### Install

Released binaries for linux/darwin, amd64/arm64 — no Go toolchain needed.
Each archive is self-contained: it carries `mpv` and `yt-dlp` beside the
binary, and the installer unpacks them into `libexec`, so nothing else has to
be installed.

```bash
# linux / macos, amd64 / arm64 — self-contained binaries
curl -fsSL https://raw.githubusercontent.com/cllpse/youtuimusic/main/install.sh | sh

# macos (or linuxbrew), from the tap
brew install cllpse/tap/youtuimusic

# with Go installed (deps must be on PATH)
go install github.com/cllpse/youtuimusic/cmd/youtuimusic@latest
```

Arch (AUR) packaging exists too; see `scripts/release-channels.sh` for its
current status. Homebrew and AUR install `mpv` and `yt-dlp` as dependencies
rather than unpacking the bundled copies.

At runtime youtuimusic looks beside its own executable first, then on `PATH`.
`yt-dlp` is the exception: it has to keep up with YouTube, so a newer copy on
`PATH` wins over the bundled snapshot and the bundle is only the fallback.
The installer skips a bundled dependency that is already on `PATH`, so it
fills in only what is missing rather than shadowing what you already have.
The bundled programs are third-party works with their own licenses — see
`THIRD_PARTY_NOTICES.md`. Skip dependency handling entirely with
`YTMUIMUSIC_NO_DEPS=1`.

### Signing in

youtuimusic reads the session out of a browser you are already signed in to.
Chrome, Chromium, Edge, Brave, Opera, Vivaldi and Helium are read through
their Chromium cookie store; Firefox, LibreWolf and Waterfox through
`cookies.sqlite`. Native installs are found first, then the Flatpak and Snap
sandboxes. The right profile is chosen by looking for `__Secure-1PSIDTS` — the
one cookie Google actually authenticates with — rather than by counting
cookies. Chromium's values are decrypted the way the browser wrote them: the
Secret Service keyring, or the `v10` fallback when there is none.

If no browser has a session, the TUI shows a sign-in screen. Press **enter**
and youtuimusic opens your default browser to the sign-in page — the browser
you already use, with its extensions and saved passwords, not a fresh profile.
Sign in there, then press **enter** again to read it. The session is cached in the
config directory (`youtuimusic/session.json`) as the fallback for a locked
keyring or a machine with no browser, and refreshed from the browser on every
successful read. Set `YOUTUIMUSIC_SESSION` to use a captured header file
alone, or run `youtuimusic --auth-clear` to delete the cached session. The
browser's own cookies are left alone, so the next run offers the sign-in
screen again.

### Build from source

Needs `mpv` and `yt-dlp` on `PATH`. No particular font: the interface draws
nothing outside the usual range, and a test enforces it.

```bash
go build ./cmd/youtuimusic && ./youtuimusic
```

Tests are offline by default. The ones that need the network are opt-in:

```bash
go test ./...                  # fast, no network
YTM_NET=1 go test ./...        # includes resolve + end-to-end playback
```

## Decisions, and the measurements behind them

Each of these was tested before it was chosen, mostly against a real library
on a real machine rather than from documentation.

**mpv over JSON IPC, not libmpv bindings.** The IPC socket exposes commands,
property reads and property observation — everything the player needs — at a
~29 µs round trip. That keeps the binary cgo-free, avoids the LGPL linkage,
and drops the `LC_NUMERIC=C` workaround libmpv needs to not segfault.

**yt-dlp for stream URLs, as a subprocess.** The tempting alternative is
calling InnerTube's `player` endpoint directly: it answers in ~117 ms versus
yt-dlp's ~1.3 s, at identical quality (opus 136 kbps). It was measured against
18 real tracks and returned a playable URL for **0 of them** — `LOGIN_REQUIRED`
or, on the `ios`/`android` clients, 23-25 formats with no URL at all because
YouTube now serves them over SABR. yt-dlp resolved every one. What yt-dlp
actually buys is PO tokens, client fallbacks and SABR handling: an arms race
won by community size, not by language.

**Prefetch rather than optimise the resolve.** A cold resolve is ~2.3 s; a
cache hit is ~600 ns. Resolving the highlighted row before the user presses
play is worth more than any speedup to resolving itself.

**The API cannot sort anything this shows, so sorting is done here.** Order
parameters exist, but only on the library browse endpoints — added songs,
library albums, uploads — and only as `a_to_z`, `z_to_a` and
`recently_added`. A playlist's contents take no order parameter, and the
liked playlist is a playlist: `VLLM`. Checked against ytmusicapi's
`get_playlist` and `get_library_songs` rather than assumed.

**One page at a time, and the rest on request.** Listings are paged at the
server — 100 tracks for a playlist, 20 results for a search — and reads cost
what they page: 100 tracks ≈ 0.8 s, 529 tracks ≈ 2.7 s. So a page comes back
with the token for the next rather than the requests being spent up front,
and a list that has more ends with a row offering it. Reaching that row —
walking onto it, scrolling to it, or clicking it — is what asks.

## Using it

| | |
|---|---|
| `h`/`l`, `←`/`→`, `tab` | move between playlist tabs |
| `j`/`k`, `↑`/`↓` | move the cursor |
| `pgup`/`pgdown`, `ctrl+u`/`ctrl+d` | a window at a time |
| `g` / `G`, `home`/`end` | the top, the bottom |
| `enter` | play the highlighted track |
| `space` | pause and resume, or start the highlighted track |
| `n` / `p` | next and previous track |
| `r` | repeat: off, all, one |
| `/` | search in a popover, `enter` to run it, `esc` to close |
| `+` / `-` | thumbs up or down; the same key again clears it |
| `s` / `S` | sort by the next column, and reverse it |
| `ctrl+c` | quit |

With the track menu or the popover open, `j`/`k` and `enter` work it and
`esc` closes it. The transport keys keep working either way: pausing should
not depend on what is on top.

The progress bar and the controls share a box — transport on the left,
repeat on the right — and under it a status bar says what the app is doing
and what is playing, in two blocks the way lipgloss's
own example lays one out. Rating is not a glyph: a liked or disliked row is
drawn in the colour of that rating, so a page of them reads at a glance, and
the same colours appear as marks down the scrollbar. `+` and `-` rate the
highlighted row.

Right-clicking a track opens a menu: like or unlike it, go to its album, go
to its artist. A track that links nowhere has those rows greyed. An artist
opens with their songs and then their releases; a release has nothing to
play, so opening one shows the album.

Albums, artists and search all open the same popover over the list, inset so
that what it covers is still visible around it and stopping short of the
player, which stays usable. It says what it is showing, carries its own table — same columns, same
scrolling, same paging — and `esc` or a click outside closes it, leaving everything
underneath as it was. The search one puts its box on the top line.

The table names its columns, and clicking one sorts by it — again to
reverse. Sorting an incomplete listing would put the wrong rows at the top,
so asking for an order fetches the rest of it and re-sorts as the pages
land. Clearing the sort puts the listing back in the order it arrived in. A listing that says when its tracks were added, which in practice
means the liked playlist, gets a column for it; one that does not is not
given a column of blanks.

A list longer than the window gets a scrollbar down its right edge. It can
be clicked and dragged, and it is only there when there is something to
scroll.

The mouse works too: click a tab to open it, click a track to select it and
again to play it, click any control, and drag the progress bar to scrub. The wheel changes tab
over the tab row; over the list it moves the view and leaves the selection
where it is, so looking further down a playlist does not lose your place.
Moving the cursor brings the view back to it.

A tab already visited comes back from memory, so moving between them is
instant after the first look. A move only reaches the server once the
selection settles, so running across the tabs is one request rather than one
per tab.

**Colours come from the terminal, not from this program.** Everything drawn
names an entry in the sixteen-colour ANSI palette, so the scheme the user
already has is the scheme the app wears. Three names in `internal/ui` decide
all of it — `accent`, `accentBright` and `muted` — so recolouring the
interface is one edit. That rules out the progress
component's own blend: it interpolates in RGB and emits true colour, so the
steps between two named endpoints are values this program invented. The bar
uses a colour function returning palette entries instead — a ramp with steps
rather than a fade, softened by the half block, which carries a foreground
and a background and so fits two steps in every cell. A test asserts no
frame ever emits a `38;5;` or `38;2;` sequence.

## Layout

```
cmd/youtuimusic     entry point
internal/ytm        InnerTube client (auth, playlists, search, rating)
internal/auth       the session: the browser's cookies first, a file second
internal/chromium   Chromium family: profiles, keyring, cookie decryption
internal/gecko      Firefox family: cookies.sqlite and profiles.ini
internal/player     mpv over JSON IPC
internal/stream     yt-dlp resolution + cache
internal/ui         bubbletea model, tabs / table / progress. One table
                    component draws both the main list and the popover's
```

Packaging and the release process live in [RELEASE.md](RELEASE.md).

## Status

- [x] `internal/player` — mpv IPC, observed state, proven end to end
- [x] `internal/stream` — resolution, TTL cache, single-flight
- [x] `internal/ytm` — playlists, tracks, search and rating, all verified
      against a real account
- [x] `internal/ui` — bubbletea shell, wired to the backends, keyboard and
      mouse, scrolling
- [x] `internal/auth` — reads the session out of a signed-in browser, in the
      TUI when none is found, with a cached file as fallback
- [x] `internal/chromium` + `internal/gecko` — profile discovery, keyring and
      cookie decryption for the Chromium and Firefox families
