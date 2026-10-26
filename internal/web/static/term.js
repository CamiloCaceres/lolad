// Live terminal on the agent page: xterm.js over a websocket to
// /agents/<name>/terminal. Read-only unless the operator takes control. xterm
// is loaded only when the panel is opened, and the socket closes with it.
(() => {
  "use strict";
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { v ? localStorage.setItem(k, v) : localStorage.removeItem(k); } catch { /* private mode */ } },
  };

  let loading;
  function loadXterm(panel) {
    if (window.Terminal && window.FitAddon) return Promise.resolve();
    if (loading) return loading;
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = panel.dataset.xtermCss;
    document.head.append(css);
    const script = (src) => new Promise((ok, fail) => {
      const s = document.createElement("script");
      s.src = src;
      s.onload = ok;
      s.onerror = () => fail(new Error("could not load " + src));
      document.head.append(s);
    });
    loading = script(panel.dataset.xtermJs).then(() => script(panel.dataset.fitJs));
    return loading;
  }

  function palette(el) {
    const bg = getComputedStyle(el).backgroundColor || "#1e1e1e";
    const dark = { black: "#2b2b2b", red: "#d9776b", green: "#8fbf8f", yellow: "#d6b36a", blue: "#86a8d6", magenta: "#b99ad6", cyan: "#86c2c2", white: "#d4d4d4" };
    return {
      background: bg, foreground: "#dadada", cursor: "#dadada", cursorAccent: bg, selectionBackground: "#4a5a70",
      black: dark.black, red: dark.red, green: dark.green, yellow: dark.yellow, blue: dark.blue, magenta: dark.magenta, cyan: dark.cyan, white: dark.white,
      brightBlack: "#7a7a7a", brightRed: "#e8968b", brightGreen: "#a9d2a9", brightYellow: "#e6c98a", brightBlue: "#a3bfe4", brightMagenta: "#ccb3e4", brightCyan: "#a3d4d4", brightWhite: "#f0f0f0",
    };
  }

  // A read-only viewer never resizes the agent's window (tmux attach -r -f
  // ignore-size), so its stream has the attach pty's default size. Draw
  // exactly that grid and scale the font to the panel instead of re-wrapping.
  const WATCH_COLS = 80, WATCH_ROWS = 24;

  const sessions = new Map(); // panel -> session

  function session(panel) {
    const agent = panel.dataset.agent;
    const host = panel.querySelector("[data-term]");
    const status = panel.querySelector("[data-term-status]");
    const mode = panel.querySelector("[data-term-mode]");
    const toggle = panel.querySelector("[data-term-control]");
    const again = panel.querySelector("[data-term-reconnect]");
    const s = { control: false, ws: null, term: null, fit: null, ro: null, size: "" };

    const say = (t) => { status.textContent = t; };
    function paint() {
      panel.classList.toggle("is-control", s.control);
      mode.querySelector("span").textContent = s.control ? "You have control" : "Read-only";
      mode.querySelector("use").setAttribute("href", s.control ? "#i-unlock" : "#i-lock");
      toggle.querySelector("span").textContent = s.control ? "Release control" : "Take control";
      toggle.querySelector("use").setAttribute("href", s.control ? "#i-lock" : "#i-unlock");
      toggle.setAttribute("aria-pressed", String(s.control));
      if (s.term) s.term.options.disableStdin = !s.control;
    }
    // layout sizes the terminal: fitted to the panel when in control (the
    // session follows), a fixed scaled grid when watching.
    function layout() {
      if (!s.term) return;
      if (s.control) {
        s.term.options.fontSize = 12.5;
        try { s.fit.fit(); } catch { /* hidden */ }
        return;
      }
      const width = host.clientWidth - 8;
      if (width <= 0) return;
      // ~0.6em per monospace cell; stay legible, let the panel scroll below 7px.
      s.term.options.fontSize = Math.max(7, Math.min(14, Math.floor((width / WATCH_COLS / 0.6) * 10) / 10));
      if (s.term.cols !== WATCH_COLS || s.term.rows !== WATCH_ROWS) s.term.resize(WATCH_COLS, WATCH_ROWS);
    }
    function sendSize() {
      if (!s.ws || s.ws.readyState !== WebSocket.OPEN || !s.term) return;
      const size = s.term.cols + "x" + s.term.rows;
      if (size === s.size) return;
      s.size = size;
      s.ws.send(JSON.stringify({ type: "resize", cols: s.term.cols, rows: s.term.rows }));
    }
    function connect() {
      disconnect();
      again.hidden = true;
      say("Connecting…");
      s.term.reset();
      s.size = "";
      const url = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/agents/" + encodeURIComponent(agent) + "/terminal?mode=" + (s.control ? "control" : "watch");
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      s.ws = ws;
      ws.onopen = () => {
        say(s.control ? "Live · your keystrokes go to the session" : "Live · watching");
        sendSize();
        if (s.control) s.term.focus();
      };
      ws.onmessage = (e) => s.term.write(typeof e.data === "string" ? e.data : new Uint8Array(e.data));
      ws.onclose = (e) => {
        if (s.ws !== ws) return;
        s.ws = null;
        const why = { 4409: agent + " isn't running", 4404: "No such agent", 1000: "Session ended" }[e.code] || ("Disconnected" + (e.reason ? ": " + e.reason : ""));
        say(why);
        again.hidden = false;
      };
    }
    function disconnect() {
      if (s.ws) { const ws = s.ws; s.ws = null; ws.close(); }
    }
    s.start = () => {
      say("Loading terminal…");
      loadXterm(panel).then(() => {
        if (!panel.open || !document.body.contains(panel)) return;
        if (!s.term) {
          s.term = new window.Terminal({
            fontFamily: 'ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace',
            fontSize: 12.5, lineHeight: 1.15, cursorBlink: false, scrollback: 5000,
            disableStdin: true, allowProposedApi: false, theme: palette(host),
          });
          s.fit = new window.FitAddon.FitAddon();
          s.term.loadAddon(s.fit);
          s.term.open(host);
          s.term.onData((d) => {
            if (s.control && s.ws && s.ws.readyState === WebSocket.OPEN) s.ws.send(JSON.stringify({ type: "input", data: d }));
          });
          s.ro = new ResizeObserver(() => { layout(); sendSize(); });
          s.ro.observe(host);
        }
        layout();
        paint();
        connect();
      }, (err) => say(err.message));
    };
    s.stop = () => { disconnect(); say("Not connected"); };
    s.dispose = () => {
      disconnect();
      if (s.ro) s.ro.disconnect();
      if (s.term) s.term.dispose();
    };
    toggle.addEventListener("click", () => {
      if (!s.control && !confirm("Take control of " + agent + "'s session? Your keystrokes go straight into it; the agent may be mid-turn.")) return;
      s.control = !s.control;
      paint();
      layout();
      if (panel.open && s.term) connect();
    });
    again.addEventListener("click", () => { if (s.term) connect(); });
    panel.addEventListener("toggle", () => {
      store.set("lolad:term:" + agent, panel.open ? "1" : "");
      panel.open ? s.start() : s.stop();
    });
    return s;
  }

  function init(root) {
    root.querySelectorAll("details[data-terminal]").forEach((panel) => {
      if (sessions.has(panel)) return;
      const s = session(panel);
      sessions.set(panel, s);
      if (store.get("lolad:term:" + panel.dataset.agent) === "1") panel.open = true; // fires toggle
    });
  }
  function sweep() {
    for (const [panel, s] of sessions) {
      if (!document.body.contains(panel)) { s.dispose(); sessions.delete(panel); }
    }
  }
  document.addEventListener("htmx:afterSettle", (e) => { sweep(); init(e.detail.elt || document); });
  window.addEventListener("pagehide", () => sessions.forEach((s) => s.dispose()));
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => init(document));
  else init(document);
})();
