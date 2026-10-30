import {ReadingError, type ReadingClient, type ReadingPeriodicalType, type ReadingPeriodicalGroup, type ReadingPeriodicalIssue, type ReadingYearChoice, type ReadingDayChoice, type ReadingSeriesOverview} from '@portico/client-core';
import {createWindowedCollection} from '@core/collections/windowed.ts';

export type ReadingLibraryItem = Awaited<ReturnType<ReadingClient['list']>>['items'][number];
export type ReadingContinueItem = Awaited<ReturnType<ReadingClient['continue']>>['items'][number];
const pageSize = 60;

/** Cursor breadcrumbs contain no media models. The existing sparse collection
 * alone owns resident rows (six pages); scrolling never scans the library. */
export function createReadingCollection(client: ReadingClient, libraryId: string) {
  return cursorCollection<ReadingLibraryItem>(libraryId, item => item.id, item => item.libraryId,
    (cursor, limit, signal) => client.list({libraryId, cursor, limit}, signal));
}

/** Server-issued series identity and signed cursors define membership and order.
 * Reuse the bounded collection; never sort a generic browse result or derive an id
 * from a label. No progress, numbering or page-count facts are added here. */
export function createReadingSeriesCollection(client: ReadingClient, libraryId: string, seriesId: string) {
  let overview: ReadingSeriesOverview | undefined, disposed = false;
  const listeners = new Set<() => void>(), metadata = new WeakMap<object, ReadingSeriesOverview | undefined>();
  const pages = cursorCollection<ReadingLibraryItem>(libraryId, item => item.id, item => item.libraryId,
    async (cursor, limit, signal) => {
      const page = await client.series(libraryId, seriesId, {cursor, limit}, signal);
      if (!disposed && !signal.aborted) metadata.set(page, page.overview);
      return page;
    }, page => {
      const next = metadata.get(page);
      if (overview && next && (overview.id !== next.id || overview.title !== next.title || overview.total !== next.total)) throw new ReadingError('invalid_document');
      overview = next; for (const listener of [...listeners]) listener();
    });
  return {...pages, overview: {getSnapshot: () => overview, subscribe: (listener: () => void) => {
    if (!disposed) listeners.add(listener);
    return () => {listeners.delete(listener);};
  }}, dispose() {disposed = true; overview = undefined; for (const listener of [...listeners]) listener(); listeners.clear(); pages.dispose();}};
}

export function createReadingPeriodicalGroupsCollection(client: ReadingClient, libraryId: string, kind: ReadingPeriodicalType) {
  return cursorCollection<ReadingPeriodicalGroup>(libraryId, group => group.id, group => group.latest.item.libraryId,
    async (cursor, limit, signal) => {
      const page = await client.periodicalGroups(libraryId, kind, {cursor, limit}, signal);
      return {items: page.groups, nextCursor: page.nextCursor};
    });
}
export function createReadingPeriodicalIssuesCollection(client: ReadingClient, libraryId: string, seriesId: string, kind: ReadingPeriodicalType, issueDate?: string, year?: string, onYears?: (choices: readonly ReadingYearChoice[]) => void, month?: string, onDays?: (month: string | undefined, choices: readonly ReadingDayChoice[], latestDate?: string) => void) {
  return cursorCollection<ReadingPeriodicalIssue>(libraryId, issue => issue.item.id, issue => issue.item.libraryId,
    async (cursor, limit, signal) => {
      const page = await client.periodicalIssues(libraryId, seriesId, kind, {cursor, limit, issueDate, year, month}, signal);
      if (!signal.aborted) {onYears?.(page.yearChoices); onDays?.(page.calendarMonth, page.dayChoices, cursor === undefined ? page.items[0]?.issueDate : undefined);}
      return page;
    });
}

/** The canonical recent index already selects the actual progressed version.
 * Keep its observed/CAS/stale/location facts intact; never Prepare every row. */
export function createReadingContinueCollection(client: ReadingClient, libraryId?: string) {
  return cursorCollection<ReadingContinueItem>(libraryId, row => row.item.id, row => row.item.libraryId,
    (cursor, limit, signal) => client.continue({libraryId, cursor, limit}, signal));
}

function cursorCollection<T>(libraryId: string | undefined, keyOf: (item: T) => string, libraryOf: (item: T) => string,
  fetch: (cursor: string | undefined, limit: number, signal: AbortSignal) => Promise<Readonly<{items: readonly T[]; nextCursor?: string}>>,
  accept?: (page: Readonly<{items: readonly T[]; nextCursor?: string}>) => void) {
  const cursors = new Map<number, string | undefined>([[0, undefined]]);
  const waiting = new Map<number, Set<() => void>>();
  let ended: number | undefined, frontier = 0, lowerBound = 0, disposed = false;
  const wake = () => {for (const listeners of waiting.values()) for (const listener of [...listeners]) listener();};
  const cursor = async (start: number, signal: AbortSignal) => {
    if (start > frontier + pageSize * 2) throw new ReadingError('invalid_cursor');
    if (!cursors.has(start) && (ended === undefined || start < ended)) await new Promise<void>((resolve, reject) => {
      const listeners = waiting.get(start) ?? new Set<() => void>(); waiting.set(start, listeners);
      const release = () => {listeners.delete(check); if (!listeners.size) waiting.delete(start); signal.removeEventListener('abort', check);};
      const check = () => {
        if (disposed || signal.aborted) {release(); reject(new ReadingError('cancelled'));}
        else if (cursors.has(start) || ended !== undefined && start >= ended) {release(); resolve();}
      };
      listeners.add(check); signal.addEventListener('abort', check, {once: true}); check();
    });
    if (disposed || signal.aborted) throw new ReadingError('cancelled');
    return cursors.get(start);
  };
  const collection = createWindowedCollection<T>({pageSize, maxResidentPages: 6, maxConcurrent: 2, prefetchPages: 1,
    keyOf,
    async fetchPage(start, count, signal) {
      const next = await cursor(start, signal);
      if (ended !== undefined && start >= ended) return {items: [], total: ended};
      const response = await fetch(next, count, signal);
      if (disposed || signal.aborted) throw new ReadingError('cancelled');
      if (libraryId !== undefined && response.items.some(item => libraryOf(item) !== libraryId) || response.nextCursor && (response.items.length !== count || [...cursors.values()].includes(response.nextCursor)))
        throw new ReadingError('invalid_document');
      accept?.(response);
      if (response.nextCursor) {
        cursors.set(start + count, response.nextCursor); frontier = Math.max(frontier, start + count);
        lowerBound = Math.max(lowerBound, start + response.items.length + 1);
      } else {ended = start + response.items.length;}
      wake();
      return {items: response.items, total: ended ?? lowerBound, totalIsLowerBound: ended === undefined};
    },
  });
  return Object.freeze({collection, dispose() {if (disposed) return; disposed = true; collection.dispose(); wake(); cursors.clear(); waiting.clear();}});
}
