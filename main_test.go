package main

import (
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fake is an Umami account of a few websites, with traffic that rises in
// the day and falls at night.
type fake struct {
	sites  []website
	scale  map[string]float64
	err    error
	calls  int
	mu     sync.Mutex
	keyErr bool
}

func newFake() *fake {
	return &fake{
		sites: []website{
			{ID: "a", Name: "Acme Docs", Domain: "docs.acme.dev"},
			{ID: "b", Name: "Personal blog", Domain: "egoist.dev"},
			{ID: "c", Name: "Landing page", Domain: "acme.dev"},
			{ID: "d", Name: "Changelog", Domain: "changelog.acme.dev"},
			{ID: "e", Name: "Status", Domain: "status.acme.dev"},
		},
		scale: map[string]float64{"a": 120, "b": 46, "c": 210, "d": 9, "e": 0},
	}
}

func (f *fake) websites(context.Context) ([]website, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.keyErr {
		return nil, &apiError{status: 401}
	}
	return f.sites, f.err
}

func (f *fake) stats(_ context.Context, id string, from, to time.Time) (cur, prev stats, err error) {
	k := f.scale[id] * to.Sub(from).Hours() / 24
	cur = stats{Pageviews: num(k * 3.1), Visitors: num(k), Visits: num(k * 1.3), Bounces: num(k * 0.55), Totaltime: num(k * 1.3 * 94)}
	p := k * (0.8 + 0.1*float64(len(id)+int(id[0])%4))
	prev = stats{Pageviews: num(p * 3), Visitors: num(p), Visits: num(p * 1.25), Bounces: num(p * 0.6), Totaltime: num(p * 1.25 * 80)}
	return cur, prev, nil
}

func (f *fake) active(_ context.Context, id string) (int, error) {
	return int(f.scale[id] / 30), nil
}

func (f *fake) series(_ context.Context, id string, from, to time.Time, unit, _ string) (views, visitors []point, err error) {
	step := 24 * time.Hour
	if unit == "hour" {
		step = time.Hour
	}
	for t, i := from, 0; !t.After(to); t, i = t.Add(step), i+1 {
		v := f.scale[id] / 24
		if unit == "day" {
			v = f.scale[id] * (1 + 0.25*math.Sin(float64(i)/2.3))
		} else {
			v *= 1 + 0.8*math.Sin(float64(t.Hour()-9)/24*2*math.Pi)
		}
		v = math.Round(v)
		if v <= 0 {
			continue // Umami leaves out units without any
		}
		x := t.Format("2006-01-02 15:04:05")
		visitors = append(visitors, point{X: x, Y: num(v)})
		views = append(views, point{X: x, Y: num(v * 2.9)})
	}
	return views, visitors, nil
}

func (f *fake) metrics(_ context.Context, id, kind string, _, _ time.Time, limit int) ([]metric, error) {
	k := f.scale[id]
	var rows []metric
	switch kind {
	case "path":
		rows = []metric{{"/", num(k * 2)}, {"/docs/getting-started", num(k * 1.1)}, {"/pricing", num(k * 0.6)}, {"/blog/introducing-acme-2", num(k * 0.3)}, {"/about", num(k * 0.1)}}
	case "referrer":
		rows = []metric{{"", num(k)}, {"www.google.com", num(k * 0.7)}, {"github.com", num(k * 0.4)}, {"news.ycombinator.com", num(k * 0.2)}}
	case "country":
		rows = []metric{{"US", num(k * 0.9)}, {"DE", num(k * 0.4)}, {"JP", num(k * 0.3)}, {"CN", num(k * 0.2)}}
	}
	if k == 0 {
		rows = nil
	}
	return rows[:min(limit, len(rows))], nil
}

// testApp is an app on the fake account, at 15:20 on Oct 12, 2026, whose
// work runs before refresh returns.
func testApp(t *testing.T, f *fake) *app {
	t.Helper()
	cfg := config{Endpoint: cloudEndpoint, Key: "api_test"}
	var src source
	if f != nil {
		src = f
	}
	a := newApp(cfg, src)
	if f == nil {
		a.cfg.Key = ""
		a.page = pageSettings
		a.resetSettings()
	}
	loc := time.FixedZone("CST", 8*3600)
	a.now = func() time.Time { return time.Date(2026, 10, 12, 15, 20, 0, 0, loc) }
	r := &runner{}
	a.spawn, a.post = r.spawn, r.post
	runners[a] = r
	return a
}

// runner stands in for the main thread: work runs in goroutines, and what
// it posts runs as settle drains it.
type runner struct {
	mu    sync.Mutex
	wg    sync.WaitGroup
	queue []func()
}

var runners = map[*app]*runner{}

func (r *runner) spawn(fn func()) {
	r.wg.Add(1)
	go func() { defer r.wg.Done(); fn() }()
}

func (r *runner) post(fn func()) {
	r.mu.Lock()
	r.queue = append(r.queue, fn)
	r.mu.Unlock()
}

// settle waits for the app's work, and what it starts, to end, then
// draws a frame.
func settle(a *app, tt *ui.Tester) {
	r := runners[a]
	for {
		r.wg.Wait()
		r.mu.Lock()
		q := r.queue
		r.queue = nil
		r.mu.Unlock()
		if len(q) == 0 {
			break
		}
		for _, fn := range q {
			fn()
		}
	}
	if tt != nil {
		tt.Frame()
	}
}

// snapshot writes the tester's frame to $UMA_SNAPSHOTS, to look at.
func snapshot(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("UMA_SNAPSHOTS")
	if dir == "" {
		return
	}
	tt.Frame()
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func newTester(a *app, dark bool) *ui.Tester {
	tt := ui.NewTester(a.view, panelWidth, panelHeight)
	tt.SetScale(2)
	tt.SetDark(dark)
	return tt
}

func TestList(t *testing.T) {
	a := testApp(t, newFake())
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	for _, s := range []string{"Acme Docs", "Personal blog", "Landing page", "Visitors", "Compared with the previous 24 hours"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	// The busiest website comes first.
	if v := a.visible(); v[0].ID != "c" || v[len(v)-1].ID != "e" {
		t.Errorf("order %v", []string{v[0].ID, v[len(v)-1].ID})
	}
	if got := a.liveTotal(); got != 4+1+7 {
		t.Errorf("live %d", got)
	}
	snapshot(t, tt, "list")
	tt.SetDark(true)
	snapshot(t, tt, "list-dark")
}

func TestRange(t *testing.T) {
	f := newFake()
	a := testApp(t, f)
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	if err := tt.Click("7D"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if a.cfg.Range != 1 || len(a.buckets) != 7 {
		t.Fatalf("range %d, %d buckets", a.cfg.Range, len(a.buckets))
	}
	if !tt.HasText("Compared with the previous 7 days") {
		t.Errorf("texts %q", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.Key3)
	settle(a, tt)
	if a.cfg.Range != 2 || len(a.site("a").visitors) != 30 {
		t.Errorf("range %d after Cmd+3", a.cfg.Range)
	}
	snapshot(t, tt, "list-30d")
}

func TestDetail(t *testing.T) {
	a := testApp(t, newFake())
	a.cfg.Range = 1
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	if err := tt.Click("Acme Docs"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if a.page != pageSite || a.open != "a" {
		t.Fatalf("page %v %q", a.page, a.open)
	}
	for _, s := range []string{"Bounce rate", "Visit duration", "/docs/getting-started", "Direct", "google.com", "United States", "Open in Umami"} {
		if !tt.HasText(s) {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	snapshot(t, tt, "detail")
	// Hover the chart.
	if r, ok := tt.Find("Visitors and views by day"); ok {
		tt.Move(r.X+r.W*0.6, r.Y+r.H/2)
		tt.Frame()
		if a.selected < 0 {
			t.Error("no bucket under the pointer")
		}
		snapshot(t, tt, "detail-hover")
	}
	tt.SetDark(true)
	snapshot(t, tt, "detail-dark")
	if err := tt.Click("Open in Umami"); err != nil {
		t.Fatal(err)
	}
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != "https://cloud.umami.is/websites/a" {
		t.Errorf("opened %q", got)
	}
	tt.Key(0, ui.KeyEscape)
	if a.page != pageSites {
		t.Errorf("page %v after Escape", a.page)
	}
}

func TestOnboarding(t *testing.T) {
	a := testApp(t, nil)
	tt := newTester(a, false)
	if !tt.HasText("Connect to Umami") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "onboarding")

	f := newFake()
	f.keyErr = true
	connect := func() { a.connect(func(string, string) source { return f }) }
	a.editKey = "api_wrong"
	connect()
	settle(a, tt)
	if a.configured() || !tt.HasText("The API key was rejected.") {
		t.Fatalf("configured %v, texts %q", a.configured(), tt.Texts())
	}
	snapshot(t, tt, "onboarding-error")

	f.keyErr = false
	saved := 0
	a.changed = func(config) { saved++ }
	a.editKey = "api_right"
	connect()
	settle(a, tt)
	if !a.configured() || a.page != pageSites || saved == 0 || len(a.sites) != 5 {
		t.Fatalf("configured %v, page %v, saved %d", a.configured(), a.page, saved)
	}
}

func TestSettings(t *testing.T) {
	a := testApp(t, newFake())
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	snapshot(t, tt, "settings")
	if err := tt.Click("Show Landing page"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.cfg.Hidden) != 1 || a.cfg.Hidden[0] != "c" {
		t.Fatalf("hidden %q", a.cfg.Hidden)
	}
	if err := tt.Click("Websites"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Landing page") {
		t.Error("a hidden website is listed")
	}
}

func TestErrors(t *testing.T) {
	f := newFake()
	f.err = errors.New("Couldn't reach the server.")
	a := testApp(t, f)
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	if !tt.HasText("Couldn't load your websites") {
		t.Fatalf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "error")
	f.err = nil
	if err := tt.Click("Try again"); err != nil {
		t.Fatal(err)
	}
	settle(a, tt)
	if !tt.HasText("Acme Docs") {
		t.Errorf("texts %q after retrying", tt.Texts())
	}
}

func TestBucketize(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 10, 12, 15, 20, 0, 0, loc)
	from, _, buckets := spans[0].window(now)
	if len(buckets) != 24 || !from.Equal(time.Date(2026, 10, 11, 16, 0, 0, 0, loc)) {
		t.Fatalf("from %v, %d buckets", from, len(buckets))
	}
	got := bucketize([]point{
		{X: "2026-10-12 15:00:00", Y: 3},
		{X: "2026-10-11T16:00:00+08:00", Y: 2},
		{X: "2026-10-12T06:00:00Z", Y: 5}, // 14:00 here
		{X: "nonsense", Y: 9},
	}, buckets, true)
	if got[23] != 3 || got[0] != 2 || got[22] != 5 {
		t.Errorf("buckets %v", got)
	}
	_, _, days := spans[1].window(now)
	if len(days) != 7 || days[6].Day() != 12 || days[0].Day() != 6 {
		t.Errorf("days %v", days)
	}
}

func TestFormat(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 999: "999", 9999: "9,999", 12_480: "12.4k", 123_456: "123k", 1_250_000: "1.2M"} {
		if got := count(v); got != want {
			t.Errorf("count(%v) = %q, want %q", v, got, want)
		}
	}
	for d, want := range map[time.Duration]string{48 * time.Second: "48s", 125 * time.Second: "2m 05s", 72 * time.Minute: "1h 12m"} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
	for in, want := range map[string]string{
		"":                               cloudEndpoint,
		"cloud.umami.is":                 cloudEndpoint,
		"umami.example.com":              "https://umami.example.com/api",
		"https://umami.example.com/api/": "https://umami.example.com/api",
		"https://api.umami.is/v1":        cloudEndpoint,
		"http://localhost:3000":          "http://localhost:3000/api",
	} {
		if got := normalizeEndpoint(in); got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
	if got := dashboardURL("https://umami.example.com/api", "x"); got != "https://umami.example.com/websites/x" {
		t.Errorf("dashboard %q", got)
	}
	if !strings.HasPrefix(percent(0.082), "8.2") {
		t.Errorf("percent %q", percent(0.082))
	}
}

func TestStatsShapes(t *testing.T) {
	// Umami 3, with counts as strings as Postgres's bigints come.
	var cur stats
	if err := json.Unmarshal([]byte(`{"pageviews":"12","visitors":5,"visits":6,"bounces":3,"totaltime":600}`), &cur); err != nil || cur.Pageviews != 12 || cur.Visitors != 5 {
		t.Errorf("v3 %+v %v", cur, err)
	}
	// Umami 2.
	var old stats
	if err := json.Unmarshal([]byte(`{"pageviews":{"value":10,"prev":8},"visitors":{"value":4,"prev":2}}`), &old); err != nil || old.Pageviews != 10 || old.Visitors != 4 {
		t.Errorf("v2 %+v %v", old, err)
	}
}

func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/websites":
			w.Write([]byte(`{"data":[{"id":"a","name":"A","domain":"a.dev"}],"count":1,"page":1,"pageSize":100}`))
		case "/api/websites/a/stats":
			if r.URL.Query().Get("startAt") == "" {
				w.WriteHeader(400)
				return
			}
			w.Write([]byte(`{"pageviews":30,"visitors":10,"visits":12,"bounces":6,"totaltime":1200,"comparison":{"pageviews":20,"visitors":8,"visits":9,"bounces":5,"totaltime":900}}`))
		case "/api/websites/a/active":
			w.Write([]byte(`{"visitors":3}`))
		case "/api/websites/a/metrics":
			if r.URL.Query().Get("type") == "path" {
				w.WriteHeader(400) // Umami 2
				return
			}
			w.Write([]byte(`[{"x":"/","y":5}]`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	cl := newClient(srv.URL+"/api/", "k")
	list, err := cl.websites(ctx)
	if err != nil || len(list) != 1 || list[0].Domain != "a.dev" {
		t.Fatalf("websites %v %v", list, err)
	}
	cur, prev, err := cl.stats(ctx, "a", time.Now().Add(-time.Hour), time.Now())
	if err != nil || cur.Visitors != 10 || prev.Visitors != 8 || cur.visitDuration() != 100*time.Second {
		t.Errorf("stats %+v %+v %v", cur, prev, err)
	}
	if n, err := cl.active(ctx, "a"); n != 3 || err != nil {
		t.Errorf("active %d %v", n, err)
	}
	if m, err := cl.metrics(ctx, "a", "path", time.Now(), time.Now(), 5); len(m) != 1 || err != nil {
		t.Errorf("metrics %v %v", m, err)
	}
	_, err = newClient(srv.URL+"/api", "wrong").websites(ctx)
	if err == nil || err.Error() != "The API key was rejected." {
		t.Errorf("wrong key: %v", err)
	}
}

// drag drags from one element's middle onto another, as a user would.
func drag(t *testing.T, tt *ui.Tester, from, to string, below bool) {
	t.Helper()
	a, ok := tt.Find(from)
	b, ok2 := tt.Find(to)
	if !ok || !ok2 {
		t.Fatalf("no %q or %q in %q", from, to, tt.Texts())
	}
	x, y := a.X+a.W/2, a.Y+a.H/2
	// Into the top or the bottom quarter of the target.
	ty := b.Y + b.H*0.25
	if below {
		ty = b.Y + b.H*0.75
	}
	tt.Press(x, y)
	for i := 1; i <= 8; i++ {
		tt.Move(x, y+(ty-y)*float32(i)/8)
	}
	tt.Release(x, ty)
	tt.Frame()
}

func names(sites []*site) []string {
	var out []string
	for _, s := range sites {
		out = append(out, s.Name)
	}
	return out
}

func TestReorderList(t *testing.T) {
	a := testApp(t, newFake())
	saved := 0
	a.changed = func(config) { saved++ }
	a.refresh()
	settle(a, nil)
	tt := newTester(a, false)
	// By visitors: Landing page, Acme Docs, Personal blog, Changelog, Status.
	drag(t, tt, "Changelog", "Landing page", false)
	got := names(a.visible())
	want := []string{"Changelog", "Landing page", "Acme Docs", "Personal blog", "Status"}
	if !slices.Equal(got, want) || !a.cfg.CustomOrder || saved == 0 {
		t.Fatalf("order %q, custom %v, saved %d", got, a.cfg.CustomOrder, saved)
	}
	if a.page != pageSites {
		t.Errorf("a drag opened a website")
	}
	// The order stays as the numbers change.
	a.setRange(1)
	settle(a, tt)
	if got := names(a.visible()); !slices.Equal(got, want) {
		t.Errorf("order %q after another range", got)
	}
	if !tt.HasText("in your order") {
		t.Errorf("texts %q", tt.Texts())
	}
	snapshot(t, tt, "list-custom")
	// A click still opens a website.
	if err := tt.Click("Acme Docs"); err != nil {
		t.Fatal(err)
	}
	if a.page != pageSite || a.open != "a" {
		t.Errorf("page %v %q after a click", a.page, a.open)
	}
	a.back()
	tt.Frame()
	// The menu goes back to the most visitors first, and keeps the order.
	if err := tt.Click("Order"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Most Visitors First"); err != nil {
		t.Fatalf("%v: menu %q", err, tt.Menu())
	}
	tt.Frame()
	if a.cfg.CustomOrder || a.visible()[0].Name != "Landing page" || len(a.cfg.Order) != 5 {
		t.Errorf("custom %v, order %q", a.cfg.CustomOrder, names(a.visible()))
	}
}

func TestReorderSettings(t *testing.T) {
	a := testApp(t, newFake())
	a.cfg.Hidden = []string{"a"} // Acme Docs
	a.refresh()
	settle(a, nil)
	a.page = pageSettings
	tt := newTester(a, false)
	tt.SetSize(panelWidth, 900)
	if !tt.HasText("Acme Docs") {
		t.Fatalf("hidden sites aren't in the settings: %q", tt.Texts())
	}
	snapshot(t, tt, "settings-order")
	drag(t, tt, "Landing page", "status.acme.dev", true)
	if got := names(a.ordered()); got[len(got)-1] != "Landing page" || !a.cfg.CustomOrder {
		t.Fatalf("order %q", got)
	}
	// Moving shown sites in the list leaves the hidden one in its place.
	before := slices.Index(ids(a.ordered()), "a")
	a.reorderVisible([]int{0}, 2)
	if after := slices.Index(ids(a.ordered()), "a"); after != before {
		t.Errorf("the hidden site moved from %d to %d", before, after)
	}
	// Back to the most visitors first, from the segmented control.
	if err := tt.Click("Most visitors first"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.cfg.CustomOrder {
		t.Error("still in the user's order")
	}
}

func TestMoveRows(t *testing.T) {
	in := []string{"a", "b", "c", "d", "e"}
	for _, tc := range []struct {
		rows []int
		to   int
		want string
	}{
		{[]int{3}, 0, "dabce"},
		{[]int{0}, 5, "bcdea"},
		{[]int{0}, 2, "bacde"},
		{[]int{1, 3}, 5, "acebd"},
		{[]int{4}, 1, "aebcd"},
	} {
		if got := strings.Join(moveRows(in, tc.rows, tc.to), ""); got != tc.want {
			t.Errorf("moveRows(%v, %d) = %s, want %s", tc.rows, tc.to, got, tc.want)
		}
	}
	// A website the order doesn't know yet comes after it.
	a := testApp(t, newFake())
	a.refresh()
	settle(a, nil)
	a.cfg.Order, a.cfg.CustomOrder = []string{"e", "d"}, true
	if got := names(a.ordered()); got[0] != "Status" || got[1] != "Changelog" || got[2] != "Acme Docs" {
		t.Errorf("order %q", got)
	}
}
