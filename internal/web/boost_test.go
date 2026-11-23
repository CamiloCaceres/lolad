package web_test

import (
	"regexp"
	"strings"
	"testing"
)

// Regression: a boosted link (or plain form) inside an element with hx-target
// inherits that target, so the whole next page, sidebar included, was swapped
// into the element ("Talk to" on an assistant's page gave two sidebars).
// Every boosted link and form on every screen must be free of an inherited
// hx-target, unless an ancestor blocks it with hx-disinherit.
func TestBoostedLinksInheritNoTarget(t *testing.T) {
	e := setup(t)
	pages := []string{
		"/", "/agents", "/agents/forge", "/agents/lola", "/agents/forge/settings",
		"/inbox/forge", "/inbox/lola", "/org", "/connections", "/settings",
		"/traffic", "/new", "/devices", "/email",
	}
	for _, p := range pages {
		resp, page := e.do("GET", p, nil, false)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", p, resp.StatusCode)
			continue
		}
		for _, bad := range inheritedTargets(page) {
			t.Errorf("%s: %s", p, bad)
		}
	}
}

var (
	tagRe  = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	rawRe  = regexp.MustCompile(`(?is)<(script|style|textarea)\b[^>]*>.*?</(script|style|textarea)>`)
	voidEl = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}
)

func attr(attrs, name string) (string, bool) {
	m := regexp.MustCompile(`(?:^|\s)` + regexp.QuoteMeta(name) + `(?:="([^"]*)"|='([^']*)')?(?:\s|/?$)`).FindStringSubmatch(attrs)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

// inheritedTargets returns the boosted links and forms that would inherit an
// hx-target from an ancestor.
func inheritedTargets(page string) []string {
	type el struct{ name, attrs string }
	var stack []el
	var bad []string
	for _, m := range tagRe.FindAllStringSubmatch(rawRe.ReplaceAllString(page, ""), -1) {
		closing, name, attrs := m[1] == "/", strings.ToLower(m[2]), m[3]
		if closing {
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == name {
					stack = stack[:i]
					break
				}
			}
			continue
		}
		if boosted(name, attrs) {
			for i := len(stack) - 1; i >= 0; i-- {
				a := stack[i].attrs
				if v, ok := attr(a, "hx-boost"); ok && v == "false" {
					break
				}
				if v, ok := attr(a, "hx-disinherit"); ok && (v == "*" || strings.Contains(" "+v+" ", " hx-target ")) {
					break
				}
				if v, ok := attr(a, "hx-target"); ok {
					bad = append(bad, "<"+name+" "+strings.TrimSpace(attrs)+"> inherits hx-target="+v+" from <"+stack[i].name+">")
					break
				}
			}
		}
		if !voidEl[name] && !strings.HasSuffix(strings.TrimSpace(attrs), "/") {
			stack = append(stack, el{name, attrs})
		}
	}
	return bad
}

// boosted reports whether hx-boost turns this element into a page change.
func boosted(name, attrs string) bool {
	if v, ok := attr(attrs, "hx-boost"); ok && v == "false" {
		return false
	}
	switch name {
	case "a":
		href, ok := attr(attrs, "href")
		if !ok || strings.HasPrefix(href, "#") || strings.Contains(href, "://") {
			return false
		}
		if _, dl := attr(attrs, "download"); dl {
			return false
		}
		if v, _ := attr(attrs, "target"); v == "_blank" {
			return false
		}
		_, get := attr(attrs, "hx-get")
		return !get
	case "form":
		for _, verb := range []string{"hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete"} {
			if _, ok := attr(attrs, verb); ok {
				return false
			}
		}
		return true
	}
	return false
}
