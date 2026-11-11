import {BrowseQueryBuilder, type BrowseCapabilities, type BrowseNode, type BrowseResult, type BrowseSortSelection, type BrowseWholeSelection, type BrowseWholeSelectionUnavailable} from './browse.ts';
import type {FetchPage} from './collections/windowed.ts';
import type {ContentEntry} from './library-content.ts';
import {countItems, entriesFromItems, readFacet, readItems} from './items.ts';
import {libraryItemQuery} from './library-browse.ts';
import {linkAbortSignals} from './abort-lifetime.ts';
import {isStaleAnswer} from './freshness-actions.ts';

/** One applied Browse query: what every page, count, facet read and saved view is built from.
 * `nodes` holds the fixed source filter followed by the viewer's own predicates. */
export type BrowseSessionQuery = Readonly<{
  libraryId: string;
  pivot: string;
  capabilities: BrowseCapabilities;
  nodes: readonly BrowseNode[];
  sorts: readonly BrowseSortSelection[];
  homeRowId?: string;
}>;

/** The canonical builder for a query; callers add the limit and range they need. */
export function browseSessionBuilder(query: BrowseSessionQuery): BrowseQueryBuilder {
  const builder = new BrowseQueryBuilder(query.capabilities, query.pivot);
  for (const node of query.nodes) builder.add(node);
  for (const sort of query.sorts) builder.sort(sort.field, sort.direction, sort.seed);
  if (query.homeRowId) builder.displayHomeRow(query.homeRowId);
  return builder;
}

/** A browse page is one library's scope; entity entries (show, album, artist) can omit their
 * library even though every destination needs it. Fill only what the server left absent. */
export function withLibraryScope(page: BrowseResult, libraryId: string): BrowseResult {
  if (!libraryId) return page;
  let changed = false;
  const entries = page.entries.map(entry => {
    if (entry.libraryId) return entry;
    changed = true;
    return Object.freeze({...entry, libraryId});
  });
  return changed ? Object.freeze({...page, entries: Object.freeze(entries)}) : page;
}

/** The session was retired (a new query, a closed scope); its late answers are not anyone's. */
export const browseSessionClosed = (): Error => Object.assign(new Error('browse-scope-closed'), {name: 'AbortError'});
/** The server no longer knows where this list had got to: read it again from the top. */
export const browseListMoved = (): Error => Object.assign(new Error('browse-list-moved'), {code: 'stale_continuation', retry: 'after_refresh'});
/** An answer that says this list is gone for this viewer, or that its place was lost: not a passing failure. */
const ended = (error: unknown): boolean => isStaleAnswer(error)
  || typeof error === 'object' && error !== null && ('code' in error && error.code === 'cursor_expired'
    || 'status' in error && typeof error.status === 'number' && [401, 403, 404].includes(error.status));

export type BrowseSessionOptions<T> = Readonly<{
  query: BrowseSessionQuery;
  request: (path: string, method: string, body: unknown, signal: AbortSignal) => Promise<unknown>;
  /** The owning screen is still open for this viewer. */
  current: () => boolean;
  /** What the collection holds for each entry (the entry itself, or a membership row). */
  item: (entry: ContentEntry, page: BrowseResult, index: number) => T;
  /** The first page arrived: the list has answered. The value names this reading of the list and nothing else. */
  onRevision?: (revision: string) => void;
  /** Kept for the screens that offer "select everything in this view"; the server does not describe a whole view yet, so neither is called. */
  onWhole?: (whole: BrowseWholeSelection) => void;
  onWholeUnavailable?: (value: BrowseWholeSelectionUnavailable | undefined) => void;
  onPage?: (page: BrowseResult) => void;
  /** The list's exact length, or where each letter starts: each comes from its own request, after the first page is already shown. */
  onFacts?: (facts: Readonly<{total?: number; positionIndex?: Readonly<Record<string, number>>}>) => void;
  /** The list ended under this query. Later pages refuse until a new session. */
  onStale?: (error: unknown) => void;
}>;

let readings = 0;

/** One applied query's page source, shared by web Browse and the native library.
 * Each query gets its own session: a new filter, sort or opening retires the old one,
 * so a late page can never land on the query that replaced it.
 *
 * The screens ask by position: the page after one already read follows its cursor, and any other
 * position is opened directly. */
export class BrowseSession<T> {
  readonly query: BrowseSessionQuery;
  private readonly options: BrowseSessionOptions<T>;
  private pinned?: string;
  private ended = false;
  private retired = false;
  private life = new AbortController();
  private entries = new WeakSet<ContentEntry>();
  /** Where each page starts: position to cursor. Position 0 needs none. */
  private cursors = new Map<number, string>([[0, '']]);
  private total?: number;
  private counting?: Promise<void>;
  private lettering?: Promise<void>;
  private letters?: Readonly<Record<string, number>>;
  /** One walk at a time, so two pages never race for the same cursor. */
  private queue: Promise<unknown> = Promise.resolve();
  constructor(options: BrowseSessionOptions<T>) {
    this.options = options;
    this.query = options.query;
  }
  private live(): boolean {return !this.retired && !this.ended && this.options.current();}
  /** Names this reading of the list, once its first page has replied. */
  get revision(): string | undefined {return this.live() ? this.pinned : undefined;}
  get stale(): boolean {return this.ended;}
  wholeSelection(): BrowseWholeSelection | undefined {return undefined;}
  wholeSelectionUnavailable(): BrowseWholeSelectionUnavailable | undefined {return undefined;}
  /** The entry arrived in one of this query's pages, and the query is still current. */
  owns(entry: ContentEntry): boolean {return this.live() && this.entries.has(entry);}
  retire(): void {this.retired = true; this.life.abort(); this.entries = new WeakSet();}
  forgetWholeSelection(): void {}
  private end(error: unknown): void {
    if (this.retired || this.ended) return;
    this.ended = true; this.life.abort(); this.entries = new WeakSet();
    this.options.onStale?.(error);
  }
  fetchPage: FetchPage<T> = (start, count, signal) => {
    const run = this.queue.then(() => this.read(start, count, signal));
    this.queue = run.catch(() => undefined);
    return run;
  };
  private async read(start: number, count: number, signal: AbortSignal) {
    if (this.retired || !this.options.current()) throw browseSessionClosed();
    if (this.ended) throw browseListMoved();
    const list = libraryItemQuery({id: this.query.libraryId}, this.query.pivot, this.query.nodes, this.query.sorts);
    const lifetime = linkAbortSignals([signal, this.life.signal]), active = lifetime.signal;
    const api = {request: <R>(path: string, method = 'GET', body?: unknown, input?: AbortSignal) => this.options.request(path, method, body, input ?? active) as Promise<R>};
    try {
      // The count is its own request and no page waits for it; until it answers the list is "at least this long".
      this.counting ??= countItems({request: <R>(path: string, method = 'GET', body?: unknown, input?: AbortSignal) => this.options.request(path, method, body, input ?? this.life.signal) as Promise<R>}, list, this.life.signal)
        .then(total => {if (total !== undefined) {this.total = total; if (this.live()) this.options.onFacts?.({total});}}, () => undefined);
      // The letter rail, for a list in title order: each first letter and how many titles begin with it.
      const title = this.query.sorts[0]?.field === 'title' ? this.query.sorts[0] : undefined;
      if (title) this.lettering ??= readFacet({request: <R>(path: string, method = 'GET', body?: unknown, input?: AbortSignal) => this.options.request(path, method, body, input ?? this.life.signal) as Promise<R>}, 'letter', list, this.life.signal)
        .then(facet => {
          const counts = title.direction === 'desc' ? [...facet.values].reverse() : facet.values, letters: Record<string, number> = {};
          let position = 0;
          for (const letter of counts) {letters[letter.value] = position; position += letter.count;}
          this.letters = letters;
          if (this.live()) this.options.onFacts?.({positionIndex: letters});
        }).catch(() => undefined);
      // The next page follows its cursor; anywhere else (a scroll bar, a letter) opens the list at that position.
      const cursor = this.cursors.get(start);
      const page = await readItems(api, list, cursor ? {cursor, limit: count} : {at: start, limit: count}, active);
      const end = start + page.items.length;
      if (page.nextCursor && page.items.length) this.cursors.set(end, page.nextCursor);
      if (this.retired || !this.options.current() || signal.aborted) throw browseSessionClosed();
      const entries = entriesFromItems(page.items, this.query.libraryId);
      const counted = this.total ?? (page.nextCursor ? undefined : end), total = counted ?? end + 1;
      const result: BrowseResult = Object.freeze({pivot: this.query.pivot, applied: {query: null, sort: this.query.sorts, seek: null}, entries,
        pageInfo: {start, total, totalIsLowerBound: counted === undefined, revision: this.pinned ?? '', hasMore: !!page.nextCursor, nextCursor: page.nextCursor ?? ''}, positionIndex: []});
      const first = this.pinned === undefined;
      this.pinned ??= 'reading-' + ++readings;
      for (const entry of entries) this.entries.add(entry);
      if (first) this.options.onRevision?.(this.pinned);
      this.options.onPage?.(result);
      if (!this.live() || signal.aborted) throw browseSessionClosed();
      return {items: entries.map((entry, index) => this.options.item(entry, result, index)), total, ...(counted === undefined ? {totalIsLowerBound: true} : {}), positionIndex: this.letters ?? {}};
    } catch (error) {
      if (!this.retired && this.options.current() && !signal.aborted && ended(error)) this.end(error);
      throw error;
    } finally {lifetime.dispose();}
  }
}
