import {DetailService, type DetailApi, type DetailScope} from './detail.ts';
import {PersonalSavedService, type PersonalResource} from './personal-saved.ts';
import {runSelectionJobs, type HomeJobTask} from './home-selection/execution.ts';
import {fetchHomeDestinationReceipt, type HomeDestinationReceipt} from './home-selection/destination-receipt.ts';
import type {Job} from './bulk-jobs.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {parseHomeDestinationPicker} from './home-selection/destination-picker.ts';
import type {HomeSelectionPickerRow} from './home-selection/types.ts';

export type ReadingCollectionItem = Readonly<{id: string; libraryId: string; kind: 'ebook' | 'comic'; title: string}>;
export type ReadingCollectionOptions = Readonly<{api: DetailApi; scope: DetailScope; requestId: () => Promise<string>;
  signal: AbortSignal; isCurrent: () => boolean; canCreate: boolean}>;
export type ReadingCollectionSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'adding' | 'result' | 'error' | 'uncertain';
  rows: readonly HomeSelectionPickerRow[]; query: string; next: boolean; previous: boolean;
  destination?: PersonalResource; receipt?: HomeDestinationReceipt; error?: unknown;
}>;
const unavailable = () => Object.assign(new Error('reading_collection_unavailable'), {code: 'unavailable'});

/** An explicit, bounded Reading selection. No query conversion or Home selection owner.
 * Opening and each write re-read at most 200 Detail documents, sequentially, for the
 * exact publications' live grants. Picker pages are at most 40 rows. Membership uses
 * ONE existing collection-add job per batch, retaining its CAS, operation and receipt. */
export class ReadingCollection {
  readonly items: readonly ReadingCollectionItem[];
  private readonly detail: DetailService;
  private readonly saved: PersonalSavedService;
  private readonly listeners = new Set<() => void>();
  private state: ReadingCollectionSnapshot = Object.freeze({phase: 'idle', rows: [], query: '', next: false, previous: false});
  private closed = false;
  private running = false;
  private fence?: string;
  private detailFence?: string;
  private cursor?: string;
  private history: (string | undefined)[] = [];
  private chosen?: HomeSelectionPickerRow;
  private pendingCreate = false;
  private createdId?: string;
  private remaining: readonly string[];
  private tasks: HomeJobTask[] = [];
  private readonly jobs = new Map<string, Job>();
  constructor(private readonly options: ReadingCollectionOptions, input: readonly ReadingCollectionItem[]) {
    if (!input.length || input.length > 200 || input.some(item => !item.id || !item.libraryId || !['ebook', 'comic'].includes(item.kind))
      || new Set(input.map(item => item.id)).size !== input.length) throw unavailable();
    this.items = Object.freeze(input.map(item => Object.freeze({...item})));
    this.remaining = Object.freeze(this.items.map(item => item.id));
    this.check();
    const api: DetailApi = {acknowledgeUserMutation: async intentId => {this.check(); await options.api.acknowledgeUserMutation?.(intentId); this.check();}, request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
      this.check();
      const value = await options.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, options.signal]) : options.signal);
      this.check(); return value;
    }};
    const requestId = async () => {this.check(); const id = await options.requestId(); this.check(); return id;};
    this.detail = new DetailService({api, scope: options.scope, requestId});
    this.saved = new PersonalSavedService(api, options.scope, requestId);
    this.api = api;
    options.signal.addEventListener('abort', this.dispose, {once: true});
  }
  private readonly api: DetailApi;
  private check() {if (this.closed || this.options.signal.aborted || !this.options.isCurrent()) throw Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});}
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.state;
  private publish(value: ReadingCollectionSnapshot) {this.check(); this.state = Object.freeze(value); for (const listener of [...this.listeners]) listener();}
  private async run(phase: 'loading' | 'adding', work: () => Promise<void>) {
    this.check(); if (this.running) throw unavailable();
    this.running = true; this.publish({...this.state, phase, error: undefined});
    try {await work(); this.check();}
    catch (error) {
      if (!this.closed && !this.options.signal.aborted && this.options.isCurrent()) this.publish({...this.state,
        phase: this.saved.getSnapshot().retryPending || this.tasks.some(task => task.attempted && !task.rejected) ? 'uncertain' : 'error', error});
      throw error;
    } finally {this.running = false;}
  }
  private async grants() {
    for (const item of this.items) {
      this.check(); await this.detail.select({libraryId: item.libraryId, itemId: item.id}, {related: 'none'}); this.check();
      const state = this.detail.getSnapshot(), data = state.data;
      if (state.phase !== 'ready' || !data || data.scope.libraryId !== item.libraryId || data.item.id !== item.id || data.item.kind !== item.kind
        || !data.actions.some(action => action.id === 'add_to_collection' && action.enabled)) throw unavailable();
      if (this.detailFence && this.detailFence !== data.scope.viewerFence) throw unavailable();
      this.detailFence = data.scope.viewerFence;
    }
  }
  private async read(query: string, cursor?: string, history: (string | undefined)[] = []) {
    this.failedRead = {query, cursor, history};
    const request = {kind: 'collection' as const, query, limit: 40, ...(cursor ? {cursor} : {}), itemIds: this.items.map(item => item.id)};
    const raw = await this.api.request<unknown>(routePath('post_home_destination_picker'), 'POST', request, this.options.signal);
    this.check();
    if (!raw || typeof raw !== 'object' || !('viewerFence' in raw) || typeof raw.viewerFence !== 'string' || !raw.viewerFence) throw unavailable();
    if (this.fence && this.fence !== raw.viewerFence) throw unavailable();
    const page = parseHomeDestinationPicker(raw, request, this.options.scope.serverId, raw.viewerFence);
    this.fence = raw.viewerFence; this.failedRead = undefined;
    this.check(); this.cursor = cursor; this.history = history;
    this.nextCursor = page.nextCursor;
    this.publish({phase: 'ready', rows: page.rows, query, next: !!page.nextCursor && history.length < 64, previous: !!history.length});
  }
  private nextCursor = '';
  private failedRead?: Readonly<{query: string; cursor?: string; history: (string | undefined)[]}>;
  load = () => this.run('loading', async () => {if (this.chosen || this.pendingCreate) throw unavailable(); await this.grants(); await this.read('');});
  search = (query: string) => this.run('loading', async () => {if (this.chosen || this.pendingCreate) throw unavailable(); await this.grants(); await this.read(query);});
  page = (rendered: ReadingCollectionSnapshot, previous = false) => {
    if (this.state !== rendered || rendered.phase !== 'ready' || this.chosen || this.pendingCreate) return Promise.reject(unavailable());
    const cursor = previous ? this.history.at(-1) : this.nextCursor;
    if (previous ? !this.history.length : !cursor) return Promise.reject(unavailable());
    const history = previous ? this.history.slice(0, -1) : [...this.history, this.cursor];
    return this.run('loading', async () => {await this.grants(); await this.read(rendered.query, cursor, history);});
  };
  choose = (rendered: ReadingCollectionSnapshot, id: string) => {
    if (this.state !== rendered || rendered.phase !== 'ready' || this.chosen || this.pendingCreate) return Promise.reject(unavailable());
    const row = rendered.rows.find(row => row.id === id && row.canAdd);
    if (!row) return Promise.reject(unavailable());
    return this.run('adding', async () => {
      await this.saved.select({view: 'resource', resourceId: id}); this.check();
      const resource = this.resource(id);
      if (resource.revision !== row.revision) throw unavailable();
      this.chosen = row; await this.append();
    });
  };
  private resource(id: string) {
    const state = this.saved.getSnapshot(), resource = state.resource;
    if (state.loading || state.error || !resource || resource.id !== id || resource.kind !== 'collection'
      || resource.viewerFence !== this.fence || !resource.actions.includes('entries')) throw unavailable();
    return resource;
  }
  private async append(retry = false) {
    if (!this.chosen || !this.remaining.length) throw unavailable();
    await this.grants();
    const destination = this.resource(this.chosen.id);
    if (!retry) {
      if (this.jobs.size >= 200) throw unavailable();
      const operationId = (await this.options.requestId()).replaceAll('-', ''); this.check();
      this.tasks = [{request: {operationId, command: 'collection-add', selector: {items: {ids: this.remaining}},
        args: {collectionId: destination.id, expectedRevision: destination.revision}}, failures: [], observed: false}];
    }
    await runSelectionJobs(this.api, this.tasks, this.options.signal, this.options.isCurrent, () => {}, undefined, undefined, false);
    this.check();
    for (const task of this.tasks) if (task.job) this.jobs.set(task.job.jobId, task.job);
    const receipt = await fetchHomeDestinationReceipt(this.api, {kind: 'collection', destinationId: destination.id, jobIds: [...this.jobs.keys()]},
      this.options.scope.serverId, this.fence!, this.options.signal);
    this.check();
    if (receipt.added + receipt.alreadyPresent + receipt.failed + receipt.unknown !== this.items.length
      || receipt.failures.some(failure => !this.items.some(item => item.id === failure.itemId))) throw unavailable();
    if (receipt.complete) this.remaining = Object.freeze(receipt.failures.map(row => row.itemId));
    this.publish({...this.state, phase: receipt.complete ? 'result' : 'uncertain', destination, receipt,
      ...(!receipt.complete ? {error: unavailable()} : {})});
  }
  create = (name: string) => this.run('adding', async () => {
    if (!this.options.canCreate || this.chosen || this.pendingCreate || !name.trim()) throw unavailable();
    await this.grants();
    await this.saved.select({view: 'collections'}); this.check();
    this.pendingCreate = true;
    await this.saved.mutate({action: 'create', kind: 'collection', name: name.trim()}); this.check();
    await this.created();
  });
  private async created() {
    const state = this.saved.getSnapshot(), id = this.createdId ?? state.result?.resourceId;
    if (state.retryPending || state.mutationError || !id) throw state.mutationError ?? unavailable();
    this.createdId = id;
    await this.saved.select({view: 'resource', resourceId: id}); this.check();
    const resource = this.resource(id);
    this.chosen = {id, name: resource.name, revision: resource.revision, count: resource.entryCount, artwork: [], canAdd: true};
    this.pendingCreate = false; await this.append();
  }
  retry = () => this.run(this.chosen || this.pendingCreate ? 'adding' : 'loading', async () => {
    if (this.pendingCreate) {await this.grants(); if (!this.createdId) {await this.saved.retry(); this.check();} await this.created();}
    else if (this.chosen) {
      if (this.tasks.some(task => task.rejected)) {
        // An explicit retry of a definitely refused command may capture a new
        // CAS for the same destination and remaining IDs. Ambiguous attempts
        // below retain their byte-identical original body.
        await this.saved.select({view: 'resource', resourceId: this.chosen.id}); this.check();
        await this.append(); return;
      }
      if (this.state.receipt?.complete) {
        await this.saved.select({view: 'resource', resourceId: this.chosen.id}); this.check();
        await this.append();
      } else await this.append(true);
    }
    else {await this.grants(); const read = this.failedRead ?? {query: this.state.query, cursor: this.cursor, history: this.history}; await this.read(read.query, read.cursor, read.history);}
  });
  dispose = () => {if (this.closed) return; this.closed = true; this.options.signal.removeEventListener('abort', this.dispose);
    this.detail.dispose(); this.saved.dispose(); this.listeners.clear();};
}
