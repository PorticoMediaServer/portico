import {
  SearchService, clearSearchHistory, recordSearch, searchDependencies, searchFirstPages, searchHistoryQuery, searchQueryProblem,
  type SearchEntry, type SearchGroup, type SearchGroupResult, type SearchHistoryEntry, type SearchHistoryPage, type SearchSnapshot,
} from '../../../../../packages/client-core/src/search';
import {queryKeys} from '../../../../../packages/client-core/src/query/keys';
import type {ViewerQueries} from '../../../../../packages/client-core/src/query/viewer-queries';
import type {FreshDocument} from '../../../../../packages/client-core/src/freshness-hub';
import {viewerQueriesFor} from '../../shell/freshness';
import {searchReadingOffered, searchRequestGroups} from '../../../../../packages/client-core/src/presentation/search-model';
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
 * Search owns its response fence; a Home fence is never substituted for it.
 * First pages and Recent searches come from this viewer's cache (`['search','results',…]` and
 * `['search','history']`, as web): a query already read is shown again with no request, a recording
 * read fills the plain read's entry, and Recent searches are read again only when the server says it
 * recorded one. Continuations and recording reads are the server's.
 * While shown, the search on screen stays current: when a title it shows, the libraries, this profile's
 * state or its preferences move, its first pages are read again and replaced in place (a failed read
 * leaves them as they were). An open selection, or rows paged past their first page, hold the shown
 * results as they are; a move meanwhile is read once the hold ends or with the next search. */
export class NativeSearchDestination {
  readonly service: SearchService;
  private readonly queries: ViewerQueries;
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
  private readonly freshness: FreshDocument;
  private holds = 0;

  constructor(options: NativeSearchDestination['options']) {
    this.options = options;
    this.queries = viewerQueriesFor(options.api, options.scope);
    this.service = new SearchService({api: options.api, scope: options.scope, firstPages: searchFirstPages(this.queries)});
    const text = options.initialQuery ?? '';
    this.state = Object.freeze({text, group: 'all', ...(text.trim() ? {problem: searchQueryProblem(text)} : {}), search: this.service.getSnapshot(), groups: [], recent: [], clearing: false});
    this.unsubscribe = this.service.subscribe(this.changed);
    this.freshness = this.queries.keepCurrent({
      dependencies: searchDependencies(),
      // Only first pages are read again: a re-read of page one cannot say which later cards still stand.
      hasContent: () => !this.disposed && !this.paused && this.holds === 0 && this.groupReads.size === 0
        && this.service.getSnapshot().phase === 'ready' && this.service.getSnapshot().pagination.cursor === null,
      revalidate: context => this.service.revalidate(context),
    });
    this.freshness.setOpen(true);
  }
  /** Keep the shown results as they are while a selection is open over them (an answer read again
   *  starts a new revision, which ends it). Returns the release; a move meanwhile is read then. */
  hold = (): (() => void) => {
    this.holds++;
    let held = true;
    return () => {if (!held) return; held = false; if (--this.holds === 0 && !this.disposed) this.freshness.check();};
  };
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
    // Something to keep now: a move during the first load is read.
    if (search.phase === 'ready') this.freshness.check();
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
    // This read answers whatever moved before it (the cache's own first page is current too).
    this.freshness.markCurrent();
    // SR-6: the All view asks for the drawn groups only, as web does, so no section arrives
    // that this app cannot name (Collections and Authors were drawn under "All").
    const selected = this.state.group, all = selected === 'all', readingOffered = searchReadingOffered(this.state.capabilities);
    try {
      await this.service.select(this.state.text, {
        ...(selected === 'all' ? {groups: searchRequestGroups(readingOffered)} : {group: selected}),
        record: record && this.options.rememberHistory(),
      });
      // An answer that first says Reading is installed adds it by a read-only repeat.
      if (all && !readingOffered && this.current() && !this.paused && revision === this.selectionRevision
        && searchReadingOffered(this.service.getSnapshot().data?.capabilities)) {
        await this.service.select(this.state.text, {groups: searchRequestGroups(true), record: false});
      }
      if (record && this.current() && !this.paused && revision === this.selectionRevision) await this.refreshHistory();
    } catch (error) {if (this.current() && !this.paused && revision === this.selectionRevision) this.options.reportError(error);}
  }
  private committedText?: string;
  commit = async (): Promise<void> => {this.stopTimer(); this.committedText = this.state.text.trim(); await this.select(true);};
  useRecent = (query: string): void => {this.setText(query); void this.commit();};
  private recording?: AbortController;
  /** SR-3: opening or playing a result commits the query it came from, once, as web does; on
   *  TV, which has no Search key, it is the only natural commit. The record is its own named
   *  request, so the drawn answer (and the open or play starting from it) is never replaced. */
  recordOpened = (): void => {
    const data = this.state.search.data, text = data?.query.q.trim() ?? '';
    if (!this.current() || this.paused || !text || this.committedText === text || !this.options.rememberHistory()) return;
    const group = this.state.group !== 'all' ? this.state.group : data?.groups[0]?.id ?? data?.query.groups[0];
    if (!group) return;
    this.committedText = text;
    this.recording?.abort();
    const recording = new AbortController();
    this.recording = recording;
    void recordSearch(this.options.api, text, group, recording.signal).then(async recorded => {
      if (this.recording !== recording) return;
      this.recording = undefined;
      if (recorded) void this.queries.client.invalidateQueries({queryKey: queryKeys.searchHistory()});
      if (recorded && this.current() && !this.paused) await this.refreshHistory();
    }).catch(error => {if (!recording.signal.aborted && this.current()) this.options.reportError(error);});
  };
  private groupChanged = (group: SearchGroup): void => {
    const read = this.groupReads.get(group);
    if (!read || !this.current() || this.paused || read.revision !== this.selectionRevision || this.state.group !== 'all'
      || this.service.getSnapshot().data !== read.base) return;
    const search = read.service.getSnapshot();
    if (search.error?.code === 'unauthorized' || search.error?.code === 'forbidden'
      || search.data && search.data.scope.viewerFence !== read.base.scope.viewerFence) {
      // A known access/fence change retires all retained facts before the canonical All reread.
      this.stopGroupReads(); this.service.cancel(); this.forgetFirstPages(); void this.select(false); return;
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
          this.stopGroupReads(); this.service.cancel(); this.forgetFirstPages(); void this.select(false); return;
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
  /** The canonical reread after an access, fence or count change, and an explicit Try again, is the
   * server's answer, never a cached first page (`['search','results']` is every `queryKeys.searchResults`). */
  private forgetFirstPages(): void {void this.queries.client.invalidateQueries({queryKey: ['search', 'results']}, {cancelRefetch: false});}
  retry = (): Promise<void> => {
    if (!this.current() || this.paused) return Promise.resolve();
    this.forgetFirstPages(); return this.service.retry();
  };
  /** Stop hidden work without turning a real arrived query/page into a draft. */
  pause = (): void => {
    if (!this.current() || this.paused) return;
    this.paused = true; this.selectionRevision++; this.stopTimer(); this.historyRevision++; this.history?.abort(); this.history = undefined;
    this.freshness.setOpen(false);
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
    // Shown again: what moved while hidden is read now.
    this.freshness.setOpen(true);
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
      // The cached recents unless something moved them (a recorded search, an erase, another device's hint).
      const page = await this.queries.client.fetchQuery(searchHistoryQuery(this.queries, this.options.scope));
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
      this.queries.client.setQueryData<SearchHistoryPage>(queryKeys.searchHistory(), held => held && {...held, entries: []});
      if (!controller.signal.aborted && this.current() && revision === this.historyRevision) this.publish({recent: []});
    } catch (error) {if (!controller.signal.aborted && this.current()) this.options.reportError(error);}
    finally {if (this.history === controller) this.history = undefined; if (revision === this.historyRevision) this.publish({clearing: false});}
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.selectionRevision++; this.stopTimer(); this.history?.abort(); this.historyRevision++;
    this.recording?.abort(); this.recording = undefined; this.freshness.dispose();
    this.unsubscribe(); this.stopGroupReads(); this.service.cancel(); this.service.dispose(); this.pages.clear(); this.listeners.clear();
    this.state = Object.freeze({...this.state, text: '', search: this.service.getSnapshot(), capabilities: undefined, paging: {}, groups: [], recent: [], clearing: false});
  };
}
