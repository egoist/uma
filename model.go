package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// source is what the app reads from Umami: *client, or a fake in tests.
type source interface {
	websites(ctx context.Context) ([]website, error)
	stats(ctx context.Context, id string, from, to time.Time) (cur, prev stats, err error)
	active(ctx context.Context, id string) (int, error)
	series(ctx context.Context, id string, from, to time.Time, unit, tz string) (views, visitors []point, err error)
	metrics(ctx context.Context, id, kind string, from, to time.Time, limit int) ([]metric, error)
}

// span is a range of time the app shows, with its buckets.
type span struct {
	label string // in the range control
	long  string // in sentences: "vs. previous 7 days"
	hours bool   // buckets of an hour, or of a day
	n     int    // buckets
}

var spans = []span{
	{"24H", "previous 24 hours", true, 24},
	{"7D", "previous 7 days", false, 7},
	{"30D", "previous 30 days", false, 30},
	{"90D", "previous 90 days", false, 90},
}

// window returns the range of s ending now, and the start of each bucket.
func (s span) window(now time.Time) (from, to time.Time, buckets []time.Time) {
	if s.hours {
		from = now.Truncate(time.Hour).Add(-time.Duration(s.n-1) * time.Hour)
		// Truncate works in UTC: put the hour back in local time.
		from = time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), 0, 0, 0, now.Location())
		for i := range s.n {
			buckets = append(buckets, from.Add(time.Duration(i)*time.Hour))
		}
	} else {
		y, m, d := now.Date()
		from = time.Date(y, m, d-(s.n-1), 0, 0, 0, 0, now.Location())
		for i := range s.n {
			buckets = append(buckets, time.Date(y, m, d-(s.n-1)+i, 0, 0, 0, 0, now.Location()))
		}
	}
	return from, now, buckets
}

func (s span) unit() string {
	if s.hours {
		return "hour"
	}
	return "day"
}

// bucketize spreads points the server gave over buckets, which are
// zero where it gave none.
func bucketize(points []point, buckets []time.Time, hours bool) []float64 {
	out := make([]float64, len(buckets))
	if len(buckets) == 0 {
		return out
	}
	loc := buckets[0].Location()
	for _, p := range points {
		t, ok := parseTime(p.X, loc)
		if !ok {
			continue
		}
		if hours {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		} else {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
		if i := slices.IndexFunc(buckets, t.Equal); i >= 0 {
			out[i] += float64(p.Y)
		}
	}
	return out
}

// parseTime reads the times of series: wall clock times in the zone the
// series was asked in, or instants.
func parseTime(s string, loc *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.In(loc), true
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// localZone is the IANA name of the local time zone, which Umami groups
// series in.
func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if p, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(p, "zoneinfo/"); ok {
			return name
		}
	}
	return "UTC"
}

// site is a website and what the app knows of it.
type site struct {
	website
	cur, prev stats
	views     []float64 // per bucket
	visitors  []float64
	active    int
	loaded    bool
	err       error

	// What its page shows besides, for the range the app shows.
	detailGen int
	pages     []metric
	referrers []metric
	countries []metric
	detailErr error
}

type page int

const (
	pageSites page = iota
	pageSite
	pageSettings
)

// app is the state of the panel. Everything in it is read and changed on
// the main thread: work in other goroutines posts its results back with
// post.
type app struct {
	cfg     config
	cfgDir  string
	src     source
	tz      string
	now     func() time.Time
	post    func(func())     // runs a function on the main thread, then redraws
	spawn   func(func())     // starts work in the background
	changed func(cfg config) // the config changed: save it, update the tray
	onLive  func(n int)      // the count of visitors online changed
	quit    func()

	page     page
	open     string // the ID of the site its page shows
	sites    []*site
	listErr  error
	loading  int // loads under way
	gen      int // increments as what is shown changes; results of older ones are dropped
	loadedAt time.Time
	buckets  []time.Time
	selected int // the bucket the pointer is over in the site's chart, or -1

	// The settings' fields, until saved.
	editEndpoint string
	editKey      string
	connecting   bool
	connectErr   string
}

func newApp(cfg config, src source) *app {
	a := &app{
		cfg:      cfg,
		src:      src,
		tz:       localZone(),
		now:      time.Now,
		post:     func(fn func()) { fn() },
		spawn:    func(fn func()) { fn() },
		changed:  func(config) {},
		onLive:   func(int) {},
		quit:     func() {},
		selected: -1,
	}
	a.cfg.Range = min(max(a.cfg.Range, 0), len(spans)-1)
	a.resetSettings()
	if !a.configured() {
		a.page = pageSettings
	}
	return a
}

func (a *app) configured() bool { return a.src != nil && a.cfg.Key != "" }

func (a *app) span() span { return spans[a.cfg.Range] }

func (a *app) site(id string) *site {
	for _, s := range a.sites {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// ordered returns every site in the order the app shows them: the user's
// own, or those with the most visitors first. Sites the user's order does
// not have yet come after it, by name.
func (a *app) ordered() []*site {
	out := slices.Clone(a.sites)
	byName := func(x, y *site) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	}
	if a.cfg.CustomOrder {
		rank := func(s *site) int {
			if i := slices.Index(a.cfg.Order, s.ID); i >= 0 {
				return i
			}
			return len(a.cfg.Order)
		}
		slices.SortStableFunc(out, func(x, y *site) int {
			if rx, ry := rank(x), rank(y); rx != ry {
				return rx - ry
			}
			return byName(x, y)
		})
		return out
	}
	slices.SortStableFunc(out, func(x, y *site) int {
		if x.cur.Visitors != y.cur.Visitors {
			if x.cur.Visitors > y.cur.Visitors {
				return -1
			}
			return 1
		}
		return byName(x, y)
	})
	return out
}

// visible returns the sites shown, in order.
func (a *app) visible() []*site {
	var out []*site
	for _, s := range a.ordered() {
		if !slices.Contains(a.cfg.Hidden, s.ID) {
			out = append(out, s)
		}
	}
	return out
}

// reorderVisible moves the shown sites at rows before the one at to, as
// the user dragged them in the list, and keeps that order from now on.
// Hidden sites keep their places among the others.
func (a *app) reorderVisible(rows []int, to int) {
	moved := moveRows(ids(a.visible()), rows, to)
	all := ids(a.ordered())
	j := 0
	for i, id := range all {
		if !slices.Contains(a.cfg.Hidden, id) {
			all[i] = moved[j]
			j++
		}
	}
	a.setOrder(all)
}

// reorderAll moves sites at rows of every site, hidden ones included, as
// in the settings.
func (a *app) reorderAll(rows []int, to int) {
	a.setOrder(moveRows(ids(a.ordered()), rows, to))
}

func (a *app) setOrder(order []string) {
	a.cfg.Order, a.cfg.CustomOrder = order, true
	a.changed(a.cfg)
}

// setCustomOrder shows the sites in the user's order, or goes back to
// showing those with the most visitors first, keeping the user's order
// for later.
func (a *app) setCustomOrder(on bool) {
	if on == a.cfg.CustomOrder {
		return
	}
	if on && len(a.cfg.Order) == 0 {
		a.cfg.Order = ids(a.ordered()) // start from what shows
	}
	a.cfg.CustomOrder = on
	a.changed(a.cfg)
}

func ids(sites []*site) []string {
	out := make([]string, len(sites))
	for i, s := range sites {
		out[i] = s.ID
	}
	return out
}

// moveRows moves the items at rows, ascending, before the item at to, or
// to the end for len(items).
func moveRows[T any](items []T, rows []int, to int) []T {
	var picked, rest []T
	at := to
	for i, it := range items {
		if slices.Contains(rows, i) {
			picked = append(picked, it)
			if i < to {
				at--
			}
		} else {
			rest = append(rest, it)
		}
	}
	at = min(max(at, 0), len(rest))
	return slices.Concat(rest[:at], picked, rest[at:])
}

// totals sums the sites shown.
func (a *app) totals() (cur, prev stats, visitors []float64, active int, loaded bool) {
	loaded = true
	for _, s := range a.visible() {
		if !s.loaded {
			loaded = false
		}
		cur.Pageviews += s.cur.Pageviews
		cur.Visitors += s.cur.Visitors
		cur.Visits += s.cur.Visits
		prev.Pageviews += s.prev.Pageviews
		prev.Visitors += s.prev.Visitors
		prev.Visits += s.prev.Visits
		active += s.active
		if visitors == nil {
			visitors = make([]float64, len(s.visitors))
		}
		for i := range min(len(visitors), len(s.visitors)) {
			visitors[i] += s.visitors[i]
		}
	}
	return cur, prev, visitors, active, loaded
}

// liveTotal is the count of visitors on the sites shown now.
func (a *app) liveTotal() int {
	n := 0
	for _, s := range a.visible() {
		n += s.active
	}
	return n
}

// refresh loads the list of websites, then each one's numbers for the range
// shown.
func (a *app) refresh() {
	if !a.configured() {
		return
	}
	a.gen++
	gen, src := a.gen, a.src
	sp := a.span()
	from, to, buckets := sp.window(a.now())
	a.buckets = buckets
	a.loading++
	a.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		list, err := src.websites(ctx)
		a.post(func() {
			if gen != a.gen {
				a.loading--
				return
			}
			a.listErr = err
			if err != nil {
				a.loading--
				return
			}
			a.merge(list)
		})
		if err != nil {
			return
		}
		var wg sync.WaitGroup
		limit := make(chan struct{}, 6)
		for _, w := range list {
			wg.Add(1)
			go func() {
				defer wg.Done()
				limit <- struct{}{}
				defer func() { <-limit }()
				a.loadSite(ctx, src, gen, w.ID, sp, from, to, buckets)
			}()
		}
		wg.Wait()
		a.post(func() {
			a.loading--
			if gen == a.gen {
				a.loadedAt = a.now()
			}
		})
	})
	if a.page == pageSite {
		a.loadDetail()
	}
}

// merge keeps the sites the server listed, and what is known of them.
func (a *app) merge(list []website) {
	sites := make([]*site, 0, len(list))
	for _, w := range list {
		s := a.site(w.ID)
		if s == nil {
			s = &site{}
		}
		s.website = w
		sites = append(sites, s)
	}
	a.sites = sites
	if a.page == pageSite && a.site(a.open) == nil {
		a.page = pageSites
	}
}

func (a *app) loadSite(ctx context.Context, src source, gen int, id string, sp span, from, to time.Time, buckets []time.Time) {
	var (
		cur, prev       stats
		views, visitors []point
		active          int
		errs            [3]error
		wg              sync.WaitGroup
	)
	wg.Add(3)
	go func() { defer wg.Done(); cur, prev, errs[0] = src.stats(ctx, id, from, to) }()
	go func() {
		defer wg.Done()
		views, visitors, errs[1] = src.series(ctx, id, from, to, sp.unit(), a.tz)
	}()
	go func() { defer wg.Done(); active, errs[2] = src.active(ctx, id) }()
	wg.Wait()
	a.post(func() {
		s := a.site(id)
		if gen != a.gen || s == nil {
			return
		}
		s.err = nil
		for _, err := range errs {
			if err != nil {
				s.err = err
				break
			}
		}
		if errs[0] == nil {
			s.cur, s.prev = cur, prev
		}
		if errs[1] == nil {
			s.views = bucketize(views, buckets, sp.hours)
			s.visitors = bucketize(visitors, buckets, sp.hours)
		}
		if errs[2] == nil {
			s.active = active
		}
		s.loaded = true
		a.onLive(a.liveTotal())
	})
}

// refreshActive updates how many people are on each site now, as the menu
// bar shows.
func (a *app) refreshActive() {
	if !a.configured() || len(a.sites) == 0 {
		return
	}
	src := a.src
	ids := make([]string, len(a.sites))
	for i, s := range a.sites {
		ids[i] = s.ID
	}
	a.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		counts := make([]int, len(ids))
		errs := make([]error, len(ids))
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() { defer wg.Done(); counts[i], errs[i] = src.active(ctx, id) }()
		}
		wg.Wait()
		a.post(func() {
			for i, id := range ids {
				if s := a.site(id); s != nil && errs[i] == nil {
					s.active = counts[i]
				}
			}
			a.onLive(a.liveTotal())
		})
	})
}

// openSite shows a site's page.
func (a *app) openSite(id string) {
	a.page, a.open, a.selected = pageSite, id, -1
	a.loadDetail()
}

// loadDetail loads the breakdowns the site's page shows, once per range.
func (a *app) loadDetail() {
	s := a.site(a.open)
	if s == nil || s.detailGen == a.gen {
		return
	}
	s.detailGen, s.detailErr = a.gen, nil
	s.pages, s.referrers, s.countries = nil, nil, nil
	gen, id, src := a.gen, s.ID, a.src
	from, to, _ := a.span().window(a.now())
	a.loading++
	a.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var (
			res  [3][]metric
			errs [3]error
			wg   sync.WaitGroup
		)
		for i, kind := range []string{"path", "referrer", "country"} {
			wg.Add(1)
			go func() { defer wg.Done(); res[i], errs[i] = src.metrics(ctx, id, kind, from, to, 6) }()
		}
		wg.Wait()
		a.post(func() {
			a.loading--
			s := a.site(id)
			if s == nil || s.detailGen != gen {
				return
			}
			s.pages, s.referrers, s.countries = res[0], res[1], res[2]
			for _, err := range errs {
				if err != nil {
					s.detailErr = err
					break
				}
			}
		})
	})
}

// setRange shows another range of time.
func (a *app) setRange(i int) {
	if i == a.cfg.Range || i < 0 || i >= len(spans) {
		return
	}
	a.cfg.Range = i
	a.selected = -1
	for _, s := range a.sites {
		s.loaded = false
	}
	a.changed(a.cfg)
	a.refresh()
}

// stale reports whether the numbers shown are old enough to load again as
// the panel opens.
func (a *app) stale() bool {
	return a.loadedAt.IsZero() || a.now().Sub(a.loadedAt) > time.Minute
}

func (a *app) resetSettings() {
	a.editEndpoint, a.editKey = a.cfg.Endpoint, a.cfg.Key
	if a.editEndpoint == "" {
		a.editEndpoint = cloudEndpoint
	}
	a.connectErr = ""
}

// connect tries the settings' endpoint and key, and keeps them if Umami
// takes them.
func (a *app) connect(newSource func(endpoint, key string) source) {
	endpoint := normalizeEndpoint(a.editEndpoint)
	key := strings.TrimSpace(a.editKey)
	if key == "" {
		a.connectErr = "Paste an API key."
		return
	}
	a.editEndpoint = endpoint
	a.connecting, a.connectErr = true, ""
	src := newSource(endpoint, key)
	a.spawn(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := src.websites(ctx)
		a.post(func() {
			a.connecting = false
			if err != nil {
				a.connectErr = err.Error()
				return
			}
			first := !a.configured()
			a.cfg.Endpoint, a.cfg.Key, a.src = endpoint, key, src
			a.sites, a.listErr = nil, nil
			a.changed(a.cfg)
			if first {
				a.page = pageSites
			}
			a.refresh()
		})
	})
}

// disconnect forgets the API key.
func (a *app) disconnect() {
	a.cfg.Key, a.src = "", nil
	a.sites, a.listErr = nil, nil
	a.gen++
	a.resetSettings()
	a.page = pageSettings
	a.changed(a.cfg)
}

func (a *app) toggleHidden(id string) {
	if i := slices.Index(a.cfg.Hidden, id); i >= 0 {
		a.cfg.Hidden = slices.Delete(a.cfg.Hidden, i, i+1)
	} else {
		a.cfg.Hidden = append(a.cfg.Hidden, id)
	}
	a.changed(a.cfg)
	a.onLive(a.liveTotal())
}

// change is how much cur grew from prev, as a fraction, and whether there
// was anything before to compare with.
func change(cur, prev float64) (float64, bool) {
	if prev == 0 || math.IsNaN(prev) {
		return 0, false
	}
	return (cur - prev) / prev, true
}
