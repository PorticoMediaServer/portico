import {Platform} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {captureRememberedDownloadsViewer} from './remembered-downloads-viewer';
import {createRememberedDownloadsDevice} from './remembered-downloads-device';
import {readRememberedPlaybackSettings} from './remembered-playback-settings';
import {readRememberedReadingPreferences} from './remembered-reading-settings';
import {createRememberedReadingOwner} from './remembered-reading-owner';
import type {OfflineEntry} from '../platform/offline-downloads';
import {createNativeReceiptOnlyPlayback} from '../platform/playback/receipt-only';
import {noAccountDownloads, type AccountDownloadsEntry, type AccountDownloadsSnapshot} from '../screens/accounts/downloads-entry-model';

export type RememberedDownloadsOpening = Readonly<{entry: AccountDownloadsEntry; device: ReturnType<typeof createRememberedDownloadsDevice>['device'];
  player?: ReturnType<typeof createNativeReceiptOnlyPlayback>; reading?: (entry: OfflineEntry) => ReturnType<typeof createRememberedReadingOwner>; canRead?: (entry: OfflineEntry) => boolean; noticeCopy?: Readonly<{title: string; body: string}>; current: () => boolean; dispose: () => void}>;
type InstallationOwner = Parameters<typeof captureRememberedDownloadsViewer>[0];
const available: AccountDownloadsSnapshot = Object.freeze({available: true});
const cancelled = () => Object.assign(new Error('remembered_downloads_opening_closed'), {name: 'AbortError', code: 'cancelled'});

/** One root-owned offline opening. Construction never restores/adopts a server
 * session. Only the selected remembered owner, actual retained settings and
 * genuine signed device receipts can make the account shortcut available. */
export async function createRememberedDownloadsOpening(installation: InstallationOwner, options: Readonly<{
  signal: AbortSignal; isCurrent: () => boolean; onOpen: (source: RememberedDownloadsOpening) => void; onRetired: () => void;
}>): Promise<RememberedDownloadsOpening | undefined> {
  if (Platform.isTV) return undefined;
  let closed = false;
  let identity: Awaited<ReturnType<typeof captureRememberedDownloadsViewer>>;
  let device: ReturnType<typeof createRememberedDownloadsDevice> | undefined;
  let player: ReturnType<typeof createNativeReceiptOnlyPlayback> | undefined;
  let offIdentity = () => {}, offDevice = () => {};
  let reading: RememberedDownloadsOpening['reading'];
  const noticeCopy = Object.freeze({title: defaultI18n.t('next.reading.savedOnlySettingsTitle'),
    body: defaultI18n.t('next.reading.savedOnlySettingsBody')});
  const canRead = (entry: OfflineEntry) => {try {if (!current() || !device || !reading || !noticeCopy || device.device.playabilityReason(entry) !== null) return false;
    device.publicationSource(entry); return current();} catch {return false;}};
  const listeners = new Set<() => void>();
  const ownerCurrent = () => {try {return !closed && !options.signal.aborted && options.isCurrent() && installation.current() && !!identity?.current();} catch {return false;}};
  const current = () => ownerCurrent() && (!device || device.current());
  const check = () => {if (!current()) throw cancelled();};
  const dispose = () => {
    if (closed) return; closed = true; listeners.clear(); options.signal.removeEventListener('abort', dispose); offIdentity(); offDevice();
    for (const release of [() => player?.dispose(), () => device?.dispose(), () => identity?.dispose(), options.onRetired]) {
      try {release();} catch { /* The opening is already retired; finish all original cleanup. */ }
    }
  };
  const usable = () => {try {if (!current() || !device) return false;
    const snapshot = device.device.getSnapshot(); return snapshot.supported && snapshot.ready
      && snapshot.entries.some(entry => {const kind = entry.kind ?? entry.info?.kind ?? '';
        return entry.state === 'ready' && ((!!player?.current() && ['movie', 'episode', 'song', 'audiobook_file'].includes(kind))
          || (['ebook', 'comic'].includes(kind) && canRead(entry))) && device!.device.playabilityReason(entry) === null;}) && current();
  } catch {return false;}};
  const changed = () => {if (!current()) {dispose(); return;} for (const listener of listeners) {try {listener();} catch { /* One observer cannot prevent another retirement notification. */ }}};
  try {
    if (options.signal.aborted || !options.isCurrent() || !installation.current()) throw cancelled();
    options.signal.addEventListener('abort', dispose, {once: true});
    identity = await captureRememberedDownloadsViewer(installation, options);
    if (!identity) {dispose(); return undefined;} check();
    offIdentity = identity.subscribe(dispose); check();
    const settings = await readRememberedPlaybackSettings(identity, current).catch(() => undefined); check();
    const readingSettings = await readRememberedReadingPreferences(identity, current, 'mobile').catch(() => undefined); check();
    if (!settings && !readingSettings) {dispose(); return undefined;}
    device = createRememberedDownloadsDevice(identity, ownerCurrent); check();
    // Settings are the exact protected snapshot for this opening, not guessed
    // defaults. The root disposes this opening before any live shell replaces it.
    if (settings) player = createNativeReceiptOnlyPlayback({identity, device: device.device, isCurrent: current,
      settings: () => {check(); return settings;}, subscribeSettings: listener => {check(); listeners.add(listener); return () => {listeners.delete(listener);};}});
    if (readingSettings) reading = entry => {check(); return createRememberedReadingOwner(device!, entry, readingSettings, current);};
    check(); offDevice = device.device.subscribe(changed); await device.open(); check();
    const entry: AccountDownloadsEntry = Object.freeze({getSnapshot: () => usable() ? available : noAccountDownloads,
      subscribe: listener => {if (closed) return () => {}; listeners.add(listener); return () => {listeners.delete(listener);};}, current,
      open: () => {if (usable()) options.onOpen(source);},
      // The root owns this lease; account-route borrowers cannot retire it.
      dispose: () => {},
    });
    const source = Object.freeze({entry, device: device.device, ...(player ? {player} : {}), ...(reading ? {reading, canRead, noticeCopy} : {}), current, dispose});
    return source;
  } catch (error) {dispose(); throw error;}
}
