import {nativeDeviceSettings} from './device-settings';

export type ReaderHintMemory = Readonly<{seen: () => boolean; markSeen: () => void}>;

type Owner = Parameters<typeof nativeDeviceSettings>[0];

/** The reader's first-run hint (tap zones, swipe, press and hold) shows once on this device. The reader's web view keeps no
 * storage of its own, so this device's settings remember the "Got it". A device whose settings can't be read shows it. */
export function nativeReaderHintMemory(owner: Owner): ReaderHintMemory {
  return Object.freeze({
    seen: () => {try {return nativeDeviceSettings(owner).read('readerHintSeen') === true;} catch {return false;}},
    // Written once: a guest that repeats Got it costs nothing once this device has it.
    markSeen: () => {try {
      const settings = nativeDeviceSettings(owner);
      if (settings.read('readerHintSeen') !== true) void settings.write('readerHintSeen', true).catch(() => {});
    } catch { /* retired: shows again next time */ }},
  });
}
