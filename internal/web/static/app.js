// lolad dashboard glue. Pages are server-rendered and htmx-enhanced; this file
// turns the SSE stream into DOM events elements refresh on, and handles the
// small interactions htmx doesn't: the composer, thread scrolling, toasts,
// relative times and the policy matrix tooltip.
(() => {
  "use strict";
  const body = document.body;
  const fire = (name, detail) => body.dispatchEvent(new CustomEvent(name, { detail, bubbles: true }));
  const visible = () => document.visibilityState === "visible";
  const store = {
    get(k) { try { return localStorage.getItem(k); } catch { return null; } },
    set(k, v) { try { v ? localStorage.setItem(k, v) : localStorage.removeItem(k); } catch { /* private mode */ } },
  };

  // ------------------------------------------------------------ live stream
  // Server events become: lolad:mail | lolad:agent, lolad:a-<agent>, and
  // lolad:mail-visible (mail while this tab is visible; the open thread
  // refreshes on it, which marks the new mail read).
  let live = "connecting";
  function paintLive() {
    const el = document.getElementById("live");
    if (!el) return;
    el.classList.toggle("is-on", live === "on");
    el.classList.toggle("is-off", live === "off");
    const label = live === "on" ? "Live" : live === "off" ? "Reconnecting" : "Connecting";
    el.title = live === "on" ? "Live updates on" : "Live updates paused: reconnecting";
    const span = el.querySelector("span");
    if (span) span.textContent = label;
  }
  function connect() {
    if (!("EventSource" in window)) return;
    const es = new EventSource("/stream");
    let dropped = false;
    es.addEventListener("open", () => {
      live = "on";
      paintLive();
      if (dropped) { // resync whatever we missed
        fire("lolad:mail", {});
        fire("lolad:agent", {});
        if (visible()) fire("lolad:mail-visible", {});
      }
    });
    es.addEventListener("error", () => { dropped = true; live = "off"; paintLive(); });
    es.addEventListener("lolad", (e) => {
      let ev;
      try { ev = JSON.parse(e.data); } catch { return; }
      if (ev.class === "mail") {
        fire("lolad:mail", ev);
        if (visible()) fire("lolad:mail-visible", ev);
      } else {
        fire("lolad:agent", ev);
      }
      if (ev.agent) fire("lolad:a-" + ev.agent, ev);
    });
  }
  document.addEventListener("visibilitychange", () => { if (visible()) fire("lolad:mail-visible", {}); });

  // ------------------------------------------------------------ toasts
  function toast(message, level) {
    const box = document.getElementById("toasts");
    if (!box || !message) return;
    const t = document.createElement("div");
    t.className = "toast" + (level === "error" ? " is-error" : "");
    t.textContent = message;
    box.append(t);
    while (box.children.length > 3) box.firstElementChild.remove();
    setTimeout(() => { t.classList.add("is-leaving"); setTimeout(() => t.remove(), 200); }, level === "error" ? 6000 : 2600);
  }
  document.addEventListener("lolad:toast", (e) => toast(e.detail && e.detail.message, e.detail && e.detail.level));
  document.addEventListener("htmx:sendError", () => toast("Couldn't reach lolad. Is the daemon running?", "error"));
  document.addEventListener("htmx:responseError", (e) => {
    const x = e.detail.xhr;
    if (x && x.getResponseHeader("HX-Trigger")) return; // the server already explained
    toast("Request failed (" + (x ? x.status : "?") + ")" + (x && x.responseText && x.responseText.length < 200 ? ": " + x.responseText : ""), "error");
  });

  // ------------------------------------------------------------ times
  function when(d) {
    const now = new Date();
    const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate());
    const days = Math.round((day(now) - day(d)) / 864e5);
    const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
    if (days <= 0) return hm;
    if (days === 1) return "Yesterday";
    if (days < 7) return d.toLocaleDateString([], { weekday: "long" });
    return d.toLocaleDateString([], d.getFullYear() === now.getFullYear() ? { day: "numeric", month: "short" } : { day: "numeric", month: "short", year: "numeric" });
  }
  function ago(d) {
    const s = (Date.now() - d) / 1000;
    if (s < 45) return "now";
    if (s < 3600) return Math.round(s / 60) + "m";
    if (s < 86400) return Math.floor(s / 3600) + "h";
    return Math.floor(s / 86400) + "d";
  }
  function retime(root) {
    root.querySelectorAll("time[data-ago], time[data-when]").forEach((t) => {
      const d = new Date(t.getAttribute("datetime"));
      if (isNaN(d)) return;
      t.textContent = t.hasAttribute("data-ago") ? ago(d) : when(d);
    });
  }
  setInterval(() => retime(document), 30000);

  // ------------------------------------------------------------ title
  function retitle() {
    const app = document.getElementById("app");
    const badges = document.getElementById("nav-badges");
    if (!app) return;
    const n = badges ? parseInt(badges.dataset.unread || "0", 10) : 0;
    document.title = (n > 0 ? "(" + n + ") " : "") + (app.dataset.title || "lolad") + " · lolad";
  }

  // ------------------------------------------------------------ thread
  const scroller = () => document.getElementById("thread-scroll");
  let stick = true;
  function toBottom(smooth) {
    const s = scroller();
    if (s) s.scrollTo({ top: s.scrollHeight, behavior: smooth && !matchMedia("(prefers-reduced-motion: reduce)").matches ? "smooth" : "auto" });
  }
  function openThread() {
    const s = scroller();
    if (!s) return;
    const fresh = document.getElementById("new");
    if (fresh && fresh.offsetTop > s.clientHeight * 0.6) s.scrollTop = fresh.offsetTop - 24;
    else toBottom(false);
  }
  document.addEventListener("htmx:beforeSwap", (e) => {
    if (e.detail.target && e.detail.target.id === "msgs") {
      const s = scroller();
      stick = !s || s.scrollHeight - s.scrollTop - s.clientHeight < 120;
    }
  });
  document.addEventListener("htmx:afterSwap", (e) => {
    if (e.detail.target && e.detail.target.id === "msgs") {
      const mine = e.detail.requestConfig && e.detail.requestConfig.verb === "post";
      if (stick || mine) toBottom(true);
    }
  });

  // composer: Enter sends (on keyboards), the field grows, drafts survive navigation
  const coarse = matchMedia("(pointer: coarse)");
  function grow(ta) { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, innerHeight * 0.4) + "px"; }
  function draftKey(form) { return "lolad:draft:" + form.getAttribute("action"); }
  function setupComposer(root) {
    root.querySelectorAll("form[data-composer]").forEach((form) => {
      if (form.dataset.ready) return;
      form.dataset.ready = "1";
      const ta = form.querySelector("textarea");
      const btn = form.querySelector("button[type=submit]");
      const sync = () => { btn.disabled = !ta.value.trim(); grow(ta); };
      ta.value = store.get(draftKey(form)) || "";
      sync();
      ta.addEventListener("input", () => { store.set(draftKey(form), ta.value); sync(); });
      ta.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !coarse.matches) {
          e.preventDefault();
          if (ta.value.trim()) form.requestSubmit();
        }
      });
      form.addEventListener("htmx:afterRequest", (e) => {
        if (e.detail.elt !== form || !e.detail.successful) return;
        ta.value = "";
        store.set(draftKey(form), "");
        sync();
        ta.focus();
      });
      if (!coarse.matches && !document.querySelector(".msg.is-open")) ta.focus({ preventScroll: true });
    });
  }

  // ------------------------------------------------------------ org matrix
  let tip;
  function showTip(cell) {
    if (!tip) { tip = document.createElement("div"); tip.className = "tip"; tip.setAttribute("aria-hidden", "true"); body.append(tip); }
    const allow = cell.classList.contains("is-allow");
    tip.innerHTML = "";
    const head = document.createElement("strong");
    head.textContent = cell.dataset.from + " → " + cell.dataset.to + ": " + (allow ? "allowed" : "denied");
    tip.append(head, document.createTextNode(cell.dataset.reason || ""));
    const r = cell.getBoundingClientRect();
    tip.classList.add("is-on");
    const w = tip.offsetWidth, h = tip.offsetHeight;
    let x = Math.max(8, Math.min(innerWidth - w - 8, r.left + r.width / 2 - w / 2));
    let y = r.top - h - 8;
    if (y < 8) y = r.bottom + 8;
    tip.style.left = x + "px";
    tip.style.top = y + "px";
    cell.closest("table").querySelectorAll(".is-hot").forEach((c) => c.classList.remove("is-hot"));
  }
  function hideTip() { if (tip) tip.classList.remove("is-on"); }
  document.addEventListener("pointerover", (e) => { const c = e.target.closest && e.target.closest("[data-cell]"); if (c) showTip(c); });
  document.addEventListener("pointerout", (e) => { if (e.target.closest && e.target.closest("[data-cell]")) hideTip(); });
  document.addEventListener("focusin", (e) => { const c = e.target.closest && e.target.closest("[data-cell]"); c ? showTip(c) : hideTip(); });
  document.addEventListener("scroll", hideTip, true);
  document.addEventListener("click", (e) => {
    const c = e.target.closest && e.target.closest("[data-cell]");
    if (!c) return;
    const box = document.getElementById("test-box");
    if (!box) return;
    box.querySelector("[data-test-from]").value = c.dataset.from;
    box.querySelector("[data-test-to]").value = c.dataset.to;
    box.requestSubmit();
    if (coarse.matches) showTip(c);
  });

  // ------------------------------------------------------------ traffic
  document.addEventListener("change", (e) => {
    const f = e.target.closest && e.target.closest("form[data-autosubmit]");
    if (f) f.requestSubmit();
  });
  // Don't refresh the list under someone reading an expanded message.
  document.addEventListener("htmx:confirm", (e) => {
    const el = e.detail.elt;
    if (el && el.id === "traffic" && el.querySelector("details[open]") && e.detail.triggeringEvent && e.detail.triggeringEvent.type === "lolad:mail") e.preventDefault();
  });

  // ------------------------------------------------------------ boot
  function settle(root) {
    retime(root);
    setupComposer(root);
    paintLive();
    retitle();
  }
  document.addEventListener("htmx:afterSettle", (e) => {
    settle(e.detail.elt || document);
    if (e.detail.elt === body) { openThread(); return; }
    // A form answered with an error: make sure it is seen.
    const err = document.querySelector("#main .flash.is-error[role=alert]");
    if (err && !err.dataset.seen) {
      err.dataset.seen = "1";
      const r = err.getBoundingClientRect();
      if (r.top < 0 || r.bottom > innerHeight) err.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    }
  });
  const boot = () => { settle(document); openThread(); connect(); };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
