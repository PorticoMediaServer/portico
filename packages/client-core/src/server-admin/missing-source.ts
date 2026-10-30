import {listMissingTitles, parseMissingEmptied, type MissingTitlesPage, type MissingEmptied, type TitleIdentityApi} from '../title-identity.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';

export type MissingSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; page?: MissingTitlesPage; busy: boolean; removed?: MissingEmptied; error?: 'read'|'remove'}>;
const empty: MissingSnapshot = Object.freeze({phase: 'idle', busy: false});
const terminal = (error: unknown) => {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(code || '');
};

/** One owner/server lifetime. A removal is one bounded server pass, never an
 * automatic loop whose later passes could include newly missing titles. */
export class SettingsMissingSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private cursor = '';
  private removing = false;
  private ownsRead(read: AbortController) {return this.current() && this.read === read && !read.signal.aborted && !this.removing;}
  constructor(private readonly api: TitleIdentityApi, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): MissingSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<MissingSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
  private failure(error: unknown) {
    if (terminal(error)) {this.read?.abort();this.lifetime.abort();this.state = empty;for (const listener of this.listeners) listener();this.reportError(error);}
  }
  load = async (cursor = '', keepError?: MissingSnapshot['error']): Promise<void> => {
    if (!this.current() || this.removing) return;
    this.read?.abort();const read = new AbortController();this.read = read;
    this.publish({phase: 'loading', error: keepError});
    if (!this.ownsRead(read)) return;
    try {
      const page = await listMissingTitles(this.api, {cursor: cursor || undefined, limit: 100}, read.signal);
      if (!this.ownsRead(read)) return;
      this.cursor = cursor;this.publish({phase: 'ready', page});
    } catch (error) {
      if (this.ownsRead(read)) {this.publish({phase: 'error', error: 'read'});this.failure(error);}
    }
  };
  refresh = () => this.load(this.cursor);
  /** The dialog captures the actual page and count. A refreshed page invalidates
   * that confirmation; the owner must inspect and confirm its new count. */
  remove = async (page: MissingTitlesPage, typed: string, isAttemptCurrent: () => boolean = () => true): Promise<void> => {
    if (!this.current() || !isAttemptCurrent() || this.removing || this.state.phase !== 'ready' || this.state.page !== page || !page.total || !page.observedAt || typed !== String(page.total)) return;
    this.removing = true;this.read?.abort();this.read = undefined;this.publish({busy: true, removed: undefined, error: undefined});
    let sent = false, settled = false;
    try {
      if (!this.current() || !isAttemptCurrent() || !this.removing || this.state.page !== page) return;
      sent = true;
      const removed = parseMissingEmptied(await this.api.request(routePath('empty_missing_titles'), 'POST', {before: page.observedAt}, this.lifetime.signal));
      if (!this.current() || !isAttemptCurrent()) return;
      // A lost response is not retried. Read the current list and require another
      // explicit typed confirmation for any remaining work.
      settled = true; this.publish({removed, page: undefined, phase: 'idle'});
    } catch (error) {
      if (this.current() && isAttemptCurrent()) {settled = true;this.publish({page: undefined, phase: 'error', error: 'remove'});this.failure(error);}
    } finally {this.removing = false;if (sent && !settled && !isAttemptCurrent()) this.publish({page: undefined, phase: 'error', error: 'remove'});this.publish({busy: false});}
    if (this.current() && (isAttemptCurrent() || settled)) await this.load('', this.state.error);
  };
  dispose = () => {this.read?.abort();this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
