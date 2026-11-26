// Settings → Updates. Only the desktop app can update itself, so the section
// stays hidden unless the app's bridge is there (window.__loladUpdates, from
// desktop/bridge.go). Actions go to the app as "lolad:update\n<action>"; the
// app answers with window.__loladUpdate(state) for every change (contract:
// UpdateState in desktop/updates.go). State text is only ever set with
// textContent.
(() => {
  let last = null;

  function ago(iso) {
    const t = Date.parse(iso);
    if (!t) return '';
    const s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return 'just now';
    if (s < 3600) { const m = Math.floor(s / 60); return m + (m === 1 ? ' minute ago' : ' minutes ago'); }
    const d = new Date(t), now = new Date();
    const hm = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return 'today at ' + hm;
    const y = new Date(now); y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return 'yesterday at ' + hm;
    return d.toLocaleDateString([], { month: 'short', day: 'numeric', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' });
  }

  function checked(s) { return s.last_checked ? 'Last checked ' + ago(s.last_checked) : 'Not checked yet'; }

  function agents(s) {
    return s.agents_restart ? 'Your assistants will restart during the update.' : 'Your assistants keep running.';
  }

  // view returns what the section shows for a state.
  function view(s) {
    const v = s.version, cur = s.current;
    switch (s.phase) {
      case 'checking': return { title: 'Checking for updates…', sub: 'Version ' + cur, busy: 'Checking…' };
      case 'uptodate': return { title: 'Lolad is up to date', sub: 'Version ' + cur + ' · ' + checked(s), btn: ['check', 'Check for updates'] };
      case 'available': return { title: 'Lolad ' + v + ' is available', sub: 'You have ' + cur + '. ' + agents(s), notes: true, btn: ['download', 'Download and install', 1] };
      case 'downloading': return { title: 'Downloading Lolad ' + v + '…', sub: s.percent + '%', bar: true, notes: true, busy: 'Downloading…' };
      case 'ready': return { title: 'Lolad ' + v + ' is ready', sub: 'Downloaded and checked. Restart Lolad to finish. ' + agents(s), notes: true, btn: ['restart', 'Restart to finish', 1] };
      case 'restarting': return { title: 'Restarting Lolad…', sub: 'Installing ' + v + '. This window comes back in a moment.', busy: 'Restarting…' };
      case 'failed': return {
        title: { download: 'Couldn’t download the update', restart: 'Couldn’t finish the update' }[s.retry] || 'Couldn’t check for updates',
        sub: s.error || 'Something went wrong.', bad: true, btn: ['retry', 'Try again'] };
      case 'manual': return { title: 'Lolad ' + v + ' is available', sub: 'This version is too old to update in place. Download the new installer.', notes: true, get: true };
      case 'onboarding': return { title: 'Lolad ' + v + ' is available', sub: 'Finish setting up Lolad first. The update will wait.', notes: true };
      case 'updated': return { title: 'Updated to Lolad ' + cur, sub: 'You have the latest version. ' + checked(s), good: true, btn: ['check', 'Check for updates'] };
      case 'unavailable': return { title: 'Lolad ' + cur, sub: s.error || 'This copy of Lolad can’t update itself.' };
    }
    return { title: 'Lolad ' + cur, sub: checked(s), btn: ['check', 'Check for updates'] };
  }

  function button(label, action, primary, disabled) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn-sm' + (primary ? ' btn-primary' : '');
    b.textContent = label;
    if (action) b.dataset.updAction = action;
    b.disabled = !!disabled;
    return b;
  }

  function render() {
    const sec = document.querySelector('[data-updates]');
    if (!sec || !last) return;
    const s = last, v = view(s);
    const row = sec.querySelector('.upd-row');
    row.dataset.phase = s.phase;
    row.classList.toggle('is-bad', !!v.bad);
    row.classList.toggle('is-good', !!v.good);
    sec.querySelector('[data-upd-title]').textContent = v.title;
    sec.querySelector('[data-upd-sub]').textContent = v.sub;
    const bar = sec.querySelector('[data-upd-bar]');
    bar.hidden = !v.bar;
    bar.setAttribute('aria-valuenow', String(s.percent || 0));
    bar.firstElementChild.style.width = (s.percent || 0) + '%';
    const notes = sec.querySelector('[data-upd-notes]');
    notes.hidden = !(v.notes && s.notes);
    notes.textContent = s.notes || '';
    const link = sec.querySelector('[data-upd-link]');
    const url = /^https:\/\//.test(s.notes_url || '') ? s.notes_url : '';
    link.hidden = !(v.notes && url);
    if (url) link.href = url;
    const acts = sec.querySelector('[data-upd-actions]');
    acts.replaceChildren();
    if (v.btn) acts.append(button(v.btn[1], v.btn[0], v.btn[2]));
    else if (v.busy) acts.append(button(v.busy, '', false, true));
    else if (v.get && url) {
      const a = document.createElement('a');
      a.className = 'btn btn-sm btn-primary'; a.href = url; a.target = '_blank'; a.rel = 'noopener';
      a.textContent = 'Get the installer';
      acts.append(a);
    }
    sec.hidden = false;
  }

  window.__loladUpdate = (s) => {
    if (!s || typeof s !== 'object' || typeof s.phase !== 'string') return;
    last = s;
    render();
  };

  function init() {
    const sec = document.querySelector('[data-updates]');
    if (!sec || typeof window.__loladUpdates !== 'function') return;
    if (last) render();
    window.__loladUpdates('state');
    if (location.hash === '#updates') sec.scrollIntoView({ block: 'start' });
  }

  document.addEventListener('click', (e) => {
    const b = e.target.closest && e.target.closest('[data-upd-action]');
    if (!b || typeof window.__loladUpdates !== 'function') return;
    e.preventDefault();
    b.disabled = true; // the next state re-renders the button
    window.__loladUpdates(b.dataset.updAction);
  });
  // The "last checked" wording ages while the page is open.
  setInterval(() => { if (last && last.last_checked) render(); }, 60000);
  document.addEventListener('DOMContentLoaded', init);
  document.addEventListener('htmx:afterSettle', init);
  if (document.readyState !== 'loading') init();
})();
