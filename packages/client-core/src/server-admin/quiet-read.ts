/**
 * One read behind a console panel (a list of keys, the devices, what storage is in use), kept
 * current the way every other open screen is (`freshness-hub.ts`):
 *
 * - What is shown stays while it is read again. Nothing is blanked and nothing spins unless the
 *   owner asked for the read.
 * - It is read again when something it was built from moves on the feed, when the feed could not
 *   say everything that happened, and, for the things the server sends no hint about, at a calm
 *   pace while the page is in front. Those reads wait for a pause in the owner's input.
 *   A read that names neither dependencies nor a pace is read once and when asked, as a
 *   document opened in a dialog to be edited should be.
 * - A read the owner did not ask for that fails says nothing: what is shown is kept and it is
 *   tried again, a little later each time. The shell's one "can't reach the server" state covers
 *   an outage. A first load gets a couple of quiet attempts before its own error is shown, and
 *   the attempts continue behind it.
 * - A read the owner asked for (Try again, Refresh, the read after their own action) answers at
 *   once, error included.
 *
 * No React and no DOM: the web's `useRead` and the phone's panels sit on it, and it is tested
 * under Node.
 */
import {finalAnswer, type FreshDocument, type FreshnessDependency, type FreshnessHub, type RevalidateContext} from '../freshness-hub.ts';

export type QuietReadState<T> = Readonly<{
  data?: T;
  /** Why the read failed, when that is to be shown. `data` beside it is what was read before. */
  error?: unknown;
  /** A read the screen is waiting on: the first, or one the owner asked for. */
  loading: boolean;
  /** When `data` was read. */
  at?: number;
}>;

/** The calm pace for a panel the server sends no hint about: within the half minute an open screen is promised. */
export const QUIET_READ_EVERY_MS = 30_000;

export class QuietRead<T> {
  private state: QuietReadState<T> = Object.freeze({loading: false});
  private listeners = new Set<() => void>();
  private read: () => Promise<T> = () => Promise.reject(new Error('not configured'));
  private dependencies: readonly FreshnessDependency[] = [];
  private every = 0;
  /** Kept current by itself: it named what it depends on, or a pace. */
  private live = false;
  private tick: unknown;
  private seq = 0;
  private quietError: unknown;
  private open = false;
  private disposed = false;
  private readonly hub: FreshnessHub;
  private readonly document: FreshDocument;
  private readonly offDocument: () => void;

  constructor(hub: FreshnessHub) {
    this.hub = hub;
    this.document = hub.document({
      dependencies: () => this.dependencies,
      hasContent: () => this.state.data !== undefined,
      revalidate: context => this.quietly(context),
      recover: context => this.quietly(context),
    });
    // A first load that keeps failing behind the scenes is said after a couple of attempts.
    this.offDocument = this.document.subscribe(() => {
      if (this.state.data === undefined && this.quietError !== undefined && this.document.getState().failures >= 2 && this.state.error === undefined) this.publish({loading: false, error: this.quietError});
    });
  }

  getSnapshot = (): QuietReadState<T> => this.state;
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<QuietReadState<T>>): void {
    if (this.disposed) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }

  /** What to read, what it was built from, and how often to look when nothing on the feed would say it moved (0: never). */
  configure(read: () => Promise<T>, dependencies: readonly FreshnessDependency[] = [], every = 0): void {
    this.read = read;
    this.dependencies = dependencies;
    this.live = dependencies.length > 0 || every > 0;
    if (every !== this.every) { this.every = every; this.arrange(); }
  }

  /** The panel is on screen (true) or not. Quiet reads only run while it is. */
  setOpen(open: boolean): void {
    if (this.disposed || this.open === open) return;
    this.open = open;
    this.document.setOpen(open);
    this.arrange();
  }

  private arrange(): void {
    if (this.tick !== undefined) { this.hub.timers.clearTimer(this.tick); this.tick = undefined; }
    if (this.disposed || !this.open || !(this.every > 0)) return;
    this.tick = this.hub.timers.setTimer(() => { this.tick = undefined; this.nudge(); this.arrange(); }, this.every);
  }

  /** Something may have moved that the feed does not announce: read again, quietly, if the page is in front. */
  nudge(): void {
    if (this.disposed || !this.hub.isActive() || this.state.loading) return;
    this.document.invalidate();
  }

  /**
   * Read now. `asked` (the default) is a read the owner is waiting on: its failure is shown at
   * once. A read nobody asked for (the first, when a panel opens) is quiet about a failure that
   * may pass: it is tried again by itself before anything is said.
   */
  reload(asked = true): void {
    if (this.disposed) return;
    const n = ++this.seq;
    this.quietError = undefined;
    // The screen is reading it itself: earlier hints are answered by this read, and a quiet read in flight is dropped.
    this.document.markCurrent();
    this.publish({loading: true, error: undefined});
    this.read().then(data => {
      if (n !== this.seq) return;
      this.publish({data, loading: false, error: undefined, at: this.hub.timers.now()});
    }, error => {
      if (n !== this.seq) return;
      if (asked || this.state.data !== undefined || finalAnswer(error)) { this.publish({loading: false, error}); return; }
      // Still loading as far as the owner is concerned; the hub tries again and backs off.
      this.quietError = error;
      this.document.invalidate();
    });
  }

  private async quietly(context: RevalidateContext): Promise<void> {
    const n = this.seq;
    const filled = this.state.data !== undefined;
    // Something read once to be looked at or edited is not moved under the owner because the feed reconnected.
    if (filled && !this.live) return;
    let data: T;
    try {
      data = await this.read();
    } catch (error) {
      if (n === this.seq && !context.signal.aborted) {
        this.quietError = error;
        // An answer the server will repeat (not allowed, gone) is the panel's to show; anything else is tried again unsaid.
        if (finalAnswer(error)) this.publish({loading: false, error});
      }
      throw error;
    }
    // Shown in a pause in the owner's input, so a list does not move under a press.
    if (filled) await context.settle();
    if (context.signal.aborted || n !== this.seq) return;
    this.quietError = undefined;
    this.publish({data, loading: false, error: undefined, at: this.hub.timers.now()});
  }

  dispose(): void {
    if (this.disposed) return;
    if (this.tick !== undefined) this.hub.timers.clearTimer(this.tick);
    this.offDocument();
    this.document.dispose();
    this.disposed = true;
    this.listeners.clear();
  }
}
