import {browseCapabilitiesPath, browseMembershipId, parseBrowseCapabilities, browseContextFacetsPath, parseBrowseFacets, browseCountsPath, browseCountsRequest, parseBrowseCounts, parseBrowseNode,
  type BrowseCapabilities, type BrowseNode, type BrowseSortSelection, type BrowseWholeSelection, type BrowseWholeSelectionUnavailable} from '@core/browse.ts';
import {BrowseSession, browseSessionBuilder, browseSessionClosed, type BrowseSessionQuery} from '@core/browse-session.ts';
import {createWindowedCollection, type WindowedCollection} from '@core/collections/windowed.ts';
import type {ContentEntry, LibraryContentApi} from '@core/library-content.ts';
import {readReadingFacet} from '@core/browse-preparation.ts';
import {isStaleAnswer} from '@core/freshness-actions.ts';

export {withLibraryScope} from '@core/browse-session.ts';
export type BrowseMember = Readonly<{membershipId: string; entry: ContentEntry}>;

export type BrowseSource = Readonly<{libraryId: string; pivot: string; base?: readonly BrowseNode[]; predicates?: readonly BrowseNode[]; homeRowId?: string; sort?: BrowseSortSelection; sorts?: readonly BrowseSortSelection[]}>;
export type BrowseState = Readonly<{phase: 'loading' | 'ready' | 'error'; capabilities?: BrowseCapabilities; predicates: readonly BrowseNode[]; sort?: BrowseSortSelection; error?: unknown;
  /** Actual opaque page token, absent until this applied query has replied. */
  revision?: string; queryRevision: number}>;
function captureNode(input: BrowseNode): BrowseNode {
  const freeze = (node: BrowseNode): BrowseNode => {
    if ('field' in node) return Object.freeze({...node, value: Array.isArray(node.value) ? Object.freeze([...node.value]) : node.value});
    if ('all' in node) return Object.freeze({all: Object.freeze(node.all.map(freeze))});
    if ('any' in node) return Object.freeze({any: Object.freeze(node.any.map(freeze))});
    return Object.freeze({not: freeze(node.not)});
  };
  return freeze(parseBrowseNode(input));
}

/** Private screen controller. The parent owns the authenticated API and exact viewer lifetime. */
export class BrowseController {
  readonly collection: WindowedCollection<BrowseMember>;
  readonly source: BrowseSource;
  private state: BrowseState = Object.freeze({phase: 'loading', predicates: [], queryRevision: 0});
  private issued = new WeakSet<BrowseState>();
  private listeners = new Set<() => void>();
  private life = new AbortController();
  private epoch = 0;
  private sorts: readonly BrowseSortSelection[] = [];
  /** The applied query's page source; replaced, never reused, when the query changes. */
  private session?: BrowseSession<BrowseMember>;
  constructor(private api: LibraryContentApi, source: BrowseSource, private isCurrent: () => boolean) {
    this.source = Object.freeze({...source,
      ...(source.base ? {base: Object.freeze(source.base.map(captureNode))} : {}),
      ...(source.predicates ? {predicates: Object.freeze(source.predicates.map(captureNode))} : {}),
      ...(source.sort ? {sort: Object.freeze({...source.sort})} : {}),
      ...(source.sorts ? {sorts: Object.freeze(source.sorts.map(sort => Object.freeze({...sort})))} : {})});
    this.state = Object.freeze({phase: 'loading', predicates: this.source.predicates ?? Object.freeze([]), queryRevision: 0});
    this.collection = createWindowedCollection({pageSize: 60, maxResidentPages: 6, maxConcurrent: 1, prefetchPages: 1,
      keyOf: (item: BrowseMember) => item.membershipId, fetchPage: async () => { throw browseSessionClosed(); }});
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private current() { return !this.life.signal.aborted && this.isCurrent(); }
  /** Borrowed row/menu actions require the same current resident object.
   * Explicit selection has its own retained picks and operation lifetime. */
  contains(entry: ContentEntry): boolean {
    return this.current() && this.state.phase === 'ready' && !!this.state.revision && !!this.session?.owns(entry)
      && this.collection.residentItems().some(member => member.entry === entry);
  }
  /** The same title in the list this controller holds now. A press that crossed a re-read of the
   * list (a stale selection query re-browses it) acts on this, never silently on nothing. */
  resident(entry: ContentEntry): ContentEntry | undefined {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.revision) return undefined;
    return this.collection.residentItems().find(member => member.entry.id === entry.id && member.entry.libraryId === entry.libraryId)?.entry;
  }
  /** Resolves once the list is ready again, or after `ms`; never rejects. */
  settled(signal: AbortSignal, ms: number): Promise<void> {
    if (this.state.phase === 'ready' && this.state.revision) return Promise.resolve();
    return new Promise(resolve => {
      const done = () => {clearTimeout(timer); off(); signal.removeEventListener('abort', done); resolve();};
      const off = this.subscribe(() => {if (this.state.phase !== 'loading' && this.state.revision) done();});
      const timer = setTimeout(done, ms); signal.addEventListener('abort', done, {once: true});
    });
  }
  /** The server's exact whole-view descriptor for the page this controller holds.
   * It is present only while that same page revision is the current one; any
   * invalidate, retry or revision change drops it before a new one can arrive. */
  wholeSelection(): BrowseWholeSelection | undefined {
    return this.current() && this.state.phase === 'ready' && !!this.state.revision ? this.session?.wholeSelection() : undefined;
  }
  wholeSelectionUnavailable(): BrowseWholeSelectionUnavailable | undefined {
    return this.current() && this.state.phase === 'ready' && !!this.state.revision ? this.session?.wholeSelectionUnavailable() : undefined;
  }
  private publish(patch: Partial<BrowseState>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    this.issued.add(this.state);
    for (const listener of this.listeners) listener();
  }
  async load() {
    if (!this.current()) return;
    const epoch = ++this.epoch;
    this.session?.retire();
    this.session = undefined;
    this.publish({phase: 'loading', error: undefined, revision: undefined});
    try {
      const caps = parseBrowseCapabilities(await this.api.request(browseCapabilitiesPath(this.source.libraryId, this.source.pivot), 'GET', undefined, this.life.signal));
      if (!this.current() || epoch !== this.epoch) return;
      const pivot = caps.resolvedPivot ?? caps.pivots.find(pivot => pivot.id === this.source.pivot);
      this.sorts = this.source.sorts ?? (this.source.sort ? [this.source.sort] : pivot?.defaultSort ?? []);
      const sort = this.sorts[0];
      if (!pivot?.browsable || !pivot.supportedViews.some(view => view === 'grid' || view === 'list') || !sort || caps.queryLimits.maximumLimit < 60) throw new Error('browse-capability-required');
      this.publish({phase: 'ready', capabilities: caps, sort});
      if (!this.current() || epoch !== this.epoch) return;
      this.reset();
    } catch (error) { if (epoch === this.epoch) this.publish({phase: 'error', error}); }
  }
  acceptsQuery(original: BrowseState): boolean {
    return this.current() && this.issued.has(original) && original.phase === 'ready' && this.state.phase === 'ready'
      && this.state.queryRevision === original.queryRevision && this.state.capabilities === original.capabilities
      && this.state.predicates === original.predicates && this.state.sort === original.sort;
  }
  setQuery(predicates: readonly BrowseNode[], sort?: BrowseSortSelection, original: BrowseState = this.state): boolean {
    // A rendered control or delayed draft belongs to its original applied query.
    // Page/count publication may advance without replacing that query; another
    // query, capabilities or opening must never lend the callback its state.
    const current = () => this.acceptsQuery(original);
    if (!current() || !original.capabilities) return false;
    const chosen = sort ?? original.sort;
    if (!chosen) return false;
    const captured = Object.freeze(predicates.map(captureNode)), selected = chosen === original.sort ? chosen : Object.freeze({...chosen});
    this.builder(original.capabilities, captured, selected).build();
    // Capturing/validating caller-owned input cannot re-enter and then overwrite
    // a query accepted in the meantime.
    if (!current()) return false;
    if (selected !== original.sort) this.sorts = [selected];
    this.reset({predicates: captured, sort: selected});
    return true;
  }
  private sessionQuery(caps: BrowseCapabilities, predicates: readonly BrowseNode[], sort: BrowseSortSelection): BrowseSessionQuery {
    return {libraryId: this.source.libraryId, pivot: this.source.pivot, capabilities: caps, nodes: [...(this.source.base ?? []), ...predicates],
      sorts: sort === this.state.sort && this.sorts.length ? this.sorts : [sort], ...(this.source.homeRowId ? {homeRowId: this.source.homeRowId} : {})};
  }
  private builder(caps: BrowseCapabilities, predicates: readonly BrowseNode[], sort: BrowseSortSelection) {
    return browseSessionBuilder(this.sessionQuery(caps, predicates, sort)).limit(60);
  }
  /** One automatic re-read per query when its FIRST page is refused as stale (a 412
   * revision_mismatch while a library imports): nothing is shown yet, so nothing jumps. A second
   * refusal is shown as this library's error, never a loop. A stale continuation deeper in a list
   * that has already answered keeps the explicit refresh, so the viewer's place never resets. */
  private staleRetried = false;
  private reset(next: Partial<Pick<BrowseState, 'predicates' | 'sort'>> = {}, automatic = false) {
    if (!automatic) this.staleRetried = false;
    const {capabilities: caps, predicates, sort} = {...this.state, ...next};
    if (!caps || !sort) return;
    const epoch = ++this.epoch;
    this.session?.retire();
    this.session = undefined;
    this.publish({...next, revision: undefined, queryRevision: this.state.queryRevision + 1});
    if (!this.current() || epoch !== this.epoch) return;
    const session = new BrowseSession<BrowseMember>({query: this.sessionQuery(caps, predicates, sort),
      request: (path, method, body, signal) => this.api.request(path, method, body, signal),
      ...(this.api.requestRaw ? {requestRaw: this.api.requestRaw.bind(this.api)} : {}),
      current: () => this.current() && epoch === this.epoch,
      item: (entry, page, index) => Object.freeze({membershipId: browseMembershipId(page, index)!, entry}),
      onRevision: revision => this.publish({revision}),
      onWhole: () => this.publish({}),
      onWholeUnavailable: () => this.publish({}),
      onPreparationError: error => this.publish({error}),
      onStale: error => {
        if (!this.staleRetried && this.state.revision === undefined && this.current() && epoch === this.epoch) {this.staleRetried = true; this.reset({}, true); return;}
        this.publish({revision: undefined, error});
      }});
    this.session = session;
    this.collection.invalidate(session.fetchPage);
    this.collection.ensureRange(0, 0);
  }
  async facets(field: string, query: string, predicates: readonly BrowseNode[], signal: AbortSignal) {
    const {capabilities: caps, sort} = this.state;
    if (!caps || !sort || !this.current()) throw browseSessionClosed();
    const epoch = this.epoch, body = this.builder(caps, predicates, sort).build();
    const path = browseContextFacetsPath(this.source.libraryId, field, this.source.pivot, body.query, query, 200);
    const reading = caps.library.kind === 'book' || caps.library.kind === 'comic';
    const page = reading ? await readReadingFacet(this.api, this.source.libraryId, path, field, {signal, isCurrent: () => this.current() && epoch === this.epoch})
      : parseBrowseFacets(await this.api.request(path, 'GET', undefined, signal), field);
    if (!this.current() || epoch !== this.epoch || signal.aborted) throw browseSessionClosed();
    return page;
  }
  async count(predicates: readonly BrowseNode[], signal: AbortSignal) {
    return (await this.counts([{id: 'draft', predicates}], signal))[0];
  }
  async counts(groups: readonly Readonly<{id: string; predicates: readonly BrowseNode[]}>[], signal: AbortSignal) {
    const {capabilities: caps, sort} = this.state;
    if (!caps || !sort || !this.current()) throw browseSessionClosed();
    const epoch = this.epoch;
    const queries = groups.map(group => ({id: group.id, query: this.builder(caps, group.predicates, sort).build().query ?? null}));
    const raw = await this.api.request(browseCountsPath(this.source.libraryId), 'POST', browseCountsRequest(this.source.pivot, queries), signal);
    if (!this.current() || epoch !== this.epoch || signal.aborted) throw browseSessionClosed();
    return parseBrowseCounts(raw, groups.map(group => group.id));
  }
  /** The same server query used by pages, counts, saved views and whole-group Play. */
  query() {
    const {capabilities, predicates, sort} = this.state;
    if (!this.current() || !capabilities || !sort) throw browseSessionClosed();
    return this.builder(capabilities, predicates, sort).build();
  }
  /** Fixed source membership is the default, not a user-applied filter. */
  canSaveView() {
    const {capabilities, predicates, sort, phase} = this.state;
    if (!this.current() || phase !== 'ready' || !capabilities || !sort) return false;
    const pivot = capabilities.resolvedPivot ?? capabilities.pivots.find(value => value.id === this.source.pivot);
    if (!pivot) return false;
    return predicates.length > 0 || this.sorts.length !== pivot.defaultSort.length || this.sorts.some((value, index) => {
      const base = pivot.defaultSort[index];
      return !base || value.field !== base.field || value.direction !== base.direction || value.seed !== base.seed;
    });
  }
  /** UI controls preserve compound predicates and validate every generated node. */
  parseNode = (value: unknown) => parseBrowseNode(value);
  retry = () => {
    this.session?.forgetWholeSelection();
    if (this.state.phase === 'error') void this.load();
    else if (isStaleAnswer(this.collection.getSnapshot().error)) this.reset();
    else this.collection.retry();
  };
  dispose() { this.life.abort(); this.epoch++; this.session?.retire(); this.collection.dispose(); this.listeners.clear(); }
}
