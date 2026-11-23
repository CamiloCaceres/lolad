package main

import "sync"

// navGate holds window navigation until the webview exists. Only Windows
// needs it; on other systems the shell opens it at once.
//
// On Windows, Wails creates the window object before WebView2: SetURL or
// Reload in that gap dereferences a nil webview and the app dies (v0.2.0-beta.1
// crashed ~10 ms after launch on a fresh install, when the first feed update
// found the daemon not up yet). The gate opens on the webview's first finished
// navigation (the bundled offline page), which can only happen once the
// webview exists. Until then it keeps the latest URL asked for and loads it
// when it opens; a Reload before then has nothing to reload and is dropped.
//
// Wails' WindowRuntimeReady can't be the signal: it fires only on pages that
// load /wails/runtime.js, and neither the offline page nor the dashboard does.
type navGate struct {
	mu      sync.Mutex
	ready   bool
	pending string
	// goTo and reload ask the window to act. They are called with mu held, so
	// they must not block (the shell posts them with application.InvokeAsync);
	// that keeps navigations in the order they were asked for.
	goTo   func(url string)
	reload func()
}

func newNavGate(goTo func(url string), reload func()) *navGate {
	return &navGate{goTo: goTo, reload: reload}
}

// Navigate loads url now, or once the webview is ready.
func (g *navGate) Navigate(url string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.ready {
		g.pending = url
		return
	}
	g.goTo(url)
}

// Reload reloads the page if there is one yet.
func (g *navGate) Reload() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready {
		g.reload()
	}
}

// IsReady reports whether the webview exists yet.
func (g *navGate) IsReady() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ready
}

// Ready opens the gate and loads the URL that was waiting, if any. Later calls
// do nothing.
func (g *navGate) Ready() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready {
		return
	}
	g.ready = true
	if g.pending != "" {
		g.goTo(g.pending)
		g.pending = ""
	}
}
