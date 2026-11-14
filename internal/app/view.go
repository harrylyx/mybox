package app

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/harrylyx/QuickBo/internal/config"
	"github.com/harrylyx/QuickBo/internal/live"
	"github.com/harrylyx/QuickBo/internal/model"
)

const (
	sideWidth float32 = 224
	pagePad   float32 = 28

	// maxSidebarItems keeps one frame bounded when a subscription brings
	// thousands of sources. The rest remain reachable through search in
	// 管理 → 片源.
	maxSidebarItems = 200
)

var cardMotion = ui.Animation{Duration: 200 * time.Millisecond, Ease: ui.EaseOut}

// shellSwap is the one animated seam between the empty shell and the app.
// Both live under the same keyed parent, so mygo keeps a copy of whichever
// one just left and animates it out while the other enters.
//
// Purpose: continuity and orientation, not delight. The swap is rare (first
// configuration load, or re-enabling subscriptions), which is why it earns
// a directional move. Both directions share the steep-start Enter curve;
// the exit is shorter and travels a small fixed offset so it releases
// attention quietly. The exit takes a 2px blur, so the two pages never
// read as doubled text during the overlap; the entering page stays sharp.
var (
	shellEase      = ui.CubicBezier(0.22, 1, 0.36, 1)
	shellEnterPose = &ui.Motion{Y: 28, Opacity: 0, Scale: 1}
	shellExitPose  = &ui.Motion{Y: -12, Opacity: 0, Blur: 2, Scale: 1}
)

func shellSwap(c *ui.Context) ui.ElementTransition {
	if c.Preferences().ReduceMotion {
		return ui.ElementTransition{Duration: -1}
	}
	return ui.ElementTransition{
		Duration:     280 * time.Millisecond,
		ExitDuration: 180 * time.Millisecond,
		Ease:         shellEase,
		ExitEase:     shellEase,
		Enter:        shellEnterPose,
		Exit:         shellExitPose,
	}
}

func (a *App) animateCard(card, frame ui.Element, hot, light bool) {
	pose := ui.Pose{}
	if hot {
		pose.Y = -2
	}
	card.AnimatePose(pose, cardMotion)
	y, blur, ink := cardShadow(light, hot)
	frame.Shadow(0, y, blur, 0, ink).
		Transition(ui.ElementTransition{Duration: 200 * time.Millisecond, Ease: ui.EaseOut, Shadows: true})
}

// View draws one frame. The chrome is a cinema shell: a quiet source
// rail, a full-bleed hero, and a poster wall. Red is reserved for the
// primary action and for progress.
func (a *App) View(c *ui.Context) {
	a.drawFrame(func() {
		if a.px.pip {
			// A click can order this window front after enterPip hid it. Stay
			// hidden while the picture is in the small window.
			if a.win != nil && a.win.IsVisible() {
				a.win.Hide()
			}
			return
		}
		th := a.useTheme(c)
		a.watchLivePlayback()
		a.playerKeys(c)
		ui.Box(c.Key("shell")).Fill().Background(th.Background).Children(func() {
			if a.catalog == nil {
				a.viewWelcome(c, th)
			} else {
				a.viewMain(c, th)
			}
		})
		a.viewSubs(c, th)
		a.viewSettings(c, th)
	})
}

// viewMain is the app itself: the fullscreen player or the sidebar shell.
// It shares one keyed parent with the welcome screen, so mygo can hold a
// copy of whichever screen just left and animate it out (shellSwap).
func (a *App) viewMain(c *ui.Context, th *ui.Theme) {
	ui.Box(c.Key("main-screen")).Fill().Background(th.Background).Transition(shellSwap(c)).Children(func() {
		if a.screen == screenPlay || a.liveFullscreen() {
			// A live channel in fullscreen takes the whole window, as a VOD does.
			ui.Box(c).Fill().Background(ui.Hex("#000000")).Children(func() {
				a.viewPlay(c, th)
			})
			return
		}
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			a.viewSidebar(c, th)
			main := ui.Box(c.Key("main")).Grow(1).FillHeight().Background(th.Background).Clip()
			main.Children(func() {
				// The detail page sits on its poster: blurred, tinted with the
				// poster's color, fading into the page (detailBackdrop).
				if a.screen == screenDetail {
					a.detailBackdrop(c, th)
				}
				// Positioned too, after the backdrop, so it paints over it:
				// mygo paints positioned elements over the flow.
				ui.Column(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Children(func() {
					a.viewTop(c, th)
					if a.screen != screenDetail {
						ui.Box(c).FillWidth().Height(1).Background(th.Border)
					}
					ui.Column(c).Grow(1).Children(func() {
						if a.screen == screenHome {
							a.viewHome(c, th)
							return
						}
						if a.screen == screenBrowse {
							a.viewBrowse(c, th)
							return
						}
						ui.Column(c).Grow(1).Padding(20, pagePad, 16, pagePad).Gap(16).Children(func() {
							switch a.screen {
							case screenCats:
								a.viewCats(c, th)
							case screenLiveAgg:
								a.viewLiveAgg(c, th)
							case screenHistory:
								a.viewHistory(c, th)
							case screenFav:
								a.viewFavs(c, th)
							case screenSubs, screenSources, screenBlacklist, screenRules:
								a.viewManage(c, th)
							case screenMore:
								a.viewMore(c, th)
							case screenIndex:
								a.viewIndex(c, th)
							case screenUnsupported:
								a.viewUnsupported(c, th)
							case screenDetail:
								a.viewDetail(c, th)
							case screenLive:
								a.viewLive(c, th)
							case screenEmpty:
								a.emptyState(c, th, "这份配置是空的", "没有点播站，也没有直播。换一份配置，或往里面加上站点。",
									stateAction{label: "添加订阅", primary: true, click: a.openSubs},
									stateAction{label: "打开本地文件", click: a.pickFile})
							default:
								a.viewBrowse(c, th)
							}
						})
					})
				})
			})
		})
	})
}

func (a *App) viewSubs(c *ui.Context, th *ui.Theme) {
	ui.Modal(c, &a.adding, func() {
		ui.Column(c).Width(640).Gap(14).Children(func() {
			a.subsPanel(c, th, false)
		})
	})
}

// subsPanel is the subscription list with its add field: in a dialog before
// any configuration is loaded (page false), and as the 订阅 tab of the
// management page after.
func (a *App) subsPanel(c *ui.Context, th *ui.Theme, page bool) {
	if !page {
		ui.Text(c, "订阅").FontSize(fontHeadline).Bold()
	}
	ui.Text(c, "订阅地址或本地 JSON，多份会合并。").TextColor(th.TextMuted).Wrap()

	sortSel := a.subSort
	if sortSel == "" {
		sortSel = "名称"
	}
	batchLabel := "批量管理"
	if a.subBatch {
		batchLabel = "完成"
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.SearchField(c, &a.subFilter).Placeholder("搜索订阅").Width(200).Label("搜索订阅")
		ui.Column(c).Grow(1)
		ui.Select(c, &sortSel, subSortOptions).Label("排序").Width(124)
		if ui.Button(c, batchLabel).Clicked() {
			a.subBatch = !a.subBatch
			if !a.subBatch {
				a.subSel = nil
				a.subConfirm = nil
			}
		}
	})
	a.subSort = sortSel

	subs := a.filteredSubs()
	counts := a.subSourceCounts()
	stop := false
	list := ui.Scroll(c).FillWidth()
	if page {
		list.Grow(1).MinHeight(160)
	} else {
		list.Height(240)
	}
	list.Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if len(subs) == 0 {
				if len(a.subs) == 0 {
					ui.Text(c, "还没有订阅").TextColor(th.TextMuted)
				} else {
					ui.Text(c, "没有匹配「"+strings.TrimSpace(a.subFilter)+"」的订阅").TextColor(th.TextMuted)
				}
				return
			}
			for i := range subs {
				sub := subs[i]
				ref := sub.Ref
				ui.Row(c.Key("sub:"+ref)).FillWidth().Padding(10, 12).Radius(8).Background(th.Surface).AlignItems(ui.Center).Gap(8).Children(func() {
					if a.subBatch {
						checked := a.subSel[ref]
						if ui.Checkbox(c, &checked, "").Changed() {
							if a.subSel == nil {
								a.subSel = map[string]bool{}
							}
							if checked {
								a.subSel[ref] = true
							} else {
								delete(a.subSel, ref)
							}
						}
					}
					ui.Column(c).Grow(1).Gap(2).Shrink(1).Children(func() {
						ui.Text(c, sub.Name).FontSize(fontBody).SingleLine()
						ui.Text(c, ref).FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
						if !sub.Enabled {
							ui.Text(c, "已停用").FontSize(fontCaption).TextColor(th.TextMuted)
						}
					})
					if n := counts[subLabel(sub)]; n > 0 {
						ui.Text(c, fmt.Sprintf("%d 个源", n)).FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
					}
					if !a.subBatch {
						label, glyph := "停用", iconPause
						if !sub.Enabled {
							label, glyph = "启用", iconPlay
						}
						a.iconBtn(c, "sub-toggle:"+ref, glyph, label, 36, 18, th.TextMuted, func() {
							a.toggleSubscription(ref)
							stop = true
						})
						a.iconBtn(c, "sub-drop:"+ref, iconTrash, "删除订阅", 36, 18, th.Danger, func() {
							a.removeSubscription(ref)
							stop = true
						})
					}
				})
				if stop {
					return
				}
			}
		})
	})
	if stop {
		return
	}

	if a.subBatch {
		all := ui.CheckOff
		selCount := 0
		for _, s := range subs {
			if a.subSel[s.Ref] {
				selCount++
			}
		}
		switch {
		case selCount == len(subs) && len(subs) > 0:
			all = ui.CheckOn
		case selCount > 0:
			all = ui.CheckMixed
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.CheckboxState(c, &all, "全选").Changed() {
				if a.subSel == nil {
					a.subSel = map[string]bool{}
				}
				if all == ui.CheckOn {
					for _, s := range subs {
						a.subSel[s.Ref] = true
					}
				} else {
					a.subSel = nil
				}
			}
			ui.Column(c).Grow(1)
			selected := func() []string {
				var out []string
				for r, on := range a.subSel {
					if on {
						out = append(out, r)
					}
				}
				return out
			}
			if ui.Button(c, "启用").Disabled(selCount == 0).Clicked() && selCount > 0 {
				a.setSubscriptionsEnabled(selected(), true)
				stop = true
			}
			if ui.Button(c, "停用").Disabled(selCount == 0).Clicked() && selCount > 0 {
				a.setSubscriptionsEnabled(selected(), false)
				stop = true
			}
			if ui.Button(c, "删除").Disabled(selCount == 0).Clicked() && selCount > 0 {
				a.subConfirm = selected()
				stop = true
			}
		})
		if stop {
			return
		}
		if n := len(a.subConfirm); n > 0 {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("删除选中的 %d 份订阅？", n)).Grow(1)
				if ui.Button(c, "取消").Clicked() {
					a.subConfirm = nil
				}
				if ui.Button(c, "确认删除").Clicked() {
					refs := append([]string(nil), a.subConfirm...)
					a.subConfirm = nil
					a.deleteSubscriptions(refs)
					stop = true
				}
			})
			if stop {
				return
			}
		}
	}

	ui.Text(c, "配置 URL").FontSize(fontCaption).TextColor(th.TextMuted)
	field := ui.TextInput(c, &a.urlDraft).Placeholder("https://example.com/tvbox.json").Label("配置 URL")
	if field.Submitted() {
		ref := a.urlDraft
		a.urlDraft = ""
		a.adding = false
		a.AddSubscription(ref)
		return
	}
	if a.loadErr != "" && a.catalog != nil {
		a.noteWithActions(c, th, a.loadErr, true, stateAction{label: "重试订阅", primary: true, click: a.retrySubs})
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if !page && ui.Button(c, "取消").Clicked() {
			a.adding = false
			a.subBatch = false
			a.subSel = nil
			a.subConfirm = nil
			a.subFilter = ""
		}
		if ui.Button(c, "打开文件").Clicked() {
			a.pickFile()
		}
		if ui.PrimaryButton(c, "添加订阅").Clicked() {
			ref := a.urlDraft
			a.urlDraft = ""
			a.adding = false
			a.AddSubscription(ref)
			return
		}
	})
}

func (a *App) viewSettings(c *ui.Context, th *ui.Theme) {
	ui.Modal(c, &a.settingsOpen, func() {
		ui.Column(c).Width(500).Gap(14).Children(func() {
			ui.Text(c, "设置").FontSize(fontHeadline).Bold()
			// The sections scroll together when the window is short; the
			// title and the one primary action stay put outside the scroll.
			ui.Scroll(c).FillWidth().MaxHeight(490).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					settingsCard(c, th, "settings-appearance", "外观", func() {
						ui.Row(c).Gap(8).Children(func() {
							for _, opt := range [][2]string{{appearanceSystem, "跟随系统"}, {appearanceLight, "浅色"}, {appearanceDark, "深色"}} {
								cur := a.appearance
								if !validAppearance(cur) {
									cur = appearanceSystem
								}
								a.choice(c, th, "外观"+opt[1], opt[1], cur == opt[0], func() {
									a.appearance = opt[0]
									a.saveSettings()
								})
							}
						})
					})
					a.viewUpdateSettings(c, th)
					a.viewStorageSettings(c, th)
				})
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "完成").Clicked() {
					a.settingsOpen = false
				}
			})
		})
	})
}

// settingsCard groups one section of the dialog in a captioned surface, so
// it reads as three short groups instead of one long list of controls.
func settingsCard(c *ui.Context, th *ui.Theme, key, caption string, body func()) {
	ui.Column(c.Key(key)).FillWidth().Padding(12, 14).Gap(8).
		Radius(10).Background(th.Surface).Border(1, th.Border).Children(func() {
		ui.Text(c, caption).FontSize(fontCaption).Bold().TextColor(th.TextMuted)
		body()
	})
}

// viewStorageSettings is the settings modal's 存储 section: each cache
// with what it takes and a way to drop it, plus the crash log's directory.
// A clear asks twice, as deleting subscriptions does, so a slip does not
// wipe the collection.
func (a *App) viewStorageSettings(c *ui.Context, th *ui.Theme) {
	// No settingsPath means nowhere to keep the files (tests); the
	// in-memory posters alone are not worth the row.
	if a.settingsPath == "" {
		return
	}
	settingsCard(c, th, "settings-storage", "存储", func() {
		// The log is not a cache: it is the evidence a crash report needs.
		// It leads the group so the shortcut stays visible on short windows,
		// and opens its directory instead of deleting it.
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "运行日志").FontSize(fontBody)
			ui.Text(c, humanBytes(a.fileSize(a.logPath()))).FontSize(fontCaption).TextColor(th.TextMuted)
			ui.Box(c).Grow(1)
			if ui.Button(c, "打开").Tooltip("在文件管理器里打开 quickbo.log 所在目录").Clicked() {
				a.openLogDir()
			}
		})
		ui.Box(c).FillWidth().Height(1).Background(th.Border)
		var records int64
		for _, p := range a.recordPaths() {
			records += a.fileSize(p)
		}
		type cacheRow struct {
			key, name, size string
			clear           func()
		}
		rows := []cacheRow{
			{"posters", "图片缓存", fmt.Sprintf("%d 张", len(a.posters)), a.clearPosterCache},
			{"index", "首页采集", humanBytes(a.fileSize(a.cachePath())), a.clearCache},
			{"configs", "订阅解析", humanBytes(a.fileSize(a.configCachePath())), a.clearConfigCache},
			{"records", "临时记录", humanBytes(records), a.clearRecords},
		}
		ui.Column(c).Gap(6).Children(func() {
			for _, row := range rows {
				row := row
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, row.name).FontSize(fontBody)
					ui.Text(c, row.size).FontSize(fontCaption).TextColor(th.TextMuted)
					ui.Box(c).Grow(1)
					if a.cacheConfirm == row.key {
						if ui.Button(c, "取消").Clicked() {
							a.cacheConfirm = ""
						}
						if ui.Button(c, "确认清除").Clicked() {
							row.clear()
							a.cacheConfirm = ""
						}
					} else if ui.Button(c, "清除").Clicked() {
						a.cacheConfirm = row.key
					}
				})
			}
		})
	})
}

// openSubs shows the subscriptions: the 订阅 tab of the management page
// once a configuration is loaded, a dialog on the welcome screen before.
func (a *App) openSubs() {
	a.urlDraft = ""
	a.settingsOpen = false
	if a.catalog != nil {
		a.adding = false
		a.goManage(screenSubs)
		return
	}
	a.adding = true
}

func (a *App) openSettings() {
	a.adding = false
	a.settingsOpen = true
}

// hoverAction builds a row's hover-only button at all times and only toggles
// whether it shows. Building it on hover alone made the row taller while
// hovered, so after a delete the row sliding under the pointer grew, moved
// the rows below, and the sidebar shook.
func (a *App) hoverAction(c *ui.Context, show bool, build func()) {
	slot := ui.Row(c).Shrink(0)
	if !show {
		slot = slot.Invisible()
	}
	slot.Children(build)
}

func (a *App) viewSidebar(c *ui.Context, th *ui.Theme) {
	ink := sidebarInk(a.light)
	// The window controls the desktop leaves over the page (the traffic
	// lights on macOS, at the top left) sit over this rail: it clears the
	// band, and drags the window by its padding and header, as a native
	// sidebar does.
	bar := c.TitleBar()
	top := float32(20)
	if bar.Height > 0 {
		top = max(top, bar.Height+6)
	}
	col := ui.Themed(c, sidebarTheme(th, ink)).Width(sideWidth).FillHeight().Background(ink).Padding(top, 12, 16, 12).Gap(18).DragWindow()
	col.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(2, 8, 0, 8).DragWindow().Children(func() {
			logoMark(c, th, 30)
			ui.Text(c, "QuickBo").FontSize(fontTitle).FontWeight(weightTitle).PassThrough()
		})
		side := ui.Sidebar(c, &a.sideSel, func() {
			ui.SidebarSection(c, "导航", nil, func() {
				ui.SidebarItem(c, "home", iconHome, "首页")
				ui.SidebarItem(c, "cats", iconList, "分类")
				if a.catalog != nil && len(a.catalog.Lives) > 0 {
					ui.SidebarItem(c, "liveagg", iconSignal, "直播")
				}
			})
			ui.SidebarSection(c, "资料库", nil, func() {
				ui.SidebarItem(c, "history", iconClock, "历史")
				ui.SidebarItem(c, "fav", iconHeart, "收藏")
			})
			for _, g := range a.sideGroups() {
				g := g
				ui.SidebarSection(c, sideGroupTitle(g), a.sidebarOpenState(g.name, a.groupHasSelection(g)), func() {
					sites := g.sites
					if len(sites) > maxSidebarItems {
						sites = sites[:maxSidebarItems]
					}
					for _, s := range sites {
						m := s.Meta()
						key := m.Key
						item := ui.SidebarItem(c, "site:"+key, iconScreen, m.Name)
						hovered := item.Hovered()
						item.ContextMenu(func(menu *ui.Menu) {
							if menu.Item("删除").Chosen() {
								a.dropSite(key)
							}
						})
						item.Children(func() {
							a.hoverAction(c, hovered, func() {
								a.iconBtn(c, "drop-site:"+key, iconClose, "移除源", 18, 11, th.TextMuted, func() {
									a.dropSite(key)
								})
							})
						})
					}
					if len(g.sites) > len(sites) {
						ui.Text(c, fmt.Sprintf("还有 %d 个片源，到「管理 → 片源」搜索", len(g.sites)-len(sites))).
							FontSize(fontCaption).TextColor(th.TextMuted).Wrap()
					}
					lives := g.lives
					if len(lives) > maxSidebarItems {
						lives = lives[:maxSidebarItems]
					}
					for _, li := range lives {
						lv, idx := li.entry, li.idx
						item := ui.SidebarItem(c, fmt.Sprintf("live:%d", idx), iconSignal, lv.Name)
						hovered := item.Hovered()
						item.ContextMenu(func(menu *ui.Menu) {
							if menu.Item("删除").Chosen() {
								a.dropLive(lv.URL)
							}
						})
						item.Children(func() {
							a.hoverAction(c, hovered, func() {
								a.iconBtn(c, "drop-live:"+lv.URL, iconClose, "移除直播源", 18, 11, th.TextMuted, func() {
									a.dropLive(lv.URL)
								})
							})
						})
					}
					if len(g.lives) > len(lives) {
						ui.Text(c, fmt.Sprintf("还有 %d 个直播源", len(g.lives)-len(lives))).
							FontSize(fontCaption).TextColor(th.TextMuted).Wrap()
					}
				})
			}
		}).Grow(1)
		if side.Changed() {
			a.onSide()
		}
		ui.Box(c).FillWidth().Height(1).Background(th.Border)
		// The column's own gap is 18; these two sit closer, 管理 above 设置.
		ui.Column(c).Gap(4).Children(func() {
			a.sideEntry(c, th, "manage-entry", "管理", "打开管理", iconSliders, isManage(a.screen), func() {
				a.settingsOpen = false
				a.goManage(a.manageLast)
			})
			a.sideEntry(c, th, "settings-entry", "设置", "打开设置", iconGear, a.settingsOpen, a.openSettings)
		})
	})
}

// sideEntry is a full-width row at the bottom of the sidebar: an icon and a
// label, a hover fill, and the accent tint while it is the open page.
func (a *App) sideEntry(c *ui.Context, th *ui.Theme, key, label, tip string, icon *ui.SVG, on bool, click func()) {
	entry := ui.Row(c.Key(key)).FillWidth().Padding(10, 10).Radius(8).AlignItems(ui.Center).Gap(10).
		OnClick(click).Cursor(ui.CursorPointer).Tooltip(tip).Label(label)
	switch {
	case on:
		entry.Background(th.Accent.Alpha(0.12))
	case entry.Hovered():
		entry.Background(th.SurfaceHover)
	}
	entry.Transition(ui.ElementTransition{Duration: 150 * time.Millisecond}).Children(func() {
		ui.Icon(c, icon).Size(18, 18).TextColor(th.TextMuted)
		ui.Text(c, label).FontSize(fontBody).TextColor(th.Text)
	})
}

func (a *App) viewTop(c *ui.Context, th *ui.Theme) {
	// The native title bar is hidden, so this row is the window's: it
	// drags it, and pads away from the window controls the desktop leaves
	// over the page — none on macOS, where the traffic lights sit over
	// the sidebar, the caption buttons on Windows and Linux on the right.
	bar := c.TitleBar()
	ui.Row(c).DragWindow().Padding(12, pagePad+bar.Right, 12, pagePad).Gap(12).AlignItems(ui.Center).Children(func() {
		if a.screen == screenDetail {
			// The title is the page's own, in large: the bar only takes
			// you back, as a disc over the backdrop.
			glassButton(c, th, "detail-back", iconBack, "返回", 36, th.Text, a.back)
		} else {
			if a.screen == screenPlay {
				if ui.Button(c, "返回").Clicked() {
					a.back()
				}
			}
			ui.Column(c).Gap(1).Shrink(1).Children(func() {
				if a.screen != screenHome {
					ui.Text(c, a.crumb()).FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
				}
				ui.Text(c, a.headline()).FontSize(fontTitle).FontWeight(weightTitle).SingleLine()
			})
		}
		// Pass-through so the empty middle of the bar stays a drag handle.
		ui.Column(c).Grow(1).PassThrough()
		if a.screen == screenBrowse || a.screen == screenHome || a.screen == screenLive || a.screen == screenLiveAgg {
			ph := "搜索片名"
			if a.screen == screenHome {
				ph = "搜索全部片源"
			}
			if a.screen == screenLive || a.screen == screenLiveAgg {
				ph = "过滤频道"
			}
			field := ui.SearchField(c, &a.query).Placeholder(ph).Width(260).Label("搜索")
			if a.screen == screenBrowse || a.screen == screenHome {
				a.watchSearch(c, field)
			}
		}
	})
	if a.loading || a.loadErr != "" {
		ui.Column(c).Padding(0, pagePad, 10, pagePad).Gap(8).Children(func() {
			if a.loading {
				msg := "正在读取新的配置…"
				if a.loadNote != "" {
					msg = a.loadNote
				}
				a.loadingRow(c, th, msg)
			}
			if a.loadErr != "" {
				a.noteWithActions(c, th, a.loadErr, true, stateAction{label: "重试订阅", primary: true, click: a.retrySubs})
			}
		})
	}
}

const searchWait = 300 * time.Millisecond

// watchSearch commits a typed query after a short pause, on Enter, or as
// soon as the field is cleared. The search field already draws its own
// clear button while it holds text.
func (a *App) watchSearch(c *ui.Context, field ui.Element) {
	q := strings.TrimSpace(a.query)
	changed := q != strings.TrimSpace(a.committed)
	if field.Changed() && changed {
		a.searchDue = time.Now().Add(searchWait)
	}
	ready := !a.searchDue.IsZero() && !time.Now().Before(a.searchDue)
	cleared := field.Changed() && q == "" && strings.TrimSpace(a.committed) != ""
	if field.Submitted() || cleared || (ready && changed) {
		a.applyQuery()
		return
	}
	if changed && !a.searchDue.IsZero() {
		c.After(time.Until(a.searchDue))
	}
}

func (a *App) applyQuery() {
	a.committed = strings.TrimSpace(a.query)
	a.pageNum = 1
	a.searchDue = time.Time{}
	if a.screen == screenHome {
		if a.committed == "" {
			a.homeHits = nil
			a.homeSearching = false
		} else {
			a.searchHome()
		}
		return
	}
	if a.committed == "" {
		a.screen = screenHome
		a.sideSel = "home"
		a.lastSide = "home"
	} else if a.siteKey != "" {
		a.sideSel = "site:" + a.siteKey
		a.lastSide = a.sideSel
	}
	a.loadList()
}

func (a *App) crumb() string {
	switch a.screen {
	case screenHome:
		return "首页"
	case screenCats:
		return "首页"
	case screenLiveAgg:
		return "直播"
	case screenHistory:
		return "资料库"
	case screenFav:
		return "资料库"
	case screenSubs, screenSources, screenBlacklist, screenRules:
		return "管理"
	case screenMore:
		return "首页"
	case screenDetail:
		return "详情"
	case screenPlay:
		return "正在播放"
	case screenLive:
		return "直播"
	case screenUnsupported:
		return "源"
	case screenIndex:
		return "仓库"
	default:
		if strings.TrimSpace(a.query) != "" {
			return "搜索"
		}
		return "首页"
	}
}

func (a *App) headline() string {
	switch a.screen {
	case screenHome:
		if q := strings.TrimSpace(a.committed); q != "" {
			return "搜索「" + q + "」"
		}
		return "全部片源"
	case screenCats:
		return "分类"
	case screenLiveAgg:
		return "直播"
	case screenHistory:
		return "观看历史"
	case screenFav:
		return "我的收藏"
	case screenSubs:
		return "订阅"
	case screenSources:
		return "片源"
	case screenBlacklist:
		return "黑名单"
	case screenRules:
		return "首页分类"
	case screenMore:
		return a.moreRow.Name
	case screenDetail:
		if a.detailTitle != "" {
			return a.detailTitle
		}
	}
	if a.screen == screenLive && a.catalog != nil && a.liveIndex >= 0 && a.liveIndex < len(a.catalog.Lives) {
		return a.catalog.Lives[a.liveIndex].Name
	}
	if m, ok := a.currentMeta(); ok && a.screen != screenIndex {
		return m.Name
	}
	return "QuickBo"
}

func (a *App) viewBrowse(c *ui.Context, th *ui.Theme) {
	searching := strings.TrimSpace(a.committed) != ""
	a.maybeLoadMore()
	cols, cellW, cellH, tracks := a.posterGrid(c)
	ui.Scroll(c).Grow(1).TrackScroll(&a.browseScroll).Children(func() {
		ui.Column(c).Gap(20).Children(func() {
			ui.Column(c).Padding(8, pagePad, 0, pagePad).Gap(18).Children(func() {
				if m, ok := a.currentMeta(); ok && m.Level == config.LevelPartial && m.Reason != "" {
					a.note(c, th, m.Reason, false)
				}
				if searching {
					label := fmt.Sprintf("搜索「%s」", strings.TrimSpace(a.committed))
					if a.page.Total > 0 && a.page.Total < 500 {
						label += fmt.Sprintf("  ·  %d 条", a.page.Total)
					}
					ui.Text(c, label).FontSize(fontHero).Bold()
				}
				if len(a.cats) > 0 && !searching {
					a.catTabs(c, th)
				}
				if a.listLoad && len(a.page.Items) == 0 {
					a.skeletonRow(c, th, cols, cellW, cellH)
					return
				}
				if a.listErr != "" {
					a.noteWithActions(c, th, a.listErr, true, stateAction{label: "重试", primary: true, click: a.retryList})
				}
				n := len(a.page.Items)
				if n == 0 && a.listErr == "" {
					actions := []stateAction{
						{label: "重新加载", primary: true, click: a.retryList},
						{label: "返回首页", click: a.goHome},
					}
					if searching {
						actions[0] = stateAction{label: "清空搜索", primary: true, click: func() {
							a.query = ""
							a.committed = ""
							a.pageNum = 1
							a.retryList()
						}}
					}
					a.emptyState(c, th, "没有结果", "换一个分类，或换个关键词再搜。", actions...)
					return
				}
				ui.Grid(c).ColumnTracks(tracks...).Gap(16).Children(func() {
					for i := 0; i < n; i++ {
						a.posterCard(c, th, a.siteKey, a.page.Items[i], cellW, cellH)
					}
				})
				if a.appending {
					a.loadingRow(c, th, "正在加载更多…")
				}
				a.viewPager(c, th)
			})
		})
	})
}

// posterGrid sizes a 2:3 cover wall from the window. Fixed tracks keep a
// short page from stretching two titles into banners.
func (a *App) posterGrid(c *ui.Context) (cols int, cellW, cellH float32, tracks []ui.Track) {
	winW, _ := c.Size()
	inner := winW - sideWidth - pagePad*2
	if inner < 320 {
		inner = winW - 48
	}
	if inner < 280 {
		inner = 280
	}
	const gap float32 = 16
	cols = int((inner + gap) / (164 + gap))
	if cols < 2 {
		cols = 2
	}
	if cols > 7 {
		cols = 7
	}
	cellW = (inner - gap*float32(cols-1)) / float32(cols)
	if cellW < 120 {
		cellW = 120
	}
	cellH = cellW * 1.5
	tracks = make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(cellW)
	}
	return cols, cellW, cellH, tracks
}

func portraitHeight(winH float32) float32 {
	ph := winH * 0.46
	if ph < 220 {
		return 220
	}
	if ph > 460 {
		return 460
	}
	return ph
}

// choiceTipRunes is how long a choice's label may be before it gets a
// tooltip with the whole of it.
const choiceTipRunes = 5

// episodeCell is how wide an episode button is for names up to longest
// runes: narrow for 第12集, wider for a variety show's dated, titled ones.
func episodeCell(longest int) float32 {
	switch {
	case longest <= 5:
		return 104
	case longest <= 9:
		return 160
	}
	return 232
}

func longestEpisode(eps []model.Episode) int {
	n := 0
	for _, ep := range eps {
		n = max(n, utf8.RuneCountInString(ep.Name))
	}
	return n
}

func (a *App) episodeTracks(c *ui.Context, longest int) []ui.Track {
	winW, winH := c.Size()
	available := winW - sideWidth - pagePad*2 - portraitHeight(winH)*2/3 - 22
	const gap float32 = 8
	cell := episodeCell(longest)
	cols := int((available + gap) / (cell + gap))
	if cols < 2 {
		cols = 2
	}
	if cols > 8 {
		cols = 8
	}
	tracks := make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(cell)
	}
	return tracks
}

func (a *App) catTabs(c *ui.Context, th *ui.Theme) {
	ui.ScrollBoth(c).MaxHeight(48).Children(func() {
		ui.Row(c).Gap(20).AlignItems(ui.End).Children(func() {
			for _, cat := range a.cats {
				cat := cat
				a.tab(c, th, "cat:"+cat.ID, cat.Name, cat.ID == a.catID, func() { a.pickCat(cat.ID) })
			}
		})
	})
}

func (a *App) tab(c *ui.Context, th *ui.Theme, key, label string, on bool, click func()) {
	p := ui.Column(c.Key(key)).Gap(8).Padding(10, 2).OnClick(click).Cursor(ui.CursorPointer)
	hovered := p.Hovered()
	fg := th.TextMuted
	if on || hovered {
		fg = th.Text
	}
	p.Children(func() {
		ui.Text(c, label).FontSize(fontBody).TextColor(fg).SingleLine()
		bar := ui.Box(c).Height(2).Radius(1)
		switch {
		case on:
			bar.Background(th.Accent)
		case hovered:
			bar.Background(th.Border)
		}
	})
}

func (a *App) viewContinue(c *ui.Context, th *ui.Theme) {
	if a.lib == nil || a.screen != screenHome {
		return
	}
	watches := a.lib.watches()
	if len(watches) == 0 {
		return
	}
	if len(watches) > 8 {
		watches = watches[:8]
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Text(c, "继续观看").FontSize(fontTitle).Bold()
		ui.ScrollBoth(c).MaxHeight(248).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				for _, w := range watches {
					w := w
					card := ui.Column(c.Key("w-" + w.Key)).Width(104).Gap(6).OnClick(func() {
						a.playHistory(w)
					}).Cursor(ui.CursorPointer)
					card.ContextMenu(func(menu *ui.Menu) {
						if menu.Item("从继续观看删除").Chosen() {
							a.lib.removeWatch(w.Key)
						}
					})
					hot := card.Hovered()
					card.Children(func() {
						frame := ui.Column(c.Key("continue-frame:" + w.Key)).Width(104).Height(156).Radius(8).Clip().Background(th.Surface)
						a.animateCard(card, frame, hot, a.light)
						frame.Children(func() {
							a.cover(c, th, w.Pic, w.Title, 104, 156, nil, hot)
						})
						ui.Text(c, w.Title).FontSize(fontCaption).SingleLine()
						if meta := continueMeta(w); meta != "" {
							ui.Text(c, meta).FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
						}
						frac := float32(0)
						if w.Duration > 0 {
							frac = float32(w.Position / w.Duration)
						}
						ui.Box(c).FillWidth().Height(4).Radius(2).Background(th.SurfaceHover).Children(func() {
							ui.Box(c).Height(4).WidthPercent(frac * 100).Background(th.Accent)
						})
					})
				}
			})
		})
	})
}

func (a *App) posterCard(c *ui.Context, th *ui.Theme, siteKey string, item model.Item, cellW, cellH float32) {
	a.posterCardWithRemove(c, th, siteKey, item, cellW, cellH, nil)
}

func (a *App) posterCardWithRemove(c *ui.Context, th *ui.Theme, siteKey string, item model.Item, cellW, cellH float32, remove func()) {
	open := func() {
		if siteKey != "" {
			a.siteKey = siteKey
		}
		a.openDetail(item)
	}
	card := ui.Column(c.Key("card:" + siteKey + ":" + item.ID)).Width(cellW).Gap(8).OnClick(open).Cursor(ui.CursorPointer)
	hot := card.Hovered()
	card.Children(func() {
		frame := ui.Column(c.Key("poster-frame:" + siteKey + ":" + item.ID)).Width(cellW).Height(cellH).Radius(8).Clip().Background(th.Surface)
		a.animateCard(card, frame, hot, a.light)
		frame.Children(func() {
			a.cover(c, th, item.Pic, item.Title, cellW, cellH, open, hot)
			if item.Remarks != "" {
				ui.Column(c).Absolute().Left(8).Top(8).Padding(3, 6).Radius(4).Background(ui.RGBA(0, 0, 0, 0.62)).Children(func() {
					ui.Text(c, item.Remarks).FontSize(fontCaption).TextColor(th.AccentText).SingleLine()
				})
			}
			if hot && remove != nil {
				a.removeBadge(c, th, "remove-poster:"+siteKey+":"+item.ID, "删除收藏", remove)
			}
		})
		ui.Text(c, item.Title).FontSize(fontBody).SingleLine().OnClick(open)
	})
}

func (a *App) removeBadge(c *ui.Context, th *ui.Theme, key, label string, remove func()) {
	badge := ui.Column(c.Key(key)).Absolute().Right(8).Top(8).Size(24, 24).Radius(12).Center().
		Background(ui.RGBA(0, 0, 0, 0.72)).OnClick(remove).Cursor(ui.CursorPointer).Tooltip(label).Label(label)
	if badge.Hovered() {
		badge.Background(th.Accent)
	}
	badge.Transition(ui.ElementTransition{Duration: 150 * time.Millisecond}).Children(func() {
		ui.Icon(c, iconClose).Size(14, 14).TextColor(th.AccentText)
	})
}

// coverPreload is how far outside the viewport, in DIPs, a cover starts
// loading, so posters are there by the time they scroll in.
const coverPreload = 600

func (a *App) cover(c *ui.Context, th *ui.Theme, pic, title string, w, h float32, click func(), zoom bool) {
	a.coverFit(c, th, pic, title, w, h, click, zoom, ui.Cover)
}

// coverFit is cover with how the picture fills its box: Contain shows the
// whole of a landscape cover where Cover would crop it to its middle.
func (a *App) coverFit(c *ui.Context, th *ui.Theme, pic, title string, w, h float32, click func(), zoom bool, fit ui.Fit) {
	pic = model.ResolveURL("", pic)
	if pic != "" && !a.failed[pic] {
		// One element while loading and after, so the poster fades in over
		// its placeholder once it arrives instead of popping in.
		box := ui.Box(c.Key("cover:" + pic)).Width(w).Height(h)
		// Only covers near the viewport load and count as drawn. Otherwise a
		// long grid marks every poster as just drawn each frame, the cache
		// evicts ones on screen, they reload and evict others: posters blink
		// and new ones never get a download slot.
		var bmp *ui.Bitmap
		if box.InViewMargin(coverPreload) {
			bmp = a.poster(pic)
		} else {
			bmp = a.cachedPoster(pic)
			// InViewMargin answers from the frame before; a cover laid out for
			// the first time needs one more frame to know whether it shows.
			if laid := a.screen + "|" + pic; !a.coverLaid[laid] {
				if a.coverLaid == nil || len(a.coverLaid) > 20000 {
					a.coverLaid = map[string]bool{}
				}
				a.coverLaid[laid] = true
				c.After(50 * time.Millisecond)
			}
		}
		in := fadeIn(box, bmp != nil)
		if in < 1 {
			box.Background(th.SurfaceHover)
		}
		box.Children(func() {
			if bmp == nil || in <= 0 {
				return
			}
			img := ui.Image(c, bmp).Width(w).Height(h).Fit(fit).Transition(ui.ElementTransition{Duration: 180 * time.Millisecond})
			if in < 1 {
				img.Opacity(in)
			}
			if zoom {
				img.Scale(1.03, 1.03)
			}
			if click != nil {
				img.OnClick(click)
			}
		})
		return
	}
	ui.Column(c).Width(w).Height(h).Center().Gradient(th.Surface, th.SurfaceHover, 160).Children(func() {
		ui.Text(c, mark(title)).FontSize(fontHero).Bold().TextColor(th.TextMuted)
	})
}

func (a *App) skeletonRow(c *ui.Context, th *ui.Theme, cols int, cellW, cellH float32) {
	if cols < 2 {
		cols = 5
	}
	tracks := make([]ui.Track, cols)
	for i := range tracks {
		tracks[i] = ui.Fixed(cellW)
	}
	ui.Column(c).Gap(16).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(16).Children(func() {
			for i := 0; i < cols; i++ {
				ui.Column(c).Width(cellW).Gap(8).Children(func() {
					ui.Box(c).Width(cellW).Height(cellH).Radius(8).Background(th.Surface)
					ui.Box(c).Width(cellW * 0.62).Height(12).Radius(4).Background(th.SurfaceHover)
				})
			}
		})
		a.loadingRow(c, th, "正在加载…")
	})
}

func (a *App) viewPager(c *ui.Context, th *ui.Theme) {
	if a.pageNum <= 1 && !a.page.HasNext() {
		return
	}
	label := fmt.Sprintf("第 %d 页", a.pageNum)
	if a.page.PageCount > 0 {
		label = fmt.Sprintf("第 %d / %d 页", a.pageNum, a.page.PageCount)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "上一页").Disabled(a.pageNum <= 1).Clicked() && a.pageNum > 1 {
			a.pageNum--
			a.loadList()
		}
		ui.Text(c, label).TextColor(th.TextMuted)
		if ui.Button(c, "下一页").Disabled(!a.page.HasNext()).Clicked() && a.page.HasNext() {
			a.pageNum++
			a.loadList()
		}
	})
}

func (a *App) viewDetail(c *ui.Context, th *ui.Theme) {
	if a.detailErr != "" {
		a.noteWithActions(c, th, a.detailErr, true, stateAction{label: "重试", primary: true, click: a.retryDetail})
	}
	d := a.detail
	title := a.detailTitle
	if d != nil && d.Title != "" {
		title = d.Title
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(22).Children(func() {
			ui.Row(c).Gap(32).AlignItems(ui.Start).Padding(8, 0, 6, 0).Children(func() {
				pic := ""
				if d != nil {
					pic = d.Pic
				}
				_, winH := c.Size()
				a.portrait(c, th, pic, title, portraitHeight(winH))
				ui.Column(c).Grow(1).Shrink(1).Gap(14).Padding(6, 0, 0, 0).Children(func() {
					ui.Text(c, title).FontSize(34).FontWeight(weightHeavy).Wrap()
					if d == nil {
						ui.Column(c).Gap(10).Children(func() {
							ui.Box(c).Width(240).Height(12).Radius(6).Background(th.SurfaceHover)
							ui.Box(c).Width(180).Height(12).Radius(6).Background(th.SurfaceHover)
							ui.Box(c).Width(208).Height(12).Radius(6).Background(th.SurfaceHover)
						})
						return
					}
					ui.Row(c).Gap(6).Wrap().Children(func() {
						if d.Remarks != "" {
							metaPill(c, th, d.Remarks, true)
						}
						for _, m := range []string{d.Year, d.Area, d.TypeName} {
							if strings.TrimSpace(m) != "" {
								metaPill(c, th, strings.TrimSpace(m), false)
							}
						}
					})
					who := joinMeta(prefix("导演 ", d.Director), prefix("主演 ", d.Actors))
					if who != "" {
						ui.Text(c, who).FontSize(fontCaption).TextColor(th.TextMuted).Wrap()
					}
					if d.Content != "" {
						body := ui.Text(c, d.Content).FontSize(fontBody).LineHeight(1.6).TextColor(th.Text.Alpha(0.78)).MaxWidth(760).Wrap()
						long := utf8.RuneCountInString(d.Content) > 160 || strings.Count(d.Content, "\n") >= 5
						if !a.synopsisOpen {
							body.MaxLines(5)
						}
						if long {
							label := "展开全文"
							if a.synopsisOpen {
								label = "收起"
							}
							if ui.Button(c, label).Clicked() {
								a.synopsisOpen = !a.synopsisOpen
							}
						}
					}
					ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(8, 0, 0, 0).Children(func() {
						busy := a.offersBusy()
						if a.linePick >= 0 && a.linePick < len(d.Lines) && len(d.Lines[a.linePick].Episodes) > 0 {
							ep := d.Lines[a.linePick].Episodes[0]
							ln := d.Lines[a.linePick].Name
							label := "播放"
							if ep.Name != "" {
								label = "播放 " + ep.Name
							}
							if busy {
								busyPlayPill(c, th, "detail-play", label)
							} else if primaryPill(c, th, "detail-play", iconPlay, label).Clicked() {
								a.playEpisode(ln, ep)
							}
						}
						if busy {
							ui.Text(c, "检测完成后自动选出最佳片源").FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
						}
						on := a.lib != nil && a.lib.hasFav(mediaKey(a.siteKey, d.ID))
						tip, fg := "收藏", th.Text
						if on {
							tip, fg = "取消收藏", th.Accent
						}
						glassButton(c, th, "fav-detail", iconHeart, tip, 44, fg, func() {
							if a.lib == nil {
								return
							}
							a.lib.toggleFav(favItem{
								Key: mediaKey(a.siteKey, d.ID), SiteKey: a.siteKey, VodID: d.ID,
								Title: d.Title, Pic: d.Pic, Remarks: d.Remarks, Year: d.Year,
							})
						})
					})
				})
			})
			if d == nil {
				if a.detailLoading {
					msg := "正在读取影片详情…"
					if len(a.offers) > 0 {
						msg = "正在读取各片源的线路和清晰度…"
					}
					a.loadingRow(c, th, msg)
				} else if a.detailErr == "" {
					a.emptyState(c, th, "暂时没有详情", "返回列表，或换一个片源再试。",
						stateAction{label: "返回", primary: true, click: a.back})
				}
				if len(a.offers) > 0 {
					a.viewOffers(c, th)
				}
				return
			}
			if len(a.offers) > 0 {
				a.viewOffers(c, th)
			}
			if len(d.Lines) == 0 {
				if a.detailLoading {
					a.loadingRow(c, th, "正在读取影片详情…")
					return
				}
				a.emptyState(c, th, "没有剧集", "这个详情页没有返回可播放的地址。",
					stateAction{label: "返回", primary: true, click: a.back})
				return
			}
			if a.linePick >= len(d.Lines) {
				a.linePick = 0
			}
			if len(d.Lines) > 1 {
				ui.ScrollBoth(c).MaxHeight(52).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						for i, line := range d.Lines {
							i := i
							a.choice(c, th, "line:"+line.Name, lineLabel(line.Name, i), i == a.linePick, func() { a.linePick = i })
						}
					})
				})
			} else if !slugLine(d.Lines[0].Name) {
				ui.Text(c, d.Lines[0].Name).FontSize(fontCaption).Bold().TextColor(th.TextMuted)
			}
			line := d.Lines[a.linePick]
			if a.epLine != line.Name {
				a.epLine = line.Name
				a.epPage = 0
			}
			const epWindow = 30
			total := len(line.Episodes)
			pages := (total + epWindow - 1) / epWindow
			if pages < 1 {
				pages = 1
			}
			if a.epPage >= pages {
				a.epPage = 0
			}
			start := a.epPage * epWindow
			end := min(start+epWindow, total)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "剧集").FontSize(fontTitle).Bold()
				ui.Text(c, fmt.Sprintf("%d 集", total)).FontSize(fontCaption).TextColor(th.TextMuted)
			})
			if pages > 1 {
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for p := 0; p < pages; p++ {
						p := p
						from := p*epWindow + 1
						to := min((p+1)*epWindow, total)
						label := fmt.Sprintf("%d–%d", from, to)
						a.choice(c, th, "ep-page:"+line.Name+":"+label, label, p == a.epPage, func() { a.epPage = p })
					}
				})
			}
			shown := line.Episodes[start:end]
			tracks := a.episodeTracks(c, longestEpisode(shown))
			ui.Grid(c).ColumnTracks(tracks...).Gap(8).Children(func() {
				for _, ep := range shown {
					ep := ep
					label := ep.Name
					if label == "" {
						label = "播放"
					}
					key := line.Name + "\n" + ep.URL + "\n" + label
					if a.offersBusy() {
						busyChoice(c, th, key, label)
						continue
					}
					on := a.playLine == line.Name && label == currentEpisodeName(a)
					a.choice(c, th, key, label, on, func() {
						a.playEpisode(line.Name, ep)
					})
				}
			})
		})
	})
}

// portrait is the detail page's poster frame. Many CMS sites hand out
// landscape covers: instead of cropping those to the middle of a 2:3
// poster, the frame takes the picture's own shape once it has loaded —
// the same area, widened — so the whole cover shows.
func (a *App) portrait(c *ui.Context, th *ui.Theme, pic, title string, h float32) {
	pic = model.ResolveURL("", pic)
	w, ht := h*2/3, h
	if bmp := a.cachedPoster(pic); bmp != nil {
		if bw, bh := bmp.Size(); bw > 0 && bh > 0 {
			aspect := float32(bw) / float32(bh)
			if aspect > 16.0/9.0 {
				aspect = 16.0 / 9.0
			}
			if aspect > 2.0/3.0 {
				w = float32(math.Sqrt(float64(h * h * 2 / 3 * aspect)))
				ht = w / aspect
			}
		}
	}
	frame := ui.Column(c).Width(w).Height(ht).Radius(14).Clip().Background(th.Surface).
		Border(1, th.Border).Shadow(0, 24, 48, -12, ui.RGBA(0, 0, 0, 0.55))
	frame.Children(func() {
		a.coverFit(c, th, pic, title, w, ht, nil, false, ui.Contain)
	})
}

func (a *App) viewLive(c *ui.Context, th *ui.Theme) {
	if a.liveLoading && len(a.channels) == 0 {
		_, cellW, cellH, _ := a.posterGrid(c)
		a.skeletonRow(c, th, 4, cellW, cellH)
		return
	}
	if a.liveErr != "" {
		a.noteWithActions(c, th, a.liveErr, true, stateAction{label: "重试", primary: true, click: a.retryLive})
		return
	}
	chs := a.visibleChannels()
	if len(chs) == 0 {
		if strings.TrimSpace(a.query) != "" && len(a.channels) > 0 {
			a.emptyState(c, th, "没有频道符合「"+strings.TrimSpace(a.query)+"」", "清空搜索框可以看到全部频道。",
				stateAction{label: "清空搜索", primary: true, click: func() {
					a.query = ""
					a.committed = ""
				}})
			return
		}
		a.emptyState(c, th, "没有频道", "这份播放列表是空的。",
			stateAction{label: "重新加载列表", primary: true, click: a.retryLive},
			stateAction{label: "管理订阅", click: a.openSubs})
		return
	}
	groups := liveGroups(chs)
	shown := chs
	if a.liveGroup != "" {
		shown = nil
		for _, ch := range chs {
			if liveGroupName(ch) == a.liveGroup {
				shown = append(shown, ch)
			}
		}
	}
	base := ""
	if a.catalog != nil && a.liveIndex >= 0 && a.liveIndex < len(a.catalog.Lives) {
		if entry := a.catalog.Lives[a.liveIndex]; entry != nil {
			base = entry.URL
		}
	}
	ui.Row(c).Grow(1).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(168).Gap(8).Children(func() {
			ui.Text(c, "分组").FontSize(fontCaption).TextColor(th.TextMuted)
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					a.choice(c, th, "grp-all", "全部", a.liveGroup == "", func() { a.liveGroup = "" })
					for _, g := range groups {
						g := g
						a.choice(c, th, "grp-"+g, g, a.liveGroup == g, func() { a.liveGroup = g })
					}
				})
			})
		})
		playing := a.liveStageOpen()
		chCol := ui.Column(c)
		if playing {
			chCol = chCol.Width(300)
		} else {
			chCol = chCol.Grow(1)
		}
		chCol.Children(func() {
			ui.Text(c, fmt.Sprintf("%d 个频道", len(shown))).FontSize(fontCaption).TextColor(th.TextMuted)
			ui.List(c, &a.chState, len(shown), func(i int) {
				ch := shown[i]
				onAir := a.fromLive && ch.Name == a.playTitle
				row := ui.Row(c).FillWidth().Padding(8, 12).Radius(8).Gap(12).AlignItems(ui.Center).
					OnClick(func() { a.playChannel(ch) }).Cursor(ui.CursorPointer)
				bg := ui.Transparent
				switch {
				case onAir:
					bg = th.Accent.Alpha(0.16)
				case row.Hovered():
					bg = th.SurfaceHover
				}
				row.Background(bg).Children(func() {
					a.liveLogo(c, th, ch, base)
					ui.Text(c, ch.Name).Grow(1).SingleLine()
					a.iconBtn(c, "ch:"+ch.Group+"/"+ch.Name+"/"+ch.URL, iconPlay, "播放", 44, 20, th.Text, func() { a.playChannel(ch) })
				})
			}).Grow(1)
		})
		if playing {
			ui.Column(c).Grow(1).Children(func() {
				a.paintStage(c, th, true)
			})
		}
	})
}

func (a *App) liveLogo(c *ui.Context, th *ui.Theme, ch live.Channel, base string) {
	frame := ui.Column(c).Size(36, 36).Radius(8).Background(th.Surface).Center().Clip()
	if logo := model.ResolveURL(base, ch.Logo); logo != "" {
		if bmp := a.poster(logo); bmp != nil {
			frame.Children(func() {
				ui.Image(c, bmp).Size(30, 30).Fit(ui.Contain)
			})
			return
		}
	}
	frame.Children(func() {
		ui.Text(c, mark(ch.Name)).FontSize(fontBody).Bold().TextColor(th.Text)
	})
}

func (a *App) viewUnsupported(c *ui.Context, th *ui.Theme) {
	m, ok := a.currentMeta()
	if !ok {
		a.emptyState(c, th, "没有选中的站点", "",
			stateAction{label: "回到首页", primary: true, click: a.goHome})
		return
	}
	ui.Column(c).Grow(1).Center().Children(func() {
		ui.Column(c).Width(460).Gap(12).Children(func() {
			ui.Box(c).Width(28).Height(2).Radius(1).Background(th.Accent)
			ui.Text(c, m.Name).FontSize(fontHero).Bold()
			ui.Text(c, config.KindLabel(m.Kind)).FontSize(fontCaption).TextColor(th.TextMuted)
			reason := m.Reason
			if reason == "" {
				reason = "这个站点本版不支持。"
			}
			ui.Text(c, reason).Wrap()
			if m.Kind == config.KindJar {
				ui.Text(c, "QuickBo 不执行 jar，也不会去加载 DEX。换一个 CMS、JS 或 XBPQ 源即可。").TextColor(th.TextMuted).Wrap()
			}
			if m.API != "" {
				ui.Text(c, m.API).FontSize(fontCaption).TextColor(th.TextMuted).Selectable().Wrap()
			}
			if m.Kind == config.KindRemote && (strings.HasPrefix(m.API, "http://") || strings.HasPrefix(m.API, "https://")) {
				if ui.Button(c, "当作配置打开").Clicked() {
					a.Load(m.API)
				}
			}
		})
	})
}

func (a *App) viewIndex(c *ui.Context, th *ui.Theme) {
	if a.loading {
		ui.Column(c).Gap(8).Padding(8, 0).Children(func() {
			msg := a.loadNote
			if msg == "" {
				msg = "正在批量加载配置…"
			}
			a.loadingRow(c, th, msg)
			ui.Text(c, "打不开，或第一页没有内容的源，不会出现在侧栏。").TextColor(th.TextMuted).Wrap()
		})
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Text(c, "这是一份仓库索引，还不是点播配置。").FontSize(fontHeadline).Bold()
			ui.Text(c, "选一个地址继续加载。").TextColor(th.TextMuted)
			if a.catalog == nil {
				return
			}
			ui.Column(c).Gap(8).Children(func() {
				for _, u := range a.catalog.Index {
					u := u
					name := u.Name
					if name == "" {
						name = u.URL
					}
					row := ui.Column(c.Key(u.URL)).FillWidth().Padding(14, 16).Gap(4).Radius(8).Background(th.Surface).OnClick(func() { a.Load(u.URL) }).Cursor(ui.CursorPointer)
					if row.Hovered() {
						row.Background(th.SurfaceHover)
					}
					row.Children(func() {
						ui.Text(c, name).Bold()
						ui.Text(c, u.URL).FontSize(fontCaption).TextColor(th.TextMuted).Selectable()
					})
				}
			})
		})
	})
}

// choice is a chip that picks one of several (a source, a line, an
// episode). The chosen one is tinted and outlined with the accent rather
// than filled, leaving the filled look to the screen's primary action.
func (a *App) choice(c *ui.Context, th *ui.Theme, key, label string, on bool, click func()) {
	p := ui.Column(c.Key(key)).Padding(9, 14).Radius(10).OnClick(click).Cursor(ui.CursorPointer)
	// A long label, as a variety show's episode, may be cut to fit: the
	// pointer resting on it shows it whole.
	if utf8.RuneCountInString(label) > choiceTipRunes {
		p.Tooltip(label)
	}
	bg, fg := selectionColors(th, on, p.Hovered())
	edge := th.Border
	if on {
		edge = th.Accent.Alpha(0.55)
	}
	p.Background(p.AnimateColor("bg", bg, colorEase)).Border(1, p.AnimateColor("edge", edge, colorEase))
	pressPose(p, 0.96)
	p.Children(func() {
		w := 400
		if on {
			w = weightStrong
		}
		ui.Text(c, label).FontSize(fontBody).FontWeight(w).TextColor(fg).SingleLine()
	})
}

// stageChoice is a choice on the player's near-black panels. Those panels
// stay dark in either appearance, so the label stays light instead of
// following the window theme.
func (a *App) stageChoice(c *ui.Context, th *ui.Theme, key, label string, on bool, click func()) {
	p := ui.Column(c.Key(key)).Padding(9, 14).Radius(10).OnClick(click).Cursor(ui.CursorPointer)
	if utf8.RuneCountInString(label) > choiceTipRunes {
		p.Tooltip(label)
	}
	ink := th.AccentText
	bg, fg := ink.Alpha(0.12), ink
	edge := ink.Alpha(0.22)
	switch {
	case on:
		bg, fg, edge = th.Accent, th.AccentText, th.Accent
	case p.Hovered():
		bg = ink.Alpha(0.2)
	}
	p.Background(p.AnimateColor("bg", bg, colorEase)).Border(1, p.AnimateColor("edge", edge, colorEase))
	pressPose(p, 0.96)
	p.Children(func() {
		w := 400
		if on {
			w = weightStrong
		}
		ui.Text(c, label).FontSize(fontBody).FontWeight(w).TextColor(fg).SingleLine()
	})
}

// stageButton is a text button on those same dark panels.
func stageButton(c *ui.Context, th *ui.Theme, label string) ui.Element {
	b := ui.ButtonBase(c).Height(36).PaddingX(14).Radius(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(label)
	ink := th.AccentText
	bg := ink.Alpha(0.12)
	switch {
	case b.Pressed():
		bg = ink.Alpha(0.24)
	case b.Hovered():
		bg = ink.Alpha(0.18)
	}
	b.Background(b.AnimateColor("bg", bg, colorEase)).TextColor(ink)
	pressPose(b, 0.96)
	b.Children(func() {
		ui.Text(c, label).FontSize(fontBody).TextColor(ink).SingleLine()
	})
	return b
}

// deadChoice is a choice that cannot be picked: greyed, marked 无法播放,
// with why in its tooltip.
func (a *App) deadChoice(c *ui.Context, th *ui.Theme, key, label, reason string) {
	tip := "无法播放"
	if reason != "" {
		tip += "：" + reason
	}
	ui.Row(c.Key(key)).Padding(10, 12).Radius(8).Gap(6).AlignItems(ui.Center).
		Background(th.SurfaceHover.Alpha(0.45)).Cursor(ui.CursorNotAllowed).Tooltip(tip).Label(label + "，" + tip).
		Children(func() {
			ui.Text(c, label).FontSize(fontBody).TextColor(th.TextMuted.Alpha(0.6)).SingleLine()
			ui.Text(c, "无法播放").FontSize(fontCaption).TextColor(th.Danger.Alpha(0.75)).SingleLine()
		})
}

type stateAction struct {
	label   string
	primary bool
	click   func()
}

func (a *App) actionRow(c *ui.Context, actions []stateAction) {
	if len(actions) == 0 {
		return
	}
	ui.Row(c).Gap(8).Children(func() {
		for _, action := range actions {
			action := action
			var btn ui.Element
			if action.primary {
				btn = primaryPill(c, c.Theme(), "action:"+action.label, nil, action.label)
			} else {
				btn = ui.Button(c, action.label)
			}
			if btn.Clicked() && action.click != nil {
				action.click()
			}
		}
	})
}

func (a *App) note(c *ui.Context, th *ui.Theme, text string, bad bool) {
	a.noteWithActions(c, th, text, bad)
}

func (a *App) noteWithActions(c *ui.Context, th *ui.Theme, text string, bad bool, actions ...stateAction) {
	bg := th.Surface
	fg := th.TextMuted
	if bad {
		bg = th.Danger.Alpha(0.14)
		fg = th.Danger
	}
	ui.Column(c).FillWidth().Padding(12, 14).Radius(8).Background(bg).Gap(10).Children(func() {
		ui.Text(c, text).TextColor(fg).Wrap()
		a.actionRow(c, actions)
	})
}

func (a *App) emptyState(c *ui.Context, th *ui.Theme, title, body string, actions ...stateAction) {
	ui.Column(c).FillWidth().Padding(36, 0).Gap(10).Children(func() {
		ui.Box(c).Width(28).Height(2).Radius(1).Background(th.Accent)
		ui.Text(c, title).FontSize(fontHeadline).Bold()
		if body != "" {
			ui.Text(c, body).TextColor(th.TextMuted).Wrap()
		}
		a.actionRow(c, actions)
	})
}

func (a *App) retrySubs() {
	a.loadErr = ""
	if len(a.activeRefs()) == 0 {
		a.openSubs()
		return
	}
	a.LoadSaved()
}

func (a *App) retryList() {
	a.listErr = ""
	a.loadList()
}

func (a *App) retryDetail() {
	if a.detailItem.ID == "" {
		return
	}
	a.openDetail(a.detailItem)
}

func (a *App) retryLive() {
	if a.catalog == nil || a.liveIndex < 0 || a.liveIndex >= len(a.catalog.Lives) {
		a.openSubs()
		return
	}
	a.selectLive(a.liveIndex)
}

func (a *App) retryPlay() {
	if strings.TrimSpace(a.playURL) == "" {
		a.openSubs()
		return
	}
	a.playErr = ""
	a.startPlay(a.playTitle, a.playURL, a.playHeaders, a.fromLive)
}

func (a *App) goHome() {
	a.closePlayer()
	a.screen = screenHome
	a.sideSel = "home"
	a.lastSide = "home"
	a.query = ""
	a.committed = ""
	a.homeHits = nil
	a.homeSearching = false
}

func (a *App) loadingRow(c *ui.Context, th *ui.Theme, text string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c)
		ui.Text(c, text).TextColor(th.TextMuted)
	})
}

func continueMeta(w watchItem) string {
	var parts []string
	if ep := strings.TrimSpace(w.Episode); ep != "" {
		parts = append(parts, ep)
	}
	if w.Duration > 0 && w.Position >= 0 && w.Duration > w.Position {
		parts = append(parts, "剩余 "+clock(w.Duration-w.Position))
	}
	return strings.Join(parts, " · ")
}

func joinMeta(parts ...string) string {
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "   ·   ")
}

func (a *App) viewHistory(c *ui.Context, th *ui.Theme) {
	items := a.lib.watches()
	if len(items) == 0 {
		a.emptyState(c, th, "还没有历史", "播放过的片子会出现在这里，并记住进度。",
			stateAction{label: "去首页", primary: true, click: a.goHome})
		return
	}
	_, cellW, cellH, tracks := a.posterGrid(c)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(16).Children(func() {
			for _, w := range items {
				w := w
				card := ui.Column(c.Key("hist-" + w.Key)).Width(cellW).Gap(8).OnClick(func() {
					a.playHistory(w)
				}).Cursor(ui.CursorPointer)
				hot := card.Hovered()
				card.Children(func() {
					frame := ui.Column(c.Key("history-frame:" + w.Key)).Width(cellW).Height(cellH).Radius(8).Clip().Background(th.Surface)
					a.animateCard(card, frame, hot, a.light)
					frame.Children(func() {
						a.cover(c, th, w.Pic, w.Title, cellW, cellH, nil, hot)
						if hot {
							a.removeBadge(c, th, "remove-history:"+w.Key, "删除历史", func() {
								a.lib.removeWatch(w.Key)
							})
						}
					})
					ui.Text(c, w.Title).FontSize(fontBody).SingleLine()
					sub := w.Episode
					if line := displayLine(w.Line); line != "" && sub != "" {
						sub = line + "  ·  " + sub
					}
					ui.Text(c, sub).FontSize(fontCaption).TextColor(th.TextMuted).SingleLine()
					frac := float32(0)
					if w.Duration > 0 {
						frac = float32(w.Position / w.Duration)
					}
					ui.Box(c).Width(cellW).Height(3).Radius(2).Background(th.SurfaceHover).Children(func() {
						ui.Box(c).Height(3).WidthPercent(frac * 100).Background(th.Accent)
					})
				})
			}
		})
	})
}

func (a *App) viewFavs(c *ui.Context, th *ui.Theme) {
	items := a.lib.favs()
	if len(items) == 0 {
		a.emptyState(c, th, "还没有收藏", "在详情页点心形图标，片子会留在这里。",
			stateAction{label: "去首页", primary: true, click: a.goHome})
		return
	}
	_, cellW, cellH, tracks := a.posterGrid(c)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Grid(c).ColumnTracks(tracks...).Gap(16).Children(func() {
			for _, f := range items {
				f := f
				a.posterCardWithRemove(c, th, f.SiteKey,
					model.Item{ID: f.VodID, Title: f.Title, Pic: f.Pic, Remarks: f.Remarks, Year: f.Year},
					cellW, cellH, func() { a.lib.removeFav(f.Key) })
			}
		})
	})
}

func currentEpisodeName(a *App) string {
	if a.detail == nil {
		return ""
	}
	for _, ln := range a.detail.Lines {
		if ln.Name == a.playLine && a.playIndex >= 0 && a.playIndex < len(ln.Episodes) {
			n := ln.Episodes[a.playIndex].Name
			if n == "" {
				return "播放"
			}
			return n
		}
	}
	return a.resumeEpisode
}

func prefix(p, v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return p + v
}

func liveGroupName(ch live.Channel) string {
	if strings.TrimSpace(ch.Group) == "" {
		return "未分组"
	}
	return ch.Group
}

func liveGroups(chs []live.Channel) []string {
	var out []string
	seen := map[string]bool{}
	for _, ch := range chs {
		g := liveGroupName(ch)
		if seen[g] {
			continue
		}
		seen[g] = true
		out = append(out, g)
	}
	return out
}

func slugLine(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || len([]rune(n)) > 16 {
		return false
	}
	for _, r := range n {
		if r > 127 || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-') {
			return false
		}
	}
	return true
}

func lineLabel(name string, index int) string {
	if slugLine(name) {
		return fmt.Sprintf("线路 %d", index+1)
	}
	n := strings.TrimSpace(name)
	if n == "" {
		return fmt.Sprintf("线路 %d", index+1)
	}
	return n
}

func displayLine(name string) string {
	if slugLine(name) {
		return ""
	}
	return strings.TrimSpace(name)
}

func mark(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "·"
	}
	r := []rune(title)
	if len(r) > 1 {
		return string(r[:1])
	}
	return string(r)
}
