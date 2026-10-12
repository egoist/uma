// Uma shows the websites of an Umami account, and their visitors, in a
// panel of the menu bar.
package main

import (
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const version = "0.1.0"

// shell is what lives as long as the app: the tray icon, the panel while
// it is open, and the state the panel shows.
type shell struct {
	a     *app
	tray  *mygo.Tray
	panel *mygo.Window // the open panel, or nil
	live  int

	saving   sync.Mutex
	savedKey string
}

func (s *shell) ready() {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	cfg := loadConfig(dir)
	s.savedKey = cfg.Key
	var src source
	if cfg.Key != "" {
		src = newClient(cfg.Endpoint, cfg.Key)
	}
	a := newApp(cfg, src)
	s.a = a
	a.post = func(fn func()) {
		mygo.RunOnMain(func() {
			fn()
			if s.panel != nil {
				s.panel.Update(func() {})
			}
		})
	}
	a.spawn = func(fn func()) { go fn() }
	a.quit = mygo.App.Quit
	a.onLive = func(n int) { s.live = n; s.updateTray() }
	a.changed = func(cfg config) {
		s.updateTray()
		go s.save(dir, cfg)
	}

	s.tray, err = mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "Umami",
	})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Uma", Click: func(*mygo.MenuItem, *mygo.Window) { s.toggle() }},
		{Label: "Refresh", Click: func(*mygo.MenuItem, *mygo.Window) { a.refresh() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	if runtime.GOOS == "linux" {
		// AppIndicator only shows menus, and tells nothing of clicks.
		s.tray.SetMenu(menu)
	} else {
		s.tray.OnClick(s.toggle)
		s.tray.OnRightClick(func() {
			if s.panel != nil {
				s.panel.Close()
			}
			// The menu for this popup only, so that clicks keep toggling
			// the panel.
			s.tray.SetMenu(menu)
			defer s.tray.SetMenu(nil)
			s.tray.PopUpMenu()
		})
	}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { s.tray.Destroy() })

	a.refresh()
	// Visitors online, for the menu bar, every minute; the rest every
	// five, or as the panel opens on numbers older than a minute.
	go func() {
		tick := time.NewTicker(time.Minute)
		for n := 1; ; n++ {
			<-tick.C
			mygo.RunOnMain(func() {
				if n%5 == 0 {
					a.refresh()
				} else {
					a.refreshActive()
				}
			})
		}
	}()
}

// toggle opens the panel below the icon, or closes it.
func (s *shell) toggle() {
	if s.panel != nil {
		s.panel.Close()
		return
	}
	if s.a.stale() && s.a.loading == 0 {
		s.a.refresh()
	}
	s.panel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      s.tray,
		Placement: mygo.PlacementBottom,
		Gap:       4,
		Width:     panelWidth,
		Height:    panelHeight,
		Focusable: true,
		Shadow:    true,
		Popover:   true,
		Content:   ui.View(s.a.view),
	})
	s.panel.OnClosed(func() {
		s.panel = nil
		s.a.selected = -1
	})
}

// updateTray shows the count of visitors online beside the icon.
func (s *shell) updateTray() {
	if s.tray == nil {
		return
	}
	title, tip := "", "Umami"
	if s.a.configured() && !s.a.cfg.HideLive && s.live > 0 {
		title = fmt.Sprint(s.live)
	}
	if s.a.configured() {
		tip = fmt.Sprintf("%d %s online", s.live, plural(s.live, "visitor", "visitors"))
	}
	s.tray.SetTitle(title)
	s.tray.SetToolTip(tip)
}

// save writes the config, the key to the keychain only as it changes.
func (s *shell) save(dir string, cfg config) {
	s.saving.Lock()
	defer s.saving.Unlock()
	if cfg.Key == s.savedKey && runtime.GOOS == "darwin" {
		// Leave the keychain alone: it has this key.
		cfg.Key = ""
		if err := writeConfig(dir, cfg); err != nil {
			log.Print("config: ", err)
		}
		return
	}
	if err := saveConfig(dir, cfg); err != nil {
		log.Print("config: ", err)
		return
	}
	s.savedKey = cfg.Key
}

func main() {
	s := &shell{}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// The panel is the app's only window: keep running as it closes.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(s.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
