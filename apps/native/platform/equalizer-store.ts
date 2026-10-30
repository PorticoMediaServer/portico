/** One app/device setting. Applied processing is observed separately by the renderer. */
export type DeviceEqualizer = Readonly<{on: boolean; preamp: number; gains: readonly number[]}>;
export type DeviceEqualizerSnapshot = Readonly<{
  supported: boolean; ready: boolean; pending: boolean; value?: DeviceEqualizer;
}>;
export type DeviceEqualizerPort = Readonly<{
  read: () => Promise<unknown>;
  write: (value: DeviceEqualizer) => Promise<unknown>;
  reset: () => Promise<unknown>;
}>;

const invalid = () => new Error('audio_eq_invalid');
const gain = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value) && value >= -12 && value <= 12 && Number.isInteger(value * 2);
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);

export function deviceEqualizerValue(value: unknown): DeviceEqualizer {
  if (!record(value) || Object.keys(value).sort().join(',') !== 'gains,on,preamp' || typeof value.on !== 'boolean' || !gain(value.preamp) || !Array.isArray(value.gains) || value.gains.length !== 10 || !value.gains.every(gain)) throw invalid();
  return Object.freeze({on: value.on, preamp: value.preamp, gains: Object.freeze([...value.gains])});
}
const same = (a: DeviceEqualizer, b: DeviceEqualizer) => a.on === b.on && a.preamp === b.preamp && a.gains.every((value, index) => value === b.gains[index]);

/** Both entry points share this serialized native record, without a local persistence shadow. */
export function createDeviceEqualizer(port?: DeviceEqualizerPort) {
  let snapshot: DeviceEqualizerSnapshot = Object.freeze({supported: !!port, ready: false, pending: false});
  const listeners = new Set<() => void>();
  let queue: Promise<unknown> = Promise.resolve(), pending = 0;
  const publish = (patch: Partial<DeviceEqualizerSnapshot>) => {
    snapshot = Object.freeze({...snapshot, ...patch});
    for (const listener of listeners) listener();
  };
  const enqueue = <T,>(run: (native: DeviceEqualizerPort) => Promise<T>): Promise<T> => {
    if (!port) return Promise.reject(new Error('audio_eq_unavailable'));
    pending++; publish({pending: true});
    const next = queue.then(() => run(port));
    queue = next.then(() => undefined, () => undefined);
    return next.finally(() => { pending--; publish({pending: pending > 0}); });
  };
  const read = async (native: DeviceEqualizerPort) => {
    const value = deviceEqualizerValue(await native.read());
    publish({ready: true, value}); return value;
  };
  return Object.freeze({
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    refresh: () => enqueue(read),
    update: async (patch: Partial<DeviceEqualizer>): Promise<DeviceEqualizer> => {
      if (!record(patch) || !Object.keys(patch).length || Object.keys(patch).some(key => !['on', 'preamp', 'gains'].includes(key)) || 'on' in patch && typeof patch.on !== 'boolean' || 'preamp' in patch && !gain(patch.preamp) || 'gains' in patch && (!Array.isArray(patch.gains) || patch.gains.length !== 10 || !patch.gains.every(gain))) throw invalid();
      // Capture caller arrays before joining another screen's in-flight operation.
      const captured = {...patch, ...('gains' in patch ? {gains: [...patch.gains!]} : {})};
      return enqueue(async native => {
        const held = snapshot.value ?? await read(native);
        const asked = deviceEqualizerValue({...held, ...captured});
        const confirmed = deviceEqualizerValue(await native.write(asked));
        if (!same(asked, confirmed)) throw invalid();
        publish({ready: true, value: confirmed}); return confirmed;
      });
    },
    reset: () => enqueue(async native => {
      const held = snapshot.value ?? await read(native);
      const confirmed = deviceEqualizerValue(await native.reset());
      if (confirmed.on !== held.on || confirmed.preamp !== 0 || confirmed.gains.some(value => value !== 0)) throw invalid();
      publish({ready: true, value: confirmed}); return confirmed;
    }),
  });
}

export type DeviceEqualizerStore = ReturnType<typeof createDeviceEqualizer>;
