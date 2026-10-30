import {parseUpdateReport, type UpdateReport} from '../administration.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';

/** The read and the pressed check answer with the same report shape. */
export type UpdatesApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
export type UpdatesSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; report?: UpdateReport; busy: boolean; error?: 'read'|'check'}>;
const empty: UpdatesSnapshot = Object.freeze({phase: 'idle', busy: false});
const terminal = (error: unknown) => {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(code ?? '');
};

/** One owner/server lifetime. The read never contacts the release feed; Check
 * now asks the server for exactly one check and takes its answer as the new
 * report. A pressed check is not replayed: a failure leaves the last read
 * report in place and says the check failed. */
export class SettingsUpdatesSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private write?: AbortController;
  constructor(private readonly api: UpdatesApi, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private current = () => !this.lifetime.signal.aborted && this.isCurrent();
  private ownsRead = (read: AbortController) => this.current() && this.read === read && !read.signal.aborted && !this.write;
  private ownsWrite = (write: AbortController) => this.current() && this.write === write && !write.signal.aborted;
  getSnapshot = (): UpdatesSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<UpdatesSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
  private failure(error: unknown, kind: 'read'|'check') {
    if (terminal(error)) {this.read?.abort();this.write?.abort();this.lifetime.abort();this.state = empty;for (const listener of this.listeners) listener();this.reportError(error);}
    else this.publish({phase: this.state.report ? 'ready' : 'error', busy: false, error: kind});
  }
  load = async (): Promise<void> => {
    if (!this.current() || this.write) return;
    this.read?.abort();const read = new AbortController();this.read = read;
    this.publish({phase: this.state.report ? 'ready' : 'loading', error: undefined});
    if (!this.ownsRead(read)) return;
    try {const report = parseUpdateReport(await this.api.request(routePath('get_admin_updates'), 'GET', undefined, read.signal));if (this.ownsRead(read)) this.publish({phase: 'ready', report, busy: false});}
    catch (error) {if (this.ownsRead(read)) this.failure(error, 'read');}
    finally {if (this.read === read) this.read = undefined;}
  };
  /** One pressed Check now. It is not retried, and its answer replaces the report.
   * A read already in flight is retired first, so its late answer cannot stand
   * over the pressed check. */
  check = async (): Promise<void> => {
    if (!this.current() || this.write) return;
    this.read?.abort();this.read = undefined;
    const write = new AbortController();this.write = write;
    this.publish({busy: true, error: undefined});
    if (!this.ownsWrite(write)) return;
    try {
      const report = parseUpdateReport(await this.api.request(routePath('post_admin_updates_check'), 'POST', undefined, write.signal));
      if (this.ownsWrite(write)) this.publish({phase: 'ready', report, busy: false});
    } catch (error) {if (this.ownsWrite(write)) this.failure(error, 'check');}
    finally {if (this.write === write) {this.write = undefined;if (this.current() && this.state.busy) this.publish({busy: false});}}
  };
  dispose = () => {this.read?.abort();this.write?.abort();this.read = undefined;this.write = undefined;this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
