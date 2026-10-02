// Command youtuimusic is a small YouTube Music TUI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/cllpse/youtuimusic/internal/auth"
	"github.com/cllpse/youtuimusic/internal/player"
	"github.com/cllpse/youtuimusic/internal/state"
	"github.com/cllpse/youtuimusic/internal/stream"
	"github.com/cllpse/youtuimusic/internal/ui"
	"github.com/cllpse/youtuimusic/internal/ytm"
)

// version is overridden at link time with -X main.version=1.2.3; GoReleaser
// passes its {{.Version}}, which has no leading v.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		fs := flag.NewFlagSet("youtuimusic", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		showVersion := fs.Bool("version", false, "print version and exit")
		clearAuth := fs.Bool("auth-clear", false, "clear the saved sign-in and exit")
		if err := fs.Parse(os.Args[1:]); err != nil {
			os.Exit(2)
		}
		if *showVersion {
			fmt.Println(version)
			return
		}
		if *clearAuth {
			if err := auth.Clear(); err != nil {
				fmt.Fprintln(os.Stderr, "youtuimusic:", err)
				os.Exit(1)
			}
			fmt.Println("authorization cleared")
			return
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(os.Stderr, "youtuimusic: unknown argument %q\n", fs.Arg(0))
			os.Exit(2)
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "youtuimusic:", err)
		os.Exit(1)
	}
}

func run() error {
	// The session comes first: it can wait on a keyring prompt, which has to
	// happen before the TUI owns the terminal, and without one there is no
	// point starting mpv.
	session, err := auth.Load()
	if err != nil {
		return fmt.Errorf("no YouTube Music session found.\n\n"+
			"Sign in to https://music.youtube.com in your browser, then run youtuimusic again.\n"+
			"Supported: Chrome, Chromium, Brave, Edge, Vivaldi, Opera, Helium, Firefox,\n"+
			"LibreWolf, Waterfox, Zen and Floorp.\n\n%w", err)
	}

	// A signal stops the program the way ctrl+c does, so mpv is not left
	// behind.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	// The browser keeps the session alive and the API never does, so a
	// request turned away mid-listen goes back to the browser for its copy.
	client := ytm.NewClient(session)
	client.Refresh = auth.Load

	model := ui.New(ui.Services{
		Library: client,
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
