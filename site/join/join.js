// /join: greet the person a friend invited, offer the right download, and let
// them copy the link to paste into Lolad.
//
// The invite is in location.hash. Browsers never send the fragment to a
// server, and this script never does either: no fetch, no storage, no
// analytics. It only decodes the display name, the offered assistants and the
// expiry, to show them. Everything is written with textContent.
(function () {
  "use strict";
  const $ = (sel) => document.querySelector(sel);

  // ---- the invite (docs/friend-connect-design.md §2.2: "2." + base64url(JSON))
  function decode(hash) {
    let s = (hash || "").replace(/^#/, "").replace(/\s+/g, "");
    if (!s || s.length > 4096 || s.slice(0, 2) !== "2.") return null;
    try {
      const b64 = s.slice(2).replace(/-/g, "+").replace(/_/g, "/");
      const bin = atob(b64 + "===".slice((b64.length + 3) % 4));
      const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
      const p = JSON.parse(new TextDecoder().decode(bytes));
      return p && typeof p === "object" ? p : null;
    } catch (e) {
      return null;
    }
  }
  const clean = (v, max) => (typeof v === "string" ? v.replace(/[\u0000-\u001f]/g, "").trim().slice(0, max) : "");

  const invite = decode(location.hash);
  const who = invite ? clean(invite.d, 40) || clean(invite.n, 32) : "";
  const agents = invite && Array.isArray(invite.a)
    ? invite.a.slice(0, 8).map((x) => (Array.isArray(x) ? clean(x[1], 40) || clean(x[0], 32) : "")).filter(Boolean)
    : [];
  const expired = invite && typeof invite.e === "number" && invite.e * 1000 < Date.now();

  const problem = $("[data-problem]");
  function warn(text) {
    problem.textContent = text;
    problem.classList.add("is-warn");
    problem.hidden = false;
  }

  if (who) {
    $("[data-who]").textContent = who;
    $("[data-who-2]").textContent = who;
    document.title = who + " invited you to Lolad";
  }
  if (agents.length) {
    const ul = $("[data-agents]");
    for (const name of agents) {
      const li = document.createElement("li");
      li.textContent = name;
      ul.appendChild(li);
    }
    $("[data-offer]").hidden = false;
  }
  if (!invite) {
    $("#join-title").textContent = "You’re invited to\u00a0Lolad.";
    warn("This invite link looks incomplete. Ask the person who sent it to copy the whole link and send it again. You can still install Lolad now.");
  } else if (expired) {
    warn("This invite has expired. You can still install Lolad, then ask " + (who || "your friend") + " for a new link.");
  }

  // ---- the right download for this computer
  const ua = navigator.userAgent || "";
  const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
  // iPadOS says "Macintosh"; touch gives it away.
  const mobile = /Android|iPhone|iPad|iPod|Mobile/i.test(ua) || (navigator.maxTouchPoints > 1 && /Macintosh/.test(ua));
  const os = mobile ? "" : /Win/i.test(platform) || /Windows/i.test(ua) ? "windows" : /Mac/i.test(platform) || /Mac OS X/i.test(ua) ? "mac" : "";
  if (os) {
    const other = os === "mac" ? "windows" : "mac";
    const keep = $('[data-downloads] [data-os="' + os + '"]');
    const drop = $('[data-downloads] [data-os="' + other + '"]');
    drop.remove();
    const note = $("[data-other]");
    const a = document.createElement("a");
    a.href = drop.href;
    a.textContent = other === "mac" ? "Download for Mac" : "Download for Windows";
    note.append("On another computer? ", a, ".");
    note.hidden = false;
    for (const el of document.querySelectorAll("[data-for]")) el.hidden = el.dataset.for !== os;
  } else if (mobile) {
    $("[data-mobile]").hidden = false;
    // The phone app is only a companion to Lolad on a computer: offer it second.
    if (/Android/i.test(ua)) $("[data-android]").hidden = false;
  }

  // ---- copy the link, to paste into Lolad (or send to your own computer)
  if (invite && navigator.clipboard) {
    const row = $("[data-copy-row]");
    const label = $("[data-copy-label]");
    const status = $("[data-copy-status]");
    row.hidden = false;
    $("[data-copy]").addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(location.href);
        label.textContent = "Copied";
        status.textContent = mobile ? "Send it to yourself, then open it on your computer." : "Now paste it into Lolad.";
      } catch (e) {
        status.textContent = "Copy the whole address of this page from the address bar instead.";
      }
    });
  } else if (invite) {
    $("[data-copy-row]").hidden = false;
    $("[data-copy]").hidden = true;
    $("[data-copy-status]").textContent = "Copy the whole address of this page from the address bar. That is your invite link.";
  }
})();
