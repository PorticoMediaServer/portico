import type {HttpLocalApi} from '../../../../../packages/client-core/src';
import {PlaybackOptionsClient} from '../../../../../packages/client-core/src/playback-v1/options';
import {localV1Http} from '../../../../../packages/client-core/src/playback-v1/local-http';
import type {PlaybackOptions} from '../../../../../packages/client-core/src/playback-v1/types';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativePlaybackAdapter} from '../../platform/playback/types';

export type NativeWatchOptionsSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'failed'; fence?: PlaybackFence; options?: PlaybackOptions; error?: unknown}>;

/** UI request lifetime only. The genuine playback adapter remains the sole
 * session/selection owner. A panel reads choices for its exact current version,
 * and a late response cannot replace another source's rows. */
export class NativeWatchPanelOptions {
  private state: NativeWatchOptionsSnapshot = Object.freeze({phase: 'idle'});
  private listeners = new Set<() => void>();
  private pending?: AbortController;
  private disposed = false;
  private readonly client: PlaybackOptionsClient;
  private readonly off: () => void;
  private readonly playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'subscribe'>;
  constructor(api: HttpLocalApi, playback: Pick<NativePlaybackAdapter, 'getSnapshot' | 'subscribe'>) {
    this.playback = playback;
    this.client = new PlaybackOptionsClient(localV1Http(api));
    this.off = playback.subscribe(() => {if (this.state.fence && !this.current(this.state.fence)) this.clear();});
  }
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.state;
  private current(fence: PlaybackFence): boolean {
    const source = this.playback.getSnapshot();
    return !this.disposed && !source.retired && fence.kind === 'queue' && sameFence(source.fence, fence)
      && sameFence(source.dock.local?.fence, fence) && source.dock.local?.expanded === true;
  }
  private publish(value: NativeWatchOptionsSnapshot): void {this.state = Object.freeze(value); for (const listener of [...this.listeners]) listener();}
  load = async (fence: PlaybackFence): Promise<void> => {
    if (!this.current(fence) || fence.kind !== 'queue') return;
    this.pending?.abort();
    const request = new AbortController(); this.pending = request;
    this.publish({phase: 'loading', fence});
    try {
      const options = await this.client.options(fence.itemId, {versionId: fence.sourceId}, request.signal);
      if (request.signal.aborted || this.pending !== request || !this.current(fence)) return;
      if (options.itemId !== fence.itemId || new Set(options.versions.map(version => version.id)).size !== options.versions.length
        || options.versions.filter(version => version.id === fence.sourceId).length !== 1) throw new Error('Invalid playback choices.');
      this.publish({phase: 'ready', fence, options});
    } catch (error) {
      if (!request.signal.aborted && this.pending === request && this.current(fence)) this.publish({phase: 'failed', fence, error});
    } finally {if (this.pending === request) this.pending = undefined;}
  };
  clear = (): void => {this.pending?.abort(); this.pending = undefined; if (this.state.phase !== 'idle') this.publish({phase: 'idle'});};
  dispose = (): void => {if (this.disposed) return; this.clear(); this.disposed = true; this.off(); this.listeners.clear();};
}
