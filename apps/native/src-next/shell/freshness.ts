import {dependsOn, FreshnessHub, titlesWithPersonalState, type FreshDocument, type FreshnessHubOptions, type RevalidateContext} from '../../../../packages/client-core/src/freshness-hub';
import type {EventsClient} from '../../../../packages/client-core/src/playback-v1/events';

type Read = (context: RevalidateContext) => Promise<void>;
export type NativeShellFreshnessReads = Readonly<{
  home: Read;
  hasHome: () => boolean;
  libraries: Read;
  preferences: Read;
  unread: Read;
  guide: Read;
  identity: Read;
}>;

/** The canonical hub maps event types to resources and retains invalidations
 * received during a read. Scan progress has no dependency on Home or shell facts;
 * scan presenters may observe it on the same hub without another event feed. */
export class NativeShellFreshness {
  readonly hub: FreshnessHub;
  private readonly home: FreshDocument;
  private readonly unread: FreshDocument;
  private readonly guide: FreshDocument;
  private readonly libraries: FreshDocument;
  constructor(reads: NativeShellFreshnessReads, options: FreshnessHubOptions = {}) {
    this.hub = new FreshnessHub(options);
    const document = (dependencies: readonly string[], revalidate: Read) => {
      const value = this.hub.document({dependencies, revalidate}); value.setOpen(true); return value;
    };
    this.home = this.hub.document({dependencies: [dependsOn.libraries, ...titlesWithPersonalState(), dependsOn.list('recommendations'), dependsOn.list('guide')],
      hasContent: reads.hasHome, revalidate: reads.home});
    this.libraries = document([dependsOn.libraries], reads.libraries);
    document([dependsOn.settings('preferences')], reads.preferences);
    this.unread = document([dependsOn.event('notification.changed')], reads.unread);
    this.guide = document([dependsOn.list('guide'), dependsOn.list('liveSources')], reads.guide);
    // A feed resync checks identity too, without a second whole-shell refresh.
    document([], reads.identity);
  }
  attach(events: EventsClient): void {this.hub.attach(events);}
  setActive(active: boolean): void {this.hub.setActive(active);}
  setHomeOpen(open: boolean): void {this.home.setOpen(open);}
  invalidateHome(): void {this.home.invalidate();}
  loadAuxiliary(): void {this.unread.invalidate(); this.guide.invalidate();}
  /** Read the library directory again now; a failure takes the hub's own backoff. */
  invalidateLibraries(): void {this.libraries.invalidate();}
  dispose(): void {this.hub.dispose();}
}

/** Independent resource reads can finish while a full refresh is still waiting
 * on another fact. An older round may not replace a newer published answer.
 * A newer read that failed or was cancelled does not discard an older success. */
export class NativeReadOrder {
  private started = 0;
  private published = 0;
  begin(): number {return ++this.started;}
  apply(ticket: number, publish: () => void): boolean {
    if (ticket <= this.published) return false;
    this.published = ticket; publish(); return true;
  }
}

/** One in-flight explicit/foreground refresh, with a trailing read whenever an
 * overlapping request arrived. A failed older read cannot erase that request. */
export function nativeQueuedRefresh(read: () => Promise<void>, current: () => boolean): () => Promise<void> {
  let pending: Promise<void> | undefined, dirty = false;
  return () => {
    dirty = true;
    if (!pending) {
      let complete!: () => void, fail!: (error: unknown) => void;
      const flight = new Promise<void>((resolve, reject) => {complete = resolve; fail = reject;});
      // Own the slot before invoking read(), including a synchronous throw or
      // reentrant refresh. Retire it in the same turn that finishes the loop:
      // a later microtask must start a new read, not join an already-done one.
      pending = flight;
      void (async () => {
        try {
          do {
            dirty = false;
            try {await read();}
            catch (error) {if (!dirty || !current()) throw error;}
          } while (dirty && current());
          pending = undefined; complete();
        } catch (error) {pending = undefined; fail(error);}
      })();
      return flight;
    }
    return pending;
  };
}
