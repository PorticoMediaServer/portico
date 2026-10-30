import {encodedRoutePath} from '../../../contracts/src/route-paths.ts';
import {ConsoleClient, type FeedbackStatus, type ConsoleAPI} from '../console.ts';
import type {FeedbackItem} from '../notifications.ts';
import type {ReportTransport} from '../client-reports.ts';
import {randomId} from '../random-id.ts';

export type FeedbackReport = FeedbackItem;

export type FeedbackSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  reports: readonly FeedbackReport[];
  /** The server's own cursor; present only while it holds more reports. */
  next?: string;
}>;

const empty: FeedbackSnapshot = Object.freeze({phase: 'idle', reports: Object.freeze([])});

/**
 * Owner-scoped viewer feedback over the feedback-report routes. The list, one report
 * and the status/reply write all go through the same console client and captured
 * viewer fence as the rest of Settings. A reply reaches the reporter's inbox.
 */
export class SettingsFeedbackSource {
  private state: FeedbackSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  private readonly console: ConsoleClient;
  private readonly reportIO = new Map<string, {signal: AbortSignal; admit: () => boolean}>();
  private mutation?: object;
  /** One operation per intended change, kept until the server answers it. */
  private readonly operations = new Map<string, string>();
  constructor(transport: ReportTransport, serverId: string, viewerId: string, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}) {
    const api = transport as ConsoleAPI;
    this.console = new ConsoleClient({request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
      const held = this.reportIO.get(path.split('?')[0]);
      const valid = () => this.current() && !signal?.aborted && (!held || !held.signal.aborted && held.admit());
      if (!valid()) throw Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});
      const request = new AbortController(), abort = () => request.abort();
      signal?.addEventListener('abort', abort, {once: true});held?.signal.addEventListener('abort', abort, {once: true});this.lifetime.signal.addEventListener('abort', abort, {once: true});
      try {
        if (!valid()) throw Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});
        const value = await (api.requestConsole ? api.requestConsole<T>(path, method, body, request.signal) : api.request<T>(path, method, body, request.signal));
        if (!valid()) throw Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});
        return value;
      } catch (error) {
        const failure = error && typeof error === 'object' ? error as {status?: number; code?: string} : {};
        if (this.current() && (failure.status === 401 || failure.status === 403 || ['unauthorized','forbidden','not_permitted'].includes(failure.code ?? ''))) this.retire();
        throw error;
      } finally {signal?.removeEventListener('abort', abort);held?.signal.removeEventListener('abort', abort);this.lifetime.signal.removeEventListener('abort', abort);}
    }}, {serverId, viewerId});
    this.console.onDenied(() => this.retire());
  }
  private retire() {if (this.current()) {this.dispose();this.reportError(new Error('feedback_authority_ended'));}}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): FeedbackSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: FeedbackSnapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}}
  load = (): Promise<void> => {
    if (!this.current() || this.reading) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      this.publish({...this.state, phase: 'loading'});
      try {
        const page = await this.console.feedbackReports();
        if (!this.current()) return;
        this.publish({phase: 'ready', reports: page.items, ...(page.nextCursor ? {next: page.nextCursor} : {})});
      } catch {if (this.current() && !this.lifetime.signal.aborted) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task;return task;
  };
  /** The next page the server named, appended in its order. */
  more = (): Promise<void> => {
    const cursor = this.state.next;
    if (!this.current() || this.reading || !cursor) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return;
      this.publish({...this.state, phase: 'loading'});
      try {
        const page = await this.console.feedbackReports(cursor);
        if (!this.current()) return;
        const known = new Set(this.state.reports.map(report => report.id));
        this.publish({...this.state, phase: 'ready', reports: [...this.state.reports, ...page.items.filter(report => !known.has(report.id))], ...(page.nextCursor ? {next: page.nextCursor} : {next: undefined})});
      } catch {if (this.current()) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task;return task;
  };
  /** One report exactly as the server keeps it, for the dialog that is open now. */
  private async withReportIO<T>(id: string, signal: AbortSignal, admit: () => boolean, action: () => Promise<T>): Promise<T> {
    const paths = [encodedRoutePath('get_admin_feedback_reports_id', {id: encodeURIComponent(id)}), encodedRoutePath('post_admin_feedback_reports_id_status', {id: encodeURIComponent(id)})];
    if (!this.current() || signal.aborted || !admit() || paths.some(path => this.reportIO.has(path))) throw Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});
    const held = {signal, admit};for (const path of paths) this.reportIO.set(path, held);
    try {return await action();} finally {for (const path of paths) if (this.reportIO.get(path) === held) this.reportIO.delete(path);}
  }
  async read(id: string, signal: AbortSignal): Promise<FeedbackReport> {
    const selected = this.state.reports.find(report => report.id === id);
    if (!selected) throw new Error('The selected report is no longer listed.');
    const admit = () => this.current() && !signal.aborted && this.state.reports.includes(selected);
    return this.withReportIO(id, signal, admit, async () => {
      const report = await this.console.feedbackReport(id);
      if (!admit() || report.id !== id) throw Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});
      this.publish({...this.state, reports: this.state.reports.map(row => row === selected ? report : row)});
      return report;
    });
  }
  /**
   * Save status and reply on the report the dialog captured. If the report moved while the
   * dialog was open, the owner's edit is carried over to the newer revision once (ruling: the
   * change is silent), and the answer is the row the list then shows.
   */
  async triage(report: FeedbackReport, status: FeedbackStatus, reply: string, signal: AbortSignal): Promise<FeedbackReport> {
    if (!this.current() || signal.aborted || this.mutation || !this.state.reports.includes(report) || !['open','in-progress','resolved','closed'].includes(status) || reply.length > 2000) throw Object.assign(new Error('The feedback scope ended.'), {name: 'AbortError'});
    const flight = {};this.mutation = flight;
    const admit = () => this.current() && !signal.aborted && this.mutation === flight && this.state.reports.includes(report);
    try {
      return await this.withReportIO(report.id, signal, admit, async () => {
        let current = report;
        for (let attempt = 0; attempt < 2; attempt++) {
          try {
            if (!admit()) throw Object.assign(new Error('The feedback scope ended.'), {name: 'AbortError'});
            // A retry after a lost answer reuses its operation, so it is the same change.
            const intent = JSON.stringify([current.id, current.revision, status, reply]);
            const operationId = this.operations.get(intent) ?? randomId();this.operations.set(intent, operationId);
            const saved = await this.console.transitionFeedback(current.id, {operationId, expectedRevision: current.revision, status, reply});
            this.operations.delete(intent);
            if (!admit() || saved.id !== report.id) throw Object.assign(new Error('The feedback scope ended.'), {name: 'AbortError'});
            this.publish({...this.state, reports: this.state.reports.map(row => row === report ? saved : row)});
            return saved;
          } catch (error) {
            if (!admit()) throw Object.assign(new Error('The feedback scope ended.'), {name: 'AbortError'});
            const code = (error as {code?: string}).code, httpStatus = (error as {status?: number}).status;
            // A stated refusal is final for that operation; only a lost answer keeps it.
            if (typeof httpStatus === 'number' && httpStatus >= 400 && httpStatus < 500) this.operations.delete(JSON.stringify([current.id, current.revision, status, reply]));
            if (attempt === 0 && (code === 'console_conflict' || code === 'revision_mismatch')) {
              current = await this.console.feedbackReport(report.id);
              if (!admit() || current.id !== report.id) throw Object.assign(new Error('The feedback scope ended.'), {name: 'AbortError'});
              continue;
            }
            throw error;
          }
        }
        throw new Error('feedback_conflict');
      });
    } finally {if (this.mutation === flight) this.mutation = undefined;}
  }
  retry = (): Promise<void> => {this.state = empty;return this.load();};
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.listeners.clear();this.reportIO.clear();this.operations.clear();this.mutation = undefined;this.console.dispose();this.state = empty;}}
}

/** The kind of problem the triage screens draw: Playback, Metadata, Subtitles or Other. */
export function feedbackTriageCategory(report: Pick<FeedbackItem, 'kind' | 'category'>): 'playback' | 'metadata' | 'subtitles' | 'other' {
  return report.kind === 'playback' ? report.category === 'subtitles' ? 'subtitles' : 'playback' : report.kind === 'library' ? 'metadata' : 'other';
}

/** Where a report came from, as the `server.feedback.sentFrom` sentence takes it. */
export function feedbackSentFrom(report: Pick<FeedbackItem, 'client'>): Readonly<{platform: string; version: string; online: 'yes' | 'no' | 'unknown'}> | undefined {
  const client = report.client;
  return client ? {platform: client.platform, version: client.appVersion, online: client.online === undefined ? 'unknown' : client.online ? 'yes' : 'no'} : undefined;
}
