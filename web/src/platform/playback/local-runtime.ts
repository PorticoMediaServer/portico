import {localPlaybackScope, sameLocalScope, type LocalPlaybackScope} from '../../../../packages/player-core/src/index.ts';
import type {LocalPlayerLoadContext} from '@core/playback-v1/local-port.ts';
import {captureLocalQueue, WebLocalQueue, type WebLocalQueuePreparation, type WebLocalQueueResource, type WebLocalQueueSnapshot} from './local-queue.ts';
import {PlayerSlot, type PlaybackMediaPort} from './web-playback.ts';
import type {HlsModule, VideoLike, WebMediaPlayerOptions} from './web-media-player.ts';

/** A player and queue for downloads alone, beside the online runtime.
 *
 * It reuses the same `PlayerSlot` and `WebLocalQueue` the shell player uses and the
 * platform's own `createPlayer` factory. It has no `WebPlaybackSource`, no V1 queue,
 * session, item-listening or capability route, and it mints no viewer fence: the only
 * authority it knows is the download scope the app already validated. Anything the
 * platform cannot report locally stays unknown here; nothing is borrowed from a server.
 *
 * One element, one active queue. A replacement retires the exact queue and awaits its
 * owned preparation release before the slot is used again; dispose returns the same task
 * until it finishes, and a failed cleanup keeps custody so a retry can finish it.
 */
export type LocalRuntimeEnvironment = Readonly<{
  createPlayer?: (video: VideoLike, options: WebMediaPlayerOptions) => PlaybackMediaPort;
  loadHls?: () => Promise<HlsModule>;
  nativeHls?: 'first' | 'fallback' | 'never';
  playerTimers?: WebMediaPlayerOptions['timers'];
}>;

export type LocalRuntimeOptions = Readonly<{
  scope: LocalPlaybackScope;
  environment: LocalRuntimeEnvironment;
  /** False once the persisted authority is spent or the window is leaving. */
  isCurrent: () => boolean;
  /** Retires the app-owned preparation this runtime let go of. */
  release: () => Promise<void>;
  reportError: (error: unknown) => void;
}>;

type Active = Readonly<{
  token: number;
  queue: WebLocalQueue;
  resource: WebLocalQueueResource;
  /** The actual owned preparation release; the queue's own release path shares it. */
  release: () => Promise<void>;
  off: () => void;
}>;

export class LocalPlaybackRuntime {
  readonly slot = new PlayerSlot();
  private player?: PlaybackMediaPort;
  private video?: VideoLike;
  private attachment?: Readonly<{video: VideoLike; token: number}>;
  private attachmentSerial = 0;
  private active?: Active;
  private serial = 0;
  private run = 0;
  private lifecycle: 'live' | 'retiring' | 'retired' = 'live';
  private activeRetire?: Readonly<{active: Active; task: Promise<void>}>;
  private readonly owned = new Map<WebLocalQueueResource, () => Promise<void>>();
  private retirement?: Promise<void>;
  private readonly listeners = new Set<() => void>();
  private readonly scope: LocalPlaybackScope;

  constructor(private readonly options: LocalRuntimeOptions) {
    this.scope = localPlaybackScope(options.scope);
  }

  /** The one element this runtime plays into. Repeating the same element is idempotent;
   * a different element while one is attached is refused, not replaced. */
  mount(video: VideoLike): () => void {
    this.assertAlive();
    if (this.attachment) {
      if (this.attachment.video !== video) throw new Error('playback-element-replaced');
      return () => {};
    }
    const create = this.options.environment.createPlayer;
    if (!create) throw new Error('local_playback_unavailable');
    const token = ++this.attachmentSerial;
    this.video = video;
    this.player = create(video, {loadHls: this.options.environment.loadHls, nativeHls: this.options.environment.nativeHls, timers: this.options.environment.playerTimers});
    this.slot.mount(this.player);
    this.attachment = {video, token};
    return () => {
      if (this.attachment?.token !== token) return;
      this.attachment = undefined;
      this.video = undefined;
      this.player = undefined;
      this.slot.unmount();
    };
  }

  get mounted(): boolean { return !!this.attachment && this.lifecycle === 'live'; }

  /** Start one receipt-verified preparation. The operation is fenced before its first await,
   * the resource is in custody from entry, and only the newest start may adopt. */
  async play(resource: WebLocalQueueResource, admission: Readonly<{signal: AbortSignal; isCurrent(): boolean}>): Promise<void> {
    const token = ++this.serial;
    const release = this.custody(resource);
    try {
      if (this.lifecycle !== 'live' || !this.options.isCurrent() || !this.attachment || !this.slot.canLoadLocal) throw new Error('local_playback_unavailable');
      if (admission.signal.aborted || !admission.isCurrent()) throw new DOMException('Local selection retired', 'AbortError');
      if (!sameLocalScope(resource.preparation.scope, this.scope)) throw new Error('local_playback_unavailable');
      const preparation = captureLocalQueue(resource.preparation, this.scope);
      // Retire whatever is active, re-checking the fence after each await so a concurrent
      // start cannot slip between the retirement and this adoption.
      for (let attempt = 0; attempt < 3 && this.active; ++attempt) {
        await this.retireActive(this.active!);
        if (token !== this.serial || this.lifecycle !== 'live' || !this.options.isCurrent()) throw new Error('local_playback_unavailable');
      }
      if (this.active) throw new Error('local_playback_unavailable');
      if (token !== this.serial || this.lifecycle !== 'live' || !this.options.isCurrent()) throw new Error('local_playback_unavailable');
      let queue: WebLocalQueue;
      queue = new WebLocalQueue({
        scope: this.scope, run: ++this.run, port: this.slot, preparation,
        // The action's lifetime only admits the start; the runtime's own scope life keeps it.
        isCurrent: () => this.lifecycle === 'live' && this.options.isCurrent(),
        release,
        reportError: error => { if (this.lifecycle === 'live') this.options.reportError(error); },
      });
      const off = queue.subscribe(() => this.notify());
      const active: Active = {token, queue, resource, release, off};
      this.active = active;
      this.notify();
      await queue.start({signal: admission.signal, isCurrent: () => this.lifecycle === 'live' && this.options.isCurrent() && admission.isCurrent()}, {autoplay: true});
      if (token !== this.serial || this.active !== active || this.lifecycle !== 'live') throw new Error('local_playback_unavailable');
    } catch (error) {
      // Clean the exact queue this operation owns; never the newer one that superseded it.
      const mine = this.active && this.active.token === token ? this.active : undefined;
      if (mine) await this.retireActive(mine).catch(retireError => this.options.reportError(retireError));
      await this.settle(resource).catch(releaseError => this.options.reportError(releaseError));
      throw error;
    }
  }

  /** Local engine commands only, fenced by the queue's own snapshot. */
  resume(): void { const active = this.active, fence = active?.queue.getSnapshot().fence; if (active && fence) active.queue.resume(fence); }
  pause(): void { const active = this.active, fence = active?.queue.getSnapshot().fence; if (active && fence) active.queue.pause(fence); }
  seek(seconds: number): void { const active = this.active, fence = active?.queue.getSnapshot().fence; if (active && fence) active.queue.seek(fence, seconds); }

  getSnapshot(): WebLocalQueueSnapshot | undefined { return this.active?.queue.getSnapshot(); }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  /** Terminal. Drains every resource held since its play entry — including starts that never
   * adopted — before the element is handed on; repeated calls share the same task. */
  dispose(): Promise<void> {
    if (this.retirement) return this.retirement;
    if (this.lifecycle === 'retired') return Promise.resolve();
    this.lifecycle = 'retiring';
    const task = (async () => {
      try {
        const active = this.active;
        if (active) await this.retireActive(active);
        for (const resource of [...this.owned.keys()]) await this.settle(resource);
        if (this.owned.size > 0) throw new Error('local_release_pending');
        this.attachment = undefined;
        this.video = undefined;
        this.player = undefined;
        try { this.slot.unmount(); } catch { /* already unmounted */ }
        this.lifecycle = 'retired';
      } catch (error) {
        // Still terminal: no new play or mount. The same task is retried on the next dispose.
        this.retirement = undefined;
        this.options.reportError(error);
        throw error;
      }
    })();
    this.retirement = task;
    return task;
  }

  async stop(): Promise<void> { await this.dispose(); }

  private alive(): boolean { return this.lifecycle === 'live' && this.options.isCurrent(); }

  private assertAlive(): void { if (!this.alive()) throw new Error('local_playback_unavailable'); }

  /** Every resource is in custody from the moment play is entered, before any await. */
  private custody(resource: WebLocalQueueResource): () => Promise<void> {
    const held = this.owned.get(resource);
    if (held) return held;
    const release = retryableOnce(() => resource.release());
    this.owned.set(resource, release);
    return release;
  }

  private async settle(resource: WebLocalQueueResource): Promise<void> {
    const release = this.owned.get(resource);
    if (!release) return;
    await release();
    this.owned.delete(resource);
  }

  /** Retire one exact queue; deduped per owner so an older failure cannot touch a newer one. */
  private retireActive(active: Active): Promise<void> {
    if (this.activeRetire?.active === active) return this.activeRetire.task;
    const task = (async () => {
      try { active.off(); } catch { /* no longer subscribed */ }
      try { active.queue.dispose(); } catch { /* release below still runs */ }
      await this.settle(active.resource);
      if (this.active === active) { this.active = undefined; this.notify(); }
    })();
    const tracked = task.finally(() => { if (this.activeRetire?.task === tracked) this.activeRetire = undefined; });
    this.activeRetire = {active, task: tracked};
    return tracked;
  }

  private notify(): void { for (const listener of [...this.listeners]) listener(); }
}

function retryableOnce(run: () => Promise<void>): () => Promise<void> {
  let task: Promise<void> | undefined;
  return () => task ??= run().catch(error => { task = undefined; throw error; });
}

