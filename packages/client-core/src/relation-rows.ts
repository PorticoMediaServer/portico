import {readListeningSimilar, type ContentEntry, type LibraryContentApi} from './library-content.ts';
import type {ListeningViewerScope} from './listening.ts';
import type {HomeApi, HomeRow} from './home.ts';
import {entriesFromItems, readRelatedItems} from './items.ts';

/**
 * More like this on title, show and listening pages, read in server order to the final cursor.
 */

const errorCode = (error: unknown): string => typeof (error as {code?: unknown})?.code === 'string' ? (error as {code: string}).code : 'request_failed';

/** The page a row belongs to: a title, or a show. */
export type RelationRowOwner = Readonly<{kind: 'item' | 'show'; id: string}>;

/** A page of a relation row: its entries and, while the row goes on, its continuation. */
export type RelationPage = Readonly<{entries: readonly ContentEntry[]; nextCursor?: string}>;

export type RelationRowSnapshot = Readonly<{
  entries: readonly ContentEntry[];
  /** The next page's continuation; absent at the row's end. */
  cursor?: string;
  loading: boolean;
  /** The last continuation failed. A stale one (the row moved, or a second refusal) ends the row. */
  error?: Readonly<{code: string; stale: boolean}>;
}>;

/**
 * One row read to exhaustion, from its first page. `more()` reads the next page once (a repeated
 * call while a page is loading, or at the row's end, does nothing); a title already shown keeps
 * its first place. A failed page keeps the continuation for a later `more()`.
 *
 * A cursor the server refuses (`cursor_expired`, or the older `invalid_cursor`) means its scope, the viewer fence or profile, no
 * longer matches: access was decided again, not the row finished (relation_rows.go). The row is read
 * again once from its first page (`read('')`), whose entries and cursor replace what is shown, so
 * nothing allowed is hidden and nothing read under the old fence stays. A second refusal before any
 * continuation succeeds ends the row; once a page continues from the re-read, a later, separate
 * access change is read again the same way. A real move of the row (`stale_continuation`, a revision
 * check) ends it.
 */
export class RelationRowPager {
  private state: RelationRowSnapshot;
  private readonly listeners = new Set<() => void>();
  private controller: AbortController | undefined;
  private disposed = false;
  /** `read('')` reads the row's first page. */
  private readonly read: (cursor: string, signal: AbortSignal) => Promise<RelationPage>;
  private reread = false;

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
      // A continuation answered: the re-read (if any) worked, and a later refusal is a new access change.
      this.reread = false;
      this.publish({entries: Object.freeze([...this.state.entries, ...added]), ...(page.nextCursor ? {cursor: page.nextCursor} : {}), loading: false});
    } catch (error) {
      if (this.disposed || controller.signal.aborted) return;
      const code = errorCode(error);
      if ((code === 'invalid_cursor' || code === 'cursor_expired') && !this.reread) {
        this.reread = true;
        try {
          const first = await this.read('', controller.signal);
          if (this.disposed || controller.signal.aborted) return;
          const seen = new Set<string>();
          this.publish({entries: Object.freeze(first.entries.filter(entry => !seen.has(entry.id) && seen.add(entry.id))), ...(first.nextCursor ? {cursor: first.nextCursor} : {}), loading: false});
          return;
        } catch (again) {
          if (this.disposed || controller.signal.aborted) return;
          this.publish({entries: this.state.entries, loading: false, error: Object.freeze({code: errorCode(again), stale: true})});
          return;
        }
      }
      const stale = code === 'stale_continuation' || code === 'invalid_cursor' || code === 'cursor_expired';
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

/** A row the server answered whole: there is nothing more to read. */
export function completeRow(entries: readonly ContentEntry[]): RelationRowPager {
  return new RelationRowPager({entries}, () => Promise.reject(Object.assign(new Error('This row is complete.'), {code: 'row_complete'})));
}

/** More like this keeps its ranked continuation on the title's ordinary related route. */
export function recommendationRowPager(api: HomeApi, owner: RelationRowOwner, row: HomeRow, limit = 15): RelationRowPager {
  if (row.relation !== 'more_like') return completeRow(row.entries);
  return new RelationRowPager({entries: row.entries, nextCursor: row.nextCursor}, async (cursor, signal) => {
    const related = await readRelatedItems(api, owner.id, signal, {relation: 'more_like_this', cursor, limit});
    const group = related.groups.find(group => group.relation === 'more_like_this');
    return {entries: entriesFromItems(group?.items ?? []).filter(entry => entry.id !== owner.id), nextCursor: group?.nextCursor};
  });
}

/** One page of a listening title's similar row (readListeningSimilar). */
export type ListeningSimilarRead = Awaited<ReturnType<typeof readListeningSimilar>>;

/** A listening title's similar row read to its end from its first page: later pages keep the first page's basis and revision. */
export function listeningSimilarPager<S extends ListeningViewerScope>(api: LibraryContentApi, scope: S, first: ListeningSimilarRead, options: {limit?: number} = {}): RelationRowPager {
  let basis = first.revision;
  return new RelationRowPager(first, async (cursor, signal) => {
    const next = await readListeningSimilar(api, scope, first.itemId, {...options, ...(first.bySound ? {by: 'sound' as const} : {}), ...(cursor ? {cursor} : {}), signal});
    // A page on another basis (by sound or not) is not this row any more; a first page read again ('') moves the revision.
    if (next.bySound !== first.bySound || cursor && (next.revision.catalog !== basis.catalog || next.revision.viewer !== basis.viewer)) throw Object.assign(new Error('Reload to continue this row.'), {code: 'stale_continuation'});
    if (!cursor) basis = next.revision;
    return next;
  });
}
