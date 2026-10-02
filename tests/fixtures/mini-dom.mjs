// Minimal, test-only DOM for running desktop/shell.js and the shared UI factory in
// node:vm. It is NOT a browser: it implements only what those scripts use (HTML
// parsing for innerHTML, simple selectors without combinators, attributes, dataset,
// classList, events with capture/bubble, ShadowRoot, TreeWalker, MutationObserver).
// Unsupported selectors throw so tests fail loudly instead of passing vacuously.
import vm from 'node:vm';

const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr']);
const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: '\u00a0' };
const decode = (s) => s.replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (m, e) => {
  if (e[0] === '#') return String.fromCodePoint(e[1].toLowerCase() === 'x' ? parseInt(e.slice(2), 16) : Number(e.slice(1)));
  return ENTITIES[e.toLowerCase()] ?? m;
});

// ---- selectors -------------------------------------------------------------
function parseCompound(src) {
  const s = src.trim();
  const parts = { tag: null, conds: [] };
  let i = 0;
  const m0 = /^(\*|[a-z][a-z0-9-]*)/i.exec(s);
  if (m0) { if (m0[1] !== '*') parts.tag = m0[1].toLowerCase(); i = m0[0].length; }
  while (i < s.length) {
    const c = s[i];
    if (c === '#' || c === '.') {
      const m = /^[#.]((?:\\.|[\w-])+)/.exec(s.slice(i));
      if (!m) throw new Error(`mini-dom: unsupported selector ${src}`);
      const v = m[1].replace(/\\(.)/g, '$1');
      parts.conds.push(c === '#' ? { attr: 'id', value: v } : { cls: v });
      i += m[0].length;
    } else if (c === '[') {
      const m = /^\[\s*([\w:-]+)\s*(?:=\s*(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'|([\w-]+)))?\s*\]/.exec(s.slice(i));
      if (!m) throw new Error(`mini-dom: unsupported selector ${src}`);
      const raw = m[2] ?? m[3] ?? m[4];
      parts.conds.push({ attr: m[1].toLowerCase(), value: raw === undefined ? undefined : raw.replace(/\\(.)/g, '$1') });
      i += m[0].length;
    } else if (s.startsWith(':not(', i)) {
      let depth = 1;
      let j = i + 5;
      for (; j < s.length && depth; j++) { if (s[j] === '(') depth++; else if (s[j] === ')') depth--; }
      parts.conds.push({ not: parseCompound(s.slice(i + 5, j - 1)) });
      i = j;
    } else {
      throw new Error(`mini-dom: unsupported selector ${src}`);
    }
  }
  return parts;
}

function splitList(sel) {
  const out = [];
  let depth = 0; let quote = null; let cur = '';
  for (const ch of sel) {
    if (quote) { if (ch === quote) quote = null; cur += ch; continue; }
    if (ch === '"' || ch === "'") quote = ch;
    if (ch === '(' || ch === '[') depth++;
    if (ch === ')' || ch === ']') depth--;
    if (ch === ',' && !depth) { out.push(cur); cur = ''; continue; }
    cur += ch;
  }
  out.push(cur);
  return out.map((x) => x.trim()).filter(Boolean);
}

const cache = new Map();
function compile(sel) {
  if (!cache.has(sel)) cache.set(sel, splitList(sel).map(parseCompound));
  return cache.get(sel);
}

function matchCompound(el, c) {
  if (c.tag && el.localName !== c.tag) return false;
  for (const k of c.conds) {
    if (k.not) { if (matchCompound(el, k.not)) return false; continue; }
    if (k.cls) { if (!el.classList.contains(k.cls)) return false; continue; }
    if (!el.hasAttribute(k.attr)) return false;
    if (k.value !== undefined && el.getAttribute(k.attr) !== k.value) return false;
  }
  return true;
}

// ---- nodes -----------------------------------------------------------------
export function createDom() {
  const observers = [];
  let pending = false;
  const notify = () => {
    if (pending || !observers.some((o) => o.active)) return;
    pending = true;
    queueMicrotask(() => { pending = false; for (const o of observers) if (o.active) o.cb([], o); });
  };

  class Node {
    constructor() { this.parentNode = null; this.childNodes = []; this.listeners = []; }
    get ownerDocument() { return document; }
    get parentElement() { return this.parentNode instanceof Element ? this.parentNode : null; }
    get children() { return this.childNodes.filter((n) => n instanceof Element); }
    get textContent() { return this.childNodes.map((n) => n.textContent).join(''); }
    set textContent(v) { this.replaceChildren(...(String(v ?? '') ? [new Text(String(v))] : [])); }
    getRootNode() { let n = this; while (n.parentNode) n = n.parentNode; return n; }
    contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
    _insert(nodes, index) {
      const list = nodes.flatMap((n) => (n instanceof Fragment ? n.childNodes.splice(0) : [typeof n === 'string' ? new Text(n) : n]));
      for (const n of list) { n.remove?.(); n.parentNode = this; }
      this.childNodes.splice(index, 0, ...list);
      notify();
    }
    append(...n) { this._insert(n, this.childNodes.length); }
    prepend(...n) { this._insert(n, 0); }
    appendChild(n) { this.append(n); return n; }
    replaceChildren(...n) { for (const c of this.childNodes) c.parentNode = null; this.childNodes = []; this._insert(n, 0); }
    remove() {
      const p = this.parentNode;
      if (!p) return;
      p.childNodes.splice(p.childNodes.indexOf(this), 1);
      this.parentNode = null;
      notify();
    }
    after(...n) { const p = this.parentNode; p._insert(n, p.childNodes.indexOf(this) + 1); }
    get previousElementSibling() {
      const sibs = this.parentNode?.childNodes || [];
      for (let i = sibs.indexOf(this) - 1; i >= 0; i--) if (sibs[i] instanceof Element) return sibs[i];
      return null;
    }
    *descendants() { for (const c of this.childNodes) { if (c instanceof Element) { yield c; yield* c.descendants(); } } }
    querySelectorAll(sel) { const cs = compile(sel); return [...this.descendants()].filter((e) => cs.some((c) => matchCompound(e, c))); }
    querySelector(sel) { const cs = compile(sel); for (const e of this.descendants()) if (cs.some((c) => matchCompound(e, c))) return e; return null; }
    addEventListener(type, fn, opt) { this.listeners.push({ type, fn, capture: opt === true || !!opt?.capture }); }
    removeEventListener(type, fn, opt) {
      const capture = opt === true || !!opt?.capture;
      this.listeners = this.listeners.filter((l) => !(l.type === type && l.fn === fn && l.capture === capture));
    }
    dispatchEvent(ev) {
      const path = [];
      for (let n = this; n; n = n.parentNode || n.host) path.push(n);
      if (!path.includes(document)) path.push(document);
      ev.target = this;
      const fire = (n, capture) => {
        for (const l of [...n.listeners]) if (l.type === ev.type && l.capture === capture && !ev.stopped) l.fn.call(n, ev);
      };
      for (const n of [...path].reverse()) { if (ev.stopped) break; fire(n, true); }
      if (ev.bubbles) for (const n of path) { if (ev.stopped) break; fire(n, false); }
      return !ev.defaultPrevented;
    }
    get innerHTML() { return this.childNodes.map(serialize).join(''); }
    set innerHTML(html) { this.replaceChildren(); parseInto(this, String(html)); }
  }

  class Text extends Node {
    constructor(v) { super(); this.nodeValue = v; }
    get textContent() { return this.nodeValue; }
    set textContent(v) { this.nodeValue = String(v); }
  }

  class Fragment extends Node {}
  class ShadowRoot extends Fragment {
    constructor(host) { super(); this.host = host; this.adoptedStyleSheets = []; this.activeElement = null; }
  }

  const camel = (k) => k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  const kebab = (k) => k.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);

  class Element extends Node {
    constructor(name) {
      super();
      this.localName = name.toLowerCase();
      this.attrs = new Map();
      this.shadowRoot = null;
      this._value = undefined;
      const styleProps = {};
      this.style = new Proxy(styleProps, {
        get: (t, k) => (k === 'cssText' ? Object.entries(t).map(([a, b]) => `${a}:${b}`).join(';') : t[k] ?? ''),
        set: (t, k, v) => {
          if (k === 'cssText') {
            for (const key of Object.keys(t)) delete t[key];
            for (const decl of String(v).split(';')) { const i = decl.indexOf(':'); if (i > 0) t[camel(decl.slice(0, i).trim())] = decl.slice(i + 1).trim(); }
          } else t[k] = v;
          return true;
        },
      });
      const el = this;
      this.dataset = new Proxy({}, {
        get: (_, k) => el.getAttribute(`data-${kebab(String(k))}`) ?? undefined,
        set: (_, k, v) => { el.setAttribute(`data-${kebab(String(k))}`, v); return true; },
      });
      this.classList = {
        contains: (c) => (el.getAttribute('class') || '').split(/\s+/).includes(c),
        add: (...cs) => { for (const c of cs) if (!el.classList.contains(c)) el.setAttribute('class', `${el.getAttribute('class') || ''} ${c}`.trim()); },
        remove: (...cs) => el.setAttribute('class', (el.getAttribute('class') || '').split(/\s+/).filter((x) => x && !cs.includes(x)).join(' ')),
        toggle: (c, force) => { const on = force ?? !el.classList.contains(c); if (on) el.classList.add(c); else el.classList.remove(c); return on; },
      };
    }
    get tagName() { return this.localName.toUpperCase(); }
    getAttribute(k) { return this.attrs.has(k.toLowerCase()) ? this.attrs.get(k.toLowerCase()) : null; }
    setAttribute(k, v) { this.attrs.set(k.toLowerCase(), String(v)); }
    removeAttribute(k) { this.attrs.delete(k.toLowerCase()); }
    hasAttribute(k) { return this.attrs.has(k.toLowerCase()); }
    toggleAttribute(k, force) { const on = force ?? !this.hasAttribute(k); if (on) this.setAttribute(k, ''); else this.removeAttribute(k); return on; }
    get id() { return this.getAttribute('id') || ''; }
    set id(v) { this.setAttribute('id', v); }
    get type() { return this.getAttribute('type') || (this.localName === 'input' ? 'text' : ''); }
    get hidden() { return this.hasAttribute('hidden'); }
    set hidden(v) { this.toggleAttribute('hidden', !!v); }
    get disabled() { return this.hasAttribute('disabled'); }
    set disabled(v) { this.toggleAttribute('disabled', !!v); }
    get value() {
      if (this._value !== undefined) return this._value;
      if (this.localName === 'select') {
        const opts = this.querySelectorAll('option');
        const sel = opts.find((o) => o.hasAttribute('selected')) || opts[0];
        return sel ? sel.value : '';
      }
      if (this.localName === 'option') return this.getAttribute('value') ?? this.textContent;
      return this.getAttribute('value') ?? '';
    }
    set value(v) { this._value = String(v); }
    get selectionStart() { return this.value.length; }
    setSelectionRange() {}
    getClientRects() { return this.hidden ? [] : [{}]; }
    focus() { const r = this.getRootNode(); if (r instanceof ShadowRoot) r.activeElement = this; document.activeElement = r instanceof ShadowRoot ? r.host : this; }
    closest(sel) { const cs = compile(sel); for (let n = this; n instanceof Element; n = n.parentNode) if (cs.some((c) => matchCompound(n, c))) return n; return null; }
    matches(sel) { return compile(sel).some((c) => matchCompound(this, c)); }
    attachShadow() { if (this.shadowRoot) throw new Error('shadow root already attached'); this.shadowRoot = new ShadowRoot(this); return this.shadowRoot; }
    cloneNode(deep) {
      const c = new Element(this.localName);
      for (const [k, v] of this.attrs) c.attrs.set(k, v);
      c.style.cssText = this.style.cssText;
      if (deep) for (const n of this.childNodes) c.append(n instanceof Text ? new Text(n.nodeValue) : n.cloneNode(true));
      return c;
    }
    click() { this.dispatchEvent(new Event('click', { bubbles: true })); }
  }

  class Document extends Node {
    constructor() { super(); this.activeElement = null; }
    createElement(n) { return new Element(n); }
    createTextNode(v) { return new Text(v); }
    createTreeWalker(root) {
      const texts = [];
      const walk = (n) => { for (const c of n.childNodes) { if (c instanceof Text) texts.push(c); else walk(c); } };
      walk(root);
      let i = -1;
      return { nextNode: () => texts[++i] || null };
    }
  }

  class Event {
    constructor(type, init = {}) { this.type = type; this.bubbles = !!init.bubbles; this.defaultPrevented = false; this.stopped = false; this.key = init.key; }
    preventDefault() { this.defaultPrevented = true; }
    stopPropagation() { this.stopped = true; }
  }

  function serialize(n) {
    if (n instanceof Text) return n.nodeValue.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));
    const attrs = [...n.attrs].map(([k, v]) => ` ${k}="${v.replace(/&/g, '&amp;').replace(/"/g, '&quot;')}"`).join('');
    return VOID.has(n.localName) ? `<${n.localName}${attrs}>` : `<${n.localName}${attrs}>${n.childNodes.map(serialize).join('')}</${n.localName}>`;
  }

  function parseInto(parent, html) {
    const stack = [parent];
    const top = () => stack[stack.length - 1];
    const re = /<!--[\s\S]*?-->|<\/([a-zA-Z][\w-]*)\s*>|<([a-zA-Z][\w-]*)((?:\s+[^\s"'>/=]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'=<>`]+))?)*)\s*(\/?)>|([^<]+|<)/g;
    for (let m = re.exec(html); m; m = re.exec(html)) {
      if (m[1]) {
        const name = m[1].toLowerCase();
        const i = stack.findLastIndex((n, k) => k > 0 && n.localName === name);
        if (i > 0) stack.length = i;
      } else if (m[2]) {
        const el = new Element(m[2]);
        const attrRe = /([^\s"'>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
        for (let a = attrRe.exec(m[3]); a; a = attrRe.exec(m[3])) el.setAttribute(a[1], decode(a[2] ?? a[3] ?? a[4] ?? ''));
        top().childNodes.push(el);
        el.parentNode = top();
        if (!VOID.has(el.localName) && !m[4]) stack.push(el);
      } else if (m[5] !== undefined) {
        const t = new Text(decode(m[5]));
        t.parentNode = top();
        top().childNodes.push(t);
      }
    }
    notify();
  }

  class MutationObserver {
    constructor(cb) { this.cb = cb; this.active = false; observers.push(this); }
    observe() { this.active = true; }
    disconnect() { this.active = false; }
  }

  class CSSStyleSheet { replaceSync(text) { this.text = String(text); } }

  const document = new Document();
  const html = document.createElement('html');
  const body = document.createElement('body');
  html.append(body);
  document.append(html);
  document.documentElement = html;
  document.body = body;

  const window = {};
  const context = vm.createContext({
    window, document, Element, Node, Text, Event, MutationObserver, CSSStyleSheet,
    NodeFilter: { SHOW_TEXT: 4 },
    CSS: { escape: (s) => String(s).replace(/["\\]/g, '\\$&') },
    getComputedStyle: (el) => ({ position: el.style.position || 'static' }),
    matchMedia: () => ({ matches: false }),
    setTimeout, clearTimeout, setInterval, clearInterval, queueMicrotask, crypto: globalThis.crypto,
    console,
  });
  window.window = window;
  return {
    context, window, document, observers, Event,
    run: (code) => vm.runInContext(code, context),
    click: (el) => el.dispatchEvent(new Event('click', { bubbles: true })),
    tick: () => new Promise((r) => setTimeout(r, 0)),
  };
}
