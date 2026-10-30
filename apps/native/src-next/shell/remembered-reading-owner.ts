import type {PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import type {OfflineEntry} from '../platform/offline-downloads';
import {closeReadingResource, createReadingSessionOwner} from '../screens/read/reading-session-owner';
import {rememberedReadingHistoryPaused} from './remembered-reading-settings';
import type {createRememberedDownloadsDevice} from './remembered-downloads-device';

type Device = ReturnType<typeof createRememberedDownloadsDevice>;
const cancelled = () => Object.assign(new Error('remembered_reading_retired'), {name: 'AbortError', code: 'cancelled'});

/** A private publication occurrence bound to the root's original device custody,
 * exact retained entry, signed source and confirmed protected preferences. */
export function createRememberedReadingOwner(device: Device, entry: OfflineEntry, snapshot: PreferenceSnapshot, rootCurrent: () => boolean) {
  const source = device.publicationSource(entry);
  const current = () => {try {
    if (!rootCurrent() || !device.current() || device.device.playabilityReason(entry) !== null) return false;
    const live = device.publicationSource(entry);
    return live.sourceId === source.sourceId && live.revision === source.revision && rootCurrent() && device.current();
  } catch {return false;}};
  if (!current()) throw cancelled();
  const owner = createReadingSessionOwner({current, subscribe: listener => device.device.subscribe(listener), acquire: async (_signal, ownerCurrent) => {
    if (!ownerCurrent()) throw cancelled();
    const session = await device.readingSession(entry, () => rememberedReadingHistoryPaused(snapshot));
    const resource = {session, release: () => {}};
    try {
      if (!ownerCurrent() || session.manifest.itemId !== entry.itemId || session.manifest.sourceId !== source.sourceId
        || session.manifest.revision !== source.revision || !ownerCurrent()) throw cancelled();
      return resource;
    } catch (error) {try {await closeReadingResource(resource);} catch { /* Preserve the original refusal. */ } throw error;}
  }});
  return Object.freeze({owner, preferences: Object.freeze({getSnapshot: () => {if (!current()) throw cancelled(); return snapshot;},
    subscribe: (listener: () => void) => device.device.subscribe(listener)})});
}
