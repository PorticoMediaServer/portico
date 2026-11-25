// N1-2: Android exports the same equalizer methods as Apple (`audioProcessing`,
// `setAudioEqualizer`, `resetAudioEqualizer` on PorticoPlaybackModule), so both use one adapter.
export {nativeEqualizer} from './equalizer.ios';
export type {DeviceEqualizer, DeviceEqualizerSnapshot, DeviceEqualizerStore} from './equalizer-store';
