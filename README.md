# youtuimusic

A small YouTube Music TUI: playlists as tabs, tracks below them, a progress
bar at the bottom. Search, and thumbs up/down. That is the whole scope,
deliberately.

## Running

Needs `mpv` and `yt-dlp` on `PATH`, and a terminal set to a [Nerd
Font](https://www.nerdfonts.com/) — the transport controls are Material
Design icons from the private use area, and without one they are blank boxes.

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

**Fetch the first page of a playlist, not all of it.** Playlist reads are
paged at ~100 tracks server-side: 100 tracks ≈ 0.8 s, 529 tracks ≈ 2.7 s. Show
the first page and fill in the rest behind it.

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
| `/` | search, `enter` to run it, `esc` to cancel; results arrive as their own tab |
| `+` / `-` | thumbs up or down; the same key again clears it |
| `q`, `ctrl+c` | quit |

Under the bar are the controls: transport on the left, thumbs in the middle,
repeat on the right. The thumbs there act on what is playing, which is what
sitting beside the transport means; `+` and `-` still act on the highlighted
row.

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
internal/player     mpv over JSON IPC
internal/stream     yt-dlp resolution + cache
internal/ui         bubbletea model, tabs / table / progress
```

## Status

- [x] `internal/player` — mpv IPC, observed state, proven end to end
- [x] `internal/stream` — resolution, TTL cache, single-flight
- [x] `internal/ytm` — playlists, tracks, search and rating, all verified
      against a real account
- [x] `internal/ui` — bubbletea shell, wired to the backends, keyboard and
      mouse, scrolling
- [ ] `internal/chromium` — reading the browser's cookies directly, so that
      signing in needs no steps. Decryption and the keyring are done; profile
      discovery is not. Until it lands, `internal/auth` reads a session file.
