// Package playback resolves media addresses and drives the mpv player. It is
// the seam between the UI (internal/app) and the player kernels
// (internal/player/mpvlib, and internal/player/mpvproc as the fallback).
package playback

import (
	"context"
	"log"
	"os"

	"github.com/harrylyx/QuickBo/internal/player"
	"github.com/harrylyx/QuickBo/internal/player/mpvlib"
	"github.com/harrylyx/QuickBo/internal/player/mpvplugin"
	"github.com/harrylyx/QuickBo/internal/player/mpvproc"
)

// Controller owns kernel selection and player construction.
type Controller struct {
	mpvPath string
	// libErr is why libmpv could not be used; playback then falls back to
	// an mpv process in its own window.
	libErr error
}

// New returns a controller. libmpv (or mpv) is located on first Play.
// QUICKBO_PLAYER=window forces the mpv-process kernel.
func New() *Controller {
	c := &Controller{}
	if os.Getenv("QUICKBO_PLAYER") == "window" {
		c.libErr = errForcedWindow
	}
	return c
}

type forcedWindow struct{}

func (forcedWindow) Error() string { return "QUICKBO_PLAYER=window" }

var errForcedWindow error = forcedWindow{}

// Play starts playback of raw. It prefers libmpv, which renders inside the
// app window (the player implements player.VideoSource); without libmpv it
// starts an mpv process, whose video shows in a window of its own.
func (c *Controller) Play(ctx context.Context, raw string, headers map[string]string) (player.Player, error) {
	if c.libErr == nil {
		p, err := mpvlib.New()
		if err == nil {
			if err := p.Load(ctx, raw, headers); err != nil {
				_ = p.Close()
				log.Printf("playback: libmpv 打开失败: %v", err)
				return nil, err
			}
			return p, nil
		}
		c.libErr = err
		log.Printf("playback: 回退到独立 mpv 窗口: %v", err)
	}
	exe, err := c.mpv()
	if err != nil {
		log.Printf("playback: 找不到 mpv: %v", err)
		return nil, err
	}
	p, err := mpvproc.New(exe)
	if err != nil {
		return nil, err
	}
	if err := p.Load(ctx, raw, headers); err != nil {
		_ = p.Close()
		log.Printf("playback: mpv 子进程打开失败: %v", err)
		return nil, err
	}
	return p, nil
}

// FallbackNote explains, once playback has fallen back to an mpv window, why
// the video is not inside the app. It is empty while libmpv is in use.
func (c *Controller) FallbackNote() string {
	if c.libErr == nil || c.libErr == errForcedWindow {
		return ""
	}
	return "未能加载 libmpv，改用独立 mpv 窗口播放：" + c.libErr.Error()
}

func (c *Controller) mpv() (string, error) {
	if c.mpvPath != "" {
		return c.mpvPath, nil
	}
	path, err := mpvplugin.Find()
	if err != nil {
		return "", err
	}
	if _, err := mpvplugin.Check(path); err != nil {
		return "", err
	}
	c.mpvPath = path
	return path, nil
}
