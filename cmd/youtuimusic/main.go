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

// version is overridden at link time: -X main.version=v1.2.3.
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
	// A signal stops the program the way ctrl+c does, so mpv is not left
	// behind.
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

	// A cached session starts straight into the library. Without one the TUI
	// shows the sign-in screen, and enter reads the browser. This is what
	// --auth-clear resets: it removes the cache, not the browser's cookies.
	var library ui.Library
	if session, ok := auth.Cached(); ok {
		library = ytm.NewClient(session)
	}
	signIn := func(context.Context) (ui.Library, error) {
		// Signing in is a browser window: open the browser you already use,
		// so it gets your extensions and saved passwords, then read the
		// session out of it. The read can still succeed without the window
		// when the browser is already signed in.
		openErr := auth.OpenLogin()
		session, err := auth.Load()
		if err == nil {
			return ytm.NewClient(session), nil
		}
		if openErr != nil {
			return nil, fmt.Errorf("%w (could not open a browser: %v)", err, openErr)
		}
		return nil, err
	}

	model := ui.New(ui.Services{
		Library: library,
		Streams: streams,
		Audio:   audio,
		SignIn:  signIn,
	}).Restore(state.Load())

	final, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	// The key handler records on the way out; a signal does not go through it,
	// so record once more here. The write is idempotent.
	if m, ok := final.(ui.Model); ok {
		m.Record()
	}
	return err
}
