import {addDeadline, serverDeadline} from './freshness-deadline.ts';

export type ResyncReadContext = Readonly<{signal: AbortSignal; isCurrent(): boolean}>;
export type ResyncRegistration = Readonly<{
  invalidate(): void;
  setOpen(open: boolean): void;
  dispose(): void;
  getState(): Readonly<{owed: boolean; reading: boolean}>;
}>;
type Service = {read(context: ResyncReadContext): Promise<void>; priority: number; externalAdmission: boolean; open: boolean; generation: number; answered: number;
  running?: AbortController; due: number; failures: number; disposed: boolean};
type Pending = {signal: AbortSignal; start(): void; cancel(): void};
type Options = {now?: () => number; random?: () => number; setTimer?: (fn: () => void, ms: number) => unknown; clearTimer?: (timer: unknown) => void};

/** One authority's observational dispatcher. Debt is memory only; retiring a read
 * aborts its ownership but keeps its permit until the underlying work settles. */
export class ResyncCoordinator {
  private services = new Set<Service>();
  private pending = new Set<Pending>();
  private active = 0;
  private activeServices = 0;
  private disposed = false;
  private foreground = true;
  private queued = false;
  private timer: unknown;
  private readonly now: () => number;
  private readonly random: () => number;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (timer: unknown) => void;
  constructor(options: Options = {}) {
    this.now = options.now ?? Date.now; this.random = options.random ?? Math.random;
    this.setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = options.clearTimer ?? (timer => clearTimeout(timer as ReturnType<typeof setTimeout>));
  }
  register(read: Service['read'], options: {priority?: number; open?: boolean; admission?: 'external'} = {}): ResyncRegistration {
    if (this.disposed) throw new Error('resync_retired');
    if (this.services.size >= 128) throw new Error('resync_service_limit');
    const service: Service = {read, priority: options.priority ?? 1, externalAdmission: options.admission === 'external', open: options.open ?? true, generation: 0, answered: 0, due: 0, failures: 0, disposed: false};
    this.services.add(service);
    return {
      invalidate: () => {if (!service.disposed) {service.generation++; this.plan();}},
      setOpen: open => {
        if (service.disposed || service.open === open) return;
        service.open = open;
        if (!open) service.running?.abort();
        this.plan();
      },
      getState: () => ({owed: service.generation !== service.answered, reading: !!service.running}),
      dispose: () => {service.disposed = true; service.running?.abort(); this.services.delete(service); this.plan();},
    };
  }
  setActive(active: boolean): void {
    if (this.foreground === active || this.disposed) return;
    this.foreground = active;
    if (!active) for (const service of this.services) if (service.priority > 0) service.running?.abort();
    this.plan();
  }

  /** Called synchronously before the feed acknowledges a barrier. */
  invalidate(): void {for (const service of this.services) service.generation++; this.plan();}

  /** Shared admission for existing freshness owners: their debt, quiet waits,
   * shared-input cohorts and retirement remain with those owners. */
  run<T>(signal: AbortSignal, read: () => Promise<T>): Promise<T> {
    if (this.disposed || signal.aborted) return Promise.reject(new Error('resync_retired'));
    if (this.pending.size >= 128) return Promise.reject(new Error('resync_read_limit'));
    return new Promise<T>((resolve, reject) => {
      const pending: Pending = {
        signal,
        cancel: () => {this.pending.delete(pending); signal.removeEventListener('abort', pending.cancel); reject(new Error('resync_retired'));},
        start: () => {
          this.pending.delete(pending); signal.removeEventListener('abort', pending.cancel); this.active++;
          // Invoke in this dispatch turn. Existing shared-input gather cohorts must
          // not be shifted into a later timer turn by admission.
          let work: Promise<T>;
          try {work = read();} catch (error) {work = Promise.reject(error);}
          void work.then(resolve, reject).finally(() => {this.active--; this.plan();});
        },
      };
      this.pending.add(pending); signal.addEventListener('abort', pending.cancel, {once: true});
      this.plan();
    });
  }
  private plan(): void {
    if (this.disposed || this.queued) return;
    this.queued = true;
    queueMicrotask(() => {this.queued = false; this.dispatch();});
  }
  private dispatch(): void {
    if (this.disposed) return;
    if (this.timer !== undefined) {this.clearTimer(this.timer); this.timer = undefined;}
    const now = this.now();
    const ready = [...this.services].filter(s => s.open && (this.foreground || s.priority <= 0) && !s.running && s.generation !== s.answered).sort((a, b) => a.priority - b.priority);
    // Urgent current-session/control reconciliation is admitted before visible data.
    for (const service of ready) {
      if (this.activeServices >= 4) break;
      if (service.due > now || !service.externalAdmission && this.active >= 4) continue;
      const generation = service.generation, controller = new AbortController();
      service.running = controller; this.activeServices++;
      if (!service.externalAdmission) this.active++;
      const context: ResyncReadContext = {signal: controller.signal, isCurrent: () => !this.disposed && !service.disposed && service.open && (this.foreground || service.priority <= 0) && !controller.signal.aborted && generation === service.generation};
      let work: Promise<void>;
      try {work = service.read(context);} catch (error) {work = Promise.reject(error);}
      void work.then(() => {
        if (context.isCurrent()) {service.answered = generation; service.failures = 0;}
        service.due = Math.max(service.due, addDeadline(this.now(), service.priority <= 0 ? 0 : 1000));
      }, error => {
        if (controller.signal.aborted || service.disposed || this.disposed) return;
        service.failures++;
        const floor = Math.max([1000, 2000, 5000, 10000, 30000][Math.min(service.failures - 1, 4)]!, serverDeadline(error, this.now()) - this.now());
        service.due = addDeadline(this.now(), Math.ceil(floor * (1 + Math.max(0, Math.min(1, this.random() || 0)) * .25)));
      }).finally(() => {service.running = undefined; this.activeServices--; if (!service.externalAdmission) this.active--; this.plan();});
    }
    for (const pending of this.pending) {
      if (this.active >= 4) break;
      if (pending.signal.aborted) pending.cancel(); else pending.start();
    }
    if (this.activeServices < 4) {
      const due = [...this.services].filter(s => s.open && (this.foreground || s.priority <= 0) && !s.running && s.generation !== s.answered && (s.externalAdmission || this.active < 4)).reduce((n, s) => Math.min(n, s.due), Infinity);
      if (due !== Infinity) this.timer = this.setTimer(() => {this.timer = undefined; this.plan();}, Math.min(2_147_000_000, Math.max(0, due - this.now())));
    }
  }
  dispose(): void {
    this.disposed = true;
    if (this.timer !== undefined) this.clearTimer(this.timer);
    this.timer = undefined;
    for (const service of this.services) {service.disposed = true; service.running?.abort();}
    this.services.clear();
    for (const pending of this.pending) pending.cancel();
  }
}
