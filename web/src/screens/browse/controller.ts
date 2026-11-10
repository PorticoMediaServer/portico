import {readBrowseCapabilitiesResponse, readBrowseCountsResponse, readBrowseFacetResponse, readReadingBrowseFacet} from '@core/query/browse-reads.ts';
import {browseMembershipId, parseBrowseCapabilities, parseBrowseFacets, parseBrowseCounts, parseBrowseNode, completeBrowseNode,
  type BrowseCapabilities, type BrowseNode, type BrowseSortSelection, type BrowseWholeSelection, type BrowseWholeSelectionUnavailable} from '@core/browse.ts';
import {BrowseSession, browseSessionBuilder, browseSessionClosed, type BrowseSessionQuery} from '@core/browse-session.ts';
import {createWindowedCollection, type WindowedCollection} from '@core/collections/windowed.ts';
import type {ContentEntry, LibraryContentApi} from '@core/library-content.ts';
import {isStaleAnswer} from '@core/freshness-actions.ts';
import {listedTitlesWithPersonalState, readsPersonalState, titleIdsOf, type FreshDocument} from '@core/freshness-hub.ts';
import type {ViewerQueries} from '@core/query/viewer-queries.ts';

export {withLibraryScope} from '@core/browse-session.ts';
export type BrowseMember = Readonly<{membershipId: string; entry: ContentEntry}>;

export type BrowseSource = Readonly<{libraryId: string; pivot: string; base?: readonly BrowseNode[]; predicates?: readonly BrowseNode[]; homeRowId?: string; sort?: BrowseSortSelection; sorts?: readonly BrowseSortSelection[];
  /** The order this tab opens in when it is not the pivot's (Recently aired). Save view compares against it. */
  defaultSorts?: readonly BrowseSortSelection[]}>;
export type BrowseState = Readonly<{phase: 'loading' | 'ready' | 'error'; capabilities?: BrowseCapabilities; predicates: readonly BrowseNode[]; sort?: BrowseSortSelection; error?: unknown;
  /** Actual opaque page token, absent until this applied query has replied. */
  revision?: string; queryRevision: number}>;
/** A grid's filters and order: what the page's address carries. */
export type BrowseQuery = Readonly<{predicates: readonly BrowseNode[]; sorts: readonly BrowseSortSelection[]}>;
function captureNode(input: BrowseNode): BrowseNode {
  const freeze = (node: BrowseNode): BrowseNode => {
    if ('field' in node) return Object.freeze({...node, value: Array.isArray(node.value) ? Object.freeze([...node.value]) : node.value});
    if ('all' in node) return Object.freeze({variant: 'group' as const, all: Object.freeze(node.all.map(freeze))});
    if ('any' in node) return Object.freeze({variant: 'group' as const, any: Object.freeze(node.any.map(freeze))});
    return Object.freeze({variant: 'group' as const, not: freeze(node.not)});
  };
  return freeze(parseBrowseNode(completeBrowseNode(input)));
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
  /** The page whose address owns this grid's query, when one does (LB-1). */
  private route?: (query: BrowseQuery) => void;
  /** The session a quiet re-read is replacing: its rows stay the shown ones until the new session answers. */
  private replacing?: BrowseSession<BrowseMember>;
  /** Keeps the grid current on the viewer's hub while it is cached, read in place while it is shown. */
  private freshness?: FreshDocument;
  constructor(private api: LibraryContentApi, source: BrowseSource, private isCurrent: () => boolean) {
    this.source = Object.freeze({...source,
      ...(source.base ? {base: Object.freeze(source.base.map(captureNode))} : {}),
      ...(source.predicates ? {predicates: Object.freeze(source.predicates.map(captureNode))} : {}),
      ...(source.sort ? {sort: Object.freeze({...source.sort})} : {}),
      ...(source.sorts ? {sorts: Object.freeze(source.sorts.map(sort => Object.freeze({...sort})))} : {}),
      ...(source.defaultSorts ? {defaultSorts: Object.freeze(source.defaultSorts.map(sort => Object.freeze({...sort})))} : {})});
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
    return this.current() && this.state.phase === 'ready' && !!this.state.revision && (!!this.session?.owns(entry) || !!this.replacing?.owns(entry))
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
      const caps = parseBrowseCapabilities(await readBrowseCapabilitiesResponse(this.api, this.source.libraryId, this.source.pivot, this.life.signal));
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
    const sorts = selected === original.sort && this.sorts.length ? this.sorts : [selected];
    // A page whose address owns the query is asked to move there; the grid follows the address.
    if (this.route) {this.route({predicates: captured, sorts}); return true;}
    this.sorts = sorts;
    this.reset({predicates: captured, sort: selected});
    return true;
  }
  /**
   * The page's address owns this grid's filters and order: the toolbar's changes are sent to it
   * (a history replace), and the grid follows the address with `follow`, never the other way
   * round. Returns the release.
   */
  routeQueries(route: (query: BrowseQuery) => void): () => void {
    this.route = route;
    return () => {if (this.route === route) this.route = undefined;};
  }
  /**
   * The grid is a cached value that outlives its screen: the hub keeps it current. While it is
   * shown (`show`), a title it holds, its library's content or order, or (for a query that reads
   * it) this profile's state moving re-reads its visible pages in place; while it is not, the move
   * is remembered and read when it is shown again. Ends with `dispose`.
   */
  keepCurrent(queries: ViewerQueries): void {
    if (this.freshness || !this.current()) return;
    const library = this.source.libraryId === '*' ? undefined : this.source.libraryId;
    this.freshness = queries.keepCurrent({
      dependencies: () => listedTitlesWithPersonalState(library, titleIdsOf(this.collection.residentItems().map(member => member.entry)),
        readsPersonalState(this.fields())),
      // Held (a selection is open on it): a move is remembered and read once the hold ends.
      hasContent: () => this.state.phase === 'ready' && !!this.state.revision && this.holds === 0,
      revalidate: context => this.revalidate(context.settle),
    });
  }
  /** On screen now; returns the hide. A grid shown again after its opening failed tries again. */
  show(): () => void {
    const document = this.freshness;
    document?.setOpen(true);
    if (this.state.phase === 'error') void this.load();
    return () => document?.setOpen(false);
  }
  isDisposed(): boolean { return this.life.signal.aborted; }
  private holds = 0;
  /** Keep the shown pages as they are (a selection is open over them; reading them again would
   *  start a new page revision and end it). Returns the release; a move meanwhile is read then. */
  hold(): () => void {
    this.holds++;
    let held = true;
    return () => {if (!held) return; held = false; if (--this.holds === 0) this.freshness?.check();};
  }
  /** Every field the applied query filters or sorts on. */
  private fields(): string[] {
    const out: string[] = this.sorts.map(sort => sort.field);
    const visit = (node: BrowseNode): void => {
      if ('field' in node) out.push(node.field);
      else if ('all' in node) node.all.forEach(visit);
      else if ('any' in node) node.any.forEach(visit);
      else visit(node.not);
    };
    [...(this.source.base ?? []), ...this.state.predicates].forEach(visit);
    return out;
  }
  /**
   * Reads the visible pages again under a fresh session and shows them in place, keeping the
   * scroll. A failed read leaves the grid and its session as they were (the hub tries again later);
   * a query change in the meantime makes it moot.
   */
  async revalidate(hold?: () => Promise<void>): Promise<void> {
    const {capabilities: caps, predicates, sort, phase} = this.state;
    if (!this.current() || phase !== 'ready' || !caps || !sort || !this.state.revision || !this.session) return;
    const epoch = this.epoch, previous = this.session;
    const next = this.openSession(caps, predicates, sort, epoch);
    this.replacing = previous;
    this.session = next;
    try {
      await this.collection.revalidate({fetchPage: next.fetchPage, ...(hold ? {hold} : {})});
      if (this.session === next) previous.retire();
    } catch (error) {
      if (this.session === next) {next.retire(); this.session = previous;}
      throw error;
    } finally {if (this.replacing === previous) this.replacing = undefined;}
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
    const session = this.openSession(caps, predicates, sort, epoch);
    this.session = session;
    this.collection.invalidate(session.fetchPage);
    this.collection.ensureRange(0, 0);
  }
  private openSession(caps: BrowseCapabilities, predicates: readonly BrowseNode[], sort: BrowseSortSelection, epoch: number): BrowseSession<BrowseMember> {
    const session: BrowseSession<BrowseMember> = new BrowseSession<BrowseMember>({query: this.sessionQuery(caps, predicates, sort),
      request: (path, method, body, signal) => this.api.request(path, method, body, signal),
      current: () => this.current() && epoch === this.epoch && (this.session === session || this.replacing === session),
      item: (entry, page, index) => Object.freeze({membershipId: browseMembershipId(page, index)!, entry}),
      onRevision: revision => this.publish({revision}),
      onWhole: () => this.publish({}),
      onWholeUnavailable: () => this.publish({}),
      onStale: error => {
        if (!this.staleRetried && this.state.revision === undefined && this.current() && epoch === this.epoch) {this.staleRetried = true; this.reset({}, true); return;}
        this.publish({revision: undefined, error});
      }});
    return session;
  }
  async facets(field: string, query: string, predicates: readonly BrowseNode[], signal: AbortSignal) {
    const {capabilities: caps, sort} = this.state;
    if (!caps || !sort || !this.current()) throw browseSessionClosed();
    const epoch = this.epoch, body = this.builder(caps, predicates, sort).build();
    const reading = caps.library.kind === 'book' || caps.library.kind === 'comic';
    const page = reading ? await readReadingBrowseFacet(this.api, this.source.libraryId, field, this.source.pivot, body.query, query, 200, {signal, isCurrent: () => this.current() && epoch === this.epoch})
      : parseBrowseFacets(await readBrowseFacetResponse(this.api, this.source.libraryId, field, this.source.pivot, body.query, query, 200, signal), field);
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
    const raw = await readBrowseCountsResponse(this.api, this.source.libraryId, this.source.pivot, queries, signal);
    if (!this.current() || epoch !== this.epoch || signal.aborted) throw browseSessionClosed();
    return parseBrowseCounts(raw, groups.map(group => group.id));
  }
  /** The same server query used by pages, counts, saved views and whole-group Play. */
  query() {
    const {capabilities, predicates, sort} = this.state;
    if (!this.current() || !capabilities || !sort) throw browseSessionClosed();
    return this.builder(capabilities, predicates, sort).build();
  }
  /** Fixed source membership is the default, not a user-applied filter, and the tab's own opening
   * order is its default, not a change (LB-3: Recently aired opens newest first). */
  canSaveView() {
    const {capabilities, predicates, sort, phase} = this.state;
    if (!this.current() || phase !== 'ready' || !capabilities || !sort) return false;
    const pivot = capabilities.resolvedPivot ?? capabilities.pivots.find(value => value.id === this.source.pivot);
    if (!pivot) return false;
    const defaults = this.source.defaultSorts ?? pivot.defaultSort;
    return predicates.length > 0 || this.sorts.length !== defaults.length || this.sorts.some((value, index) => {
      const base = defaults[index];
      return !base || value.field !== base.field || value.direction !== base.direction || value.seed !== base.seed;
    });
  }
  /** UI controls preserve compound predicates and validate every generated node. */
  parseNode = (value: unknown) => parseBrowseNode(completeBrowseNode(value));
  retry = () => {
    this.session?.forgetWholeSelection();
    if (this.state.phase === 'error') void this.load();
    else if (isStaleAnswer(this.collection.getSnapshot().error)) this.reset();
    else this.collection.retry();
  };
  dispose() { this.route = undefined; this.freshness?.dispose(); this.freshness = undefined; this.life.abort(); this.epoch++; this.session?.retire(); this.replacing?.retire(); this.replacing = undefined; this.collection.dispose(); this.listeners.clear(); }
}
