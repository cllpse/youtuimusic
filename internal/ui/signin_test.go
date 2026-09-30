package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// With no library but a SignIn, the app starts on the sign-in screen and
// waits for enter rather than reading the browser on its own.
func TestSignInWaitsForEnter(t *testing.T) {
	lib := library()
	st := &fakeStreams{}
	signIn := func(context.Context) (Library, error) { return lib, nil }
	base := New(Services{Streams: st, Audio: newFakeAudio(), SignIn: signIn})

	if !base.signedOut {
		t.Fatal("a model with SignIn but no Library should start signed out")
	}
	boot := drain(t, base, base.Init())
	if !boot.signedOut || boot.signingIn {
		t.Fatalf("Init signed in on its own: signedOut=%v signingIn=%v", boot.signedOut, boot.signingIn)
	}

	next, cmd := boot.Update(keyPress("enter"))
	if !next.(Model).signingIn {
		t.Error("enter did not start the sign-in")
	}
	m := drain(t, next.(Model), cmd)

	if m.signedOut || m.signingIn {
		t.Fatalf("still signing in: signedOut=%v signingIn=%v", m.signedOut, m.signingIn)
	}
	if m.services.Library == nil {
		t.Error("the signed-in library was not installed")
	}
	if len(m.Playlists) != 2 || len(m.Tracks) == 0 {
		t.Fatalf("library did not load: playlists=%d tracks=%d", len(m.Playlists), len(m.Tracks))
	}
}

// A failed sign-in keeps the error and offers a retry with enter.
func TestSignInFailureOffersRetry(t *testing.T) {
	lib := library()
	attempts := 0
	signIn := func(context.Context) (Library, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("no browser")
		}
		return lib, nil
	}
	base := New(Services{Streams: &fakeStreams{}, Audio: newFakeAudio(), SignIn: signIn})
	m := drain(t, base, base.Init())

	next, cmd := m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if !m.signedOut || m.signingIn {
		t.Fatalf("state after failure: signedOut=%v signingIn=%v", m.signedOut, m.signingIn)
	}
	if m.Err == nil {
		t.Fatal("the failure was not kept")
	}

	next, cmd = m.Update(keyPress("enter"))
	m = drain(t, next.(Model), cmd)
	if m.signedOut || m.Err != nil {
		t.Fatalf("the retry did not settle: err=%v", m.Err)
	}
	if attempts != 2 {
		t.Errorf("sign-in attempts = %d, want 2", attempts)
	}
}

// The sign-in screen names itself, so a first run is explained.
func TestSignInScreenIsShown(t *testing.T) {
	m := New(Services{SignIn: func(context.Context) (Library, error) { return nil, nil }})
	m.width, m.height = 100, 24
	if got := m.View().Content; !strings.Contains(got, "Sign in to YouTube Music") {
		t.Errorf("sign-in screen missing: %q", got)
	}
}

// A zero Services still renders nothing and is inert; the sign-in screen is
// only for a model that was given a way to sign in.
func TestZeroServicesStaysInertWithNoSignIn(t *testing.T) {
	m := New(Services{})
	if m.signedOut {
		t.Error("a model with no SignIn should not claim to be signed out")
	}
	if got := m.services.Library; got != nil {
		t.Errorf("library = %v, want nil", got)
	}
}
