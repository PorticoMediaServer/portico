import {NativeModules} from 'react-native';
import {createDeviceEqualizer, deviceEqualizerValue, type DeviceEqualizer} from './equalizer-store';

type AppleAudioProcessing = {
  audioProcessing?: () => Promise<unknown>;
  setAudioEqualizer?: (value: {enabled: boolean; preampDb: number; bands: readonly number[]}) => Promise<unknown>;
  resetAudioEqualizer?: () => Promise<unknown>;
};
const native = NativeModules.PorticoPlaybackModule as AppleAudioProcessing | undefined;
const decode = (value: unknown): DeviceEqualizer => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('audio_eq_invalid');
  const raw = value as Record<string, unknown>;
  // The native snapshot also carries independent rate/route observations. Only the
  // committed app-local EQ record belongs in this shared settings facade.
  return deviceEqualizerValue({on: raw.eqEnabled, preamp: raw.eqPreampDb, gains: raw.eqBands});
};

export const nativeEqualizer = createDeviceEqualizer(
  typeof native?.audioProcessing === 'function' && typeof native.setAudioEqualizer === 'function' && typeof native.resetAudioEqualizer === 'function' ? {
    read: async () => decode(await native.audioProcessing!()),
    write: async value => decode(await native.setAudioEqualizer!({enabled: value.on, preampDb: value.preamp, bands: [...value.gains]})),
    reset: async () => decode(await native.resetAudioEqualizer!()),
  } : undefined,
);
export type {DeviceEqualizer, DeviceEqualizerSnapshot, DeviceEqualizerStore} from './equalizer-store';
