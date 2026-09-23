// Command youtuimusic is a small YouTube Music TUI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/auth"
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
		if errors.Is(err, auth.ErrNoSession) {
			return fmt.Errorf("%w\n\nSign in with a Chromium-family browser and export "+
				"the session, or point %s at a file of request headers", err, auth.EnvPath)
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
