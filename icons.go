package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"

	"github.com/egoist/mygo/ui"
)

// Icons, in the manner of Lucide: strokes in the color of the text.
func icon(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var (
	iconRefresh  = icon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`)
	iconSettings = icon(`<path d="M20 7h-9"/><path d="M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`)
	iconBack     = icon(`<path d="m15 18-6-6 6-6"/>`)
	iconExternal = icon(`<path d="M7 7h10v10"/><path d="M7 17 17 7"/>`)
	iconUp       = icon(`<path d="M12 19V5"/><path d="m5 12 7-7 7 7"/>`)
	iconDown     = icon(`<path d="M12 5v14"/><path d="m19 12-7 7-7-7"/>`)
	iconChevron  = icon(`<path d="m6 9 6 6 6-6"/>`)
	iconGrip     = icon(`<circle cx="9" cy="5" r="1"/><circle cx="9" cy="12" r="1"/><circle cx="9" cy="19" r="1"/><circle cx="15" cy="5" r="1"/><circle cx="15" cy="12" r="1"/><circle cx="15" cy="19" r="1"/>`)
	iconAlert    = icon(`<circle cx="12" cy="12" r="10"/><path d="M12 8v4"/><path d="M12 16h.01"/>`)
)

// trayIcon draws three rising bars, 18 points high at twice the scale:
// black on transparent for macOS to tint, a gray that shows on light and
// dark taskbars elsewhere.
func trayIcon() []byte {
	const size = 36
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	ink := color.NRGBA{R: 128, G: 128, B: 128, A: 255}
	if runtime.GOOS == "darwin" {
		ink = color.NRGBA{A: 255}
	}
	// Bars 6 wide with rounded ends, 4 apart, from the baseline at 30.
	bars := []struct{ x, top float64 }{{7, 19}, {15, 12}, {23, 5}}
	for y := range size {
		for x := range size {
			// Supersample each pixel 4x4 for smooth ends.
			cover := 0
			for sy := range 4 {
				for sx := range 4 {
					px, py := float64(x)+(float64(sx)+0.5)/4, float64(y)+(float64(sy)+0.5)/4
					for _, b := range bars {
						if inBar(px, py, b.x, b.top, 6, 31) {
							cover++
							break
						}
					}
				}
			}
			if cover > 0 {
				c := ink
				c.A = uint8(int(ink.A) * cover / 16)
				img.SetNRGBA(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// inBar reports whether (px, py) is in a bar from x to x+w and top to
// bottom whose ends are round.
func inBar(px, py, x, top, w, bottom float64) bool {
	r := w / 2
	cx := x + r
	if px < x || px > x+w || py > bottom || py < top {
		return false
	}
	if py < top+r {
		return math.Hypot(px-cx, py-(top+r)) <= r
	}
	if py > bottom-r {
		return math.Hypot(px-cx, py-(bottom-r)) <= r
	}
	return true
}
