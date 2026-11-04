import {
  movedPin, readLibraryNavigation, readPinOrder, saveLibraryNavigation, savePinOrder,
  setResourcePin, withLibraryPinned, withPinRetry,
  type LibraryNavigationPins, type PinOrder, type PinnedResource, type PinsApi,
} from '@core/library-pins.ts';

export type PinsSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error'; busy: boolean;
  libraries?: LibraryNavigationPins; resources?: PinOrder; error?: unknown;
}>;

/** Edit pins on web (phone-app 4c/4d, the same operations the native pin editor uses): library pins and the pinned
 * collections, views and playlists, bound to one viewer. It owns no visual composition and never reorders the
 * shell's library facts optimistically. */
export class PinsController {
  private readonly options: Readonly<{api: PinsApi; serverId: string; current: () => boolean;
    libraryIds: () => readonly string[]; requestId: () => Promise<string>}>;
  private readonly api: PinsApi;
  private readonly abort = new AbortController();
  private readonly listeners = new Set<() => void>();
  private state: PinsSnapshot = Object.freeze({phase: 'idle', busy: false});
  private pending?: Promise<void>;
  private disposed = false;
  constructor(options: PinsController['options']) {
    this.options = options;
    this.api = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.requireCurrent();
      const request = new AbortController(), cancel = () => request.abort();
      this.abort.signal.addEventListener('abort', cancel, {once: true}); signal?.addEventListener('abort', cancel, {once: true});
      if (this.abort.signal.aborted || signal?.aborted) cancel();
      try {
        if (request.signal.aborted) this.cancelled();
        const value = await options.api.request<T>(path, method, body, request.signal);
        this.requireCurrent(); if (request.signal.aborted) this.cancelled();
        return value;
      } finally {this.abort.signal.removeEventListener('abort', cancel); signal?.removeEventListener('abort', cancel);}
    }};
  }
  private cancelled(): never {throw Object.assign(new Error('pins_cancelled'), {name: 'AbortError', code: 'cancelled'});}
  private current(): boolean {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  private requireCurrent(): void {if (!this.current()) this.cancelled();}
  getSnapshot = (): PinsSnapshot => {this.current(); return this.state;};
  subscribe = (listener: () => void): (() => void) => {
    if (!this.current()) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  private publish(patch: Partial<PinsSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch}); for (const listener of [...this.listeners]) listener();
  }
  private async read(): Promise<void> {
    const [libraries, resources] = await Promise.all([readLibraryNavigation(this.api), readPinOrder(this.api, this.options.serverId)]);
    this.requireCurrent(); this.publish({phase: 'ready', libraries, resources, error: undefined});
  }
  load = (): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    return this.run(async () => {this.publish({phase: 'loading'}); await this.read();});
  };
  private run(work: () => Promise<void>): Promise<void> {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    const task = Promise.resolve().then(() => {this.requireCurrent(); return work();}).catch(error => {this.publish({phase: 'error', error});})
      .finally(() => {if (this.pending === task) this.pending = undefined; this.publish({busy: false});});
    this.pending = task; this.publish({busy: true, error: undefined}); return task;
  }
  private libraryCurrent(id: string): boolean {return this.current() && this.options.libraryIds().includes(id);}
  pinLibrary = (id: string, pinned: boolean): Promise<void> => {
    if (!this.libraryCurrent(id)) return Promise.resolve();
    return this.run(async () => {
      await withPinRetry(() => readLibraryNavigation(this.api), async navigation => {
        if (!this.libraryCurrent(id)) this.cancelled();
        return saveLibraryNavigation(this.api, navigation.revision, withLibraryPinned(navigation.pinnedLibraryIds, id, pinned));
      });
      await this.read();
    });
  };
  moveLibrary = (id: string, by: -1 | 1): Promise<void> => {
    if (!this.libraryCurrent(id)) return Promise.resolve();
    return this.run(async () => {
      await withPinRetry(() => readLibraryNavigation(this.api), async navigation => {
        if (!this.libraryCurrent(id)) this.cancelled();
        const index = navigation.pinnedLibraryIds.indexOf(id);
        return index < 0 ? navigation : saveLibraryNavigation(this.api, navigation.revision, movedPin(navigation.pinnedLibraryIds, index, by));
      });
      await this.read();
    });
  };
  moveResource = (entry: PinnedResource, by: -1 | 1): Promise<void> => {
    if (!this.current() || !this.state.resources?.order.includes(entry)) return Promise.resolve();
    return this.run(async () => {
      await withPinRetry(() => readPinOrder(this.api, this.options.serverId), async pins => {
        const index = pins.order.findIndex(value => value.kind === entry.kind && value.id === entry.id);
        return index < 0 ? pins : savePinOrder(this.api, this.options.serverId, pins.revision, movedPin(pins.order, index, by));
      });
      await this.read();
    });
  };
  unpinResource = (entry: PinnedResource): Promise<void> => {
    if (!this.current() || !this.state.resources?.order.includes(entry)) return Promise.resolve();
    return this.run(async () => {
      await withPinRetry(() => readPinOrder(this.api, this.options.serverId), async pins => {
        const live = pins.order.find(value => value.kind === entry.kind && value.id === entry.id);
        if (!live) return;
        const operationId = await this.options.requestId(); this.requireCurrent();
        await setResourcePin(this.api, live.kind, live.id, false, live.pinRevision, operationId);
      });
      await this.read();
    });
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.abort.abort(); this.listeners.clear();
    this.state = Object.freeze({phase: 'idle', busy: false});
  };
}
