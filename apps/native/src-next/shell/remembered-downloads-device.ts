import {offlineDownloads, type OfflineEntry} from '../platform/offline-downloads';
import type {NativeOfflinePlaybackDevice} from '../platform/playback/offline-playback';
import type {RememberedDownloadsViewer} from './remembered-downloads-viewer';
import {claimNativeDownloadsDevice} from './downloads-device-custody';

const cancelled = () => Object.assign(new Error('remembered_downloads_device_closed'), {name: 'AbortError', code: 'cancelled'});
/** Shell-owned, offline-only device lease. Remembered identity selects whose
 * receipts may be considered; the existing device verifies each receipt and
 * artifact before any file is returned. No API/service/connect path exists. */
export function createRememberedDownloadsDevice(identity: RememberedDownloadsViewer, ownerCurrent: () => boolean) {
  let closed = false, offIdentity = () => {};
  const lifetime = new AbortController();
  const active = () => {try {return !closed && identity.current() && ownerCurrent();} catch {return false;}};
  if (!active()) throw cancelled();
  const listeners = new Set<() => void>(), plays = new Map<string, OfflineEntry>();
  const custody = claimNativeDownloadsDevice(offlineDownloads, () => dispose());
  const current = () => active() && custody.current();
  const check = () => {if (!current()) throw cancelled();};
  const changed = () => {if (!current()) {dispose(); return;} listeners.forEach(listener => listener());};
  let offDevice = () => {};
  function dispose() {
    if (closed) return; closed = true; lifetime.abort(); plays.clear(); offIdentity(); offDevice();
    if (custody.current()) {custody.release(); offlineDownloads.disconnect(); offlineDownloads.adopt(undefined);}
    for (const listener of listeners) {try {listener();} catch { /* Retirement cannot be reversed. */ }} listeners.clear();
  }
  try {
    offIdentity = identity.subscribe(dispose); check();
    // The nominal lease contains the exact protected selected owner, not a label,
    // directory row or invented Home context. adopt itself disconnects online IO.
    offlineDownloads.disconnect(); check();
    offlineDownloads.adopt(identity.owner); check();
    offDevice = offlineDownloads.subscribe(changed);
  } catch (error) {dispose(); throw error;}
  const findEntry = (entry: OfflineEntry) => {
    check(); const matches = offlineDownloads.getSnapshot().entries.filter(value => value.id === entry.id);
    const live = matches.length === 1 ? matches[0] : undefined;
    if (!live || !entry.receipt || live.itemId !== entry.itemId || JSON.stringify(live.receipt) !== JSON.stringify(entry.receipt)
      ) return undefined;
    return live;
  };
  const entryCurrent = (entry: OfflineEntry) => {const live = findEntry(entry);
    if (!live || offlineDownloads.playabilityReason(live, identity.owner) !== null) throw cancelled(); return live;};
  const device: NativeOfflinePlaybackDevice = Object.freeze({
    getSnapshot: () => {check(); return offlineDownloads.getSnapshot();},
    subscribe: listener => {check(); listeners.add(listener); return () => {listeners.delete(listener);};},
    playabilityReason: entry => {const live = findEntry(entry); return live ? offlineDownloads.playabilityReason(live, identity.owner) : 'other_profile';},
    localMedia: async entry => {const live = entryCurrent(entry), media = await offlineDownloads.localMedia(live); entryCurrent(live);
      if (media.entryId !== live.id || media.itemId !== live.itemId || !media.playId) throw cancelled();
      plays.set(media.playId, live); while (plays.size > 16) plays.delete(plays.keys().next().value!); return media;},
    recordProgress: progress => {check(); const entry = plays.get(progress.playId);
      if (!entry || entry.id !== progress.entryId || entry.itemId !== progress.itemId) return;
      entryCurrent(entry); offlineDownloads.recordProgress(progress);},
  });
  // These ports retain the original entry and custody; no online connection is
  // available to the remembered owner. Each async result is checked again.
  const publicationCurrent = (entry: OfflineEntry) => {try {entryCurrent(entry); return true;} catch {return false;}};
  const publicationSource = (entry: OfflineEntry) => {
    const live = entryCurrent(entry), source = offlineDownloads.publicationSource(live); entryCurrent(entry); return source;
  };
  const readingFacts = async (entry: OfflineEntry) => {
    const live = entryCurrent(entry), facts = await offlineDownloads.readingFacts(live); entryCurrent(entry); return facts;
  };
  const publicationLease = (entry: OfflineEntry) => {
    const live = entryCurrent(entry);
    const lease = offlineDownloads.publicationLease(live, {signal: lifetime.signal, current: () => publicationCurrent(entry)});
    try {entryCurrent(entry); return lease;} catch (error) {lease.dispose(); throw error;}
  };
  const readingSession = async (entry: OfflineEntry, paused: () => boolean) => {
    const live = entryCurrent(entry);
    const session = await offlineDownloads.readingSession(live, {signal: lifetime.signal, current: () => publicationCurrent(entry),
      paused: () => {try {return paused() !== false;} catch {return true;}}});
    try {entryCurrent(entry); return session;} catch (error) {await session.close(); throw error;}
  };
  return Object.freeze({device, current, publicationSource, readingFacts, publicationLease, readingSession, open: async () => {check(); await offlineDownloads.open(); check();}, dispose});
}
