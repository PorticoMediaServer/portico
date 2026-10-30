/** The launch page's one media surface, owned by the shell.
 *
 * It draws into one visible element through the same `PlaybackMediaOwner` the player
 * screen uses, and runs the local-only runtime (`LocalPlaybackRuntime`) beside the online
 * stack. There is no second player, queue or store: the desktop host supplies its own
 * `createPlayer` factory and the app-owned download broker as the local port. This owner is
 * the exclusive device owner, so it adopts without the app retirement gate; the online
 * owner's adoption awaits this host's actual retirement first.
 */
import {localPlaybackScope, type LocalPlaybackScope} from '../../../packages/player-core/src/index.ts';
import {LocalPlaybackRuntime, type LocalRuntimeEnvironment} from '../platform/playback/local-runtime.ts';
import type {WebLocalQueueResource} from '../platform/playback/local-queue.ts';
import {PlaybackMediaOwner} from './playback-media-owner.ts';

export type OfflineMediaSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'playing' | 'paused' | 'ended' | 'error';
  itemId?: string;
}>;

export type OfflineMediaLifetime = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;

const idle: OfflineMediaSnapshot = Object.freeze({phase: 'idle'});

export class OfflineMediaHost {
  // `null`: this is the device's exclusive owner, so it must not await its own retirement.
  private readonly owner = new PlaybackMediaOwner<HTMLVideoElement>(null);
  private readonly scope: LocalPlaybackScope;
  private readonly listeners = new Set<() => void>();
  private runtime?: LocalPlaybackRuntime;
  private surface?: HTMLVideoElement;
  private attachment?: () => void;
  private attachSerial = 0;
  private live = true;
  private retirement?: Promise<void>;

  constructor(private readonly options: Readonly<{
    scope: LocalPlaybackScope;
    /** The platform's own factory and local port, never a launch-only player. */
    environment: LocalRuntimeEnvironment;
    isCurrent: () => boolean;
    reportError: (error: unknown) => void;
  }>) {
    this.scope = localPlaybackScope(options.scope);
  }

  /** The one element this host draws into, while it is attached. */
  get element(): HTMLVideoElement | undefined { return this.surface; }

  get mounted(): boolean { return !!this.surface && !!this.attachment && !!this.runtime?.mounted; }

  /** False once this owner has started leaving the device. */
  get active(): boolean { return this.live; }

  /** Attach (or release) the page's visible element. A release starts the terminal
   * retirement; a rerender with the same element is a no-op. */
  attach(element: HTMLVideoElement | null): void {
    if (element === this.surface) return;
    if (!element) { this.detach(); void this.retire().catch(error => this.options.reportError(error)); return; }
    if (!this.live) { this.options.reportError(new Error('offline_media_retiring')); return; }
    this.detach();
    const runtime = this.runtime ?? (this.runtime = this.create());
    const token = ++this.attachSerial;
    this.surface = element;
    void this.owner.adopt(runtime, element).then(release => {
      // A stale attachment must not draw into a newer element; release it exactly once.
      if (token !== this.attachSerial || this.surface !== element) { release?.(); return; }
      this.attachment = release;
      this.notify();
    }, error => {
      if (token !== this.attachSerial) return;
      this.surface = undefined;
      this.options.reportError(error);
    });
  }

  /** Hand one prepared resource to the approved runtime, which takes custody on entry and
   * owns every cleanup path. False means no runtime exists, so custody stays with the caller
   * (the launch host tracks and retries that release); this host never releases it. */
  async play(resource: WebLocalQueueResource, admission: OfflineMediaLifetime): Promise<boolean> {
    const runtime = this.runtime;
    if (!runtime) return false;
    await runtime.play(resource, {signal: admission.signal, isCurrent: () => this.live && !!this.surface && admission.isCurrent()});
    return true;
  }

  snapshot(): OfflineMediaSnapshot {
    const queue = this.runtime?.getSnapshot();
    if (!queue) return idle;
    const item = queue.preparation.items[queue.index], state = queue.observation?.state;
    const phase: OfflineMediaSnapshot['phase'] = queue.phase === 'ready'
      ? (state === 'playing' ? 'playing' : state === 'loading' || state === 'buffering' ? 'loading' : 'paused')
      : queue.phase === 'disposed' ? 'idle' : queue.phase;
    return Object.freeze({phase, ...(item ? {itemId: item.itemId} : {})});
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }

  /** The device's actual retirement, awaited by the next owner's adoption. It retires a
   * still-live player first; a player that never attached is already free. */
  adoptionRetirement(): Promise<void> {
    if (this.retirement) return this.retirement;
    if (!this.runtime) return Promise.resolve();
    return this.retire();
  }

  /** Terminal. Repeated calls share one task; a failed cleanup keeps the runtime's custody
   * and is retried here instead of being reported as finished. */
  retire(): Promise<void> {
    if (this.retirement) return this.retirement;
    this.live = false;
    this.detach();
    const runtime = this.runtime;
    const task = (async () => {
      await runtime?.dispose();
      if (this.runtime === runtime) this.runtime = undefined;
    })();
    const tracked = task.finally(() => { if (this.retirement === tracked) this.retirement = undefined; });
    this.retirement = tracked;
    this.notify();
    return tracked;
  }

  private create(): LocalPlaybackRuntime {
    const runtime = new LocalPlaybackRuntime({
      scope: this.scope,
      environment: this.options.environment,
      isCurrent: () => this.live && !!this.surface && this.options.isCurrent(),
      // Every resource carries its own release; the runtime needs no fallback owner.
      release: () => Promise.resolve(),
      reportError: error => this.options.reportError(error),
    });
    runtime.subscribe(() => this.notify());
    return runtime;
  }

  private detach(): void {
    ++this.attachSerial;
    const release = this.attachment;
    this.attachment = undefined;
    this.surface = undefined;
    try { release?.(); } catch (error) { this.options.reportError(error); }
  }

  private notify(): void { for (const listener of [...this.listeners]) listener(); }
}
