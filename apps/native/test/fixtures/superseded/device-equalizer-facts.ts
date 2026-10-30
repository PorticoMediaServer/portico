// Test-only historical model; no production consumer mounts this module.
import type {DeviceEqualizer, DeviceEqualizerSnapshot} from '../../../src-next/platform/playback/equalizer';

/** Settings states only what the one device store has actually established. */
export type NativeSettingsEqualizerFacts =
  | Readonly<{state: 'unsupported'}>
  | Readonly<{state: 'unconfirmed'; pending: boolean}>
  | Readonly<{state: 'confirmed'; pending: boolean; value: DeviceEqualizer}>;

/** Projects the confirmed device record without copying, defaulting or
 * claiming any applied processing. A value is exposed only once support and
 * readiness are both established, so an unready snapshot never leaks a stale
 * record; a pending write keeps the last confirmed value by identity until its
 * native acknowledgement arrives. Absent support stays absent. */
export function nativeSettingsEqualizerFacts(snapshot: DeviceEqualizerSnapshot): NativeSettingsEqualizerFacts {
  if (!snapshot.supported) return {state: 'unsupported'};
  if (!snapshot.ready || !snapshot.value) return {state: 'unconfirmed', pending: snapshot.pending};
  return {state: 'confirmed', pending: snapshot.pending, value: snapshot.value};
}
