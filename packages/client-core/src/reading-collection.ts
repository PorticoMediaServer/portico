import type {ViewerQueries} from './query/viewer-queries.ts';
import type {DetailApi, DetailScope} from './detail.ts';
import {receiptOf} from './home-selection/destinations.ts';
import type {HomeDestinationReceipt} from './home-selection/destination-receipt.ts';
import {addToDestination, createDestination, readDestinationPage, type ListDestinationApi} from './home-selection/list-destinations.ts';
import type {HomeSelectionPickerRow} from './home-selection/types.ts';

export type ReadingCollectionItem = Readonly<{id: string; libraryId: string; kind: 'ebook' | 'comic'; title: string}>;
export type ReadingCollectionOptions = Readonly<{queries: ViewerQueries; api: DetailApi; scope: DetailScope; requestId: () => Promise<string>;
  signal: AbortSignal; isCurrent: () => boolean; canCreate: boolean}>;
/** The collection the titles went into, as the sheets name it. */
export type ReadingCollectionDestination = Readonly<{id: string; name: string; entryCount: number}>;
export type ReadingCollectionSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'adding' | 'result' | 'error' | 'uncertain';
  rows: readonly HomeSelectionPickerRow[]; query: string; next: boolean; previous: boolean;
  destination?: ReadingCollectionDestination; receipt?: HomeDestinationReceipt; error?: unknown;
}>;
const unavailable = () => Object.assign(new Error('reading_collection_unavailable'), {code: 'unavailable'});

/** Books or comics chosen in a Read library, added to one of the profile's collections or a new one, on the rebuilt
 * server's lists (`list-destinations.ts`, the same reads and writes as Home's add-to sheets): pages of 40 collections
 * with a search by name, then the titles added in parts of 500. An addition whose answer was lost is sent again with
 * its own key, so nothing is added twice. */
export class ReadingCollection {
  readonly items: readonly ReadingCollectionItem[];
  private readonly listeners = new Set<() => void>();
  private state: ReadingCollectionSnapshot = Object.freeze({phase: 'idle', rows: [], query: '', next: false, previous: false});
  private closed = false;
  private running = false;
  private cursor?: string;
  private nextCursor = '';
  private history: (string | undefined)[] = [];
  private chosen?: HomeSelectionPickerRow;
  private creating?: string;
  private key?: string;
  private failedRead?: Readonly<{query: string; cursor?: string; history: (string | undefined)[]}>;
  private readonly api: ListDestinationApi;
  constructor(private readonly options: ReadingCollectionOptions, input: readonly ReadingCollectionItem[]) {
    if (!input.length || input.length > 200 || input.some(item => !item.id || !item.libraryId || !['ebook', 'comic'].includes(item.kind))
      || new Set(input.map(item => item.id)).size !== input.length) throw unavailable();
    this.items = Object.freeze(input.map(item => Object.freeze({...item})));
    this.check();
    const raw = (options.api as {requestRaw?: ListDestinationApi['requestRaw']}).requestRaw;
    this.api = {
      request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
        this.check();
        const value = await options.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, options.signal]) : options.signal);
        this.check(); return value;
      },
      ...(raw ? {requestRaw: async (path: string, method: 'POST', request?: Parameters<NonNullable<ListDestinationApi['requestRaw']>>[2]) => {
        this.check(); const value = await raw.call(options.api, path, method, request); this.check(); return value;
      }} : {}),
    } as ListDestinationApi;
    options.signal.addEventListener('abort', this.dispose, {once: true});
  }
  private check() {if (this.closed || this.options.signal.aborted || !this.options.isCurrent()) throw Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});}
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.state;
  private publish(value: ReadingCollectionSnapshot) {this.check(); this.state = Object.freeze(value); for (const listener of [...this.listeners]) listener();}
  private async run(phase: 'loading' | 'adding', work: () => Promise<void>) {
    this.check(); if (this.running) throw unavailable();
    this.running = true; this.publish({...this.state, phase, error: undefined});
    try {await work(); this.check();}
    catch (error) {
      // A lost answer to an addition is unknown, not refused: Try again sends it again with the same key.
      if (!this.closed && !this.options.signal.aborted && this.options.isCurrent()) this.publish({...this.state, phase: phase === 'adding' ? 'uncertain' : 'error', error});
      throw error;
    } finally {this.running = false;}
  }
  private async keyed() {if (!this.key) {this.key = await this.options.requestId(); this.check();} return this.key;}
  private async read(query: string, cursor?: string, history: (string | undefined)[] = []) {
    this.failedRead = {query, cursor, history};
    const page = await readDestinationPage(this.api, {kind: 'collection', query, limit: 40, ...(cursor ? {cursor} : {})}, this.options.signal);
    this.check(); this.failedRead = undefined; this.cursor = cursor; this.history = history; this.nextCursor = page.nextCursor;
    this.publish({phase: 'ready', rows: page.rows, query, next: !!page.nextCursor && history.length < 64, previous: !!history.length});
  }
  load = () => this.run('loading', async () => {if (this.chosen || this.creating) throw unavailable(); await this.read('');});
  search = (query: string) => this.run('loading', async () => {if (this.chosen || this.creating) throw unavailable(); await this.read(query);});
  page = (rendered: ReadingCollectionSnapshot, previous = false) => {
    if (this.state !== rendered || rendered.phase !== 'ready' || this.chosen || this.creating) return Promise.reject(unavailable());
    const cursor = previous ? this.history.at(-1) : this.nextCursor;
    if (previous ? !this.history.length : !cursor) return Promise.reject(unavailable());
    const history = previous ? this.history.slice(0, -1) : [...this.history, this.cursor];
    return this.run('loading', () => this.read(rendered.query, cursor, history));
  };
  choose = (rendered: ReadingCollectionSnapshot, id: string) => {
    if (this.state !== rendered || rendered.phase !== 'ready' || this.chosen || this.creating) return Promise.reject(unavailable());
    const row = rendered.rows.find(row => row.id === id && row.canAdd);
    if (!row) return Promise.reject(unavailable());
    return this.run('adding', async () => {this.chosen = row; await this.append();});
  };
  private async append() {
    const row = this.chosen!, added = await addToDestination(this.api, row.id, this.items.map(item => item.id), await this.keyed(), this.options.signal);
    this.check();
    this.publish({...this.state, phase: 'result', destination: {id: row.id, name: row.name, entryCount: added.count}, receipt: receiptOf('collection', row.id, added.added, added.already)});
  }
  create = (name: string) => this.run('adding', async () => {
    if (!this.options.canCreate || this.chosen || this.creating || !name.trim()) throw unavailable();
    this.creating = name.trim(); await this.made();
  });
  private async made() {
    const made = await createDestination(this.api, 'collection', this.creating!, this.items.map(item => item.id), await this.keyed(), this.options.signal);
    this.check(); this.chosen = made.row; this.creating = undefined;
    this.publish({...this.state, phase: 'result', destination: {id: made.row.id, name: made.row.name, entryCount: made.row.count}, receipt: receiptOf('collection', made.row.id, made.added, 0)});
  }
  retry = () => this.run(this.chosen || this.creating ? 'adding' : 'loading', async () => {
    if (this.creating) await this.made();
    else if (this.chosen) await this.append();
    else {const read = this.failedRead ?? {query: this.state.query, cursor: this.cursor, history: this.history}; await this.read(read.query, read.cursor, read.history);}
  });
  dispose = () => {if (this.closed) return; this.closed = true; this.options.signal.removeEventListener('abort', this.dispose); this.listeners.clear();};
}
