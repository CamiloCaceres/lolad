// New assistant only: local previews, job presets and discovery selection. No
// setup stages are invented; the service is synchronous, so the progress
// indicator is indeterminate.
(() => {
 "use strict";
 const slugify = s => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
 // Same as the server's hue(): FNV-1a over the lowercased name, six hues.
 const hue = name => { let h = 2166136261; for (const b of new TextEncoder().encode(name.toLowerCase())) h = Math.imul(h ^ b, 16777619) >>> 0; return "h" + (h % 6); };
 function update(root) {
  const form = root.querySelector("[data-new-form]");
  if (!form) return;
  const value = key => form.elements.namedItem(key).value.trim();
  const slug = value("slug") || slugify(value("name"));
  form.elements.namedItem("model").setAttribute("list","new-models-"+value("harness"));
  form.querySelector("[data-new-start-help]").textContent=form.elements.namedItem("start").checked ? "It keeps running, also after lolad restarts." : "It stays asleep until you press Start. Messages wait for it.";
  const path = root.dataset.defaultDir.replace(/\/$/, "") + "/" + (slug || "<slug>");
  const paint = (key,text) => { const el = form.querySelector('[data-new-preview="'+key+'"]'); if (el) el.textContent=text; };
  const name = value("name");
  paint("name", name || "Your new assistant");
  const team = form.querySelector('input[name="team"]:checked');
  const teamName = team ? team.dataset.teamName : "";
  paint("role", (value("role") || "Assistant") + (teamName ? " · " + teamName + " team" : ""));
  const ask = value("permissions") === "default";
  paint("how", (ask ? "Will ask you before acting" : "Will work on its own") + " and wake up when a message arrives.");
  const start = form.elements.namedItem("start").checked;
  const submit = form.querySelector("[data-new-submit]");
  if (submit) submit.textContent = start ? "Wake " + (name || "it up") : "Create " + (name || "assistant");
  const orb = form.querySelector("[data-new-orb]");
  if (orb) { orb.classList.remove("h0","h1","h2","h3","h4","h5"); orb.classList.add(hue(name)); }
  paint("slug", slug || "Made from its name");
  paint("dir", value("dir") || path);
  paint("runtime", (value("harness")==="codewhale" ? "Codewhale" : "Claude Code")+" · "+(value("model") || "default model"));
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
  dirField.setCustomValidity(known ? "This folder already has an assistant in it. Choose another folder or use Bring one you have." : "");
  const dirError=form.querySelector("#new-dir-error");
  if (known) { dirError.textContent="This folder already has an assistant in it. Choose another folder or use Bring one you have.";dirError.dataset.known="true";form.querySelector(".new-advanced").open=true; }
  else if(dirError.dataset.known){dirError.textContent="";delete dirError.dataset.known;}
  dirField.setAttribute("aria-invalid",String(known));
  const adopt=root.querySelector("[data-adopt-form]");
  if (adopt) {
   adopt.querySelector("[data-adopt-preview]").textContent=adopt.elements.namedItem("dir").value || "Choose or enter a directory";
   adopt.elements.namedItem("model").setAttribute("list","new-models-"+adopt.elements.namedItem("harness").value);
  }
 }
 // A preset fills the job and title, but never overwrites words you wrote.
 let lastMission = "", lastRole = "";
 document.addEventListener("change", e => {
  const preset = e.target.closest('input[name="preset"]'); if (!preset) return;
  const form = preset.form, mission = form.elements.namedItem("mission"), role = form.elements.namedItem("role");
  if (!mission.value.trim() || mission.value === lastMission) { mission.value = preset.dataset.mission; lastMission = mission.value; }
  if (!role.value.trim() || role.value === lastRole) { role.value = preset.dataset.role; lastRole = role.value; }
  if (!preset.dataset.mission) mission.focus();
 });
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
