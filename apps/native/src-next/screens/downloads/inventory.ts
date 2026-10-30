/** Snapshot-wide indexing is done once per publication, while native rows subscribe to
 * their own identity. Grouping depends on membership/group facts, never transfer bytes. */
export function createDownloadInventory<T, M>(source: {getSnapshot: () => Readonly<{entries: readonly T[]}>; subscribe: (listener: () => void) => () => void}, options: {
  identity: (entry: T) => string;
  membershipKey: (entry: T) => unknown;
  project: (entries: readonly T[]) => M;
  /** Receipt/playability fences can change without changing an entry's fields. */
  entryFence?: (entry: T) => unknown;
}) {
  let published: Readonly<{entries: readonly T[]}> | undefined, membership: M | undefined, membershipToken: string | undefined;
  let entries = new Map<string, T>();
  const selected = new Map<string, {value: Readonly<{entry: T}>; token: string}>();
  const read = () => {
    const next = source.getSnapshot(); if (next === published) return;
    published = next; entries = new Map(next.entries.map(entry => [options.identity(entry), entry]));
    const token = JSON.stringify(next.entries.map(entry => [options.identity(entry), options.membershipKey(entry)]));
    if (token !== membershipToken) {membershipToken = token; membership = options.project(next.entries);}
    for (const id of selected.keys()) if (!entries.has(id)) selected.delete(id);
  };
  const getEntry = (id: string): Readonly<{entry: T}> | undefined => {
    read(); const entry = entries.get(id); if (!entry) return;
    // Only mounted rows ask for a full entry comparison. A bridge that recreates all
    // objects on each progress tick must not redraw all those otherwise identical rows.
    const token = JSON.stringify([entry, options.entryFence?.(entry)]), previous = selected.get(id);
    if (previous?.token === token) return previous.value;
    const value = Object.freeze({entry}); selected.set(id, {value, token}); return value;
  };
  return {
    subscribe: source.subscribe,
    getMembership: (): M => {read(); return membership!;},
    getEntry,
    release: (id: string) => {selected.delete(id);},
  };
}

export type DownloadViewport = Readonly<{offset: number; height: number}>;
export function createDownloadViewport() {
  let snapshot: DownloadViewport = Object.freeze({offset: 0, height: 0});
  const listeners = new Set<() => void>();
  return {
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    update: (next: DownloadViewport) => {
      if (![next.offset, next.height].every(Number.isFinite) || next.height < 0 || next.offset === snapshot.offset && next.height === snapshot.height) return;
      snapshot = Object.freeze({...next}); listeners.forEach(listener => listener());
    },
  };
}

/** Measured rows retain their real height; unopened rows use only an estimate until
 * mounted, just as a native virtual list does. The binary search bounds native hosts. */
export function downloadWindow(offsets: readonly number[], offset: number, viewport: number): Readonly<{start: number; end: number}> {
  const count = Math.max(0, offsets.length - 1);
  if (!count) return {start: 0, end: 0};
  const height = Number.isFinite(viewport) && viewport > 0 ? viewport : 800;
  const top = Number.isFinite(offset) ? offset : 0, first = Math.max(0, top - height), last = Math.max(height, top + height * 2);
  const lower = (value: number) => {
    let low = 0, high = offsets.length;
    while (low < high) {const mid = (low + high) >>> 1; if (offsets[mid]! < value) low = mid + 1; else high = mid;}
    return low;
  };
  return {start: Math.max(0, Math.min(count - 1, lower(first) - 1)), end: Math.max(1, Math.min(count, lower(last)))};
}
