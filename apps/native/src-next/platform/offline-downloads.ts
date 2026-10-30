import {offlineDownloads, offlineOwnerKey, type OfflineDownloads, type OfflineEntry, type OfflineOthersTarget, type OfflineSettings} from '../../platform/offline-downloads';
import {serviceText} from '../../../../packages/client-core/src/presentation/service-text';

export {groupOfflineEntries} from '../../platform/offline-downloads';
export type {OfflineEntry, OfflineSettings, OfflineSnapshot, OfflineGroup, OfflinePlayabilityReason, OfflineEntryFacts, OfflineReadingFacts, OfflineOthersTarget, OfflinePendingDownload} from '../../platform/offline-downloads';

/** The shell alone binds the current viewer/server through adopt/connect/disconnect. */
export {offlineDownloads};
export type {OfflineDownloads};

type DeviceDownloads = Pick<OfflineDownloads, 'getSnapshot' | 'subscribe' | 'open' | 'setSettings' | 'pause' | 'resume' | 'remove' | 'removeOthersConfirmed' | 'playable' | 'playabilityReason' | 'displayFacts' | 'note' | 'localMedia'> & Partial<Pick<OfflineDownloads, 'publicationSource' | 'readingFacts' | 'admission' | 'cancelPending'>>;
export type NativeOfflineDownloadsAdapter = Readonly<{
  admission?: OfflineDownloads['admission'];
  cancelPending?: OfflineDownloads['cancelPending'];
  publicationSource?: OfflineDownloads['publicationSource'];
  readingFacts?: OfflineDownloads['readingFacts'];
  getSnapshot: DeviceDownloads['getSnapshot'];
  subscribe: DeviceDownloads['subscribe'];
  open: DeviceDownloads['open'];
  setSettings: (settings: Partial<OfflineSettings>) => Promise<void>;
  pause: (entry: OfflineEntry) => Promise<void>;
  resume: (entry: OfflineEntry) => Promise<void>;
  remove: (entry: OfflineEntry) => Promise<void>;
  /** Exactly the other-viewer set a confirm drew; a changed target refuses. */
  removeOthers: (expected: OfflineOthersTarget) => Promise<void>;
  playable: (entry: OfflineEntry) => string;
  playabilityReason: (entry:OfflineEntry) => ReturnType<DeviceDownloads['playabilityReason']>;
  displayFacts: DeviceDownloads['displayFacts'];
  note: (entry: OfflineEntry) => string;
  localMedia: DeviceDownloads['localMedia'];
  openOffline: (entry: OfflineEntry) => Promise<void>;
}>;
/** Local storage remains usable before the same-engine offline player is installed. */
export type NativeOfflineDownloadsDevice = Omit<NativeOfflineDownloadsAdapter, 'openOffline'> & Partial<Pick<NativeOfflineDownloadsAdapter, 'openOffline'>>;

/** No server list and no second native module owner. Resolve every action against the
 * currently published viewer list, so an old screen cannot act on another viewer's file. */
export function createNativeOfflineDownloadsAdapter(openOffline: (entry: OfflineEntry) => Promise<void>, device?: DeviceDownloads): NativeOfflineDownloadsAdapter;
export function createNativeOfflineDownloadsAdapter(openOffline?: undefined, device?: DeviceDownloads): NativeOfflineDownloadsDevice;
export function createNativeOfflineDownloadsAdapter(
  openOffline?: (entry: OfflineEntry) => Promise<void>,
  device: DeviceDownloads = offlineDownloads,
): NativeOfflineDownloadsDevice {
  const current = (entry: OfflineEntry): OfflineEntry => {
    const live = device.getSnapshot().entries.find(value => value.id === entry.id);
    const owner = (value: OfflineEntry) => value.receipt ? offlineOwnerKey(value.receipt.claims.viewer) : value.owner;
    if (!live || live.itemId !== entry.itemId || owner(live) !== owner(entry)) throw new Error(serviceText('downloads.offline.otherProfile'));
    return live;
  };
  return Object.freeze({
    ...(device.admission ? {admission: (bytes: number) => device.admission!(bytes)} : {}),
    ...(device.cancelPending ? {cancelPending: (id: string) => device.cancelPending!(id)} : {}),
    ...(device.publicationSource ? {publicationSource: (entry: OfflineEntry) => device.publicationSource!(entry)} : {}),
    ...(device.readingFacts ? {readingFacts: async (entry: OfflineEntry) => {
      const facts = await device.readingFacts!(current(entry)); current(entry); return facts;
    }} : {}),
    getSnapshot: device.getSnapshot,
    subscribe: device.subscribe,
    open: () => device.open(),
    setSettings: settings => device.setSettings(settings),
    pause: entry => device.pause(current(entry)),
    resume: entry => device.resume(current(entry)),
    remove: entry => device.remove(current(entry)),
    removeOthers: expected => device.removeOthersConfirmed(expected),
    playable: entry => device.playable(current(entry)),
    playabilityReason: entry => device.playabilityReason(current(entry)),
    displayFacts: async entry => {const facts=await device.displayFacts(current(entry));current(entry);return facts;},
    note: entry => device.note(current(entry)),
    localMedia: async entry => {
      const media=await device.localMedia(current(entry));
      const reason=device.playable(current(entry));if(reason)throw new Error(reason);
      return media;
    },
    ...(openOffline ? {openOffline: async (entry: OfflineEntry) => {
      const live = current(entry), reason = device.playable(live);
      if (reason) throw new Error(reason);
      await openOffline(live);
    }} : {}),
  });
}
