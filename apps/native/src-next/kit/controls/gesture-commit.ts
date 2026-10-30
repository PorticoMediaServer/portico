/** A UI occurrence, never playback/navigation admission. The original command
 * still validates its own fence and lease. No callback, clock or model is owned
 * here; callers retain their exact begin-time packet only until their own lift. */
export class GestureCommitScope<T> {
  private issued = 0;
  private active?: number;
  private lastRun = 0;
  private pair?: Readonly<{run: number; value: T}>;
  activate(): number {
    this.pair = undefined; this.lastRun = 0;
    if (this.issued >= Number.MAX_SAFE_INTEGER) {this.active = undefined; return 0;}
    return this.active = ++this.issued;
  }
  retire(ticket: number): boolean {
    if (!(ticket > 0) || ticket !== this.active) return false;
    this.active = undefined; this.pair = undefined; return true;
  }
  bind(ticket: number, run: number, value: T | undefined): void {
    if (ticket !== this.active || !(ticket > 0) || !Number.isSafeInteger(run) || run <= this.lastRun) return;
    this.lastRun = run; this.pair = value === undefined ? undefined : {run, value};
  }
  claim(ticket: number, run: number): T | undefined {
    if (!(ticket > 0) || ticket !== this.active || !this.pair || this.pair.run !== run) return;
    const value = this.pair.value; this.pair = undefined; return value;
  }
  discard(ticket: number, run: number): void {
    if (ticket === this.active && this.pair?.run === run) this.pair = undefined;
  }
}
