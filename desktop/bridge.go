package main

import (
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The webview has no download manager and drops target=_blank links, so the
// two things the dashboard does that need the OS (save an attachment, open an
// image full size) go through a tiny bridge: a click handler injected into the
// dashboard posts a line to Go, and Go validates it before acting.
//
// Message format: "lolad:<verb>\n<absolute url>[\n<filename>]".
const bridgeJS = `(()=>{
if(window.__loladBridge)return;window.__loladBridge=1;
const post=m=>{try{
  if(window.webkit&&window.webkit.messageHandlers&&window.webkit.messageHandlers.external){window.webkit.messageHandlers.external.postMessage(m);return true}
  if(window.chrome&&window.chrome.webview){window.chrome.webview.postMessage(m);return true}
}catch(_){}return false};
document.addEventListener('click',e=>{
  if(e.defaultPrevented||e.button!==0)return;
  const a=e.target&&e.target.closest&&e.target.closest('a[href]');
  if(!a)return;
  if(a.hasAttribute('download')){
    if(post('lolad:download\n'+a.href+'\n'+(a.getAttribute('download')||'')))e.preventDefault();
  }else if(a.target==='_blank'||(a.origin&&a.origin!==location.origin)){
    if(post('lolad:open\n'+a.href))e.preventDefault();
  }
});
})();`

// handleBridge receives raw messages from the page. base is the dashboard
// origin ("http://127.0.0.1:4100"); anything from another origin is ignored.
func handleBridge(app *application.App, base string) func(application.Window, string, *application.OriginInfo) {
	return func(_ application.Window, msg string, origin *application.OriginInfo) {
		if !strings.HasPrefix(msg, "lolad:") || origin == nil || !origin.IsMainFrame || originOf(origin.Origin) != base {
			return
		}
		parts := strings.SplitN(msg, "\n", 3)
		if len(parts) < 2 {
			return
		}
		target, err := neturl.Parse(parts[1])
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
			return
		}
		switch parts[0] {
		case "lolad:open":
			// Same-origin links open in the browser too: the image view is a
			// bare resource, not a dashboard page.
			_ = app.Browser.OpenURL(target.String())
		case "lolad:download":
			if originOf(target.String()) != base {
				return // only ever fetch from our own daemon
			}
			name := ""
			if len(parts) == 3 {
				name = parts[2]
			}
			go func() {
				path, err := download(target.String(), name)
				application.InvokeAsync(func() {
					if err != nil {
						app.Dialog.Error().SetTitle("Download failed").SetMessage(err.Error()).Show()
						return
					}
					reveal(path)
				})
			}()
		}
	}
}

func originOf(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// download saves url into ~/Downloads without overwriting anything. The name
// comes from the page, so only its last path element is kept.
func download(url, name string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daemon answered %s", resp.Status)
	}
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || name == "." || name == "/" || name == ".." {
		name = "attachment"
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads")
	_ = os.MkdirAll(dir, 0o755)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 2; ; i++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, cerr := io.Copy(f, resp.Body)
			if e := f.Close(); cerr == nil {
				cerr = e
			}
			if cerr != nil {
				_ = os.Remove(path)
				return "", cerr
			}
			return path, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// reveal shows the saved file in the file manager. It never opens it:
// attachments come from other agents and may be flagged as suspicious.
func reveal(path string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", "-R", path).Start()
	case "windows":
		_ = exec.Command("explorer", "/select,"+path).Start()
	}
}
