import {BrowseQueryBuilder, browseCapabilitiesPath, browseContextFacetsPath, browseCountsPath, browseCountsRequest, browseMembershipId, browsePath, parseBrowseCapabilities,
  parseBrowseCounts, parseBrowseFacets, parseBrowseResult, type BrowseCapabilities, type BrowseCount, type BrowseNode, type BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {createWindowedCollection, type WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {fetchHomeRow, type HomeApi, type HomeRow} from '../../../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {acrossCondition, acrossPivot, groupSort, homeRowQuery, type WholeGroupQuery} from '../../../../../packages/client-core/src/presentation/browse-groups';

export type NativeHomeRowItem = Readonly<{entry: ContentEntry; membershipId: string}>;
export type NativeHomeRowKind = Readonly<{id: string; libraryIds: readonly string[]}>;
export type NativeHomeRowSnapshot = Readonly<{
  phase: 'loading' | 'ready' | 'error'; row?: HomeRow; capabilities?: BrowseCapabilities;
  sort?: BrowseSortSelection; filters: readonly BrowseNode[]; selectedKind?: string;
  counts: readonly BrowseCount[]; error?: unknown;
}>;
const cancelled = () => Object.assign(new Error('native_home_row_retired'), {name: 'AbortError'});
const stale = () => Object.assign(new Error('native_home_row_changed'), {code: 'stale_continuation'});

/** One genuine Home row across the authorized libraries. The server evaluates
 * membership, order, filters, display targets and counts; memory stays sparse. */
export class NativeHomeRowController {
  readonly collection: WindowedCollection<NativeHomeRowItem>;
  /** The chips this result may lawfully offer. A row the server bounded to one
   * library narrows to that library once the row is read. */
  kinds: readonly NativeHomeRowKind[];
  private readonly allKinds: readonly NativeHomeRowKind[];
  private readonly api: HomeApi;
  readonly rowId: string;
  private readonly current: () => boolean;
  private readonly viewerFence: string;
  private state: NativeHomeRowSnapshot = Object.freeze({phase: 'loading', filters: [], counts: []});
  private listeners = new Set<() => void>();
  private abort = new AbortController();
  private generation = 0;
  private alive = true;
  constructor(options: {api: HomeApi; rowId: string; viewerFence: string; current: () => boolean; kinds: readonly NativeHomeRowKind[]}) {
    this.api = options.api; this.rowId = options.rowId; this.viewerFence = options.viewerFence; this.current = options.current;
    this.allKinds = Object.freeze(options.kinds.filter(kind => kind.libraryIds.length).map(kind => Object.freeze({id: kind.id, libraryIds: Object.freeze([...kind.libraryIds])})));
    this.kinds = this.allKinds;
    this.collection = createWindowedCollection({pageSize: 60, maxResidentPages: 6, maxConcurrent: 1,
      keyOf: item => item.membershipId, fetchPage: async () => {throw cancelled();}});
  }
  getSnapshot = (): NativeHomeRowSnapshot => this.state;
  isCurrentItem = (item: NativeHomeRowItem, index: number): boolean => this.valid() && this.state.phase === 'ready' && this.collection.itemAt(index) === item;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private valid(generation = this.generation): boolean {return this.alive && this.current() && generation === this.generation;}
  private publish(patch: Partial<NativeHomeRowSnapshot>): void {
    if (!this.valid()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  private async request(path: string, method = 'GET', body?: unknown, signal = this.abort.signal): Promise<unknown> {
    const generation = this.generation;
    if (!this.valid(generation) || signal.aborted) throw cancelled();
    const result = await this.api.request(path, method, body, signal);
    if (!this.valid(generation) || signal.aborted) throw cancelled();
    return result;
  }
  load = async (): Promise<void> => {
    if (!this.alive || !this.current()) return;
    this.abort.abort(); this.abort = new AbortController(); const generation = ++this.generation;
    this.publish({phase: 'loading', error: undefined});
    try {
      const [raw, row] = await Promise.all([this.request(browseCapabilitiesPath('*', acrossPivot('homeRow'))), fetchHomeRow(this.api, this.rowId, {limit: 60}, this.abort.signal)]);
      if (!this.valid(generation)) return;
      if (row.id !== this.rowId || row.viewerFence !== this.viewerFence) throw stale();
      const capabilities = parseBrowseCapabilities(raw), pivot = capabilities.pivots.find(value => value.id === acrossPivot('homeRow'));
      if (!pivot?.defaultSort[0]) throw stale();
      const sort = groupSort(this.state.sort, true, capabilities.sorts, pivot.defaultSort[0]);
      // A library-scoped row belongs to that one library: offering every other
      // library's chip would promise counts and pages the row cannot contain.
      const scoped = row.libraryId ? this.allKinds.filter(kind => kind.id === row.libraryId) : this.allKinds;
      this.kinds = scoped.length ? scoped : this.allKinds;
      const selectedKind = this.kinds.some(kind => kind.id === this.state.selectedKind) ? this.state.selectedKind : undefined;
      this.publish({phase: 'ready', row, capabilities, sort, ...(selectedKind === this.state.selectedKind ? {} : {selectedKind})});
      this.requery();
    } catch (error) {if (this.valid(generation)) this.publish({phase: 'error', error});}
  };
  private query(selected = true, filters = this.state.filters): BrowseNode {
    const kind = selected ? this.kinds.find(value => value.id === this.state.selectedKind) : undefined;
    return {all: [acrossCondition('homeRow', this.rowId), ...filters, ...(kind ? [{field: 'library' as const, operator: 'in' as const, value: kind.libraryIds}] : [])]};
  }
  private builder(query = this.query()): BrowseQueryBuilder {
    if (!this.state.capabilities || !this.state.sort) throw cancelled();
    return new BrowseQueryBuilder(this.state.capabilities, acrossPivot('homeRow')).add(query).sort(this.state.sort.field, this.state.sort.direction, this.state.sort.seed).limit(60).displayHomeRow(this.rowId);
  }
  private requery(): void {
    if (!this.valid() || this.state.phase !== 'ready') return;
    const generation = this.generation, query = this.query(), sort = this.state.sort!, capabilities = this.state.capabilities!;
    let revision: string | undefined;
    this.collection.invalidate(async (start, count, signal) => {
      if (!this.valid(generation) || count > 60) throw cancelled();
      const body = new BrowseQueryBuilder(capabilities, acrossPivot('homeRow')).add(query).sort(sort.field, sort.direction, sort.seed)
        .limit(Math.min(60, count)).displayHomeRow(this.rowId).range({start, ...(revision ? {revision} : {})}).build();
      const page = parseBrowseResult(await this.request(browsePath('*'), 'POST', body, signal));
      if (!this.valid(generation) || page.pageInfo.start !== start || page.pivot !== acrossPivot('homeRow') || page.entries.length > 0 && !page.displayTargets || revision && revision !== page.pageInfo.revision) throw stale();
      revision = page.pageInfo.revision;
      return {items: page.entries.map((entry, index) => {
        const membershipId = browseMembershipId(page, index); if (!membershipId) throw stale();
        return Object.freeze({entry, membershipId});
      }), total: page.pageInfo.total, totalIsLowerBound: page.pageInfo.totalIsLowerBound,
        positionIndex: Object.fromEntries(page.positionIndex.map(anchor => [anchor.key, anchor.index]))};
    });
    void this.readCounts(generation);
  }
  facets = async (field: string, search = '', filters = this.state.filters) => {
    const offered = this.state.capabilities?.fields.some(value => value.id === field || value.facetSource?.field === field || field === 'decade' && value.id === 'year');
    if (!offered || this.state.phase !== 'ready') throw cancelled();
    const query = this.query(true, filters), stamp = JSON.stringify(this.query());
    const raw = await this.request(browseContextFacetsPath('*', field, acrossPivot('homeRow'), query, search, 200));
    if (stamp !== JSON.stringify(this.query())) throw cancelled();
    return parseBrowseFacets(raw, field);
  };
  /** Draft previews never change the retained grid or its committed filters. */
  previewCounts = async (filters: readonly BrowseNode[], signal = this.abort.signal): Promise<readonly BrowseCount[]> => {
    if (!this.valid() || this.state.phase !== 'ready') throw cancelled();
    this.builder(this.query(true, filters)).build();
    const base = this.query(false, filters), generation = this.generation, stamp = JSON.stringify(this.query());
    const queries = [{id: 'all', query: base}, ...this.kinds.map(kind => ({id: kind.id, query: {all: [base, {field: 'library' as const, operator: 'in' as const, value: kind.libraryIds}]}}))];
    const counts: BrowseCount[] = [];
    for (let start = 0; start < queries.length; start += 8) {
      const batch = queries.slice(start, start + 8);
      const raw = await this.request(browseCountsPath('*'), 'POST', browseCountsRequest(acrossPivot('homeRow'), batch), signal);
      if (!this.valid(generation) || stamp !== JSON.stringify(this.query())) throw cancelled();
      counts.push(...parseBrowseCounts(raw, batch.map(query => query.id)));
    }
    return Object.freeze(counts);
  };
  setSort = (sort: BrowseSortSelection): boolean => {
    if (!this.valid() || !this.state.capabilities) return false;
    new BrowseQueryBuilder(this.state.capabilities, acrossPivot('homeRow')).add(this.query()).sort(sort.field, sort.direction, sort.seed).limit(60).displayHomeRow(this.rowId).build();
    this.publish({sort: Object.freeze({...sort}), counts: [], error: undefined}); this.requery(); return true;
  };
  setFilters = (filters: readonly BrowseNode[]): boolean => {
    if (!this.valid() || this.state.phase !== 'ready') return false;
    const query = this.query(true, filters); this.builder(query).build();
    this.publish({filters: Object.freeze([...filters]), counts: [], error: undefined}); this.requery(); return true;
  };
  selectKind = (id: string | undefined): boolean => {
    if (!this.valid() || this.state.phase !== 'ready' || id !== undefined && !this.kinds.some(kind => kind.id === id)) return false;
    this.publish({selectedKind: id, error: undefined}); this.requery(); return true;
  };
  private countTicket = 0;
  private async readCounts(generation: number): Promise<void> {
    const ticket = ++this.countTicket, base = this.query(false);
    const queries = [{id: 'all', query: base}, ...this.kinds.map(kind => ({id: kind.id, query: {all: [base, {field: 'library' as const, operator: 'in' as const, value: kind.libraryIds}]}}))];
    try {
      const counts: BrowseCount[] = [];
      for (let start = 0; start < queries.length; start += 8) {
        const batch = queries.slice(start, start + 8);
        const raw = await this.request(browseCountsPath('*'), 'POST', browseCountsRequest(acrossPivot('homeRow'), batch));
        if (!this.valid(generation) || ticket !== this.countTicket) return;
        counts.push(...parseBrowseCounts(raw, batch.map(query => query.id)));
      }
      this.publish({counts: Object.freeze(counts)});
    } catch (error) {if (this.valid(generation) && ticket === this.countTicket) this.publish({error});}
  }
  wholeGroup = (): WholeGroupQuery | undefined => {
    if (!this.valid() || this.state.phase !== 'ready' || !this.state.row || !this.state.sort) return;
    const source = homeRowQuery(this.rowId, this.state.row.entries.map(entry => entry.kind));
    return Object.freeze({...source, query: this.query(), sort: Object.freeze([this.state.sort])});
  };
  dispose = (): void => {this.alive = false; ++this.generation; ++this.countTicket; this.abort.abort(); this.collection.dispose(); this.listeners.clear();};
}
