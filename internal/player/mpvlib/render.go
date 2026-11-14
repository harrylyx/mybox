package mpvlib

import (
	"image"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// frameAlign is the alignment libmpv's software renderer asks for, for both
// the pixel pointer and the stride, to stay on its fast SIMD paths.
const frameAlign = 64

// maxRenderSide caps the render target. Past this the CPU renderer cannot
// keep up anyway, and the GPU scales the picture the rest of the way.
const maxRenderSide = 3840

// frameQueue hands rendered frames from the render thread to the UI thread.
// The UI keeps showing the frame it took until it takes the next one
// (Bitmap.Update keeps the pixels without copying), so a buffer only returns
// to the free list once a newer frame has replaced it on screen.
type frameQueue struct {
	mu      sync.Mutex
	pending *image.RGBA
	shown   *image.RGBA
	free    []*image.RGBA
	count   uint64
}

// get returns a buffer of w×h, reusing a free one of that size.
func (q *frameQueue) get(w, h int) *image.RGBA {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.free) > 0 {
		img := q.free[len(q.free)-1]
		q.free = q.free[:len(q.free)-1]
		if b := img.Rect; b.Dx() == w && b.Dy() == h {
			return img
		}
	}
	return newFrame(w, h)
}

// put publishes img as the newest frame, recycling a frame the UI never took.
func (q *frameQueue) put(img *image.RGBA) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending != nil {
		q.free = append(q.free, q.pending)
	}
	q.pending = img
	q.count++
}

// take returns the newest frame not yet taken, or nil.
func (q *frameQueue) take() *image.RGBA {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending == nil {
		return nil
	}
	if q.shown != nil {
		q.free = append(q.free, q.shown)
	}
	q.shown, q.pending = q.pending, nil
	return q.shown
}

// newFrame allocates an RGBA image whose pixels start on a frameAlign
// boundary. w is a multiple of 16, so the stride 4w is one too.
func newFrame(w, h int) *image.RGBA {
	stride := 4 * w
	buf := make([]byte, stride*h+frameAlign)
	off := 0
	if r := int(uintptr(unsafe.Pointer(&buf[0])) % frameAlign); r != 0 {
		off = frameAlign - r
	}
	return &image.RGBA{Pix: buf[off : off+stride*h : off+stride*h], Stride: stride, Rect: image.Rect(0, 0, w, h)}
}

// renderTarget is the argument block of one mpv_render_context_render call.
// It lives on the heap and is pinned for the call, as C reads it.
type renderTarget struct {
	size   [2]int32
	format [5]byte // "rgb0" NUL-terminated
	stride uintptr // size_t
	params [5]renderParam
}

func newRenderTarget() *renderTarget {
	t := &renderTarget{}
	copy(t.format[:], "rgb0")
	return t
}

// render draws the current video frame into img.
func (t *renderTarget) render(ctx unsafe.Pointer, img *image.RGBA) int32 {
	var pin runtime.Pinner
	defer pin.Unpin()
	pix := &img.Pix[0]
	pin.Pin(t)
	pin.Pin(pix)
	t.size = [2]int32{int32(img.Rect.Dx()), int32(img.Rect.Dy())}
	t.stride = uintptr(img.Stride)
	t.params = [5]renderParam{
		{Type: renderParamSWSize, Data: unsafe.Pointer(&t.size[0])},
		{Type: renderParamSWFormat, Data: unsafe.Pointer(&t.format[0])},
		{Type: renderParamSWStride, Data: unsafe.Pointer(&t.stride)},
		{Type: renderParamSWPointer, Data: unsafe.Pointer(pix)},
		{Type: renderParamInvalid},
	}
	rc := mpvRenderRender(ctx, &t.params[0])
	if rc >= 0 {
		opaque(img.Pix)
	}
	return rc
}

// opaque sets every pixel's fourth byte to 255. "rgb0" leaves it undefined,
// and image.RGBA reads it as alpha. A 4K frame is eight million pixels, so
// this walks words instead of bytes; the CPU renderer was missing frames on
// 超清 live while it filled alpha one byte at a time.
func opaque(pix []byte) {
	const alpha = uint32(0xFF) << 24 // little-endian RGBA: A is the high byte
	i := 0
	for i < len(pix) && uintptr(unsafe.Pointer(&pix[i]))%4 != 0 {
		if i%4 == 3 {
			pix[i] = 0xff
		}
		i++
	}
	n := (len(pix) - i) / 4
	if n > 0 {
		words := unsafe.Slice((*uint32)(unsafe.Pointer(&pix[i])), n)
		for k := range words {
			words[k] |= alpha
		}
		i += n * 4
	}
	for ; i < len(pix); i++ {
		if i%4 == 3 {
			pix[i] = 0xff
		}
	}
}

// fitRenderSize clamps a requested target size to what the renderer accepts:
// at least 16×16, no side over maxRenderSide (keeping the aspect), and a
// width that is a multiple of 16 so rows stay 64-byte aligned.
func fitRenderSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if m := max(w, h); m > maxRenderSide {
		w = w * maxRenderSide / m
		h = h * maxRenderSide / m
	}
	w = max(16, w&^15)
	h = max(16, h)
	return w, h
}

// Update callbacks: libmpv calls the one C function pointer, from its own
// threads, with the id of the session as its argument. purego callbacks are
// a limited resource, so there is exactly one for the process.
var (
	updateCBOnce sync.Once
	updateCB     uintptr
	updateSinks  sync.Map // uintptr id -> chan struct{}
	nextSinkID   atomic.Uintptr
)

func updateCallback() uintptr {
	updateCBOnce.Do(func() {
		// The callback must return one uintptr-sized result: purego on
		// Windows routes NewCallback through syscall.NewCallback, which
		// panics at registration over a void function. Returning 0 is
		// harmless everywhere: the C side declares the callback void and
		// ignores the return register.
		updateCB = purego.NewCallback(func(id uintptr) uintptr {
			if ch, ok := updateSinks.Load(id); ok {
				select {
				case ch.(chan struct{}) <- struct{}{}:
				default:
				}
			}
			return 0
		})
	})
	return updateCB
}

// renderer owns one mpv_render_context and the goroutine that drives it.
type renderer struct {
	ctx    unsafe.Pointer
	id     uintptr
	wake   chan struct{}
	stop   chan struct{}
	done   chan struct{}
	frames *frameQueue

	size atomic.Uint64 // requested w<<32 | h
}

func newRenderer(h unsafe.Pointer, frames *frameQueue) (*renderer, error) {
	apiType := []byte("sw\x00")
	params := []renderParam{
		{Type: renderParamAPIType, Data: unsafe.Pointer(&apiType[0])},
		{Type: renderParamInvalid},
	}
	var pin runtime.Pinner
	pin.Pin(&apiType[0])
	pin.Pin(&params[0])
	var ctx unsafe.Pointer
	rc := mpvRenderCreate(&ctx, h, &params[0])
	pin.Unpin()
	if err := mpvError("创建 libmpv 渲染上下文失败", rc); err != nil {
		return nil, err
	}
	r := &renderer{
		ctx:    ctx,
		id:     nextSinkID.Add(1),
		wake:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		frames: frames,
	}
	updateSinks.Store(r.id, r.wake)
	mpvRenderSetUpdateCB(ctx, updateCallback(), r.id)
	go r.loop()
	return r, nil
}

// setSize asks for frames of w×h device pixels.
func (r *renderer) setSize(w, h int) {
	w, h = fitRenderSize(w, h)
	v := uint64(w)<<32 | uint64(h)
	if r.size.Swap(v) != v {
		r.poke()
	}
}

func (r *renderer) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *renderer) loop() {
	defer close(r.done)
	// The render API wants one thread for all render calls of a context.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	target := newRenderTarget()
	var lastSize uint64
	for {
		select {
		case <-r.stop:
			return
		case <-r.wake:
		}
		flags := mpvRenderUpdate(r.ctx)
		size := r.size.Load()
		w, h := int(size>>32), int(size&0xffffffff)
		if w == 0 || h == 0 {
			continue // the UI has not laid the video out yet
		}
		if flags&renderUpdateFrame == 0 && size == lastSize {
			continue
		}
		img := r.frames.get(w, h)
		if target.render(r.ctx, img) < 0 {
			continue
		}
		mpvRenderReportSwap(r.ctx)
		lastSize = size
		r.frames.put(img)
	}
}

// close stops the render goroutine and frees the context. It must run before
// the mpv handle is destroyed.
func (r *renderer) close() {
	close(r.stop)
	<-r.done
	mpvRenderSetUpdateCB(r.ctx, 0, 0)
	mpvRenderFree(r.ctx)
	updateSinks.Delete(r.id)
}
