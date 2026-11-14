package app

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// logPath is where the crash log lives: beside settings.json, in the same
// directory cmd/quickbo writes quickbo.log. An empty settingsPath means
// there is nowhere to keep either file (tests).
func (a *App) logPath() string {
	if a.settingsPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.settingsPath), "quickbo.log")
}

// openLogDir reveals the settings directory in the desktop's file manager,
// so the user can reach quickbo.log when reporting a crash. The command
// runs detached: a missing file manager must never stall the UI thread.
func (a *App) openLogDir() {
	if a.settingsPath == "" {
		return
	}
	dir := filepath.Dir(a.settingsPath)
	if a.revealDir != nil {
		a.revealDir(dir)
		return
	}
	go revealDir(dir)
}

// revealDir opens a directory with the platform's file manager. The three
// commands are all GUI processes, so none flashes a console on Windows.
// A runtime switch keeps the platform difference in one function: no build
// tags, no cgo, and libmpv stays the only native library in the app.
func revealDir(dir string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	// Run waits in the detached goroutine, so the child is reaped. All
	// three commands hand the path to the desktop and return quickly.
	_ = cmd.Run()
}
