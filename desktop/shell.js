// Injected into the Codex renderer via CDP Runtime.evaluate as
//   `(<this file>)(state, iconSrc, assets)`.
// Must be a single function expression. Idempotent: repeated calls reuse window.__meerkat
// while assets.version matches; a different (or missing, i.e. pre-versioned) monitor is torn
// down first so stale render handlers are never reused after a code upgrade.
//
// assets = { version, load? }: `load` is a function expression built by injector.mjs around the
// locally trusted React mount bundle (internal/web/assets/mount/meerkat-ui.js); calling it returns
// an adapter `(container, {readonlyNote, theme, onAction}) => {update, setDisconnected, destroy}`
// backed by MeerkatUI.mount(..., {readonly: true}). The mount attaches its own ShadowRoot (with its
// inlined CSS) to the container, nested inside the overlay's ShadowRoot, so host and Meerkat
// styles never mix. When the monitor is missing or outdated and no `load` was sent, returns
// {needAssets:true}.
//
// state: null (only re-attach) | 'remove' | {kind:'workflow',data,legacyActive,at}
//        | {kind:'legacy',legacyActive,count,at} | {kind:'error',message,at}
// The host view is read-only: no stop/settings writes and no command execution. "Reconnect"
// only asks the injector (via an optional CDP binding) to poll once more.
//
// VERSION-SENSITIVE: relies on Codex renderer internals that are not a public API:
//   [data-app-action-sidebar-scroll]     sidebar navigation container
//   [data-app-shell-main-content-layout] main content area
//   a sidebar button/link labelled "Plugins" (preferred attachment reference), else
//   [data-sidebar-destination="builtin:automations"] native "Scheduled" nav (Codex 26.924; outside the scroll)
//   #app-shell-sidebar                   native sidebar root (clicks there close the overlay)
// Any Codex update may rename these; the function then returns {ok:false, missing:[...]}.
(function meerkatShell(state, iconSrc, assets) {
  const SIDEBAR = '[data-app-action-sidebar-scroll]';
  const MAIN = '[data-app-shell-main-content-layout]';
  const ENTRY = 'data-meerkat-entry';
  const VIEW = 'data-meerkat-view';
  const SCHEDULED = '[data-sidebar-destination="builtin:automations"]';
  const SIDEBAR_ROOT = '#app-shell-sidebar';
  const NATIVE_DEST = '[data-sidebar-destination]';
  const PLUGIN_LABELS = ['Plugins', '插件'];
  const BINDING = '__meerkatReconnect';
  const RECONNECT_TIMEOUT_MS = 10000;
  const READONLY_NOTE = 'Codex 桌面视图为只读（非官方实验适配器）：不能在这里停止运行或修改设置，请使用 coordinator CLI。';
  // Attributes that would make the clone impersonate the reference's route/selection state.
  const STRIP = ['id', 'href', 'aria-current', 'aria-selected', 'data-state', 'data-active', 'data-selected',
    'data-sidebar-destination'];
  const version = assets && typeof assets.version === 'string' ? assets.version : '';

  // Tears down any monitor, including pre-versioned ones that only know remove()/onClick/observer.
  const teardown = (old) => {
    if (!old || typeof old !== 'object') return;
    try { if (typeof old.remove === 'function') old.remove(); } catch { /* best effort */ }
    try { old.observer?.disconnect?.(); } catch { /* best effort */ }
    try { old.ui?.destroy?.(); } catch { /* best effort */ }
    clearTimeout(old.timer);
    if (typeof old.onClick === 'function') document.removeEventListener('click', old.onClick, true);
    document.querySelectorAll(`[${ENTRY}], [${VIEW}]`).forEach((n) => n.remove());
    if (window.__meerkat === old) delete window.__meerkat;
  };

  if (state === 'remove') { teardown(window.__meerkat); return { ok: true }; }

  let monitor = window.__meerkat;
  if (monitor && (!version || monitor.version !== version)) {
    if (!assets || typeof assets.load !== 'function') return { ok: false, needAssets: true, missing: ['Meerkat UI assets'] };
    teardown(monitor);
    monitor = null;
  }
  if (!monitor && (!assets || typeof assets.load !== 'function')) {
    return { ok: false, needAssets: true, missing: ['Meerkat UI assets'] };
  }

  const lineIcon = (size) => {
    const icon = document.createElement('span');
    icon.setAttribute('data-meerkat-icon', '');
    icon.setAttribute('aria-hidden', 'true');
    icon.style.cssText = `display:inline-block;width:${size}px;height:${size}px;flex:none;background:currentColor;`
      + 'mask-position:center;mask-size:contain;mask-repeat:no-repeat;'
      + '-webkit-mask-position:center;-webkit-mask-size:contain;-webkit-mask-repeat:no-repeat;';
    icon.style.maskImage = `url("${iconSrc}")`;
    icon.style.webkitMaskImage = `url("${iconSrc}")`;
    return icon;
  };

  // Codex marks its theme on <html> in some versions; otherwise follow the OS preference.
  const hostTheme = () => {
    const el = document.documentElement;
    const cls = el ? el.classList : null;
    if (cls && cls.contains('dark')) return 'dark';
    if (cls && cls.contains('light')) return 'light';
    return typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  };

  if (!monitor) {
    const createUI = assets.load();
    if (typeof createUI !== 'function') return { ok: false, missing: ['Meerkat UI mount'] };
    monitor = window.__meerkat = {
      version, createUI, state: null, last: null, error: null,
      open: false, ui: null, observer: null, timer: 0, savedPosition: null, waiters: [],
    };

    const findPlugins = (sidebar) => {
      for (const node of sidebar.querySelectorAll('button, a, [role="button"], [role="link"]')) {
        if (node.hasAttribute(ENTRY)) continue;
        if (PLUGIN_LABELS.includes((node.textContent || '').trim())) return node;
      }
      return null;
    };

    // Replaces the cloned label/icon; the line icon inherits currentColor from the native entry.
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
      const icon = lineIcon(22);
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
        if (monitor.open) entry.setAttribute('aria-current', 'page');
      }
      if (!entry.querySelector('[data-meerkat-icon]')) relabel(entry);
      // Re-create the overlay only if a rerender dropped it (avoids observer feedback loops).
      if (monitor.open && !main.querySelector(`[${VIEW}]`)) monitor.show();
      return { ok: true };
    };

    const settle = (err) => {
      for (const w of monitor.waiters.splice(0)) {
        clearTimeout(w.timer);
        if (err) w.reject(new Error(err)); else w.resolve();
      }
    };

    // Read-only host: only "reconnect" is supported, and it merely asks the injector to poll.
    const onAction = (action) => {
      if (action?.type !== 'reconnect') return Promise.reject(new Error(READONLY_NOTE));
      const call = window[BINDING];
      if (typeof call !== 'function') return Promise.reject(new Error('桌面适配器未连接，请重新运行 desktop/injector.mjs'));
      return new Promise((resolve, reject) => {
        const w = { resolve, reject, timer: 0 };
        w.timer = setTimeout(() => {
          monitor.waiters = monitor.waiters.filter((x) => x !== w);
          reject(new Error('重连超时'));
        }, RECONNECT_TIMEOUT_MS);
        monitor.waiters.push(w);
        try { call('reconnect'); } catch { settle('无法通知桌面适配器'); }
      });
    };

    // Applies the last known snapshot, then the current error (if any) so history stays visibly stale.
    monitor.paint = () => {
      const ui = monitor.ui;
      if (!ui) return;
      if (monitor.last) ui.update(monitor.last.data, monitor.last.legacyActive);
      if (monitor.error) ui.setDisconnected(monitor.error);
      else if (!monitor.last) ui.setDisconnected('正在等待本地状态服务');
    };

    monitor.apply = (s) => {
      if (!s || typeof s !== 'object') return;
      monitor.state = s;
      if (s.kind === 'workflow' || s.kind === 'legacy') {
        monitor.last = { data: s.kind === 'workflow' ? s.data : null, legacyActive: Array.isArray(s.legacyActive) ? s.legacyActive : [] };
        monitor.error = null;
        if (monitor.ui) monitor.ui.update(monitor.last.data, monitor.last.legacyActive);
        settle(null);
      } else {
        monitor.error = (typeof s.message === 'string' && s.message) || '状态服务不可用';
        if (monitor.ui) monitor.ui.setDisconnected(monitor.error);
        settle(monitor.error);
      }
    };

    monitor.show = () => {
      const main = document.querySelector(MAIN);
      if (!main) return;
      let view = main.querySelector(`[${VIEW}]`);
      if (!view) {
        if (monitor.ui) { try { monitor.ui.destroy(); } catch { /* detached by host rerender */ } monitor.ui = null; }
        view = document.createElement('section');
        view.setAttribute(VIEW, '');
        view.setAttribute('aria-label', 'Meerkat');
        // `contain` keeps the UI's fixed-position sheets inside the overlay.
        view.style.cssText = 'position:absolute;inset:0;z-index:50;overflow:auto;contain:layout paint;';
        if (getComputedStyle(main).position === 'static') {
          monitor.savedPosition = main.style.position;
          main.style.position = 'relative';
        }
        // Outer ShadowRoot isolates the host; the React mount nests its own ShadowRoot in `root`.
        const shadow = view.attachShadow({ mode: 'open' });
        const root = document.createElement('div');
        root.setAttribute('data-meerkat-mount', '');
        root.style.cssText = 'display:block;min-height:100%;';
        shadow.append(root);
        main.append(view);
        try {
          monitor.ui = monitor.createUI(root, { readonly: true, readonlyNote: READONLY_NOTE, theme: hostTheme(), onAction });
        } catch (e) {
          monitor.close();
          throw e;
        }
        monitor.paint();
      }
      monitor.open = true;
      const entry = document.querySelector(`[${ENTRY}]`);
      if (entry) entry.setAttribute('aria-current', 'page');
    };

    monitor.close = () => {
      monitor.open = false;
      if (monitor.ui) { try { monitor.ui.destroy(); } catch { /* already detached */ } monitor.ui = null; }
      document.querySelectorAll(`[${VIEW}]`).forEach((v) => {
        const main = v.parentElement;
        v.remove();
        if (main && monitor.savedPosition !== null) main.style.position = monitor.savedPosition;
      });
      monitor.savedPosition = null;
      const entry = document.querySelector(`[${ENTRY}]`);
      if (entry) entry.removeAttribute('aria-current');
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
      settle('Meerkat 已移除');
      monitor.observer.disconnect();
      clearTimeout(monitor.timer);
      document.removeEventListener('click', monitor.onClick, true);
      document.querySelectorAll(`[${ENTRY}]`).forEach((n) => n.remove());
      if (window.__meerkat === monitor) delete window.__meerkat;
    };
  }

  monitor.apply(state);
  const result = monitor.ensure();
  return { ...result, version: monitor.version };
})
