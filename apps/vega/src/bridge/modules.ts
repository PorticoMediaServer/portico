import DeviceInfo from '@amazon-devices/react-native-device-info';
import {AppState, Platform} from '@amazon-devices/react-native-kepler';
import {probeVegaPlayer} from './capability-probe';
import {boundedVegaProbe} from './capability-output';
import type {CapabilityDocument} from '../../../../packages/client-core/src/client-profile';
import {base64Url} from './encoding';
import {ModuleEvents, failure, withEvents} from './events';
import {randomBytes, uuid} from './random';
import {readRecord, serialized, writeRecord} from './storage';

const appVersion: string = String((require('../../package.json') as {version?: string}).version ?? '');

let named: Promise<string> | undefined;
/** A name that only says what kind of device this is: no better than no name at all. */
const genericName = /^(unknown|fire ?tv|amazon fire ?tv|fire ?tv stick|vega)$/i;

/**
 * The name the owner gave this device ("Living Room"), for sign-in approvals; empty when the
 * system does not say or only says what kind of device it is, and the app then says that itself
 * (`deviceModelName`). Sign-in waits for the device's identity, so the question is given a moment
 * and no longer.
 */
function deviceName(): Promise<string> {
  named ??= new Promise<string>(resolve => {
    const settle = (value: unknown) => resolve(typeof value === 'string' && !genericName.test(value.trim()) ? value.trim() : '');
    const timer = setTimeout(() => settle(''), 1500);
    try {
      Promise.resolve(DeviceInfo.getDeviceName()).then(settle, () => settle('')).finally(() => clearTimeout(timer));
    } catch {
      clearTimeout(timer);
      settle('');
    }
  });
  return named;
}

/**
 * `NativeModules.PorticoCapabilities`: what only the system knows about this device. Event:
 * `portico.capabilities`, sent when the answer may have changed.
 */
export const capabilityEvents = new ModuleEvents();
let playerProfile: CapabilityDocument | null = null;
let probing: Promise<CapabilityDocument | null> | undefined;
let capabilityGeneration = 0;
/** A late device answer republishes capabilities; unknown never becomes an invented fact. */
function capabilityProfile(refresh = false): Promise<CapabilityDocument | null> {
  if (refresh) {++capabilityGeneration; probing = undefined; playerProfile = null;}
  const generation = capabilityGeneration;
  probing ??= probeVegaPlayer(refresh).then(profile => {
    if (generation !== capabilityGeneration) return null;
    playerProfile = profile;
    if (profile) capabilityEvents.emit('portico.capabilities');
    return profile;
  });
  return boundedVegaProbe(probing, 2000).then(profile => generation === capabilityGeneration ? profile ?? playerProfile : null);
}
// Output facts must be asked again after the television/receiver could have changed.
AppState.addEventListener('change', state => {
  if (state !== 'active') return;
  void capabilityProfile(true);
  capabilityEvents.emit('portico.capabilities');
});
export const PorticoCapabilities = withEvents({
  async facts(): Promise<{model: string; osVersion: string; deviceName: string; appVersion: string; vegaProfile: CapabilityDocument | null}> {
    const constants = Platform.constants as {Model?: string; Release?: string};
    return {model: constants.Model ?? '', osVersion: constants.Release ?? String(Platform.Version ?? ''), deviceName: await deviceName(), appVersion, vegaProfile: await capabilityProfile()};
  },
}, capabilityEvents);

/** `NativeModules.PorticoSiriRemote`: no touch surface on this remote, so no events; left and
 * right seek through the shared timeline, as on an Apple TV remote without one. */
export const PorticoSiriRemote = withEvents({setEnabled(_enabled: boolean): void {}}, new ModuleEvents());

/** `NativeModules.PorticoActivity` (Handoff, Spotlight, Siri on iPhone): nothing to do here. */
export const PorticoActivity = {setCurrent(_title: unknown): void {}, indexTitles(_titles: unknown): void {}, clear(): void {}};

/** `NativeModules.PorticoCast`: a television never casts. */
export const PorticoCast = withEvents({
  available: (): Promise<boolean> => Promise.resolve(false),
  current: (): Promise<null> => Promise.resolve(null),
  choose(): void {},
  send: (_json: string): Promise<boolean> => Promise.reject(failure('cast_unavailable', 'Cast is unavailable.')),
  end(_stopReceiver: boolean): void {},
}, new ModuleEvents());

/** `NativeModules.PorticoDownloads`: downloads are for handheld devices. The shared code offers
 * them nowhere on a television (`offline-downloads.ts`); every method still answers. */
const noDownloads = () => Promise.reject(failure('downloads_unavailable', 'Downloads are unavailable on this device.'));
export const PorticoDownloads = withEvents({
  list: (): Promise<unknown[]> => Promise.resolve([]),
  start: noDownloads,
  pause: (_id: string): Promise<void> => Promise.resolve(),
  remove: (_id: string): Promise<void> => Promise.resolve(),
  setMeta: (_id: string, _meta: string): Promise<void> => Promise.resolve(),
  setMetas: (_metas: unknown): Promise<void> => Promise.resolve(),
  writeExtra: noDownloads,
  readExtra: (..._args: unknown[]): Promise<null> => Promise.resolve(null),
  fetchExtra: noDownloads,
  storage: (): Promise<{freeBytes: number; usedBytes: number}> => Promise.resolve({freeBytes: 0, usedBytes: 0}),
  setCellular: (_allowed: boolean): Promise<void> => Promise.resolve(),
  readState: (): Promise<string> => Promise.resolve(''),
  writeState: (_json: string): Promise<void> => Promise.resolve(),
  localFile: (_id: string): Promise<null> => Promise.resolve(null),
  play: noDownloads,
  stop: (): Promise<void> => Promise.resolve(),
}, new ModuleEvents());

export const PorticoImagePicker = {pick: (): Promise<null> => Promise.resolve(null)};
export const PorticoSubtitleFiles = {pick: (): Promise<null> => Promise.resolve(null)};

/**
 * `NativeModules.PorticoTopShelf`: the token and address the home-screen rows read their feed
 * with. The record is kept so the app renews the token only when it is due; publishing rows to
 * the Vega home screen from it is the home-screen integration's work.
 */
type Shelf = {baseUrl: string; token: string; expiresAt: string; owner: string};
const SHELF = 'configuration';
export const topShelfEvents = new ModuleEvents();
export const PorticoTopShelf = withEvents({
  configure(baseUrl: string, token: string, expiresAt: string, owner: string): Promise<void> {
    return serialized('top-shelf', async () => {
      const shelf: Shelf = {baseUrl, token, expiresAt, owner};
      await writeRecord('top-shelf', SHELF, JSON.stringify(shelf));
      topShelfEvents.emit('changed');
    });
  },
  expiresAt(): Promise<null | {baseUrl: string; expiresAt: string; owner?: string}> {
    return serialized('top-shelf', async () => {
      try {
        const shelf = JSON.parse((await readRecord('top-shelf', SHELF)) ?? 'null') as Shelf | null;
        return shelf && typeof shelf.baseUrl === 'string' && typeof shelf.expiresAt === 'string' ? {baseUrl: shelf.baseUrl, expiresAt: shelf.expiresAt, owner: shelf.owner} : null;
      } catch {
        return null;
      }
    });
  },
  clear(): Promise<void> {
    return serialized('top-shelf', async () => {
      await writeRecord('top-shelf', SHELF, null);
      topShelfEvents.emit('changed');
    });
  },
  changed(): void { topShelfEvents.emit('changed'); },
}, topShelfEvents);

/** The home-screen integration reads the token through this, never through the JS-facing module. */
export async function topShelfConfiguration(): Promise<Shelf | null> {
  try { return JSON.parse((await readRecord('top-shelf', SHELF)) ?? 'null') as Shelf | null; } catch { return null; }
}

/**
 * `NativeModules.PorticoAccountAuth`: a television signs in by code, so there is no browser and
 * no provider here. The continuation record and binding exist because the shared account code
 * keeps one per account service on every device.
 */
const originPattern = /^https?:\/\/[^\s/?#]+$/i;
export const PorticoAccountAuth = {
  bundleId: 'tv.getportico.portico.vega',
  canSignInApple: false,
  canOpenBrowser: false,
  newBinding(): Promise<string> {
    try { return Promise.resolve(base64Url(randomBytes(32))); } catch { return Promise.reject(failure('random', 'Secure randomness is unavailable.')); }
  },
  newRequestId: (): Promise<string> => Promise.resolve(uuid()),
  async loadContinuation(origin: string): Promise<string | null> {
    if (typeof origin !== 'string' || !originPattern.test(origin)) throw failure('origin', 'Invalid account origin.');
    try { return await readRecord('account-continuation', origin); } catch { throw failure('secure_storage', 'The sign-in in progress could not be read.'); }
  },
  async saveContinuation(origin: string, value: string): Promise<void> {
    if (typeof origin !== 'string' || !originPattern.test(origin) || typeof value !== 'string' || !value.length || value.length > 8192) throw failure('continuation', 'Invalid account continuation.');
    try { await writeRecord('account-continuation', origin, value); } catch { throw failure('secure_storage', 'The sign-in in progress could not be saved.'); }
  },
  openBrowser: (_url: string, _transactionId: string): Promise<string> => Promise.reject(failure('browser_unavailable', 'This device signs in with a code.')),
  signInApple: (_nonce: string): Promise<never> => Promise.reject(failure('apple_unavailable', 'This device signs in with a code.')),
  cancel: (): Promise<void> => Promise.resolve(),
};
