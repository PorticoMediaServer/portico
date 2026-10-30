import type {LocalSession, ReadingClient, ReadingContinueItem, ReadingItem, ReadingSeriesOverview} from '../../../../../packages/client-core/src';
import {ReadingError} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeReadOrigin, openNativeReadBook, type NativeReadOrigin} from './eligibility';

export type NativeReadSeriesState = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  items: readonly ReadingItem[];
  nextCursor?: string;
  /** The signed series summary when the server publishes it. */
  overview?: ReadingSeriesOverview;
  /** The one real continue row for this series, when it exists. */
  continued?: ReadingContinueItem;
  /** The first available volume's declared reading direction, when it publishes one. */
  direction?: 'ltr' | 'rtl' | 'vertical';
  error?: unknown;
  /** A later page failed; the volumes already loaded stay on screen. */
  moreError?: unknown;
  morePending?: boolean;
}>;
type SeriesRoute = Extract<NativeShellRoute, {kind: 'read-series'}>;

const refused = () => Object.assign(new Error('native_read_series_refused'), {name: 'AbortError', code: 'cancelled'});
const cancelled = (error: unknown): boolean => error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;

/** Signed `reading.series` membership and order, plus the canonical continue
 * row for this series when it exists. Read count, per-volume finished state,
 * dates and page counts have no producer here, so they stay absent. */
export class NativeReadSeries {
  private readonly context: NativeShellBootstrapContext;
  private readonly libraryId: string;
  private readonly seriesId: string;
  private readonly route: SeriesRoute;
  private readonly comic: boolean;
  private origin?: NativeReadOrigin;
  private captured?: Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']}>;
  private authority?: ShellAuthority;
  private life?: AbortController;
  private client?: ReadingClient;
  private state: NativeReadSeriesState = Object.freeze({phase: 'idle', items: []});
  private readonly listeners = new Set<() => void>();
  private flight = 0;
  /** The one open command and the one next-page request this mounted page owns. */
  private command?: object;
  private pageTicket?: object;
  private disposed = false;

  constructor(context: NativeShellBootstrapContext, route: SeriesRoute, comic: boolean) {
    this.context = context; this.libraryId = route.libraryId; this.seriesId = route.seriesId; this.route = route; this.comic = comic;
  }
  /** Run from the page's layout effect with the exact rendered route. */
  arm = (): boolean => {
    if (this.disposed) return false;
    const selected = this.context.session(), origin = nativeReadOrigin(this.context, this.route);
    if (!selected || !origin) return false;
    this.retire();
    this.origin = origin;
    this.authority = this.context.authority();
    this.captured = Object.freeze({sessionFamilyId: selected.sessionFamilyId, viewer: Object.freeze({...selected.viewer})});
    this.state = Object.freeze({phase: 'idle', items: []});
    this.emit();
    return true;
  };
  release = (): void => {
    this.retire();
    this.origin = undefined; this.captured = undefined; this.authority = undefined;
  };
  private retire(): void {
    this.flight++;
    this.life?.abort();
    this.client?.dispose();
    this.client = undefined; this.life = undefined;
    this.command = undefined; this.pageTicket = undefined;
  }
  private current = (): boolean => {
    const origin = this.origin, captured = this.captured, selected = this.context.session();
    if (this.disposed || !origin || !captured || !this.authority || !selected || !this.context.current()) return false;
    if (selected.sessionFamilyId !== captured.sessionFamilyId || !sameAuthority(this.authority, this.context.authority())) return false;
    const snapshot = this.context.navigation.getSnapshot();
    return snapshot.route === origin.route && snapshot.revision === origin.revision;
  };
  private reading(): ReadingClient {
    const captured = this.captured;
    if (!captured || !this.current()) throw refused();
    this.life ??= new AbortController();
    return this.client ??= this.context.api.reading({viewer: captured.viewer, isCurrent: this.current, signal: this.life.signal});
  }
  private publish(next: NativeReadSeriesState): void {
    if (this.disposed || !this.current()) return;
    this.state = Object.freeze(next);
    this.emit();
  }
  private emit(): void {for (const listener of [...this.listeners]) listener();}
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => {this.listeners.delete(listener);};
  };
  getSnapshot = (): NativeReadSeriesState => this.state;
  load = async (): Promise<void> => {
    if (!this.current()) return;
    const flight = ++this.flight;
    // H14: a reload of the same series keeps its painted books until the new reading lands.
    this.publish({...this.state, phase: 'loading', error: undefined});
    try {
      const client = this.reading(), signal = this.life!.signal;
      const page = await client.series(this.libraryId, this.seriesId, {limit: 60}, signal);
      if (flight !== this.flight || !this.current()) return;
      let continued: ReadingContinueItem | undefined;
      try {
        const rows = await client.continue({libraryId: this.libraryId, limit: 50}, signal);
        if (flight !== this.flight || !this.current()) return;
        continued = rows.items.find(row => row.item.series?.id === this.seriesId && row.item.inventoryAvailable && !row.progress.stale && row.progress.location.progression > 0);
      } catch (error) {
        if (cancelled(error)) return;
        // The series still lists without a continue row.
      }
      if (flight !== this.flight || !this.current()) return;
      // One authorized preview of the volume the page would read gives its real
      // declared direction (ComicInfo.xml and only that). A book or periodical
      // series opens no extra session for an absent direction. It is closed
      // before the page can become ready, and a close refusal fails the page
      // rather than publishing a projection the server still holds open.
      const previewItem = this.comic ? continued?.item ?? page.items.find(value => value.inventoryAvailable) : undefined;
      let direction: 'ltr' | 'rtl' | 'vertical' | undefined;
      if (previewItem?.format === 'cbz') {
        try {
          const session = await client.open({itemId: previewItem.id, sourceId: previewItem.sourceId}, signal);
          let closeError: unknown;
          try {
            const manifest = session.manifest;
            // Only the exact signed witness may yield a direction: the same item
            // and source, and a real comic publication at both ends.
            if (manifest.itemId === previewItem.id && manifest.sourceId === previewItem.sourceId && manifest.format === 'cbz') {
              const declared = manifest.metadata.direction;
              if (declared === 'ltr' || declared === 'rtl' || declared === 'vertical') direction = declared;
            }
          } finally {try {await session.close();} catch (error) {closeError = error;}}
          if (flight !== this.flight || !this.current()) return;
          if (closeError) {this.publish({phase: 'error', items: [], error: closeError}); return;}
        } catch (error) {
          if (cancelled(error)) return;
          // No direction is drawn when the preview cannot be opened.
        }
      }
      if (flight !== this.flight || !this.current()) return;
      this.publish({phase: 'ready', items: page.items, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {}),
        ...(page.overview ? {overview: page.overview} : {}), ...(continued ? {continued} : {}),
        ...(direction ? {direction} : {})});
    } catch (error) {
      if (flight !== this.flight || !this.current() || cancelled(error)) return;
      this.publish({phase: 'error', items: [], error});
    }
  };
  /** The next page only appends for the exact flight and cursor that asked for
   * it; a retry retires the flight and its result is dropped. */
  more = async (): Promise<void> => {
    const cursor = this.state.nextCursor;
    if (!cursor || this.pageTicket || this.state.phase !== 'ready' || !this.current()) return;
    const flight = this.flight, ticket = {};
    this.pageTicket = ticket;
    this.publish({...this.state, moreError: undefined, morePending: true});
    try {
      const client = this.reading(), signal = this.life!.signal;
      const page = await client.series(this.libraryId, this.seriesId, {cursor, limit: 60}, signal);
      if (flight !== this.flight || !this.current() || this.state.nextCursor !== cursor) return;
      this.publish({...this.state, phase: 'ready', items: [...this.state.items, ...page.items],
        ...(page.nextCursor ? {nextCursor: page.nextCursor} : {nextCursor: undefined}),
        ...(page.overview ? {overview: page.overview} : {}), moreError: undefined, morePending: false});
    } catch (error) {
      if (flight !== this.flight || !this.current() || cancelled(error)) return;
      this.publish({...this.state, moreError: error, morePending: false});
    } finally {if (this.pageTicket === ticket) this.pageTicket = undefined;}
  };
  retry = (): Promise<void> => this.load();
  itemLifetime = (item: ReadingItem): Readonly<{signal: AbortSignal; isCurrent: () => boolean}> | undefined => {
    const life = this.life;
    const current = () => this.life === life && this.current() && this.state.phase === 'ready' && this.state.items.includes(item);
    return life && current() ? Object.freeze({signal: life.signal, isCurrent: current}) : undefined;
  };
  /** Only a volume this page actually listed or its own continue row, with
   * available inventory, opens. The item is the one the page drew; it is never
   * reselected from newer state at press time. */
  open = async (item: ReadingItem): Promise<boolean> => {
    const origin = this.origin, life = this.life;
    if (this.command || !origin || !life || !this.current() || this.state.phase !== 'ready' || item.inventoryAvailable !== true) return false;
    const listed = this.state.items.some(candidate => candidate.id === item.id && candidate.sourceId === item.sourceId)
      || this.state.continued !== undefined && this.state.continued.item.id === item.id
        && this.state.continued.item.sourceId === item.sourceId && this.state.continued.item.inventoryAvailable === true;
    if (!listed) return false;
    const ticket = {};
    this.command = ticket;
    try {return await openNativeReadBook(this.context, {libraryId: this.libraryId, item}, origin, life.signal);}
    finally {if (this.command === ticket) this.command = undefined;}
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.release();
    this.listeners.clear();
  };
}
