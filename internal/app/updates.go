package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// The update plugin's check and preferences. Tests replace them: the real
// functions panic until UseUpdater has mounted the plugin.
var (
	checkForUpdates    = updater.CheckForUpdates
	updateChecks       = updater.AutomaticChecks
	setUpdateChecks    = updater.SetAutomaticChecks
	updateDownloads    = updater.AutomaticDownloads
	setUpdateDownloads = updater.SetAutomaticDownloads
	lastUpdateCheck    = updater.LastCheck
)

// UseUpdater checks GitHub releases for a new version and offers it in a
// native update window. It must run before App.Run. The window follows the
// app's Chinese UI.
func (a *App) UseUpdater() {
	mygo.Use(native.New(updater.Options{Language: "zh-Hans"}))
	a.updates = true
	// A check or an answer in the update window changes what settings show.
	updater.OnChange(func() {
		if a.win != nil {
			a.win.Invalidate()
		}
	})
}

func (a *App) viewUpdateSettings(c *ui.Context, th *ui.Theme) {
	if !a.updates {
		return
	}
	settingsCard(c, th, "settings-updates", "更新", func() {
		a.updateToggle(c, th, "update-checks", "自动检查更新", updateCheckedText(lastUpdateCheck()), updateChecks, setUpdateChecks, false)
		a.updateToggle(c, th, "update-downloads", "下载后下次启动安装", "下载新版本，下次打开应用时安装", updateDownloads, setUpdateDownloads, !updateChecks())

		label := "当前版本"
		if ver := strings.TrimSpace(mygo.App.Version()); ver != "" {
			label = "当前版本 " + ver
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(fontBody).Grow(1)
			if ui.Button(c, "检查更新").Clicked() {
				checkForUpdates()
			}
		})
	})
}

func (a *App) updateToggle(c *ui.Context, th *ui.Theme, key, title, desc string, get func() bool, set func(bool), disabled bool) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		var titleEl ui.Element
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			titleEl = ui.Text(c, title).FontSize(fontBody)
			ui.Text(c, desc).FontSize(fontCaption).TextColor(th.TextMuted).Wrap()
		})
		on := get()
		sw := ui.Switch(c.Key(key), &on).Label(title).LabelledBy(titleEl)
		if disabled {
			sw = sw.Disabled(true)
		}
		// The label click reaches the switch. Ignore it while checking is off,
		// so installs cannot be turned on without checks.
		if sw.Changed() && !disabled {
			set(on)
		}
	})
}

func updateCheckedText(t time.Time) string {
	if t.IsZero() {
		return "每天检查一次"
	}
	return "每天检查一次，上次检查 " + t.Local().Format("2006-01-02 15:04")
}
