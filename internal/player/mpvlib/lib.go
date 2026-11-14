// Package mpvlib implements player.Player on libmpv, loaded at run time with
// purego so the build stays CGO_ENABLED=0. Video is drawn by libmpv's
// software renderer into RGBA buffers the UI shows as a bitmap, so playback
// happens inside the QuickBo window instead of mpv's own.
package mpvlib

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// libmpv's C API, bound once by load. Signatures follow mpv/client.h and
// mpv/render.h (client API 2.x).
var (
	mpvClientAPIVersion  func() uint64
	mpvErrorString       func(code int32) string
	mpvCreate            func() unsafe.Pointer
	mpvInitialize        func(h unsafe.Pointer) int32
	mpvTerminateDestroy  func(h unsafe.Pointer)
	mpvSetOptionString   func(h unsafe.Pointer, name, value string) int32
	mpvSetPropertyString func(h unsafe.Pointer, name, value string) int32
	mpvGetPropertyString func(h unsafe.Pointer, name string) unsafe.Pointer
	mpvFree              func(p unsafe.Pointer)
	mpvCommand           func(h unsafe.Pointer, args unsafe.Pointer) int32
	mpvCommandAsync      func(h unsafe.Pointer, userdata uint64, args unsafe.Pointer) int32
	mpvObserveProperty   func(h unsafe.Pointer, userdata uint64, name string, format int32) int32
	mpvWaitEvent         func(h unsafe.Pointer, timeout float64) *mpvEvent
	mpvWakeup            func(h unsafe.Pointer)
	mpvRenderCreate      func(res *unsafe.Pointer, h unsafe.Pointer, params *renderParam) int32
	mpvRenderSetUpdateCB func(ctx unsafe.Pointer, cb uintptr, cbctx uintptr)
	mpvRenderUpdate      func(ctx unsafe.Pointer) uint64
	mpvRenderRender      func(ctx unsafe.Pointer, params *renderParam) int32
	mpvRenderReportSwap  func(ctx unsafe.Pointer)
	mpvRenderFree        func(ctx unsafe.Pointer)
)

// Constants from mpv/client.h and mpv/render.h.
const (
	formatFlag   = 3
	formatDouble = 5

	eventNone            = 0
	eventShutdown        = 1
	eventEndFile         = 7
	eventFileLoaded      = 8
	eventPlaybackRestart = 21
	eventPropertyChange  = 22

	endFileEOF   = 0
	endFileError = 4

	renderParamInvalid   = 0
	renderParamAPIType   = 1
	renderParamSWSize    = 17
	renderParamSWFormat  = 18
	renderParamSWStride  = 19
	renderParamSWPointer = 20

	renderUpdateFrame = 1
)

// mpvEvent mirrors struct mpv_event.
type mpvEvent struct {
	ID            int32
	Error         int32
	ReplyUserdata uint64
	Data          unsafe.Pointer
}

// mpvEventProperty mirrors struct mpv_event_property.
type mpvEventProperty struct {
	Name   *byte
	Format int32
	_      int32
	Data   unsafe.Pointer
}

// mpvEventEndFile mirrors the leading fields of struct mpv_event_end_file.
type mpvEventEndFile struct {
	Reason int32
	Error  int32
}

// renderParam mirrors struct mpv_render_param.
type renderParam struct {
	Type int32
	_    int32
	Data unsafe.Pointer
}

var (
	loadOnce sync.Once
	loadPath string
	loadErr  error
)

// Load locates and binds libmpv once. It returns the path of the library in
// use, or an error carrying an install hint.
func Load() (string, error) {
	loadOnce.Do(func() { loadPath, loadErr = load() })
	return loadPath, loadErr
}

func load() (string, error) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return "", errors.New("内嵌播放只支持 64 位系统")
	}
	var tried []string
	var lib uintptr
	var path string
	for _, p := range candidates() {
		h, err := openLib(p)
		if err == nil {
			lib, path = h, p
			break
		}
		tried = append(tried, p)
	}
	if lib == 0 {
		return "", fmt.Errorf("未找到 libmpv（已尝试 %s）。%s", strings.Join(tried, "、"), InstallHint())
	}
	bind := func(fptr any, name string) error {
		addr, err := symLib(lib, name)
		if err != nil {
			return fmt.Errorf("libmpv 缺少符号 %s: %w", name, err)
		}
		purego.RegisterFunc(fptr, addr)
		return nil
	}
	for _, b := range []struct {
		fptr any
		name string
	}{
		{&mpvClientAPIVersion, "mpv_client_api_version"},
		{&mpvErrorString, "mpv_error_string"},
		{&mpvCreate, "mpv_create"},
		{&mpvInitialize, "mpv_initialize"},
		{&mpvTerminateDestroy, "mpv_terminate_destroy"},
		{&mpvSetOptionString, "mpv_set_option_string"},
		{&mpvSetPropertyString, "mpv_set_property_string"},
		{&mpvGetPropertyString, "mpv_get_property_string"},
		{&mpvFree, "mpv_free"},
		{&mpvCommand, "mpv_command"},
		{&mpvCommandAsync, "mpv_command_async"},
		{&mpvObserveProperty, "mpv_observe_property"},
		{&mpvWaitEvent, "mpv_wait_event"},
		{&mpvWakeup, "mpv_wakeup"},
		{&mpvRenderCreate, "mpv_render_context_create"},
		{&mpvRenderSetUpdateCB, "mpv_render_context_set_update_callback"},
		{&mpvRenderUpdate, "mpv_render_context_update"},
		{&mpvRenderRender, "mpv_render_context_render"},
		{&mpvRenderReportSwap, "mpv_render_context_report_swap"},
		{&mpvRenderFree, "mpv_render_context_free"},
	} {
		if err := bind(b.fptr, b.name); err != nil {
			return "", err
		}
	}
	// The render API and the fields used here need client API 2.x.
	if v := mpvClientAPIVersion(); v>>16 < 2 {
		return "", fmt.Errorf("libmpv 版本过旧（client API %d.%d，需要 2.0 以上）。%s", v>>16, v&0xffff, InstallHint())
	}
	return path, nil
}

// mpvError turns a negative libmpv status into an error.
func mpvError(op string, code int32) error {
	if code >= 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", op, mpvErrorString(code))
}

// cstrings holds NUL-terminated copies of Go strings plus a NULL-terminated
// array of pointers to them, the const char** that mpv_command takes. The
// pinner keeps everything in place while C reads it.
type cstrings struct {
	bufs [][]byte
	ptrs []*byte
	pin  runtime.Pinner
}

func newCStrings(args []string) *cstrings {
	cs := &cstrings{bufs: make([][]byte, len(args)), ptrs: make([]*byte, len(args)+1)}
	for i, a := range args {
		b := make([]byte, len(a)+1)
		copy(b, a)
		cs.bufs[i] = b
		cs.ptrs[i] = &b[0]
		cs.pin.Pin(&b[0])
	}
	cs.pin.Pin(&cs.ptrs[0])
	return cs
}

func (cs *cstrings) ptr() unsafe.Pointer { return unsafe.Pointer(&cs.ptrs[0]) }
func (cs *cstrings) free()               { cs.pin.Unpin() }

// command runs mpv_command synchronously.
func command(h unsafe.Pointer, args ...string) error {
	cs := newCStrings(args)
	defer cs.free()
	return mpvError(args[0], mpvCommand(h, cs.ptr()))
}

// commandAsync runs mpv_command_async; mpv copies the arguments before it
// returns, so they need not outlive the call.
func commandAsync(h unsafe.Pointer, args ...string) error {
	cs := newCStrings(args)
	defer cs.free()
	return mpvError(args[0], mpvCommandAsync(h, 0, cs.ptr()))
}

// goString copies a NUL-terminated C string.
func goString(p *byte) string {
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}
