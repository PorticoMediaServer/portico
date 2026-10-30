import {
  SearchService, clearSearchHistory, readSearchHistory, searchQueryProblem,
  type SearchEntry, type SearchGroup, type SearchGroupResult, type SearchHistoryEntry, type SearchSnapshot,
} from '../../../../../packages/client-core/src/search';
import type {ContentScope, LibraryContentApi} from '../../../../../packages/client-core/src/library-content';

export const nativeSearchTailKey = (group: SearchGroup, cursor: string): string => JSON.stringify(['search-tail', group, cursor]);
export const nativeSearchEntryKey = (group: SearchGroup, entry: SearchEntry): string => JSON.stringify([group, entry.kind, entry.id]);

export type NativeSearchPageIssue = NonNullable<SearchSnapshot['error']> | Readonly<{code: SearchGroupResult['errorCode']; retryable: true}>;
export type NativeSearchPageState = Readonly<{loading: boolean; error: NativeSearchPageIssue | null; fulfilled?: Readonly<{waitingId: string; firstItemId?: string}>}>;

const pageIssue = (search: SearchSnapshot, group: SearchGroup): NativeSearchPageIssue | null => {
  const failed = search.phase === 'ready' ? search.groups.find(result => result.id === group && result.status === 'error') : undefined;
  return search.error ?? (failed ? {code: failed.errorCode, retryable: true} : null);
};

export type NativeSearchSnapshot = Readonly<{
  text: string;
  group: SearchGroup | 'all';
  problem?: ReturnType<typeof searchQueryProblem>;
  search: SearchSnapshot;
  groups: readonly SearchGroupResult[];
  recent: readonly SearchHistoryEntry[];
  clearing: boolean;
  capabilities?: NonNullable<SearchSnapshot['data']>['capabilities'];
  paging?: Readonly<Partial<Record<SearchGroup, NativeSearchPageState>>>;
}>;

/** One genuine SearchService and its history for one restored viewer lifetime.
 * Search owns its response fence; a Home fence is never substituted for it. */
export class NativeSearchDestination {
  readonly service: SearchService;
  private readonly options: Readonly<{
    api: LibraryContentApi; scope: ContentScope; current: () => boolean;
    rememberHistory: () => boolean; reportError: (error: unknown) => void;
    /** One route-owned seed from the captured title; copied once, then the draft is the user's. */
    initialQuery?: string;
  }>;
  private state: NativeSearchSnapshot;
  private readonly listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private history?: AbortController;
  private historyRevision = 0;
  private readonly unsubscribe: () => void;
  private disposed = false;
  private paused = false;
  private selectionRevision = 0;
  private pageQuery = '';
  private displayedData?: SearchSnapshot['data'];
  private capabilityQuery = '';
  private pages = new Map<SearchGroup, SearchGroupResult>();
  private groupReads = new Map<SearchGroup, {service: SearchService; unsubscribe: () => void; revision: number; base: NonNullable<SearchSnapshot['data']>}>();

  constructor(options: NativeSearchDestination['options']) {
    this.options = options;
    this.service = new SearchService({api: options.api, scope: options.scope});
    const text = options.initialQuery ?? '';
    this.state = Object.freeze({text, group: 'all', ...(text.trim() ? {problem: searchQueryProblem(text)} : {}), search: this.service.getSnapshot(), groups: [], recent: [], clearing: false});
    this.unsubscribe = this.service.subscribe(this.changed);
  }
  getSnapshot = (): NativeSearchSnapshot => {this.current(); return this.state;};
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current(): boolean {
    if (!this.disposed && !this.options.current()) this.dispose();
    return !this.disposed;
  }
  private publish(patch: Partial<NativeSearchSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  private changed = (): void => {
    if (!this.current()) return;
    const search = this.service.getSnapshot();
    const query = JSON.stringify([search.query?.q, search.query?.group]);
    const arrived = search.phase === 'ready' && search.data !== this.displayedData;
    const reset = query !== this.pageQuery || !search.data || arrived && !search.pagination.cursor;
    if (reset) {this.pages.clear(); this.stopGroupReads(); this.pageQuery = query;}
    if (!search.data) this.displayedData = undefined;
    if (arrived) {
      this.displayedData = search.data;
      for (const group of search.groups) {
        const previous = search.pagination.cursor ? this.pages.get(group.id) : undefined;
        const items = new Map<string, SearchEntry>();
        for (const entry of [...(previous?.items ?? []), ...group.items]) items.set(nativeSearchEntryKey(group.id, entry), entry);
        this.pages.set(group.id, Object.freeze({...group, items: Object.freeze([...items.values()])}));
      }
    }
    const denied = search.error?.code === 'unauthorized' || search.error?.code === 'forbidden';
    const capabilities = !denied && search.data?.capabilities || (!denied && search.query?.q === this.capabilityQuery ? this.state.capabilities : undefined);
    if (search.data && !denied) this.capabilityQuery = search.data.query.q;
    else if (!capabilities) this.capabilityQuery = '';
    this.publish({search, capabilities, groups: Object.freeze([...this.pages.values()]), ...(reset ? {paging: {}} : {})});
  };
  private stopGroupReads(): void {
    for (const read of this.groupReads.values()) {read.unsubscribe(); read.service.dispose();}
    this.groupReads.clear();
  }
  private stopTimer(): void {clearTimeout(this.timer); this.timer = undefined;}
  setText(text: string): void {
    if (!this.current() || this.paused) return;
    this.selectionRevision++; this.stopTimer(); this.stopGroupReads(); this.service.cancel();
    const problem = searchQueryProblem(text);
    this.publish({text, group: 'all', problem, paging: {}});
    if (!text.trim() || problem) return;
    // notes/search.md: one260ms debounce; Enter commits immediately.
    this.timer = setTimeout(() => {this.timer = undefined; void this.select(false);}, 260);
  }
  selectGroup(group: SearchGroup | 'all'): void {
    if (!this.current() || this.paused) return;
    this.stopTimer(); this.stopGroupReads(); this.publish({group, paging: {}}); void this.select(false);
  }
  private async select(record: boolean): Promise<void> {
    if (!this.current() || this.paused || !this.state.text.trim() || this.state.problem) return;
    const revision = ++this.selectionRevision; this.stopGroupReads(); this.publish({paging: {}});
    try {
      await this.service.select(this.state.text, {
        ...(this.state.group === 'all' ? {} : {group: this.state.group}),
        record: record && this.options.rememberHistory(),
      });
      if (record && this.current() && !this.paused && revision === this.selectionRevision) await this.refreshHistory();
    } catch (error) {if (this.current() && !this.paused && revision === this.selectionRevision) this.options.reportError(error);}
  }
  commit = async (): Promise<void> => {this.stopTimer(); await this.select(true);};
  useRecent = (query: string): void => {this.setText(query); void this.commit();};
  private groupChanged = (group: SearchGroup): void => {
    const read = this.groupReads.get(group);
    if (!read || !this.current() || this.paused || read.revision !== this.selectionRevision || this.state.group !== 'all'
      || this.service.getSnapshot().data !== read.base) return;
    const search = read.service.getSnapshot();
    if (search.error?.code === 'unauthorized' || search.error?.code === 'forbidden'
      || search.data && search.data.scope.viewerFence !== read.base.scope.viewerFence) {
      // A known access/fence change retires all retained facts before the canonical All reread.
      this.stopGroupReads(); this.service.cancel(); void this.select(false); return;
    }
    const pageError = pageIssue(search, group);
    let fulfilled: Readonly<{waitingId: string; firstItemId?: string}> | undefined;
    if (search.phase === 'ready' && search.data !== read.base) {
      const result = search.groups.find(value => value.id === group);
      if (result?.status === 'error') {
        if (!search.pagination.cursor) this.pages.set(group, result);
      } else if (result) {
        const previous = search.pagination.cursor ? this.pages.get(group) : undefined;
        if (previous && result.totalCountExact && result.totalCount < previous.items.length) {
          // The authoritative count invalidates the retained projection, but cannot identify
          // which old cards survived. Retire it and let the canonical All read answer.
          this.stopGroupReads(); this.service.cancel(); void this.select(false); return;
        }
        const known = new Set(previous?.items.map(entry => nativeSearchEntryKey(group, entry)));
        const firstAdded = result.items.find(entry => !known.has(nativeSearchEntryKey(group, entry)));
        if (search.pagination.cursor) fulfilled = {waitingId: nativeSearchTailKey(group, search.pagination.cursor),
          ...(firstAdded ? {firstItemId: nativeSearchEntryKey(group, firstAdded)} : {})};
        const items = new Map<string, SearchEntry>();
        for (const entry of [...(previous?.items ?? []), ...result.items]) items.set(nativeSearchEntryKey(group, entry), entry);
        this.pages.set(group, Object.freeze({...result, items: Object.freeze([...items.values()])}));
      }
    }
    this.publish({groups: Object.freeze([...this.pages.values()]), paging: {...this.state.paging,
      [group]: {...this.state.paging?.[group], loading: search.phase === 'loading', error: pageError, ...(fulfilled ? {fulfilled} : {})}}});
  };
  next = (group: SearchGroup): Promise<void> => {
    if (!this.current() || this.paused) return Promise.resolve();
    if (this.state.group !== 'all') return this.service.next(group);
    let read = this.groupReads.get(group);
    if (!read) {
      const base = this.service.getSnapshot().data, service = this.service.forkContinuation(group);
      if (!base || !service) return Promise.resolve();
      read = {service, base, revision: this.selectionRevision, unsubscribe: service.subscribe(() => this.groupChanged(group))};
      this.groupReads.set(group, read);
    }
    return read.service.next(group);
  };
  retryGroup = (group: SearchGroup): Promise<void> => {
    if (!this.current() || this.paused) return Promise.resolve();
    const read = this.groupReads.get(group);
    return read ? read.service.retry() : this.state.group === 'all' ? this.next(group) : this.service.retry();
  };
  retry = (): Promise<void> => this.current() && !this.paused ? this.service.retry() : Promise.resolve();
  /** Stop hidden work without turning a real arrived query/page into a draft. */
  pause = (): void => {
    if (!this.current() || this.paused) return;
    this.paused = true; this.selectionRevision++; this.stopTimer(); this.historyRevision++; this.history?.abort(); this.history = undefined;
    this.service.pause();
    for (const [group, read] of this.groupReads) {read.service.pause();
      if (read.service.getSnapshot().phase === 'idle') {read.unsubscribe(); read.service.dispose(); this.groupReads.delete(group);}}
    this.publish({paging: Object.fromEntries([...this.groupReads].map(([group, read]) => [group, {...this.state.paging?.[group], loading: false, error: pageIssue(read.service.getSnapshot(), group) ?? this.state.paging?.[group]?.error ?? null}]))});
    if (this.state.clearing) this.publish({recent: [], clearing: false});
  };
  /** A paused arrived page needs no synthetic reread. An interrupted draft
   * resumes only as a normal non-recording search after actual activation. */
  resume = (): void => {
    if (!this.current() || !this.paused) return;
    this.paused = false;
    for (const read of this.groupReads.values()) read.revision = this.selectionRevision;
    if (this.state.search.phase === 'idle') void this.select(false);
  };
  refreshHistory = async (): Promise<void> => {
    if (!this.current()) return;
    if (!this.options.rememberHistory()) {
      if (!this.state.clearing) {this.history?.abort(); this.history = undefined; this.historyRevision++;}
      this.publish({recent: []}); return;
    }
    if (this.state.clearing) return;
    this.history?.abort();
    const revision = ++this.historyRevision;
    if (this.paused) return;
    const controller = new AbortController(); this.history = controller;
    try {
      const page = await readSearchHistory(this.options.api, this.options.scope, controller.signal);
      if (!controller.signal.aborted && this.current() && revision === this.historyRevision) this.publish({recent: this.options.rememberHistory() ? page.entries : []});
    } catch (error) {if (!controller.signal.aborted && this.current() && revision === this.historyRevision) this.options.reportError(error);}
    finally {if (this.history === controller) this.history = undefined;}
  };
  clearHistory = async (): Promise<void> => {
    if (!this.current() || this.paused || this.state.clearing) return;
    this.history?.abort(); const revision = ++this.historyRevision;
    const controller = new AbortController(); this.history = controller;
    this.publish({clearing: true});
    try {
      await clearSearchHistory(this.options.api, controller.signal);
      if (!controller.signal.aborted && this.current() && revision === this.historyRevision) this.publish({recent: []});
    } catch (error) {if (!controller.signal.aborted && this.current()) this.options.reportError(error);}
    finally {if (this.history === controller) this.history = undefined; if (revision === this.historyRevision) this.publish({clearing: false});}
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.selectionRevision++; this.stopTimer(); this.history?.abort(); this.historyRevision++;
    this.unsubscribe(); this.stopGroupReads(); this.service.cancel(); this.service.dispose(); this.pages.clear(); this.listeners.clear();
    this.state = Object.freeze({...this.state, text: '', search: this.service.getSnapshot(), capabilities: undefined, paging: {}, groups: [], recent: [], clearing: false});
  };
}
