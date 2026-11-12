import type {ReportTransport} from '../client-reports.ts';
import {LOG_DETAIL_MINUTES, logLevels, type LogDetailMinutes, type LogLevel} from '../server-administration.ts';
import {readEventStream} from '../sse.ts';
import {backoffDelay, parseRetryAfter, type Backoff} from '../timer.ts';
import {callServer} from '../query/server-api.ts';
import {serverNow} from '../server-clock.ts';

/**
 * Messages on the rebuilt server (console rules 66 to 68): `GET /v1/admin/logs` is the server's log, newest
 * first, from a least level; `GET /v1/admin/logs/events` follows it live (an `event: line` for each line, the
 * last lines in memory first); `PUT /v1/admin/logs/detail` starts "Record more detail" for some minutes or
 * ends it; and the owner's `logs` settings document says how many days messages are kept (7, 14 or 30).
 * Each line has a kind (server, playback, scan or network), and Messages filters by level and kind. Lines are
 * kept newest first, as rule 66 shows them: a line followed live arrives at the top.
 */
export const MESSAGE_KINDS = ['server', 'playback', 'scan', 'network'] as const;
export type MessageKind = typeof MESSAGE_KINDS[number];
export type MessageConnection = Readonly<{open: (headers: Record<string, string>, signal: AbortSignal) => Promise<Response>}>;
/** One line of the server's log. `sequence` names it on this page: the server gives a line no id. `detail` is its values, "name: value". */
export type MessageLine = Readonly<{sequence: string; at: string; level: LogLevel; kind: MessageKind; message: string; detail: string}>;
export const KEEP_MESSAGE_DAYS = [7, 14, 30] as const;
export type KeepMessageDays = typeof KEEP_MESSAGE_DAYS[number];
export type MessagesSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; lines: readonly MessageLine[]; level: LogLevel|''; kind: MessageKind|''; live: boolean; reconnecting: boolean; readAt?: number;
  /** When "Record more detail" ends; absent while it is off. */
  detailUntil?: string;
  /** How many days messages are kept: read for the owner only. */
  keepDays?: KeepMessageDays;
  /** How long the next "Record more detail" lasts: 30 minutes unless the person chose another length (rule 67). */
  detailMinutes: LogDetailMinutes; changing: boolean}>;
const empty: MessagesSnapshot = Object.freeze({phase: 'idle', lines: [], level: '', kind: '', live: false, reconnecting: false, detailMinutes: 30, changing: false});
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const invalid = () => Object.assign(new Error('invalid_server_log'), {code: 'invalid_server_log'});
const time = (v: unknown): v is string => typeof v === 'string' && Number.isFinite(Date.parse(v));

/** One line as the server sends it, in a page or on the live stream. */
export function messageLine(raw: unknown): MessageLine {
  if (!object(raw) || !time(raw.time) || typeof raw.message !== 'string' || !(logLevels as readonly unknown[]).includes(raw.level)) throw invalid();
  const attrs = object(raw.attrs) ? Object.entries(raw.attrs).filter((e): e is [string, string] => typeof e[1] === 'string') : [];
  const detail = attrs.map(([name, value]) => name + ': ' + value).join(' · ');
  // A line from a server without kinds reads as the server's own.
  const kind = (MESSAGE_KINDS as readonly unknown[]).includes(raw.kind) ? raw.kind as MessageKind : 'server';
  return Object.freeze({sequence: [raw.time, raw.level, raw.message, detail].join('\u0000'), at: raw.time, level: raw.level as LogLevel, kind, message: raw.message, detail});
}
function linesPage(raw: unknown): Readonly<{lines: readonly MessageLine[]; detailUntil?: string}> {
  if (!object(raw) || !Array.isArray(raw.items) || raw.detailUntil !== undefined && !time(raw.detailUntil)) throw invalid();
  return {lines: raw.items.map(messageLine), ...(raw.detailUntil ? {detailUntil: raw.detailUntil as string} : {})};
}
function keptDays(raw: unknown): KeepMessageDays {
  const days = object(raw) && object(raw.values) ? raw.values.keepDays : undefined;
  if (!(KEEP_MESSAGE_DAYS as readonly unknown[]).includes(days)) throw invalid();
  return days as KeepMessageDays;
}
/** Adds a line once, newest first, keeping the newest 500. The live stream starts with lines already shown. */
export function appendMessage(lines: readonly MessageLine[], line: MessageLine): readonly MessageLine[] {
  if (lines.some(l => l.sequence === line.sequence)) return lines;
  const at = Date.parse(line.at);
  let index = 0;
  while (index < lines.length && Date.parse(lines[index].at) > at) index++;
  return [...lines.slice(0, index), line, ...lines.slice(index)].slice(0, 500);
}
const messageBackoff: Backoff = {baseMs: 2000, maxMs: 64000, jitter: 'none'};
export function messageRetryMs(attempt: number, retryAfter: string|null, now = serverNow()): number {
  if (retryAfter !== null) {
    const ms = parseRetryAfter(retryAfter, now);
    if (ms !== undefined) return Math.min(ms, 2147483647);
    // The helper reports no floor for zero, a past date or a number past safe integers; the first two mean "reconnect now".
    const text = retryAfter.trim();
    if (/^\d+$/.test(text)) return /^0+$/.test(text) ? 0 : 2147483647;
    if (/[a-z]/i.test(text) && Number.isFinite(Date.parse(text))) return 0;
  }
  return backoffDelay(messageBackoff, attempt);
}
function terminalMessageError(error: unknown): boolean {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized', 'forbidden', 'not_permitted', 'scope_changed', 'viewer_changed', 'settings_scope_changed'].includes(code || '');
}
/** One captured server/viewer lifetime, bounded to the last 500 lines. */
export class SettingsMessagesSource {
  private state: MessagesSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private tail?: AbortController;
  private timer?: ReturnType<typeof setTimeout>;
  private generation = 0;
  private retired = false;
  private readonly keeping: boolean;
  /** `keeping` reads and sets how long messages are kept: the owner's alone, so an administrator is never sent that read. */
  constructor(private readonly transport: ReportTransport, private readonly connection: MessageConnection|undefined, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}, options: Readonly<{keeping?: boolean}> = {}) {
    this.keeping = options.keeping === true;
  }
  private current() {return !this.retired && !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): MessagesSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<MessagesSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
  private failure(error: unknown) {
    if (terminalMessageError(error)) {
      this.pause();this.retired = true;this.state = empty;this.lifetime.abort();
      for (const listener of this.listeners) listener();
    }
    this.reportError(error);
  }
  load = async (): Promise<void> => {
    if (!this.current()) return;
    this.read?.abort(); const read = new AbortController();this.read = read;
    this.publish({phase: 'loading'});
    const {level, kind} = this.state;
    try {
      const [page, keepDays] = await Promise.all([
        callServer(this.transport, 'GET /v1/admin/logs', {query: {level: level || undefined, kind: kind || undefined}, signal: read.signal}).then(linesPage),
        this.keeping ? callServer(this.transport, 'GET /v1/admin/settings/{name}', {id: 'logs', signal: read.signal}).then(keptDays) : Promise.resolve(undefined),
      ]);
      if (this.read !== read || read.signal.aborted || !this.current()) return;
      this.publish({phase: 'ready', lines: page.lines.slice(0, 500), detailUntil: page.detailUntil, ...(keepDays ? {keepDays} : {}), readAt: serverNow()});
    } catch (error) {if (this.read === read && !read.signal.aborted && this.current()) {this.publish({phase: 'error'});this.failure(error);}}
  };
  select = async (level: LogLevel|'', kind: MessageKind|'' = this.state.kind) => {
    if (!this.current()) return;
    const generation = ++this.generation;this.stopTail();
    this.publish({level, kind, lines: []});await this.load();
    if (generation === this.generation && this.current() && this.state.phase === 'ready') this.startTail();
  };
  setLive = (live: boolean) => {if (!this.current()) return;++this.generation;this.stopTail();this.publish({live, reconnecting: false});if (live) this.startTail();};
  private stopTail() {this.tail?.abort();this.tail = undefined;clearTimeout(this.timer);}
  private startTail() {
    if (!this.current() || !this.connection || !this.state.live) return;
    const controller = new AbortController();this.tail = controller;let attempt = 0;
    const active = () => this.current() && this.tail === controller && !controller.signal.aborted && this.state.live;
    const connect = async () => {
      if (!active()) return;
      let retryAfter: string|null = null;
      let response: Response|undefined;
      try {
        response = await this.connection!.open({Accept: 'text/event-stream'}, controller.signal);
        if (!active()) {await response.body?.cancel();return;}
        if (!response.ok) {
          if (response.status === 429) retryAfter = response.headers.get('Retry-After');
          await response.body?.cancel();
          // Authentication/permission failures need a fresh user action, never an endless retry.
          if (response.status === 401 || response.status === 403) {this.failure(Object.assign(new Error('Stream permission refused.'), {status: response.status}));return;}
          throw new Error('Stream refused.');
        }
        this.publish({reconnecting: false});attempt = 0;
        await readEventStream(response, event => {
          if (!active() || event.event !== 'line') return;
          let line: MessageLine;try {line = messageLine(JSON.parse(event.data));} catch {return;}
          const {level, kind} = this.state;
          if (level && logLevels.indexOf(line.level) > logLevels.indexOf(level) || kind && line.kind !== kind) return;
          this.publish({lines: appendMessage(this.state.lines, line)});
        }, controller.signal);
      } catch (error) {if (!active()) return;
        if (terminalMessageError(error)) {this.failure(error);return;}
      } finally {try {await response?.body?.cancel();} catch { /* The fetch transport may already have cancelled it. */ }}
      // The server ends a follow after half an hour, and on a restart: open it again.
      if (!active()) return;
      this.publish({reconnecting: true});attempt++;
      this.timer = setTimeout(() => {void connect();}, messageRetryMs(attempt, retryAfter));
    };
    void connect();
  }
  /** The length the next "Record more detail" lasts, wherever it is started from. */
  chooseLength = (minutes: LogDetailMinutes) => {if ((LOG_DETAIL_MINUTES as readonly number[]).includes(minutes)) this.publish({detailMinutes: minutes});};
  /** Starts "Record more detail" for the chosen length or ends it. The same request is safe to send again. */
  detail = async (on: boolean): Promise<void> => {
    const minutes = this.state.detailMinutes;
    if (!this.current() || this.state.changing) return;
    this.publish({changing: true});
    try {
      const answer = await callServer(this.transport, 'PUT /v1/admin/logs/detail', {body: {minutes: on ? minutes : 0}, signal: this.lifetime.signal});
      if (!this.current()) return;
      if (!object(answer) || answer.detailUntil !== undefined && !time(answer.detailUntil)) throw invalid();
      this.publish({detailUntil: answer.detailUntil});
    } catch (error) {if (this.current()) this.failure(error);throw error;}
    finally {this.publish({changing: false});}
  };
  /** Sets how many days messages are kept (the owner's). */
  keep = async (days: KeepMessageDays): Promise<void> => {
    if (!this.current() || !this.keeping || this.state.changing || !(KEEP_MESSAGE_DAYS as readonly number[]).includes(days)) return;
    this.publish({changing: true});
    try {
      const saved = keptDays(await callServer(this.transport, 'PUT /v1/admin/settings/{name}', {id: 'logs', body: {keepDays: days}, signal: this.lifetime.signal}));
      this.publish({keepDays: saved});
    } catch (error) {if (this.current()) this.failure(error);throw error;}
    finally {this.publish({changing: false});}
  };
  pause = () => {++this.generation;this.stopTail();this.read?.abort();this.read = undefined;this.publish({live: false, reconnecting: false});};
  dispose = () => {this.pause();this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
