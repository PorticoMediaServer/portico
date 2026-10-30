import {encodedRoutePath} from '../../contracts/src/route-paths.ts';
import {parseLyricsView, type LyricsView, type LyricRevision} from './lyrics.ts';
import type {RepairScope, RepairTarget} from './metadata-repair.ts';

type Api = Readonly<{request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>}>;
type Snapshot = Readonly<{phase: 'loading' | 'ready' | 'saving' | 'uncertain' | 'error' | 'denied'; data: LyricsView | null; error: unknown}>;
/** Owner-wide lyrics are bound to the editor's exact repair grant, source, fence and CAS. */
export class MetadataLyricsService {
  private state: Snapshot = Object.freeze({phase: 'loading', data: null, error: null});
  private readonly listeners = new Set<() => void>();
  private epoch = 0;
  private disposed = false;
  private controller?: AbortController;
  private fence?: string;
  private readonly options: {api: Api; scope: RepairScope; target: RepairTarget; current: () => boolean};
  constructor(options: {api: Api; scope: RepairScope; target: RepairTarget; current: () => boolean}) {
    if (options.target.kind !== 'item') throw Error('Choose a song.');
    this.options = {...options, scope: Object.freeze({...options.scope}), target: Object.freeze({...options.target})};
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(state: Snapshot) {if (this.disposed) return; this.state = Object.freeze(state); for (const listener of this.listeners) listener();}
  private current(epoch: number) {return !this.disposed && this.epoch === epoch && this.options.current();}
  private path(selection = false) {return encodedRoutePath(selection ? 'put_items_id_lyrics_selection' : 'get_items_id_lyrics', {id: encodeURIComponent(this.options.target.id)});}
  private async request(path: string, method: string, body: unknown, epoch: number) {
    if (!this.current(epoch)) throw new DOMException('Lyrics grant retired', 'AbortError');
    const controller = new AbortController(); this.controller?.abort(); this.controller = controller;
    const timer = setTimeout(() => controller.abort(), 22000);
    try {
      const result = await this.options.api.request<unknown>(path, method, body, controller.signal);
      if (!this.current(epoch)) throw new DOMException('Lyrics grant retired', 'AbortError');
      return result;
    } finally {clearTimeout(timer);}
  }
  private accept(raw: unknown, expectedSource?: LyricsView['source']) {
    const target = {itemId: this.options.target.id, libraryId: this.options.target.libraryId};
    const data = parseLyricsView(raw, this.options.scope, target);
    if (!data.canShare || this.fence && data.scope.viewerFence !== this.fence) throw Object.assign(Error('Lyrics owner changed'), {status: 403});
    if (expectedSource && (data.source?.id !== expectedSource.id || data.source.version !== expectedSource.version)) throw Object.assign(Error('Lyrics source changed'), {status: 409});
    this.fence = data.scope.viewerFence;
    this.publish({phase: 'ready', data, error: null});
  }
  private failure(error: unknown, writing: boolean) {
    const status = (error as {status?: number} | null)?.status;
    const denied = status === 401 || status === 403;
    this.publish({phase: denied ? 'denied' : writing && status !== 409 && status !== 400 ? 'uncertain' : 'error', data: denied ? null : this.state.data, error});
  }
  async load() {
    const epoch = ++this.epoch;
    this.publish({phase: 'loading', data: null, error: null});
    if (!this.current(epoch)) return;
    try {const raw = await this.request(this.path(), 'GET', undefined, epoch); if (this.current(epoch)) this.accept(raw);}
    catch (error) {if (this.current(epoch)) this.failure(error, false);}
  }
  async choose(resource: LyricRevision | null, expected: LyricsView) {
    const source = expected.source;
    if (this.state.phase !== 'ready' || this.state.data !== expected || !source || !expected.canShare || resource && (!expected.resources.includes(resource) || resource.scope !== 'library' || resource.deleted)) return;
    await this.write(this.path(true), {scope: 'library', sourceId: source.id, sourceVersion: source.version, sessionId: '', expectedSelectionRevision: expected.librarySelection?.revision ?? 0, resourceId: resource?.id ?? '', revision: resource?.revision ?? 0}, expected);
  }
  async upload(text: string, language: string, format: 'text' | 'lrc', expected: LyricsView) {
    const source = expected.source;
    if (this.state.phase !== 'ready' || this.state.data !== expected || !source || !expected.canShare || !text.trim() || new TextEncoder().encode(text).length > 262144 || !/^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$/.test(language)) return;
    await this.write(this.path(), {scope: 'library', sourceId: source.id, sourceVersion: source.version, sessionId: '', resourceId: '', expectedRevision: 0, language, format, text}, expected);
  }
  private async write(path: string, body: unknown, expected: LyricsView) {
    const epoch = ++this.epoch;
    this.publish({phase: 'saving', data: expected, error: null});
    // A synchronous subscriber can retire the editor during publication.
    if (!this.current(epoch) || this.state.phase !== 'saving' || this.state.data !== expected) return;
    try {
      await this.request(path, path === this.path(true) ? 'PUT' : 'POST', body, epoch);
      if (!this.current(epoch) || this.state.data !== expected) return;
      const raw = await this.request(this.path(), 'GET', undefined, epoch);
      if (this.current(epoch)) this.accept(raw, expected.source);
    } catch (error) {if (this.current(epoch)) this.failure(error, true);}
  }
  dispose() {this.disposed = true; this.epoch++; this.controller?.abort(); this.listeners.clear();}
}
