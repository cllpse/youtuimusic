// Command youtuimusic is a small YouTube Music TUI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/auth"
	"github.com/cllpse/youtuimusic/internal/chromium"
	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/stream"
	"github.com/cllpse/youtuimusic/internal/ui"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "youtuimusic:", err)
		os.Exit(1)
	}
}

func run() error {
	session, err := auth.Load()
	if err != nil {
		// Telling someone to export a session would be wrong now: the
		// app reads the browser, so the fix is nearly always in the
		// browser rather than in a file.
		if errors.Is(err, chromium.ErrNoBrowser) || errors.Is(err, auth.ErrNoSession) {
			return fmt.Errorf("%w\n\nSign in to %s in a Chromium-based browser and "+
				"run this again — the session is read from there. If the browser's "+
				"keyring is locked, unlock it first. To use a captured session "+
				"instead, point %s at a file of request headers",
				err, auth.Host, auth.EnvPath)
		}
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// mpv is started once and lives for the session; the first track pays no
	// process startup.
	audio, err := player.New(ctx)
	if err != nil {
		return fmt.Errorf("starting mpv: %w", err)
	}
	defer func() { _ = audio.Close() }()

	model := ui.New(ui.Services{
		Library: ytm.NewClient(session),
		Streams: stream.New(),
		Audio:   audio,
	})

	_, err = tea.NewProgram(model).Run()
	return err
}
