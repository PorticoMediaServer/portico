import {deleteClientReport, listClientReports, readClientReport, type ClientReport, type ReportTransport} from '../client-reports.ts';
import {readCatalogueFailures, type CatalogueFailures} from '../catalogue-failures.ts';
import {ConsoleClient, type EvidenceRecord, type ConsoleAPI} from '../console.ts';

export type RecordLane = 'runtime' | 'client' | 'audit';
export type RecordsSlice = Readonly<{lane: RecordLane; items: readonly EvidenceRecord[]; next?: string}>;
export type ReportsSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  reports: readonly ClientReport[];
  /** The server's own cursor; present only while it holds more reports. */
  next?: string;
  /** Titles a catalogue rebuild could not finish; absent until the server answers. */
  setAside?: CatalogueFailures;
  /** What the server noted on the selected lane, a page at a time. */
  records?: RecordsSlice;
}>;

const empty: ReportsSnapshot = Object.freeze({phase: 'idle', reports: Object.freeze([])});

/**
 * Owner-scoped reads for the drawn Client reports panel and its Titles set aside
 * section. One report's body is returned to the caller and never cached here: the
 * log can name titles and devices, so it lives only inside the open dialog.
 */
export class SettingsReportsSource {
  private state: ReportsSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  private readonly console: ConsoleClient;
  private readonly transport: ReportTransport;
  private recordsGeneration = 0;
  private removing?: object;
  constructor(transport: ReportTransport, private readonly serverId: string, viewerId: string, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void = () => {}) {
    const api = transport as ConsoleAPI;
    this.transport = {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => this.request(signal, current => api.request<T>(path, method, body, current))};
    this.console = new ConsoleClient({request: this.transport.request, requestConsole: <T>(path: string, method: string, body: unknown, signal: AbortSignal) => this.request(signal, current => api.requestConsole ? api.requestConsole<T>(path, method, body, current) : api.request<T>(path, method, body, current))}, {serverId, viewerId});
    this.console.onDenied(() => this.retire());
  }
  private retire() {if (this.current()) {this.dispose();this.reportError(new Error('reports_authority_ended'));}}
  private async request<T>(signal: AbortSignal | undefined, send: (signal: AbortSignal) => Promise<T>): Promise<T> {
    if (!this.current() || signal?.aborted) throw Object.assign(new Error('The report request ended.'), {name: 'AbortError'});
    const request = new AbortController(), abort = () => request.abort();
    signal?.addEventListener('abort', abort, {once: true});this.lifetime.signal.addEventListener('abort', abort, {once: true});
    try {
      if (!this.current() || signal?.aborted) throw Object.assign(new Error('The report request ended.'), {name: 'AbortError'});
      const value = await send(request.signal);
      if (!this.current() || signal?.aborted) throw Object.assign(new Error('The report request ended.'), {name: 'AbortError'});
      return value;
    } catch (error) {
      const failure = error && typeof error === 'object' ? error as {status?: number; code?: string} : {};
      if (this.current() && (failure.status === 401 || failure.status === 403 || ['unauthorized','forbidden','not_permitted'].includes(failure.code ?? ''))) this.retire();
      throw error;
    } finally {signal?.removeEventListener('abort', abort);this.lifetime.signal.removeEventListener('abort', abort);}
  }
  listed = (report: ClientReport): boolean => this.current() && this.state.reports.includes(report);
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): ReportsSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: ReportsSnapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}}
  private signal() {return this.lifetime.signal;}
  load = (): Promise<void> => {
    if (!this.current() || this.reading) return this.reading ?? Promise.resolve();
    const recordsGeneration = ++this.recordsGeneration;
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return;
      this.publish({...this.state, phase: 'loading'});
      try {
        const [page, setAside, records] = await Promise.all([
          listClientReports(this.transport, this.serverId, '', this.signal()),
          readCatalogueFailures(this.transport, this.signal()).catch(() => undefined),
          this.console.records('runtime').catch(() => undefined),
        ]);
        if (!this.current()) return;
        this.publish({...this.state, phase: 'ready', reports: page.items, ...(page.nextCursor ? {next: page.nextCursor} : {}), ...(setAside ? {setAside} : {}),
          ...(records && recordsGeneration === this.recordsGeneration ? {records: {lane: 'runtime' as const, items: records.items, ...(records.nextCursor ? {next: records.nextCursor} : {})}} : {})});
      } catch {if (this.current() && !this.lifetime.signal.aborted) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task;return task;
  };
  /** The next page the server named, appended in its order; no page is ever guessed. */
  more = (): Promise<void> => {
    const cursor = this.state.next;
    if (!this.current() || this.reading || !cursor) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return;
      this.publish({...this.state, phase: 'loading'});
      try {
        const page = await listClientReports(this.transport, this.serverId, cursor, this.signal());
        if (!this.current()) return;
        const known = new Set(this.state.reports.map(report => report.id));
        this.publish({...this.state, phase: 'ready', reports: [...this.state.reports, ...page.items.filter(report => !known.has(report.id))], ...(page.nextCursor ? {next: page.nextCursor} : {next: undefined})});
      } catch {if (this.current()) this.publish({...this.state, phase: 'error'});}
      finally {this.reading = undefined;}
    });
    this.reading = task;return task;
  };
  /** One report exactly as the server keeps it, for the dialog that is open now. */
  async read(id: string, signal: AbortSignal, expected?: ClientReport): Promise<ClientReport> {
    const selected = this.state.reports.find(report => report.id === id);
    if (!selected || expected && selected !== expected || !this.current() || signal.aborted) throw Object.assign(new Error('The report selection ended.'), {name: 'AbortError'});
    const report = await readClientReport(this.transport, this.serverId, id, signal);
    if (!this.listed(selected) || signal.aborted || report.id !== id) throw Object.assign(new Error('The report selection ended.'), {name: 'AbortError'});
    return report;
  }
  /** One explicit listed deletion; a lost response is never automatically replayed. */
  async remove(id: string, expected?: ClientReport, caller?: Readonly<{signal: AbortSignal; isCurrent: () => boolean}>): Promise<boolean> {
    const selected = this.state.reports.find(report => report.id === id);
    const callerCurrent = () => !caller || !caller.signal.aborted && caller.isCurrent();
    if (!selected || expected && selected !== expected || !this.current() || this.removing || !callerCurrent()) return false;
    const attempt = {};this.removing = attempt;
    const owns = () => this.current() && this.removing === attempt && callerCurrent();
    const transport: ReportTransport = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!owns()) throw Object.assign(new Error('The report deletion ended.'), {name: 'AbortError'});
      // The original caller signal is combined with the source lifetime by this.transport.
      const value = await this.transport.request<T>(path, method, body, signal);
      if (!owns()) throw Object.assign(new Error('The report deletion ended.'), {name: 'AbortError'});
      return value;
    }};
    this.publish({...this.state, phase: 'loading'});
    try {
      // Publication can synchronously close/retire the original confirmation.
      if (!this.listed(selected) || !owns()) throw Object.assign(new Error('The report deletion ended.'), {name: 'AbortError'});
      await deleteClientReport(transport, id, caller?.signal ?? this.signal());
      if (!owns()) throw Object.assign(new Error('The report deletion ended.'), {name: 'AbortError'});
      const page = await listClientReports(transport, this.serverId, '', caller?.signal ?? this.signal());
      if (!owns()) throw Object.assign(new Error('The report deletion ended.'), {name: 'AbortError'});
      this.publish({...this.state, phase: 'ready', reports: page.items, ...(page.nextCursor ? {next: page.nextCursor} : {next: undefined})});
      return true;
    } catch {if (this.current()) this.publish({...this.state, phase: 'error'});return false;}
    finally {if (this.removing === attempt) this.removing = undefined;}
  }
  /** Switch the records lane from its first page; the list keeps only that lane's rows. */
  selectLane = async (lane: RecordLane): Promise<void> => {
    const shown = this.state.records;
    if (!this.current()) return;
    const generation = ++this.recordsGeneration;
    if (shown?.lane === lane && shown.items.length > 0) return;
    try {
      const page = await this.console.records(lane);
      if (!this.current() || generation !== this.recordsGeneration) return;
      this.publish({...this.state, records: {lane, items: page.items, ...(page.nextCursor ? {next: page.nextCursor} : {})}});
    } catch {if (this.current() && generation === this.recordsGeneration) this.publish({...this.state, phase: 'error'});}
  };
  /** The next page of the selected lane, only when the server named one. */
  moreRecords = async (): Promise<void> => {
    const current = this.state.records;
    if (!this.current() || !current?.next) return;
    const generation = ++this.recordsGeneration;
    try {
      const page = await this.console.records(current.lane, current.next);
      if (!this.current()) return;
      if (generation !== this.recordsGeneration || this.state.records !== current) return;
      const known = new Set(current.items.map(item => item.sequence));
      this.publish({...this.state, records: {lane: current.lane, items: [...current.items, ...page.items.filter(item => !known.has(item.sequence))], ...(page.nextCursor ? {next: page.nextCursor} : {})}});
    } catch {if (this.current() && generation === this.recordsGeneration) this.publish({...this.state, phase: 'error'});}
  };
  /** Back to the first page of the selected lane. */
  latestRecords = async (): Promise<void> => {
    const current = this.state.records;
    if (!this.current() || !current) return;
    const generation = ++this.recordsGeneration;
    try {
      const page = await this.console.records(current.lane);
      if (!this.current()) return;
      if (generation !== this.recordsGeneration || this.state.records !== current) return;
      this.publish({...this.state, records: {lane: current.lane, items: page.items, ...(page.nextCursor ? {next: page.nextCursor} : {})}});
    } catch {if (this.current() && generation === this.recordsGeneration) this.publish({...this.state, phase: 'error'});}
  };
  /** Re-read the titles a rebuild set aside; the server tries them again by itself. */
  checkSetAside = async (): Promise<void> => {
    if (!this.current()) return;
    try {
      const setAside = await readCatalogueFailures(this.transport, this.signal());
      if (this.current()) this.publish({...this.state, setAside});
    } catch {if (this.current() && !this.lifetime.signal.aborted) this.publish({...this.state, phase: 'error'});}
  };
  retry = (): Promise<void> => {this.state = empty;return this.load();};
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.recordsGeneration++;this.removing = undefined;this.listeners.clear();this.console.dispose();this.state = empty;}}
}
