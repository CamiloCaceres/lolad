//go:build ignore

// gen draws the Lolad icons from code (no third-party art): the app icon, the
// macOS template tray icons and the Windows light/dark tray icons, then writes
// Lolad.ico. Lolad.icns is built from the 1024 PNG by `iconutil` on macOS.
//
//	cd desktop/assets/icons && go run gen.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

// The mark is an assistant orb with a silk band of light across it, and the
// amber "your turn" moon at its top right, on a 100-unit square. It is the
// same drawing as internal/web/static/logo.svg.
const orbX, orbY, orbR = 44.0, 56.0, 34.0
const moonX, moonY, moonR = 81.0, 21.0, 9.0

func sdRRect(x, y, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	in := math.Min(math.Max(dx, dy), 0)
	return out + in - r
}

// curve samples a cubic Bézier into a polyline.
func curve(x0, y0, x1, y1, x2, y2, x3, y3 float64) [][2]float64 {
	var pts [][2]float64
	for i := 0; i <= 48; i++ {
		t := float64(i) / 48
		u := 1 - t
		pts = append(pts, [2]float64{
			u*u*u*x0 + 3*u*u*t*x1 + 3*u*t*t*x2 + t*t*t*x3,
			u*u*u*y0 + 3*u*u*t*y1 + 3*u*t*t*y2 + t*t*t*y3,
		})
	}
	return pts
}

var (
	band1 = curve(6, 78, 26, 58, 52, 54, 84, 62)
	band2 = curve(10, 92, 32, 74, 58, 72, 86, 80)
)

// distLine is the distance from (x, y) to a polyline, and how far along it
// (0..1) the nearest point lies.
func distLine(pts [][2]float64, x, y float64) (float64, float64) {
	best, at := math.Inf(1), 0.0
	for i := 1; i < len(pts); i++ {
		ax, ay, bx, by := pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1]
		dx, dy := bx-ax, by-ay
		t := math.Max(0, math.Min(1, ((x-ax)*dx+(y-ay)*dy)/(dx*dx+dy*dy)))
		if d := math.Hypot(x-ax-t*dx, y-ay-t*dy); d < best {
			best, at = d, (float64(i-1)+t)/float64(len(pts)-1)
		}
	}
	return best, at
}

// inMark is the one-colour silhouette for tray icons: the orb with the band
// cut out of it, so the band still reads at 22 points.
func inMark(x, y float64) bool {
	if math.Hypot(x-orbX, y-orbY) >= orbR {
		return false
	}
	d, _ := distLine(band1, x, y)
	return d > 4.2
}

func inCircle(x, y, cx, cy, r float64) bool { return math.Hypot(x-cx, y-cy) < r }

// bang is an exclamation mark centred on (cx, cy), height h.
func inBang(x, y, cx, cy, h float64) bool {
	w := h * 0.17
	return sdRRect(x, y, cx-w, cy-h*0.42, cx+w, cy+h*0.1, w) < 0 || inCircle(x, y, cx, cy+h*0.32, w*1.1)
}

type rgba struct{ r, g, b, a float64 }

// render samples fn on an n×n grid, 4×4 per pixel, and averages premultiplied.
func render(n int, fn func(x, y float64) rgba) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	const ss = 4
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+.5)/ss) / float64(n) * 100
					y := (float64(py) + (float64(sy)+.5)/ss) / float64(n) * 100
					c := fn(x, y)
					r += c.r * c.a
					g += c.g * c.a
					b += c.b * c.a
					a += c.a
				}
			}
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{uint8(r / a * 255), uint8(g / a * 255), uint8(b / a * 255), uint8(a / ss / ss * 255)})
			}
		}
	}
	return img
}

var (
	none  = rgba{}
	black = rgba{0, 0, 0, 1}
	white = rgba{1, 1, 1, 1}
	amber = rgba{1, 0.71, 0.28, 1} // --you #FFB547
	red   = rgba{1, 0.36, 0.48, 1} // --st-failed #FF5C7A
	slate = rgba{0.09, 0.10, 0.13, 1}
)

func hex(v uint32) rgba {
	return rgba{float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255, 1}
}

func mix(a, b rgba, t float64) rgba {
	return rgba{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

// over composites c (with its own alpha) onto an opaque base.
func over(base, c rgba) rgba {
	return rgba{base.r + (c.r-base.r)*c.a, base.g + (c.g-base.g)*c.a, base.b + (c.b-base.b)*c.a, base.a + (1-base.a)*c.a}
}

type stop struct {
	at float64
	c  rgba
}

func ramp(stops []stop, t float64) rgba {
	if t <= stops[0].at {
		return stops[0].c
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].at {
			return mix(stops[i-1].c, stops[i].c, (t-stops[i-1].at)/(stops[i].at-stops[i-1].at))
		}
	}
	return stops[len(stops)-1].c
}

var (
	orbStops  = []stop{{0, hex(0xEEF3FF)}, {.2, hex(0x9FB8FF)}, {.5, hex(0x6C7DFF)}, {.78, hex(0x7B4FE0)}, {1, hex(0x241A5E)}}
	moonStops = []stop{{0, hex(0xFFE7BD)}, {.5, hex(0xFFB547)}, {1, hex(0xE5841A)}}
	aqua      = hex(0x3DE8D0)
	frost     = hex(0xDCE6FF)
)

// smooth is a soft step for antialiased edges in mark units.
func smooth(d, w float64) float64 { return math.Max(0, math.Min(1, 0.5-d/w)) }

// mark paints the full-colour mark over base at (x, y).
func mark(base rgba, x, y float64) rgba {
	c := base
	// moon glow, then the orb, then the moon itself
	if g := math.Hypot(x-moonX, y-moonY); g < 14 {
		c = over(c, rgba{1, 0.71, 0.28, 0.16 * smooth(g-14, 1.2)})
	}
	if d := math.Hypot(x-orbX, y-orbY); d < orbR+1 {
		// radial light from the upper left, like the SVG's radialGradient
		t := math.Hypot(x-(orbX-orbR*0.32), y-(orbY-orbR*0.4)) / (1.6 * orbR)
		o := ramp(orbStops, t)
		for _, b := range []struct {
			pts         [][2]float64
			half, alpha float64
		}{{band1, 4.5, 0.85}, {band2, 2, 0.47}} {
			bd, along := distLine(b.pts, x, y)
			// fade in from the left end, out towards the right end
			a := b.alpha * smooth(bd-b.half, 1.6) * math.Min(along/0.45, (1-along)/0.55)
			col := mix(aqua, frost, math.Max(0, along-0.45)/0.55)
			o = over(o, rgba{col.r, col.g, col.b, math.Max(0, a)})
		}
		o = over(o, rgba{frost.r, frost.g, frost.b, 0.18 * smooth(math.Abs(d-(orbR-0.8))-0.8, 0.8)})
		o.a = smooth(d-orbR, 1.0)
		c = over(c, o)
	}
	if d := math.Hypot(x-moonX, y-moonY); d < moonR+1 {
		m := ramp(moonStops, math.Hypot(x-(moonX-moonR*0.24), y-(moonY-moonR*0.28))/(1.24*moonR))
		m.a = smooth(d-moonR, 1.0)
		c = over(c, m)
	}
	return c
}

// tray draws the orb in ink. Needing you adds the moon; a problem adds a "!"
// badge in the same place. A clear ring
// around the badge keeps it readable; on a template icon the badge is just
// more ink (alpha is all macOS uses).
func tray(state string, ink, badge, bang rgba) func(x, y float64) rgba {
	return func(x, y float64) rgba {
		switch state {
		case "attention":
			// the amber moon: your turn
			if inCircle(x, y, moonX, moonY, 14) {
				return badge
			}
			if inCircle(x, y, moonX, moonY, 19) {
				return none
			}
		case "problem":
			if inBang(x, y, 79, 21, 22) {
				return bang
			}
			if inCircle(x, y, 79, 21, 20) {
				return badge
			}
			if inCircle(x, y, 79, 21, 26) {
				return none
			}
		}
		if inMark(x, y) {
			return ink
		}
		return none
	}
}

func app(x, y float64) rgba {
	// macOS grid: 824pt tile on a 1024pt canvas.
	edge := sdRRect(x, y, 100/1024.0*100, 100/1024.0*100, 924/1024.0*100, 924/1024.0*100, 185/1024.0*100)
	if edge >= 0.15 {
		return none
	}
	// Midnight tile: the dashboard's background, with a soft glow behind the orb.
	bg := mix(hex(0x161A33), hex(0x07080F), (y-10)/80)
	if g := math.Hypot(x-46, y-54); g < 40 {
		bg = over(bg, rgba{0.42, 0.5, 1, 0.22 * (1 - g/40) * (1 - g/40)})
	}
	bg = over(bg, rgba{1, 1, 1, 0.08 * smooth(math.Abs(edge+0.4)-0.25, 0.4)})
	// The mark sits in the tile with generous padding.
	c := mark(bg, (x-50)/0.66+50, (y-50)/0.66+50)
	c.a = smooth(edge, 0.3)
	return c
}

func write(path string, img image.Image) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	must(os.WriteFile(path, b.Bytes(), 0o644))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	must(png.Encode(&b, img))
	return b.Bytes()
}

// writeICO packs PNG frames into an .ico (Vista+ PNG-in-ICO).
func writeICO(path string, sizes []int, fn func(x, y float64) rgba) {
	var frames [][]byte
	for _, s := range sizes {
		frames = append(frames, pngBytes(render(s, fn)))
	}
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		d := byte(s)
		if s >= 256 {
			d = 0
		}
		b.Write([]byte{d, d, 0, 0})
		binary.Write(&b, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&b, binary.LittleEndian, uint32(len(frames[i])))
		binary.Write(&b, binary.LittleEndian, uint32(off))
		off += len(frames[i])
	}
	for _, f := range frames {
		b.Write(f)
	}
	must(os.WriteFile(path, b.Bytes(), 0o644))
}

func main() {
	write("app-1024.png", render(1024, app))
	writeICO("Lolad.ico", []int{16, 24, 32, 48, 64, 128, 256}, app)

	for _, st := range []string{"normal", "attention", "problem"} {
		// macOS template: black + alpha only, 44px for a 22pt menu bar @2x.
		write("tray-mac-"+st+".png", render(44, tray(st, black, black, none)))
		// Windows: a white mark for dark taskbars and a dark one for light.
		write("tray-win-dark-"+st+".png", render(32, tray(st, white, map[string]rgba{"attention": amber, "problem": red}[st], white)))
		write("tray-win-light-"+st+".png", render(32, tray(st, slate, map[string]rgba{"attention": amber, "problem": red}[st], white)))
	}

	// Lolad.icns through the macOS iconset tool, when available.
	if _, err := exec.LookPath("iconutil"); err == nil {
		set := "Lolad.iconset"
		must(os.MkdirAll(set, 0o755))
		for _, s := range []int{16, 32, 128, 256, 512} {
			write(filepath.Join(set, "icon_"+itoa(s)+"x"+itoa(s)+".png"), render(s, app))
			write(filepath.Join(set, "icon_"+itoa(s)+"x"+itoa(s)+"@2x.png"), render(s*2, app))
		}
		must(exec.Command("iconutil", "-c", "icns", set, "-o", "Lolad.icns").Run())
		must(os.RemoveAll(set))
	}
}

func itoa(n int) string {
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
