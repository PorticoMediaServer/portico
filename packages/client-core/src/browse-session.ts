import {BrowseQueryBuilder, type BrowseCapabilities, type BrowseNode, type BrowseResult, type BrowseSortSelection, type BrowseWholeSelection, type BrowseWholeSelectionUnavailable} from './browse.ts';
import type {FetchPage} from './collections/windowed.ts';
import type {ContentEntry} from './library-content.ts';
import {entriesFromItems, readItems} from './items.ts';
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
  /** The list ended under this query. Later pages refuse until a new session. */
  onStale?: (error: unknown) => void;
}>;

let readings = 0;

/** One applied query's page source, shared by web Browse and the native library.
 * Each query gets its own session: a new filter, sort or opening retires the old one,
 * so a late page can never land on the query that replaced it.
 *
 * The server pages by cursor and the screens ask by position, so the session remembers the
 * cursor at each position it has passed and walks forward from the nearest one. */
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
      // Walk from the nearest position already passed to the one asked for.
      let at = [...this.cursors.keys()].filter(known => known <= start).reduce((best, known) => Math.max(best, known), 0);
      for (;;) {
        const cursor = this.cursors.get(at)!, wanted = at === start;
        const page = await readItems(api, list, {cursor: cursor || undefined, limit: wanted ? count : Math.min(200, start - at), total: this.total === undefined}, active);
        if (page.total !== undefined) this.total = page.total;
        const end = at + page.items.length;
        if (page.nextCursor && page.items.length) this.cursors.set(end, page.nextCursor);
        if (!wanted && page.items.length && page.nextCursor) { at = end; continue; }
        if (this.retired || !this.options.current() || signal.aborted) throw browseSessionClosed();
        const entries = wanted ? entriesFromItems(page.items, this.query.libraryId) : [];
        const total = this.total ?? end;
        const result: BrowseResult = Object.freeze({pivot: this.query.pivot, applied: {query: null, sort: this.query.sorts, seek: null}, entries,
          pageInfo: {start, total, totalIsLowerBound: false, revision: this.pinned ?? '', hasMore: wanted && !!page.nextCursor, nextCursor: wanted ? page.nextCursor ?? '' : ''}, positionIndex: []});
        const first = this.pinned === undefined;
        this.pinned ??= 'reading-' + ++readings;
        for (const entry of entries) this.entries.add(entry);
        if (first) this.options.onRevision?.(this.pinned);
        this.options.onPage?.(result);
        if (!this.live() || signal.aborted) throw browseSessionClosed();
        return {items: entries.map((entry, index) => this.options.item(entry, result, index)), total, positionIndex: {}};
      }
    } catch (error) {
      if (!this.retired && this.options.current() && !signal.aborted && ended(error)) this.end(error);
      throw error;
    } finally {lifetime.dispose();}
  }
}
