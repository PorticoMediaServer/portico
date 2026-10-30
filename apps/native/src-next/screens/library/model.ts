import type {Library} from '../../../../../packages/client-core/src';
import type {SavedDefinition} from '../../../../../packages/client-core/src/personal-saved';
import {BrowseTotalsMemory, browseCapabilitiesPath, completeBrowseNode, parseBrowseCapabilities, type BrowseCapabilities, type BrowseNode, type BrowseRememberedTotal, type BrowseSortSelection, type BrowseWholeSelection, type BrowseWholeSelectionUnavailable} from '../../../../../packages/client-core/src/browse';
import {BrowseSession, browseSessionBuilder, type BrowseSessionQuery} from '../../../../../packages/client-core/src/browse-session';
import {createWindowedCollection, type WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {isStaleAnswer} from '../../../../../packages/client-core/src/freshness-actions';
import {LibraryContentService, type ContentEntry, type ContentScope, type LibraryContentApi, type LibraryContentSnapshot} from '../../../../../packages/client-core/src/library-content';
import {libraryTabs, type LibraryTab, type LibraryTabId} from '../../../../../packages/client-core/src/presentation/library-tabs';
import {NativeLibraryFilterDraft} from './filters';
import type {NativeLibraryOpening} from './opening';

export const nativeLibraryEntryKey = (entry: ContentEntry): string => JSON.stringify([entry.kind, entry.libraryId, entry.id]);
const cancelled = () => Object.assign(new Error('native_library_retired'), {name: 'AbortError'});

export type NativeLibraryMemory = Readonly<{
  firstVisibleId?: string;
  firstVisibleIndex: number;
  offset: number;
}>;
export type NativeLibraryViewDraft = Readonly<{predicates: readonly BrowseNode[]; sort: BrowseSortSelection}>;
export type NativeLibrarySnapshot = Readonly<{
  library: Library;
  tab: LibraryTab;
  phase: 'idle' | 'loading' | 'ready' | 'error';
  capabilities?: BrowseCapabilities;
  predicates: readonly BrowseNode[];
  queryRevision: number;
  /** The actual opaque Browse page token, absent until the current query arrives. */
  revision?: string;
  /** The server's exact whole-view aggregate, present only on a replayable view's first page. */
  wholeSelection?: BrowseWholeSelection;
  wholeSelectionUnavailable?: BrowseWholeSelectionUnavailable;
  unfilteredTotal?: BrowseRememberedTotal;
  sort?: BrowseSortSelection;
  defaultSort?: BrowseSortSelection;
  collection?: WindowedCollection<ContentEntry>;
  discover: LibraryContentSnapshot;
  memory: NativeLibraryMemory;
  error?: unknown;
}>;

/** Ruling 37 keeps today's canSave rule, against this tab's actual opening sort. */
export function nativeLibraryCanSave(source: NativeLibrarySnapshot, draft: NativeLibraryViewDraft): boolean {
  return source.phase === 'ready' && !!source.defaultSort &&
    (JSON.stringify(draft.predicates) !== JSON.stringify(source.tab.fixedFilter ?? [])
      || draft.sort.field !== source.defaultSort.field || draft.sort.direction !== source.defaultSort.direction);
}

/** One authorized library in one actual session. This holds sparse native pages,
 * never an array sized by the library's count or a client-filtered catalogue. */
export class NativeLibraryDestination {
  private readonly options: Readonly<{api: LibraryContentApi; scope: ContentScope; library: Library; current: () => boolean; /** An explicit opening tab, for a surface whose pivot came from the server's own reading pivots. */ tab?: LibraryTab; /** One exact saved definition offered by the Saved page, applied once. */ initial?: NativeLibraryOpening}>;
  private readonly discover: LibraryContentService;
  private readonly unsubscribe: () => void;
  private state: NativeLibrarySnapshot;
  private readonly listeners = new Set<() => void>();
  private controller?: AbortController;
  private operation = 0;
  /** P2-61 HOME-RESILIENCE: one automatic re-read per query when its FIRST page is refused as
   * stale (a 412 revision_mismatch while a library imports). Reset only by the viewer's own query. */
  private staleRetried = false;
  private disposed = false;
  private memory: NativeLibraryMemory = {firstVisibleIndex: 0, offset: 0};
  private pending?: Promise<void>;
  private pendingTab?: LibraryTabId;
  private readonly filters = new Set<NativeLibraryFilterDraft>();
  private readonly totals = new BrowseTotalsMemory();
  /** The applied query's page source; a new tab or query retires it. */
  private session?: BrowseSession<ContentEntry>;
  private readonly initialOffered: boolean;
  private initialApplied = false;

  constructor(options: NativeLibraryDestination['options']) {
    this.options = options;
    this.discover = new LibraryContentService({api: {request: this.request}, scope: options.scope});
    const tabs = libraryTabs(options.library.kind);
    const offered = options.initial ? tabs.find(value => value.pivot === options.initial!.definition.pivot) : undefined;
    this.initialOffered = !!offered;
    const tab = options.tab ?? offered ?? tabs.find(value => value.id === 'browse') ?? tabs[0]!;
    this.state = Object.freeze({library: options.library, tab, phase: 'idle', predicates: [], queryRevision: 0, discover: this.discover.getSnapshot(), memory: {firstVisibleIndex: 0, offset: 0}});
    this.unsubscribe = this.discover.subscribe(() => this.publish({discover: this.discover.getSnapshot()}));
  }
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  private request = async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!this.current() || signal?.aborted) throw cancelled();
    const result = await this.options.api.request<T>(path, method, body, signal);
    if (!this.current() || signal?.aborted) throw cancelled();
    return result;
  };
  getSnapshot = (): NativeLibrarySnapshot => {this.current(); return this.state;};
  /** Exact latest native measurement; it does not cause a React render per pixel. */
  getMemory = (): NativeLibraryMemory => {this.current(); return this.memory;};
  /** Capture the actual staged Filters draft, not whichever query is active
   * after the naming sheet opens. Canonical capabilities validate the whole tree. */
  captureSavedView = (draft: NativeLibraryViewDraft): SavedDefinition => {
    if (!this.current()) throw cancelled();
    const {capabilities, tab, phase} = this.state;
    if (phase !== 'ready' || !capabilities || !tab.pivot) throw new Error('native_library_view_unavailable');
    const body = this.build(capabilities, tab, draft.predicates, draft.sort, 0, 1);
    const query = body.query ? JSON.parse(JSON.stringify(body.query)) as BrowseNode : null;
    const freeze = (node: BrowseNode): void => {
      if ('all' in node) {node.all.forEach(freeze); Object.freeze(node.all);}
      else if ('any' in node) {node.any.forEach(freeze); Object.freeze(node.any);}
      else if ('not' in node) freeze(node.not);
      else if (Array.isArray(node.value)) Object.freeze(node.value);
      Object.freeze(node);
    };
    if (query) freeze(query);
    return Object.freeze({libraryId: this.options.library.id, pivot: tab.pivot, query,
      sort: Object.freeze((body.sort ?? []).map(value => Object.freeze({...value}))), presentation: 'grid'});
  };
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeLibrarySnapshot>): void {
    if (!this.current()) return;
    if (patch.memory) this.memory = patch.memory;
    this.state = Object.freeze({...this.state, ...patch, memory: this.memory});
    for (const listener of [...this.listeners]) listener();
  }
  load = (tabId: LibraryTabId = this.state.tab.id): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending && this.pendingTab === tabId) return this.pending;
    const task = this.loadTab(tabId).finally(() => {if (this.pending === task) {this.pending = undefined; this.pendingTab = undefined;}});
    this.pending = task; this.pendingTab = tabId; return task;
  };
  private async loadTab(tabId: LibraryTabId): Promise<void> {
    if (!this.current()) return;
    const tab = this.options.tab && this.options.tab.id === tabId ? this.options.tab : libraryTabs(this.options.library.kind).find(value => value.id === tabId);
    if (!tab) throw new Error('native_library_tab_not_offered');
    if (tab.id === this.state.tab.id && this.state.phase === 'ready') return;
    const retryQuery = tab.id === this.state.tab.id && this.state.phase === 'error' && this.state.collection && this.state.sort
      ? {predicates: this.state.predicates, sort: this.state.sort} : undefined;
    this.retireFilters(); this.controller?.abort(); this.discover.cancel(); this.state.collection?.dispose();
    this.session?.retire(); this.session = undefined;
    const controller = new AbortController(), revision = ++this.operation; this.controller = controller; this.staleRetried = false;
    this.publish({tab, phase: 'loading', capabilities: undefined, collection: undefined, predicates: [], sort: undefined, defaultSort: undefined, revision: undefined, wholeSelection: undefined, wholeSelectionUnavailable: undefined, unfilteredTotal: undefined, error: undefined,
      memory: {firstVisibleIndex: 0, offset: 0}});
    try {
      if (!this.initialApplied && this.options.initial && this.options.initial.definition.pivot !== tab.pivot) throw new Error('native_library_view_pivot_unavailable');
      if (tab.shows === 'rows') {
        await this.discover.select({libraryId: this.options.library.id, view: 'discover'});
        if (!this.current() || revision !== this.operation || controller.signal.aborted) return;
        const snapshot = this.discover.getSnapshot();
        this.publish({phase: snapshot.error ? 'error' : 'ready', error: snapshot.error ?? undefined});
        return;
      }
      if (!tab.pivot) throw new Error('native_library_tab_producer_required');
      const capabilities = parseBrowseCapabilities(await this.request(browseCapabilitiesPath(this.options.library.id, tab.pivot), 'GET', undefined, controller.signal));
      if (revision !== this.operation || controller.signal.aborted) return;
      if (capabilities.library.id !== this.options.library.id || capabilities.library.kind !== this.options.library.kind) throw new Error('native_library_scope_mismatch');
      const pivot = capabilities.pivots.find(value => value.id === tab.pivot && value.browsable);
      if (!pivot) throw new Error('native_library_pivot_unavailable');
      const sort = tab.defaultSort && capabilities.sorts.some(value => value.id === tab.defaultSort!.field && pivot.sorts?.includes(value.id) && value.directions.includes(tab.defaultSort!.direction))
        ? tab.defaultSort : pivot.defaultSort[0];
      if (!sort) throw new Error('native_library_sort_required');
      if (this.options.initial && !this.initialOffered && !this.initialApplied) throw new Error('native_library_view_pivot_unavailable');
      const fixed = (tab.fixedFilter ?? []).map(value => completeBrowseNode({...value}) as BrowseNode);
      let predicates = retryQuery?.predicates ?? fixed, chosenSort = retryQuery?.sort ?? sort;
      if (this.options.initial && this.initialOffered && !this.initialApplied) {
        const definition = this.options.initial.definition;
        if (definition.libraryId !== this.options.library.id || definition.pivot !== tab.pivot) throw new Error('native_library_view_pivot_unavailable');
        if (definition.presentation !== 'grid') throw new Error('native_library_view_presentation_unavailable');
        if (definition.sort.length !== 1) throw new Error('native_library_view_sort_unavailable');
        const first = definition.sort[0]!;
        const offeredSort = capabilities.sorts.find(value => value.id === first.field && pivot.sorts?.includes(value.id) && value.directions.includes(first.direction));
        if (!offeredSort) throw new Error('native_library_view_sort_unavailable');
        chosenSort = Object.freeze({field: offeredSort.id, direction: first.direction, ...(first.seed === undefined ? {} : {seed: first.seed})});
        predicates = Object.freeze([...fixed, ...(definition.query ? [definition.query] : [])]);
      }
      // The canonical builder validates every predicate, direction and request bound.
      // The initial definition stays pending until this exact build succeeds, so a
      // retry re-applies it or fails honestly rather than opening defaults.
      this.build(capabilities, tab, predicates, chosenSort, 0, 1);
      const collection = createWindowedCollection({fetchPage: this.open(capabilities, tab, predicates, chosenSort).fetchPage,
        pageSize: Math.min(60, capabilities.queryLimits.maximumLimit), maxResidentPages: 4, prefetchPages: 1, maxConcurrent: 1, keyOf: nativeLibraryEntryKey});
      this.publish({phase: 'ready', capabilities, predicates, sort: chosenSort, defaultSort: sort, collection});
      if (this.options.initial && this.initialOffered) this.initialApplied = true;
      collection.ensureRange(0, 0);
    } catch (error) {if (this.current() && revision === this.operation && !controller.signal.aborted) this.publish({phase: 'error', error});}
  }
  private query(capabilities: BrowseCapabilities, tab: LibraryTab, predicates: readonly BrowseNode[], sort: BrowseSortSelection): BrowseSessionQuery {
    return {libraryId: this.options.library.id, pivot: tab.pivot!, capabilities, nodes: predicates, sorts: [sort]};
  }
  private build(capabilities: BrowseCapabilities, tab: LibraryTab, predicates: readonly BrowseNode[], sort: BrowseSortSelection, start: number, count: number) {
    return browseSessionBuilder(this.query(capabilities, tab, predicates, sort)).limit(count).range({start}).build();
  }
  /** The shared Browse session for this exact tab, filter and sort (SIM-12). This model keeps
   * only what is native: the remembered unfiltered total and its own published state. */
  private open(capabilities: BrowseCapabilities, tab: LibraryTab, predicates: readonly BrowseNode[], sort: BrowseSortSelection): BrowseSession<ContentEntry> {
    this.session?.retire();
    const base = JSON.stringify(tab.fixedFilter ?? []) === JSON.stringify(predicates);
    const totalsKey = JSON.stringify([this.options.scope, this.options.library.id, tab.id, tab.pivot]);
    let answered = false;
    const session: BrowseSession<ContentEntry> = new BrowseSession<ContentEntry>({query: this.query(capabilities, tab, predicates, sort),
      request: (path, method, body, signal) => this.request(path, method, body, signal),
      ...(this.options.api.requestRaw ? {requestRaw: this.options.api.requestRaw.bind(this.options.api)} : {}),
      onPreparationError: error => this.publish({error}),
      current: () => this.current() && this.session === session && this.state.phase === 'ready',
      item: entry => entry,
      onRevision: revision => this.publish({revision}),
      // Only the first page of a replayable view carries the aggregate; later pages never
      // clear or restate it, and another query's descriptor is not adopted here.
      onWhole: whole => {if (this.state.wholeSelection !== whole) this.publish({wholeSelection: whole, wholeSelectionUnavailable: undefined});},
      onWholeUnavailable: (value: BrowseWholeSelectionUnavailable | undefined) => {
        if (this.state.wholeSelectionUnavailable !== value) this.publish({wholeSelectionUnavailable: value, ...(value ? {wholeSelection: undefined} : {})});
      },
      onPage: page => {
        answered = true;
        const total = this.totals.observe(totalsKey, page, base);
        if (this.state.unfilteredTotal !== total) this.publish({unfilteredTotal: total});
      },
      onStale: error => {
        // Nothing is shown yet, so a re-read cannot jump. A second refusal is this library's error,
        // never a loop; a list that has already answered keeps its explicit refresh.
        const {capabilities: offered, tab: shown, predicates: applied, sort: sorted, collection} = this.state;
        if (isStaleAnswer(error) && !answered && !this.staleRetried && this.current() && this.session === session && offered && sorted && collection) {
          this.staleRetried = true; this.session = undefined;
          collection.invalidate(this.open(offered, shown, applied, sorted).fetchPage);
          return;
        }
        this.publish({phase: 'error', error, wholeSelection: undefined, wholeSelectionUnavailable: undefined});
      }});
    this.session = session;
    return session;
  }
  setQuery = (predicates: readonly BrowseNode[], sort = this.state.sort, keep?: NativeLibraryFilterDraft): void => {
    if (!this.current() || !this.state.capabilities || !this.state.collection || !sort) return;
    const {capabilities, tab, collection} = this.state;
    this.build(capabilities, tab, predicates, sort, 0, 1);
    this.retireFilters(keep);
    this.staleRetried = false;
    const next = Object.freeze([...predicates]);
    this.session?.retire(); this.session = undefined;
    this.publish({predicates: next, queryRevision: this.state.queryRevision + 1, revision: undefined, wholeSelection: undefined, wholeSelectionUnavailable: undefined, sort, memory: {firstVisibleIndex: 0, offset: 0}});
    collection.invalidate(this.open(capabilities, tab, next, sort).fetchPage);
  };
  setQuick = (id: string, enabled: boolean): void => {
    const preset = this.state.capabilities?.quickFilters.find(value => value.id === id);
    if (!preset) throw new Error('native_library_quick_not_offered');
    const same = (node: BrowseNode) => JSON.stringify(node) === JSON.stringify(preset.query);
    this.setQuery([...this.state.predicates.filter(node => !same(node)), ...(enabled ? [preset.query] : [])]);
  };
  /** The actual section cursor, not a catalogue walk or a new browse query. */
  nextDiscover = (sectionId: string): Promise<void> => {
    if (!this.current() || this.state.tab.shows !== 'rows') return Promise.resolve();
    return this.discover.next(sectionId);
  };
  /** `live` binds the draft to its capabilities rather than one query snapshot, so
   * the docked panel survives its own applies (tablet-read#1b). The modal panel
   * keeps the opening-query binding it has always had. */
  createFilters = (mode: Readonly<{live?: boolean}> = {}): NativeLibraryFilterDraft => {
    const source = this.getSnapshot();
    if (!this.current() || !source.capabilities || !source.tab.pivot || !source.sort || !source.collection) throw new Error('native_library_filter_source_required');
    const draft = new NativeLibraryFilterDraft({api: {request: this.request, ...(this.options.api.requestRaw ? {requestRaw: this.options.api.requestRaw.bind(this.options.api)} : {})}, capabilities: source.capabilities, pivot: source.tab.pivot, sort: source.sort,
      predicates: source.predicates, fixed: (source.tab.fixedFilter ?? []) as readonly BrowseNode[],
      current: () => this.current() && this.state.capabilities === source.capabilities
        && (mode.live === true || this.state.predicates === source.predicates) && this.state.sort === source.sort,
      apply: (predicates, keep) => this.setQuery(predicates, this.state.sort, keep), onDispose: () => this.filters.delete(draft)});
    this.filters.add(draft); return draft;
  };
  private retireFilters(keep?: NativeLibraryFilterDraft) {for (const draft of [...this.filters]) if (draft !== keep) draft.dispose();}
  contains = (entry: ContentEntry): boolean => this.current() && this.state.phase === 'ready' && (this.state.collection
    ? !!this.session?.owns(entry) && this.state.collection.residentItems().includes(entry)
    : this.state.discover.appended.some(section => section.entries.includes(entry)));
  remember = (firstVisibleIndex: number, offset: number): void => {
    if (!this.current() || !Number.isSafeInteger(firstVisibleIndex) || firstVisibleIndex < 0 || !Number.isFinite(offset) || offset < 0) return;
    const first = this.state.collection?.itemAt(firstVisibleIndex);
    const firstVisibleId = first && nativeLibraryEntryKey(first), before = this.memory;
    if (before.firstVisibleIndex === firstVisibleIndex && before.offset === offset && before.firstVisibleId === firstVisibleId) return;
    this.memory = Object.freeze({firstVisibleIndex, offset, ...(firstVisibleId ? {firstVisibleId} : {})});
    if (before.firstVisibleIndex !== firstVisibleIndex || before.firstVisibleId !== firstVisibleId) this.publish({});
  };
  retry = (): void => {
    if (this.state.phase === 'error') void this.load();
    else if (this.state.tab.shows === 'rows') void this.discover.retry();
    else this.state.collection?.retry();
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.retireFilters(); this.operation++; this.controller?.abort(); this.unsubscribe(); this.pending = undefined; this.pendingTab = undefined;
    this.discover.cancel(); this.discover.dispose(); this.state.collection?.dispose(); this.totals.clear();
    this.session?.retire(); this.session = undefined;
    this.memory = {firstVisibleIndex: 0, offset: 0};
    this.state = Object.freeze({...this.state, phase: 'idle', capabilities: undefined, collection: undefined, predicates: [], sort: undefined, revision: undefined, wholeSelection: undefined, wholeSelectionUnavailable: undefined,
      discover: this.discover.getSnapshot(), memory: {firstVisibleIndex: 0, offset: 0}, unfilteredTotal: undefined, error: undefined});
    this.listeners.clear();
  };
}
