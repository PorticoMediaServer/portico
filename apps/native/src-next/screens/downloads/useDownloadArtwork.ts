import {useEffect, useRef, useState} from 'react';
import type {NativeOfflineDownloadsDevice, OfflineEntry, OfflineEntryFacts} from '../../platform/offline-downloads';
import {downloadEntryIdentity} from './presentation';

/** Only stable cached artwork/media are retained here. An item bookmark is not an
 * artifact completion fact and this hook does not project it into card progress. */
export function useDownloadArtwork(device: Partial<Pick<NativeOfflineDownloadsDevice, 'displayFacts'>>, entry: OfflineEntry) {
  const identity = JSON.stringify([downloadEntryIdentity(entry), entry.artworkUrl]);
  const latestEntry = useRef(entry); latestEntry.current = entry;
  const [loaded, setLoaded] = useState<{device: object; identity: string; facts: OfflineEntryFacts}>();
  useEffect(() => {
    let current = true;
    if (!device.displayFacts) {setLoaded(undefined); return;}
    void device.displayFacts(latestEntry.current).then(facts => {
      if (current) setLoaded({device, identity, facts: {backdropUrl: facts.backdropUrl, media: facts.media}});
    }).catch(() => {if (current) setLoaded(undefined);});
    return () => {current = false;};
    // Transfer byte publications do not change a cached bundle's identity.
  }, [device, identity]);
  return loaded?.device === device && loaded.identity === identity ? loaded.facts : undefined;
}
