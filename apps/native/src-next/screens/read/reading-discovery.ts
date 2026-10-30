import {ReadingError, type LocalSession, type ReadingClient, type ReadingContinueItem, type ReadingItem, type ReadingShortRead} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeReadOrigin, openNativeReadBook, type NativeReadOrigin} from './eligibility';

export type NativeReadingDiscoveryState = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** Continue reading, in the server's order, with its real progress. */
  continueItems: readonly ReadingContinueItem[];
  /** The server's `recent` Discover row, in its order. */
  recent: readonly ReadingItem[];
  /** The server's real `short-reads` row (complete word counts only). */
  shortReads: readonly ReadingShortRead[];
  error?: unknown;
}>;

const refused = () => Object.assign(new Error('native_reading_discovery_refused'), {name: 'AbortError', code: 'cancelled'});
const cancelled = (reason: unknown): boolean => reason instanceof ReadingError && reason.code === 'cancelled';

/** The Discover rows that have real producers: Continue reading
 * (`GET /v1/reading/continue`) and the `recent` and `short-reads` rows of
 * `GET /v1/reading/discover`. Short reads carry the server's complete word count
 * and estimate; classifications, series and periodicals stay omitted until their
 * producers exist, and no length estimate is invented here. `libraryId` is passed
 * through, so no other library's titles can appear. */
export class NativeReadingDiscovery {
  private readonly context: NativeShellBootstrapContext;
  private readonly libraryId: string;
  private readonly authority: ShellAuthority;
  private readonly captured?: Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']}>;
  private readonly life = new AbortController();
  private client?: ReadingClient;
  private origin?: NativeReadOrigin;
  private state: NativeReadingDiscoveryState = Object.freeze({phase: 'idle', continueItems: [], recent: [], shortReads: []});
  private listeners = new Set<() => void>();
  private disposed = false;
  private opened = false;
  /** Each paged row's own next cursor, and the rows asking for it now (WEB-053). */
  private cursors = new Map<'recent' | 'short-reads', string>();
  private loadingMore = new Set<'recent' | 'short-reads'>();

  constructor(context: NativeShellBootstrapContext, libraryId: string) {
    this.context = context; this.libraryId = libraryId; this.authority = context.authority();
    const selected = context.session();
    this.captured = selected && Object.freeze({sessionFamilyId: selected.sessionFamilyId, viewer: Object.freeze({...selected.viewer})});
  }
  private current = (): boolean => {
    const captured = this.captured, selected = this.context.session();
    return !this.disposed && !this.life.signal.aborted && this.context.current() && !!captured && !!selected
      && selected.sessionFamilyId === captured.sessionFamilyId && sameAuthority(this.authority, this.context.authority());
  };
  private reading(): ReadingClient {
    if (!this.current()) throw refused();
    return this.client ??= this.context.api.reading({viewer: this.captured!.viewer, isCurrent: this.current});
  }
  private publish(next: NativeReadingDiscoveryState): void {
    if (this.disposed || !this.current()) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  load = async (): Promise<void> => {
    if (!this.current() || this.opened) return;
    this.origin ??= nativeReadOrigin(this.context, this.context.navigation.getSnapshot().route);
    this.opened = true;
    this.publish({phase: 'loading', continueItems: [], recent: [], shortReads: []});
    const signal = this.life.signal;
    try {
      const [continuePage, discover] = await Promise.allSettled([
        this.reading().continue({libraryId: this.libraryId, limit: 20}, signal),
        this.reading().discover({libraryId: this.libraryId, limit: 20, shortReadsLimit: 12}, signal),
      ]);
      if (!this.current()) return;
      const failures = [continuePage, discover].filter((result): result is PromiseRejectedResult => result.status === 'rejected' && !cancelled(result.reason));
      const continueItems = continuePage.status === 'fulfilled' ? continuePage.value.items : [];
      this.cursors.clear();
      if (discover.status === 'fulfilled') for (const row of discover.value.rows)
        if ((row.key === 'recent' || row.key === 'short-reads') && row.nextCursor) this.cursors.set(row.key, row.nextCursor);
      const recent = discover.status === 'fulfilled' ? discover.value.rows.find(row => row.key === 'recent')?.items ?? [] : [];
      const shortReads = discover.status === 'fulfilled' ? discover.value.rows.find(row => row.key === 'short-reads')?.shortReads ?? [] : [];
      this.publish({phase: failures.length ? 'error' : 'ready', continueItems, recent, shortReads,
        ...(failures.length ? {error: failures[0]!.reason} : {})});
    } catch (error) {
      if (this.current() && !cancelled(error)) this.publish({phase: 'error', continueItems: [], recent: [], shortReads: [], error});
    }
  };
  /** Near a row's end: that row's next page alone, appended without repeating a
   * title already drawn. A failed page ends the row and is reported. */
  more = async (row: 'recent' | 'short-reads'): Promise<void> => {
    const cursor = this.cursors.get(row);
    if (!cursor || this.loadingMore.has(row) || !this.current() || this.state.phase !== 'ready') return;
    this.loadingMore.add(row);
    const before = this.state;
    try {
      const page = await this.reading().discoverRow(row, {libraryId: this.libraryId, cursor, limit: 20}, this.life.signal);
      if (!this.current() || this.state !== before) return;
      if (page.row.nextCursor) this.cursors.set(row, page.row.nextCursor); else this.cursors.delete(row);
      if (row === 'recent') {
        const seen = new Set(before.recent.map(item => item.id));
        this.publish({...before, recent: [...before.recent, ...page.row.items.filter(item => !seen.has(item.id))]});
      } else {
        const seen = new Set(before.shortReads.map(entry => entry.item.id));
        this.publish({...before, shortReads: [...before.shortReads, ...(page.row.shortReads ?? []).filter(entry => !seen.has(entry.item.id))]});
      }
    } catch (error) {
      if (!this.current() || cancelled(error)) return;
      this.cursors.delete(row);
      throw error;
    } finally {this.loadingMore.delete(row);}
  };
  retry = async (): Promise<void> => {
    if (!this.current()) return;
    this.opened = false;
    this.state = Object.freeze({phase: 'idle', continueItems: [], recent: [], shortReads: []});
    await this.load();
  };
  /** Only an item this Discover page shows, with available inventory, opens. */
  open = async (item: ReadingItem): Promise<boolean> => {
    const origin = this.origin;
    if (!origin || !this.current() || item.inventoryAvailable !== true) return false;
    const resident = this.state.continueItems.some(row => row.item.id === item.id && row.item.sourceId === item.sourceId)
      || this.state.recent.some(row => row.id === item.id && row.sourceId === item.sourceId)
      || this.state.shortReads.some(row => row.item.id === item.id && row.item.sourceId === item.sourceId);
    if (!resident) return false;
    try {return await openNativeReadBook(this.context, {libraryId: this.libraryId, item}, origin, this.life.signal);}
    catch (error) {
      const cancelledOpen = error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;
      if (this.life.signal.aborted || !this.current() || cancelledOpen) return false;
      throw error;
    }
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.life.abort(); this.client?.dispose(); this.client = undefined;
  };
}
