import {createDeviceEqualizer} from './equalizer-store';

/** Platforms bind only their installed native producer; absent support stays absent. */
export const nativeEqualizer = createDeviceEqualizer();
export type {DeviceEqualizer, DeviceEqualizerSnapshot, DeviceEqualizerStore} from './equalizer-store';
