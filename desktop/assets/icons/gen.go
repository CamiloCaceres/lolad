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

// The mark is the word "lolad" reduced to "l o l": two bars and a ring, on a
// 100-unit square. Baseline y=78, ascender top y=22.
func sdRRect(x, y, x0, y0, x1, y1, r float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	dx, dy := math.Abs(x-cx)-hx, math.Abs(y-cy)-hy
	out := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	in := math.Min(math.Max(dx, dy), 0)
	return out + in - r
}

func inMark(x, y float64) bool {
	if sdRRect(x, y, 14, 22, 24.5, 78, 5.2) < 0 || sdRRect(x, y, 75.5, 22, 86, 78, 5.2) < 0 {
		return true
	}
	d := math.Hypot(x-50, y-61)
	return d < 17.5 && d > 8.5
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
	none   = rgba{}
	black  = rgba{0, 0, 0, 1}
	white  = rgba{1, 1, 1, 1}
	amber  = rgba{0.96, 0.62, 0.04, 1}
	red    = rgba{0.94, 0.27, 0.27, 1}
	slate  = rgba{0.09, 0.10, 0.13, 1}
	indigo = rgba{0.33, 0.30, 0.90, 1}
	violet = rgba{0.55, 0.30, 0.92, 1}
)

// tray draws the mark in ink, with the state badge top right. A clear ring
// around the badge keeps it readable; on a template icon the badge is just
// more ink (alpha is all macOS uses).
func tray(state string, ink, badge, bang rgba) func(x, y float64) rgba {
	return func(x, y float64) rgba {
		switch state {
		case "attention":
			if inCircle(x, y, 79, 21, 15) {
				return badge
			}
			if inCircle(x, y, 79, 21, 21) {
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
	if sdRRect(x, y, 100/1024.0*100, 100/1024.0*100, 924/1024.0*100, 924/1024.0*100, 185/1024.0*100) >= 0 {
		return none
	}
	t := (x + y) / 200
	g := rgba{indigo.r + (violet.r-indigo.r)*t, indigo.g + (violet.g-indigo.g)*t, indigo.b + (violet.b-indigo.b)*t, 1}
	// Scale the mark to sit in the tile with generous padding.
	mx, my := (x-50)*1.18+50, (y-50)*1.18+50
	if inMark(mx, my) {
		return white
	}
	return g
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
