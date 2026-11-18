import {type BrowseCapabilities, type BrowseCount, type BrowseNode, type BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {BrowseSession, browseSessionBuilder, browseSessionClosed} from '../../../../../packages/client-core/src/browse-session';
import {createWindowedCollection, type WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {fetchHomeRow, type HomeApi, type HomeRow} from '../../../../../packages/client-core/src/home';
import {countHomeRow, hasHomeRowQueue, homeRowDefaultSort, homeRowPivot, homeRowSource, isRecentHomeRow, readHomeRowCapabilities, readHomeRowFacetChoices} from '../../../../../packages/client-core/src/home-row-browse';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';

export type NativeHomeRowItem = Readonly<{entry: ContentEntry; membershipId: string}>;
export type NativeHomeRowKind = Readonly<{id: string; libraryIds: readonly string[]}>;
export type NativeHomeRowSnapshot = Readonly<{
  phase: 'loading' | 'ready' | 'error'; row?: HomeRow; capabilities?: BrowseCapabilities;
  sort?: BrowseSortSelection; filters: readonly BrowseNode[]; selectedKind?: string;
  counts: readonly BrowseCount[]; error?: unknown;
}>;

/** One visible Home row with a separate browse session for each applied query. */
export class NativeHomeRowController {
  readonly collection: WindowedCollection<NativeHomeRowItem>;
  kinds: readonly NativeHomeRowKind[] = [];
  private readonly api: HomeApi;
  readonly rowId: string;
  private libraryId?: string;
  private state: NativeHomeRowSnapshot = Object.freeze({phase: 'loading', filters: [], counts: []});
  private listeners = new Set<() => void>();
  private abort = new AbortController();
  private session?: BrowseSession<NativeHomeRowItem>;
  private alive = true;
  constructor(private options: {api: HomeApi; rowId: string; libraryId?: string; current: () => boolean; kinds: readonly NativeHomeRowKind[]}) {
    this.api = options.api; this.rowId = options.rowId; this.libraryId = options.libraryId;
    this.kinds = !this.libraryId && isRecentHomeRow(this.rowId) ? options.kinds : [];
    this.collection = createWindowedCollection({pageSize: 60, maxResidentPages: 6, maxConcurrent: 1,
      keyOf: item => item.membershipId, fetchPage: async () => {throw browseSessionClosed();}});
  }
  getSnapshot = (): NativeHomeRowSnapshot => this.state;
  isCurrentItem = (item: NativeHomeRowItem, index: number): boolean => this.current() && this.state.phase === 'ready' && this.collection.itemAt(index) === item;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(): boolean {return this.alive && this.options.current();}
  private publish(patch: Partial<NativeHomeRowSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  load = async (): Promise<void> => {
    if (!this.current()) return;
    this.abort.abort(); this.session?.retire(); this.abort = new AbortController();
    const {signal} = this.abort, current = () => this.current() && !signal.aborted;
    this.publish({phase: 'loading', error: undefined});
    try {
      const row = await fetchHomeRow(this.api, this.rowId, {library: this.libraryId, limit: 1}, signal);
      const libraryId = this.libraryId ?? row.libraryId;
      const capabilities = await readHomeRowCapabilities(this.api, this.rowId, libraryId, signal);
      if (!current()) return;
      this.libraryId = libraryId;
      this.kinds = !libraryId && isRecentHomeRow(this.rowId) ? this.options.kinds : [];
      this.publish({phase: 'ready', row, capabilities, sort: this.state.sort ?? homeRowDefaultSort[0]});
      this.requery();
    } catch (error) {if (current()) this.publish({phase: 'error', error});}
  };
  private nodes(filters: readonly BrowseNode[], kind = this.kinds.find(value => value.id === this.state.selectedKind)): readonly BrowseNode[] {
    return kind ? [...filters, {variant: 'predicate', field: 'library', operator: 'in', value: kind.libraryIds}] : filters;
  }
  private query(filters = this.state.filters, sort = this.state.sort!) {
    return {libraryId: this.libraryId ?? '*', homeRowId: this.rowId, pivot: homeRowPivot,
      capabilities: this.state.capabilities!, nodes: this.nodes(filters), sorts: [sort]};
  }
  private requery(): void {
    if (!this.current() || this.state.phase !== 'ready') return;
    this.session?.retire();
    const query = this.query(), current = () => this.current() && this.session === session;
    const session: BrowseSession<NativeHomeRowItem> = new BrowseSession({query, current,
      request: (path, method, body, signal) => this.api.request(path, method, body, signal),
      item: entry => Object.freeze({entry, membershipId: entry.id}),
      onFacts: facts => this.collection.learn?.(session.fetchPage, facts),
    });
    this.session = session;
    this.collection.invalidate(session.fetchPage);
    const filters = this.state.filters;
    const scopes = [undefined, ...this.kinds];
    const signal = this.abort.signal;
    void Promise.all(scopes.map(kind => this.count(kind, filters, signal))).then(counts => {
      if (current()) this.publish({counts});
    }).catch(error => {if (current()) this.publish({error});});
  }
  facets = async (field: string, search = '', filters = this.state.filters) => {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.capabilities?.fields.some(value => value.facetSource?.field === field)) throw browseSessionClosed();
    const session = this.session;
    const result = await readHomeRowFacetChoices(this.api, homeRowSource(this.rowId, this.libraryId, this.nodes(filters), []), field, search, this.abort.signal);
    if (!this.current() || this.session !== session) throw browseSessionClosed();
    return result;
  };
  private async count(kind: NativeHomeRowKind | undefined, filters: readonly BrowseNode[], signal: AbortSignal): Promise<BrowseCount> {
    const nodes = kind ? this.nodes(filters, kind) : filters;
    const total = await countHomeRow(this.api, homeRowSource(this.rowId, this.libraryId, nodes, []), signal);
    return {id: kind?.id ?? 'all', total: total ?? 0, totalIsLowerBound: false, revision: 'counted'};
  }
  /** Draft counts follow the selected scope while the displayed grid keeps its applied filters. */
  previewCounts = async (filters: readonly BrowseNode[], signal = this.abort.signal): Promise<readonly BrowseCount[]> => {
    if (!this.current() || this.state.phase !== 'ready' || signal.aborted) throw browseSessionClosed();
    browseSessionBuilder(this.query(filters)).build();
    const session = this.session;
    const count = await this.count(this.kinds.find(value => value.id === this.state.selectedKind), filters, signal);
    if (!this.current() || this.session !== session || signal.aborted) throw browseSessionClosed();
    return [count];
  };
  setSort = (sort: BrowseSortSelection): boolean => {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.capabilities?.resolvedPivot?.sorts?.includes(sort.field)) return false;
    browseSessionBuilder(this.query(this.state.filters, sort)).build();
    this.publish({sort: Object.freeze({...sort}), error: undefined}); this.requery(); return true;
  };
  setFilters = (filters: readonly BrowseNode[]): boolean => {
    if (!this.current() || this.state.phase !== 'ready') return false;
    browseSessionBuilder(this.query(filters)).build();
    this.publish({filters: Object.freeze([...filters]), counts: [], error: undefined}); this.requery(); return true;
  };
  selectKind = (id: string | undefined): boolean => {
    if (!this.current() || this.state.phase !== 'ready' || id === this.state.selectedKind || id !== undefined && !this.kinds.some(kind => kind.id === id)) return false;
    this.publish({selectedKind: id, error: undefined}); this.requery(); return true;
  };
  queueSource = () => {
    if (!this.current() || this.state.phase !== 'ready' || !this.session?.revision || !hasHomeRowQueue(this.rowId)) return;
    return homeRowSource(this.rowId, this.libraryId, this.nodes(this.state.filters), [this.state.sort!]);
  };
  dispose = (): void => {this.alive = false; this.abort.abort(); this.session?.retire(); this.collection.dispose(); this.listeners.clear();};
}
