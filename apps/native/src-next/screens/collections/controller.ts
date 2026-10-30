import {createWindowedCollection, type FetchPage, type WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {libraryContentPages, type ContentEntry, type ContentProjection, type ContentScope, type LibraryContentApi} from '../../../../../packages/client-core/src/library-content';

export type NativeCollectionSort = Readonly<{id: string; labelKey: string; directions: readonly ('asc' | 'desc')[]}>;

export type NativeCollectionState = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  /** The server's own heading for the collection, else the route's real title. */
  title: string;
  /** The members, read a page at a time into a bounded window (LR05). */
  collection: WindowedCollection<ContentEntry>;
  totalCount: number;
  /** The first member, while its page is in memory (the backdrop). */
  first?: ContentEntry;
  /** Every member, only while the whole collection fits in memory (the year span). */
  complete?: readonly ContentEntry[];
  projection?: ContentProjection;
  sorts: readonly NativeCollectionSort[];
  sort?: string;
  error?: Readonly<{code: string; message: string; retryable: boolean}>;
}>;

export const nativeCollectionEntryKey = (entry: ContentEntry): string => `${entry.kind}:${entry.id}`;
const PAGE = 40;

const failure = (error: unknown): NativeCollectionState['error'] => {
  const value = error && typeof error === 'object' ? error as {code?: unknown; message?: unknown; retryable?: unknown} : undefined;
  return Object.freeze({code: typeof value?.code === 'string' ? value.code : 'unavailable', message: typeof value?.message === 'string' ? value.message : '',
    retryable: value?.retryable !== false});
};

/** The catalogue collection view, read from the real library content projection
 * (`view=collection&entityId=…`). No saved user collection is substituted. Members
 * live in a bounded window over start-addressed pages: a page that leaves memory is
 * read again by its own index, and the first page pins the viewer fence and revision. */
export class NativeCollectionController {
  readonly collection: WindowedCollection<ContentEntry>;
  private readonly api: LibraryContentApi;
  private readonly scope: ContentScope;
  private readonly fallbackTitle: string;
  private readonly libraryId: string;
  private readonly collectionId: string;
  private readonly current: () => boolean;
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribe: () => void;
  /** The pages that can make the whole collection resident (and page 0 holds the backdrop). */
  private static readonly watched = 4;
  private projection?: ContentProjection;
  private order?: Readonly<{sort: string; direction: 'asc' | 'desc'}>;
  /** Each query's page source; a later one's projections never reach an earlier query. */
  private source = 0;
  private opened = false;
  private disposed = false;
  private cached: NativeCollectionState;

  constructor(options: Readonly<{api: LibraryContentApi; scope: ContentScope;
    libraryId: string; collectionId: string; title: string; current: () => boolean}>) {
    this.api = options.api;
    this.scope = options.scope;
    this.fallbackTitle = options.title;
    this.libraryId = options.libraryId;
    this.collectionId = options.collectionId;
    this.current = options.current;
    this.collection = createWindowedCollection({fetchPage: this.pages(), pageSize: PAGE, maxResidentPages: NativeCollectionController.watched, prefetchPages: 1, maxConcurrent: 1,
      keyOf: nativeCollectionEntryKey});
    this.cached = this.read();
    const stops = [this.collection.subscribe(this.changed)];
    for (let page = 0; page < NativeCollectionController.watched; page++) stops.push(this.collection.subscribePage(page, this.changed));
    this.unsubscribe = () => stops.forEach(stop => stop());
  }
  /** Each member's absolute index, for selection over a sparse window. */
  private readonly positions = new WeakMap<ContentEntry, number>();
  private pages(): FetchPage<ContentEntry> {
    const source = ++this.source;
    const read = libraryContentPages({api: this.api, scope: this.scope, pageSize: PAGE,
      route: {libraryId: this.libraryId, view: 'collection', entityId: this.collectionId, ...(this.order ?? {})},
      onPage: projection => {
        if (this.disposed || source !== this.source) return;
        // Every page carries the same header; only a real change reaches the screen.
        const same = this.projection && JSON.stringify(this.projection) === JSON.stringify(projection);
        if (!same) {this.projection = projection; this.changed();}
      }});
    return async (start, count, signal) => {
      const page = await read(start, count, signal);
      page.items.forEach((entry, index) => this.positions.set(entry, start + index));
      return page;
    };
  }
  /** The members in memory with their absolute indices (at most the window's budget). */
  resident = (): Readonly<{entries: readonly ContentEntry[]; positions: readonly number[]}> => {
    const entries = this.disposed ? [] : this.collection.residentItems();
    return {entries, positions: entries.map(entry => this.positions.get(entry) ?? -1)};
  };
  private read(): NativeCollectionState {
    const view = this.collection.getSnapshot(), projection = this.projection;
    const stale = view.error && typeof view.error === 'object' && (view.error as {code?: unknown}).code === 'stale_continuation';
    const phase: NativeCollectionState['phase'] = !this.opened ? 'idle' : view.status === 'error' ? stale ? 'refresh-required' : 'error'
      : view.total === undefined ? 'loading' : 'ready';
    const totalCount = view.total ?? 0, resident = view.total === undefined ? [] : this.collection.residentItems();
    const first = this.collection.itemAt(0);
    return Object.freeze({
      phase, title: projection?.heading.fallback?.trim() || this.fallbackTitle, collection: this.collection, totalCount,
      ...(first ? {first} : {}),
      ...(view.total !== undefined && !view.totalIsLowerBound && resident.length === view.total ? {complete: resident} : {}),
      ...(projection ? {projection} : {}),
      sorts: (projection?.sorts ?? []).map(sort => ({id: sort.id, labelKey: sort.labelKey, directions: sort.directions})),
      ...(projection?.query.sort ? {sort: projection.query.sort} : {}),
      ...(view.status === 'error' ? {error: failure(view.error)} : {}),
    });
  }
  private changed = () => {
    if (this.disposed || !this.current()) return;
    this.cached = this.read();
    for (const listener of [...this.listeners]) listener();
  };
  getSnapshot = () => this.cached;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  /** Only an entry this window holds right now is acted on. */
  contains = (entry: ContentEntry): boolean => !this.disposed && this.collection.residentItems().includes(entry);
  /** Read the first page at once; the grid takes over the visible range when it lays out. */
  open = async (): Promise<void> => {
    if (this.disposed || !this.current() || this.opened) return;
    this.opened = true;
    this.collection.ensureRange(0, 0);
    this.changed();
  };
  sort = async (sort: string, direction: 'asc' | 'desc'): Promise<void> => {
    if (this.disposed || !this.current()) return;
    this.order = Object.freeze({sort, direction});
    this.collection.invalidate(this.pages());
  };
  /** A changed library reads the collection again from the top; any other failure retries its pages. */
  retry = async (): Promise<void> => {
    if (this.disposed || !this.current()) return;
    if (this.cached.phase === 'refresh-required') {this.projection = undefined; this.collection.invalidate(this.pages());}
    else this.collection.retry();
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.unsubscribe();
    this.listeners.clear();
    this.collection.dispose();
  };
}
