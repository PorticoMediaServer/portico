import {emptyServerCache, readCachePreview, readServerStorage, type ServerStorage} from '@core/server-admin/server-storage.ts';
import type {ServerRequester} from '@core/query/server-api.ts';

export type StorageTransport = ServerRequester;
export type StorageSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; storage?: ServerStorage}>;
const idle: StorageSnapshot = Object.freeze({phase: 'idle'});

/**
 * The Storage page's reads on the rebuilt server (#3208), shared by its rows: the data folder and what uses space
 * (`GET /v1/admin/storage`). A refused read ends the
 * page's lifetime and is reported, as every owner read is.
 */
export class SettingsStorageSource {
  private state: StorageSnapshot = idle;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  constructor(private readonly api: StorageTransport, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}) {}
  getSnapshot = (): StorageSnapshot => this.current() ? this.state : idle;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private publish(patch: Partial<StorageSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of [...this.listeners]) listener();}}
  private refused(error: unknown) {
    const failure = error && typeof error === 'object' ? error as {status?: number} : {};
    if (failure.status !== 401 && failure.status !== 403) return false;
    this.dispose(); this.reportError(error); return true;
  }
  load = (): Promise<void> => {
    if (!this.current() || this.reading || this.state.phase === 'ready') return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      this.publish({phase: 'loading'});
      try {
        const storage = await readServerStorage(this.api, this.lifetime.signal);
        this.publish({phase: 'ready', storage});
      } catch (error) {if (this.current() && !this.refused(error)) this.publish({phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task;
    return task;
  };
  retry = (): Promise<void> => {this.state = idle; return this.load();};
  /** What Empty cache would free now. */
  preview = (): Promise<number> => readCachePreview(this.api, this.lifetime.signal);
  /** Empties the cache, then reads the usage again. Answers what was freed. */
  emptyCache = async (): Promise<number> => {
    const freed = await emptyServerCache(this.api, this.lifetime.signal);
    if (this.current()) void this.retry();
    return freed;
  };
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.listeners.clear();this.state = idle;}}
}
