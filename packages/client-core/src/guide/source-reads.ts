import type {ChannelApi} from '../channel-guide.ts';
import type {ChannelSource} from './types.ts';

export type ChannelSourcesSnapshot = Readonly<{sources: readonly ChannelSource[]; loading: boolean; failed: boolean}>;
const SOURCES_REUSE_MS = 60_000;
/** A viewer's rail probe, shared by mounts; unmounted/authenticated-away work is cancelled. */
export class ChannelSourcesStore {
  private snapshot: ChannelSourcesSnapshot;
  private listeners = new Set<() => void>();
  private controller?: AbortController;
  private completedAt?: number;
  private api: ChannelApi;
  private read: (api: ChannelApi, signal: AbortSignal) => Promise<readonly ChannelSource[]>;
  private now: () => number;
  constructor(api: ChannelApi, read: (api: ChannelApi, signal: AbortSignal) => Promise<readonly ChannelSource[]>,
    now: () => number = Date.now, initial: readonly ChannelSource[] = []) {
    this.api = api; this.read = read; this.now = now;
    this.snapshot = {sources: initial, loading: true, failed: false};
  }
  /** A replacement API means replacement credentials, even when its viewer name is unchanged. */
  bind(api: ChannelApi) {
    if (api === this.api) return;
    this.cancel(); this.api = api; this.completedAt = undefined;
    this.set({sources: [], loading: true, failed: false});
    if (this.listeners.size) this.load();
  }
  get = () => this.snapshot;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    if (this.listeners.size === 1 && !this.controller &&
      (this.snapshot.loading || this.completedAt === undefined || this.now() - this.completedAt >= SOURCES_REUSE_MS || this.now() < this.completedAt)) this.load();
    return () => { this.listeners.delete(fn); if (!this.listeners.size) this.cancel(); };
  };
  load = () => {
    this.cancel(); this.completedAt = undefined;
    this.set({...this.snapshot, loading: true, failed: false});
    if (!this.listeners.size) return;
    const controller = new AbortController(); this.controller = controller;
    Promise.resolve().then(() => {
      if (controller.signal.aborted) return [];
      return this.read(this.api, controller.signal);
    }).then(sources => {
      if (this.controller !== controller || controller.signal.aborted) return;
      this.completedAt = this.now();
      this.set({sources: [...sources].sort((a, b) => a.position - b.position), loading: false, failed: false});
    }, () => {
      if (this.controller === controller && !controller.signal.aborted) this.set({...this.snapshot, loading: false, failed: true});
    }).finally(() => { if (this.controller === controller) this.controller = undefined; });
  };
  /** Used when dropping an inactive viewer store from the small rail cache. */
  dispose() { this.cancel(); this.listeners.clear(); }
  private cancel() { const controller = this.controller; this.controller = undefined; controller?.abort(); }
  private set(next: ChannelSourcesSnapshot) { this.snapshot = next; for (const listener of [...this.listeners]) listener(); }
}
