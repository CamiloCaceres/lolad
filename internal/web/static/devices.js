// The countdown contains no code/token and works after ordinary page navigation.
(() => {
  function update() {
    for (const el of document.querySelectorAll('[data-pair-expires]')) {
      const seconds = Math.max(0, Math.ceil((Date.parse(el.dataset.pairExpires) - Date.now()) / 1000));
      el.textContent = seconds ? `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')} remaining` : 'Expired — generate a new code';
    }
  }
  update(); setInterval(update, 1000);
})();
