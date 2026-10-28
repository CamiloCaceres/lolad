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
 fire("lolad:federation", {});
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
      } else if (ev.class === "federation") {
        fire("lolad:federation", ev);
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
      const sync = () => { btn.disabled = !canSend(form); grow(ta); };
      form._sync = sync;
      ta.value = store.get(draftKey(form)) || "";
      setupAttach(form);
      sync();
      ta.addEventListener("input", () => { store.set(draftKey(form), ta.value); sync(); });
      ta.addEventListener("keydown", (e) => {
        if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !coarse.matches) {
          e.preventDefault();
          if (canSend(form)) form.requestSubmit();
        }
      });
      // Nothing to send, or files still uploading: don't post.
      form.addEventListener("htmx:confirm", (e) => { if (e.detail.elt === form && !canSend(form)) e.preventDefault(); });
      form.addEventListener("htmx:afterRequest", (e) => {
        if (e.detail.elt !== form || !e.detail.successful) return;
        ta.value = "";
        store.set(draftKey(form), "");
        clearFiles(form);
        sync();
        ta.focus();
      });
      if (!coarse.matches && !document.querySelector(".msg.is-open")) ta.focus({ preventScroll: true });
    });
  }

  // ------------------------------------------------------------ attachments
  // Each file uploads as soon as it is added (one request per file, three at
  // a time); the form then sends the returned IDs as "attachment" fields.
  // Send waits for uploads, and for failed files to be removed.
  const files = new WeakMap(); // composer form -> [{file, name, state, id, error, xhr, li, url}]
  const svgNS = "http://www.w3.org/2000/svg";
  function icon(name) {
    const svg = document.createElementNS(svgNS, "svg");
    svg.setAttribute("class", "i");
    svg.setAttribute("aria-hidden", "true");
    const use = document.createElementNS(svgNS, "use");
    use.setAttribute("href", "#i-" + name);
    svg.append(use);
    return svg;
  }
  const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
  const sizeLabel = (n) => n < 1000 ? n + " B" : n < 1e6 ? (n / 1000).toFixed(1) + " KB" : (n / 1e6).toFixed(1) + " MB";
  const two = (n) => String(n).padStart(2, "0");
  function pastedName(type, i) {
    const d = new Date();
    const ext = ((type.split("/")[1] || "png").split("+")[0]).replace("jpeg", "jpg");
    return "pasted-image-" + d.getFullYear() + "-" + two(d.getMonth() + 1) + "-" + two(d.getDate()) + "-" +
      two(d.getHours()) + two(d.getMinutes()) + two(d.getSeconds()) + (i ? "-" + (i + 1) : "") + "." + ext;
  }
  const kindOf = (f) => /^image\//.test(f.type) ? "image" : /^(video|audio)\//.test(f.type) ? "media" :
    /\.(zip|gz|tgz|tar|7z|rar)$/i.test(f.name) ? "archive" : /^text\/|pdf|json/.test(f.type) || /\.(md|csv|txt|log|json|pdf)$/i.test(f.name) ? "doc" : "blank";
  const listOf = (form) => files.get(form) || [];
  function canSend(form) {
    const list = listOf(form);
    if (list.some((f) => f.state !== "done")) return false;
    const ta = form.querySelector("textarea");
    return !!ta.value.trim() || list.length > 0;
  }
  function announce(form, msg) {
    const s = form.parentElement.querySelector("[data-att-status]");
    if (s) { s.textContent = ""; setTimeout(() => { s.textContent = msg; }, 30); }
  }
  function attachOff(form) {
    const note = form.parentElement.querySelector("[data-attach-note]");
    if (note) note.hidden = false;
    announce(form, form.dataset.attachOff);
  }

  function setupAttach(form) {
    const button = form.querySelector("[data-attach]");
    if (!button) return;
    button.hidden = false;
    const hint = form.parentElement.querySelector("[data-attach-hint]");
    if (hint) hint.hidden = false;
    files.set(form, []);
    const input = form.querySelector("[data-attach-input]");
    button.addEventListener("click", () => {
      if (form.dataset.attachOff) { attachOff(form); return; }
      if (input) input.click();
    });
    if (input) input.addEventListener("change", () => { addFiles(form, [...input.files]); input.value = ""; });
    form.addEventListener("paste", (e) => pasteFiles(form, e));
  }

  // pastedFiles returns the files on a paste, or none when it should paste as
  // text. Some apps put the file in items but not files (Safari), and macOS
  // often adds text alongside an image or a copied file (its name), so text
  // wins only when it looks like copied content: a table (tabs) or several
  // lines, e.g. cells copied from a spreadsheet that also carry a picture.
  function pastedFiles(cd) {
    if (!cd) return [];
    let found = cd.files && cd.files.length ? [...cd.files] : [];
    if (!found.length && cd.items) found = [...cd.items].filter((i) => i.kind === "file").map((i) => i.getAsFile()).filter(Boolean);
    if (!found.length) return [];
    const text = [...cd.types].includes("text/plain") ? cd.getData("text/plain") : "";
    if (text && (text.includes("\t") || text.trim().split("\n").length > 1)) return [];
    return found;
  }
  function pasteFiles(form, e) {
    const found = pastedFiles(e.clipboardData);
    if (!found.length) return;
    e.preventDefault();
    if (form.dataset.attachOff) { attachOff(form); return; }
    let n = 0;
    addFiles(form, found.map((f) => {
      if (/^image\//.test(f.type) && (!f.name || /^image\.\w+$/i.test(f.name))) return new File([f], pastedName(f.type, n++), { type: f.type, lastModified: Date.now() });
      return f;
    }));
  }
  // A paste anywhere in a conversation (not into another field) goes to its composer.
  document.addEventListener("paste", (e) => {
    const t = e.target;
    if (t && t.closest && (t.closest("[data-composer]") || t.closest("input, textarea, select, [contenteditable]"))) return;
    const form = document.querySelector("form[data-composer]");
    if (form && files.has(form)) pasteFiles(form, e);
  });

  function addFiles(form, picked) {
    if (!picked.length) return;
    const list = listOf(form);
    const max = parseInt(form.dataset.maxFiles || "10", 10);
    const maxBytes = parseInt(form.dataset.maxBytes || "0", 10);
    const room = max - list.filter((f) => f.state !== "error").length;
    if (picked.length > room) {
      toast("You can attach up to " + max + " files to one message.", "error");
      picked = picked.slice(0, Math.max(room, 0));
    }
    for (const file of picked) {
      const item = { file, name: file.name || "file", state: "queued" };
      if (maxBytes && file.size > maxBytes) { item.state = "error"; item.error = "Larger than " + form.dataset.maxLabel; }
      else if (file.size === 0) { item.state = "error"; item.error = "Empty file or a folder"; }
      list.push(item);
      paintItem(form, item);
    }
    if (picked.length) announce(form, picked.length === 1 ? "Attaching " + picked[0].name : "Attaching " + picked.length + " files");
    pump(form);
    paintQueue(form);
  }

  function pump(form) {
    const list = listOf(form);
    let active = list.filter((f) => f.state === "uploading").length;
    for (const item of list) {
      if (active >= 3) break;
      if (item.state === "queued") { upload(form, item); active++; }
    }
  }

  function upload(form, item) {
    item.state = "uploading";
    paintItem(form, item);
    const x = new XMLHttpRequest();
    item.xhr = x;
    x.open("POST", form.dataset.upload);
    x.responseType = "json";
    x.setRequestHeader("Accept", "application/json");
    x.upload.addEventListener("progress", (e) => { if (e.lengthComputable) { item.progress = e.loaded / e.total; paintItem(form, item); } });
    const done = (ok, err) => {
      item.xhr = null;
      if (item.state === "removed") return;
      if (ok) { item.state = "done"; announce(form, item.name + " is ready to send"); }
      else { item.state = "error"; item.error = err; announce(form, item.name + " was not attached: " + err); }
      paintItem(form, item);
      paintQueue(form);
      pump(form);
    };
    x.addEventListener("load", () => {
      const r = x.response || {};
      if (x.status === 200 && r.id) {
        item.id = r.id;
        item.name = r.filename || item.name;
        item.flagged = r.scan_status === "suspicious";
        done(true);
      } else done(false, r.error || (x.status === 413 ? "Too large to upload" : "Upload failed (" + x.status + ")"));
    });
    x.addEventListener("error", () => done(false, "Upload failed: couldn't reach lolad"));
    const fd = new FormData();
    fd.append("file", item.file, item.name);
    x.send(fd);
  }

  function removeItem(form, item) {
    item.state = "removed";
    if (item.xhr) item.xhr.abort();
    if (item.url) URL.revokeObjectURL(item.url);
    const list = listOf(form);
    list.splice(list.indexOf(item), 1);
    if (item.li) item.li.remove();
    announce(form, item.name + " removed");
    paintQueue(form);
    pump(form);
    form.querySelector("textarea").focus();
  }

  function clearFiles(form) {
    for (const item of listOf(form)) {
      if (item.xhr) item.xhr.abort();
      if (item.url) URL.revokeObjectURL(item.url);
    }
    if (files.has(form)) files.set(form, []);
    const q = form.querySelector("[data-att-queue]");
    if (q) q.replaceChildren();
    paintQueue(form);
  }

  // paintQueue: list visibility, hidden ID fields and the Send button.
  function paintQueue(form) {
    const list = listOf(form);
    const q = form.querySelector("[data-att-queue]");
    if (q) q.hidden = list.length === 0;
    const ids = form.querySelector("[data-att-ids]");
    if (ids) {
      ids.replaceChildren(...list.filter((f) => f.state === "done").map((f) => {
        const i = document.createElement("input");
        i.type = "hidden"; i.name = "attachment"; i.value = f.id;
        return i;
      }));
    }
    form.classList.toggle("has-files", list.length > 0);
    if (form._sync) form._sync();
  }

  function paintItem(form, item) {
    if (!item.li) {
      const li = el("li", "att-chip");
      const thumb = el("span", "att-thumb");
      if (/^image\/(png|jpeg|gif|webp)$/.test(item.file.type) && item.file.size < 40e6) {
        item.url = URL.createObjectURL(item.file);
        const img = el("img");
        img.alt = "";
        img.src = item.url;
        thumb.append(img);
      } else thumb.append(icon(kindOf(item.file)));
      const meta = el("span", "att-meta");
      const name = el("span", "att-name", item.name);
      name.title = item.name;
      const info = el("span", "att-size");
      meta.append(name, info);
      const bar = el("span", "att-bar");
      bar.setAttribute("role", "progressbar");
      bar.setAttribute("aria-label", "Uploading " + item.name);
      bar.setAttribute("aria-valuemin", "0");
      bar.setAttribute("aria-valuemax", "100");
      bar.append(el("i"));
      const rm = el("button", "att-remove");
      rm.type = "button";
      rm.setAttribute("aria-label", "Remove " + item.name);
      rm.title = "Remove";
      rm.append(icon("x"));
      rm.addEventListener("click", () => removeItem(form, item));
      li.append(thumb, meta, bar, rm);
      item.li = li;
      item.info = info;
      item.bar = bar;
      form.querySelector("[data-att-queue]").append(li);
    }
    const li = item.li;
    li.className = "att-chip is-" + item.state;
    const pct = Math.round((item.progress || 0) * 100);
    item.bar.hidden = item.state !== "uploading" && item.state !== "queued";
    item.bar.setAttribute("aria-valuenow", String(pct));
    item.bar.firstChild.style.width = (item.state === "queued" ? 0 : pct) + "%";
    item.info.textContent = item.state === "error" ? item.error :
      item.state === "queued" ? sizeLabel(item.file.size) + " · waiting" :
      item.state === "uploading" ? sizeLabel(item.file.size) + " · " + (pct >= 100 ? "finishing" : pct + "%") :
      sizeLabel(item.file.size) + (item.flagged ? " · flagged: type mismatch" : "");
    li.classList.toggle("is-flagged", !!item.flagged);
    li.querySelector(".att-name").textContent = item.name;
  }

  // Drag and drop anywhere on a conversation. dragover repeats while a drag
  // is over the window; the overlay hides shortly after it stops.
  let dropTimer;
  const hasFiles = (e) => e.dataTransfer && [...(e.dataTransfer.types || [])].includes("Files");
  const composer = () => document.querySelector("form[data-composer][data-upload]");
  function showDrop(on) {
    const d = document.querySelector("[data-drop]");
    if (!d) return;
    if (on && d.hidden) {
      const f = composer();
      if (f) announce(f, f.dataset.attachOff ? f.dataset.attachOff : "Drop files to attach them");
    }
    d.hidden = !on;
  }
  document.addEventListener("dragover", (e) => {
    if (!hasFiles(e) || !composer()) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = composer().dataset.attachOff ? "none" : "copy";
    showDrop(true);
    clearTimeout(dropTimer);
    dropTimer = setTimeout(() => showDrop(false), 180);
  });
  document.addEventListener("drop", (e) => {
    const f = composer();
    if (!hasFiles(e) || !f) return;
    e.preventDefault();
    clearTimeout(dropTimer);
    showDrop(false);
    if (f.dataset.attachOff) { attachOff(f); return; }
    addFiles(f, [...e.dataTransfer.files]);
    f.querySelector("textarea").focus({ preventScroll: true });
  });

  // A flagged file downloads only after a confirmation.
  document.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[data-confirm]");
    if (a && !confirm(a.dataset.confirm)) e.preventDefault();
  });
  // A preview that can't load (expired, removed) becomes a plain line.
  document.addEventListener("error", (e) => {
    const img = e.target;
    if (!(img instanceof HTMLImageElement) || !img.hasAttribute("data-att-img")) return;
    const fig = img.closest(".att-img");
    if (fig) fig.classList.add("is-broken");
  }, true);
  // Leaving with uploads in flight loses them.
  window.addEventListener("beforeunload", (e) => {
    const f = composer();
    if (f && listOf(f).some((x) => x.state === "uploading" || x.state === "queued")) { e.preventDefault(); e.returnValue = ""; }
  });

  // v3 creation landing banner; dismissal survives live header refreshes.
  document.addEventListener("click", (e) => {
    const dismiss = e.target.closest && e.target.closest("[data-dismiss-arrival]");
    if (dismiss) dismiss.closest(".arrival").remove();
  });

  // v3 inbox: copy the original Markdown, preserving links and code fences.
  document.addEventListener("click", async (e) => {
    const button = e.target.closest && e.target.closest("[data-copy-message]");
    if (!button) return;
    const source = button.parentElement.querySelector("[data-deliver-source]");
    if (!source) return;
    try {
      await navigator.clipboard.writeText(source.value);
      toast("Deliverable copied");
    } catch {
      source.hidden = false;
      source.focus(); source.select();
      toast("Select and copy the deliverable below", "error");
    }
  });

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


  // ------------------------------------------------------------ settings
  // model picker: typing an id picks "Other"; a rotate time turns rotation on
  document.addEventListener("input", (e) => {
    const t = e.target;
    if (t.matches && t.matches("[data-custom-model]")) {
      const r = t.closest(".model").querySelector("[data-custom-radio]");
      if (r && t.value.trim()) r.checked = true;
    }
    if (t.matches && t.matches("[data-rotate-time]")) {
      const on = t.closest(".rotate").querySelector("[data-rotate-on]");
      if (on) on.checked = !!t.value;
    }
    if (t.matches && t.matches("[data-filter]")) {
      const q = t.value.trim().toLowerCase();
      let shown = 0;
      document.querySelectorAll(t.dataset.filter).forEach((el) => {
        const hit = !q || el.textContent.toLowerCase().includes(q);
        el.hidden = !hit;
        if (hit) shown++;
      });
      document.querySelectorAll(".lib-group").forEach((g) => { g.hidden = !g.querySelector(".skill:not([hidden])"); });
      const none = document.querySelector("[data-filter-empty]");
      if (none) none.hidden = shown > 0;
    }
    if (t.matches && t.matches("[data-acct-address]")) fillServers(t);
  });
  document.addEventListener("focusin", (e) => {
    const t = e.target;
    if (t.matches && t.matches("[data-custom-model]")) {
      const r = t.closest(".model").querySelector("[data-custom-radio]");
      if (r) r.checked = true;
    }
  });

  // inline reveal: a button shows the form named by aria-controls
  document.addEventListener("click", (e) => {
    const open = e.target.closest && e.target.closest("[data-reveal]");
    const close = e.target.closest && e.target.closest("[data-reveal-close]");
    if (open) {
      const f = document.getElementById(open.getAttribute("aria-controls"));
      if (!f) return;
      f.hidden = !f.hidden;
      open.setAttribute("aria-expanded", String(!f.hidden));
      if (!f.hidden) { const i = f.querySelector("input:not([type=hidden])"); if (i) i.focus(); }
    } else if (close) {
      const f = close.closest("form");
      f.hidden = true;
      f.reset();
      const b = document.querySelector('[aria-controls="' + f.id + '"]');
      if (b) { b.setAttribute("aria-expanded", "false"); b.focus(); }
    }
  });

  // email: fill servers for well-known providers while they're empty or ours
  function fillServers(addr) {
    const form = addr.form;
    let list = [];
    try { list = JSON.parse(form.dataset.providers || "[]"); } catch { /* none */ }
    const domain = (addr.value.split("@")[1] || "").toLowerCase();
    const p = list.find((x) => x.Domain === domain);
    [["[data-acct-imap]", p && p.IMAP], ["[data-acct-smtp]", p && p.SMTP]].forEach(([sel, v]) => {
      const f = form.querySelector(sel);
      if (!f || (f.value && !f.dataset.auto)) return;
      f.value = v || "";
      if (v) f.dataset.auto = "1"; else delete f.dataset.auto;
    });
  }
  document.addEventListener("input", (e) => {
    if (e.target.matches && e.target.matches("[data-acct-imap], [data-acct-smtp]")) delete e.target.dataset.auto;
  }, true);

  // file editor: unsaved-changes guard and ⌘S / Ctrl+S
  const editor = () => document.querySelector("form[data-editor]");
  function dirty() {
    const f = editor();
    if (!f) return false;
    const ta = f.querySelector("[data-editor-text]");
    return !!ta && ta.dataset.orig !== undefined && ta.value !== ta.dataset.orig;
  }
  function setupEditor(root) {
    root.querySelectorAll("form[data-editor]").forEach((f) => {
      if (f.dataset.ready) return;
      f.dataset.ready = "1";
      const ta = f.querySelector("[data-editor-text]");
      const flag = f.querySelector("[data-dirty]");
      // A conflict comes back with the user's text in the editor: still unsaved.
      ta.dataset.orig = f.querySelector(".conflict") ? "\u0000" : ta.value;
      const sync = () => { if (flag) flag.hidden = !dirty(); };
      sync();
      ta.addEventListener("input", sync);
      ta.addEventListener("keydown", (e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") { e.preventDefault(); f.requestSubmit(); }
      });
    });
  }
  const leave = () => !dirty() || confirm("Discard your unsaved changes to " + editor().dataset.path + "?");
  window.addEventListener("click", (e) => {
    const a = e.target.closest && e.target.closest("a[href]");
    if (!a || a.hasAttribute("data-discard") || e.defaultPrevented || a.target === "_blank") return;
    if (!leave()) { e.preventDefault(); e.stopPropagation(); }
    else if (dirty()) delete editor().querySelector("[data-editor-text]").dataset.orig; // leaving: don't ask again on unload
  }, true);
  window.addEventListener("beforeunload", (e) => { if (dirty()) { e.preventDefault(); e.returnValue = ""; } });

  // ------------------------------------------------------------ boot
  function settle(root) {
    retime(root);
    setupComposer(root);
    setupEditor(root);
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

// Traffic: keep an expanded audit record in place while agent/policy events arrive.
document.addEventListener("htmx:beforeRequest", (event) => {
  const target = event.detail.elt;
  const trigger = event.detail.triggeringEvent;
  if (target && target.id === "traffic" && trigger && trigger.type === "lolad:agent" && target.querySelector("details[open]")) event.preventDefault();
});

// An invitation only lives in its creation response; never put it in storage.
document.addEventListener('click', async function(event) {
 const button=event.target.closest('[data-copy-invite]');
 if(!button)return;
 const input=document.getElementById('invitation-code');
 if(!input)return;
 try {await navigator.clipboard.writeText(input.value);button.textContent='Copied';}
 catch {input.focus();input.select();button.textContent='Select and copy invitation';}
});
