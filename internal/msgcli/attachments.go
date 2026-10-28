package msgcli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CamiloCaceres/lolad/internal/amp"
	"github.com/CamiloCaceres/lolad/internal/core"
)

// transfer is the client for file bodies: large local files outlast the
// 15 s API timeout.
func (c *client) transfer() *http.Client {
	h := *c.http
	h.Timeout = 30 * time.Minute
	return &h
}

// upload streams one local file to the provider and returns the attachment
// object to put in the signed payload.
func (c *client) upload(path string) (core.Attachment, error) {
	var a core.Attachment
	f, e := os.Open(path)
	if e != nil {
		return a, e
	}
	defer f.Close()
	if st, e := f.Stat(); e != nil {
		return a, e
	} else if st.IsDir() {
		return a, fmt.Errorf("%s is a folder; zip it first", path)
	}
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if ct == "" {
		ct = "application/octet-stream"
	}
	r, e := http.NewRequestWithContext(c.ctx, "POST", c.base+"/v1/attachments/upload?filename="+url.QueryEscape(filepath.Base(path)), f)
	if e != nil {
		return a, e
	}
	r.Header.Set("Authorization", "Bearer "+c.credentials.APIKey)
	r.Header.Set("Content-Type", ct)
	resp, e := c.transfer().Do(r)
	if e != nil {
		return a, e
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var v struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v)
		if v.Message == "" {
			v.Message = resp.Status
		}
		return a, &serverError{status: resp.StatusCode, reason: filepath.Base(path) + ": " + v.Message}
	}
	return a, json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&a)
}
func (c *client) uploadAll(paths []string) ([]core.Attachment, error) {
	var out []core.Attachment
	for _, p := range paths {
		a, e := c.upload(p)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}

// agentHome is where received files go: the agent dir when lolad launched us.
func agentHome() string {
	if h := os.Getenv("LOLABOT_HOME"); h != "" {
		return h
	}
	wd, _ := os.Getwd()
	return wd
}

// ignoreAttachments keeps received files out of the agent's git history.
func ignoreAttachments(home string) {
	p := filepath.Join(home, ".gitignore")
	b, e := os.ReadFile(p)
	if e != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v := strings.TrimSpace(line); v == "attachments/" || v == "attachments" || v == "/attachments/" || v == "/attachments" {
			return
		}
	}
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	f, e := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
	if e != nil {
		return
	}
	_, _ = f.WriteString(prefix + "# lolad: files received with messages\nattachments/\n")
	_ = f.Close()
}

type saved struct {
	core.Attachment
	Path, Note string
}

// saveAttachments downloads a message's files into dest/<message id>/ and
// checks each SHA-256 digest. Files the scan marked suspicious or rejected
// are skipped unless force (a person has confirmed them).
func (c *client) saveAttachments(msgID string, list []core.Attachment, dest string, force bool) []saved {
	var out []saved
	dir := filepath.Join(dest, msgID)
	used := map[string]bool{}
	for _, a := range list {
		s := saved{Attachment: a}
		out = append(out, s)
		i := len(out) - 1
		if a.ScanStatus == "rejected" || (a.ScanStatus == "suspicious" && !force) {
			out[i].Note = "not saved: the scan marked it " + a.ScanStatus + "; ask the user before opening it (lolad msg download " + msgID + " --force)"
			continue
		}
		if a.Storage != "" && a.Storage != "provider" {
			out[i].Note = "not saved: " + a.Storage + " attachments are fetched with their own tool"
			continue
		}
		name := amp.SanitizeFilename(a.Filename)
		for n := 2; used[name]; n++ {
			ext := filepath.Ext(a.Filename)
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(amp.SanitizeFilename(a.Filename), ext), n, ext)
		}
		used[name] = true
		path := filepath.Join(dir, name)
		if have, e := fileDigest(path); e == nil && have == a.Digest {
			out[i].Path = path
			continue
		}
		if e := c.download(a, dir, path); e != nil {
			out[i].Note = "not saved: " + e.Error()
			continue
		}
		out[i].Path = path
	}
	return out
}
func fileDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
func (c *client) download(a core.Attachment, dir, path string) error {
	if !strings.HasPrefix(a.URL, "/v1/attachments/") {
		return fmt.Errorf("unsupported download URL")
	}
	r, e := http.NewRequestWithContext(c.ctx, "GET", c.base+a.URL, nil)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+c.credentials.APIKey)
	resp, e := c.transfer().Do(r)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(dir, ".download-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, a.Size+1))
	if ce := tmp.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != a.Digest {
		return fmt.Errorf("digest mismatch, file discarded")
	}
	return os.Rename(tmp.Name(), path)
}
func printAttachments(w io.Writer, list []saved) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(w, "\nAttachments (%d):\n", len(list))
	for _, s := range list {
		where := s.Path
		if where == "" {
			where = s.Note
		}
		fmt.Fprintf(w, "- %s (%s, %s): %s\n", s.Filename, s.ContentType, humanSize(s.Size), where)
	}
}
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
