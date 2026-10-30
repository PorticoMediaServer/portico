import {invoke} from '@tauri-apps/api/core';
import type {EqualizerSettings} from '../../../web/src/platform/playback/audio/equalizer.ts';
export type {EqualizerSettings} from '../../../web/src/platform/playback/audio/equalizer.ts';

// No output/account/source identifier: Settings and the player's future shortcut use this
// single app-local native preference. Shared controls follow music-plus.html at 812dacac.
export type EqualizerSnapshot = Readonly<{revision: number; settings: EqualizerSettings; applied: boolean}>;

function snapshot(value: unknown): EqualizerSnapshot {
  if (!value || typeof value !== 'object') throw new Error('preferences_unavailable');
  const wire = value as Record<string,unknown>, config = wire.settings as Record<string,unknown> | undefined;
  const gain = (v:unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= -12 && v <= 12 && Number.isInteger(v * 2);
  if (!Number.isSafeInteger(wire.revision) || Number(wire.revision) < 0 || typeof wire.applied !== 'boolean' || !config || typeof config.enabled !== 'boolean' || !gain(config.preampDb) || !Array.isArray(config.bandsDb) || config.bandsDb.length !== 10 || !config.bandsDb.every(gain)) throw new Error('preferences_unavailable');
  const settings: EqualizerSettings = Object.freeze({enabled:config.enabled,preampDb:config.preampDb,bandsDb:Object.freeze([...config.bandsDb]) as EqualizerSettings['bandsDb']});
  return Object.freeze({revision:Number(wire.revision),settings,applied:wire.applied});
}

export const desktopEqualizer = Object.freeze({
  read: () => invoke<unknown>('desktop_equalizer').then(snapshot),
  set: (expectedRevision:number,settings:EqualizerSettings) => invoke<unknown>('desktop_equalizer_change',{change:{action:'set',expectedRevision,settings}}).then(snapshot),
  resetToFlat: (expectedRevision:number) => invoke<unknown>('desktop_equalizer_change',{change:{action:'resetFlat',expectedRevision}}).then(snapshot),
});
