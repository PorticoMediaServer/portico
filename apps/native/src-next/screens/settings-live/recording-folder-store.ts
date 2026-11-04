import {channelRequestID} from '../../../../../packages/client-core/src/live-source-draft';
import type {RecordingRootIntent, RecordingRootStatus} from '../../../../../packages/client-core/src/server-admin/recording-root';

export type RecordingRootPort = Readonly<{
  read: (signal: AbortSignal) => Promise<RecordingRootStatus>;
  save: (intent: RecordingRootIntent, signal: AbortSignal) => Promise<RecordingRootStatus>;
  current: () => boolean;
}>;
export type RecordingRootState = Readonly<{
  loading: boolean; saving: boolean; status?: RecordingRootStatus; unsupported?: boolean;
  /** A write whose outcome is unknown: Choose stays off until Try again settles the same intent. */
  uncertain?: boolean;
  /** The server retired the write's operation key; the folder shown is a fresh read. */
  retired?: boolean;
  error?: unknown;
}>;

const facts = (error: unknown) => {
  const v = error && typeof error === 'object' ? error as Record<string, unknown> : {};
  return {code: typeof v.code === 'string' ? v.code : '', status: typeof v.status === 'number' ? v.status : undefined};
};
const refusedCodes = new Set(['invalid_request', 'invalid_console_request', 'console_conflict', 'revision_mismatch']);

/** live-owner 10/16c: the recordings folder. Choosing a folder saves it at once (it isn't one of
 * the Save bar's changes); a write is never reminted, and a retired one is reread, not replayed. */
export class NativeRecordingRoot {
  private state: RecordingRootState = Object.freeze({loading: false, saving: false});
  private readonly listeners = new Set<() => void>();
  private flight?: Readonly<{generation: number; abort: AbortController}>;
  private generation = 0;
  private pending?: RecordingRootIntent;
  private disposed = false;
  constructor(private readonly port: RecordingRootPort, private readonly operationId: () => string = channelRequestID) {}
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.state;
  private live = () => !this.disposed && this.port.current();
  private publish(next: RecordingRootState) {
    if (!this.live()) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  private begin() {
    this.flight?.abort.abort();
    const flight = Object.freeze({generation: ++this.generation, abort: new AbortController()});
    this.flight = flight;
    return flight;
  }
  private valid(flight: Readonly<{generation: number; abort: AbortController}>) {return this.live() && this.flight === flight && !flight.abort.signal.aborted;}
  /** The folder the server will use: the requested one when set, else the one in use. */
  folder = () => this.state.status ? this.state.status.requested || this.state.status.effective : '';
  canChoose = () => {const s = this.state; return this.live() && !s.loading && !s.saving && !s.uncertain && !!s.status && !s.status.operatorControlled && !this.pending;};
  async load(): Promise<void> {
    if (!this.live() || this.state.loading || this.state.saving || this.pending) return;
    const flight = this.begin();
    this.publish({...this.state, loading: true, error: undefined});
    try {
      const status = await this.port.read(flight.abort.signal);
      if (this.valid(flight)) this.publish({loading: false, saving: false, status, retired: this.state.retired});
    } catch (error) {
      if (!this.valid(flight)) return;
      const f = facts(error);
      if (f.status === 404 || f.code === 'not_found') this.publish({loading: false, saving: false, unsupported: true});
      else this.publish({...this.state, loading: false, error});
    }
  }
  choose(path: string): Promise<void> {
    const status = this.state.status;
    if (!this.canChoose() || !status || !path || new TextEncoder().encode(path).length > 1024 || path === this.folder()) return Promise.resolve();
    return this.send(Object.freeze({expectedRevision: status.revision, operationId: this.operationId(), path}));
  }
  /** Try again settles an unconfirmed write with its own intent; anything else rereads. */
  retry(): Promise<void> {return this.pending ? this.send(this.pending) : this.load();}
  private async send(intent: RecordingRootIntent): Promise<void> {
    if (!this.live() || this.state.loading || this.state.saving || this.pending && this.pending !== intent) return;
    this.pending = intent;
    const flight = this.begin();
    this.publish({...this.state, saving: true, error: undefined, retired: false});
    try {
      const status = await this.port.save(intent, flight.abort.signal);
      if (!this.valid(flight)) return;
      this.pending = undefined;
      this.publish({loading: false, saving: false, status});
    } catch (error) {
      if (!this.valid(flight)) return;
      const f = facts(error);
      if (f.code === 'console_expired' || f.code === 'console_capacity') {
        // These typed responses retire this operation key. Expiry doesn't prove it never
        // succeeded: reread, never remint or replay the write.
        this.pending = undefined;
        this.publish({...this.state, saving: false, uncertain: false, retired: true});
        await this.load();
        return;
      }
      const refused = [400, 409, 422].includes(f.status ?? 0) || refusedCodes.has(f.code);
      if (refused) this.pending = undefined;
      this.publish({...this.state, saving: false, uncertain: !refused, error});
    }
  }
  dispose() {
    this.disposed = true; this.pending = undefined;
    this.flight?.abort.abort(); this.flight = undefined;
    this.listeners.clear();
  }
}
