import { useEffect, useMemo, useState } from 'react';
import { App, type AppActions } from './App';
import type { LegacyActive, Snapshot } from './generated/workflow';
import type { Transport } from './transport';

/** Wires a Transport to the shared App: initial full snapshot, live updates, stale immediately on failure. */
export function Connected({ transport, initialTheme }: { transport: Transport; initialTheme?: 'light' | 'dark' }) {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [legacy, setLegacy] = useState<LegacyActive[]>([]);
  const [stale, setStale] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);
  const [epoch, setEpoch] = useState(0);

  useEffect(() => {
    return transport.subscribe(
      (u) => { setSnapshot(u.snapshot); setLegacy(u.legacyActive); setStale(null); setConnected(true); },
      (msg) => { setStale(msg); setConnected(false); },
    );
  }, [transport, epoch]);

  const actions = useMemo<AppActions>(() => ({
    readonly: transport.readonly,
    stop: (runId, requestId) => transport.stop(runId, requestId),
    settings: async (input) => {
      await transport.settings(input);
      const u = await transport.read();
      setSnapshot(u.snapshot); setLegacy(u.legacyActive);
    },
    reconnect: async () => {
      const u = await transport.read();
      setSnapshot(u.snapshot); setLegacy(u.legacyActive); setStale(null); setConnected(true);
      setEpoch((e) => e + 1);
    },
  }), [transport]);

  return <App snapshot={snapshot} legacyActive={legacy} connected={connected} stale={stale} actions={actions} initialTheme={initialTheme} />;
}
