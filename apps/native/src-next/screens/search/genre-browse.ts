import {
  BrowseQueryBuilder, browseCapabilitiesPath, browsePath, parseBrowseCapabilities, parseBrowseResult,
  type BrowseCapabilities, type BrowseNode, type BrowseSortSelection,
} from '../../../../../packages/client-core/src/browse';
import {searchGenreBrowsePivot, searchGenreBrowseRequest} from '../../../../../packages/client-core/src/search-genres';
import {createWindowedCollection, type FetchPage, type WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import type {ContentEntry, LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {NativeLibraryFilterDraft} from '../library/filters';
import {nativeLibraryEntryKey, type NativeLibraryMemory} from '../library/model';

export type NativeGenreBrowseSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  capabilities?: BrowseCapabilities; predicates: readonly BrowseNode[]; sort?: BrowseSortSelection;
  /** The actual opaque Browse token. It is never split into Home revisions. */
  revision?: string; queryRevision: number; collection?: WindowedCollection<ContentEntry>; error?: unknown;
}>;
const closed: NativeGenreBrowseSnapshot = Object.freeze({phase: 'idle', predicates: [], queryRevision: 0});
const retired = () => Object.assign(new Error('native_genre_browse_retired'), {name: 'AbortError', code: 'cancelled'});

/** A real across-library video-titles source, not a Library object with id '*'.
 * The canonical helper supplies its immutable genre and initial ordering. */
export class NativeGenreBrowseDestination {
  private readonly options: Readonly<{api: LibraryContentApi; value: string; current: () => boolean}>;
  private readonly base: BrowseNode;
  private readonly initialSort: BrowseSortSelection;
  private readonly listeners = new Set<() => void>();
  private readonly drafts = new Set<NativeLibraryFilterDraft>();
  private state: NativeGenreBrowseSnapshot = closed;
  private memory: NativeLibraryMemory = {firstVisibleIndex: 0, offset: 0};
  private entries = new WeakSet<ContentEntry>();
  private abort?: AbortController;
  private pending?: Promise<void>;
  private disposed = false;
  constructor(options: NativeGenreBrowseDestination['options']) {
    this.options = options;
    const request = searchGenreBrowseRequest(options.value);
    if (!request.query || !request.sort?.[0]) throw new Error('native_genre_browse_contract_required');
    this.base = request.query; this.initialSort = request.sort[0];
  }
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  private request = async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!this.current() || signal?.aborted) throw retired();
    const result = await this.options.api.request<T>(path, method, body, signal);
    if (!this.current() || signal?.aborted) throw retired();
    return result;
  };
  getSnapshot = (): NativeGenreBrowseSnapshot => {this.current(); return this.state;};
  getMemory = (): NativeLibraryMemory => {this.current(); return this.memory;};
  subscribe = (listener: () => void): (() => void) => {if (!this.current()) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeGenreBrowseSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  load = (): Promise<void> => {
    if (!this.current() || this.state.phase === 'ready') return Promise.resolve();
    if (this.pending) return this.pending;
    this.abort?.abort(); this.retireDrafts(); this.state.collection?.dispose();
    this.entries = new WeakSet();
    const abort = new AbortController(); this.abort = abort;
    const task = Promise.resolve().then(async () => {
      this.publish({...closed, phase: 'loading', capabilities: undefined, collection: undefined, revision: undefined, sort: undefined, error: undefined,
        queryRevision: this.state.queryRevision + 1});
      const capabilities = parseBrowseCapabilities(await this.request(browseCapabilitiesPath('*', searchGenreBrowsePivot), 'GET', undefined, abort.signal));
      if (!this.current() || abort.signal.aborted) return;
      if (capabilities.library.id !== '*' || !capabilities.pivots.some(pivot => pivot.id === searchGenreBrowsePivot && pivot.browsable)
        || capabilities.resolvedPivot && capabilities.resolvedPivot.id !== searchGenreBrowsePivot) throw new Error('native_genre_browse_scope_mismatch');
      const predicates = Object.freeze([this.base]), sort = this.initialSort;
      this.build(capabilities, predicates, sort, 0, 1);
      const collection = createWindowedCollection({fetchPage: this.pages(capabilities, predicates, sort),
        pageSize: Math.min(60, capabilities.queryLimits.maximumLimit), maxResidentPages: 4, prefetchPages: 1, maxConcurrent: 1, keyOf: nativeLibraryEntryKey});
      this.publish({phase: 'ready', capabilities, predicates, sort, collection}); collection.ensureRange(0, 0);
    }).catch(error => {if (!abort.signal.aborted) this.publish({phase: 'error', error});})
      .finally(() => {if (this.pending === task) this.pending = undefined;});
    this.pending = task; return task;
  };
  private build(capabilities: BrowseCapabilities, predicates: readonly BrowseNode[], sort: BrowseSortSelection, start: number, count: number, revision?: string) {
    if (!predicates.some(node => JSON.stringify(node) === JSON.stringify(this.base))) throw new Error('native_genre_anchor_required');
    const builder = new BrowseQueryBuilder(capabilities, searchGenreBrowsePivot);
    for (const node of predicates) builder.add(node);
    return builder.sort(sort.field, sort.direction, sort.seed).limit(count).range({start, ...(revision ? {revision} : {})}).build();
  }
  private pages(capabilities: BrowseCapabilities, predicates: readonly BrowseNode[], sort: BrowseSortSelection): FetchPage<ContentEntry> {
    let revision: string | undefined;
    return async (start, count, signal) => {
      if (!this.current() || this.state.phase !== 'ready' || this.state.predicates !== predicates || this.state.sort !== sort) throw retired();
      try {
        const result = parseBrowseResult(await this.request(browsePath('*'), 'POST', this.build(capabilities, predicates, sort, start, count, revision), signal));
        if (result.pivot !== searchGenreBrowsePivot || result.pageInfo.start !== start || result.entries.length > count || !result.pageInfo.revision
          || revision && result.pageInfo.revision !== revision || result.entries.some(entry => !entry.libraryId || !['movie', 'show'].includes(entry.kind))) {
          throw Object.assign(new Error('native_genre_browse_scope_mismatch'), {code: 'stale_continuation'});
        }
        revision ??= result.pageInfo.revision;
        result.entries.forEach(entry => this.entries.add(entry));
        if (this.state.predicates === predicates && this.state.sort === sort && this.state.revision !== revision) this.publish({revision});
        return {items: result.entries, total: result.pageInfo.total, totalIsLowerBound: result.pageInfo.totalIsLowerBound,
          positionIndex: Object.fromEntries(result.positionIndex.map(value => [value.key, value.index]))};
      } catch (error) {
        // A changed source cannot be patched into resident pages from the old
        // source. Explicit retry starts a fresh collection and opaque revision.
        if (!signal.aborted && this.state.predicates === predicates && (error as {code?: unknown})?.code === 'stale_continuation') this.publish({phase: 'error', error});
        throw error;
      }
    };
  }
  setQuery = (predicates: readonly BrowseNode[], sort = this.state.sort): void => {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.capabilities || !this.state.collection || !sort) return;
    this.build(this.state.capabilities, predicates, sort, 0, 1);
    this.retireDrafts(); const next = Object.freeze([...predicates]);
    const collection = this.state.collection, capabilities = this.state.capabilities;
    this.entries = new WeakSet();
    this.memory = {firstVisibleIndex: 0, offset: 0};
    this.publish({predicates: next, sort, revision: undefined, queryRevision: this.state.queryRevision + 1});
    collection.invalidate(this.pages(capabilities, next, sort));
  };
  createFilters = (): NativeLibraryFilterDraft => {
    const source = this.getSnapshot();
    if (source.phase !== 'ready' || !source.capabilities || !source.sort || !source.collection) throw new Error('native_genre_browse_not_ready');
    const draft = new NativeLibraryFilterDraft({api: {request: this.request}, capabilities: source.capabilities, pivot: searchGenreBrowsePivot,
      predicates: source.predicates, fixed: [this.base], sort: source.sort,
      current: () => this.current() && this.state.phase === 'ready' && this.state.predicates === source.predicates && this.state.sort === source.sort,
      apply: predicates => this.setQuery(predicates), onDispose: () => {this.drafts.delete(draft);}});
    this.drafts.add(draft); return draft;
  };
  contains = (entry: ContentEntry): boolean => this.current() && this.state.phase === 'ready' && this.entries.has(entry) && this.state.collection?.residentItems().includes(entry) === true;
  remember = (firstVisibleIndex: number, offset: number): void => {
    if (!this.current() || !Number.isSafeInteger(firstVisibleIndex) || firstVisibleIndex < 0 || !Number.isFinite(offset) || offset < 0) return;
    const first = this.state.collection?.itemAt(firstVisibleIndex);
    this.memory = Object.freeze({firstVisibleIndex, offset, ...(first ? {firstVisibleId: nativeLibraryEntryKey(first)} : {})});
  };
  retry = (): void => {if (this.state.phase === 'error') void this.load(); else this.state.collection?.retry();};
  private retireDrafts(): void {for (const draft of [...this.drafts]) draft.dispose();}
  dispose = (): void => {if (this.disposed) return; this.disposed = true; this.abort?.abort(); this.retireDrafts(); this.state.collection?.dispose();
    this.state = closed; this.entries = new WeakSet(); this.memory = {firstVisibleIndex: 0, offset: 0}; this.listeners.clear();};
}
