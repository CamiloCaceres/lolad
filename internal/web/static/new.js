// New agent only: local previews and discovery selection. No setup stages are
// invented; the service is synchronous, so the progress indicator is indeterminate.
(() => {
 "use strict";
 const slugify = s => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
 function update(root) {
  const form = root.querySelector("[data-new-form]");
  if (!form) return;
  const value = key => form.elements.namedItem(key).value.trim();
  const slug = value("slug") || slugify(value("name"));
  form.elements.namedItem("model").setAttribute("list","new-models-"+value("harness"));
  form.querySelector("[data-new-start-help]").textContent=form.elements.namedItem("start").checked ? "Keep it running, including after lolad restarts." : "Start it manually when ready. Mail will wait without waking it.";
  const path = root.dataset.defaultDir.replace(/\/$/, "") + "/" + (slug || "<slug>");
  const paint = (key,text) => { const el = form.querySelector('[data-new-preview="'+key+'"]'); if (el) el.textContent=text; };
  paint("name", value("name") || "Your new agent");
  paint("slug", slug || "Name sets the slug");
  paint("dir", value("dir") || path);
  paint("runtime", (value("harness")==="codewhale" ? "Codewhale" : "Claude Code")+" · "+(value("model") || "harness default"));
  paint("mission",value("mission") || "Its job will appear here as you write.");
  paint("files", value("harness")==="codewhale" ? "Codewhale home config and a manual launcher. Template brain/, memory/ and tools/ files." : "Claude Code settings and an AGENTS.md link. Template brain/, memory/ and tools/ files.");
  form.querySelector("[data-new-default-path]").textContent=path;
  form.elements.namedItem("dir").placeholder=path;
  const collision=(root.dataset.existing || "").split(",").includes(slug);
  const invalidSlug=!!value("name") && !/^[a-z0-9][a-z0-9-]{0,62}$/.test(slug);
  const field=form.elements.namedItem("slug");
  field.setCustomValidity(collision ? "This slug already exists. Choose another name or slug." : invalidSlug ? "Use 1–63 lowercase letters, numbers or dashes." : "");
  field.setAttribute("aria-invalid",String(collision || invalidSlug));
  const error=form.querySelector("#new-slug-error");
  if (collision || invalidSlug) {
   error.textContent=collision ? "This slug already exists. Choose another name or edit the slug." : "Use 1–63 lowercase letters, numbers or dashes.";
   form.querySelector(".new-advanced").open=true;
   error.dataset.collision="true";
  } else if (error.dataset.collision) { error.textContent=""; delete error.dataset.collision; }
  const dirField=form.elements.namedItem("dir");
  const known=Array.from(root.querySelectorAll("[data-known-dir]")).some(el=>el.dataset.knownDir===(value("dir") || path));
  dirField.setCustomValidity(known ? "This directory already contains an agent. Choose another directory or use Adopt." : "");
  const dirError=form.querySelector("#new-dir-error");
  if (known) { dirError.textContent="This directory already contains an agent. Choose another directory or use Adopt.";dirError.dataset.known="true";form.querySelector(".new-advanced").open=true; }
  else if(dirError.dataset.known){dirError.textContent="";delete dirError.dataset.known;}
  dirField.setAttribute("aria-invalid",String(known));
  const adopt=root.querySelector("[data-adopt-form]");
  if (adopt) {
   adopt.querySelector("[data-adopt-preview]").textContent=adopt.elements.namedItem("dir").value || "Choose or enter a directory";
   adopt.elements.namedItem("model").setAttribute("list","new-models-"+adopt.elements.namedItem("harness").value);
  }
 }
 document.addEventListener("input", e => {const root=e.target.closest("[data-new-agent]");if(root) update(root);});
 document.addEventListener("change", e => {const root=e.target.closest("[data-new-agent]");if(root) update(root);});
 document.addEventListener("click", e => {
  const button=e.target.closest("[data-adopt-dir]");if(!button) return;
  const form=button.closest("form");
  ["dir","slug","harness","model"].forEach(k=>{form.elements.namedItem(k).value=button.dataset["adopt"+k[0].toUpperCase()+k.slice(1)] || "";});
  update(form.closest("[data-new-agent]"));
  form.elements.namedItem("dir").focus();
 });
 document.addEventListener("htmx:afterSwap",e=>{
  const root=document.querySelector("[data-new-agent]");if(root) update(root);
  const target=e.detail.target;
  const error=root && target && (target.id==="new-body" || target===document.body) && root.querySelector("[role=alert]");if(error) error.focus();
 });
 // Show a network failure at the form, with recovery instead of an unsafe retry.
 document.addEventListener("htmx:sendError",e=>{
  const form=e.detail.elt && e.detail.elt.closest("[data-new-form], [data-adopt-form]");if(!form)return;
  const message=document.createElement("div");message.className="flash is-error new-error";message.setAttribute("role","alert");
  message.textContent="Connection lost during setup. Check Agents before trying again: setup may have completed. If files were left behind, adopt the directory to recover.";
  form.querySelector(".new-editor").prepend(message);
 });
 const boot=()=>{const root=document.querySelector("[data-new-agent]");if(root)update(root);};
 if(document.readyState==="loading")document.addEventListener("DOMContentLoaded",boot);else boot();
})();
