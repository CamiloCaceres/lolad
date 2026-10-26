package auth

import (
	qrcode "github.com/CamiloCaceres/lolad/internal/auth/qr"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPairURLAndSafeSVG(t *testing.T) {
	value := PairURL("https://mac.example.ts.net:4100", "ABCD2345")
	if value != "lolad://pair?u=https%3A%2F%2Fmac.example.ts.net%3A4100&c=ABCD2345" {
		t.Fatal(value)
	}
	svg, e := QRSVG(value)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(svg, "<svg") || !strings.Contains(svg, `fill="white"`) {
		t.Fatal("bad SVG")
	}
	malicious := `<script>alert("x")</script>`
	svg, e = QRSVG(malicious)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(svg, "script") || strings.Contains(svg, "alert") {
		t.Fatal("untrusted SVG markup")
	}
}

// Apple's independent Vision decoder verifies the actual pairing URI end to end.
// Portable upstream byte/alphanumeric/Reed-Solomon vectors also run in qr/... .
func TestQRRoundTrip(t *testing.T) {
	if os.Getenv("LOLAD_QR_VISION_TEST") != "1" {
		t.Skip("set LOLAD_QR_VISION_TEST=1 for the optional macOS Vision integration check; portable QR vectors always run")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("Vision decoder is macOS-only; portable encoder vectors run separately")
	}
	swift, e := exec.LookPath("swift")
	if e != nil {
		t.Skip("Swift/Vision not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pair.png")
	value := PairURL("https://mac.example.ts.net:4100", "ABCD2345")
	q, e := qrcode.New(value, qrcode.Medium)
	if e != nil {
		t.Fatal(e)
	}
	if e = q.WriteFile(768, path); e != nil {
		t.Fatal(e)
	}
	helper := filepath.Join(dir, "decode.swift")
	e = os.WriteFile(helper, []byte(`import Foundation
import Vision
import ImageIO
let source = CGImageSourceCreateWithURL(URL(fileURLWithPath:CommandLine.arguments[1]) as CFURL, nil)!
let image = CGImageSourceCreateImageAtIndex(source, 0, nil)!
let request = VNDetectBarcodesRequest()
request.symbologies = [.qr]
request.usesCPUOnly = true
try VNImageRequestHandler(cgImage:image).perform([request])
print(request.results?.first?.payloadStringValue ?? "")
`), 0600)
	if e != nil {
		t.Fatal(e)
	}
	b, e := exec.CommandContext(t.Context(), swift, helper, path).Output()
	if e != nil {
		t.Fatalf("Vision helper: %v: %s", e, b)
	}
	if strings.TrimSpace(string(b)) != value {
		t.Fatalf("round trip mismatch: %q", b)
	}
}
