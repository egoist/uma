package main

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

// settings is the page that connects to Umami and chooses what to show.
// Before any key is saved, it is the only page.
func (a *app) settings(c *ui.Context, k ink) {
	connected := a.configured()
	if connected {
		ui.Row(c).Padding(12, 12, 4, 8).Children(func() {
			textButton(c.Key("back"), k, "Websites", iconBack, true).OnClick(a.back)
		})
	}
	top := float32(4)
	if !connected {
		top = 28
	}
	ui.Scroll(c.Key("settings")).Grow(1).Padding(top, gutter, gutter, gutter).Gap(22).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			if !connected {
				mark(c, k).Margin(0, 0, 12, 0)
				ui.Text(c, "Connect to Umami").FontSize(17).FontWeight(650)
				ui.Text(c, "Your websites and their visitors, a click away in the menu bar. Create an API key in Umami under Settings → API keys, and paste it here.").
					FontSize(12).TextColor(k.muted).LineHeight(1.4)
			} else {
				ui.Text(c, "Settings").FontSize(17).FontWeight(650)
			}
		})

		title := "Account"
		if !connected {
			title = ""
		}
		section(c, k, title, func() {
			field(c, k, "API endpoint", "Umami Cloud's, or your own server's address.", func() {
				ui.TextInput(c.Key("endpoint"), &a.editEndpoint).Label("API endpoint").Placeholder(cloudEndpoint).
					Disabled(a.connecting).OnSubmit(a.save)
			})
			field(c, k, "API key", "", func() {
				in := ui.TextInput(c.Key("key"), &a.editKey).Label("API key").Placeholder("api_…").Password().
					Disabled(a.connecting).OnSubmit(a.save)
				if !connected {
					in.AutoFocus()
				}
			})
			if a.connectErr != "" {
				ui.Row(c).Gap(6).FontSize(12).TextColor(k.down).Children(func() {
					ui.Icon(c, iconAlert).Margin(1, 0, 0, 0)
					ui.Text(c, a.connectErr).Grow(1).LineHeight(1.35)
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(2, 0, 0, 0).Children(func() {
				label := "Connect"
				if connected {
					label = "Save"
				}
				dirty := a.editKey != a.cfg.Key || normalizeEndpoint(a.editEndpoint) != a.cfg.Endpoint
				ui.PrimaryButton(c.Key("connect"), label).Disabled(a.connecting || (connected && !dirty) || a.editKey == "").OnClick(a.save)
				if a.connecting {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, "Checking the key…").FontSize(12).TextColor(k.muted)
				}
				ui.Spacer(c)
				if connected && !a.connecting {
					textButton(c.Key("disconnect"), k, "Disconnect", nil, false).OnClick(a.disconnect)
				}
			})
		})

		if connected {
			section(c, k, "Menu bar", func() {
				show := !a.cfg.HideLive
				switchRow(c.Key("live"), k, "Visitors online", "Show how many people are on your websites now.", &show, func() {
					a.cfg.HideLive = !show
					a.changed(a.cfg)
				})
			})
			if len(a.sites) > 0 {
				section(c, k, "Websites", func() {
					mode := 0
					if a.cfg.CustomOrder {
						mode = 1
					}
					ui.Segmented(c.Key("order-mode"), &mode, "Most visitors first", "Your order").Label("Order").AlignSelf(ui.Start).
						OnChange(func() { a.setCustomOrder(mode == 1) })
					ui.Text(c, "Drag websites to put them in your order, here or in the list.").FontSize(11).TextColor(k.muted)
					all := ids(a.ordered())
					list := ui.Column(c.Key("all-sites")).Margin(0, -6).Label("Websites")
					list.Children(func() {
						for i, id := range all {
							s := a.site(id)
							shown := !slices.Contains(a.cfg.Hidden, id)
							row := ui.Row(c.Key(id)).Gap(8).Padding(5, 6).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorGrab)
							a.reorderable(row, k, all, i, a.reorderAll)
							row.Children(func() {
								ui.Icon(c, iconGrip).FontSize(14).TextColor(k.faint)
								ui.Column(c).Grow(1).Shrink(1).Gap(1).Children(func() {
									ui.Text(c, s.Name).SingleLine()
									ui.Text(c, s.Domain).FontSize(11).TextColor(k.muted).SingleLine()
								})
								ui.Switch(c, &shown).Label("Show " + s.Name).OnChange(func() { a.toggleHidden(id) })
							})
						}
					})
					if v, ok := ui.Drop[siteDrag](list); ok {
						a.drop(all, string(v), len(all), a.reorderAll)
					}
				})
			}
		}
	})
	ui.Row(c).Padding(10, gutter, 12, gutter).Gap(8).AlignItems(ui.Center).FontSize(11).TextColor(k.faint).
		BorderWidth(1, 0, 0, 0).BorderColor(k.line).Children(func() {
		ui.Text(c, "uma "+version).Grow(1)
		textButton(c.Key("quit"), k, "Quit", nil, false).OnClick(a.quit)
	})
}

func (a *app) save() {
	if a.connecting || a.editKey == "" {
		return
	}
	a.connect(func(endpoint, key string) source { return newClient(endpoint, key) })
}

func section(c *ui.Context, k ink, title string, build func()) {
	ui.Column(c).Gap(10).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(11).FontWeight(500).TextColor(k.faint)
		}
		build()
	})
}

func field(c *ui.Context, k ink, label, hint string, input func()) {
	ui.Column(c).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500)
		input()
		if hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(k.muted).LineHeight(1.35)
		}
	})
}

// switchRow is a setting turned on and off, with its name and what it
// does.
func switchRow(c *ui.Context, k ink, label, detail string, on *bool, changed func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		// The label toggles the switch too.
		ui.Column(c).Grow(1).Shrink(1).Gap(1).OnClick(func() { *on = !*on; changed() }).Children(func() {
			ui.Text(c, label).SingleLine()
			if detail != "" {
				ui.Text(c, detail).FontSize(11).TextColor(k.muted).SingleLine()
			}
		})
		ui.Switch(c, on).Label(label).OnChange(changed)
	})
}

// mark is the app's mark: the bars of its menu bar icon, on a tile.
func mark(c *ui.Context, k ink) ui.Element {
	tile := ui.Row(c).Size(44, 44).Radius(11).Background(k.accent).Padding(11, 0, 11, 0).
		Gap(4).Justify(ui.Center).AlignItems(ui.End).Shadow(0, 2, 6, 0, k.accent.Alpha(0.3))
	tile.Children(func() {
		for _, h := range []float32{10, 16, 22} {
			ui.Box(c).Size(6, h).Radius(3).Background(ui.RGB(255, 255, 255))
		}
	})
	return tile.AlignSelf(ui.Start)
}
