# youtuimusic

A small YouTube Music TUI: playlists on the left, tracks on the right, a
progress bar at the bottom. Search, and thumbs up/down. That is the whole
scope, deliberately.

## Running

Needs `mpv` and `yt-dlp` on `PATH`.

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
| `tab`, `h`/`l`, `←`/`→` | move between the sidebar and the table |
| `j`/`k`, `↑`/`↓` | move the cursor |
| `enter` | open a playlist, or play the highlighted track |
| `space` | pause and resume |
| `/` | search, `enter` to run it, `esc` to cancel |
| `+` / `-` | thumbs up or down; the same key again clears it |
| `q`, `ctrl+c` | quit |

The mouse works too: click a playlist to open it, click a track to select it
and again to play it, scroll either list with the wheel, and drag the
progress bar to scrub. Scrolling deliberately does not move keyboard focus.

## Layout

```
cmd/youtuimusic     entry point
internal/ytm        InnerTube client (auth, playlists, search, rating)
internal/player     mpv over JSON IPC
internal/stream     yt-dlp resolution + cache
internal/ui         bubbletea model, sidebar / table / progress
```

## Status

- [x] `internal/player` — mpv IPC, observed state, proven end to end
- [x] `internal/stream` — resolution, TTL cache, single-flight
- [x] `internal/ytm` — playlists, tracks, search and rating, all verified
      against a real account
- [x] `internal/ui` — bubbletea shell, wired to the backends, keyboard and
      mouse
- [ ] `internal/chromium` — reading the browser's cookies directly, so that
      signing in needs no steps. Decryption and the keyring are done; profile
      discovery is not. Until it lands, `internal/auth` reads a session file.
