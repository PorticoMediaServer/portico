import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {readListeningSimilar, type ContentEntry, type LibraryContentApi} from './library-content.ts';
import type {ListeningViewerScope} from './listening.ts';
import {validateHomeRow, type HomeApi, type HomeRow} from './home.ts';

/**
 * Relation rows page to their true end (JUSTIN-ANSWERS #34): a title's or show's recommendation
 * rows, its related rows, and a listening title's similar row. The server orders each row once;
 * the first page arrives with its page and every later page continues that order by cursor.
 */

function invalid(): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_recommendations', retryable: false});
}

/** The page a row belongs to: a title, or a show. */
export type RelationRowOwner = Readonly<{kind: 'item' | 'show'; id: string}>;

/** One continuation of a title's or show's row. The row and its fence are exact. */
export async function readRecommendationRowPage(api: HomeApi, owner: RelationRowOwner, row: Pick<HomeRow, 'id' | 'viewerFence'>, options: {cursor?: string; limit?: number} = {}, signal?: AbortSignal): Promise<HomeRow> {
  const query = new URLSearchParams({limit: String(options.limit ?? 37)});
  if (options.cursor) query.set('cursor', options.cursor);
  const route = owner.kind === 'show' ? 'get_shows_id_recommendations_row' : 'get_items_id_recommendations_row';
  const page = validateHomeRow(await api.request<RouteResponse<'get_items_id_recommendations_row'>>(encodedRoutePath(route, {id: encodeURIComponent(owner.id), row: encodeURIComponent(row.id)}) + '?' + query, 'GET', undefined, signal));
  if (page.id !== row.id || page.viewerFence !== row.viewerFence) invalid();
  return page;
}

/** A page of a relation row: its entries and, while the row goes on, its continuation. */
export type RelationPage = Readonly<{entries: readonly ContentEntry[]; nextCursor?: string}>;

export type RelationRowSnapshot = Readonly<{
  entries: readonly ContentEntry[];
  /** The next page's continuation; absent at the row's end. */
  cursor?: string;
  loading: boolean;
  /** The last continuation failed. A stale one ends the row (reload the page to continue). */
  error?: Readonly<{code: string; stale: boolean}>;
}>;

/**
 * One row read to exhaustion, from its first page. `more()` reads the next page once (a repeated
 * call while a page is loading, or at the row's end, does nothing); a title already shown keeps
 * its first place. A failed page keeps the continuation for a later `more()`, except a stale one,
 * which ends the row.
 */
export class RelationRowPager {
  private state: RelationRowSnapshot;
  private readonly listeners = new Set<() => void>();
  private controller: AbortController | undefined;
  private disposed = false;
  private readonly read: (cursor: string, signal: AbortSignal) => Promise<RelationPage>;

  constructor(first: RelationPage, read: (cursor: string, signal: AbortSignal) => Promise<RelationPage>) {
    this.read = read;
    this.state = Object.freeze({entries: Object.freeze([...first.entries]), ...(first.nextCursor ? {cursor: first.nextCursor} : {}), loading: false});
  }

  getSnapshot = (): RelationRowSnapshot => this.state;

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };

  more = async (): Promise<void> => {
    const cursor = this.state.cursor;
    if (this.disposed || this.state.loading || !cursor) return;
    const controller = new AbortController();
    this.controller = controller;
    this.publish({entries: this.state.entries, cursor, loading: true});
    try {
      const page = await this.read(cursor, controller.signal);
      if (this.disposed || controller.signal.aborted) return;
      const seen = new Set(this.state.entries.map(entry => entry.id));
      const added = page.entries.filter(entry => !seen.has(entry.id) && seen.add(entry.id));
      this.publish({entries: Object.freeze([...this.state.entries, ...added]), ...(page.nextCursor ? {cursor: page.nextCursor} : {}), loading: false});
    } catch (error) {
      if (this.disposed || controller.signal.aborted) return;
      const code = typeof (error as {code?: unknown})?.code === 'string' ? (error as {code: string}).code : 'request_failed';
      const stale = code === 'stale_continuation';
      this.publish({entries: this.state.entries, ...(stale ? {} : {cursor}), loading: false, error: Object.freeze({code, stale})});
    } finally {
      if (this.controller === controller) this.controller = undefined;
    }
  };

  dispose(): void {
    this.disposed = true;
    this.controller?.abort();
    this.listeners.clear();
  }

  private publish(next: RelationRowSnapshot): void {
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
}

/** The pager of a title's or show's recommendation row, from the row as its page returned it. */
export function recommendationRowPager(api: HomeApi, owner: RelationRowOwner, row: HomeRow, limit?: number): RelationRowPager {
  return new RelationRowPager({entries: row.entries, ...(row.hasMore && row.nextCursor ? {nextCursor: row.nextCursor} : {})},
    async (cursor, signal) => {
      const page = await readRecommendationRowPage(api, owner, row, {cursor, ...(limit === undefined ? {} : {limit})}, signal);
      if (page.revision.catalog !== row.revision.catalog || page.revision.viewer !== row.revision.viewer) throw Object.assign(new Error('Reload recommendations after the title changes.'), {code: 'stale_continuation'});
      return {entries: page.entries, ...(page.hasMore && page.nextCursor ? {nextCursor: page.nextCursor} : {})};
    });
}

/** One page of a listening title's similar row (readListeningSimilar). */
export type ListeningSimilarRead = Awaited<ReturnType<typeof readListeningSimilar>>;

/** A listening title's similar row read to its end from its first page: later pages keep the first page's basis and revision. */
export function listeningSimilarPager<S extends ListeningViewerScope>(api: LibraryContentApi, scope: S, first: ListeningSimilarRead, options: {limit?: number} = {}): RelationRowPager {
  return new RelationRowPager(first, async (cursor, signal) => {
    const next = await readListeningSimilar(api, scope, first.itemId, {...options, ...(first.bySound ? {by: 'sound' as const} : {}), cursor, signal});
    if (next.bySound !== first.bySound || next.revision.catalog !== first.revision.catalog || next.revision.viewer !== first.revision.viewer) throw Object.assign(new Error('Reload to continue this row.'), {code: 'stale_continuation'});
    return next;
  });
}
