// Command quickbo is a desktop TVBox-style client with a native GPU UI.
package main

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/harrylyx/QuickBo/internal/app"
)

func main() {
	ref := flag.String("config", "", "配置 URL 或本地 JSON 路径")
	flag.Parse()
	if *ref == "" && flag.NArg() > 0 {
		*ref = flag.Arg(0)
	}
	setupCrashLog()
	application := app.New()
	application.UseUpdater()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "QuickBo",
			Width:     1440,
			Height:    900,
			MinWidth:  960,
			MinHeight: 640,
			// No sidebar Vibrancy: the system material follows the system's
			// appearance, not the app's own light/dark switch, so a dark
			// app on a light system got a light sidebar.
			BackgroundColor: "light-dark(#ffffff, #0f0f12)",
			// The native title bar follows the system's appearance, not the
			// app's own light/dark switch, so a dark app kept a white bar.
			// Hiding it lets the app draw its own — the view drags the
			// window and keeps clear of the controls the desktop leaves
			// over the page (ui.Context.TitleBar).
			TitleBarStyle: mygo.TitleBarHiddenInset,
			Content:       ui.View(application.View),
		})
		application.Attach(win)
		application.RestoreSettings()
		if *ref != "" {
			application.AddSubscription(*ref)
		} else {
			application.LoadSaved()
		}
	})
	defer func() {
		// A crash launched from the desktop closes the console with the
		// app: keep the panic and its stack in quickbo.log so it can be
		// read afterwards instead of flashing away.
		if r := recover(); r != nil {
			log.Printf("panic: %v\n%s", r, debug.Stack())
			panic(r)
		}
	}()
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// setupCrashLog sends log output to quickbo.log beside the settings, in
// addition to the console. Errors are ignored on purpose: a broken log must
// never keep the app from starting.
func setupCrashLog() {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	dir = filepath.Join(dir, "quickbo")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "quickbo.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("QuickBo 启动（%s %s）", runtime.GOOS, runtime.Version())
}
