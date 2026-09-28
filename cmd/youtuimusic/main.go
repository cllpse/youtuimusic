// Command youtuimusic is a small YouTube Music TUI.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/auth"
	"github.com/cllpse/youtuimusic/internal/chromium"
	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/state"
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

	// A signal stops the program the way ctrl+c does, so the session is saved
	// on the way out and mpv is not left behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// mpv is started once and lives for the session; the first track pays no
	// process startup.
	audio, err := player.New(ctx)
	if err != nil {
		return fmt.Errorf("starting mpv: %w", err)
	}
	defer func() { _ = audio.Close() }()

	streams := stream.New()
	streams.CachePath = stream.DefaultCachePath()
	streams.Load()

	model := ui.New(ui.Services{
		Library: ytm.NewClient(session),
		Streams: streams,
		Audio:   audio,
	}).Restore(state.Load())

	final, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	// The key handler records on the way out; a signal does not go through it,
	// so record once more here. The write is idempotent.
	if m, ok := final.(ui.Model); ok {
		m.Record()
	}
	return err
}
