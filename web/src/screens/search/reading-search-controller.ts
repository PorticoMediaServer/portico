import {readReadingIndexedSearch, validReadingIndexedSearchQuery, type ReadingIndexedSearchApi, type ReadingIndexedSearchHit,
  type ReadingIndexedSearchPage} from '@core/reading-indexed-search.ts';
import type {IndexedReadingTarget, IndexedReadingTransfer} from './reading-open-handoff';

export type IndexedSearchSnapshot = Readonly<{query: string; phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  page?: ReadingIndexedSearchPage; error?: unknown; opening?: boolean}>;
const changed = () => Object.assign(new Error('Indexed search page changed'), {code: 'revision_mismatch'});

/** Own one literal query and one bounded page. Catalogue results, history,
 * totals and selection grants never participate in this controller. */
export class ReadingSearchController {
  private state: IndexedSearchSnapshot = Object.freeze({query: '', phase: 'idle'});
  private readonly listeners = new Set<() => void>();
  private request?: AbortController;
  private timer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private disposed = false;
  private transfer?: IndexedReadingTransfer;
  constructor(private readonly input: Readonly<{api: ReadingIndexedSearchApi; signal: AbortSignal; isCurrent: () => boolean;
    open: (target: IndexedReadingTarget, current: () => boolean) => IndexedReadingTransfer}>) {
    input.signal.addEventListener('abort', this.dispose, {once: true});
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);return () => {this.listeners.delete(listener);};
  };
  private current() {return !this.disposed && !this.input.signal.aborted && this.input.isCurrent();}
  private publish(state: IndexedSearchSnapshot) {
    if (!this.current()) return;
    this.state = Object.freeze(state);for (const listener of this.listeners) listener();
  }
  private cancelRead() {
    ++this.generation;this.request?.abort();this.request = undefined;
    if (this.timer !== undefined) clearTimeout(this.timer);this.timer = undefined;
  }
  select = (query: string, immediate = false) => {
    if (!this.current()) return;
    this.cancelRead();this.transfer?.cancel();this.transfer = undefined;
    this.publish({query, phase: 'idle'});
    if (!query.trim()) return;
    if (!validReadingIndexedSearchQuery(query)) {this.publish({query, phase: 'error', error: {code: 'invalid_request'}});return;}
    const generation = this.generation;
    if (immediate) void this.read(query);
    else this.timer = setTimeout(() => {this.timer = undefined;if (this.current() && generation === this.generation) void this.read(query);}, 260);
  };
  refresh = () => {if (this.current()) this.select(this.state.query, true);};
  next = (page: ReadingIndexedSearchPage) => {
    if (!this.current() || this.state.phase !== 'ready' || this.state.opening || this.state.page !== page || !page.next) return;
    void this.read(this.state.query, page.next, page.indexRevision);
  };
  contains = (page: ReadingIndexedSearchPage, hit: ReadingIndexedSearchHit) => this.current() && this.state.phase === 'ready'
    && !this.state.opening && this.state.page === page && page.hits.includes(hit);
  private async read(query: string, cursor?: string, revision?: number) {
    if (!this.current()) return;
    this.cancelRead();
    const generation = this.generation, controller = this.request = new AbortController();
    const signal = AbortSignal.any([this.input.signal, controller.signal]);
    const current = () => this.current() && !signal.aborted && generation === this.generation && this.state.query === query;
    this.publish({query, phase: 'loading'});
    try {
      const page = await readReadingIndexedSearch(this.input.api, {query, limit: 20, ...(cursor ? {cursor} : {})}, {signal, isCurrent: current});
      if (!current()) return;
      if (revision !== undefined && page.indexRevision !== revision || cursor && page.next === cursor) throw changed();
      this.publish({query, phase: 'ready', page});
    } catch (error) {
      if (!current()) return;
      const code = (error as {code?: unknown})?.code;
      this.publish({query, phase: code === 'revision_mismatch' || code === 'stale_continuation' ? 'refresh-required' : 'error', error});
    } finally {if (this.request === controller) this.request = undefined;}
  }
  open = (page: ReadingIndexedSearchPage, hit: ReadingIndexedSearchHit) => {
    if (!this.contains(page, hit)) return;
    const query = this.state.query;
    const current = () => this.current() && this.state.page === page && this.state.query === query && page.hits.includes(hit);
    this.publish({...this.state, opening: true});
    try {
      const transfer = this.transfer = this.input.open({itemId: hit.itemId, sourceId: hit.sourceId, sourceRevision: hit.sourceRevision}, current);
      void transfer.finished.catch(error => {
        if (this.transfer === transfer && current()) this.publish({query, phase: 'error', error});
      });
    } catch (error) {if (current()) this.publish({query, phase: 'error', error});}
  };
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true;this.cancelRead();this.input.signal.removeEventListener('abort', this.dispose);
    this.transfer?.releaseSource();this.transfer = undefined;this.listeners.clear();
  };
}
