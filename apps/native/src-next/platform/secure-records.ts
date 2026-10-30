import {NativeModules} from 'react-native';

/**
 * This device's saved records (sign-ins, remembered servers, profile trust, device settings) are
 * sealed by a key the system keeps. On Android before version 12 and on Fire OS the system can
 * lose that key while the records stay, and it cannot say for certain that it has: the same
 * answer comes from a key service that is failing for a moment. Nothing is removed on that
 * answer. The app looks again, and if the records are still unreadable it asks the person
 * whether to reset this device; only their confirmation removes anything.
 *
 * Apple keeps each record in the Keychain with no second key, so it has no such state.
 */
type SecureRecordsRuntime = {
  prepareSecureRecords?: () => Promise<boolean>;
  resetSecureRecords?: () => Promise<boolean>;
  restart?: () => Promise<boolean>;
};
const runtime = (): SecureRecordsRuntime | undefined => NativeModules.PorticoRuntime as SecureRecordsRuntime | undefined;
let recoveredNotice: object | undefined;
/** A preparation replay must not silently consume the native one-time notice. */
export const secureRecordsRecoveredNotice = (): object | undefined => recoveredNotice;
export const acknowledgeSecureRecordsRecovered = (expected: object): void => {if (recoveredNotice === expected) recoveredNotice = undefined;};
const prepared = (recovered: boolean): boolean => {if (recovered) recoveredNotice = Object.freeze({}); return recovered;};

/** The code launch preparation rejects with when the records are there and their key is not. */
export const SECURE_RECORDS_KEY_LOST = 'secure_records_key_lost';
export const isSecureRecordsLost = (error: unknown): boolean => !!error && typeof error === 'object' && (error as {code?: unknown}).code === SECURE_RECORDS_KEY_LOST;

/** How long to wait before looking again: a key service that was only starting answers by then. */
export const SECOND_LOOK_MS = 1500;

/**
 * Launch preparation, looking twice before concluding the key is lost. Resolves true when the
 * store reset itself (a loss the system could confirm) and the person should be told their
 * sign-in ended; false in the ordinary case. Rejects with `secure_records_key_lost` only when
 * both looks said so. An older native runtime has no preparation step: false.
 */
export async function prepareSecureRecords(wait: (ms: number) => Promise<void> = ms => new Promise(resolve => setTimeout(resolve, ms))): Promise<boolean> {
  const native = runtime();
  const prepare = native?.prepareSecureRecords;
  if (typeof prepare !== 'function') return false;
  try {
    return prepared(!!(await prepare.call(native)));
  } catch (first) {
    if (!isSecureRecordsLost(first)) throw first;
    await wait(SECOND_LOOK_MS);
    return prepared(!!(await prepare.call(native)));
  }
}

/** Whether this runtime can reset its records when the person asks. */
export const canResetSecureRecords = (): boolean => typeof runtime()?.resetSecureRecords === 'function';

/**
 * The person confirmed. True when the records were reset; false when the key answered after all
 * and everything is as it was. Rejects when the device could not be asked (nothing was removed).
 */
export async function resetSecureRecords(): Promise<boolean> {
  const native = runtime();
  const reset = native?.resetSecureRecords;
  if (typeof reset !== 'function') throw Object.assign(new Error('This device cannot reset its records.'), {code: 'unsupported'});
  return !!(await reset.call(native));
}

/**
 * Starts the app again from the top, so nothing read from the old records stays in memory.
 * False when this runtime cannot do that; the person is then asked to close and reopen the app.
 */
export async function restartApp(): Promise<boolean> {
  const native = runtime();
  const restart = native?.restart;
  if (typeof restart !== 'function') return false;
  try { return !!(await restart.call(native)); } catch { return false; }
}
