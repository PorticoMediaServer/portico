import type {MediaItem} from '../../../../../packages/client-core/src';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {NativeScreenLifetime, NativeScreenStore} from './lifetime';

type Api = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
export type NativeTogetherMediaRead = Readonly<{phase: 'loading'}> | Readonly<{phase: 'ready'; item: MediaItem}> | Readonly<{phase: 'error'; error: unknown}>;
type Snapshot = Readonly<{items: Readonly<Record<string, NativeTogetherMediaRead>>}>;

/** Transient records for the current title and native-visible queue rows only.
 * Nine records/two real in-flight reads; no queue, preference or playback store.
 * Aborted transports keep their slot until they actually finish. */
export class NativeTogetherMedia extends NativeScreenStore<Snapshot> {
  private disposed = false;
  private connected = false;
  private wanted: readonly string[] = [];
  private queue: string[] = [];
  private pending = new Map<string, AbortController>();
  private api: Api;
  private lifetime: NativeScreenLifetime;
  constructor(lifetime: NativeScreenLifetime, api: Api) {
    super({items: {}});
    lifetime.assert(); this.lifetime = lifetime; this.api = api;
  }
  select(ids: readonly string[]): void {
    if (this.disposed || !this.lifetime.current()) return;
    if (!this.connected) {this.connected = true; this.lifetime.signal.addEventListener('abort', this.dispose, {once: true});}
    const wanted = [...new Set(ids.filter(Boolean))].slice(0, 9);
    if (JSON.stringify(wanted) === JSON.stringify(this.wanted)) return;
    this.wanted = wanted;
    this.refresh();
  }
  private refresh(): void {
    const items: Record<string, NativeTogetherMediaRead> = {};
    this.queue = [];
    for (const [id, abort] of this.pending) if (!this.wanted.includes(id)) abort.abort();
    for (const id of this.wanted) {
      const prior = this.getSnapshot().items[id];
      items[id] = prior?.phase === 'ready' || prior?.phase === 'error' ? prior : {phase: 'loading'};
      if (items[id].phase === 'loading') this.queue.push(id);
    }
    this.publish({items}); this.pump();
  }
  retry = (id: string): void => {
    if (this.disposed || !this.lifetime.current() || !this.wanted.includes(id)) return;
    const items = {...this.getSnapshot().items}; delete items[id];
    this.publish({items}); this.refresh();
  };
  private pump(): void {
    while (!this.disposed && this.lifetime.current() && this.pending.size < 2) {
      const index = this.queue.findIndex(id => !this.pending.has(id));
      if (index < 0) return;
      const [id] = this.queue.splice(index, 1), abort = new AbortController();
      this.pending.set(id, abort);
      void this.lifetime.request(signal => this.api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(id)}), 'GET', undefined, signal).then(parseMediaItem), abort.signal)
        .then(item => {
          if (item.id !== id) throw new Error('native_together_media_identity_mismatch');
          this.accept(id, abort, {phase: 'ready', item});
        }, error => {this.accept(id, abort, {phase: 'error', error});})
        .catch(error => {this.accept(id, abort, {phase: 'error', error});})
        .finally(() => {
          if (this.pending.get(id) === abort) this.pending.delete(id);
          this.pump();
        });
    }
  }
  private accept(id: string, abort: AbortController, read: NativeTogetherMediaRead): void {
    if (this.disposed || abort.signal.aborted || !this.lifetime.current() || this.pending.get(id) !== abort || !this.wanted.includes(id)) return;
    this.queue = this.queue.filter(value => value !== id);
    this.publish({items: {...this.getSnapshot().items, [id]: read}});
  }
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.lifetime.signal.removeEventListener('abort', this.dispose);
    for (const abort of this.pending.values()) abort.abort();
    this.queue = []; this.wanted = [];
    this.publish({items: {}}); this.clearListeners();
  };
}
