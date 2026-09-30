// Injected into the Codex renderer via CDP Runtime.evaluate as `(<this file>)(state, iconSrc)`.
// Must be a single function expression. Idempotent: repeated calls reuse window.__meerkat.
//
// VERSION-SENSITIVE: relies on Codex renderer internals that are not a public API:
//   [data-app-action-sidebar-scroll]     sidebar navigation container
//   [data-app-shell-main-content-layout] main content area
//   a sidebar button/link labelled "Plugins" (preferred attachment reference), else
//   [data-sidebar-destination="builtin:automations"] native "Scheduled" nav (Codex 26.924; outside the scroll)
//   #app-shell-sidebar                   native sidebar root (clicks there close the overlay)
// Any Codex update may rename these; the function then returns {ok:false, missing:[...]}.
(function meerkatShell(state, iconSrc) {
  const SIDEBAR = '[data-app-action-sidebar-scroll]';
  const MAIN = '[data-app-shell-main-content-layout]';
  const ENTRY = 'data-meerkat-entry';
  const VIEW = 'data-meerkat-view';
  const SCHEDULED = '[data-sidebar-destination="builtin:automations"]';
  const SIDEBAR_ROOT = '#app-shell-sidebar';
  const NATIVE_DEST = '[data-sidebar-destination]';
  const PLUGIN_LABELS = ['Plugins', '插件'];
  // Attributes that would make the clone impersonate the reference's route/selection state.
  const STRIP = ['id', 'href', 'aria-current', 'aria-selected', 'data-state', 'data-active', 'data-selected',
    'data-sidebar-destination'];

  let monitor = window.__meerkat;
  if (!monitor) {
    monitor = window.__meerkat = { state: null, open: false, observer: null, timer: 0, savedPosition: null };

    const findPlugins = (sidebar) => {
      for (const node of sidebar.querySelectorAll('button, a, [role="button"], [role="link"]')) {
        if (node.hasAttribute(ENTRY)) continue;
        if (PLUGIN_LABELS.includes((node.textContent || '').trim())) return node;
      }
      return null;
    };

    const relabel = (node) => {
      const oldIcon = node.querySelector('svg, img');
      const iconParent = oldIcon?.parentElement;
      node.querySelectorAll('svg, img').forEach((n) => n.remove());
      const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
      let done = false;
      for (let t = walker.nextNode(); t; t = walker.nextNode()) {
        if (!done && t.nodeValue.trim()) { t.nodeValue = 'Meerkat'; done = true; } else if (t.nodeValue.trim()) t.nodeValue = '';
      }
      if (!done) node.textContent = 'Meerkat';
      const icon = document.createElement('img');
      icon.setAttribute('data-meerkat-icon', '');
      icon.src = iconSrc;
      icon.alt = '';
      icon.setAttribute('aria-hidden', 'true');
      icon.style.cssText = 'width:18px;height:18px;flex:none;';
      if (iconParent) iconParent.prepend(icon);
      else node.prepend(icon);
    };

    monitor.ensure = () => {
      const sidebar = document.querySelector(SIDEBAR);
      const main = document.querySelector(MAIN);
      const ref = (sidebar && findPlugins(sidebar)) || document.querySelector(`${SCHEDULED}:not([${ENTRY}])`);
      const missing = [];
      if (!sidebar) missing.push(SIDEBAR);
      if (!main) missing.push(MAIN);
      if (sidebar && !ref) missing.push(`sidebar "Plugins" entry or ${SCHEDULED}`);
      if (missing.length) return { ok: false, missing };

      let entry = document.querySelector(`[${ENTRY}]`);
      if (!entry || entry.previousElementSibling !== ref) {
        document.querySelectorAll(`[${ENTRY}]`).forEach((n) => n.remove());
        entry = ref.cloneNode(true);
        for (const n of [entry, ...entry.querySelectorAll('*')]) for (const a of STRIP) n.removeAttribute(a);
        entry.setAttribute(ENTRY, '');
        entry.setAttribute('aria-label', 'Meerkat');
        relabel(entry);
        entry.addEventListener('click', (e) => {
          e.preventDefault();
          e.stopPropagation();
          monitor.show();
        });
        ref.after(entry);
      }
      if (!entry.querySelector('[data-meerkat-icon]')) relabel(entry);
      // Re-create the overlay only if a rerender dropped it (avoids observer feedback loops).
      if (monitor.open && !main.querySelector(`[${VIEW}]`)) monitor.show();
      return { ok: true };
    };

    monitor.show = () => {
      const main = document.querySelector(MAIN);
      if (!main) return;
      let view = main.querySelector(`[${VIEW}]`);
      if (!view) {
        view = document.createElement('section');
        view.setAttribute(VIEW, '');
        view.setAttribute('aria-label', 'Meerkat');
        view.style.cssText = 'position:absolute;inset:0;z-index:50;overflow:auto;padding:24px 28px;'
          + 'background:Canvas;color:CanvasText;font:14px/1.5 system-ui,-apple-system,sans-serif;';
        if (getComputedStyle(main).position === 'static') {
          monitor.savedPosition = main.style.position;
          main.style.position = 'relative';
        }
        main.append(view);
      }
      monitor.open = true;
      const entry = document.querySelector(`[${ENTRY}]`);
      if (entry) entry.setAttribute('aria-current', 'page');
      monitor.render(view);
    };

    monitor.close = () => {
      monitor.open = false;
      document.querySelectorAll(`[${VIEW}]`).forEach((v) => {
        const main = v.parentElement;
        v.remove();
        if (main && monitor.savedPosition !== null) main.style.position = monitor.savedPosition;
      });
      monitor.savedPosition = null;
      const entry = document.querySelector(`[${ENTRY}]`);
      if (entry) entry.removeAttribute('aria-current');
    };

    monitor.render = (view) => {
      const s = monitor.state || { state: 'error', message: 'Waiting for status…' };
      const el = (tag, text, css) => {
        const n = document.createElement(tag);
        if (text !== undefined) n.textContent = text;
        if (css) n.style.cssText = css;
        return n;
      };
      const muted = 'opacity:.7;';
      const heading = el('div', undefined, 'display:flex;align-items:center;gap:10px;margin:0 0 4px;');
      const brandmark = el('img');
      brandmark.src = iconSrc;
      brandmark.alt = '';
      brandmark.style.cssText = 'width:28px;height:28px;flex:none;';
      heading.append(brandmark, el('h2', 'Meerkat', 'margin:0;font-size:18px;font-weight:600;'));
      const nodes = [heading];
      const status = el('p', '', 'margin:0 0 16px;' + muted);
      status.setAttribute('role', 'status');
      nodes.push(status);
      if (s.state !== 'ok') {
        status.textContent = s.message || 'Status unavailable';
        nodes.push(el('p', 'Running state unknown', 'font-weight:600;'));
      } else {
        status.textContent = 'Live · updated ' + new Date(s.at || Date.now()).toLocaleTimeString();
        nodes.push(el('p', `${s.count} running`, 'margin:0 0 8px;font-weight:600;'));
        const list = el('ul', undefined, 'list-style:none;margin:0;padding:0;');
        for (const a of s.agents) {
          const li = el('li', undefined, 'padding:10px 0;border-top:1px solid color-mix(in srgb, CanvasText 15%, transparent);');
          li.append(
            el('div', a.task || '(untitled task)', 'font-weight:500;overflow-wrap:anywhere;'),
            el('div', [a.model || '—', a.worktree || '—', monitor.elapsed(a.startedAt)].join(' · '), muted + 'font-size:13px;overflow-wrap:anywhere;'),
          );
          list.append(li);
        }
        nodes.push(list);
      }
      view.replaceChildren(...nodes);
    };

    monitor.elapsed = (startedAt) => {
      const t = Date.parse(startedAt);
      if (!Number.isFinite(t)) return '—';
      const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
      const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
      return h ? `${h}h ${String(m).padStart(2, '0')}m` : m ? `${m}m ${String(s % 60).padStart(2, '0')}s` : `${s}s`;
    };

    // Clicking any native sidebar navigation closes the overlay and reveals native content.
    monitor.onClick = (e) => {
      if (!monitor.open || !(e.target instanceof Element)) return;
      if (e.target.closest(`[${ENTRY}]`)) return;
      if (e.target.closest(`${SIDEBAR}, ${SIDEBAR_ROOT}, ${NATIVE_DEST}`)) monitor.close();
    };
    document.addEventListener('click', monitor.onClick, true);

    // Codex rerenders the sidebar; re-attach the entry after DOM changes (debounced).
    monitor.observer = new MutationObserver(() => {
      clearTimeout(monitor.timer);
      monitor.timer = setTimeout(() => monitor.ensure(), 100);
    });
    monitor.observer.observe(document.body, { childList: true, subtree: true });

    monitor.remove = () => {
      monitor.close();
      monitor.observer.disconnect();
      clearTimeout(monitor.timer);
      document.removeEventListener('click', monitor.onClick, true);
      document.querySelectorAll(`[${ENTRY}]`).forEach((n) => n.remove());
      delete window.__meerkat;
    };
  }

  if (state === 'remove') { monitor.remove(); return { ok: true }; }
  if (state) monitor.state = state;
  const result = monitor.ensure();
  if (result.ok && monitor.open) {
    const view = document.querySelector(`[${VIEW}]`);
    if (view) monitor.render(view);
  }
  return result;
})
