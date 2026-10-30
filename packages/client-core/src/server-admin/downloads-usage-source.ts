import {decodeGetDownloadUsageResponse} from '../../../contracts/src/decoders.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
import type {GetDownloadUsageResponse} from '../../../contracts/src/types.ts';
import type {CacheTransport as StorageTransport} from './cache-source.ts';

export type DownloadsUsageRow = Readonly<GetDownloadUsageResponse['profiles'][number]>;
export type UsagePage = Readonly<{rows: readonly DownloadsUsageRow[]; next: string}>;
export type DownloadsUsageSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; page?: UsagePage; previous: boolean}>;
const empty: DownloadsUsageSnapshot = Object.freeze({phase: 'idle', previous: false});

/** One explicit, bounded owner-page read. No claims or offline permissions are changed. */
export class DownloadsUsageSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private flight?: AbortController;
  private cursor = '';
  private lastDirection: 'initial' | 'next' | 'previous' = 'initial';
  private history: string[] = [];
  constructor(private readonly api: StorageTransport, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private owns(request: AbortController) {return this.current() && this.flight === request && !request.signal.aborted;}
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: DownloadsUsageSnapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of this.listeners) listener();}}
  load = async (direction: 'initial' | 'retry' | 'refresh' | 'next' | 'previous' = 'initial'): Promise<void> => {
    const retry = direction === 'retry' || direction === 'refresh';
    if (direction === 'refresh') direction = 'initial';
    if (direction === 'retry') direction = this.lastDirection;
    if (!this.current() || this.flight || direction === 'initial' && !retry && this.state.phase !== 'idle') return;
    const next = direction === 'next' ? this.state.page?.next : direction === 'previous' ? this.history.at(-1) : this.cursor;
    if (next === undefined || direction === 'next' && !next || direction === 'previous' && !this.history.length) return;
    this.lastDirection = direction;
    const request = new AbortController();this.flight = request;
    // dispose() aborts this exact flight together with the source lifetime.
    const signal = request.signal;
    this.publish({...this.state, phase: 'loading'});
    try {
      if (!this.owns(request)) return;
      const raw = await this.api.request<unknown>(routePath('get_download_usage') + '?scope=server&limit=64&cursor=' + encodeURIComponent(next), 'GET', undefined, signal);
      if (!this.owns(request)) return;
      const result = decodeGetDownloadUsageResponse(raw);
      if (result.profiles.length > 64 || result.profilesNextCursor.length > 1024 || result.profilesNextCursor && result.profilesNextCursor === next) throw new Error('invalid_download_usage_page');
      if (direction === 'next') this.history.push(this.cursor);
      else if (direction === 'previous') this.history.pop();
      this.cursor = next;
      const rows = Object.freeze(result.profiles.map(row => Object.freeze({...row})));
      this.publish({phase: 'ready', previous: this.history.length > 0, page: Object.freeze({rows, next: result.profilesNextCursor})});
    } catch (error) {
      if (this.owns(request)) {
        const refusal = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
        if (refusal.status === 401 || refusal.status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(refusal.code ?? '')) {
          this.publish(empty);this.dispose();this.reportError(new Error('download_usage_denied'));
        } else this.publish({...this.state, phase: 'error'});
      }
    }
    finally {if (this.flight === request) this.flight = undefined;}
  };
  refresh = () => this.load('refresh');
  dispose() {this.lifetime.abort();this.flight?.abort();this.flight = undefined;this.state = empty;this.history = [];this.listeners.clear();}
}
