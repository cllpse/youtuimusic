// Package player drives audio playback by talking to mpv over its JSON IPC
// socket.
//
// It deliberately does not link libmpv. The IPC protocol exposes everything we
// need — commands, property reads and property observation — over a unix
// socket, which keeps this package cgo-free and removes the locale workaround
// libmpv needs (it segfaults unless LC_NUMERIC is "C").
package player

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/cllpse/youtuimusic/internal/tool"
)

// Event is something mpv pushed: either an observed property changing, in
// which case Name is the property, or one of mpv's own events, in which case
// Name is the event. Playback state is observed rather than polled, so
// nothing has to run on a timer.
type Event struct {
	Name string
	Data any
}

// EndFile is mpv's end-file event. Data is the reason mpv gives — "eof" when
// the track ran to its end, "stop" when something replaced it.
//
// This is the only dependable signal that a track finished. The eof-reached
// property is not: mpv unloads the file at the same moment, so the property
// goes unavailable rather than true, and an observer watching for true never
// hears anything.
const EndFile = "end-file"

// The properties this package observes. They are exported because the UI
// matches on them by name: with the names in one place, a typo is a compile
// error rather than an event that silently never matches.
const (
	PropTimePos  = "time-pos"
	PropDuration = "duration"
	PropPause    = "pause"
	PropVolume   = "volume"
)

// Player is a running mpv process and the connection to it. It is safe for
// concurrent use.
type Player struct {
	cmd    *exec.Cmd
	conn   net.Conn
	socket string

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan response

	// events is the single stream the consumer reads. readLoop feeds incoming
	// (control changes) and positions (playback position) to pump, which is the
	// only writer of events and the only thing that closes it.
	//
	// The split exists because the two kinds of event need opposite treatment:
	// a dropped end-file or pause desyncs the app, while a dropped time-pos is
	// nothing — the latest one is already the truth. So control changes are
	// never dropped and positions are coalesced to the newest.
	events    chan Event
	incoming  chan Event
	positions chan float64

	closeOnce sync.Once
	closed    chan struct{}
}

type response struct {
	Err  string          `json:"error"`
	Data json.RawMessage `json:"data"`
	ID   int64           `json:"request_id"`
}

// observed properties are pushed to Events() as they change.
var observed = []string{PropTimePos, PropDuration, PropPause, PropVolume}

// New starts an mpv process and connects to its IPC socket.
func New(ctx context.Context) (*Player, error) {
	dir, err := os.MkdirTemp("", "youtuimusic")
	if err != nil {
		return nil, fmt.Errorf("ipc socket dir: %w", err)
	}
	socket := filepath.Join(dir, "mpv.sock")

	cmd := exec.CommandContext(ctx, tool.Path("mpv"),
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--input-ipc-server="+socket,
		// Audio-only playback of a remote stream: keep a healthy buffer so a
		// slow segment doesn't audibly stall.
		"--cache=yes",
		"--demuxer-max-bytes=64MiB",
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mpv (is it installed?): %w", err)
	}

	conn, err := dialSocket(ctx, socket)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}

	p := &Player{
		cmd:       cmd,
		conn:      conn,
		socket:    socket,
		pending:   make(map[int64]chan response),
		events:    make(chan Event, 64),
		incoming:  make(chan Event, 256),
		positions: make(chan float64, 1),
		closed:    make(chan struct{}),
	}
	go p.pump()
	go p.readLoop()

	for i, name := range observed {
		if _, err := p.command("observe_property", i+1, name); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("observe %s: %w", name, err)
		}
	}
	return p, nil
}

// dialSocket waits for mpv to create its socket, which it does asynchronously
// after start.
func dialSocket(ctx context.Context, socket string) (net.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("mpv ipc socket never appeared at %s: %w", socket, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// readLoop demultiplexes the single socket: replies go to whoever is waiting on
// that request id, property changes to pump. It never writes to events itself,
// so pump is the only writer and the only closer of that channel.
func (p *Player) readLoop() {
	scanner := bufio.NewScanner(p.conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Event string          `json:"event"`
			Name  string          `json:"name"`
			Data  json.RawMessage `json:"data"`
			ID    int64           `json:"request_id"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}

		if probe.Event != "" {
			switch probe.Event {
			case "property-change":
				var v any
				_ = json.Unmarshal(probe.Data, &v)
				p.publish(Event{Name: probe.Name, Data: v})
			case EndFile:
				var end struct {
					Reason string `json:"reason"`
				}
				_ = json.Unmarshal(line, &end)
				p.publish(Event{Name: EndFile, Data: end.Reason})
			}
			continue
		}

		var resp response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}
		p.mu.Lock()
		ch, ok := p.pending[resp.ID]
		delete(p.pending, resp.ID)
		p.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

// pump turns the two internal streams into the one Events() stream. Control
// changes are passed straight through; playback positions are collapsed to
// whichever is newest while the consumer is busy. It is the only writer of
// events, and closes it on shutdown so a reader sees the stream end.
func (p *Player) pump() {
	defer close(p.events)
	for {
		select {
		case <-p.closed:
			return
		case ev := <-p.incoming:
			if !p.emit(ev) {
				return
			}
		case f := <-p.positions:
			if !p.emit(Event{Name: PropTimePos, Data: f}) {
				return
			}
		}
	}
}

// emit writes one event, reporting false once the player is shutting down.
func (p *Player) emit(ev Event) bool {
	select {
	case p.events <- ev:
		return true
	case <-p.closed:
		return false
	}
}

// publish routes an event from the socket. A position goes to the latest-wins
// slot; everything else goes to the control stream, where it is never dropped.
// Blocking on the control stream is deliberate and safe: the consumer always
// drains it, and a pause or end-file matters more than a stalled event bus.
func (p *Player) publish(ev Event) {
	if ev.Name == PropTimePos {
		p.queuePosition(ev.Data)
		return
	}
	select {
	case p.incoming <- ev:
	case <-p.closed:
	}
}

// queuePosition keeps only the newest position. The slot holds one value; when
// it is full the stale one is dropped so the fresh one can take its place.
func (p *Player) queuePosition(data any) {
	f, ok := data.(float64)
	if !ok {
		return
	}
	select {
	case p.positions <- f:
		return
	default:
	}
	select {
	case <-p.positions:
	default:
	}
	select {
	case p.positions <- f:
	default:
	}
}

// command sends one IPC command and waits for its reply.
func (p *Player) command(args ...any) (json.RawMessage, error) {
	select {
	case <-p.closed:
		return nil, errors.New("player is closed")
	default:
	}

	p.mu.Lock()
	p.nextID++
	id := p.nextID
	ch := make(chan response, 1)
	p.pending[id] = ch
	p.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"command": args, "request_id": id})
	if err != nil {
		return nil, err
	}
	if _, err := p.conn.Write(append(payload, '\n')); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("write to mpv: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Err != "success" {
			if resp.Err == "property unavailable" {
				return nil, ErrUnavailable
			}
			return nil, fmt.Errorf("mpv: %s", resp.Err)
		}
		return resp.Data, nil
	case <-time.After(5 * time.Second):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, errors.New("mpv did not reply within 5s")
	}
}

// Events returns the channel of observed property changes. It is closed when
// the player shuts down.
func (p *Player) Events() <-chan Event { return p.events }

// Load starts playing a stream URL, replacing whatever is playing.
func (p *Player) Load(url string) error {
	_, err := p.command("loadfile", url, "replace")
	return err
}

// Stop clears the current file, returning mpv to idle.
func (p *Player) Stop() error {
	_, err := p.command("stop")
	return err
}

// TogglePause flips between playing and paused.
func (p *Player) TogglePause() error {
	paused, err := p.Paused()
	if err != nil {
		return err
	}
	return p.SetPaused(!paused)
}

// SetPaused pauses or resumes playback.
func (p *Player) SetPaused(paused bool) error {
	_, err := p.command("set_property", PropPause, paused)
	return err
}

// Paused reports whether playback is paused.
func (p *Player) Paused() (bool, error) {
	raw, err := p.command("get_property", PropPause)
	if err != nil {
		return false, err
	}
	var v bool
	return v, json.Unmarshal(raw, &v)
}

// Seek jumps to an absolute position in seconds.
func (p *Player) Seek(seconds float64) error {
	_, err := p.command("seek", seconds, "absolute")
	return err
}

// Position returns the playback position in seconds. Callers should normally
// prefer the time-pos events instead of polling this.
func (p *Player) Position() (float64, error) { return p.floatProperty(PropTimePos) }

// Duration returns the length of the current track in seconds.
func (p *Player) Duration() (float64, error) { return p.floatProperty(PropDuration) }

// ErrUnavailable is mpv's way of saying a property has no value yet — with
// nothing loaded, time-pos and duration are unavailable rather than zero.
var ErrUnavailable = errors.New("mpv: property unavailable")

func (p *Player) floatProperty(name string) (float64, error) {
	raw, err := p.command("get_property", name)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			// Idle is a normal state for a player, not a failure.
			return 0, nil
		}
		return 0, err
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, nil
	}
	return v, nil
}

// SetVolume sets the volume as a percentage (0-100).
func (p *Player) SetVolume(percent int) error {
	_, err := p.command("set_property", PropVolume, percent)
	return err
}

// Volume returns the current volume percentage.
func (p *Player) Volume() (int, error) {
	v, err := p.floatProperty(PropVolume)
	return int(v), err
}

// Close shuts down the connection and the mpv process.
func (p *Player) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.closed)
		if p.conn != nil {
			_ = p.conn.Close()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
			_, _ = p.cmd.Process.Wait()
		}
		if dir := filepath.Dir(p.socket); dir != "" && dir != "/" {
			err = os.RemoveAll(dir)
		}
	})
	return err
}
