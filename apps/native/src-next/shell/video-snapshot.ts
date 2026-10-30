import {sameFence, type PlaybackFence} from '../../../../packages/player-core/src';
import type {NativeShellPlaybackAdapter, NativeShellVideoSnapshot} from './playback';

type CaptureOptions = Readonly<{
  capture: NativeShellPlaybackAdapter['captureVideoSnapshot'];
  changed: (owner: NativeVideoSnapshotCapture, image: NativeShellVideoSnapshot | undefined) => void;
  reportError: (error: unknown) => void;
}>;

/** One adapter/authority owner. Clearing cancels pending work and retires its actual image. */
export class NativeVideoSnapshotCapture {
  private retained: NativeShellVideoSnapshot | undefined;
  private pending: {fence: PlaybackFence; controller: AbortController; optional: boolean} | undefined;
  private operation = 0;
  private retiring: Promise<void> = Promise.resolve();
  private inFlight: Promise<void> | undefined;
  private readonly options: CaptureOptions;

  constructor(options: CaptureOptions) {this.options = options;}

  matches(fence: PlaybackFence | undefined): boolean {
    return !!fence && (!this.retained || sameFence(this.retained.fence, fence)) && (!this.pending || sameFence(this.pending.fence, fence));
  }

  private release(image: NativeShellVideoSnapshot | undefined): Promise<void> {
    return image ? image.release().catch(this.options.reportError) : Promise.resolve();
  }

  clear(): Promise<void> {
    this.operation++;
    this.pending?.controller.abort(); this.pending = undefined;
    const previous = this.retained; this.retained = undefined;
    this.options.changed(this, undefined);
    this.retiring = Promise.all([this.retiring, this.inFlight, this.release(previous)]).then(() => {});
    return this.retiring;
  }

  prepare(fence: PlaybackFence, isCurrent: () => boolean, begin?: () => boolean): Promise<void> {
    if (this.retained && sameFence(this.retained.fence, fence)) return Promise.resolve();
    if (this.pending && sameFence(this.pending.fence, fence) && (!this.pending.optional || begin)) return this.inFlight ?? Promise.resolve();
    // A normal layout/direct-input request takes priority over optional idle
    // prefetch. Clearing cancels that original run; it never rebinds its guard.
    const retiring = this.clear(), epoch = this.operation, controller = new AbortController();
    const current = () => !controller.signal.aborted && epoch === this.operation && isCurrent();
    this.pending = {fence, controller, optional: begin !== undefined};
    const work: Promise<void> = retiring.then(() => current() && (begin?.() ?? true) ? this.options.capture(fence, {signal: controller.signal}) : undefined).then(async image => {
      if (!image) return;
      if (!current() || !sameFence(image.fence, fence)) {await this.release(image); return;}
      this.retained = image; this.options.changed(this, image);
    }).catch(error => {if (current()) this.options.reportError(error);}).finally(() => {
      if (this.pending?.controller === controller) this.pending = undefined;
      if (this.inFlight === work) this.inFlight = undefined;
    });
    this.inFlight = work;
    return work;
  }
}
