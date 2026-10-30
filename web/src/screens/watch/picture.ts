import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {dispatchWatch, watchSeek, type WatchCommands} from './model';
import type {WebPlaybackRuntimeSnapshot} from '../../platform/playback';

type Target = Readonly<{
  runtime: WatchCommands;
  snapshot: WebPlaybackRuntimeSnapshot;
  isActive: () => boolean;
  reportError: (error: unknown) => void;
}>;
type Touch = Readonly<{
  zone: 'left' | 'middle' | 'right';
  tablet: boolean;
  back: number;
  forward: number;
  single: () => void;
  fullscreen: () => void;
  feedback: (direction: 'left' | 'right', seconds: number) => void;
}>;

function current(target: Target): boolean {
  const shown = target.snapshot, live = target.runtime.getSnapshot();
  return target.isActive() && !shown.disposed && !live.disposed && !!shown.fence && !!live.fence && sameFence(shown.fence, live.fence)
    && shown.authority === live.authority && live.dock.remote?.phase !== 'playing'
    && shown.presentation?.kind === 'video' && live.presentation?.kind === 'video' && live.presentation.expanded
    && sameFence(live.presentation.fence, live.fence);
}

/** Delayed picture gestures retain the clicked source, never the next render's source. */
export class WatchPictureInput {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private touch: Readonly<{target: Target; input: Touch}> | undefined;
  private mouseTarget: Target | undefined;
  private epoch = 0;

  cancel(): void {
    clearTimeout(this.timer); this.timer = undefined; this.touch = undefined; this.mouseTarget = undefined; this.epoch++;
  }

  mouse(target: Target, detail: number): void {
    const previous = this.mouseTarget;
    this.cancel();
    if (!current(target)) return;
    if (detail > 1) {
      if (previous && current(previous) && previous.runtime === target.runtime && previous.snapshot.authority === target.snapshot.authority
        && target.snapshot.fence && sameFence(previous.snapshot.fence, target.snapshot.fence)) this.mouseTarget = previous;
      return;
    }
    const presentation = target.snapshot.presentation;
    if (!presentation) return;
    const kind = presentation.finished ? 'replay' : presentation.playing ? 'pause' : 'play';
    if (!presentation.controls.includes(kind)) return;
    this.mouseTarget = target;
    const act = () => {
      this.timer = undefined;
      if (current(target)) void dispatchWatch(target.runtime, target.snapshot, {kind}).catch(target.reportError);
    };
    if (detail === 0) act(); // Keyboard/assistive activation has no double-click ambiguity.
    else this.timer = setTimeout(act, 220);
  }

  doubleClick(target: Target, fullscreen: () => void): void {
    const previous = this.mouseTarget;
    this.cancel();
    if (previous && current(previous) && current(target) && previous.runtime === target.runtime && previous.snapshot.authority === target.snapshot.authority
      && target.snapshot.fence && sameFence(previous.snapshot.fence, target.snapshot.fence)) fullscreen();
  }

  tap(target: Target, input: Touch): void {
    const previous = this.touch;
    this.cancel();
    if (!current(target)) return;
    if (previous && target.snapshot.fence && previous.input.zone === input.zone && current(previous.target)
      && previous.target.runtime === target.runtime && sameFence(previous.target.snapshot.fence, target.snapshot.fence)) {
      if (input.zone === 'middle') { if (!input.tablet) input.fullscreen(); return; }
      const delta = input.zone === 'left' ? -input.back : input.forward;
      const position = target.snapshot.presentation?.positionSeconds;
      const seconds = position === undefined ? null : watchSeek(target.snapshot, position + delta);
      if (seconds !== null && Number.isFinite(delta) && delta !== 0) {
        const epoch = this.epoch;
        void dispatchWatch(target.runtime, target.snapshot, {kind: 'seek', seconds}).then(() => {
          if (epoch === this.epoch && current(target)) input.feedback(input.zone as 'left' | 'right', Math.abs(delta));
        }).catch(target.reportError);
      }
      return;
    }
    // A second tap in another third is a separate single tap.
    if (previous && current(previous.target)) previous.input.single();
    this.touch = {target, input};
    this.timer = setTimeout(() => {
      this.timer = undefined; this.touch = undefined;
      if (current(target)) input.single();
    }, 320);
  }

  wheel(target: Target, deltaY: number): boolean {
    const presentation = target.snapshot.presentation;
    if (!current(target) || !Number.isFinite(deltaY) || deltaY === 0 || !presentation?.controls.includes('volume')) return false;
    const volume = Math.min(1, Math.max(0, presentation.volume + (deltaY < 0 ? .05 : -.05)));
    void dispatchWatch(target.runtime, target.snapshot, {kind: 'volume', volume}).catch(target.reportError);
    return true;
  }
}
