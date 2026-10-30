import {savedEntryKey} from '@core/saved.ts';
import type {WindowedCollection, CollectionSnapshot} from '@core/collections/windowed.ts';
import type {SavedEntry, SavedService, SavedSnapshot} from '@core/saved.ts';
import type {PersonalResourceEntry, PersonalSavedService, PersonalSavedSnapshot} from '@core/personal-saved.ts';

/** Presentation of the service's actual bounded page. No offset or appended
 * membership is invented for an opaque server cursor. */
export function savedSectionWindow(service: SavedService, sectionId: string): WindowedCollection<SavedEntry> {
  let prior: SavedSnapshot | undefined, version = 0, snapshot: CollectionSnapshot;
  let disposed = false;
  const subscriptions = new Set<() => void>();
  const section = () => service.getSnapshot().projection?.sections.find(value => value.id === sectionId);
  const getSnapshot = (): CollectionSnapshot => {
    const current = service.getSnapshot();
    if (current !== prior) {
      prior = current; version++;
      const shown = section();
      snapshot = Object.freeze({generation: current.generation, total: shown ? shown.entries.length : current.phase === 'ready' ? 0 : undefined,
        totalIsLowerBound: !!shown?.nextCursor, status: current.phase === 'error' || current.phase === 'refresh-required' ? 'error' : current.phase === 'idle' ? 'idle' : shown ? 'ready' : 'loading',
        error: current.error, positionIndex: undefined, refreshing: current.phase === 'loading' && !!shown});
    }
    return snapshot;
  };
  const subscribe = (listener: () => void) => {
    if (disposed) return () => {};
    const off = service.subscribe(listener); subscriptions.add(off);
    return () => { off(); subscriptions.delete(off); };
  };
  return {
    pageSize: 40, getSnapshot, subscribe,
    subscribePage: (_page, listener) => subscribe(listener),
    getPageVersion: () => { getSnapshot(); return String(version); },
    pageOf: index => Math.floor(index / 40),
    itemAt: index => section()?.entries[index],
    keyAt: index => {const entry = section()?.entries[index]; return entry ? savedEntryKey(entry) : `placeholder:${index}`;},
    slotAt: index => {
      const item = section()?.entries[index];
      return item ? {kind: 'item', index, key: savedEntryKey(item), item} : {kind: 'placeholder', index, key: `placeholder:${index}`};
    },
    ensureRange: () => {},
    // Nothing loaded is ever evicted here, so a pinned page is already held.
    pin: () => {},
    indexOfLetter: () => undefined,
    invalidate: () => { if (!disposed) void service.refresh(); },
    revalidate: options => disposed ? Promise.resolve() : service.revalidate({settle: options?.hold}),
    retry: () => { if (!disposed) void service.retry(); },
    resident: () => ({pages: Math.ceil((section()?.entries.length ?? 0) / 40), items: section()?.entries.length ?? 0}),
    residentItems: () => section()?.entries ?? [],
    dispose: () => { disposed = true; for (const off of subscriptions) off(); subscriptions.clear(); },
  };
}

/** The service exposes its current bounded page, rather than an appended
 * collection. Preserve that contract until its canonical window seam lands. */
export function personalEntriesWindow(service: PersonalSavedService): WindowedCollection<PersonalResourceEntry> {
  let prior: PersonalSavedSnapshot | undefined, version = 0, generation = 0, snapshot: CollectionSnapshot;
  let disposed = false;
  const subscriptions = new Set<() => void>();
  const getSnapshot = (): CollectionSnapshot => {
    const current = service.getSnapshot();
    if (current !== prior) {
      if (prior?.route !== current.route) generation++;
      prior = current; version++;
      snapshot = Object.freeze({generation, total: current.loading && !current.entries.length ? undefined : current.entries.length,
        totalIsLowerBound: !!current.nextCursor, status: current.error ? 'error' : current.loading && !current.entries.length ? 'loading' : 'ready',
        error: current.error, positionIndex: undefined, refreshing: current.loading && !!current.entries.length});
    }
    return snapshot;
  };
  const subscribe = (listener: () => void) => {
    if (disposed) return () => {};
    const off = service.subscribe(listener); subscriptions.add(off);
    return () => { off(); subscriptions.delete(off); };
  };
  return {
    pageSize: 40, getSnapshot, subscribe, subscribePage: (_page, listener) => subscribe(listener),
    getPageVersion: () => { getSnapshot(); return String(version); }, pageOf: index => Math.floor(index / 40),
    itemAt: index => service.getSnapshot().entries[index], keyAt: index => service.getSnapshot().entries[index]?.id ?? `placeholder:${index}`,
    slotAt: index => { const item = service.getSnapshot().entries[index]; return item ? {kind: 'item', index, key: item.id, item} : {kind: 'placeholder', index, key: `placeholder:${index}`}; },
    // Advancing the service here would discard the page under the pointer.
    // The screen retains its explicit, existing continuation action.
    ensureRange: () => {},
    // Nothing loaded is ever evicted here, so a pinned page is already held.
    pin: () => {},
    indexOfLetter: () => undefined,
    invalidate: () => { if (!disposed) void service.refresh(); },
    revalidate: options => disposed ? Promise.resolve() : service.revalidate({settle: options?.hold}),
    retry: () => { if (!disposed) void service.retryRead(); },
    resident: () => ({pages: Math.ceil(service.getSnapshot().entries.length / 40), items: service.getSnapshot().entries.length}),
    residentItems: () => service.getSnapshot().entries,
    dispose: () => { disposed = true; for (const off of subscriptions) off(); subscriptions.clear(); },
  };
}
