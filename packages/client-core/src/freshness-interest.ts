import type {FreshnessDependency, FreshnessHub} from './freshness-hub.ts';
import {finalAnswer} from './freshness-hub.ts';
import {isSharedReadSuperseded, supersededNextAllowedAt, observationError, type SharedReadKey, type SharedWitness} from './freshness-shared.ts';
import {addDeadline as add, positiveDeadline as positive, serverDeadline} from './freshness-deadline.ts';

export type InterestOutcome =
  | Readonly<{kind: 'answered'}>
  | Readonly<{kind: 'deferred'; nextAllowedAt?: number}>
  | Readonly<{kind: 'superseded'}>
  | Readonly<{kind: 'failed'; error: unknown; retryable: boolean}>;
export type InterestReadContext = Readonly<{
  signal: AbortSignal;
  shared<T>(key: SharedReadKey, read: (signal: AbortSignal) => Promise<T>): Promise<T>;
}>;
export type FreshnessInterestOptions = Readonly<{
  name: string;
  dependencies: readonly FreshnessDependency[] | (() => readonly FreshnessDependency[]);
  read(context: InterestReadContext): Promise<InterestOutcome>;
  background?: 'never' | 'whenRetained';
}>;
export type FreshnessInterest = Readonly<{
  invalidate(): void;
  setOpen(open: boolean): void;
  setRetained(retained: boolean): void;
  getState(): Readonly<{owed: boolean; reading: boolean; failures: number; deferredUntil?: number}>;
  dispose(): void;
}>;

const MAX_DEADLINE = Number.MAX_VALUE;
const TIMER_CHUNK = 2_147_000_000;

export class Interest implements FreshnessInterest {
  private pending = false;
  private open = true;
  private retained = false;
  private disposed = false;
  private running?: {controller: AbortController; token: object};
  private planned?: object;
  private timer: unknown;
  private timerAt = 0;
  private failures = 0;
  private serverNotBefore = 0;
  private retryAt = 0;
  private deferred = false;
  private readonly source = {};
  private hub: FreshnessHub;
  private options: FreshnessInterestOptions;
  constructor(hub: FreshnessHub, options: FreshnessInterestOptions) {this.hub = hub;this.options = options;}
  dependencies(): readonly FreshnessDependency[] {
    const d = this.options.dependencies;
    try { return typeof d === 'function' ? d() : d; } catch { return []; }
  }
  getState = () => Object.freeze({owed: this.pending || !!this.running, reading: !!this.running, failures: this.failures,
    ...(this.serverNotBefore > this.hub.timers.now() ? {deferredUntil: this.serverNotBefore} : {})});
  invalidate(): void {
    if (this.disposed) return;
    // Explicit catch-up/wake is a movement too, even without a server event.
    this.hub.sharedReads.move(this.dependencies());
    this.moved();
  }
  moved(): void { if (!this.disposed) { this.pending = true; this.deferred = false; this.plan(); } }
  private eligible(): boolean {
    return !this.disposed && this.open && (this.hub.isActive() || this.options.background === 'whenRetained' && this.retained);
  }
  setOpen(open: boolean): void {
    if (this.disposed || open === this.open) return;
    this.open = open;
    if (open) { this.deferred = false; this.plan(); } else this.pause();
  }
  setRetained(retained: boolean): void {
    if (this.disposed || retained === this.retained) return;
    this.retained = retained; this.check();
  }
  check(): void { if (this.eligible()) this.plan(); else this.pause(); }
  private cancelPlan(): void {
    if (this.timer !== undefined) this.hub.timers.clearTimer(this.timer);
    this.timer = undefined;
    if (this.planned) this.hub.sharedReads.release(this.planned);
    this.planned = undefined;
  }
  private pause(): void {
    this.cancelPlan();
    if (this.running) {
      const {controller, token} = this.running;
      this.running = undefined; this.pending = true;
      controller.abort(); this.hub.sharedReads.release(token);
    }
  }
  private plan(): void {
    if (!this.eligible() || !this.pending || this.running || this.deferred || this.timer !== undefined) return;
    const now = this.hub.timers.now();
    const base = Math.max(this.hub.tuning.gatherMs, this.retryAt - now, this.serverNotBefore - now, 0);
    const random = Math.max(0, Math.min(1, this.hub.timers.random() || 0));
    this.timerAt = add(now, Math.min(MAX_DEADLINE, Math.ceil(base * (1 + random * 0.25))));
    this.planned = {};
    this.hub.sharedReads.reserve(this.planned, this.timerAt);
    this.arm();
  }
  private arm(): void {
    const remaining = Math.max(this.timerAt, this.serverNotBefore, this.retryAt) - this.hub.timers.now();
    this.timer = this.hub.timers.setTimer(() => {
      this.timer = undefined;
      if (!this.eligible()) { this.cancelPlan(); return; }
      if (this.hub.timers.now() < Math.max(this.timerAt, this.serverNotBefore, this.retryAt)) { this.arm(); return; }
      void this.run();
    }, Math.max(0, Math.min(TIMER_CHUNK, remaining)));
  }
  private async run(): Promise<void> {
    if (!this.eligible() || !this.pending || this.running) { this.cancelPlan(); return; }
    const controller = new AbortController(), token = this.planned!;
    this.planned = undefined;
    const witness: SharedWitness = this.hub.sharedReads.witness();
    this.running = {controller, token}; this.pending = false;
    // Only the two current ticket inputs: do not retain errors beyond this read.
    const observed: Record<SharedReadKey, {error: unknown; deadline: number} | undefined> = {guide: undefined, 'preferences:web': undefined};
    const learn = (key: SharedReadKey, error: unknown) => {
      if (key !== 'guide' && key !== 'preferences:web') return;
      const deadline = this.hub.sharedReads.notBefore(key);
      this.serverNotBefore = Math.max(this.serverNotBefore, deadline);
      observed[key] = {error, deadline};
    };
    let outcome: InterestOutcome;
    try {
      outcome = await this.hub.read(controller.signal, () => this.options.read({signal: controller.signal,
        shared: async (key, read) => {
          try {
            const value = await this.hub.sharedReads.borrow(token, witness, controller.signal, key, read);
            learn(key, observationError(key, value)); return value;
          } catch (error) { learn(key, error); throw error; }
        }}));
    } catch (error) {
      if (isSharedReadSuperseded(error)) this.serverNotBefore = Math.max(this.serverNotBefore, supersededNextAllowedAt(error));
      // The guide parser's legacy terminal flag describes unreadable data, not
      // proof of removal. Observation must retry that malformed proof.
      const malformed = (error as {code?: unknown} | null)?.code === 'invalid_guide_directory';
      outcome = isSharedReadSuperseded(error) ? {kind: 'superseded'} : {kind: 'failed', error, retryable: malformed || !finalAnswer(error)};
    } finally { this.hub.sharedReads.release(token); }
    if (this.disposed || this.running?.controller !== controller) return;
    this.running = undefined;
    if (!outcome || !['answered', 'deferred', 'superseded', 'failed'].includes(outcome.kind)) {
      outcome = {kind: 'failed', error: new Error('An interest read did not report an outcome.'), retryable: true};
    }
    if (controller.signal.aborted || outcome.kind === 'superseded') { this.pending = true; }
    else if (outcome.kind === 'answered') {
      this.failures = 0; this.retryAt = 0; this.hub.report(this.source, true);
    } else if (outcome.kind === 'deferred') {
      const newer = this.pending;
      this.pending = true;
      this.serverNotBefore = Math.max(this.serverNotBefore, positive(outcome.nextAllowedAt));
      // A write/conflict gate without a deadline is resumed by its owner, not a busy retry loop.
      this.deferred = !newer && this.serverNotBefore <= this.hub.timers.now();
    } else {
      this.failures++;
      this.hub.report(this.source, !outcome.retryable);
      if (outcome.retryable) {
        this.pending = true;
        const now = this.hub.timers.now(), steps = this.hub.tuning.backoffMs;
        this.retryAt = add(now, Math.max(1, steps[Math.min(this.failures - 1, steps.length - 1)] ?? 30_000));
        // The shared owner anchored this error at response time. Display waits or
        // later cohort borrowers must not restart its relative Retry-After clock.
        let sharedDeadline: number | undefined;
        for (const input of Object.values(observed)) if (input && input.error === outcome.error) sharedDeadline = Math.max(sharedDeadline ?? 0, input.deadline);
        const deadline = sharedDeadline ?? serverDeadline(outcome.error, now);
        this.serverNotBefore = Math.max(this.serverNotBefore, deadline);
      }
    }
    this.plan();
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true; this.pause(); this.pending = false;
    this.hub.report(this.source, true); this.hub.forgetInterest(this);
  }
}
