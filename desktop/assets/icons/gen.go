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

// The mark is a periwinkle ring with a dot, on a 100-unit square: the same
// drawing as internal/web/static/logo.svg (viewBox 64: ring r 22, stroke 4.5,
// dot r 6.5, #7AA2FF with a soft glow), scaled by 100/64.
const logoR, logoHalf, logoDot = 22 * 100 / 64.0, 2.25 * 100 / 64.0, 6.5 * 100 / 64.0

// Tray geometry. The ring is thicker than the logo's so it stays a ring at
// 16 px (a 2 px stroke, a 4 px dot, a 2 px gap between them), and it sits a
// little low and left to leave the top right corner for the state badge, as
// the old orb did.
const trayX, trayY, trayR, trayHalf, trayDot = 46.0, 54.0, 33.0, 6.5, 12.0
const badgeX, badgeY = 80.0, 20.0

func sdRRect(x, y, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	in := math.Min(math.Max(dx, dy), 0)
	return out + in - r
}

// inMark is the one-colour silhouette for tray icons: ring and dot.
func inMark(x, y float64) bool {
	d := math.Hypot(x-trayX, y-trayY)
	return math.Abs(d-trayR) < trayHalf || d < trayDot
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

var periwinkle = hex(0x7AA2FF)

// smooth is a soft step for antialiased edges in mark units.
func smooth(d, w float64) float64 { return math.Max(0, math.Min(1, 0.5-d/w)) }

// glow is a soft falloff outside a shape, for the SVG's feGaussianBlur.
func glow(d, reach float64) float64 {
	if d <= 0 {
		return 1
	}
	if d >= reach {
		return 0
	}
	t := 1 - d/reach
	return t * t
}

// mark paints the full-colour mark (centred on 50, 50) over base. heavy
// (1 = the logo) thickens ring and dot for the small icon sizes.
func mark(base rgba, x, y, heavy float64) rgba {
	d := math.Hypot(x-50, y-50)
	ring := math.Abs(d-logoR) - logoHalf*heavy // signed distance to the ring
	dot := d - logoDot*math.Sqrt(heavy)
	sd := math.Min(ring, dot)
	c := over(base, rgba{periwinkle.r, periwinkle.g, periwinkle.b, 0.45 * glow(sd, 6)})
	p := periwinkle
	p.a = smooth(sd, 0.5)
	return over(c, p)
}

// tray draws the ring in ink. Needing you adds a dot at the top right (periwinkle
// on Windows: needs-you is periwinkle, amber is only for warnings); a problem
// adds a red "!" badge in the same place. A clear ring around the badge keeps
// it apart from the mark. halo, when set, outlines the needs-you dot in the
// taskbar's contrast colour, since periwinkle alone is faint on a light
// taskbar. On a template icon the badge is just more ink (alpha is all macOS
// uses).
func tray(state string, ink, badge, bang, halo rgba) func(x, y float64) rgba {
	return func(x, y float64) rgba {
		switch state {
		case "attention":
			if inCircle(x, y, badgeX, badgeY, 14) {
				return badge
			}
			if halo.a > 0 && inCircle(x, y, badgeX, badgeY, 18) {
				return halo
			}
			if inCircle(x, y, badgeX, badgeY, 23) {
				return none
			}
		case "problem":
			if inBang(x, y, badgeX, badgeY+1, 22) {
				return bang
			}
			if inCircle(x, y, badgeX, badgeY, 20) {
				return badge
			}
			if inCircle(x, y, badgeX, badgeY, 26) {
				return none
			}
		}
		if inMark(x, y) {
			return ink
		}
		return none
	}
}

func app(x, y float64) rgba { return appIcon(x, y, 1) }

// appSmall is the app icon for 32 px and below: at the logo's weight the ring
// would be about one pixel wide at 16 px.
func appSmall(x, y float64) rgba { return appIcon(x, y, 1.7) }

func appIcon(x, y, heavy float64) rgba {
	// macOS grid: 824pt tile on a 1024pt canvas.
	edge := sdRRect(x, y, 100/1024.0*100, 100/1024.0*100, 924/1024.0*100, 924/1024.0*100, 185/1024.0*100)
	if edge >= 0.15 {
		return none
	}
	// Midnight tile: the dashboard's background, with a soft glow behind the ring.
	bg := mix(hex(0x161A33), hex(0x07080F), (y-10)/80)
	if g := math.Hypot(x-50, y-50); g < 40 {
		bg = over(bg, rgba{0.42, 0.5, 1, 0.22 * (1 - g/40) * (1 - g/40)})
	}
	bg = over(bg, rgba{1, 1, 1, 0.08 * smooth(math.Abs(edge+0.4)-0.25, 0.4)})
	// The mark sits in the tile with generous padding.
	c := mark(bg, (x-50)/0.8+50, (y-50)/0.8+50, heavy)
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

// appAt picks the app icon drawing for a pixel size.
func appAt(n int) func(x, y float64) rgba {
	if n <= 32 {
		return appSmall
	}
	return app
}

// writeICO packs PNG frames of the app icon into an .ico (Vista+ PNG-in-ICO).
func writeICO(path string, sizes []int) {
	var frames [][]byte
	for _, s := range sizes {
		frames = append(frames, pngBytes(render(s, appAt(s))))
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
	writeICO("Lolad.ico", []int{16, 24, 32, 48, 64, 128, 256})

	for _, st := range []string{"normal", "attention", "problem"} {
		// macOS template: black + alpha only, 44px for a 22pt menu bar @2x.
		write("tray-mac-"+st+".png", render(44, tray(st, black, black, none, none)))
		// Windows: a white mark for dark taskbars and a dark one for light.
		badge := map[string]rgba{"attention": periwinkle, "problem": red}[st]
		write("tray-win-dark-"+st+".png", render(32, tray(st, white, badge, white, white)))
		write("tray-win-light-"+st+".png", render(32, tray(st, slate, badge, white, slate)))
	}

	// Lolad.icns through the macOS iconset tool, when available.
	if _, err := exec.LookPath("iconutil"); err == nil {
		set := "Lolad.iconset"
		must(os.MkdirAll(set, 0o755))
		for _, s := range []int{16, 32, 128, 256, 512} {
			write(filepath.Join(set, "icon_"+itoa(s)+"x"+itoa(s)+".png"), render(s, appAt(s)))
			write(filepath.Join(set, "icon_"+itoa(s)+"x"+itoa(s)+"@2x.png"), render(s*2, appAt(s*2)))
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
