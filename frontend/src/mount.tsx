import { useEffect, useState } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { flushSync } from 'react-dom';
import css from './app.css?inline';
import { App, type AppActions } from './App';
import type { LegacyActive, SettingsInput, Snapshot } from './generated/workflow';
import { assertSettingsInput, isUuid, parseEnvelope, type Transport } from './transport';
import { preferredTheme } from './theme';

/** Actions mapped to the existing desktop host interface. */
export type HostAction =
  | { type: 'reconnect' }
  | { type: 'stop'; runId: string; requestId: string }
  | { type: 'settings'; input: SettingsInput };

export interface MountOptions {
  onAction?: (action: HostAction) => Promise<unknown>;
  readonly?: boolean;
  readonlyNote?: string;
  /** Optional transport supplied by the host (e.g. a read-only connector). The mount never fetches by itself. */
  transport?: Transport;
  theme?: 'light' | 'dark';
}
export interface InitialState { snapshot?: unknown; legacyActive?: unknown }
export interface MountHandle {
  update(snapshot: unknown, legacyActive?: unknown): void;
  setDisconnected(message: string): void;
  destroy(): void;
}

interface View { snapshot: Snapshot | null; legacy: LegacyActive[]; stale: string | null }

/** Validates host data through the same contract; invalid data marks the view stale instead of rendering it. */
function validate(snapshot: unknown, legacy: unknown): { snapshot: Snapshot; legacy: LegacyActive[] } {
  const env = parseEnvelope({ ok: true, data: snapshot, legacyActive: legacy ?? [], sessionToken: 'host' });
  return { snapshot: env.data, legacy: env.legacyActive ?? [] };
}

function Host({ store, actions, theme }: { store: Store; actions: AppActions; theme: 'light' | 'dark' }) {
  const [v, setV] = useState<View>(store.view);
  useEffect(() => store.listen(setV), [store]);
  return <App snapshot={v.snapshot} legacyActive={v.legacy} connected={!v.stale && !!v.snapshot} stale={v.stale} actions={actions} initialTheme={theme} />;
}

class Store {
  view: View = { snapshot: null, legacy: [], stale: null };
  #fn: ((v: View) => void) | null = null;
  listen(fn: (v: View) => void) { this.#fn = fn; fn(this.view); return () => { this.#fn = null; }; }
  set(v: Partial<View>) { this.view = { ...this.view, ...v }; this.#fn?.(this.view); }
}

export function mount(container: Element, initialState: InitialState = {}, options: MountOptions = {}): MountHandle {
  if (!container || typeof (container as Element).attachShadow !== 'function') throw new TypeError('MeerkatUI.mount: container element required');
  const shadow = container.shadowRoot ?? container.attachShadow({ mode: 'open' });
  const style = document.createElement('style');
  style.textContent = css;
  const host = document.createElement('div');
  host.className = 'meerkat-mount-host';
  shadow.replaceChildren(style, host);

  const store = new Store();
  const apply = (snapshot: unknown, legacy: unknown) => {
    try {
      const ok = validate(snapshot, legacy);
      store.set({ snapshot: ok.snapshot, legacy: ok.legacy, stale: null });
    } catch (e) {
      store.set({ stale: e instanceof Error ? e.message : '快照无效' });
    }
  };
  if (initialState.snapshot !== undefined) apply(initialState.snapshot, initialState.legacyActive);

  const t = options.transport;
  const onAction = options.onAction;
  const readonly = !!options.readonly || (!onAction && (!t || t.readonly));
  const call = (a: HostAction): Promise<unknown> => {
    if (onAction) return onAction(a);
    if (t && a.type === 'stop') return t.stop(a.runId, a.requestId);
    if (t && a.type === 'settings') return t.settings(a.input);
    if (t && a.type === 'reconnect') return t.read().then((u) => store.set({ snapshot: u.snapshot, legacy: u.legacyActive, stale: null }));
    return Promise.reject(new Error('宿主未提供操作处理'));
  };
  const actions: AppActions = {
    readonly,
    readonlyNote: options.readonlyNote,
    intervention: t?.intervention,
    stop: (runId, requestId) => {
      if (readonly) return Promise.reject(new Error('只读视图'));
      if (!isUuid(runId) || !isUuid(requestId)) return Promise.reject(new Error('无效的运行 ID'));
      return call({ type: 'stop', runId, requestId });
    },
    settings: (input) => {
      if (readonly) return Promise.reject(new Error('只读视图'));
      assertSettingsInput(input);
      return call({ type: 'settings', input });
    },
    reconnect: onAction || t ? () => call({ type: 'reconnect' }) : undefined,
  };

  let unsubscribe: (() => void) | null = t
    ? t.subscribe((u) => store.set({ snapshot: u.snapshot, legacy: u.legacyActive, stale: null }), (m) => store.set({ stale: m }))
    : null;

  let root: Root | null = createRoot(host);
  flushSync(() => root!.render(<Host store={store} actions={actions} theme={options.theme ?? preferredTheme()} />));

  return {
    update(snapshot, legacyActive = []) { if (root) apply(snapshot, legacyActive); },
    setDisconnected(message) { if (root) store.set({ stale: String(message || '连接中断').slice(0, 200) }); },
    destroy() {
      if (!root) return;
      unsubscribe?.();
      unsubscribe = null;
      root.unmount();
      root = null;
      shadow.replaceChildren();
    },
  };
}
