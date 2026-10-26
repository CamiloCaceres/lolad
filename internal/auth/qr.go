package auth

import (
	"fmt"
	qrcode "github.com/CamiloCaceres/lolad/internal/auth/qr"
	"net/url"
	"strings"
)

func PairURL(publicURL, code string) string {
	return "lolad://pair?u=" + url.QueryEscape(strings.TrimRight(publicURL, "/")) + "&c=" + url.QueryEscape(code)
}

// QRSVG emits only fixed SVG elements and numeric coordinates; no user markup.
func QRSVG(value string) (string, error) {
	q, e := qrcode.New(value, qrcode.Medium)
	if e != nil {
		return "", e
	}
	b := q.Bitmap()
	n := len(b)
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" role="img" aria-label="Scan to pair this device" shape-rendering="crispEdges"><path fill="white" d="M0 0h%dv%dH0z"/><path fill="black" d="`, n, n, n, n)
	for y, row := range b {
		for x, on := range row {
			if on {
				fmt.Fprintf(&out, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	out.WriteString(`"/></svg>`)
	return out.String(), nil
}
