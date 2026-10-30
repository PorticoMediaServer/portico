import {readReadingFacet} from '../../../../../packages/client-core/src/browse-preparation';
import {BrowseQueryBuilder, browseContextFacetsPath, browseCountsPath, browseCountsRequest, parseBrowseCounts, parseBrowseFacets,
  type BrowseCapabilities, type BrowseCount, type BrowseFacetPage, type BrowseNode, type BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {filterControlOf, filterValuesField} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';

type ReadState<T> = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; value?: T; error?: unknown}>;
export type NativeLibraryFacet = ReadState<BrowseFacetPage> & Readonly<{search: string}>;
export type NativeLibraryFilterSnapshot = Readonly<{
  open: boolean;
  predicates: readonly BrowseNode[];
  count: ReadState<BrowseCount>;
  facets: Readonly<Record<string, NativeLibraryFacet>>;
}>;
type Options = Readonly<{
  api: LibraryContentApi; capabilities: BrowseCapabilities; pivot: string; sort: BrowseSortSelection;
  predicates: readonly BrowseNode[]; fixed: readonly BrowseNode[];
  current: () => boolean; /** `keep` names the docked panel that must survive its own apply. */ apply: (predicates: readonly BrowseNode[], keep?: NativeLibraryFilterDraft) => void; onDispose: () => void;
}>;

/** A panel-local draft of one actual Library query. It never reads result pages
 * for counts, walks a facet directory, or changes the mounted grid before Apply. */
export class NativeLibraryFilterDraft {
  private state: NativeLibraryFilterSnapshot;
  private disposed = false;
  private revision = 0;
  private readonly listeners = new Set<() => void>();
  private countRead?: AbortController;
  private readonly facetReads = new Map<string, AbortController>();
  private readonly opened = new Set<string>();
  private timer?: ReturnType<typeof setTimeout>;
  private readonly options: Options;
  constructor(options: Options) {
    this.options = options;
    this.state = Object.freeze({open: true, predicates: options.predicates, count: {phase: 'idle' as const}, facets: {}});
  }
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = (): NativeLibraryFilterSnapshot => {this.current(); return this.state;};
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeLibraryFilterSnapshot>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  private query(predicates = this.state.predicates) {
    const builder = new BrowseQueryBuilder(this.options.capabilities, this.options.pivot);
    for (const node of predicates) builder.add(node);
    return builder.sort(this.options.sort.field, this.options.sort.direction, this.options.sort.seed).limit(1).build().query;
  }
  setPredicates = (predicates: readonly BrowseNode[]) => {
    if (!this.current()) return;
    const fixed = this.options.fixed, keys = new Set(fixed.map(node => JSON.stringify(node)));
    const next = Object.freeze([...fixed, ...predicates.filter(node => !keys.has(JSON.stringify(node)))]);
    this.query(next); // Refuse anything outside the current published vocabulary before I/O.
    this.revision++; this.cancelReads();
    const facets = Object.fromEntries([...this.opened].map(id => [id, {...this.state.facets[id], phase: 'loading' as const, search: this.state.facets[id]?.search ?? ''}]));
    this.publish({predicates: next, count: {phase: 'loading'}, facets});
    this.timer = setTimeout(() => {this.timer = undefined; this.refresh();}, 180);
  };
  clear = () => this.setPredicates(this.options.fixed);
  /** A count is refreshed on opening and after a debounced edit, from the
   * dedicated exact count producer. Full contextual queries go to facets. */
  refresh = () => {
    if (!this.current()) return;
    clearTimeout(this.timer); this.timer = undefined;
    void this.readCount();
    for (const id of this.opened) void this.readFacet(id, this.state.facets[id]?.search ?? '');
  };
  private async readCount() {
    this.countRead?.abort(); const controller = new AbortController(), revision = this.revision; this.countRead = controller;
    this.publish({count: {phase: 'loading'}});
    try {
      const body = browseCountsRequest(this.options.pivot, [{id: 'draft', query: this.query() ?? null}]);
      const raw = await this.options.api.request<unknown>(browseCountsPath(this.options.capabilities.library.id), 'POST', body, controller.signal);
      if (!this.current() || revision !== this.revision || controller.signal.aborted) return;
      this.publish({count: {phase: 'ready', value: parseBrowseCounts(raw, ['draft'])[0]!}});
    } catch (error) {if (this.current() && revision === this.revision && !controller.signal.aborted) this.publish({count: {phase: 'error', error}});}
  }
  setOpenFields = (ids: readonly string[]) => {
    if (!this.current()) return;
    for (const id of this.opened) if (!ids.includes(id)) {this.opened.delete(id); this.facetReads.get(id)?.abort(); this.facetReads.delete(id);}
    for (const id of ids) {
      if (this.opened.has(id)) continue;
      const field = this.options.capabilities.fields.find(value => value.id === id);
      if (!field || filterControlOf(field) !== 'values') continue;
      this.opened.add(id); void this.readFacet(id, this.state.facets[id]?.search ?? '');
    }
  };
  search = (id: string, query: string) => {if (this.current() && this.opened.has(id)) void this.readFacet(id, query);};
  retryFacet = (id: string) => {if (this.current() && this.opened.has(id)) void this.readFacet(id, this.state.facets[id]?.search ?? '');};
  private async readFacet(id: string, search: string) {
    const field = this.options.capabilities.fields.find(value => value.id === id);
    if (!field || !this.current() || !this.opened.has(id)) return;
    const facet = filterValuesField(field).facet;
    this.facetReads.get(id)?.abort(); const controller = new AbortController(), revision = this.revision; this.facetReads.set(id, controller);
    const publish = (value: NativeLibraryFacet) => this.publish({facets: Object.freeze({...this.state.facets, [id]: value})});
    const previous = this.state.facets[id]?.value;
    publish({phase: 'loading', search, value: previous});
    try {
      const path = browseContextFacetsPath(this.options.capabilities.library.id, facet, this.options.pivot, this.query(), search, 200);
      const reading = this.options.capabilities.library.kind === 'book' || this.options.capabilities.library.kind === 'comic';
      const page = reading ? await readReadingFacet(this.options.api, this.options.capabilities.library.id, path, facet,
        {signal: controller.signal, isCurrent: () => this.current() && revision === this.revision})
        : parseBrowseFacets(await this.options.api.request<unknown>(path, 'GET', undefined, controller.signal), facet);
      if (!this.current() || revision !== this.revision || controller.signal.aborted) return;
      publish({phase: 'ready', search, value: page});
    } catch (error) {if (this.current() && revision === this.revision && !controller.signal.aborted) publish({phase: 'error', search, error, value: previous});}
  }
  apply = (): boolean => {
    if (!this.current() || this.state.count.phase !== 'ready') return false;
    this.options.apply(this.state.predicates); this.dispose(); return true;
  };
  /** tablet-read#1b: the docked panel is the live editor, so its real query goes
   * to the mounted grid now and the panel keeps its own count and facet reads.
   * The panel's `current` binding never depends on the grid's predicate identity. */
  applyLive = (): void => {
    if (!this.current()) return;
    this.query(this.state.predicates); // Same vocabulary refusal as the modal Apply.
    this.options.apply(this.state.predicates, this);
  };
  private cancelReads() {
    clearTimeout(this.timer); this.timer = undefined; this.countRead?.abort();
    for (const controller of this.facetReads.values()) controller.abort(); this.facetReads.clear();
  }
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true; this.revision++; this.cancelReads(); this.opened.clear();
    this.state = Object.freeze({open: false, predicates: [], count: {phase: 'idle' as const}, facets: {}});
    for (const listener of [...this.listeners]) listener(); this.listeners.clear(); this.options.onDispose();
  };
}
