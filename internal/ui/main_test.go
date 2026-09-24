package ui

import (
	"os"
	"testing"
)

// The app writes where it was on the way out, so any test that quits would
// otherwise overwrite the real one's state. A home of its own for the whole
// package means no test can reach the user's files by accident, whether or
// not its author thought about it.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "youtuimusic-ui-test-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	for _, name := range []string{"HOME", "XDG_CONFIG_HOME"} {
		if err := os.Setenv(name, home); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}
