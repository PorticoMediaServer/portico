/**
 * A Home row's See all page (Recommendations P7): the whole row, paged from
 * `GET /v1/home/rows/{id}` by its own cursor. Home keeps a short preview of each row; this page
 * is the complete, correct list, so it keeps every page the viewer scrolls to (no preview cap),
 * drops an entry a later page repeats, and, when a cursor is refused as `stale_continuation`
 * (the catalogue or the profile changed since the first page), starts again from the first page
 * rather than showing a mixture of two rankings. Framework-free: web and Apple both drive it.
 */
import type {ContentEntry} from './library-content.ts';
import {fetchHomeRow, hasFencedHomeMembership, type HomeApi, type HomeRow} from './home.ts';
import {mergeRevalidatedList} from './freshness-actions.ts';

export const HOME_ROW_PAGE_LIMIT = 60;

export type HomeRowPageSnapshot = Readonly<{
  phase: 'loading' | 'ready' | 'error';
  /** The first page's row: title, titleText, kind, artwork shape. */
  row?: HomeRow;
  entries: readonly ContentEntry[];
  total: number;
  nextCursor: string;
  /** A next page is being read. */
  paging: boolean;
  error?: unknown;
  /** How many times a stale cursor sent the page back to its first page. */
  restarts: number;
}>;

const codeOf = (e: unknown) => (e as {code?: unknown} | null)?.code;

export class HomeRowPager {
  private state: HomeRowPageSnapshot = Object.freeze({phase: 'loading', entries: Object.freeze([]), total: 0, nextCursor: '', paging: false, restarts: 0});
  private listeners = new Set<() => void>();
  private generation = 0;
  private appendEpoch = 0;
  private controller?: AbortController;
  private disposed = false;
  private readonly api: HomeApi;
  readonly rowId: string;
  private readonly limit: number;
  constructor(api: HomeApi, rowId: string, limit = HOME_ROW_PAGE_LIMIT) { this.api = api; this.rowId = rowId; this.limit = limit; }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<HomeRowPageSnapshot>) {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const l of [...this.listeners]) l();
  }
  /** The first page, replacing whatever was shown (also the explicit Refresh). */
  async load(restart = false): Promise<void> {
    const mine = ++this.generation;
    this.controller?.abort();
    const controller = new AbortController();
    this.controller = controller;
    // A restart after a refused cursor keeps what is shown until the first page is back.
    if (restart && this.state.entries.length) this.publish({nextCursor: '', paging: false, error: undefined, restarts: this.state.restarts + 1});
    else this.publish({phase: 'loading', row: undefined, entries: Object.freeze([]), total: 0, nextCursor: '', paging: false, error: undefined, ...(restart ? {restarts: this.state.restarts + 1} : {})});
    try {
      const row = await fetchHomeRow(this.api, this.rowId, {limit: this.limit}, controller.signal);
      if (mine !== this.generation) return;
      this.publish({phase: 'ready', row, entries: row.entries, total: row.total, nextCursor: row.nextCursor});
    } catch (e) {
      if (mine !== this.generation || controller.signal.aborted) return;
      this.publish({phase: 'error', error: e});
    }
  }
  retry = (): Promise<void> => this.load();
  /**
   * Read what is shown again without a loading state and merge it into the list: new and changed
   * entries appear in place, and one removed from deep in the list goes too, because every page
   * the viewer has scrolled into is read again, stopping at the loaded length or true end.
   * Fenced rows keep only the authoritative reread pages. A failed read rejects and changes nothing;
   * a refused fenced cursor restarts the row instead of retaining part of an obsolete ranking.
   */
  async revalidate(context?: {signal?: AbortSignal; settle?: () => Promise<void>}): Promise<void> {
    if (this.disposed || this.state.phase !== 'ready' || this.state.paging || context?.signal?.aborted) return;
    const mine = this.generation, appendEpoch = this.appendEpoch;
    const current = () => !this.disposed && mine === this.generation && appendEpoch === this.appendEpoch && !context?.signal?.aborted;
    const shown = this.state.entries.length;
    const row = await fetchHomeRow(this.api, this.rowId, {limit: this.limit}, context?.signal);
    if (!current()) return;
    const fresh = [...row.entries];
    let cursor = row.nextCursor;
    while (cursor && fresh.length < shown) {
      if(!current())return;
      let page: HomeRow;
      try { page = await fetchHomeRow(this.api, this.rowId, {limit: this.limit, cursor}, context?.signal); } catch (e) {
        if (codeOf(e) === 'stale_continuation' || codeOf(e) === 'invalid_cursor') {
          if (hasFencedHomeMembership(this.rowId)) {
            if (!current()) return;
            await context?.settle?.();
            if (current() && this.state.phase === 'ready' && !this.state.paging) await this.load(true);
            return;
          }
          break;
        }
        throw e;
      }
      if (!current()) return;
      const seen = new Set(fresh.map(e => e.id));
      fresh.push(...page.entries.filter(e => !seen.has(e.id)));
      if(page.nextCursor===cursor)throw Object.assign(new Error('The row cursor did not advance.'),{code:'nonprogressing_cursor'});
      cursor = page.nextCursor;
    }
    if (!current()) return;
    await context?.settle?.();
    if (!current() || this.state.phase !== 'ready' || this.state.paging) return;
    const entries = mergeRevalidatedList(this.state.entries, fresh, e => e.id, {complete: hasFencedHomeMembership(this.rowId) || !cursor, same: (a, b) => JSON.stringify(a) === JSON.stringify(b)});
    const keptTail = entries.length > fresh.length;
    this.publish({row, entries: entries === this.state.entries ? entries : Object.freeze([...entries]), total: row.total, nextCursor: keptTail ? this.state.nextCursor : cursor});
  }
  /** The next page, once per cursor. A stale cursor restarts from the first page. */
  async more(): Promise<void> {
    const cursor = this.state.nextCursor;
    if (!cursor || this.state.paging || this.state.phase !== 'ready') return;
    const mine = this.generation;
    this.publish({paging: true});
    try {
      const page = await fetchHomeRow(this.api, this.rowId, {limit: this.limit, cursor}, this.controller?.signal);
      if (mine !== this.generation) return;
      const seen = new Set(this.state.entries.map(e => e.id));
      const entries = [...this.state.entries, ...page.entries.filter(e => !seen.has(e.id))];
      if(page.nextCursor===cursor)throw Object.assign(new Error('The row cursor did not advance.'),{code:'nonprogressing_cursor'});
      this.appendEpoch++;
      this.publish({entries: Object.freeze(entries), total: page.total, nextCursor: page.nextCursor, paging: false});
    } catch (e) {
      if (mine !== this.generation) return;
      if (codeOf(e) === 'stale_continuation' || codeOf(e) === 'invalid_cursor') { await this.load(true); return; }
      this.publish({paging: false, error: e});
    }
  }
  dispose() { this.disposed = true; this.generation++; this.controller?.abort(); this.listeners.clear(); }
}
