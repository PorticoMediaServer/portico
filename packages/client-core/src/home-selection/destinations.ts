import type {SelectionTarget} from '../selection-model/index.ts';
import type {HomeSelectionOptions, HomeSelectionPicker, HomeSelectionPickerRow} from './types.ts';
import type {HomeDestinationReceipt} from './destination-receipt.ts';
import {selectionExecutionError} from './execution.ts';
import {addToDestination, createDestination, readDestination, readDestinationPage, selectionItemIds, type ListDestinationApi} from './list-destinations.ts';

export type HomeDestinationOptions = Pick<HomeSelectionOptions, 'api' | 'events' | 'requestId'>;

const info = (error: unknown) => ({code: String((error as {code?: unknown})?.code ?? 'destination-unavailable'), retryable: (error as {code?: unknown})?.code !== 'too-many-to-add'});
/** What an addition did, in the shape the add-to sheets draw: the titles that went in and those already there. */
function receiptOf(kind: 'playlist' | 'collection', destinationId: string, added: number, alreadyPresent: number): HomeDestinationReceipt {
  return Object.freeze({kind, destinationId, jobIds: Object.freeze([]), jobs: Object.freeze([]), added, alreadyPresent, failed: 0, unknown: 0, complete: true, pending: false,
    unresolvedJobIds: Object.freeze([]), failures: Object.freeze([]), nextCursor: '', failureEntries: Object.freeze([])});
}

/**
 * One add-to-playlist or add-to-collection sheet for a selection, on the rebuilt server (`list-destinations.ts`): the
 * profile's lists to choose from, a search by name and pages of 40, the titles added in parts of 500, or a new list made
 * with them in it. Each sheet is private; paging it never changes Saved's visible route.
 */
export class HomeSelectionDestinations {
  private state: HomeSelectionPicker;
  private chosen?: string;
  /** The titles this sheet adds, read once (a whole view is resolved then) and kept for a retry. */
  private ids?: readonly string[];
  /** The operation an addition or a new list is sent under; a retry reuses it, so nothing goes in twice. */
  private operation?: Readonly<{key: string; destination: string | 'new'; name?: string}>;
  private query = '';
  private cursor?: string;
  private history: (string | undefined)[] = [];
  private nextCursor = '';
  private readVersion = 0;
  private readController?: AbortController;
  private closed = false;
  private options: HomeDestinationOptions;
  private target: SelectionTarget;
  private signal: AbortSignal;
  private current: () => boolean;
  private publish: (value: HomeSelectionPicker) => void;
  constructor(options: HomeDestinationOptions, target: SelectionTarget, kind: 'playlist' | 'collection', signal: AbortSignal, current: () => boolean, publish: (value: HomeSelectionPicker) => void) {
    this.options = options; this.target = target; this.signal = signal; this.current = current; this.publish = publish;
    this.state = {kind, phase: 'loading', rows: [], jobs: [], receipt: null, query: '', next: false, previous: false, destination: null, error: null, results: []};
  }
  private get api(): ListDestinationApi {return this.options.api as ListDestinationApi;}
  private active() {
    if (this.closed || this.signal.aborted || !this.current()) throw selectionExecutionError('cancelled');
    if (this.state.kind === 'playlist' && this.target.mode === 'query' && this.target.summary.unavailableActions?.playlist) throw selectionExecutionError('query-destination-unavailable');
  }
  private update(patch: Partial<HomeSelectionPicker>) {this.active(); this.state = Object.freeze({...this.state, ...patch}); this.publish(this.state);}
  async load(query = this.query, cursor?: string, history: (string | undefined)[] = []) {
    this.active();
    this.query = query; const version = ++this.readVersion;
    this.readController?.abort(); const controller = new AbortController(); this.readController = controller;
    const abort = () => controller.abort(); this.signal.addEventListener('abort', abort, {once: true});
    this.update({phase: 'loading', error: null, query});
    try {
      const page = await readDestinationPage(this.api, {kind: this.state.kind, query, ...(cursor ? {cursor} : {}), limit: 40}, controller.signal);
      this.active(); if (version !== this.readVersion) return;
      this.cursor = cursor; this.history = history; this.nextCursor = page.nextCursor;
      this.update({rows: page.rows, next: !!page.nextCursor, previous: history.length > 0, phase: 'ready'});
    } catch (error) {if (this.current() && version === this.readVersion) this.update({phase: 'error', error: info(error)});}
    finally {this.signal.removeEventListener('abort', abort);}
  }
  search(query: string) {if (this.operation) throw selectionExecutionError('finish-destination-retry'); return this.load(query);}
  async page(previous = false) {
    this.active(); if (this.state.phase !== 'ready') throw selectionExecutionError('picker-busy');
    if (previous) {if (this.history.length) await this.load(this.query, this.history.at(-1), this.history.slice(0, -1));}
    else if (this.nextCursor) await this.load(this.query, this.nextCursor, [...this.history, this.cursor]);
  }
  /** The titles to add, read once per sheet. A whole view of more than one addition takes is refused here. */
  private async titles(): Promise<readonly string[]> {
    if (!this.ids) {this.ids = await selectionItemIds(this.api, this.target, this.signal); this.active();}
    if (!this.ids.length) throw selectionExecutionError('destination-required');
    return this.ids;
  }
  private async key(destination: string | 'new', name?: string): Promise<string> {
    if (this.operation?.destination !== destination || this.operation.name !== name) {
      this.operation = Object.freeze({key: (await this.options.requestId()).replaceAll('-', ''), destination, ...(name === undefined ? {} : {name})});
      this.active();
    }
    return this.operation.key;
  }
  /** A new list with the selection in it. */
  async create(name: string) {
    this.active(); if (this.state.phase !== 'ready' && !(this.operation?.destination === 'new' && this.operation.name === name)) throw selectionExecutionError('picker-busy');
    this.update({phase: 'adding', error: null});
    try {
      const ids = await this.titles(), key = await this.key('new', name);
      const made = await createDestination(this.api, this.state.kind, name, ids, key, this.signal); this.active();
      this.finish(made.row, made.added, ids.length - made.added);
    } catch (error) {if (this.current()) this.update({phase: (error as {code?: unknown})?.code === 'too-many-to-add' ? 'error' : 'uncertain', error: info(error)});}
  }
  /** Adds the selection to the list chosen from the rows. */
  async choose(id: string) {
    this.active(); if (this.state.phase !== 'ready' && this.operation?.destination !== id) throw selectionExecutionError('picker-busy');
    const offered = this.state.rows.find(row => row.id === id);
    if (offered?.canAdd === false) throw selectionExecutionError('destination-not-permitted');
    this.chosen = id;
    this.update({phase: 'adding', destination: offered ? {kind: this.state.kind, ...offered} : this.state.destination, error: null});
    try {
      const ids = await this.titles();
      const row = await readDestination(this.api, this.state.kind, id, this.signal); this.active();
      if (!row.canAdd) throw selectionExecutionError('destination-not-permitted');
      this.update({destination: {kind: this.state.kind, ...row}});
      const key = await this.key(id), done = await addToDestination(this.api, id, ids, key, this.signal); this.active();
      this.finish({...row, revision: done.revision, count: done.count}, done.added, done.already);
    } catch (error) {
      if (!this.current()) return;
      const code = (error as {code?: unknown})?.code;
      this.update({phase: code === 'too-many-to-add' || code === 'destination-not-permitted' ? 'error' : 'uncertain', error: info(error)});
    }
  }
  private finish(row: HomeSelectionPickerRow, added: number, already: number) {
    this.chosen = row.id; this.operation = undefined;
    const known = this.target.mode === 'explicit' && this.target.items.every(pick => ['movie', 'episode', 'song', 'audiobook_file'].includes(pick.item.kind)) ? this.target.items.length : undefined;
    this.update({phase: 'added', receipt: receiptOf(this.state.kind, row.id, added, already), results: [], destination: {kind: this.state.kind, ...row},
      rows: this.state.rows.some(each => each.id === row.id) ? this.state.rows.map(each => each.id === row.id ? {...row, ...(known === undefined ? {} : {alreadyIn: known})} : each) : [{...row, ...(known === undefined ? {} : {alreadyIn: known})}, ...this.state.rows]});
  }
  /** Try again: the addition or the new list that did not answer, under its own operation, or the rows. */
  async retry() {
    this.active();
    const operation = this.operation;
    if (operation?.destination === 'new' && operation.name !== undefined) {this.update({phase: 'ready'}); return this.create(operation.name);}
    if (operation) {this.update({phase: 'ready'}); return this.choose(operation.destination);}
    if (this.state.phase === 'added' && this.chosen) return;
    return this.load();
  }
  getSnapshot() {return this.state;}
  dispose() {this.closed = true; this.readController?.abort();}
}
