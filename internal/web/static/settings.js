/* Settings workspace: delegated listeners survive boosted navigation. */
(() => {
  "use strict";
  document.addEventListener("keydown", (event) => {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey || event.target.closest("input, textarea, select, [contenteditable]")) return;
    const search = document.querySelector("[data-file-filter]");
    if (search) {
      event.preventDefault();
      search.closest(".is-files").classList.add("is-browsing");
      search.focus(); search.select();
    }
  });
  document.addEventListener("change", (event) => {
    if (event.target.matches("[data-file-all]")) event.target.form.requestSubmit();
  });
  // Link navigation is already guarded by app.js. Search is a GET form and
  // needs the same protection before htmx replaces the editor.
  document.addEventListener("submit", (event) => {
    if (!event.target.matches("[data-file-search]")) return;
    const editor = document.querySelector("[data-editor]");
    const text = editor && editor.querySelector("[data-editor-text]");
    if (text && text.dataset.orig !== undefined && text.value !== text.dataset.orig) {
      if (!confirm("Discard your unsaved changes to " + editor.dataset.path + "?")) {
        event.preventDefault(); event.stopImmediatePropagation();
      } else { delete text.dataset.orig; }
    }
  }, true);
})();
