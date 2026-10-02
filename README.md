# youtuimusic

A small YouTube Music TUI: playlists as tabs, tracks below them, a progress
bar at the bottom. Search, albums and artists, mixes, and thumbs up/down. It
is kept that small deliberately.

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

An Arch (AUR) package is written but not yet published; see
[RELEASE.md](RELEASE.md). Homebrew installs `mpv` and `yt-dlp` as
dependencies rather than unpacking the bundled copies, and the AUR package
will too.

The installer checks the archive against the release's `checksums.txt` before
unpacking anything. It takes its options from the environment, which with a
pipe means setting them on `sh`, not on `curl`:

```bash
# a given release rather than the latest
curl -fsSL https://raw.githubusercontent.com/cllpse/youtuimusic/main/install.sh | sh -s -- v0.2.0

# no sudo: the binary in ~/.local/bin, mpv and yt-dlp in ~/.local/libexec/youtuimusic
curl -fsSL https://raw.githubusercontent.com/cllpse/youtuimusic/main/install.sh | YOUTUIMUSIC_INSTALL_DIR="$HOME/.local/bin" sh

# the binary only; mpv and yt-dlp are up to you
curl -fsSL https://raw.githubusercontent.com/cllpse/youtuimusic/main/install.sh | YOUTUIMUSIC_NO_DEPS=1 sh
```

| variable | what it does |
|---|---|
| `YOUTUIMUSIC_INSTALL_DIR` | where the binary goes; `/usr/local/bin` by default, with `sudo` only if it is not writable |
| `YOUTUIMUSIC_LIBEXEC_DIR` | where the bundled `mpv` and `yt-dlp` go; `<install dir>/../libexec/youtuimusic` by default |
| `YOUTUIMUSIC_NO_DEPS=1` | install the binary and nothing else |

The older `YTMUIMUSIC_*` spellings of these still work.

youtuimusic only looks for its bundled programs in three places: beside its
own executable, in `libexec/youtuimusic` next to it, or in
`../libexec/youtuimusic` above it (symlinks to the executable are followed
first). `YOUTUIMUSIC_LIBEXEC_DIR` has to be one of those —
`<install dir>/../libexec/youtuimusic` or `<install dir>/libexec/youtuimusic`
— or the programs are installed where nothing will find them.

The two are looked up in opposite orders. `mpv` takes the bundled copy first
and `PATH` second. `yt-dlp` is the other way round: it has to keep up with
YouTube, so a newer copy on `PATH` wins over the bundled snapshot and the
bundle is only the fallback. The installer skips a bundled program that is
already on `PATH`, so it fills in only what is missing rather than shadowing
what you already have. The bundled programs are third-party works with their
own licenses — see `THIRD_PARTY_NOTICES.md`.

### Signing in

youtuimusic reads the session out of a browser you are already signed in to.
Chrome (stable, Beta and Dev), Chromium, Ungoogled Chromium, Edge, Brave,
Opera, Vivaldi and Helium are read through their Chromium cookie store;
Firefox, LibreWolf, Waterfox, Zen and Floorp through `cookies.sqlite`. On
Linux, native installs are found first, then the Flatpak and Snap sandboxes.
A profile counts as signed in when it has both `__Secure-1PSIDTS`, the cookie
Google authenticates with, and `__Secure-3PAPISID`, the one requests are
signed with. When several are, the one whose session was used most recently
wins, whichever browser it is in.

Chromium's values are decrypted with the password from the Secret Service or
KWallet on Linux, or the Keychain on macOS. On Linux, cookies written with
Chromium's `v10` fallback password need no keyring at all; macOS has no such
fallback. The keyring is only asked when a YouTube cookie needs it.

The cookie Google authenticates with rotates about every ten minutes, and
only the browser is handed the new one. When a request comes back signed out partway
through a session, youtuimusic reads the browser again and retries.

To sign in, sign in to [music.youtube.com](https://music.youtube.com) in one
of those browsers and start youtuimusic. The session is cached in
`~/.config/youtuimusic/session.json` as the fallback
for a locked keyring or a machine with no browser, and rewritten from the
browser on every successful read. If no browser has a session and there is
no cached one either, youtuimusic says so and exits, with the reason for each
browser it looked in. `youtuimusic --auth-clear` deletes the cached session;
the browser's own cookies are left alone.

To sign in with a captured session instead, point `YOUTUIMUSIC_SESSION` at a
file. The browser is then not read at all, not even mid-session, and the file
is never written to; if it does not exist, the cached session above is used.
The file is a JSON object of request headers. `cookie` is required;
`user-agent`, `x-goog-authuser` and `x-goog-visitor-id` are used when present,
and header names match regardless of case:

```json
{
  "cookie": "__Secure-1PSIDTS=…; __Secure-3PAPISID=…; …",
  "user-agent": "Mozilla/5.0 …"
}
```

### Files

| path | what is in it |
|---|---|
| `~/.config/youtuimusic/session.json` | the cached session (mode 0600) — the whole account, so treat it like a password |
| `~/.config/youtuimusic/state.json` | the playlist that was open, the track that was playing, and `"colour": true` if colour was switched on |
| `~/.cache/youtuimusic/streams.json` | resolved stream URLs until they expire; `$XDG_CACHE_HOME/youtuimusic/` when that is set, `~/Library/Caches/youtuimusic/` on macOS |

The first two are kept under `~/.config` on macOS too. Deleting the cache
costs one `yt-dlp` run per track and nothing else.

### Build from source

Needs the Go version named in `go.mod`, and `mpv` and `yt-dlp` on `PATH`. No
cgo, so `CGO_ENABLED=0` gives a static binary, which is how releases are
built. No particular font: the interface draws nothing outside the usual
range, and a test enforces it.

```bash
go build ./cmd/youtuimusic && ./youtuimusic
```

A build outside GoReleaser reports its version as `dev`.

Tests are offline by default. The ones that need the network are opt-in:

```bash
go test ./...                       # fast, no network
YTM_NET=1 go test ./...             # adds resolve, end-to-end playback and the live API tests
YTM_NET=1 YTM_MUTATE=1 go test ./internal/ytm   # also rates a real track, then puts it back
```

The live API tests sign in with the session youtuimusic saved on its last run
(`~/.config/youtuimusic/session.json`, or the file `YOUTUIMUSIC_SESSION`
names), and skip when there is none — run the app once first.

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

`?` shows every key in the app. That sheet is drawn from the same bindings
the keys dispatch on, so it cannot fall behind; this table is a copy of it.

The player — these work whatever is in front:

| | |
|---|---|
| `space` | play or pause; with nothing played yet, play the highlighted track |
| `n` / `p` | next and previous track |
| `r` | repeat: off, on (the whole list, round and round), one |
| `+` / `-` | like or dislike the highlighted track; the same key again clears it (`=` and `_` work too). Disliking the track that is playing moves on from it |

Getting around:

| | |
|---|---|
| `j`/`k`, `↓`/`↑` | down and up a row |
| `pgdown`/`pgup`, `ctrl+d`/`ctrl+u` | a page at a time |
| `g` / `G`, `home`/`end` | the first row, the last row |
| `h`/`l`, `←`/`→`, `shift+tab`/`tab` | previous and next playlist tab |

Everything else:

| | |
|---|---|
| `enter` | play the highlighted track, or open a release |
| `/` | search in a popover, `enter` to run it, `esc` to close |
| `M` | start a mix from the track playing, or from the highlighted one when nothing is |
| `s` / `S` | sort by the next column, and reverse it (not on a mix) |
| `m` | colour on and off; the app opens in monochrome |
| `R` | refetch the list in front, ignoring what is cached |
| `esc` | close what is in front: the popover, the menu, the key sheet |
| `?` | these keys |
| `ctrl+c` | quit |

With the track menu or the popover open, `j`/`k` and `enter` work it and
`esc` closes it. The transport keys keep working either way: pausing should
not depend on what is on top.

The progress bar and the controls share a box — transport on the left,
repeat on the right — and under it a status bar says what the app is doing
and what is playing, in two blocks the way lipgloss's
own example lays one out. Rating is not a glyph. With colour on, a liked or
disliked row is drawn in the colour of that rating, so a page of them reads
at a glance, and the same colours appear as marks down the scrollbar. In
monochrome a rated row is drawn plain, with no marks — greying a dislike
would make it look like a row that cannot be chosen — and the track menu
says which way it is rated. `+` and `-` rate the highlighted row.

Right-clicking a track opens a menu: go to its album, go to its artist, start
a mix from it, like or unlike it, dislike it or remove the dislike. A dislike
from the menu asks for confirmation first. A track that links nowhere has
those rows greyed. An artist opens with their songs and then their releases;
a release has nothing to play, so opening one shows the album.

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
already has is the scheme the app wears. The colours are named for what they
are for rather than what they look like, all in `internal/ui/theme.go`, so
recolouring the interface is one edit there. Where the palette has no colour
for a job, one is mixed from the terminal's own rather than invented: the row
highlight is a tint of the background it reports, and the status block is
each state's hue lightened, with the word on it the same hue darkened. How far
is worked out per colour — a deep blue needs lifting further than a pastel
yellow before dark text reads on it — so the word holds 5:1 contrast whatever
the theme. The terminal is asked for these (OSC 11 for the background, OSC 4
for red, green, yellow and blue) at startup and again when the window or the
focus changes, and until it answers — or in one that never does — the
palette colours are drawn as they are.

The app opens in monochrome: the terminal's own foreground and background,
with weight — faint, ordinary, bright — and inversion doing what hues would.
Only the status block keeps its state colours: green for nothing to report,
yellow while it waits — on a list, or on a track that has not started
sounding yet (resolving it, mpv opening it, or a stall on a slow network) —
blue for the player, red for an error. While it waits, the block carries the
same spinner the list's loader turns — `▓LOADING` — so a wait looks the same
wherever it is said. There is one spinner for every wait, and it stops the
moment nothing is waited on, so a still screen is not redrawn for nothing.
`m` switches colour on, and liked and disliked rows, mixes and the progress
bar get their hues. The choice is remembered in
`~/.config/youtuimusic/state.json` as `"colour": true`; a state file from
before monochrome became the default opens in monochrome.

The progress bar is drawn here rather than by the progress component, whose
blend interpolates in RGB and emits true colour — values this program would
have invented. It is two runs of palette colours: the played part in the
player's colour (the bright foreground in monochrome, a quieter step while
paused), the rest a groove in the row highlight. Two runs are two styles a
frame, where a fill from a colour function was one style per cell. A test
asserts no frame ever emits a `38;5;` or `38;2;` sequence.

## Layout

```
cmd/youtuimusic     entry point
internal/ytm        InnerTube client (auth, playlists, search, rating)
internal/auth       the session: the browser's cookies first, a file second
internal/chromium   Chromium family: profiles, keyring/KWallet/Keychain, decryption
internal/gecko      Firefox family: cookies.sqlite and profiles.ini
internal/jar        which cookies a request sends, and which profile is signed in
internal/sqlitescan read-only SQLite file reader for the cookie stores, WAL included
internal/state      what to reopen: the playlist, the track, the colour choice
internal/tool       finds mpv and yt-dlp: the bundled copy or the one on PATH
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
- [x] `internal/auth` — reads the session out of a signed-in browser at
      startup, with a cached file as fallback
- [x] `internal/chromium` + `internal/gecko` — profile discovery, keyring and
      cookie decryption for the Chromium and Firefox families
- [x] `internal/jar` — the cookies a request sends, and the choice between
      signed-in profiles by which was used most recently
- [x] `internal/sqlitescan` — the cookie stores read straight from the file
      format, uncheckpointed write-ahead log included, without linking SQLite
- [x] `internal/state` — the open playlist, the playing track and the colour
      choice, restored at launch
- [x] `internal/tool` — mpv and yt-dlp found beside the binary or on `PATH`
