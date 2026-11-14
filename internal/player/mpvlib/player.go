package mpvlib

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/harrylyx/QuickBo/internal/player"
)

// Property observation ids, passed back in mpv_event.reply_userdata.
const (
	obsTimePos = iota + 1
	obsPausedForCache
	obsPause
	obsDuration
	obsCacheDuration
	obsEOFReached
)

// state is the latest playback state, written by the event loop and read by
// the UI's getters.
type state struct {
	loaded   bool
	paused   bool
	caching  bool
	ended    bool
	position float64
	posAt    time.Time
	duration float64
	cacheDur float64
	rate     float64
	muted    bool
	fullscr  bool
	err      error
}

// Player plays one stream at a time through libmpv, rendering video into
// frames the UI draws (player.VideoSource).
type Player struct {
	// hMu guards h and r: commands hold it for reading so Close cannot
	// destroy the handle under them.
	hMu sync.RWMutex
	h   unsafe.Pointer
	r   *renderer

	frames    *frameQueue
	eventDone chan struct{}
	closing   atomic.Bool

	stMu sync.Mutex
	st   state
}

var _ player.Player = (*Player)(nil)
var _ player.VideoSource = (*Player)(nil)
var _ player.Controls = (*Player)(nil)

// SetProp sets an mpv property from its string form.
func (p *Player) SetProp(name, value string) error { return p.setProp(name, value) }

// Prop reads an mpv property in its string form.
func (p *Player) Prop(name string) (string, error) {
	var out string
	err := p.withHandle(func(h unsafe.Pointer) error {
		ptr := mpvGetPropertyString(h, name)
		if ptr == nil {
			return errors.New("mpv 没有属性 " + name)
		}
		out = goString((*byte)(ptr))
		mpvFree(ptr)
		return nil
	})
	return out, err
}

// Command runs an mpv command, as screenshot-to-file or sub-add.
func (p *Player) Command(args ...string) error {
	if len(args) == 0 {
		return errors.New("mpv: 空命令")
	}
	return p.withHandle(func(h unsafe.Pointer) error { return command(h, args...) })
}

// New loads libmpv (once per process) and returns an idle player.
func New() (*Player, error) {
	if _, err := Load(); err != nil {
		return nil, err
	}
	return &Player{st: state{rate: 1}}, nil
}

// Load starts playing raw with the given HTTP headers. Stream errors that
// surface later (unreachable host, bad data) are reported through Err.
func (p *Player) Load(_ context.Context, raw string, headers map[string]string) error {
	_ = p.Close()
	h := mpvCreate()
	if h == nil {
		return errors.New("libmpv 初始化失败（mpv_create）")
	}
	fail := func(err error) error {
		mpvTerminateDestroy(h)
		return err
	}
	opts := [][2]string{
		{"vo", "libmpv"},
		// The software renderer needs frames in system memory: decode in
		// hardware where it is safe, then copy back.
		{"hwdec", "auto-copy-safe"},
		{"keep-open", "yes"},
		{"idle", "yes"},
		{"network-timeout", "15"},
		// A 超清 live stream stutters when the demuxer only keeps a second
		// ahead of the clock. Ten seconds of readahead rides out the bursts
		// without turning the stream into a recording.
		{"cache", "yes"},
		{"demuxer-readahead-secs", "10"},
		{"demuxer-max-bytes", "67108864"},
		{"demuxer-max-back-bytes", "8388608"},
		{"terminal", "no"},
		{"osc", "no"},
		{"input-default-bindings", "no"},
		{"input-vo-keyboard", "no"},
		{"audio-client-name", "QuickBo"},
	}
	for k, v := range headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		switch strings.ToLower(k) {
		case "user-agent":
			opts = append(opts, [2]string{"user-agent", v})
		case "referer", "referrer":
			opts = append(opts, [2]string{"referrer", v})
		}
	}
	for _, o := range opts {
		if err := mpvError("设置 mpv 选项 "+o[0], mpvSetOptionString(h, o[0], o[1])); err != nil {
			return fail(err)
		}
	}
	if err := mpvError("libmpv 初始化失败", mpvInitialize(h)); err != nil {
		return fail(err)
	}
	// Other headers go in one by one: a list option set from a string would
	// split a header value on its commas.
	for k, v := range headers {
		switch strings.ToLower(k) {
		case "user-agent", "referer", "referrer":
			continue
		}
		if strings.TrimSpace(v) == "" {
			continue
		}
		if err := command(h, "change-list", "http-header-fields", "append", k+": "+v); err != nil {
			return fail(err)
		}
	}
	for _, o := range []struct {
		id     uint64
		name   string
		format int32
	}{
		{obsTimePos, "time-pos", formatDouble},
		{obsPausedForCache, "paused-for-cache", formatFlag},
		{obsPause, "pause", formatFlag},
		{obsDuration, "duration", formatDouble},
		{obsCacheDuration, "demuxer-cache-duration", formatDouble},
		{obsEOFReached, "eof-reached", formatFlag},
	} {
		mpvObserveProperty(h, o.id, o.name, o.format)
	}

	frames := &frameQueue{}
	r, err := newRenderer(h, frames)
	if err != nil {
		return fail(err)
	}

	p.stMu.Lock()
	p.st = state{rate: 1}
	p.stMu.Unlock()
	p.closing.Store(false)
	done := make(chan struct{})

	p.hMu.Lock()
	p.h, p.r, p.frames, p.eventDone = h, r, frames, done
	p.hMu.Unlock()

	go p.eventLoop(h, done)

	if err := command(h, "loadfile", raw); err != nil {
		_ = p.Close()
		return fmt.Errorf("mpv 无法打开地址: %w", err)
	}
	return nil
}

func (p *Player) eventLoop(h unsafe.Pointer, done chan struct{}) {
	defer close(done)
	for {
		ev := mpvWaitEvent(h, -1)
		if p.closing.Load() {
			return
		}
		if ev == nil {
			continue
		}
		if ev.ID == eventNone {
			// No event was waiting. The timeout argument cannot be
			// passed through purego on Windows (it goes to an integer
			// register where the callee reads a float register), so the
			// timeout mpv actually sees is not ours and wait_event may
			// return immediately: pause so that cannot spin the loop.
			time.Sleep(2 * time.Millisecond)
			continue
		}
		switch ev.ID {
		case eventShutdown:
			return
		case eventFileLoaded:
			p.update(func(s *state) { s.loaded = true; s.ended = false; s.err = nil })
		case eventPlaybackRestart:
			// A seek finished: the position sample before it is stale.
			p.update(func(s *state) { s.posAt = time.Now() })
		case eventEndFile:
			ef := (*mpvEventEndFile)(ev.Data)
			if ef == nil {
				continue
			}
			switch ef.Reason {
			case endFileEOF:
				p.update(func(s *state) { s.ended = true })
			case endFileError:
				msg := mpvErrorString(ef.Error)
				p.update(func(s *state) { s.err = errors.New(msg); s.loaded = false })
			}
		case eventPropertyChange:
			p.property(ev.ReplyUserdata, (*mpvEventProperty)(ev.Data))
		}
	}
}

func (p *Player) property(id uint64, prop *mpvEventProperty) {
	if prop == nil {
		return
	}
	var f float64
	var flag, ok bool
	switch prop.Format {
	case formatDouble:
		f, ok = *(*float64)(prop.Data), prop.Data != nil
	case formatFlag:
		flag, ok = *(*int32)(prop.Data) != 0, prop.Data != nil
	}
	p.update(func(s *state) {
		switch id {
		case obsTimePos:
			if ok {
				s.position, s.posAt = f, time.Now()
			}
		case obsPausedForCache:
			s.caching = ok && flag
		case obsPause:
			s.paused = ok && flag
		case obsDuration:
			if ok {
				s.duration = f
			} else {
				s.duration = 0
			}
		case obsCacheDuration:
			if ok {
				s.cacheDur = f
			} else {
				s.cacheDur = 0
			}
		case obsEOFReached:
			// With keep-open, reaching the end pauses on the last frame
			// instead of ending the file.
			if ok && flag {
				s.ended = true
			} else if ok {
				s.ended = false
			}
		}
	})
}

func (p *Player) update(fn func(*state)) {
	p.stMu.Lock()
	fn(&p.st)
	p.stMu.Unlock()
}

func (p *Player) read() state {
	p.stMu.Lock()
	defer p.stMu.Unlock()
	return p.st
}

// withHandle runs fn with the live mpv handle, or fails when none is loaded.
func (p *Player) withHandle(fn func(h unsafe.Pointer) error) error {
	p.hMu.RLock()
	defer p.hMu.RUnlock()
	if p.h == nil {
		return errors.New("mpvlib: 尚未 Load")
	}
	return fn(p.h)
}

func (p *Player) setProp(name, value string) error {
	return p.withHandle(func(h unsafe.Pointer) error {
		return mpvError("设置 "+name, mpvSetPropertyString(h, name, value))
	})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (p *Player) Play() error {
	if p.read().ended {
		// Playing again after the end starts over, as other players do.
		_ = p.Seek(0)
	}
	return p.setProp("pause", "no")
}

func (p *Player) Pause() error { return p.setProp("pause", "yes") }

func (p *Player) Seek(d time.Duration) error {
	sec := strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
	err := p.withHandle(func(h unsafe.Pointer) error {
		return commandAsync(h, "seek", sec, "absolute")
	})
	if err == nil {
		p.update(func(s *state) { s.position, s.posAt, s.ended = d.Seconds(), time.Now(), false })
	}
	return err
}

func (p *Player) SetRate(r float64) error {
	if err := p.setProp("speed", strconv.FormatFloat(r, 'f', 3, 64)); err != nil {
		return err
	}
	p.update(func(s *state) {
		// Re-anchor the extrapolated position before the rate changes.
		if !s.posAt.IsZero() && playing(s) {
			s.position += time.Since(s.posAt).Seconds() * s.rate
			s.posAt = time.Now()
		}
		s.rate = r
	})
	return nil
}

func (p *Player) SetVolume(v float64) error {
	v = min(max(v, 0), 1)
	return p.setProp("volume", strconv.Itoa(int(v*100)))
}

func (p *Player) SetMuted(m bool) error {
	if err := p.setProp("mute", yesNo(m)); err != nil {
		return err
	}
	p.update(func(s *state) { s.muted = m })
	return nil
}

// SetFullscreen only records the wish: the video is part of the app window,
// whose fullscreen state the app controls.
func (p *Player) SetFullscreen(f bool) error {
	p.update(func(s *state) { s.fullscr = f })
	return nil
}

func (p *Player) Fullscreen() bool { return p.read().fullscr }
func (p *Player) Muted() bool      { return p.read().muted }
func (p *Player) Ready() bool      { return p.read().loaded }
func (p *Player) Ended() bool      { return p.read().ended }
func (p *Player) Err() error       { return p.read().err }

func playing(s *state) bool { return s.loaded && !s.paused && !s.caching && !s.ended }

func (p *Player) Playing() bool {
	s := p.read()
	return playing(&s)
}

func (p *Player) Buffering() bool {
	s := p.read()
	return s.loaded && s.caching && !s.paused
}

func (p *Player) Position() time.Duration {
	s := p.read()
	pos := s.position
	// time-pos changes are coalesced; extrapolate between samples so the
	// progress bar moves smoothly.
	if playing(&s) && s.rate > 0 && !s.posAt.IsZero() {
		pos += min(time.Since(s.posAt).Seconds(), 1) * s.rate
	}
	if s.duration > 0 && pos > s.duration {
		pos = s.duration
	}
	return time.Duration(pos * float64(time.Second))
}

func (p *Player) Duration() time.Duration {
	return time.Duration(p.read().duration * float64(time.Second))
}

func (p *Player) Buffered() time.Duration {
	return time.Duration(p.read().cacheDur * float64(time.Second))
}

// SetVideoSize tells the renderer the size, in device pixels, the video is
// shown at. Frames are rendered at that size.
func (p *Player) SetVideoSize(w, h int) {
	p.hMu.RLock()
	r := p.r
	p.hMu.RUnlock()
	if r != nil {
		r.setSize(w, h)
	}
}

// TakeFrame returns the newest rendered frame not yet taken, or nil. The
// frame's pixels stay untouched until the next frame is taken.
func (p *Player) TakeFrame() *image.RGBA {
	p.hMu.RLock()
	q := p.frames
	p.hMu.RUnlock()
	if q == nil {
		return nil
	}
	return q.take()
}

// Close stops playback and releases libmpv. The handle is destroyed in the
// background, as tearing down a network stream can take a moment.
func (p *Player) Close() error {
	p.hMu.Lock()
	h, r, done := p.h, p.r, p.eventDone
	p.h, p.r, p.eventDone = nil, nil, nil
	p.hMu.Unlock()
	if h == nil {
		return nil
	}
	// The render context must go before the handle.
	r.close()
	p.closing.Store(true)
	mpvWakeup(h)
	<-done
	go mpvTerminateDestroy(h)
	p.update(func(s *state) { s.loaded = false })
	return nil
}
