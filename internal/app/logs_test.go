package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/harrylyx/QuickBo/internal/source"
)

func TestLogPathBesideSettings(t *testing.T) {
	a := New()
	a.settingsPath = ""
	if a.logPath() != "" {
		t.Fatalf("no settingsPath must mean no log path, got %q", a.logPath())
	}
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	if want := filepath.Join(dir, "quickbo.log"); a.logPath() != want {
		t.Fatalf("log path %q, want %q", a.logPath(), want)
	}
}

func TestSettingsOpensLogDir(t *testing.T) {
	a := New()
	dir := t.TempDir()
	a.settingsPath = filepath.Join(dir, "settings.json")
	a.catalog = &source.Catalog{}
	a.screen, a.sideSel, a.lastSide = screenHome, "home", "home"
	a.openSettings()
	got := ""
	a.revealDir = func(d string) { got = d }

	tt := ui.NewTester(a.View, 1200, 900)
	if !tt.HasText("运行日志") || !tt.HasText("打开") {
		t.Fatalf("settings texts %q", tt.Texts())
	}
	if err := tt.Click("打开"); err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("opened %q, want the settings directory %q", got, dir)
	}
}

func TestSettingsSectionsFitShortWindow(t *testing.T) {
	checks, downloads, checked := true, false, 0
	defer fakeUpdater(&checks, &downloads, &checked, time.Time{})()

	a := New()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	a.catalog = &source.Catalog{}
	a.screen, a.sideSel, a.lastSide = screenHome, "home", "home"
	a.updates = true
	a.openSettings()

	tt := ui.NewTester(a.View, 960, 640)
	for _, label := range []string{"设置", "外观", "更新", "存储", "运行日志", "完成"} {
		if !tt.HasText(label) {
			t.Fatalf("settings is missing %q: %q", label, tt.Texts())
		}
	}
	if err := tt.Click("完成"); err != nil {
		t.Fatal(err)
	}
	if a.settingsOpen {
		t.Fatal("完成 did not close the settings")
	}
}
