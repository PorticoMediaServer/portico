import {nativePublicationReadsAvailable} from '../../../platform/offline-reading';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeOfflineDownloadsDevice, OfflineEntry, OfflineGroup} from '../../platform/offline-downloads';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {downloadEntryIdentity} from './presentation';

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});

/** One borrowed document/row gesture, never a connection, player or store. */
export function createNativeOfflineEntryPlayback(context: NativeShellBootstrapContext, device: NativeOfflineDownloadsDevice, ownerCurrent: () => boolean): Readonly<{openOffline?: (entry: OfflineEntry) => Promise<void>; dispose: () => void}> {
  const start = context.playback?.startOffline;
  if (!start && !nativePublicationReadsAvailable()) return Object.freeze({dispose: () => {}});
  const lifetime = nativeDestinationLifetime(context);
  type Operation = {entry: OfflineEntry; identity: string; route: ReturnType<typeof context.navigation.getSnapshot>['route']; revision: number; abort: AbortController};
  const pending = new Set<Operation>();
  let disposed = false;
  const current = (operation: Operation) => {
    if (disposed || !ownerCurrent() || !lifetime.current() || operation.abort.signal.aborted || ((operation.entry.kind !== 'ebook' && operation.entry.kind !== 'comic') && context.playback.startOffline !== start)) return false;
    const nav = context.navigation.getSnapshot();
    if (nav.route !== operation.route || nav.revision !== operation.revision) return false;
    try {
      return device.getSnapshot().entries.some(entry => entry.id === operation.entry.id && entry.state === 'ready' && downloadEntryIdentity(entry) === operation.identity);
    } catch {return false;}
  };
  const check = () => {for (const operation of pending) if (!current(operation)) operation.abort.abort();};
  const stops = [device.subscribe(check), context.navigation.subscribe(check), context.subscribeAuthority(check)];
  return Object.freeze({
    openOffline: async (entry: OfflineEntry): Promise<void> => {
      if (disposed || !ownerCurrent() || !lifetime.current()) throw cancelled();
      const nav = context.navigation.getSnapshot();
      if (nav.route.kind !== 'downloads' && !(nav.route.kind === 'shell' && nav.route.destination.kind === 'saved')) throw cancelled();
      if (!device.getSnapshot().entries.includes(entry) || entry.state !== 'ready') throw cancelled();
      const reason = device.playable(entry);
      if (reason) throw new Error(reason);
      for (const previous of pending) previous.abort.abort();
      const operation: Operation = {entry, identity: downloadEntryIdentity(entry), route: nav.route, revision: nav.revision, abort: new AbortController()};
      pending.add(operation);
      try {
        if (!current(operation)) throw cancelled();
        if (entry.kind === 'ebook' || entry.kind === 'comic') {
          const source = device.publicationSource?.(entry); if (!source || !current(operation)) throw cancelled();
          context.navigation.open({kind: 'read', itemId: entry.itemId, sourceId: source.sourceId, downloadId: entry.id}, context.authority());
          return;
        }
        if (!start) throw cancelled();
        await start(entry, {signal: operation.abort.signal, isCurrent: () => current(operation)});
        if (!current(operation)) throw cancelled();
      } finally {pending.delete(operation);}
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const operation of pending) operation.abort.abort();
      pending.clear(); stops.forEach(stop => stop());
    },
  });
}

/** A group never implies an arbitrary episode/file choice. */
export function openNativeSavedDownload(device: NativeOfflineDownloadsDevice, group: OfflineGroup, browse: () => void, report: (error: unknown) => void): void {
  try {
    const entry = group.entries.length === 1 ? group.entries[0] : undefined;
    if (!entry || entry.state !== 'ready' || !device.openOffline || device.playable(entry)) {browse(); return;}
    void device.openOffline(entry).catch(report);
  } catch (error) {report(error);}
}
