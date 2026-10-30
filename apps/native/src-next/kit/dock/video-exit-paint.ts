import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativePlaybackVideoExitImage} from '../../platform/playback/types';
import {validRect} from './model';
import type {DockRect} from './types';

type Viewport = Readonly<{width: number; height: number}>;
type Captured = Readonly<{fence: PlaybackFence; image: Readonly<{uri: string; width: number; height: number}>;
  retainForExit?: () => Promise<NativePlaybackVideoExitImage | undefined>}>;
export type DockVideoExitPaint = Readonly<{fence: PlaybackFence; windowBounds: DockRect; canvasBounds?: DockRect; image: NativePlaybackVideoExitImage['image'];
  signal: AbortSignal; paintCurrent(): boolean; subscribe(listener: () => void): () => void; release(): Promise<void>}>;
export type TakeDockVideoExit = (fence: PlaybackFence, originalWindowBounds: DockRect) => DockVideoExitPaint | undefined;
const rectEqual = (a: DockRect, b: DockRect) => a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;
const permitted = (read: () => boolean) => {try {return read() === true;} catch {return false;}};

/** UI ownership of an already captured and decoded file. Never captures, starts
 * playback, admits an old fence or waits in a command. Only a completed original
 * pre-retirement grant can be consumed by the committed visual handover. */
export class DockVideoExitImages {
  private readonly claimed = new WeakSet<object>();
  private readonly records = new Set<DockVideoExitPaint>();
  private offered?: DockVideoExitPaint;
  private disposed = false;
  private offerSequence = 0;
  private pending?: Readonly<{ticket: number; fence: PlaybackFence}>;
  constructor(private readonly viewport: () => Viewport, private readonly reportError: (error: unknown) => void) {}

  async offer(captured: Captured, decoded: boolean, bounds: DockRect, originalCurrent: () => boolean,
    placement?: Readonly<{bounds: DockRect; current(): boolean}>): Promise<void> {
    const retain = captured.retainForExit;
    if (this.disposed || !retain || !decoded || this.claimed.has(captured) || !validRect(bounds) || !permitted(originalCurrent) || placement && (!validRect(placement.bounds) || !permitted(placement.current))) return;
    const view = this.viewport();
    if (!(Number.isFinite(view.width) && Number.isFinite(view.height) && view.width > 0 && view.height > 0)) return;
    const originalBounds = Object.freeze({...bounds}), originalView = Object.freeze({...view});
    this.claimed.add(captured);
    const ticket = ++this.offerSequence;
    this.pending = {ticket, fence: captured.fence};
    let grant: NativePlaybackVideoExitImage | undefined;
    try {grant = await retain();} catch (error) {if (this.pending?.ticket === ticket) this.pending = undefined; this.reportError(error); return;}
    if (this.pending?.ticket === ticket) this.pending = undefined;
    if (!grant) return;
    const sameViewport = () => {const now = this.viewport(); return now.width === originalView.width && now.height === originalView.height;};
    // The existing image-ready witness applies only to these same captured bytes.
    if (this.disposed || ticket !== this.offerSequence || !permitted(originalCurrent) || placement && !permitted(placement.current) || !sameViewport() || grant.signal.aborted || !permitted(grant.paintCurrent)
      || !sameFence(captured.fence, grant.fence) || grant.image.uri !== captured.image.uri
      || grant.image.width !== captured.image.width || grant.image.height !== captured.image.height) {
      void grant.release().catch(this.reportError); return;
    }
    let disposal: Promise<void> | undefined, off: (() => void) | undefined;
    const record: DockVideoExitPaint = Object.freeze({fence: grant.fence, windowBounds: originalBounds, canvasBounds: placement ? Object.freeze({...placement.bounds}) : undefined, image: grant.image, signal: grant.signal,
      paintCurrent: () => !this.disposed && !disposal && (!placement || permitted(placement.current)) && sameViewport() && !grant!.signal.aborted && permitted(grant!.paintCurrent),
      subscribe: (listener: () => void) => grant!.subscribe(listener),
      release: () => {
        if (!disposal) {
          this.records.delete(record); if (this.offered === record) this.offered = undefined;
          off?.(); off = undefined;
          disposal = Promise.resolve().then(() => grant!.release());
        }
        return disposal;
      }});
    this.records.add(record);
    const previous = this.offered; this.offered = record;
    if (previous) void previous.release().catch(this.reportError);
    try {
      const unsubscribe = grant.subscribe(() => {if (!record.paintCurrent()) void record.release().catch(this.reportError);});
      if (disposal) unsubscribe(); else off = unsubscribe;
    } catch (error) {this.reportError(error); void record.release().catch(this.reportError);}
    if (!record.paintCurrent()) void record.release().catch(this.reportError);
  }

  /** Call from the committed transition, with the original measured slot. No
   * successor fence, new rectangle, latest fallback or second use is accepted. */
  take: TakeDockVideoExit = (fence, bounds) => {
    const record = this.offered;
    if (!record || !sameFence(record.fence, fence) || !rectEqual(record.windowBounds, bounds)) return;
    if (!record.paintCurrent()) {void record.release().catch(this.reportError); return;}
    this.offered = undefined;
    return record;
  };

  retireUnconsumed(keep?: PlaybackFence): void {
    if (!keep || !this.pending || !sameFence(keep, this.pending.fence)) {this.offerSequence++; this.pending = undefined;}
    const previous = this.offered;
    if (previous && (!keep || !sameFence(keep, previous.fence))) {
      this.offered = undefined;
      void previous.release().catch(this.reportError);
    }
  }
  clear(): void {
    this.offerSequence++; this.pending = undefined; this.offered = undefined;
    for (const record of [...this.records]) void record.release().catch(this.reportError);
  }
  dispose(): void {this.disposed = true; this.clear();}
}
