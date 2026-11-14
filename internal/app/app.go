// Package app is the native shell: subscription loading, source browsing
// and playback. It draws with mygo's GPU UI and does not embed a webview.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/harrylyx/QuickBo/internal/config"
	"github.com/harrylyx/QuickBo/internal/httpx"
	"github.com/harrylyx/QuickBo/internal/live"
	"github.com/harrylyx/QuickBo/internal/model"
	"github.com/harrylyx/QuickBo/internal/playback"
	"github.com/harrylyx/QuickBo/internal/player"
	"github.com/harrylyx/QuickBo/internal/source"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

const (
	screenWelcome     = "welcome"
	screenIndex       = "index"
	screenBrowse      = "browse"
	screenDetail      = "detail"
	screenPlay        = "play"
	screenLive        = "live"
	screenUnsupported = "unsupported"
	screenEmpty       = "empty"
	screenHome        = "home"
	screenHistory     = "history"
	screenFav         = "fav"
	screenSources     = "sources"
	screenRules       = "rules"
	screenSubs        = "subs"
	screenBlacklist   = "blacklist"
	screenMore        = "more"
	screenCats        = "cats"
	screenLiveAgg     = "liveagg"
)

// App is the window state. Methods that change it run on the UI thread,
// except the goroutines that publish results through Window.Update.
type App struct {
	win *mygo.Window

	cfg     *config.Config
	catalog *source.Catalog
	// cfgRef is the cache identity of the current load: a single ref, or the
	// newline-joined enabled refs when several subscriptions are active.
	cfgRef       string
	subs         []subscription
	groupOf      map[string]string
	loading      bool
	loadErr      string
	loadNote     string
	adding       bool
	settingsOpen bool
	// cacheConfirm names the storage row whose clear is one click from
	// done: a clear asks twice, as deleting subscriptions does.
	cacheConfirm   string
	updates        bool // the update plugin is used (UseUpdater)
	welcomeEntered bool
	urlDraft       string

	loadCtx    context.Context
	loadCancel context.CancelFunc
	// fetchConfig overrides subscription loading in tests.
	fetchConfig func(context.Context, string) (*config.Config, error)
	// probeSite overrides the empty-list check in tests.
	probeSite func(context.Context, source.Source) (model.Page, bool)
	// revealDir overrides opening the log directory in tests, so a click
	// does not launch the desktop's file manager.
	revealDir func(string)
	afterLoad func()
	postMu    sync.Mutex

	screen    string
	sideSel   string
	lastSide  string
	siteKey   string
	cats      []model.Category
	catID     string
	page      model.Page
	pageNum   int
	query     string
	committed string
	searchDue time.Time // when a typed query should commit, after a short pause
	listErr   string
	listLoad  bool

	detail        *model.Detail
	detailTitle   string
	detailItem    model.Item
	detailErr     string
	detailLoading bool
	synopsisOpen  bool   // the detail blurb is expanded past five lines
	epPage        int    // which 30-episode window the detail grid shows
	epLine        string // the line that epPage belongs to

	playTitle   string
	playURL     string
	playHeaders map[string]string
	playErr     string
	playNote    string
	playLoading bool
	player      player.Player
	pb          *playback.Controller
	fromLive    bool

	channels    []live.Channel
	liveIndex   int
	liveGroup   string
	liveErr     string
	liveLoading bool
	liveHealth  map[string]liveHealthState
	liveTries   int
	// liveCurrent is the channel the line walk is trying, so a manual retry
	// can start the walk over after every line has failed.
	liveCurrent live.Channel
	// livePlayGen drops a stream probe once another channel has been
	// opened, or the aggregate page has left that channel.
	livePlayGen int

	parseMu   sync.Mutex
	parseRank map[string]parseRankEntry

	preferExternal bool
	light          bool
	// appearance is the chosen look: appearanceSystem, appearanceLight or
	// appearanceDark. light is what it resolves to in the current frame.
	appearance    string
	volume        float64
	rate          float64
	linePick      int
	playLine      string
	playIndex     int
	resumeAt      float64
	showDrawer    bool
	controlsUntil time.Time
	ptrX, ptrY    float32
	ptrSeen       bool
	savedVolume   float64
	appliedVolume float64
	fullscreen    bool
	// cfgCache is the config cache (configcache.go), read from disk on
	// first use; fetch goroutines share it under cfgCacheMu.
	cfgCacheMu sync.Mutex
	cfgCache   map[string]configEntry
	// videoBM shows the frames of an embedded player (player.VideoSource);
	// videoShown is set once it holds a frame of the current session.
	// heroIdx is the home banner's title, heroPrev the one it crossfades
	// from, since heroAt.
	heroIdx, heroPrev int
	heroAt            time.Time
	heroPaused        bool
	railScroll        map[string]*ui.ScrollState
	videoBM           *ui.Bitmap
	videoShown        bool
	spaceDownAt       time.Time
	spaceBoosted      bool
	// px is the player's extra state: notices, picture-in-picture, the
	// settings panel, binge-watching and per-title skips (player_extras.go).
	px             playerExtras
	bufferingSince time.Time
	keepScreen     bool
	lib            *library

	// Subscription and source management.
	subSort        string                // 订阅排序键：name/ref/enabled/count
	subFilter      string                // 订阅列表搜索关键字
	subSel         map[string]bool       // 批量选中的订阅 ref
	subBatch       bool                  // 订阅批量管理模式
	subConfirm     []string              // refs waiting on a batch-delete confirmation
	srcFilter      string                // 片源管理搜索关键字
	srcSort        string                // 片源排序键：srcSort* 常量
	srcHealth      string                // 片源健康度筛选：srcShow* 常量
	srcView        string                // 片源分组视图：srcGroup* 常量
	srcSel         map[string]bool       // 批量选中的片源 key
	srcBatch       bool                  // 片源批量管理模式
	srcList        ui.ListState          // 片源管理的虚拟列表滚动状态
	srcCache       *srcRows              // 片源管理排序/筛选/分组缓存（按输入指纹失效）
	srcActive      map[string]bool       // activeSiteKeys 缓存
	srcActiveKey   srcActiveKey          // activeSiteKeys 缓存的键
	disabled       map[string]bool       // 停用的片源（sourceHideID），保留在管理列表但不下发
	allSites       []source.Source       // 订阅里的全部片源（含停用/不支持/未通过检测）
	persistedStats map[string]sourceStat // 从设置恢复的测速结果（按 sourceHideID）
	probingActive  bool                  // 批量测速进行中
	probingDone    int                   // 批量测速已完成数量
	probingTotal   int                   // 批量测速总数
	blacklist      []string              // 片源黑名单关键字
	blacklistDraft string                // 黑名单关键字输入框
	manageLast     string                // 管理页上次打开的标签（screenSubs 等）
	blFilter       string                // 黑名单搜索关键字
	blSel          map[string]bool       // 批量选中的黑名单关键字（小写）
	sidebarOpen    map[string]*bool      // 侧边栏分组的展开状态（按分组名）

	browseScroll ui.ScrollState
	appending    bool
	listAnchored bool
	// The aggregate page behind a home row's 查看更多 (more_page.go): the
	// merged row, its title lookup (so opening a title uses every source that
	// lists it), and a sequence number that drops stale loads.
	moreRow     homeLane
	moreLookup  map[string][]titleHit
	moreLoading bool
	moreSeq     int
	// moreKeys is the shown title order; moreBallots every item read so far;
	// moreCursors where each matching category has been read up to.
	moreKeys      []string
	moreBallots   []ballot
	moreCursors   []moreCursor
	moreAppending bool
	// playFrom and detailFrom are the screens the player and the detail page
	// were opened from, where back returns (backTarget).
	playFrom   string
	detailFrom string
	// resumeEpisode names the episode a history entry resumes, until its
	// detail arrives and playIndex can name it.
	resumeEpisode string
	// historyToken drops a detail fetch when a newer resume has started.
	historyToken int

	grid     ui.GridState
	selected int
	chState  ui.ListState

	posters    map[string]*ui.Bitmap
	posterUsed map[string]int64
	posterTick int64
	art        map[string]artExtras
	fetching   map[string]bool
	failed     map[string]bool
	// posterTries counts network failures per poster; a slow or busy image
	// host gets posterRetries more tries before the poster counts as failed.
	posterTries map[string]int
	// coverLaid marks covers (per screen) that have been laid out once, so
	// their InViewMargin answer is known (see cover).
	coverLaid  map[string]bool
	hidden     map[string]bool
	homeRules  []homeRule
	ruleNames  []string
	ruleWords  []string
	ruleFocus  []bool
	ruleSeq    int
	rulesDirty bool
	rulesNote  string // 分类导入导出的结果提示
	// Dragging a home row by its handle (view_rules.go): the row, where it
	// started, how far it has moved, whether it was let go, the row pitch
	// measured last frame, and the drop to settle the frame after.
	ruleDragID    string
	ruleDragFrom  int
	ruleDragDY    float32
	ruleDropped   bool
	rulePitch     float32
	ruleSettle    *ruleSettle
	home          []homeLane
	homeHits      []homeLane
	homeSearching bool
	// homeGen invalidates a background tally when newer facts or rules arrive.
	homeGen        int
	asyncHomeTally bool // test hook: exercise the windowed, async tally path
	ballots        []ballot
	catFacts       []catFact
	aggExtra       *homeRule
	moreFrom       string
	// The aggregated live directory (live_agg.go): one entry per channel
	// name, the name currently open, and the lines failover may walk.
	liveAggs       []liveAgg
	liveAggName    string
	liveAggLines   []live.Channel
	liveAggLoading bool
	liveAggMiss    int
	liveAggToken   int
	liveLineAt     int
	liveWatchAt    time.Time
	lookup         map[string][]titleHit
	searchLookup   map[string][]titleHit
	offers         []playOffer
	offerPick      int
	// offersScanning is true from openTitle until the cross-source check has
	// published its final pick: the detail shown before that is provisional.
	offersScanning bool
	stats          map[string]sourceStat

	gen int
	// offerGen identifies the current title's source probe (loadOffers). It
	// changes only when another title is opened, so playing an episode or
	// coming back from the player does not cancel the probe.
	offerGen     int
	settingsPath string
}

type savedSettings struct {
	Config         string         `json:"config,omitempty"`
	Subs           []subscription `json:"subs,omitempty"`
	PreferExternal bool           `json:"preferExternal"`
	// Light is the old two-way switch, read when Appearance is missing.
	Light           bool                  `json:"light,omitempty"`
	Appearance      string                `json:"appearance,omitempty"`
	Volume          *float64              `json:"volume,omitempty"`
	Hidden          []string              `json:"hidden,omitempty"`
	Blacklist       []string              `json:"blacklist,omitempty"`
	DisabledSources []string              `json:"disabledSources,omitempty"`
	SourceStats     map[string]sourceStat `json:"sourceStats,omitempty"`
	HomeRules       []homeRule            `json:"homeRules,omitempty"`
	// AutoNext plays the next episode at the end; nil means on.
	AutoNext *bool `json:"autoNext,omitempty"`
	// Skips are the intro and outro to skip, per title (watchTitle).
	Skips map[string]skipRule `json:"skips,omitempty"`
}

// New returns an empty shell. No subscription is loaded until the user
// supplies one, or a previously saved address is restored.
func New() *App {
	a := &App{
		screen:        screenWelcome,
		pb:            playback.New(),
		volume:        1,
		px:            playerExtras{autoNext: true},
		savedVolume:   1,
		appliedVolume: 1,
		rate:          1,
		lib:           loadLibrary(filepath.Dir(settingsPath())),
		selected:      -1,
		posters:       map[string]*ui.Bitmap{},
		art:           map[string]artExtras{},
		fetching:      map[string]bool{},
		failed:        map[string]bool{},
		hidden:        map[string]bool{},
		homeRules:     defaultHomeRules(),
		lookup:        map[string][]titleHit{},
		stats:         map[string]sourceStat{},
		settingsPath:  settingsPath(),
	}
	a.grid.Selected = &a.selected
	a.grid.Key = func(i int) any {
		if i < 0 || i >= len(a.page.Items) {
			return i
		}
		if a.page.Items[i].ID != "" {
			return a.page.Items[i].ID
		}
		return i
	}
	a.grid.Label = func(i int) string {
		if i < 0 || i >= len(a.page.Items) {
			return ""
		}
		return a.page.Items[i].Title
	}
	a.chState.Label = func(i int) string {
		chs := a.visibleChannels()
		if i < 0 || i >= len(chs) {
			return ""
		}
		return chs[i].Name
	}
	return a
}

// Attach binds the window that receives async updates.
func (a *App) Attach(win *mygo.Window) { a.win = win }

// Load fetches one subscription from a URL or a local path. The subscription
// list uses LoadSubs to load several at once.
func (a *App) Load(ref string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		a.loadErr = "请输入配置地址"
		return
	}
	a.groupOf = nil
	a.loading = true
	a.loadErr = ""
	a.loadNote = ""
	ctx, _ := a.beginLoad()
	go func() {
		cctx, cancel := context.WithTimeout(withUserRefs(ctx, []string{ref}), 30*time.Second)
		defer cancel()
		cfg, err := a.fetch(cctx, ref)
		a.post(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.loading = false
				a.loadErr = err.Error()
				a.doneLoad()
				return
			}
			a.apply(cfg, ref)
		})
	}()
}

// RestoreSettings applies saved preferences (player choice, theme, volume,
// hidden sources, homepage rules and the subscription list) without loading
// anything. It returns the enabled refs. It must run on every start, including
// starts with an explicit --config, otherwise saved homepage rules and the
// other subscriptions silently fall back to defaults.
func (a *App) RestoreSettings() []string {
	b, err := os.ReadFile(a.settingsPath)
	if err != nil {
		return nil
	}
	var s savedSettings
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	a.preferExternal = s.PreferExternal
	a.appearance = s.Appearance
	switch {
	case validAppearance(a.appearance):
	case s.Light:
		a.appearance = appearanceLight
	default:
		// Settings from before the system option: they were dark.
		a.appearance = appearanceDark
	}
	a.light = a.appearance == appearanceLight
	a.hidden = map[string]bool{}
	for _, id := range s.Hidden {
		if id != "" {
			a.hidden[id] = true
		}
	}
	a.blacklist = nil
	for _, kw := range s.Blacklist {
		if kw = strings.TrimSpace(kw); kw != "" {
			a.blacklist = append(a.blacklist, kw)
		}
	}
	a.disabled = map[string]bool{}
	for _, id := range s.DisabledSources {
		if id = strings.TrimSpace(id); id != "" {
			a.disabled[id] = true
		}
	}
	a.persistedStats = nil
	if len(s.SourceStats) > 0 {
		a.persistedStats = map[string]sourceStat{}
		for id, st := range s.SourceStats {
			a.persistedStats[id] = st
		}
	}
	if len(s.HomeRules) > 0 {
		a.homeRules = normalizeRules(s.HomeRules)
		a.resetRuleEdits()
		a.noteRuleSeq()
	}
	a.px.autoNext = s.AutoNext == nil || *s.AutoNext
	a.px.skips = cloneSkips(s.Skips)
	if s.Volume != nil {
		a.volume = *s.Volume
		a.savedVolume = a.volume
		a.appliedVolume = a.volume
	}
	a.subs = nil
	for _, sub := range s.Subs {
		ref := strings.TrimSpace(sub.Ref)
		if ref == "" {
			continue
		}
		sub.Ref = ref
		if sub.Name == "" {
			sub.Name = shortRef(ref)
		}
		a.subs = append(a.subs, sub)
	}
	if len(a.subs) == 0 {
		// Settings written before the subscription list only had Config.
		if ref := strings.TrimSpace(s.Config); ref != "" {
			a.subs = []subscription{{Ref: ref, Name: shortRef(ref), Enabled: true}}
		}
	}
	return a.activeRefs()
}

// LoadSaved loads every enabled subscription. RestoreSettings must have run.
func (a *App) LoadSaved() {
	if refs := a.activeRefs(); len(refs) > 0 {
		a.LoadSubs(refs)
	}
}

func (a *App) apply(cfg *config.Config, ref string) {
	a.closePlayer()
	a.cfg = cfg
	a.cfgRef = ref
	a.catalog = source.Open(cfg)
	a.allSites = append([]source.Source(nil), a.catalog.Sites...)
	a.applyPersistedStats()
	// A different subscription set must not keep votes from the previous one.
	a.ballots = nil
	a.detail = nil
	a.page = model.Page{}
	a.query = ""
	a.saveSettings()
	if len(a.catalog.Sites) == 0 && len(a.catalog.Lives) == 0 {
		if len(a.catalog.Index) > 0 {
			a.screen = screenIndex
			a.sideSel = ""
			a.keepScreen = false
			a.expandIndex(a.catalog.Index)
			return
		}
		a.loading = false
		a.screen = screenEmpty
		a.doneLoad()
		return
	}
	full := a.catalog
	if c := a.readCache(ref); c != nil {
		a.restoreFromCache(c, full)
		return
	}
	a.catalog = &source.Catalog{Parses: full.Parses, Index: full.Index}
	a.keepWorking(a.loadCtxOrBackground(), full)
}

func (a *App) onSide() {
	if a.sideSel == a.lastSide {
		return
	}
	a.lastSide = a.sideSel
	switch {
	case a.sideSel == "cats":
		a.query = ""
		a.committed = ""
		a.screen = screenCats
		a.closePlayer()
	case a.sideSel == "liveagg":
		a.openLiveAgg()
	case a.sideSel == "home":
		a.query = ""
		a.committed = ""
		a.homeHits = nil
		a.homeSearching = false
		a.screen = screenHome
		a.closePlayer()
	case a.sideSel == "history":
		a.query = ""
		a.committed = ""
		a.screen = screenHistory
		a.closePlayer()
	case a.sideSel == "fav":
		a.query = ""
		a.committed = ""
		a.screen = screenFav
		a.closePlayer()
	case a.sideSel == "manage":
		a.goManage(a.manageLast)
	case a.sideSel == "sources":
		a.goManage(screenSources)
	case a.sideSel == "rules":
		a.goManage(screenRules)
	case strings.HasPrefix(a.sideSel, "site:"):
		a.selectSite(strings.TrimPrefix(a.sideSel, "site:"))
	case strings.HasPrefix(a.sideSel, "live:"):
		n, _ := strconv.Atoi(strings.TrimPrefix(a.sideSel, "live:"))
		a.selectLive(n)
	}
}

func (a *App) selectSite(key string) {
	src := a.catalog.Find(key)
	if src == nil {
		return
	}
	a.siteKey = key
	a.sideSel = "site:" + key
	a.lastSide = a.sideSel
	a.closePlayer()
	a.fromLive = false
	a.query = ""
	a.committed = ""
	a.page = model.Page{}
	a.pageNum = 1
	a.detail = nil
	if src.Meta().Level == config.LevelNo {
		a.screen = screenUnsupported
		return
	}
	a.screen = screenBrowse
	a.cats = nil
	a.catID = ""
	a.listErr = ""
	a.listLoad = true
	gen := a.bump()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cats, err := src.Categories(ctx)
		a.post(func() {
			if a.gen != gen || a.siteKey != key {
				return
			}
			if err != nil && len(cats) == 0 {
				a.listErr = err.Error()
				a.cats = []model.Category{{ID: "", Name: "最新"}}
				a.catID = ""
				a.loadList()
				return
			}
			if len(cats) == 0 {
				cats = []model.Category{{ID: "", Name: "全部"}}
			}
			a.cats = cats
			a.catID = cats[0].ID
			a.loadList()
		})
	}()
}

func (a *App) pickCat(id string) {
	a.catID = id
	a.pageNum = 1
	a.query = ""
	a.committed = ""
	a.loadList()
}

func (a *App) loadList() {
	src := a.current()
	if src == nil {
		return
	}
	a.listLoad = true
	a.listErr = ""
	a.appending = false
	a.page = model.Page{}
	a.selected = -1
	a.listAnchored = a.pageNum <= 1
	if a.pageNum <= 1 {
		a.browseScroll.Y = 0
	}
	cat, page, q := a.catID, a.pageNum, strings.TrimSpace(a.committed)
	key := a.siteKey
	gen := a.bump()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var (
			pg  model.Page
			err error
		)
		if q != "" {
			pg, err = src.Search(ctx, q, page)
		} else {
			pg, err = src.List(ctx, cat, page)
		}
		a.post(func() {
			if a.gen != gen || a.siteKey != key {
				return
			}
			a.listLoad = false
			a.page = pg
			if err != nil {
				a.listErr = err.Error()
			}
		})
	}()
}

func (a *App) loadMore() {
	src := a.current()
	if src == nil || a.listLoad || a.appending || !a.page.HasNext() {
		return
	}
	a.appending = true
	next := a.pageNum + 1
	if next < 1 {
		next = 2
	}
	cat, q := a.catID, strings.TrimSpace(a.committed)
	key := a.siteKey
	gen := a.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var (
			pg  model.Page
			err error
		)
		if q != "" {
			pg, err = src.Search(ctx, q, next)
		} else {
			pg, err = src.List(ctx, cat, next)
		}
		a.post(func() {
			if a.gen != gen || a.siteKey != key {
				a.appending = false
				return
			}
			a.appending = false
			if err != nil {
				a.listErr = err.Error()
				return
			}
			if len(pg.Items) == 0 {
				a.page.PageCount = a.pageNum
				return
			}
			a.pageNum = next
			a.page.Items = append(a.page.Items, pg.Items...)
			if pg.Page > 0 {
				a.page.Page = pg.Page
			}
			if pg.PageCount > 0 {
				a.page.PageCount = pg.PageCount
			}
			if pg.Total > 0 {
				a.page.Total = pg.Total
			}
		})
	}()
}

func (a *App) maybeLoadMore() {
	if a.listLoad || a.appending || !a.page.HasNext() {
		return
	}
	near := a.browseScroll.MaxY > 48 && a.browseScroll.Y >= a.browseScroll.MaxY-320
	short := a.browseScroll.MaxY <= 1 && len(a.page.Items) > 0 && len(a.page.Items) < 36
	if near || short {
		a.loadMore()
	}
}

func (a *App) openDetail(item model.Item) {
	src := a.current()
	if src == nil {
		return
	}
	a.noteDetailFrom()
	a.screen = screenDetail
	a.offers = nil
	a.offersScanning = false
	// A scan from a previously opened aggregate title must not publish its
	// stale pick over this detail.
	a.offerGen++
	a.detail = nil
	a.detailTitle = item.Title
	a.detailItem = item
	a.detailErr = ""
	a.detailLoading = true
	a.linePick = 0
	a.synopsisOpen = false
	a.epPage = 0
	a.epLine = ""
	a.closePlayer()
	id := item.ID
	key := a.siteKey
	gen := a.bump()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		d, err := src.Detail(ctx, id)
		a.post(func() {
			if a.gen != gen || a.siteKey != key {
				return
			}
			a.detailLoading = false
			if err != nil {
				a.detailErr = err.Error()
				return
			}
			if d.Title == "" {
				d.Title = item.Title
			}
			if d.Pic == "" {
				d.Pic = item.Pic
			}
			a.detail = &d
			a.linePick = model.PreferredLine(d.Lines)
		})
	}()
}

func (a *App) playEpisode(line string, ep model.Episode) {
	// The detail shown during the cross-source check is provisional: the
	// scan may still swap the source and its episode list. Guard every
	// entry point, not just the disabled button on the detail page.
	if a.screen == screenDetail && a.offersBusy() {
		return
	}
	src := a.current()
	if src == nil {
		return
	}
	a.notePlayFrom()
	a.resumeEpisode = ""
	a.screen = screenPlay
	a.playLoading = true
	a.playErr = ""
	a.playNote = ""
	a.playTitle = ep.Name
	a.fromLive = false
	a.playLine = line
	a.playIndex = episodeIndex(a.detail, line, ep)
	a.showDrawer = false
	a.closePlayer()
	key := a.siteKey
	parses := []config.ParseItem(nil)
	if a.cfg != nil {
		parses = a.cfg.Parses
	}
	// 不调用bump()，保持gen不变，让详情页的片源探测继续运行
	gen := a.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		pb, err := src.Resolve(ctx, line, ep)
		if err == nil && (pb.NeedsParse || !player.IsDirect(pb.URL)) {
			if u, h, ok := a.applyParsesRanked(ctx, pb.URL, parses); ok {
				pb.URL = u
				pb.Headers = mergeHeaders(pb.Headers, h)
			}
		}
		if err == nil && !player.IsDirect(pb.URL) {
			if u, ok := player.Unwrap(ctx, pb.URL, pb.Headers); ok && u != "" {
				pb.URL = u
			}
		}
		title := ep.Name
		a.post(func() {
			if a.gen != gen || a.siteKey != key {
				return
			}
			a.playLoading = false
			if err != nil {
				a.playErr = err.Error()
				a.playTitle = title
				return
			}
			if a.detail != nil && a.detail.Title != "" {
				title = a.detail.Title + "  ·  " + ep.Name
			}
			a.startPlay(title, pb.URL, pb.Headers, false)
		})
	}()
}

func (a *App) playChannel(ch live.Channel) {
	a.liveTries = 0
	a.startChannel(ch)
}

func (a *App) startPlay(title, raw string, headers map[string]string, fromLive bool) {
	a.closePlayer()
	a.playTitle = title
	a.playURL = raw
	a.playHeaders = headers
	a.playErr = ""
	a.playNote = ""
	a.playLoading = false
	a.fromLive = fromLive
	a.notePlayFrom()
	a.screen = screenPlay
	if strings.TrimSpace(raw) == "" {
		a.playErr = "没有播放地址"
		return
	}
	var notes []string
	if !player.IsDirect(raw) {
		notes = append(notes, "这个地址不像直接的媒体流，仍会尝试播放。")
	}
	p, err := a.pb.Play(context.Background(), raw, headers)
	if err != nil {
		a.playErr = err.Error()
		a.playNote = strings.Join(notes, " ")
		return
	}
	if note := a.pb.FallbackNote(); note != "" {
		notes = append(notes, note)
	}
	a.playNote = strings.Join(notes, " ")
	p.SetVolume(a.volume)
	if a.rate <= 0 {
		a.rate = 1
	}
	p.SetRate(a.rate)
	a.player = p
	a.resetEpisodeExtras()
	a.applyPicture()
	a.controlsUntil = time.Now().Add(4 * time.Second)
	a.rememberCurrent(raw)
}

func (a *App) selectLive(i int) {
	if a.catalog == nil || i < 0 || i >= len(a.catalog.Lives) {
		return
	}
	a.livePlayGen++
	a.liveIndex = i
	a.sideSel = "live:" + strconv.Itoa(i)
	a.lastSide = a.sideSel
	a.query = ""
	a.committed = ""
	a.screen = screenLive
	a.closePlayer()
	a.fromLive = true
	a.channels = nil
	a.liveErr = ""
	// A failed stage from another list must not follow this one: its error
	// would keep the empty player stage and its notice on screen.
	a.playErr = ""
	a.playLoading = false
	a.liveLoading = true
	entry := a.catalog.Lives[i]
	gen := a.bump()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		ch, err := entry.Load(ctx)
		a.post(func() {
			if a.gen != gen || a.liveIndex != i {
				return
			}
			a.liveLoading = false
			a.channels = ch
			if err != nil {
				a.liveErr = err.Error()
			}
		})
	}()
}

func (a *App) back() {
	a.bump()
	a.playLoading = false
	a.detailLoading = false
	a.closePlayer()
	switch a.screen {
	case screenLiveAgg:
		a.fromLive = false
		a.playErr = ""
		a.showDrawer = false
		a.liveWatchAt = time.Time{}
		return
	case screenPlay:
		if a.fromLive {
			a.screen = screenLive
			return
		}
		from := a.playFrom
		a.playFrom = ""
		if from == screenDetail || from == "" {
			if a.detail != nil {
				a.screen = screenDetail
				return
			}
			from = ""
		}
		a.screen = a.backTarget(from)
	case screenDetail:
		from := a.detailFrom
		a.detailFrom = ""
		a.screen = a.backTarget(from)
	}
}

// returnable are the screens back may return to: lists a title was opened
// from, which keep their own state while the player or detail page shows.
var returnable = map[string]bool{
	screenHome: true, screenBrowse: true, screenHistory: true, screenFav: true, screenMore: true,
}

// notePlayFrom remembers where the player was opened from. Starting another
// episode, auto-next or a retry happen on the player and keep it.
func (a *App) notePlayFrom() {
	if a.screen != screenPlay {
		a.playFrom = a.screen
	}
}

// noteDetailFrom remembers where the detail page was opened from.
func (a *App) noteDetailFrom() {
	if a.screen != screenDetail && a.screen != screenPlay {
		a.detailFrom = a.screen
	}
}

// backTarget is the screen to go back to: from when it is a list, else the
// one the sidebar selection names.
func (a *App) backTarget(from string) string {
	if returnable[from] {
		return from
	}
	switch {
	case a.sideSel == "home" || a.sideSel == "":
		return screenHome
	case a.sideSel == "history":
		return screenHistory
	case a.sideSel == "fav":
		return screenFav
	case a.siteKey != "" && a.catalog != nil && a.catalog.Find(a.siteKey) != nil:
		return screenBrowse
	}
	return screenHome
}

func (a *App) pickFile() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  win,
			Title:   "打开配置",
			Filters: []mygo.FileFilter{{Name: "配置", Extensions: []string{"json", "txt"}}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.post(func() {
			a.adding = false
			a.urlDraft = ""
			a.AddSubscription(path)
		})
	}()
}

func (a *App) poster(url string) *ui.Bitmap {
	url = model.ResolveURL("", url)
	if url == "" || a.failed[url] {
		return nil
	}
	if bmp, ok := a.posters[url]; ok {
		a.touchPoster(url)
		return bmp
	}
	if a.win == nil || a.fetching[url] || len(a.fetching) >= 8 {
		return nil
	}
	a.fetching[url] = true
	win := a.win
	headers := a.imageHeaders()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		body, _, err := httpx.Get(ctx, nil, url, headers, 4<<20)
		if err != nil && headers["Referer"] != "" {
			retry := map[string]string{"User-Agent": httpx.BrowserUA, "Accept": "image/*,*/*"}
			body, _, err = httpx.Get(ctx, nil, url, retry, 4<<20)
		}
		netErr := err != nil
		var img image.Image
		if err == nil {
			img, _, err = image.Decode(bytes.NewReader(body))
		}
		var blurred *image.RGBA
		var tint color.RGBA
		if img != nil {
			blurred, tint = blurImage(img)
		}
		raw := body
		win.Update(func() {
			delete(a.fetching, url)
			if img != nil {
				a.rememberImage(url, img, blurred, tint)
				return
			}
			if err == nil {
				if bmp, decErr := ui.DecodeBitmap(raw); decErr == nil {
					a.remember(url, bmp)
					return
				}
			}
			// A download that timed out or was refused may work next time:
			// many posters at once easily overrun a slow host.
			if netErr && a.posterTries[url] < posterRetries {
				if a.posterTries == nil {
					a.posterTries = map[string]int{}
				}
				a.posterTries[url]++
				return
			}
			delete(a.posterTries, url)
			a.failed[url] = true
		})
	}()
	return nil
}

func (a *App) imageHeaders() map[string]string {
	h := map[string]string{
		"User-Agent": httpx.BrowserUA,
		"Accept":     "image/avif,image/webp,image/*,*/*",
	}
	if m, ok := a.currentMeta(); ok {
		if u, err := url.Parse(m.API); err == nil && u.Scheme != "" && u.Host != "" {
			h["Referer"] = u.Scheme + "://" + u.Host + "/"
		}
	}
	return h
}

// posterLimit is how many decoded posters stay in memory. Only covers near
// the viewport count as drawn (see cover), so the limit only has to hold a
// few screens' worth, with room for scrolling back.
const posterLimit = 400

// posterRetries is how many extra tries a poster gets after a network error.
const posterRetries = 2

// cachedPoster returns a poster already in memory without loading it or
// marking it as drawn: for covers far from the viewport.
func (a *App) cachedPoster(url string) *ui.Bitmap {
	url = model.ResolveURL("", url)
	if url == "" || a.failed[url] {
		return nil
	}
	return a.posters[url]
}

// touchPoster marks url as just drawn, so eviction keeps what is on screen.
func (a *App) touchPoster(url string) {
	if a.posterUsed == nil {
		a.posterUsed = map[string]int64{}
	}
	a.posterTick++
	a.posterUsed[url] = a.posterTick
}

// remember stores a poster and, over posterLimit, drops the least recently
// drawn ones. Clearing everything made every visible poster reload at once,
// which then overflowed the cache again and looped.
func (a *App) remember(url string, bmp *ui.Bitmap) {
	a.posters[url] = bmp
	a.touchPoster(url)
	for len(a.posters) > posterLimit {
		oldest, at := "", int64(-1)
		for u := range a.posters {
			if u == url {
				continue
			}
			if t := a.posterUsed[u]; at < 0 || t < at {
				oldest, at = u, t
			}
		}
		if oldest == "" {
			break
		}
		delete(a.posters, oldest)
		delete(a.posterUsed, oldest)
		delete(a.art, oldest)
	}
}

func (a *App) visibleChannels() []live.Channel {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.channels
	}
	var out []live.Channel
	for _, ch := range a.channels {
		if strings.Contains(strings.ToLower(ch.Group+" "+ch.Name), q) {
			out = append(out, ch)
		}
	}
	return out
}

func (a *App) current() source.Source {
	if a.catalog == nil {
		return nil
	}
	return a.catalog.Find(a.siteKey)
}

func (a *App) currentMeta() (source.Meta, bool) {
	src := a.current()
	if src == nil {
		return source.Meta{}, false
	}
	return src.Meta(), true
}

// findAll returns a source from the full subscription list by its key, so the
// management view can address sources that did not make it into the catalog
// (disabled, unsupported or failed probe).
func (a *App) findAll(key string) source.Source {
	for _, s := range a.allSites {
		if s.Meta().Key == key {
			return s
		}
	}
	return nil
}

// statsSnapshot persists speed results keyed by the stable sourceHideID, so
// they survive a reload even when mergeConfigs renames a duplicate key.
func (a *App) statsSnapshot() map[string]sourceStat {
	if len(a.stats) == 0 {
		return nil
	}
	out := map[string]sourceStat{}
	for key, st := range a.stats {
		id := key
		if s := a.catalog.Find(key); s != nil {
			id = sourceHideID(s)
		} else if s := a.findAll(key); s != nil {
			id = sourceHideID(s)
		}
		out[id] = st
	}
	return out
}

// applyPersistedStats maps restored speed results (keyed by sourceHideID) back
// onto the freshly built catalog's Meta.Key values.
func (a *App) applyPersistedStats() {
	if len(a.persistedStats) == 0 {
		return
	}
	if a.stats == nil {
		a.stats = map[string]sourceStat{}
	}
	for _, s := range a.allSites {
		if st, ok := a.persistedStats[sourceHideID(s)]; ok {
			a.stats[s.Meta().Key] = st
		}
	}
	a.persistedStats = nil
}

func (a *App) bump() int {
	a.gen++
	return a.gen
}

func (a *App) post(fn func()) {
	if a.win == nil {
		a.postMu.Lock()
		fn()
		a.postMu.Unlock()
		return
	}
	a.win.Update(func() {
		fn()
		// The floating window shows the same state: redraw it too.
		if w := a.px.pipWin; w != nil {
			w.Invalidate()
		}
	})
}

// drawFrame runs one frame of a view. With a window, rendering and post
// callbacks share the UI thread, so they are serialized by construction.
// Without one (headless tests), the post fallback runs its callbacks on
// background goroutines under postMu; taking postMu around the whole
// frame keeps those callbacks from writing App state while the frame
// reads it, mirroring the windowed behavior.
func (a *App) drawFrame(view func()) {
	if a.win != nil {
		view()
		return
	}
	a.postMu.Lock()
	defer a.postMu.Unlock()
	view()
}

func (a *App) closePlayer() {
	embedded := a.embedded()
	if a.player != nil {
		a.player.Close()
		a.player = nil
	}
	// An mpv window takes its fullscreen with it. The app window's stays,
	// so switching episodes in fullscreen keeps it; leaving the player
	// screens ends it (playerKeys).
	if !embedded {
		a.fullscreen = false
	}
	a.videoShown = false
	a.spaceDownAt = time.Time{}
	a.spaceBoosted = false
	a.bufferingSince = time.Time{}
}

func (a *App) saveSettings() {
	if a.settingsPath == "" {
		return
	}
	vol := a.volume
	s := savedSettings{
		Config: a.primaryRef(), Subs: cloneSubs(a.subs),
		PreferExternal: a.preferExternal, Appearance: a.appearance, Volume: &vol,
		Hidden:          hiddenIDs(a.hidden),
		Blacklist:       cloneBlacklist(a.blacklist),
		DisabledSources: disabledIDs(a.disabled),
		SourceStats:     a.statsSnapshot(),
		HomeRules:       cloneRules(a.homeRules),
		Skips:           cloneSkips(a.px.skips),
	}
	if !a.px.autoNext {
		off := false
		s.AutoNext = &off
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.settingsPath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(a.settingsPath, b, 0o644)
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "quickbo", "settings.json")
}

func mergeHeaders(a, b map[string]string) map[string]string {
	if len(a) == 0 {
		return b
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func hasRequestHeaders(h map[string]string) bool {
	for k, v := range h {
		if strings.TrimSpace(v) == "" {
			continue
		}
		switch strings.ToLower(k) {
		case "referer", "referrer", "user-agent":
			return true
		}
	}
	return false
}
