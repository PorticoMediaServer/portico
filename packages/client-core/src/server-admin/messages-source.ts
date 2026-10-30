import type {ReportTransport} from '../client-reports.ts';
import {LOG_DETAIL_MINUTES, type LogDetailMinutes} from '../server-administration.ts';
import {parseLogPage, parseLogEvent, parseLogSettings, logLevels, type LogRecord, type LogLevel, type LogCategory, type LogSettings} from '../server-administration.ts';
import {readEventStream} from '../sse.ts';
import {randomId} from '../random-id.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';

export type MessageConnection = Readonly<{open: (headers: Record<string, string>, signal: AbortSignal) => Promise<Response>}>;
export type MessagesSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; lines: readonly LogRecord[]; level: LogLevel|''; category: LogCategory|''; live: boolean; reconnecting: boolean; readAt?: number; settings?: LogSettings; changing: boolean}>;
const empty: MessagesSnapshot = Object.freeze({phase: 'idle', lines: [], level: '', category: '', live: false, reconnecting: false, changing: false});
export function appendMessage(lines: readonly LogRecord[], record: LogRecord): readonly LogRecord[] {
  return lines.some(line => line.sequence === record.sequence) ? lines : [...lines.slice(-499), record];
}
export function messageRetryMs(attempt: number, retryAfter: string|null, now = Date.now()): number {
  if (retryAfter !== null) {
    const seconds = Number(retryAfter), date = Date.parse(retryAfter);
    if (retryAfter.trim() && Number.isFinite(seconds) && seconds >= 0) return Math.min(seconds * 1000, 2147483647);
    if (Number.isFinite(date)) return Math.min(Math.max(0, date - now), 2147483647);
  }
  return 1000 * 2 ** Math.min(Math.max(attempt, 1), 6);
}
function terminalMessageError(error: unknown): boolean {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized', 'forbidden', 'not_permitted', 'scope_changed', 'viewer_changed', 'settings_scope_changed'].includes(code || '');
}
/** One captured server/viewer lifetime, bounded to 500 redacted server records. */
export class SettingsMessagesSource {
  private state: MessagesSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private tail?: AbortController;
  private timer?: ReturnType<typeof setTimeout>;
  private lastId = '';
  private generation = 0;
  private retired = false;
  private detailIntent?: Readonly<{operationId: string; minutes: number}>;
  constructor(private readonly transport: ReportTransport, private readonly connection: MessageConnection|undefined, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}, private readonly operationId: () => string = randomId) {}
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
    const {level, category} = this.state;
    try {
      const query = new URLSearchParams({limit: '200', ...(level ? {level} : {}), ...(category ? {category} : {})});
      const [page, settings] = await Promise.all([
        this.transport.request<unknown>(routePath('get_admin_logs') + '?' + query, 'GET', undefined, read.signal).then(parseLogPage),
        this.transport.request<unknown>(routePath('get_admin_logs_settings'), 'GET', undefined, read.signal).then(parseLogSettings),
      ]);
      if (this.read !== read || read.signal.aborted || !this.current()) return;
      this.publish({phase: 'ready', lines: [...page.items].reverse(), settings, readAt: Date.now()});
    } catch (error) {if (this.read === read && !read.signal.aborted && this.current()) {this.publish({phase: 'error'});this.failure(error);}}
  };
  select = async (level: LogLevel|'', category: LogCategory|'') => {
    if (!this.current()) return;
    const generation = ++this.generation;this.stopTail();this.lastId = '';
    this.publish({level, category, lines: []});await this.load();
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
        response = await this.connection!.open({Accept: 'text/event-stream', ...(this.lastId ? {'Last-Event-ID': this.lastId} : {})}, controller.signal);
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
          if (!active()) return;
          if (event.id) this.lastId = event.id;
          if (event.event === 'reset') {this.publish({lines: []});return;}
          if (event.event !== 'log' && event.event !== 'message') return;
          let record: LogRecord;try {record = parseLogEvent(event.data);} catch {return;}
          const {level, category} = this.state;
          if (category && record.category !== category || level && logLevels.indexOf(record.level) > logLevels.indexOf(level)) return;
          this.publish({lines: appendMessage(this.state.lines, record)});
        }, controller.signal);
      } catch (error) {if (!active()) return;
        if (terminalMessageError(error)) {this.failure(error);return;}
      } finally {try {await response?.body?.cancel();} catch { /* The fetch transport may already have cancelled it. */ }}
      if (!active()) return;
      this.publish({reconnecting: true});attempt++;
      this.timer = setTimeout(() => {void connect();}, messageRetryMs(attempt, retryAfter));
    };
    void connect();
  }
  detail = async (on: boolean, configuredMinutes?: LogDetailMinutes): Promise<void> => {
    if (!this.current() || this.state.changing) return;
    // An uncertain write keeps its original intent, including its operation ID. Read it
    // back before another POST: the server's runtime window is not revisioned.
    const pending = this.detailIntent;
    const minutes = configuredMinutes ?? this.state.settings?.logDetailMinutes ?? 30;
    if (!pending && on && !(LOG_DETAIL_MINUTES as readonly number[]).includes(minutes)) return;
    const intent = pending ?? Object.freeze({operationId: this.operationId(), minutes: on ? minutes : 0});
    this.detailIntent = intent;this.publish({changing: true});
    try {
      if (pending) {
        const settings = parseLogSettings(await this.transport.request(routePath('get_admin_logs_settings'), 'GET', undefined, this.lifetime.signal));
        if (!this.current()) return;
        this.publish({settings});
        const active = !!settings.debugWindowUntil && Date.parse(settings.debugWindowUntil) > Date.now();
        if (active === (intent.minutes > 0)) {this.detailIntent = undefined;return;}
        // Reconcile the old command only. An opposite click cannot replay it; the
        // owner can choose a fresh action once this read has settled the display.
        if (on !== (intent.minutes > 0)) {this.detailIntent = undefined;return;}
      }
      await this.transport.request(routePath('post_admin_logs_debug_window'), 'POST', intent, this.lifetime.signal);
      if (!this.current()) return;
      this.detailIntent = undefined;await this.load();
    } catch (error) {if (this.current()) this.failure(error);throw error;}
    finally {this.publish({changing: false});}
  };
  pause = () => {++this.generation;this.stopTail();this.read?.abort();this.read = undefined;this.publish({live: false, reconnecting: false});};
  dispose = () => {this.pause();this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
