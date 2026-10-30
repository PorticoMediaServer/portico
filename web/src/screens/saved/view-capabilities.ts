import {browseCapabilitiesPath, parseBrowseCapabilities, type BrowseCapabilities} from '@core/browse.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {SavedDefinition} from '@core/personal-saved.ts';

export type ReadSavedViewCapabilities = (definition: SavedDefinition, signal: AbortSignal) => Promise<BrowseCapabilities>;
const retired = () => new DOMException('Saved view reader retired', 'AbortError');
type Read = {controller: AbortController; promise: Promise<BrowseCapabilities>; users: number; settled: boolean};

/** Captured authority only. Coalesce concurrent reads, not a permanent cache:
 * a later directory refresh must observe changed library capabilities. */
export function createSavedViewCapabilitiesReader(api: LibraryContentApi, isCurrent: () => boolean) {
  let disposed = false;
  const reads = new Map<string, Read>();
  let running = 0;
  const waiting: {start(): void; cancel(): void}[] = [];
  const current = () => !disposed && isCurrent();
  const release = () => {running--;waiting.shift()?.start();};
  const acquire = (signal: AbortSignal): Promise<() => void> => {
    if (!current() || signal.aborted) return Promise.reject(retired());
    if (running < 4) {running++;return Promise.resolve(release);}
    return new Promise((resolve, reject) => {
      const entry = {
        start() {signal.removeEventListener('abort', entry.cancel);if (!current() || signal.aborted) {reject(retired());waiting.shift()?.start();return;}running++;resolve(release);},
        cancel() {const index = waiting.indexOf(entry);if (index >= 0) waiting.splice(index, 1);signal.removeEventListener('abort', entry.cancel);reject(retired());},
      };
      waiting.push(entry);signal.addEventListener('abort', entry.cancel, {once: true});
    });
  };
  const read: ReadSavedViewCapabilities = (definition, signal) => {
    if (!current() || signal.aborted) return Promise.reject(retired());
    const libraryId = definition.libraryId, pivot = definition.pivot, key = JSON.stringify([libraryId, pivot]);
    let job = reads.get(key);
    if (!job || job.controller.signal.aborted) {
      const controller = new AbortController();
      const promise = Promise.resolve().then(async () => {
        const leave = await acquire(controller.signal);
        try {
          if (!current() || controller.signal.aborted) throw retired();
          const raw = await api.request(browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, controller.signal);
          if (!current() || controller.signal.aborted) throw retired();
          const value = parseBrowseCapabilities(raw);
          if (value.library.id !== libraryId || value.resolvedPivot && value.resolvedPivot.id !== pivot || !value.pivots.some(value => value.id === pivot)) throw Object.assign(new Error('invalid_browse'), {code: 'invalid_browse'});
          return value;
        } finally {leave();}
      });
      const created = {controller, promise, users: 0, settled: false};job = created;reads.set(key, created);
      const finished = () => {created.settled = true;if (reads.get(key) === created) reads.delete(key);};
      void promise.then(finished, finished);
    }
    const active = job;active.users++;
    return new Promise((resolve, reject) => {
      let finished = false;
      const settle = (value?: BrowseCapabilities, error?: unknown) => {
        if (finished) return;finished = true;signal.removeEventListener('abort', stop);active.controller.signal.removeEventListener('abort', stop);
        active.users--;if (!active.users && !active.settled) active.controller.abort();
        if (!current() || signal.aborted) reject(retired());else if (error !== undefined) reject(error);else if (value) resolve(value);else reject(retired());
      };
      const stop = () => settle(undefined, retired());
      signal.addEventListener('abort', stop, {once: true});
      active.controller.signal.addEventListener('abort', stop, {once: true});
      active.promise.then(value => settle(value), error => settle(undefined, error));
    });
  };
  return {read, dispose() {if (disposed) return;disposed = true;for (const job of reads.values()) job.controller.abort();reads.clear();}};
}
