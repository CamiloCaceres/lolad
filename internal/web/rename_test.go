package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CamiloCaceres/lolad/internal/web"
	"github.com/CamiloCaceres/lolad/internal/web/fake"
)

// Settings › Your Lolad's name: the row, the live check, what a rename ends,
// "Rename anyway" when mail is still on the way, the wait, and the result.
func TestRenameLolad(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	n, err := f.LoladName(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	me := f.Me().Name

	_, body, _ := do("GET", "/settings", nil)
	for _, want := range []string{"Your Lolad’s name", n.Name, me + "@" + n.Provider, `href="/settings/name"`, "Rename…"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings lack %q", want)
		}
	}

	_, body, _ = do("GET", "/settings/name", nil)
	for _, want := range []string{"Rename this Lolad", "What renaming changes", "You’re connected with", "Renaming ends"} {
		if !strings.Contains(body, want) {
			t.Errorf("rename screen lacks %q", want)
		}
	}
	if strings.Contains(body, "tenant") || strings.Contains(body, "provider") {
		t.Error("plain words only")
	}

	if _, frag, _ := do("GET", "/settings/name/check?name="+url.QueryEscape("Milo PC"), nil); !strings.Contains(frag, "is-ok") || !strings.Contains(frag, "Addresses will look like "+me+"@milo-pc.lolad.local") {
		t.Errorf("check ok:\n%s", frag)
	}
	if _, frag, _ := do("GET", "/settings/name/check?name="+url.QueryEscape(n.Name), nil); !strings.Contains(frag, "is-no") {
		t.Errorf("same name should be refused:\n%s", frag)
	}

	code, body, _ := do("POST", "/settings/name", url.Values{"name": {"Milo PC"}})
	if n.Outbox > 0 {
		if code != http.StatusConflict || !strings.Contains(body, "Rename anyway") || !strings.Contains(body, "won’t be sent") {
			t.Fatalf("outbox: %d\n%s", code, body)
		}
		code, body, _ = do("POST", "/settings/name", url.Values{"name": {"Milo PC"}, "anyway": {"1"}})
	}
	if code != http.StatusAccepted || !strings.Contains(body, "Renaming…") || !strings.Contains(body, `hx-get="/settings/name/wait"`) {
		t.Fatalf("rename: %d\n%s", code, body)
	}
	if code, _, h := do("GET", "/settings/name/wait", nil); code != http.StatusNoContent || h.Get("HX-Redirect") != "/settings/name?done=1" {
		t.Fatalf("wait: %d %v", code, h)
	}
	if _, body, _ = do("GET", "/settings/name?done=1", nil); !strings.Contains(body, "Renamed to <span class=\"mono\">milo-pc</span>") {
		t.Errorf("done:\n%s", body)
	}
}

// The same-name refusal on Sharing offers "Rename this lolad".
func TestSameNameOffersRename(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	n, _ := f.LoladName(context.Background())
	link := fake.FriendLink("Ana", n.Name, nil, time.Now().Add(time.Hour), false)
	_, body, _ := do("POST", "/connections/friends/preview", url.Values{"link": {link}})
	if !strings.Contains(body, "same name") || !strings.Contains(body, `href="/settings/name"`) || !strings.Contains(body, "Rename this lolad") {
		t.Fatalf("preview:\n%s", body)
	}
}

// When lolad isn't restarted for you, the screen says so and doesn't poll;
// a rename that rolled back says what happened and that nothing changed.
func TestRenameManualAndFailed(t *testing.T) {
	f, do := friendSetup(t, web.Options{})
	f.SetRenameRestart("manual")
	code, body, _ := do("POST", "/settings/name", url.Values{"name": {"studio"}, "anyway": {"1"}})
	if code != http.StatusAccepted || !strings.Contains(body, "Restart lolad to finish.") || strings.Contains(body, "/settings/name/wait") {
		t.Fatalf("manual: %d\n%s", code, body)
	}
	if _, body, _ = do("GET", "/settings/name", nil); !strings.Contains(body, "Restart lolad to finish.") {
		t.Error("a pending rename keeps saying how to finish it")
	}
	f.FailLastRename("studio", "the backup couldn’t be written")
	if _, body, _ = do("GET", "/settings/name?done=1", nil); !strings.Contains(body, "Rename didn’t finish") || !strings.Contains(body, "the backup couldn’t be written. Nothing changed") {
		t.Errorf("failed:\n%s", body)
	}
}
