// Onboarding (/welcome) only: the notification permission button on the last
// screen. Everything else on that page is plain forms and htmx.
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
})();
