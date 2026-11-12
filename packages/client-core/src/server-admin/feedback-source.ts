import type {FeedbackStatus} from '../console.ts';
import type {ProblemReport} from '../../../contracts/src/next/api.ts';
import type {ReportTransport} from '../client-reports.ts';
import {serverPath} from '../query/server-api.ts';

/** One report a person sent with "Report a problem", or a crash report an app sent, as the
 * owner's list shows it. What it is about, the title and the app come from the report's context,
 * as the app described them; a report without them reads as "Something else". */
export type FeedbackReport = Readonly<{
  id: string;
  status: FeedbackStatus;
  /** What it is about: playback, library, apps, account or other (a crash report is apps). */
  kind: string;
  category: string;
  message: string;
  /** The reporter's username and profile, as the server names them. */
  reporter: string;
  crash: boolean;
  /** The report came with the app's log (crash reports only, for now). */
  hasLog: boolean;
  itemId?: string;
  itemTitle?: string;
  /** Milliseconds since 1970. */
  createdAt: number;
  client?: Readonly<{platform: string; appVersion?: string; online?: boolean}>;
  /** The owner's last reply, which reached the reporter as a notice. */
  reply?: string;
  /** The current name of the device it was sent from, as the server knows it from the sign-in
   * that sent it (never what the app wrote); absent when that device was forgotten. */
  deviceName?: string;
}>;

export type FeedbackSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  reports: readonly FeedbackReport[];
  /** The server's own cursor; present only while it holds more reports. */
  next?: string;
}>;

const empty: FeedbackSnapshot = Object.freeze({phase: 'idle', reports: Object.freeze([])});
const STATUSES: Readonly<Record<string, FeedbackStatus>> = {open: 'open', in_progress: 'in-progress', resolved: 'resolved', closed: 'closed'};
const WIRE: Readonly<Record<FeedbackStatus, string>> = {open: 'open', 'in-progress': 'in_progress', resolved: 'resolved', closed: 'closed'};
const text = (value: unknown, max: number) => typeof value === 'string' && value.length <= max ? value : undefined;
const ended = () => Object.assign(new Error('The feedback request ended.'), {name: 'AbortError'});

/** A report as the server sends it. A status this app does not know reads as open; a report
 * without an id is refused. */
export function parseOwnerReport(value: unknown): FeedbackReport {
  const v = value as Partial<ProblemReport> & {context?: unknown} | null;
  if (!v || typeof v !== 'object' || !text(v.id, 200) || !v.id) throw new Error('Invalid report.');
  const context = v.context && typeof v.context === 'object' && !Array.isArray(v.context) ? v.context as Record<string, unknown> : {};
  const crash = v.kind === 'crash';
  const [kind, category] = crash ? ['apps', 'crash'] : (text(context.about, 120) ?? '').split('/');
  const item = context.item && typeof context.item === 'object' ? context.item as Record<string, unknown> : undefined;
  const platform = text(context.platform, 40);
  const created = Date.parse(text(v.createdAt, 64) ?? '');
  return Object.freeze({id: v.id, status: STATUSES[v.status ?? ''] ?? 'open', kind: kind || 'other', category: category || 'other',
    message: text(v.message, 10000) ?? '', reporter: text(v.reporter, 400) ?? '', crash, hasLog: v.hasLog === true,
    ...(item && text(item.id, 200) ? {itemId: item.id as string} : {}), ...(item && text(item.title, 400) ? {itemTitle: item.title as string} : {}),
    createdAt: Number.isFinite(created) ? created : 0,
    ...(platform ? {client: Object.freeze({platform, ...(text(context.appVersion, 40) ? {appVersion: context.appVersion as string} : {}),
      ...(typeof context.online === 'boolean' ? {online: context.online} : {})})} : {}),
    ...(text(v.reply, 4000) ? {reply: v.reply} : {}),
    ...(text((v as {deviceName?: unknown}).deviceName, 200) ? {deviceName: (v as {deviceName: string}).deviceName} : {})});
}

/**
 * The owner's reports (`GET /v1/admin/reports`, newest first, a page at a time), one report, and
 * setting its status with a reply (`PATCH /v1/admin/reports/{id}`); the reply reaches the reporter
 * as a notice. Everything goes through the captured viewer's transport; a refusal that says this
 * viewer may no longer read them ends the source.
 */
export class SettingsFeedbackSource {
  private state: FeedbackSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  private mutation?: object;
  constructor(private readonly transport: ReportTransport,
    _serverId: string, _viewerId: string, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}) {}
  private retire() {if (this.current()) {this.dispose(); this.reportError(new Error('feedback_authority_ended'));}}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): FeedbackSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(state: FeedbackSnapshot) {if (this.current()) {this.state = Object.freeze(state); for (const listener of [...this.listeners]) listener();}}

  /** One call under this source's lifetime and the caller's. */
  private async call<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const valid = () => this.current() && !signal?.aborted;
    if (!valid()) throw ended();
    const request = new AbortController(), abort = () => request.abort();
    signal?.addEventListener('abort', abort, {once: true}); this.lifetime.signal.addEventListener('abort', abort, {once: true});
    try {
      const value = await this.transport.request<T>(path, method, body, request.signal);
      if (!valid()) throw ended();
      return value;
    } catch (error) {
      const failure = error && typeof error === 'object' ? error as {status?: number; code?: string} : {};
      if (this.current() && (failure.status === 401 || failure.status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(failure.code ?? ''))) this.retire();
      throw error;
    } finally {signal?.removeEventListener('abort', abort); this.lifetime.signal.removeEventListener('abort', abort);}
  }
  /** The one read: a page of the list, or one report. */
  private document<T>(path: string, signal?: AbortSignal): Promise<T> {return this.call<T>(path, 'GET', undefined, signal);}
  private async page(cursor?: string) {
    const raw = await this.document<{items?: unknown[]; nextCursor?: string}>(serverPath('GET /v1/admin/reports', {query: {cursor}}));
    const items: FeedbackReport[] = [];
    for (const value of raw?.items ?? []) {try {items.push(parseOwnerReport(value));} catch {/* one unreadable report is left out */}}
    return {items, next: text(raw?.nextCursor, 64) || undefined};
  }

  load = (): Promise<void> => {
    if (!this.current() || this.reading) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      this.publish({...this.state, phase: 'loading'});
      try {
        const page = await this.page();
        if (!this.current()) return;
        this.publish({phase: 'ready', reports: page.items, ...(page.next ? {next: page.next} : {})});
      } catch {if (this.current()) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task; return task;
  };
  /** The next page the server named, appended in its order. */
  more = (): Promise<void> => {
    const cursor = this.state.next;
    if (!this.current() || this.reading || !cursor) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      this.publish({...this.state, phase: 'loading'});
      try {
        const page = await this.page(cursor);
        if (!this.current()) return;
        const known = new Set(this.state.reports.map(report => report.id));
        this.publish({phase: 'ready', reports: [...this.state.reports, ...page.items.filter(report => !known.has(report.id))], ...(page.next ? {next: page.next} : {})});
      } catch {if (this.current()) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task; return task;
  };
  /** One report as the server keeps it now, for the dialog that is open. */
  async read(id: string, signal: AbortSignal): Promise<FeedbackReport> {
    const selected = this.state.reports.find(report => report.id === id);
    if (!selected) throw new Error('The selected report is no longer listed.');
    const report = parseOwnerReport(await this.document(serverPath('GET /v1/admin/reports/{id}', {id}), signal));
    if (!this.current() || signal.aborted || report.id !== id || !this.state.reports.includes(selected)) throw ended();
    this.publish({...this.state, reports: this.state.reports.map(row => row === selected ? report : row)});
    return report;
  }
  /** Sets the report's status, and sends a reply when there is one. The last change wins. */
  async triage(report: FeedbackReport, status: FeedbackStatus, reply: string, signal: AbortSignal): Promise<FeedbackReport> {
    if (!this.current() || signal.aborted || this.mutation || !this.state.reports.includes(report) || !WIRE[status] || reply.length > 2000) throw ended();
    const flight = {}; this.mutation = flight;
    try {
      const words = reply.trim();
      const saved = parseOwnerReport(await this.call(serverPath('PATCH /v1/admin/reports/{id}', {id: report.id}), 'PATCH', {status: WIRE[status], ...(words ? {reply: words} : {})}, signal));
      if (!this.current() || signal.aborted || saved.id !== report.id) throw ended();
      this.publish({...this.state, reports: this.state.reports.map(row => row.id === report.id ? saved : row)});
      return saved;
    } finally {if (this.mutation === flight) this.mutation = undefined;}
  }
  retry = (): Promise<void> => {this.state = empty; return this.load();};
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort(); this.listeners.clear(); this.mutation = undefined; this.state = empty;}}
}

/** The kind of problem the triage screens draw: Playback, Metadata, Subtitles or Other. */
export function feedbackTriageCategory(report: Readonly<{kind: string; category: string}>): 'playback' | 'metadata' | 'subtitles' | 'other' {
  return report.kind === 'playback' ? report.category === 'subtitles' ? 'subtitles' : 'playback' : report.kind === 'library' ? 'metadata' : 'other';
}

/** Where a report came from, as the `server.feedback.sentFrom` sentence takes it. */
export function feedbackSentFrom(report: Readonly<{client?: FeedbackReport['client']}>): Readonly<{platform: string; version: string; online: 'yes' | 'no' | 'unknown'}> | undefined {
  const client = report.client;
  return client ? {platform: client.platform, version: client.appVersion ?? '', online: client.online === undefined ? 'unknown' : client.online ? 'yes' : 'no'} : undefined;
}
