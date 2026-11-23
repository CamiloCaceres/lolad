// lolad hub console: small enhancements. Every page works without this file.
(function () {
  "use strict";

  // Copy an invite code.
  document.querySelectorAll("[data-copy]").forEach(function (btn) {
    btn.hidden = false;
    btn.addEventListener("click", function () {
      var el = document.getElementById(btn.getAttribute("data-copy"));
      if (!el || !navigator.clipboard) return;
      navigator.clipboard.writeText(el.textContent.trim()).then(function () {
        btn.textContent = "Copied";
      });
    });
  });

  // Filter the people table as you type.
  var find = document.getElementById("find");
  if (find) {
    var rows = document.querySelectorAll("tr[data-find]");
    find.addEventListener("input", function () {
      var q = find.value.trim().toLowerCase();
      rows.forEach(function (tr) {
        tr.hidden = q !== "" && tr.getAttribute("data-find").indexOf(q) < 0;
      });
    });
  }

  // Ask before turning someone off.
  document.querySelectorAll("form[data-confirm]").forEach(function (f) {
    f.addEventListener("submit", function (e) {
      if (!window.confirm(f.getAttribute("data-confirm"))) e.preventDefault();
    });
  });
})();
