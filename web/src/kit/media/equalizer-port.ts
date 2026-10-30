import {flatEqualizer, parseEqualizer, type EqualizerSettings} from '../../platform/playback/audio/equalizer';
import {loadDeviceEqualizer, saveDeviceEqualizer, resetDeviceEqualizer, subscribeDeviceEqualizer} from '../../platform/playback/audio/equalizer-store';

/** Controlled UI adapter. Desktop can supply its app's checked native store instead. */
export type EqualizerPort = Readonly<{
  getSnapshot(): EqualizerSettings | null;
  subscribe(listener: () => void): () => void;
  start(): () => void;
  change(value: EqualizerSettings): Promise<void>;
  reset(): Promise<void>;
}>;

/** One device-local record shared with the renderer and both settings/player entry points.
 * Pending writes keep the newest slider value on screen; failed writes reload persisted intent. */
export function createBrowserEqualizerPort(reportError: (error: unknown) => void): EqualizerPort {
  let value: EqualizerSettings | null = null, pending = 0, revision = 0, active = 0;
  let off: (() => void) | undefined;
  const listeners = new Set<() => void>();
  const publish = (next: EqualizerSettings) => { value = next; for (const listener of listeners) listener(); };
  const read = async () => { const expected = revision; try { const next = await loadDeviceEqualizer(); if (active && expected === revision && pending === 0) publish(next); } catch (error) { if (active) reportError(error); } };
  const write = async (next: EqualizerSettings, save: () => Promise<EqualizerSettings>) => {
    ++revision; ++pending; publish(next);
    try { await save(); }
    finally { if (--pending === 0 && active) void read(); }
  };
  return {
    getSnapshot: () => value,
    subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    start: () => {
      if (++active === 1) { off = subscribeDeviceEqualizer(next => { if (pending === 0) { ++revision; publish(next); } }); void read(); }
      let ended = false;
      return () => { if (!ended) { ended = true; if (--active === 0) { off?.(); off = undefined; ++revision; } } };
    },
    change: next => { const parsed = parseEqualizer(next); return write(parsed, () => saveDeviceEqualizer(parsed)); },
    reset: () => write({...flatEqualizer, enabled: value?.enabled ?? false}, resetDeviceEqualizer),
  };
}
