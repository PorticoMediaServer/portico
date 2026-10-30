import type {DeviceEqualizer, DeviceEqualizerStore} from '../../platform/playback/equalizer';

export type NativeEqualizerTarget = 'preamp' | number;
export const nativeEqualizerGain = (value: number): number | undefined => Number.isFinite(value) ? Math.max(-12, Math.min(12, Math.round(value * 2) / 2)) : undefined;
export const nativeEqualizerFlat = (value: DeviceEqualizer): boolean => value.preamp === 0 && value.gains.every(gain => gain === 0);

/** Serializes editor intents against the one installed device store. It holds
 * no EQ value, output key, persistence or applied-processing observation. Read
 * the confirmed value at dispatch so rapid edits to two bands retain both. */
export function nativeEqualizerControls(store: DeviceEqualizerStore) {
  let pending: Promise<unknown> = Promise.resolve();
  const run = (command: () => Promise<void>) => {
    const next = pending.then(command); pending = next.catch(() => {}); return next;
  };
  const read = async () => {
    const state = store.getSnapshot();
    if (!state.supported) return undefined;
    if (!state.ready) await store.refresh();
    return store.getSnapshot().value;
  };
  const validTarget = (target: NativeEqualizerTarget) => target === 'preamp' || Number.isInteger(target) && target >= 0 && target <= 9;
  const writeGain = async (held: DeviceEqualizer, target: NativeEqualizerTarget, value: number) => {
    if (target === 'preamp') await store.update({preamp: value});
    else {const gains = [...held.gains]; gains[target] = value; await store.update({gains});}
  };
  return Object.freeze({
    enable: (on: boolean) => run(async () => {if (await read()) await store.update({on});}),
    gain: (target: NativeEqualizerTarget, asked: number) => run(async () => {
      const value = nativeEqualizerGain(asked);
      if (value === undefined || !validTarget(target)) return;
      const held = await read(); if (!held?.on) return;
      await writeGain(held, target, value);
    }),
    // A held TV key queues every half-dB step against the last confirmed
    // native record, rather than repeatedly capturing the same old value.
    nudge: (target: NativeEqualizerTarget, direction: -1 | 1) => run(async () => {
      if (!validTarget(target)) return;
      const held = await read(); if (!held?.on) return;
      const current = target === 'preamp' ? held.preamp : held.gains[target];
      if (current === undefined) return;
      const next = nativeEqualizerGain(current + direction * .5);
      if (next !== undefined && next !== current) await writeGain(held, target, next);
    }),
    reset: () => run(async () => {const value = await read(); if (value?.on && !nativeEqualizerFlat(value)) await store.reset();}),
    settled: () => pending,
  });
}
