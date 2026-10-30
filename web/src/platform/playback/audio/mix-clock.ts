/** An epoch maps physical context frames to one renderer's logical mix frames. */
type Epoch = {contextStart: number; logicalStart: number; frontier: number};

/**
 * Integer-frame clock for a graph whose nodes never extend past the common PCM frontier.
 * Epochs retain flat gaps so an output-lag observation made after a rebase can still map the
 * already-rendered old prefix. The renderer owns trimming once that prefix is no longer audible.
 */
export class MixClock {
  private epochs: Epoch[] = [];

  start(contextStart: number, logicalStart: number): void {
    const context = Math.floor(contextStart), logical = Math.floor(logicalStart);
    if (!Number.isFinite(context) || !Number.isFinite(logical)) throw new Error('invalid mix clock anchor');
    const previous = this.epochs[this.epochs.length - 1];
    if (previous && context < previous.contextStart) throw new Error('mix clock moved backwards');
    if (previous && logical !== this.at(context)) throw new Error('mix clock changed logical position');
    if (previous?.contextStart === context) this.epochs.pop();
    this.epochs.push({contextStart: context, logicalStart: logical, frontier: logical});
  }

  /** Extend only after all voices required by this logical interval have been scheduled. */
  admit(frontier: number): void {
    const epoch = this.epochs[this.epochs.length - 1];
    const frame = Math.floor(frontier);
    if (!epoch || !Number.isFinite(frame) || frame < epoch.frontier) throw new Error('invalid mix frontier');
    epoch.frontier = frame;
  }

  /** Map a frame on the current epoch to its node scheduling coordinate. */
  contextAt(logicalFrame: number): number {
    const epoch = this.epochs[this.epochs.length - 1];
    const frame = Math.floor(logicalFrame);
    if (!epoch || !Number.isFinite(frame) || frame < epoch.logicalStart) throw new Error('invalid logical frame');
    return epoch.contextStart + frame - epoch.logicalStart;
  }

  get contextStart(): number { return this.epochs[this.epochs.length - 1]?.contextStart ?? 0; }

  get frontier(): number { return this.epochs[this.epochs.length - 1]?.frontier ?? 0; }

  at(contextFrame: number): number {
    if (!this.epochs.length) return 0;
    const frame = Math.floor(contextFrame);
    if (!Number.isFinite(frame)) throw new Error('invalid context frame');
    let epoch = this.epochs[0]!;
    for (const candidate of this.epochs) {
      if (candidate.contextStart > frame) break;
      epoch = candidate;
    }
    return Math.min(epoch.frontier, epoch.logicalStart + Math.max(0, frame - epoch.contextStart));
  }

  /** Close the old epoch at actual suspension and freeze through the new future anchor. */
  rebase(suspendedContextFrame: number, newContextFrame: number): number {
    const suspended = Math.floor(suspendedContextFrame), next = Math.floor(newContextFrame);
    if (!this.epochs.length || !Number.isFinite(suspended) || !Number.isFinite(next) || next < suspended) throw new Error('invalid mix clock rebase');
    const held = this.at(suspended);
    // A context may suspend before a fresh future lead anchor. That epoch rendered nothing and
    // cannot precede the flat hold; retain only epochs that existed at the suspended frame.
    while (this.epochs.length && this.epochs[this.epochs.length - 1]!.contextStart > suspended) this.epochs.pop();
    // The flat epoch is necessary when suspension was early: the previous frontier can be later
    // than the sample actually rendered before the suspended clock stopped.
    this.start(suspended, held);
    this.start(next, held);
    return held;
  }

  /** Drop history only after the caller knows an output-lag observation cannot reach it. */
  trimBefore(contextFrame: number): void {
    while (this.epochs.length > 1 && this.epochs[1]!.contextStart <= contextFrame) this.epochs.shift();
  }
}
