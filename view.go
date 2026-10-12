package main

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

// ink is the panel's colors, from the theme: the panel draws over the
// popover's material on macOS, so its tints are translucent.
type ink struct {
	text, muted, faint ui.Color
	hover, press, line ui.Color
	up, down, accent   ui.Color
	knob, pop          ui.Color // the range's choice; what floats over the panel
}

func inkOf(t *ui.Theme) ink {
	k := ink{
		text:   t.Text,
		muted:  t.TextMuted,
		faint:  t.Text.Alpha(0.32),
		hover:  t.Text.Alpha(0.055),
		press:  t.Text.Alpha(0.09),
		line:   t.Text.Alpha(0.08),
		up:     ui.Hex("#1f9d55"),
		down:   ui.Hex("#d64545"),
		accent: t.Accent,
		knob:   t.Background,
		pop:    t.Background,
	}
	if t.Dark {
		k.up, k.down = ui.Hex("#4ccf7f"), ui.Hex("#ff6b6b")
		k.knob = t.Text.Alpha(0.16)
		k.pop = t.Background.Mix(t.Text, 0.14)
	}
	return k
}

// The panel's metrics.
const (
	panelWidth  = 360
	panelHeight = 560
	gutter      = 16 // the panel's side margins
)

func (a *app) view(c *ui.Context) {
	t := c.Theme()
	k := inkOf(t)
	c.Root().Background(ui.Transparent)

	if a.configured() {
		c.OnShortcut(ui.Cmd, ui.KeyR, a.refresh)
		for i, key := range []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4} {
			c.OnShortcut(ui.Cmd, key, func() { a.setRange(i) })
		}
		c.OnShortcut(ui.Cmd, ui.KeyComma, func() { a.page = pageSettings; a.resetSettings() })
	}
	// Escape goes back to the list; on the list, it closes the panel.
	if a.page != pageSites && a.configured() {
		c.OnShortcut(0, ui.KeyEscape, a.back)
	}

	box := ui.Column(c).Fill().FontSize(13).TextColor(k.text)
	switch {
	case c.Vibrancy(): // over the popover's material
	case runtime.GOOS == "windows": // which rounds and outlines the flyout
		box.Background(t.Background)
	default:
		box.Background(t.Background).Radius(12).Border(1, t.Border)
	}
	box.Children(func() {
		switch {
		case a.page == pageSettings:
			a.settings(c, k)
		case a.page == pageSite && a.site(a.open) != nil:
			a.header(c, k)
			a.detail(c, k, a.site(a.open))
		default:
			a.header(c, k)
			a.list(c, k)
		}
	})
}

func (a *app) back() {
	if a.page == pageSettings {
		a.resetSettings()
	}
	a.page, a.selected = pageSites, -1
}

// header is the bar along the top: the range, and the buttons.
func (a *app) header(c *ui.Context, k ink) {
	ui.Row(c).Padding(12, 12, 8, gutter).Gap(4).Children(func() {
		a.rangeControl(c, k)
		ui.Spacer(c)
		spin := a.loading > 0
		iconButton(c.Key("refresh"), k, iconRefresh, "Refresh", spin).OnClick(a.refresh)
		iconButton(c.Key("settings"), k, iconSettings, "Settings", false).OnClick(func() {
			a.resetSettings()
			a.page = pageSettings
		})
	})
}

func (a *app) rangeControl(c *ui.Context, k ink) {
	sel := a.cfg.Range
	seg := ui.SegmentedBase(c.Key("range"), &sel, len(spans))
	seg.Track.Padding(2).Radius(8).Background(k.hover).Label("Range")
	seg.Track.Children(func() {
		for i, s := range spans {
			el := seg.Segment(i).Padding(3, 10).Radius(6).FontSize(12).FontWeight(500).
				FontFeatures("tnum").Label(s.long[len("previous "):])
			if i == a.cfg.Range {
				el.Background(k.knob).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12)).TextColor(k.text)
			} else {
				el.TextColor(k.muted)
				if el.Hovered() {
					el.TextColor(k.text)
				}
			}
			el.Children(func() { ui.Text(c, s.label) })
		}
	})
	seg.Track.OnChange(func() { a.setRange(sel) })
}

// iconButton is a borderless button showing an icon, which turns while
// busy.
func iconButton(c *ui.Context, k ink, svg *ui.SVG, label string, busy bool) ui.Element {
	b := ui.ButtonBase(c).Size(28, 28).Center().Radius(7).Label(label).Tooltip(label)
	switch {
	case b.Pressed():
		b.Background(k.press)
	case b.Hovered():
		b.Background(k.hover)
	}
	b.Children(func() {
		ic := ui.Icon(c, svg).FontSize(15).TextColor(k.muted)
		if b.Hovered() {
			ic.TextColor(k.text)
		}
		if busy {
			ic.Rotate(ic.Loop("spin", 900*time.Millisecond, ui.Linear) * 360)
		}
	})
	return b
}

// list is the page of every website: the totals, then a row for each.
func (a *app) list(c *ui.Context, k ink) {
	switch {
	case a.listErr != nil && len(a.sites) == 0:
		a.problem(c, k, "Couldn't load your websites", a.listErr.Error())
		return
	case len(a.sites) == 0 && a.loading > 0:
		a.summary(c, k)
		ui.Column(c).Padding(4, 6).Children(func() {
			for i := range 4 {
				skeletonRow(c.Key(i), k)
			}
		})
		return
	case len(a.visible()) == 0:
		if len(a.sites) == 0 {
			a.problem(c, k, "No websites yet", "Add a website in Umami, and it shows up here.")
		} else {
			a.problem(c, k, "Every website is hidden", "Choose the websites to show in Settings.")
		}
		return
	}
	a.summary(c, k)
	ui.Row(c).Padding(8, gutter, 2, gutter).Gap(2).AlignItems(ui.Center).FontSize(11).FontWeight(500).TextColor(k.faint).Children(func() {
		ui.Text(c, "Websites")
		a.orderMenu(c.Key("order"), k)
		ui.Spacer(c)
		ui.Text(c, "Visitors")
	})
	vis := ids(a.visible())
	list := ui.Scroll(c.Key("sites")).Grow(1).Padding(0, 6, 6, 6).Label("Websites")
	list.Children(func() {
		for i, id := range vis {
			a.siteRow(c.Key(id), k, a.site(id), vis, i)
		}
	})
	// Below the last row is the end of the list.
	if v, ok := ui.Drop[siteDrag](list); ok {
		a.drop(vis, string(v), len(vis), a.reorderVisible)
	}
	a.footer(c, k)
}

// orderMenu says how the websites are ordered, and changes it.
func (a *app) orderMenu(c *ui.Context, k ink) {
	label := "by visitors"
	if a.cfg.CustomOrder {
		label = "in your order"
	}
	b := ui.ButtonBase(c).Padding(2, 5).Gap(3).Radius(5).AlignItems(ui.Center).Label("Order").
		Tooltip("Drag websites to put them in your order")
	if b.Hovered() || b.Pressed() {
		b.Background(k.hover).TextColor(k.muted)
	}
	b.Children(func() {
		ui.Text(c, label).NoWrap()
		ui.Icon(c, iconChevron).FontSize(10)
	})
	b.Menu(func(m *ui.Menu) {
		if m.Item("Most Visitors First").Checked(!a.cfg.CustomOrder).Chosen() {
			a.setCustomOrder(false)
		}
		if m.Item("Your Order").Checked(a.cfg.CustomOrder).Chosen() {
			a.setCustomOrder(true)
		}
	})
}

// summary is the totals of the websites shown, over the range.
func (a *app) summary(c *ui.Context, k ink) {
	cur, prev, visitors, active, loaded := a.totals()
	ui.Column(c).Padding(6, gutter, 4, gutter).Gap(2).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "Visitors").FontSize(12).FontWeight(500).TextColor(k.muted)
			ui.Spacer(c)
			live(c, k, active, true)
		})
		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			if loaded && len(a.sites) > 0 {
				ui.Text(c, count(float64(cur.Visitors))).FontSize(30).FontWeight(650).
					FontFeatures("tnum").LetterSpacing(-0.6).NoWrap()
				delta(c, k, float64(cur.Visitors), float64(prev.Visitors), false).Margin(0, 0, 6, 0)
			} else {
				skeleton(c, k, 96, 26).Margin(6, 0, 4, 0)
			}
			ui.Spacer(c)
			if loaded && len(a.sites) > 0 {
				ui.Column(c).AlignItems(ui.End).Margin(0, 0, 5, 0).Children(func() {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Text(c, count(float64(cur.Pageviews))).FontWeight(600).FontFeatures("tnum")
						ui.Text(c, "views").TextColor(k.muted)
					})
				})
			}
		})
		sparkline(c, k, visitors, 40, true).Margin(8, 0, 0, 0)
	})
}

// siteDrag is a website being dragged to another place, by its ID.
type siteDrag string

// reorderable makes row, the i-th website of order, one the user drags
// to another place in it, and drops others on: on its top half, they go
// before it, and on its bottom half after it. move moves them.
func (a *app) reorderable(row ui.Element, k ink, order []string, i int, move func(rows []int, to int)) {
	row.Drag(siteDrag(order[i]))
	if row.Dragging() {
		// A card under the pointer, and a ghost where it was.
		row.Opacity(0.5).Background(k.pop).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.18))
	}
	below := func() bool {
		_, y, _ := row.PointerPosition()
		return y > row.Bounds().H/2
	}
	if v, over := ui.DragOver[siteDrag](row); over && string(v) != order[i] {
		after := below()
		line := k.accent
		row.DrawOver(func(p *ui.Painter, r ui.Rect) {
			y := r.Y - 1
			if after {
				y = r.Y + r.H - 1
			}
			p.Fill(ui.Rect{X: r.X + 6, Y: y, W: r.W - 6, H: 2}, line, 1)
			p.Fill(ui.Rect{X: r.X, Y: y - 3, W: 8, H: 8}, line, 4)
		})
	}
	if v, ok := ui.Drop[siteDrag](row); ok {
		to := i
		if below() {
			to++
		}
		a.drop(order, string(v), to, move)
	}
}

// drop moves the website id dropped before the one at to in order.
func (a *app) drop(order []string, id string, to int, move func(rows []int, to int)) {
	from := slices.Index(order, id)
	if from < 0 || to == from || to == from+1 {
		return // where it was
	}
	move([]int{from}, to)
}

// siteRow is a website in the list, which opens its page.
func (a *app) siteRow(c *ui.Context, k ink, s *site, order []string, i int) {
	row := ui.ButtonBase(c).Padding(8, 10).Gap(12).Radius(8).AlignItems(ui.Center).Label(s.Name)
	a.reorderable(row, k, order, i, a.reorderVisible)
	switch {
	case row.Dragging():
	case row.Pressed():
		row.Background(k.press)
	case row.Hovered():
		row.Background(k.hover)
	}
	row.OnClick(func() { a.openSite(s.ID) })
	row.Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, s.Name).FontWeight(500).SingleLine().Shrink(1)
				if s.active > 0 {
					live(c, k, s.active, false)
				}
			})
			domain := s.Domain
			if domain == "" {
				domain = "—"
			}
			ui.Text(c, domain).FontSize(11).TextColor(k.muted).SingleLine()
		})
		switch {
		case !s.loaded:
			skeleton(c, k, 56, 20)
		case s.err != nil && s.cur.Visitors == 0:
			ui.Icon(c, iconAlert).FontSize(13).TextColor(k.down).Label(s.err.Error()).Tooltip(s.err.Error())
		default:
			sparkline(c, k, s.visitors, 20, false).Width(56)
		}
		ui.Column(c).Width(58).AlignItems(ui.End).Gap(1).Children(func() {
			if !s.loaded {
				skeleton(c, k, 32, 11).Margin(2, 0)
				skeleton(c, k, 24, 9).Margin(2, 0)
				return
			}
			ui.Text(c, count(float64(s.cur.Visitors))).FontWeight(600).FontFeatures("tnum").NoWrap()
			delta(c, k, float64(s.cur.Visitors), float64(s.prev.Visitors), false).FontSize(11)
		})
	})
}

func skeletonRow(c *ui.Context, k ink) {
	ui.Row(c).Padding(8, 10).Gap(12).Children(func() {
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			skeleton(c, k, 120, 11)
			skeleton(c, k, 80, 9)
		})
		skeleton(c, k, 56, 20)
		ui.Column(c).Width(58).AlignItems(ui.End).Gap(6).Children(func() {
			skeleton(c, k, 32, 11)
			skeleton(c, k, 24, 9)
		})
	})
}

// skeleton holds the place of a number still loading.
func skeleton(c *ui.Context, k ink, w, h float32) ui.Element {
	return ui.Box(c).Size(w, h).Radius(4).Background(k.hover)
}

// footer says what the numbers compare with, and when they loaded.
func (a *app) footer(c *ui.Context, k ink) {
	ui.Row(c).Padding(8, gutter, 10, gutter).Gap(6).FontSize(11).TextColor(k.faint).
		BorderWidth(1, 0, 0, 0).BorderColor(k.line).Children(func() {
		ui.Text(c, "Compared with the "+a.span().long).SingleLine().Grow(1).Shrink(1)
		if !a.loadedAt.IsZero() {
			c.After(10 * time.Second) // keep "ago" current
			ui.Text(c, "Updated "+ago(a.now(), a.loadedAt)).NoWrap()
		}
	})
}

// live shows how many people are on the websites now, with a dot.
func live(c *ui.Context, k ink, n int, long bool) {
	if n <= 0 && !long {
		return
	}
	ui.Row(c).Gap(5).AlignItems(ui.Center).FontSize(11).Children(func() {
		dot := ui.Box(c).Size(6, 6).Radius(3)
		if n > 0 {
			dot.Background(k.up)
		} else {
			dot.Background(k.faint)
		}
		label := fmt.Sprint(n)
		if long {
			label += " online"
		}
		el := ui.Text(c, label).FontFeatures("tnum").NoWrap()
		if n > 0 {
			el.TextColor(k.up).FontWeight(500)
		} else {
			el.TextColor(k.muted)
		}
	}).Tooltip(fmt.Sprintf("%d %s in the last 5 minutes", n, plural(n, "visitor", "visitors")))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// delta shows how a number changed from the range before: green as it
// grows, red as it falls; worse reverses them, for a bounce rate.
func delta(c *ui.Context, k ink, cur, prev float64, worse bool) ui.Element {
	f, ok := change(cur, prev)
	row := ui.Row(c).Gap(1).AlignItems(ui.Center).FontSize(11).FontWeight(500).FontFeatures("tnum")
	switch {
	case !ok && cur == 0:
		row.TextColor(k.faint).Children(func() { ui.Text(c, "—") })
		return row
	case !ok:
		row.TextColor(k.faint).Children(func() { ui.Text(c, "new").NoWrap() })
		return row
	case math.Abs(f) < 0.005:
		row.TextColor(k.muted).Children(func() { ui.Text(c, "0%") })
		return row
	}
	good := f > 0 != worse
	if good {
		row.TextColor(k.up)
	} else {
		row.TextColor(k.down)
	}
	row.Label(fmt.Sprintf("%s %s", map[bool]string{true: "up", false: "down"}[f > 0], percent(f)))
	row.Children(func() {
		if f > 0 {
			ui.Icon(c, iconUp).FontSize(10)
		} else {
			ui.Icon(c, iconDown).FontSize(10)
		}
		ui.Text(c, percent(f)).NoWrap()
	})
	return row
}

// sparkline draws a series as a line over a faint area.
func sparkline(c *ui.Context, k ink, values []float64, h float32, wide bool) ui.Element {
	box := ui.Box(c).Height(h)
	if len(values) < 2 {
		return box
	}
	_, hi := chart.Extent(values...)
	empty := hi == 0
	if empty {
		hi = 1
	}
	xs := make([]float32, len(values))
	ys := make([]float32, len(values))
	for i, v := range values {
		xs[i] = float32(i) / float32(len(values)-1)
		ys[i] = float32(v / hi * 0.92)
	}
	color := k.accent
	if empty {
		color = k.faint
	}
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		r.Y, r.H = r.Y+1, r.H-2 // room for the stroke
		p.FillPathGradient(chart.Area(r, xs, nil, ys, chart.CurveMonotone), ui.LinearGradient{
			From: color.Alpha(0.2), To: color.Alpha(0.02), Angle: 180,
		})
		width := float32(1.5)
		if wide {
			width = 1.75
		}
		p.StrokePath(chart.Line(r, xs, ys, chart.CurveMonotone), width, color)
	})
	return box
}

// problem fills the page with a message, and ways out of it.
func (a *app) problem(c *ui.Context, k ink, title, detail string) {
	ui.Column(c).Grow(1).Center().Gap(6).Padding(24, 32).Children(func() {
		ui.Text(c, title).FontSize(14).FontWeight(600).TextAlign(ui.Center)
		ui.Text(c, detail).FontSize(12).TextColor(k.muted).TextAlign(ui.Center).LineHeight(1.35)
		ui.Row(c).Gap(8).Margin(10, 0, 0, 0).Children(func() {
			if a.listErr != nil {
				ui.Button(c.Key("retry"), "Try again").OnClick(a.refresh)
			}
			ui.Button(c.Key("open-settings"), "Settings").OnClick(func() {
				a.resetSettings()
				a.page = pageSettings
			})
		})
	})
}
