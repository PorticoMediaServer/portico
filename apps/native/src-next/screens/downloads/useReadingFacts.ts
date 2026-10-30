import {useLayoutEffect, useState} from 'react';
import type {NativeOfflineDownloadsDevice, OfflineEntry, OfflineReadingFacts} from '../../platform/offline-downloads';

type Held = Readonly<{device: NativeOfflineDownloadsDevice; entry: OfflineEntry; facts: OfflineReadingFacts}>;

/** Read-only display facts for one downloaded publication. The device port applies the
 * same viewer, receipt and lifetime guards as opening it; a refusal simply leaves the
 * row with the facts it already had. Nothing here grants or starts a read. */
export function useReadingFacts(device: NativeOfflineDownloadsDevice, entry: OfflineEntry, allowed: boolean): OfflineReadingFacts | undefined {
  const [held, setHeld] = useState<Held>();
  const kind = entry.info?.kind ?? entry.kind, reading = kind === 'ebook' || kind === 'comic';
  useLayoutEffect(() => {
    const opening = {current: true};
    if (!reading || !allowed || entry.state !== 'ready' || !device.readingFacts) {
      setHeld(undefined);
      return () => {opening.current = false;};
    }
    void device.readingFacts(entry).then(facts => {if (opening.current) setHeld({device, entry, facts});},
      () => {if (opening.current) setHeld(undefined);});
    return () => {opening.current = false;};
  }, [allowed, device, entry, reading]);
  // A completed read may only be shown for the exact opening that asked for it. Any
  // changed device, entry or allowance keeps prior private facts off the row until a
  // new read replaces them, including the render before a passive effect would run.
  const current = held && held.device === device && held.entry === entry && reading && allowed && entry.state === 'ready';
  return current ? held.facts : undefined;
}
