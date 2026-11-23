// Onboarding (/welcome) only: the notification permission button on the last
// screen, and the live name and orb while shaping the first assistant.
// Everything else on that page is plain forms and htmx.
(() => {
  "use strict";
  const inApp = /LoladApp\//.test(navigator.userAgent);
  function paint(root) {
    const box = (root || document).querySelector("[data-notify]");
    if (!box) return;
    // The desktop app asks the system itself; a browser needs the Notification API.
    if (inApp || !("Notification" in window)) return;
    box.hidden = false;
    const state = box.querySelector("[data-notify-state]");
    const btn = box.querySelector("[data-notify-ask]");
    if (Notification.permission === "granted") { state.textContent = "Notifications are on."; btn.hidden = true; }
    else if (Notification.permission === "denied") { state.textContent = "Notifications are blocked in this browser’s settings."; btn.hidden = true; }
  }
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-notify-ask]");
    if (!btn || !("Notification" in window)) return;
    Notification.requestPermission().then(() => paint(document));
  });
  document.addEventListener("htmx:afterSettle", () => paint(document));
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => paint(document));
  else paint(document);

  // Ob-Shape: the orb and the name follow what you type. The hue is the same
  // hash the server uses (views.go hueIndex), so the orb doesn't jump.
  function hueOf(name) {
    let h = 2166136261;
    for (const b of new TextEncoder().encode(name.toLowerCase())) h = Math.imul(h ^ b, 16777619) >>> 0;
    return "h" + (h % 6);
  }
  function shape(name) {
    const n = name.trim() || "Your assistant";
    document.querySelectorAll("[data-shape-name]").forEach((el) => { el.textContent = n; });
    const orb = document.querySelector("[data-shape-orb]");
    if (orb) { orb.className = orb.className.replace(/\bh\d\b/, hueOf(n)); }
  }
  document.addEventListener("input", (e) => { if (e.target.matches && e.target.matches("[data-shape-input]")) shape(e.target.value); });
  document.addEventListener("click", (e) => {
    const pick = e.target.closest && e.target.closest("[data-shape-pick]");
    if (!pick) return;
    const input = document.querySelector("[data-shape-input]");
    if (input) { input.value = pick.dataset.shapePick; shape(input.value); input.focus(); }
  });
})();
