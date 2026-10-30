import type {PlayHistoryEntry, PlayHistoryFilters, PlayHistoryPage} from '../../../../../../packages/client-core/src/play-history';
import type {ReportContext} from '../../../../../../packages/client-core/src/report-context';

export type HistoryPerson = Readonly<{accountId: string; username: string}>;
export type HistoryPeoplePage = Readonly<{items: readonly HistoryPerson[]; nextCursor: string}>;
export type HistoryReader = Readonly<{
  page: (filters: PlayHistoryFilters, cursor: string, signal: AbortSignal) => Promise<PlayHistoryPage>;
  people: (filters: PlayHistoryFilters, cursor: string, signal: AbortSignal) => Promise<HistoryPeoplePage>;
}>;
export type HistoryQuery = Readonly<{period: 'today' | '7d' | '30d' | 'all'; media?: string; accountId?: string; search?: string; sort: string; direction: 'asc' | 'desc'}>;
export type HistoryState = Readonly<{
  query: HistoryQuery; report: ReportContext; entries: readonly PlayHistoryEntry[]; nextCursor: string; pages: number; total?: number;
  phase: 'idle' | 'loading' | 'ready' | 'error'; error?: unknown;
  people: readonly HistoryPerson[]; peopleCursor: string; peopleLoading: boolean; peopleError?: unknown;
}>;
const initialQuery: HistoryQuery = Object.freeze({period: 'all', sort: 'played', direction: 'desc'});
const initial = (report: ReportContext): HistoryState => Object.freeze({query: initialQuery, report, entries: [], nextCursor: '', pages: 0, phase: 'idle', people: [], peopleCursor: '', peopleLoading: false});
const authentication = (error: unknown) => !!error && typeof error === 'object' && 'status' in error && (error.status === 401 || error.status === 403);

/** Cursor reads keep the same server interval; changing filters retires every older page. */
export class NativeHistorySource {
  private state: HistoryState;
  private readonly retired: HistoryState;
  private listeners = new Set<() => void>();
  private pageFlight?: AbortController;
  private peopleFlight?: AbortController;
  private seenPages = new Set<string>();
  private seenPeople = new Set<string>();
  private closed = false;
  private readonly reader: HistoryReader;
  private readonly current: () => boolean;
  private readonly capture: () => ReportContext;
  private readonly failure: (error: unknown) => void;
  constructor(reader: HistoryReader, current: () => boolean, capture: () => ReportContext, failure: (error: unknown) => void) {
    this.reader = reader;this.current = current;this.capture = capture;this.failure = failure;
    this.state = initial(capture());this.retired = this.state;
  }
  private active = () => !this.closed && this.current();
  getSnapshot = (): HistoryState => this.active() ? this.state : this.retired;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: HistoryState) {if (this.active()) {this.state = Object.freeze(state);for (const listener of this.listeners) listener();}}
  start() {if (!this.active() || this.state.phase !== 'idle') return;void this.loadPage(false);void this.loadPeople(false);}
  change(query: HistoryQuery) {
    if (!this.active() || JSON.stringify(query) === JSON.stringify(this.state.query)) return;
    this.pageFlight?.abort();this.pageFlight = undefined;this.seenPages.clear();
    this.publish({...this.state, query: Object.freeze({...query}), entries: [], total: undefined, nextCursor: '', pages: 0, phase: 'idle', error: undefined});void this.loadPage(false);
  }
  refresh() {
    if (!this.active() || this.pageFlight) return;
    this.seenPages.clear();this.peopleFlight?.abort();this.peopleFlight = undefined;this.seenPeople.clear();
    this.publish({...this.state, report: this.capture(), nextCursor: '', pages: 0, error: undefined, peopleCursor: '', peopleError: undefined, peopleLoading: false});
    void this.loadPage(false);void this.loadPeople(false);
  }
  retry() {if (this.active()) void this.loadPage(this.state.entries.length > 0 && !!this.state.nextCursor);}
  more() {if (this.active() && this.state.phase === 'ready' && this.state.nextCursor) void this.loadPage(true);}
  morePeople() {if (this.active() && this.state.peopleCursor) void this.loadPeople(true);}
  retryPeople() {if (this.active()) void this.loadPeople(this.state.people.length > 0 && !!this.state.peopleCursor);}
  private async loadPage(append: boolean) {
    if (!this.active() || this.pageFlight) return;
    const before = this.state, cursor = append ? before.nextCursor : '', request = new AbortController();this.pageFlight = request;
    const owned = () => this.active() && this.pageFlight === request && !request.signal.aborted;
    this.publish({...before, phase: 'loading', error: undefined});
    try {
      const page = await this.reader.page({...before.report, ...before.query}, cursor, request.signal);
      if (!owned()) return;
      if (cursor) this.seenPages.add(cursor);
      const entries = append ? [...before.entries] : [], ids = new Set(entries.map(row => row.id));
      for (const entry of page.entries) if (!ids.has(entry.id)) {ids.add(entry.id);entries.push(entry);}
      const nextCursor = page.nextCursor && page.nextCursor !== cursor && !this.seenPages.has(page.nextCursor) ? page.nextCursor : '';
      this.publish({...this.state, entries, total: append ? before.total : page.total, nextCursor, pages: append ? before.pages + 1 : 1, phase: 'ready', error: undefined});
    } catch (error) {
      if (owned()) {if (authentication(error)) {this.dispose();this.failure(error);}else this.publish({...this.state, phase: 'error', error});}
    } finally {if (this.pageFlight === request) this.pageFlight = undefined;}
  }
  private async loadPeople(append: boolean) {
    if (!this.active() || this.peopleFlight) return;
    const before = this.state, cursor = append ? before.peopleCursor : '', request = new AbortController();this.peopleFlight = request;
    const owned = () => this.active() && this.peopleFlight === request && !request.signal.aborted;
    this.publish({...before, peopleLoading: true, peopleError: undefined});
    try {
      // The selector covers accounts with retained plays, including removed accounts.
      const page = await this.reader.people({...before.report, period: 'all'}, cursor, request.signal);
      if (!owned()) return;
      if (cursor) this.seenPeople.add(cursor);
      const people = append ? [...before.people] : [], ids = new Set(people.map(row => row.accountId));
      for (const person of page.items) if (!ids.has(person.accountId)) {ids.add(person.accountId);people.push({accountId: person.accountId, username: person.username});}
      const selected = before.people.find(person => person.accountId === this.state.query.accountId);
      if (selected && !ids.has(selected.accountId)) people.push(selected);
      const peopleCursor = page.nextCursor && page.nextCursor !== cursor && !this.seenPeople.has(page.nextCursor) ? page.nextCursor : '';
      this.publish({...this.state, people, peopleCursor, peopleLoading: false, peopleError: undefined});
    } catch (error) {
      if (owned()) {if (authentication(error)) {this.dispose();this.failure(error);}else this.publish({...this.state, peopleLoading: false, peopleError: error});}
    } finally {if (this.peopleFlight === request) this.peopleFlight = undefined;}
  }
  dispose() {
    if (this.closed) return;this.closed = true;this.pageFlight?.abort();this.peopleFlight?.abort();this.pageFlight = undefined;this.peopleFlight = undefined;
    for (const listener of this.listeners) listener();this.listeners.clear();
  }
}

export const historyLocation = (value: string | undefined): 'home' | 'away' | undefined => value === 'local' || value === 'home' ? 'home' : value === 'remote' || value === 'away' ? 'away' : undefined;
