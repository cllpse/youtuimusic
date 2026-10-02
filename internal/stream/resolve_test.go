package stream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A public, unrestricted track. Network tests are opt-in via YTM_NET=1 so the
// default `go test` stays fast and offline.
const publicTrack = "dQw4w9WgXcQ"

func requireNetwork(t *testing.T) {
	t.Helper()
	if os.Getenv("YTM_NET") != "1" {
		t.Skip("set YTM_NET=1 to run tests that hit the network")
	}
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		t.Skip("yt-dlp not installed")
	}
}

func TestCacheMissOnEmpty(t *testing.T) {
	r := New()
	if _, ok := r.Cached("nope"); ok {
		t.Fatal("empty resolver reported a cache hit")
	}
}

func TestCacheHitAndExpiry(t *testing.T) {
	r := New()
	want := Track{VideoID: "abc", URL: "https://example/stream"}

	r.cache["abc"] = entry{track: want, expires: time.Now().Add(time.Minute)}
	got, ok := r.Cached("abc")
	if !ok || got.URL != want.URL {
		t.Fatalf("cache hit = (%v, %v), want the stored track", got, ok)
	}

	r.cache["abc"] = entry{track: want, expires: time.Now().Add(-time.Second)}
	if _, ok := r.Cached("abc"); ok {
		t.Fatal("expired entry was served from cache")
	}
}

// A failing resolve is never served as a track. (It is remembered as a
// failure for failureTTL instead — see TestAFailureIsRemembered.)
func TestFailureIsNotCached(t *testing.T) {
	r := New()
	r.Binary = "definitely-not-a-real-binary"
	if _, err := r.Resolve(context.Background(), "abc"); err == nil {
		t.Fatal("expected an error from a missing binary")
	}
	if _, ok := r.Cached("abc"); ok {
		t.Fatal("a failed resolve was cached")
	}
}

// Prefetch and play can race on the same track; only one yt-dlp should run.
func TestConcurrentResolvesShareOneRun(t *testing.T) {
	r := New()
	r.Binary = "definitely-not-a-real-binary"

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = r.Resolve(context.Background(), "same-id")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil {
			t.Fatalf("caller %d got no error", i)
		}
	}
	r.mu.Lock()
	n := len(r.inFlight)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("in-flight map leaked %d entries", n)
	}
}

func TestResolveRealTrack(t *testing.T) {
	requireNetwork(t)
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	track, err := r.Resolve(ctx, publicTrack)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	cold := time.Since(start)
	if track.URL == "" {
		t.Fatal("no URL")
	}
	t.Logf("cold resolve: %v  codec=%s abr=%.0f dur=%v",
		cold, track.Codec, track.Bitrate, track.Duration)

	start = time.Now()
	if _, err := r.Resolve(ctx, publicTrack); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	warm := time.Since(start)
	t.Logf("cached resolve: %v", warm)

	// The whole prefetch strategy rests on a cache hit being effectively free.
	if warm > 5*time.Millisecond {
		t.Fatalf("cached resolve took %v, expected it to be ~instant", warm)
	}
}

// A failed resolve is remembered as a failure for a short while, so pressing
// play again on a track yt-dlp cannot get does not spawn the process again for
// the same answer.
func TestAFailureIsRemembered(t *testing.T) {
	r := New()
	r.Binary = "definitely-not-a-real-binary"
	_, first := r.Resolve(context.Background(), "abc")
	if first == nil {
		t.Fatal("expected an error")
	}
	if r.cachedFailure("abc") == nil {
		t.Fatal("the failure was not remembered")
	}
	// The success cache must still say nothing, or the UI would try to play
	// a track that has no URL.
	if _, ok := r.Cached("abc"); ok {
		t.Fatal("a failed resolve was served from the success cache")
	}
}

// Resolved URLs are written to disk, so reopening the app does not throw away
// a stream that is still valid.
func TestResolvedURLsSurviveARestart(t *testing.T) {
	path := t.TempDir() + "/streams.json"
	want := Track{VideoID: "abc", URL: "https://example/stream", Duration: time.Minute}

	first := New()
	first.CachePath = path
	first.cache["abc"] = entry{track: want, expires: time.Now().Add(time.Hour)}
	first.save()

	second := New()
	second.CachePath = path
	second.Load()
	got, ok := second.Cached("abc")
	if !ok || got.URL != want.URL || got.Duration != want.Duration {
		t.Fatalf("after a restart: (%+v, %v), want %+v", got, ok, want)
	}
}

// An entry whose URL has expired is dropped on load rather than offered.
func TestExpiredURLsAreDroppedOnLoad(t *testing.T) {
	path := t.TempDir() + "/streams.json"
	first := New()
	first.CachePath = path
	first.cache["abc"] = entry{track: Track{VideoID: "abc", URL: "u"}, expires: time.Now().Add(-time.Minute)}
	first.save()

	second := New()
	second.CachePath = path
	second.Load()
	if _, ok := second.Cached("abc"); ok {
		t.Fatal("an expired entry survived the restart")
	}
}

// slowBinary is a stand-in yt-dlp that never answers in time, and counts
// how many copies of it are running in dir.
func slowBinary(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\ntouch \"" + dir + "/run.$$\"\nsleep 2\nrm -f \"" + dir + "/run.$$\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

// A resolve its caller gave up on says nothing about the track, so it is not
// remembered as a failure: the next press of play tries again.
func TestACancelledResolveIsNotRemembered(t *testing.T) {
	bin, _ := slowBinary(t)
	r := New()
	r.Binary = bin
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := r.Resolve(ctx, "abc")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the deadline", err)
	}
	if r.cachedFailure("abc") != nil {
		t.Error("a cancelled resolve was remembered as the track failing")
	}
}

// A cursor stepping down a playlist asks for a prefetch per row. No more
// than maxPrefetches run at once, and the ones that waited are dropped in
// favour of the newest.
func TestPrefetchesAreCapped(t *testing.T) {
	bin, dir := slowBinary(t)
	r := New()
	r.Binary = bin
	for i := 0; i < 6; i++ {
		r.Prefetch(fmt.Sprintf("row%d", i))
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	running, _ := filepath.Glob(filepath.Join(dir, "run.*"))
	if len(running) > maxPrefetches {
		t.Errorf("%d yt-dlp processes running, want at most %d", len(running), maxPrefetches)
	}
}

// A URL that would not play is dropped, from the file as well, so neither
// the next press of play nor the next launch hands it out again.
func TestForgetDropsAURLForGood(t *testing.T) {
	path := t.TempDir() + "/streams.json"
	r := New()
	r.CachePath = path
	r.cache["abc"] = entry{track: Track{VideoID: "abc", URL: "https://x"}, expires: time.Now().Add(time.Hour)}
	r.save()

	r.Forget("abc")
	if _, ok := r.Cached("abc"); ok {
		t.Fatal("the forgotten URL is still served")
	}
	again := New()
	again.CachePath = path
	again.Load()
	if _, ok := again.Cached("abc"); ok {
		t.Fatal("the forgotten URL came back from the file")
	}
}
