# Third-party notices

youtuimusic is MIT-licensed. The released archives also carry two external
programs that it runs as subprocesses. They are separate works, aggregated for
convenience, and keep their own licenses.

## yt-dlp

- Source: https://github.com/yt-dlp/yt-dlp
- License: The Unlicense (public domain)
- Shipped as: `yt-dlp` (the standalone PyInstaller build)

## mpv

- Source: https://github.com/mpv-player/mpv
- macOS builds: official release artifacts from the mpv project
- Linux builds: https://github.com/pkgforge-dev/mpv-AppImage
- License: GPLv2-or-later (with some parts under LGPLv2.1-or-later)

mpv bundles FFmpeg, libplacebo and other libraries, whose licenses
(LGPL/GPL) also apply to those components. The complete corresponding source
for each shipped binary is available from the upstream projects above and
their release pages; youtuimusic redistributes the binaries unmodified.

If you need the exact source for a shipped build, open an issue and it will be
provided.
