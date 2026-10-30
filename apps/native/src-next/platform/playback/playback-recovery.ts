type Timers = Readonly<{setTimer?: (run: () => void, ms: number) => unknown; clearTimer?: (timer: unknown) => void}>;

/** A state-only progress tick is not evidence that media has started arriving. */
export function mediaRecovered(fact: Readonly<{type?: unknown; position?: unknown; isPlaying?: unknown; seeking?: unknown}>, previousPosition: number): boolean {
  return fact.seeking !== true && (fact.type === 'ready' || fact.isPlaying === true
    || fact.type === 'progress' && typeof fact.position === 'number' && Number.isFinite(fact.position) && fact.position > previousPosition);
}

/** One finite outage per playback run, independent of further native errors or route probes. */
export class NativePlaybackRecovery {
  private run?: string;
  private epoch = 0;
  private deadline?: unknown;
  private probe?: unknown;
  private readonly setTimer: (run: () => void, ms: number) => unknown;
  private readonly clearTimer: (timer: unknown) => void;
  constructor(timers: Timers = {}) {
    this.setTimer = timers.setTimer ?? ((run, ms) => setTimeout(run, ms));
    this.clearTimer = timers.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>));
  }
  retain(run: string | undefined): void { if (this.run !== run) this.clear(); }
  start(run: string, current: () => boolean, fail: () => void, probe: () => void): void {
    this.retain(run);
    if (this.run === undefined) {
      this.run = run;
      const epoch = ++this.epoch;
      this.deadline = this.setTimer(() => {
        if (this.epoch !== epoch || this.run !== run) return;
        this.clear();
        if (current()) fail();
      }, 90_000);
    }
    const epoch = this.epoch;
    if (this.probe === undefined) this.probe = this.setTimer(() => {
      if (this.epoch !== epoch) return;
      this.probe = undefined;
      if (this.run === run && current()) probe();
    }, 1000);
  }
  clear(): void {
    this.epoch++;
    if (this.deadline !== undefined) this.clearTimer(this.deadline);
    if (this.probe !== undefined) this.clearTimer(this.probe);
    this.deadline = undefined; this.probe = undefined; this.run = undefined;
  }
}
