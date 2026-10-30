import {ConsoleClient, type Alert, type Job, type Measurement} from '../../../../../packages/client-core/src/console';
import {AdminSessionsClient} from '../../../../../packages/client-core/src/playback-v1/admin-sessions';
import type {AdminSession} from '../../../../../packages/client-core/src/playback-v1/types';
import type {NativeScreenLifetime} from './lifetime';
import {NativeScreenStore} from './lifetime';
import {DashboardCursorWindow, type DashboardCursorSnapshot} from './dashboard-cursor-window';

export type NativeDashboardAdmission = 'owner' | 'administrator' | 'denied';
export type NativeDashboardSnapshot = Readonly<{
  admission: NativeDashboardAdmission;
  loading: boolean;
  sessions: readonly AdminSession[];
  nextCursor?: string;
  sessionWindow?: DashboardCursorSnapshot<AdminSession, SessionMeta>;
  jobWindow?: DashboardCursorSnapshot<Job, undefined>;
  totals?: Awaited<ReturnType<AdminSessionsClient['list']>>['totals'];
  health?: Measurement;
  storage?: Measurement;
  alerts: readonly Alert[];
  jobs: readonly Job[];
  error?: unknown;
}>;
type SessionPage = Awaited<ReturnType<AdminSessionsClient['list']>>;
type SessionMeta = Readonly<{totals: SessionPage['totals']; scope: SessionPage['scope']; revision?: string}>;
export type NativeDashboardReaders = Readonly<{
  sessions: Pick<AdminSessionsClient, 'list'>;
  console: Pick<ConsoleClient, 'measurement' | 'alerts' | 'jobs' | 'dispose'>;
}>;

/** Re-evaluate main-profile and live membership authority before every read and publication. */
export class NativeDashboardController extends NativeScreenStore<NativeDashboardSnapshot> {
  private disposed = false;
  private cleaned = false;
  private generation = 0;
  private role: NativeDashboardAdmission;
  private sessionFence?: string;
  private sessionRevision?: string;
  private sessions: DashboardCursorWindow<AdminSession, SessionMeta>;
  private jobs?: DashboardCursorWindow<Job, undefined>;
  readonly lifetime: NativeScreenLifetime;
  private admission: () => NativeDashboardAdmission;
  private readers: NativeDashboardReaders;
  constructor(lifetime: NativeScreenLifetime, admission: () => NativeDashboardAdmission, readers: NativeDashboardReaders) {
    super({admission: admission(), loading: false, sessions: [], alerts: [], jobs: []});
    this.lifetime = lifetime; this.admission = admission; this.readers = readers; this.role = admission();
    this.sessions = this.sessionWindow();
    if (this.role === 'owner') this.jobs = this.jobWindow();
    lifetime.signal.addEventListener('abort', this.retire, {once: true});
  }
  private valid = (role: NativeDashboardAdmission): boolean => {
    const own = () => !this.disposed && !this.lifetime.signal.aborted && this.role === role && role !== 'denied';
    return own() && this.lifetime.current() && own() && this.admission() === role && own();
  };
  private sessionWindow(): DashboardCursorWindow<AdminSession, SessionMeta> {
    const role = this.role;
    const window: DashboardCursorWindow<AdminSession, SessionMeta> = new DashboardCursorWindow<AdminSession, SessionMeta>(async (cursor, signal) => {
      if (!current()) throw new Error('native_screen_scope_ended');
      const page = await this.readers.sessions.list(cursor || undefined, 10, signal);
      if (!current() || signal.aborted) throw new Error('native_screen_scope_ended');
      if (page.scope.serverId !== this.lifetime.authority.serverId || this.sessionFence && this.sessionFence !== page.scope.viewerFence || this.sessionRevision && this.sessionRevision !== page.revision) throw new Error('dashboard_refresh_required');
      this.sessionFence = page.scope.viewerFence; if (page.revision) this.sessionRevision = page.revision;
      return {items: page.items, nextCursor: page.nextCursor, metadata: {totals: page.totals, scope: page.scope, revision: page.revision}};
    }, row => row.id, () => current(), snapshot => {
      if (current()) this.publish({...this.getSnapshot(), sessionWindow: snapshot, sessions: snapshot.items, nextCursor: snapshot.nextCursor, totals: snapshot.metadata?.totals ?? this.getSnapshot().totals});
    });
    const current = (): boolean => this.sessions === window && this.valid(role) && this.sessions === window;
    return window;
  }
  private jobWindow(): DashboardCursorWindow<Job, undefined> {
    const window: DashboardCursorWindow<Job, undefined> = new DashboardCursorWindow<Job, undefined>(async cursor => {
      if (!current()) throw new Error('native_screen_scope_ended');
      const page = await this.readers.console.jobs(cursor);
      if (!current()) throw new Error('native_screen_scope_ended');
      return {...page, metadata: undefined};
    }, row => row.id, () => current(), snapshot => {
      if (current()) this.publish({...this.getSnapshot(), jobWindow: snapshot, jobs: snapshot.items});
    }, (first, second) => first.revision === second.revision);
    const current = (): boolean => this.jobs === window && this.valid('owner') && this.jobs === window;
    return window;
  }
  private retire = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.generation++;
    const sessions = this.sessions, jobs = this.jobs; this.jobs = undefined;
    sessions.dispose(); jobs?.dispose();
    this.publish({admission: 'denied', loading: false, sessions: [], alerts: [], jobs: []});
  };
  refresh(): Promise<void> {
    if (this.disposed) return Promise.resolve();
    const before = this.generation;
    this.lifetime.assert();
    if (this.disposed || before !== this.generation) return Promise.resolve();
    const role = this.admission();
    if (this.disposed || before !== this.generation || this.lifetime.signal.aborted) return Promise.resolve();
    const generation = ++this.generation;
    if (role !== this.role) {
      const oldSessions = this.sessions, oldJobs = this.jobs;
      this.role = role; this.sessionFence = undefined; this.sessionRevision = undefined;
      this.sessions = this.sessionWindow(); this.jobs = role === 'owner' ? this.jobWindow() : undefined;
      const replacement = this.sessions, replacementJobs = this.jobs;
      const own = () => !this.disposed && this.generation === generation && this.role === role && this.sessions === replacement && this.jobs === replacementJobs;
      const current = () => own() && (role === 'denied' ? this.admission() === role : this.valid(role)) && own();
      oldSessions.dispose();
      oldJobs?.dispose();
      if (!current()) return Promise.resolve();
      this.publish({admission: role, loading: false, sessions: [], alerts: [], jobs: []});
      if (!current()) return Promise.resolve();
    }
    if (role === 'denied') return Promise.resolve();
    const sessionsWindow = this.sessions, jobsWindow = this.jobs;
    const own = () => !this.disposed && !this.lifetime.signal.aborted && this.generation === generation && this.role === role && this.sessions === sessionsWindow && this.jobs === jobsWindow;
    const admitted = () => own() && this.valid(role) && own();
    if (!admitted()) return Promise.resolve();
    const alreadyRefreshing = sessionsWindow.refreshing();
    if (!admitted()) return Promise.resolve();
    if (!alreadyRefreshing) {this.sessionFence = undefined; this.sessionRevision = undefined;}
    this.publish({...this.getSnapshot(), loading: true, error: undefined});
    if (!admitted()) return Promise.resolve();
    const started: Promise<unknown>[] = [];
    const track = <T,>(promise: Promise<T>): Promise<T> => {started.push(promise); void promise.catch(() => {}); return promise;};
    const finishEarly = () => Promise.resolve();
    try {
      const sessions = track(sessionsWindow.refresh());
      if (!admitted()) return finishEarly();
      let health: Promise<Measurement> | undefined, storage: Promise<Measurement> | undefined, alerts: Promise<Alert[]> | undefined, jobs: Promise<void> | undefined;
      if (role === 'owner') {
        health = track(this.readers.console.measurement('health')); if (!admitted()) return finishEarly();
        storage = track(this.readers.console.measurement('storage')); if (!admitted()) return finishEarly();
        alerts = track(this.readers.console.alerts()); if (!admitted()) return finishEarly();
        jobs = jobsWindow ? track(jobsWindow.refresh()) : undefined; if (!admitted()) return finishEarly();
      }
      return Promise.all([sessions, jobs, health, storage, alerts]).then(([, , nextHealth, nextStorage, nextAlerts]) => {
        if (!admitted()) return;
        this.publish({...this.getSnapshot(), loading: false, ...(role === 'owner' ? {health: nextHealth, storage: nextStorage, alerts: nextAlerts ?? []} : {})});
      }, error => {if (admitted()) this.publish({...this.getSnapshot(), loading: false, error});});
    } catch (error) {
      if (admitted()) this.publish({...this.getSnapshot(), loading: false, error});
      return finishEarly();
    }
  }
  restartSessions = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>): Promise<void> => {const window = this.sessions, role = this.role; if (window.snapshot() !== expected || !expected.refreshRequired || !this.valid(role) || this.sessions !== window || window.snapshot() !== expected) return Promise.resolve(); this.sessionFence = undefined; this.sessionRevision = undefined; return window.restart(expected);};
  restartJobs = (expected: DashboardCursorSnapshot<Job, undefined>): Promise<void> => {const window = this.jobs; return window && this.valid('owner') ? window.restart(expected) : Promise.resolve();};
  nextSessions = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>) => this.sessions.next(expected);
  previousSessions = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>) => this.sessions.previous(expected);
  retrySessions = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>) => this.sessions.retry(expected);
  nextJobs = (expected: DashboardCursorSnapshot<Job, undefined>) => this.jobs?.next(expected) ?? Promise.resolve();
  previousJobs = (expected: DashboardCursorSnapshot<Job, undefined>) => this.jobs?.previous(expected) ?? Promise.resolve();
  retryJobs = (expected: DashboardCursorSnapshot<Job, undefined>) => this.jobs?.retry(expected) ?? Promise.resolve();
  sessionFocusToken = (id: string): object | undefined => this.sessions.focusToken(id);
  jobFocusToken = (id: string): object | undefined => this.jobs?.focusToken(id);
  focusSession = (id: string, expected: DashboardCursorSnapshot<AdminSession, SessionMeta>): object | undefined => {const window = this.sessions, role = this.role; if (!this.valid(role) || this.sessions !== window || !window.isWitness(expected)) return undefined; const row = expected.items.find(item => item.id === id); if (!row || !window.snapshot().items.includes(row) || this.sessions !== window || !this.valid(role) || this.sessions !== window || !window.isWitness(expected) || !window.snapshot().items.includes(row)) return undefined; return window.focus(id);};
  blurSession = (id: string, token: object, expected: DashboardCursorSnapshot<AdminSession, SessionMeta>): void => {if (this.sessions.snapshot().scope === expected.scope) this.sessions.blur(id, token);};
  focusJob = (id: string, expected: DashboardCursorSnapshot<Job, undefined>): object | undefined => {const window = this.jobs; if (!window || !this.valid('owner') || this.jobs !== window || !window.isWitness(expected)) return undefined; const row = expected.items.find(item => item.id === id); if (!row || !window.snapshot().items.includes(row) || this.jobs !== window || !this.valid('owner') || this.jobs !== window || !window.isWitness(expected) || !window.snapshot().items.includes(row)) return undefined; return window.focus(id);};
  blurJob = (id: string, token: object, expected: DashboardCursorSnapshot<Job, undefined>): void => {const window = this.jobs; if (window && window.snapshot().scope === expected.scope) window.blur(id, token);};
  focusSessionHost = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>): void => {const window = this.sessions; if (window.snapshot() === expected && this.valid(this.role) && this.sessions === window && window.snapshot() === expected) window.focus(undefined);};
  focusJobHost = (expected: DashboardCursorSnapshot<Job, undefined>): void => {const window = this.jobs; if (window && window.snapshot() === expected && this.valid('owner') && this.jobs === window && window.snapshot() === expected) window.focus(undefined);};
  dispose(): void {if (this.cleaned) return; this.cleaned = true; this.retire(); this.lifetime.signal.removeEventListener('abort', this.retire); this.readers.console.dispose(); this.clearListeners();}
}
