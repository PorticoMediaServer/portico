import type {Job} from '../../../../../packages/client-core/src/console';
import type {DashboardStatus} from '../../../../../packages/client-core/src/admin/dashboard';
import {storageFacts, type ServerStorage} from '../../../../../packages/client-core/src/server-admin/server-storage';
import type {HealthFacts} from '../../../../../packages/client-core/src/server-admin/dashboard';
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
  /** The header's facts: needs attention while any alert is major, free space and the database's size. */
  health?: HealthFacts;
  /** When the Dashboard read was answered. */
  observedAt?: string;
  alerts: readonly DashboardAlert[];
  jobs: readonly Job[];
  error?: unknown;
}>;
type SessionPage = Awaited<ReturnType<AdminSessionsClient['list']>>;
type SessionMeta = Readonly<{totals: SessionPage['totals']; scope: SessionPage['scope']; revision?: string}>;
export type DashboardAlert = DashboardStatus['alerts'][number];
/** The rebuilt server's reads: Now playing, the Dashboard read (alerts, scans, tasks) and the data folder's storage. */
export type NativeDashboardReaders = Readonly<{
  sessions: Pick<AdminSessionsClient, 'list'>;
  status: () => Promise<DashboardStatus>;
  storage: () => Promise<ServerStorage>;
}>;

const jobStates: Readonly<Record<string, Job['state']>> = {waiting: 'queued', queued: 'queued', running: 'running', paused: 'paused', failed: 'failed', done: 'succeeded', succeeded: 'succeeded', cancelled: 'cancelled'};
/** The Dashboard read's scans and tasks as the jobs the screens draw. The rebuilt server takes no job commands, so none
 *  offers an action, and it lists them all at once: there is never a next page. */
export function dashboardJobs(status: DashboardStatus): readonly Job[] {
  const job = (id: string, kind: string, resource: string, state: Job['state'], phase: string, processed?: number, total?: number): Job =>
    ({id, kind, resource, trigger: '', state, phase, revision: 0, attempt: 0, createdAt: 0, updatedAt: 0, nextAt: 0, settingsRevision: 0, lane: '', actions: [], ...(processed === undefined ? {} : {processed}), ...(total === undefined ? {} : {total})});
  return Object.freeze([
    ...status.scans.map(scan => job('scan:' + scan.libraryId, 'scan', scan.libraryName, 'running', scan.phase, scan.analyzed, scan.total)),
    ...status.tasks.items.map(task => job(task.id, task.kind, task.libraryName ?? '', jobStates[task.state] ?? 'running', task.phase, task.processed, task.total)),
  ]);
}

/** Re-evaluate main-profile and live membership authority before every read and publication. */
export class NativeDashboardController extends NativeScreenStore<NativeDashboardSnapshot> {
  private disposed = false;
  private cleaned = false;
  private generation = 0;
  private role: NativeDashboardAdmission;
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
      if (page.scope.serverId !== this.lifetime.authority.serverId || this.sessionRevision && this.sessionRevision !== page.revision) throw new Error('dashboard_refresh_required');
      if (page.revision) this.sessionRevision = page.revision;
      return {items: page.items, nextCursor: page.nextCursor, metadata: {totals: page.totals, scope: page.scope, revision: page.revision}};
    }, row => row.id, () => current(), snapshot => {
      if (current()) this.publish({...this.getSnapshot(), sessionWindow: snapshot, sessions: snapshot.items, nextCursor: snapshot.nextCursor, totals: snapshot.metadata?.totals ?? this.getSnapshot().totals});
    });
    const current = (): boolean => this.sessions === window && this.valid(role) && this.sessions === window;
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
      this.role = role; this.sessionRevision = undefined;
      this.sessions = this.sessionWindow(); this.jobs = undefined;
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
    if (!alreadyRefreshing) {this.sessionRevision = undefined;}
    this.publish({...this.getSnapshot(), loading: true, error: undefined});
    if (!admitted()) return Promise.resolve();
    const started: Promise<unknown>[] = [];
    const track = <T,>(promise: Promise<T>): Promise<T> => {started.push(promise); void promise.catch(() => {}); return promise;};
    const finishEarly = () => Promise.resolve();
    try {
      const sessions = track(sessionsWindow.refresh());
      if (!admitted()) return finishEarly();
      let status: Promise<DashboardStatus> | undefined, storage: Promise<ServerStorage | undefined> | undefined;
      if (role === 'owner') {
        status = track(this.readers.status()); if (!admitted()) return finishEarly();
        // Free space is a nicety in the header: a storage read that fails leaves it out, never the page.
        storage = track(this.readers.storage().catch(() => undefined)); if (!admitted()) return finishEarly();
      }
      return Promise.all([sessions, status, storage]).then(([, nextStatus, nextStorage]) => {
        if (!admitted()) return;
        const owned = role === 'owner' && nextStatus ? {
          alerts: nextStatus.alerts, jobs: dashboardJobs(nextStatus), observedAt: nextStatus.observedAt,
          health: Object.freeze({degraded: nextStatus.alerts.some(alert => alert.major), ...(nextStorage ? storageFacts(nextStorage) : {})}),
        } : {};
        this.publish({...this.getSnapshot(), loading: false, ...owned});
      }, error => {if (admitted()) this.publish({...this.getSnapshot(), loading: false, error});});
    } catch (error) {
      if (admitted()) this.publish({...this.getSnapshot(), loading: false, error});
      return finishEarly();
    }
  }
  restartSessions = (expected: DashboardCursorSnapshot<AdminSession, SessionMeta>): Promise<void> => {const window = this.sessions, role = this.role; if (window.snapshot() !== expected || !expected.refreshRequired || !this.valid(role) || this.sessions !== window || window.snapshot() !== expected) return Promise.resolve(); this.sessionRevision = undefined; return window.restart(expected);};
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
  dispose(): void {if (this.cleaned) return; this.cleaned = true; this.retire(); this.lifetime.signal.removeEventListener('abort', this.retire); this.clearListeners();}
}
