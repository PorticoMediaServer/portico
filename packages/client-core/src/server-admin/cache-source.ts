import {decodePostEmptyDataCacheResponse} from '../../../contracts/src/decoders.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
export type CacheTransport = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

type Snapshot = Readonly<{phase: 'ready' | 'busy' | 'scheduled' | 'uncertain'}>;
const ready: Snapshot = Object.freeze({phase: 'ready'});
const terminal = (error: unknown) => {
  const value = error && typeof error === 'object' ? error as {status?: number; code?: string} : {};
  return value.status === 401 || value.status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(value.code ?? '');
};

/** The endpoint schedules one graceful restart. It has no operation receipt:
 * once submitted, uncertainty must never silently repeat that restart. */
export class SettingsCacheSource {
  private state = ready;
  private readonly lifetime = new AbortController();
  private readonly listeners = new Set<() => void>();
  private flight?: object;
  private submitted = false;
  constructor(private readonly api: CacheTransport, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): Snapshot => this.current() ? this.state : ready;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(phase: Snapshot['phase']) {if (this.current()) {this.state = Object.freeze({phase});for (const listener of this.listeners) listener();}}
  empty = async (confirmed: () => boolean): Promise<void> => {
    if (!this.current() || !confirmed() || this.flight || this.submitted) return;
    const attempt = {};this.flight = attempt;
    const current = () => this.current() && this.flight === attempt;
    this.publish('busy');
    try {
      if (!current() || !confirmed()) return;
      this.submitted = true;
      const raw = await this.api.request<unknown>(routePath('post_empty_data_cache'), 'POST', undefined, this.lifetime.signal);
      if (!current()) return;
      if (!confirmed()) {this.publish('uncertain');return;}
      const receipt = decodePostEmptyDataCacheResponse(raw);
      if (receipt.scheduled !== true) throw new Error('cache_restart_unconfirmed');
      this.publish('scheduled');
    } catch (error) {
      if (!current()) return;
      if (terminal(error)) {this.dispose();this.reportError(error);}
      else this.publish('uncertain');
    } finally {
      if (current()) {
        if (this.state.phase === 'busy') this.publish(this.submitted ? 'uncertain' : 'ready');
        this.flight = undefined;
      }
    }
  };
  dispose = () => {this.lifetime.abort();this.flight = undefined;this.listeners.clear();this.state = ready;};
}
