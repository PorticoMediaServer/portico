import {NativeModules} from 'react-native';
import {createDeviceEqualizer, deviceEqualizerValue, type DeviceEqualizer} from './equalizer-store';

const nativeModule = NativeModules.PorticoPlaybackModule as {equalizer?: (value: null | {enabled: boolean; preampDb: number; bands: readonly number[]} | {reset: true}) => Promise<unknown>} | undefined;
const decode = (value: unknown): DeviceEqualizer => {
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).sort().join(',') !== 'bands,enabled,preampDb') throw new Error('audio_eq_invalid');
  const raw = value as Record<string, unknown>;
  return deviceEqualizerValue({on: raw.enabled, preamp: raw.preampDb, gains: raw.bands});
};
const call = async (value: Parameters<NonNullable<NonNullable<typeof nativeModule>['equalizer']>>[0]) => decode(await nativeModule!.equalizer!(value));

export const nativeEqualizer = createDeviceEqualizer(typeof nativeModule?.equalizer === 'function' ? {
  read: () => call(null),
  write: value => call({enabled: value.on, preampDb: value.preamp, bands: value.gains}),
  reset: () => call({reset: true}),
} : undefined);
export type {DeviceEqualizer, DeviceEqualizerSnapshot, DeviceEqualizerStore} from './equalizer-store';
