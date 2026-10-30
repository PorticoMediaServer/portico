import type {HomeRow} from '../../../../packages/client-core/src/home';
import type {HomeService} from '../../../../packages/client-core/src/home-service';

type Request = {rowId: string; cursor: string; waitingId: string; hostId: string; before: ReadonlySet<string>; loading: boolean; failed: boolean;
  fulfilled?: Readonly<{waitingId: string; firstItemId?: string}>};
/** Data-request association only. Native Row and its guide continue to own every focus move. */
export class NativeHomePaging {
  private rows = new Map<string, Request>();
  private revision = 0;
  private listeners = new Set<() => void>();
  private alive = true;
  private home: Pick<HomeService, 'more'>;
  private current: () => boolean;
  private reportError: (error: unknown) => void;
  constructor(home: Pick<HomeService, 'more'>, current: () => boolean, reportError: (error: unknown) => void) {this.home = home; this.current = current; this.reportError = reportError;}
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.revision;
  private publish() {this.revision++; for (const listener of this.listeners) listener();}
  /** Keep pending appended members from rendering before their actual receipt can alias the waiting host.
   * Removals and the latest capability-bearing objects always apply immediately. */
  present(row: HomeRow): HomeRow {
    const pending = this.rows.get(row.id);
    if (!pending?.loading) return row;
    const entries = row.entries.filter(entry => pending.before.has(entry.id));
    return entries.length === row.entries.length ? row : {...row, entries};
  }
  state(rowId: string): Readonly<Request> | undefined {return this.rows.get(rowId);}
  async request(row: HomeRow, waitingId: string): Promise<void> {
    if (!this.alive || !this.current() || !row.nextCursor || this.rows.get(row.id)?.loading) return;
    const previous = this.rows.get(row.id);
    const hostId = previous?.fulfilled && previous.fulfilled.firstItemId === undefined ? previous.hostId : waitingId;
    const pending: Request = {rowId: row.id, cursor: row.nextCursor, waitingId, hostId, before: new Set(row.entries.map(entry => entry.id)), loading: true, failed: false};
    this.rows.set(row.id, pending); this.publish();
    try {
      const outcome = await this.home.more(row.id);
      if (!this.alive || !this.current() || this.rows.get(row.id) !== pending) return;
      pending.loading = false;
      if (outcome.rowId === pending.rowId && outcome.requestedCursor === pending.cursor && outcome.applied) {
        pending.fulfilled = {waitingId: pending.waitingId, ...(outcome.firstItemId === null ? {} : {firstItemId: outcome.firstItemId})};
      } else pending.failed = outcome.status === 'failed' || outcome.status === 'stale';
      this.publish();
    } catch (error) {
      if (!this.alive || !this.current() || this.rows.get(row.id) !== pending) return;
      pending.loading = false; pending.failed = true; this.reportError(error); this.publish();
    }
  }
  activate() {this.alive = true; return () => {this.alive = false; this.rows.clear(); this.revision++;};}
  dispose() {this.alive = false; this.rows.clear(); this.listeners.clear();}
}
