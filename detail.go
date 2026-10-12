package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

// detail is the page of one website.
func (a *app) detail(c *ui.Context, k ink, s *site) {
	ui.Row(c).Padding(0, 10, 4, 8).Children(func() {
		textButton(c.Key("back"), k, "Websites", iconBack, true).OnClick(a.back)
		ui.Spacer(c)
		if u := dashboardURL(a.cfg.Endpoint, s.ID); u != "" {
			svc := c.Services()
			textButton(c.Key("open"), k, "Open in Umami", iconExternal, false).OnClick(func() { svc.OpenURL(u) })
		}
	})
	ui.Scroll(c.Key("detail-"+s.ID)).Grow(1).Padding(4, gutter, gutter, gutter).Gap(18).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.Name).FontSize(17).FontWeight(650).SingleLine().Shrink(1)
				live(c, k, s.active, true)
			})
			if s.Domain != "" {
				ui.Text(c, s.Domain).FontSize(12).TextColor(k.muted).SingleLine()
			}
		})
		a.metricsGrid(c, k, s)
		a.chart(c, k, s)
		if s.detailErr != nil {
			ui.Row(c).Gap(6).AlignItems(ui.Center).FontSize(12).TextColor(k.muted).Children(func() {
				ui.Icon(c, iconAlert).TextColor(k.down)
				ui.Text(c, s.detailErr.Error()).Grow(1)
			})
		}
		breakdown(c.Key("pages"), k, "Pages", "Views", "path", s.pages, s.detailGen == a.gen && s.pages == nil && s.detailErr == nil)
		breakdown(c.Key("referrers"), k, "Referrers", "Visitors", "referrer", s.referrers, s.pages == nil && s.detailErr == nil)
		breakdown(c.Key("countries"), k, "Countries", "Visitors", "country", s.countries, s.pages == nil && s.detailErr == nil)
	})
}

// textButton is a borderless button of a label and an icon, before it or
// after it.
func textButton(c *ui.Context, k ink, label string, svg *ui.SVG, iconFirst bool) ui.Element {
	b := ui.ButtonBase(c).Padding(4, 8).Gap(3).Radius(6).FontSize(12).FontWeight(500).AlignItems(ui.Center)
	b.TextColor(k.muted)
	switch {
	case b.Pressed():
		b.Background(k.press).TextColor(k.text)
	case b.Hovered():
		b.Background(k.hover).TextColor(k.text)
	}
	b.Children(func() {
		if svg != nil && iconFirst {
			ui.Icon(c, svg).FontSize(14)
		}
		ui.Text(c, label).NoWrap()
		if svg != nil && !iconFirst {
			ui.Icon(c, svg).FontSize(12)
		}
	})
	return b
}

// metricsGrid shows the website's totals over the range, each with how it
// changed.
func (a *app) metricsGrid(c *ui.Context, k ink, s *site) {
	perVisit := func(st stats) float64 {
		if st.Visits == 0 {
			return 0
		}
		return float64(st.Pageviews) / float64(st.Visits)
	}
	cells := []struct {
		label     string
		value     string
		cur, prev float64
		worse     bool
	}{
		{"Visitors", count(float64(s.cur.Visitors)), float64(s.cur.Visitors), float64(s.prev.Visitors), false},
		{"Visits", count(float64(s.cur.Visits)), float64(s.cur.Visits), float64(s.prev.Visits), false},
		{"Views", count(float64(s.cur.Pageviews)), float64(s.cur.Pageviews), float64(s.prev.Pageviews), false},
		{"Views per visit", fmt.Sprintf("%.1f", perVisit(s.cur)), perVisit(s.cur), perVisit(s.prev), false},
		{"Bounce rate", percent(s.cur.bounceRate()), s.cur.bounceRate(), s.prev.bounceRate(), true},
		{"Visit duration", duration(s.cur.visitDuration()), s.cur.visitDuration().Seconds(), s.prev.visitDuration().Seconds(), false},
	}
	ui.Grid(c).Columns(3).GapX(12).GapY(14).Children(func() {
		for _, m := range cells {
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, m.label).FontSize(11).TextColor(k.muted).SingleLine()
				if !s.loaded {
					skeleton(c, k, 48, 16).Margin(2, 0)
					skeleton(c, k, 28, 9)
					return
				}
				ui.Text(c, m.value).FontSize(17).FontWeight(600).FontFeatures("tnum").NoWrap().LetterSpacing(-0.2)
				if m.cur == 0 && m.prev == 0 {
					delta(c, k, 0, 0, m.worse)
				} else {
					delta(c, k, m.cur, m.prev, m.worse)
				}
			})
		}
	})
}

// chart shows the website's visitors and views in each hour or day of the
// range, as Umami does: views behind, visitors in front.
func (a *app) chart(c *ui.Context, k ink, s *site) {
	sp := a.span()
	n := len(a.buckets)
	if n == 0 || len(s.views) != n || len(s.visitors) != n {
		ui.Box(c).Height(150).Radius(8).Background(k.hover)
		return
	}
	_, hi := chart.Extent(s.views...)
	y := chart.NewLinear(0, math.Max(hi, 4)).Nice(3)
	yTicks := y.Ticks(3)
	labels := make([]string, n)
	for i, b := range a.buckets {
		labels[i] = bucketLabel(b, sp.hours)
	}
	x := chart.NewBand(labels...).Padding(0.28, 0.06)
	xs := make([]float32, n)
	for i := range n {
		xs[i] = x.At(i)
	}

	// A few labels along the bottom: the first, the last and some between.
	var bottom []chart.Tick
	every := map[int]int{24: 6, 7: 1, 30: 7, 90: 30}[n]
	if every == 0 {
		every = max(1, n/4)
	}
	for i := 0; i < n; i += every {
		if n-i < every/2 && i != 0 {
			break
		}
		l := a.buckets[i].Format("Jan 2")
		if sp.hours {
			l = a.buckets[i].Format("15:04")
		} else if n == 7 {
			l = a.buckets[i].Format("Mon")
		}
		bottom = append(bottom, chart.Tick{At: xs[i], Label: l})
	}

	axisLabel := func(side chart.Side, tick chart.Tick) {
		ui.Text(c, tick.Label).NoWrap().FontSize(10).FontFeatures("tnum").TextColor(k.faint)
	}
	viewsColor, visitorsColor := k.accent.Alpha(0.28), k.accent
	if c.Theme().Dark {
		viewsColor = k.accent.Alpha(0.38)
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Gap(12).FontSize(11).TextColor(k.muted).Children(func() {
			legend(c, visitorsColor, "Visitors")
			legend(c, viewsColor, "Views")
			ui.Spacer(c)
			if i := a.selected; i >= 0 && i < n {
				ui.Text(c, bucketLabel(a.buckets[i], sp.hours)).FontFeatures("tnum").NoWrap()
			}
		})
		chart.Plot(c, chart.Axes{Right: yTicks, Bottom: bottom, Label: axisLabel}, func(area ui.Element) {
			line := k.line
			area.Draw(func(p *ui.Painter, r ui.Rect) {
				for _, tick := range yTicks {
					yy := chart.Y(r, tick.At)
					p.Line(r.X, yy, r.X+r.W, yy, 1, line)
				}
			})
			for i := range n {
				dim := a.selected >= 0 && a.selected != i
				vc, uc := viewsColor, visitorsColor
				if dim {
					vc, uc = vc.Alpha(0.14), uc.Alpha(0.4)
				}
				if s.views[i] > 0 {
					chart.Rect(c, x.Band(i), y.Span(0, s.views[i])).Radius(2, 2, 0, 0).Background(vc)
				}
				if s.visitors[i] > 0 {
					chart.Rect(c, x.Band(i), y.Span(0, s.visitors[i])).Radius(2, 2, 0, 0).Background(uc)
				}
			}
			describe := func(i int) string {
				return fmt.Sprintf("%s: %s visitors, %s views", labels[i], count(s.visitors[i]), count(s.views[i]))
			}
			chart.Select(c, area, &a.selected, xs, describe)
			if i := a.selected; i >= 0 && i < n {
				hAlign := ui.Center
				switch {
				case xs[i] < 0.2:
					hAlign = ui.Start
				case xs[i] > 0.8:
					hAlign = ui.End
				}
				chart.Anchor(c, xs[i], 1, hAlign, ui.Start, func() {
					ui.Row(c).Gap(8).Padding(4, 8).Radius(6).FontSize(11).FontFeatures("tnum").
						Background(k.pop).Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.15)).Children(func() {
						for _, v := range []struct {
							n    float64
							unit string
						}{{s.visitors[i], plural(int(s.visitors[i]), "visitor", "visitors")}, {s.views[i], plural(int(s.views[i]), "view", "views")}} {
							ui.Row(c).Gap(3).Children(func() {
								ui.Text(c, count(v.n)).FontWeight(600).NoWrap()
								ui.Text(c, v.unit).TextColor(k.muted).NoWrap()
							})
						}
					})
				})
			}
		}).Label(fmt.Sprintf("Visitors and views by %s", sp.unit())).Height(150).GapX(8).GapY(6)
	})
}

func legend(c *ui.Context, color ui.Color, label string) {
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(2).Background(color)
		ui.Text(c, label).NoWrap()
	})
}

// breakdown lists the top values of one kind, each over a bar as long as
// its share of the first.
func breakdown(c *ui.Context, k ink, title, unit, kind string, rows []metric, loading bool) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).FontSize(11).FontWeight(500).TextColor(k.faint).Padding(0, 0, 2, 0).Children(func() {
			ui.Text(c, title).Grow(1)
			ui.Text(c, unit)
		})
		switch {
		case loading:
			for i := range 3 {
				ui.Row(c.Key(i)).Height(24).AlignItems(ui.Center).Children(func() {
					skeleton(c, k, float32(160-i*36), 10)
				})
			}
			return
		case len(rows) == 0:
			ui.Text(c, "Nothing in this range").FontSize(12).TextColor(k.faint).Padding(4, 0)
			return
		}
		top := math.Max(float64(rows[0].Y), 1)
		for i, m := range rows {
			ui.Box(c.Key(i)).Height(24).Children(func() {
				share := float32(float64(m.Y) / top * 100)
				ui.Box(c).Absolute().Top(0).Bottom(0).Left(0).WidthPercent(share).Radius(5).Background(k.accent.Alpha(0.1))
				ui.Row(c).Fill().Padding(0, 8).Gap(8).AlignItems(ui.Center).FontSize(12).Children(func() {
					label := pathLabel(kind, m.X)
					ui.Text(c, label).SingleLine().Grow(1).Shrink(1).Tooltip(m.X)
					ui.Text(c, count(float64(m.Y))).FontFeatures("tnum").TextColor(k.muted).NoWrap()
				})
			})
		}
	})
}
