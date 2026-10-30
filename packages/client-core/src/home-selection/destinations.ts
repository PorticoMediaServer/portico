import {SavedService} from '../saved.ts';
import {PersonalSavedService} from '../personal-saved.ts';
import type {SelectionTarget} from '../selection-model/index.ts';
import type {HomeSelectionOptions, HomeSelectionPicker, HomeSelectionPickerRow} from './types.ts';
import {fetchHomeDestinationPicker} from './destination-picker.ts';
import {fetchHomeDestinationReceipt} from './destination-receipt.ts';
import type {Job} from '../bulk-jobs.ts';
import {runSelectionJobs, selectionExecutionError, selectionJobTasks, type HomeJobTask} from './execution.ts';

export type HomeDestinationOptions = Pick<HomeSelectionOptions, 'api' | 'events' | 'requestId'>;

const info = (error: unknown) => ({code: String((error as {code?: unknown})?.code ?? 'destination-unavailable'), retryable: true});
/** Each service is private to this dialog; paging a picker never changes Saved's visible route. */
export class HomeSelectionDestinations {
  private list: SavedService;
  private playlist: SavedService;
  private collections: PersonalSavedService;
  private collection: PersonalSavedService;
  private state: HomeSelectionPicker;
  private tasks: HomeJobTask[] = [];
  private jobHistory = new Map<string, Job>();
  private chosen?: string;
  private destinationFacts?: HomeSelectionPickerRow;
  private query = '';
  private cursor?: string;
  private history: (string | undefined)[] = [];
  private nextCursor = '';
  private readVersion = 0;
  private readController?: AbortController;
  private creating = false;
  private closed = false;
  private options: HomeDestinationOptions;
  private target: SelectionTarget;
  private signal: AbortSignal;
  private current: () => boolean;
  private publish: (value: HomeSelectionPicker) => void;
  constructor(options: HomeDestinationOptions, target: SelectionTarget, kind: 'playlist' | 'collection', signal: AbortSignal, current: () => boolean, publish: (value: HomeSelectionPicker) => void) {
    this.options = options; this.target = target; this.signal = signal; this.current = current; this.publish = publish;
    const {scope} = target.context;
    const savedScope = {serverId: scope.serverId, viewerId: JSON.stringify([scope.authority, scope.accountId, scope.profileId])};
    this.list = new SavedService({api: options.api, scope: savedScope, requestId: options.requestId});
    this.playlist = new SavedService({api: options.api, scope: savedScope, requestId: options.requestId});
    this.collections = new PersonalSavedService(options.api, savedScope, options.requestId);
    this.collection = new PersonalSavedService(options.api, savedScope, options.requestId);
    this.state = {kind, phase: 'loading', rows: [], jobs: [], receipt: null, query: '', next: false, previous: false, destination: null, error: null, results: []};
  }
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
      const identities = this.target.mode === 'explicit' ? this.target.items.map(item => item.item) : [];
      const itemIds = identities.length > 0 && identities.length <= 200 && identities.every(item => ['movie', 'episode', 'song', 'audiobook_file'].includes(item.kind)) ? identities.flatMap(item => 'id' in item ? [item.id] : []) : undefined;
      const page = await fetchHomeDestinationPicker(this.options.api, {kind: this.state.kind, query, ...(cursor ? {cursor} : {}), limit: 40, ...(itemIds ? {itemIds} : {})}, this.target.context.scope.serverId, this.target.context.scope.privacyFence, controller.signal);
      this.active(); if (version !== this.readVersion) return;
      this.cursor = cursor; this.history = history; this.nextCursor = page.nextCursor;
      this.update({rows: page.rows, next: !!page.nextCursor, previous: history.length > 0, phase: 'ready'});
    } catch (error) {if (this.current() && version === this.readVersion) this.update({phase: 'error', error: info(error)});}
    finally {this.signal.removeEventListener('abort', abort);}
  }
  search(query: string) {if (this.tasks.length || this.creating) throw selectionExecutionError('finish-destination-retry'); return this.load(query);}
  async page(previous = false) {
    this.active(); if (this.state.phase !== 'ready') throw selectionExecutionError('picker-busy');
    if (previous) {if (this.history.length) await this.load(this.query, this.history.at(-1), this.history.slice(0, -1));}
    else if (this.nextCursor) await this.load(this.query, this.nextCursor, [...this.history, this.cursor]);
  }
  async create(name: string) {
    this.active(); if (this.creating || this.state.phase !== 'ready') throw selectionExecutionError('picker-busy');
    this.creating = true; this.update({phase: 'adding', error: null});
    try {
      if (this.state.kind === 'playlist') {
        await this.list.select({view: 'playlists'}); this.active();
        await this.list.mutate({action: 'create', name}); this.active();
        const state = this.list.getSnapshot();
        if (state.mutationError || !state.lastReceipt) throw selectionExecutionError(state.mutationError?.code ?? 'destination-unconfirmed', true);
        this.chosen = state.lastReceipt.playlistId;
      } else {
        await this.collections.select({view: 'collections'}); this.active();
        if (!await this.collections.mutate({action: 'create', kind: 'collection', name})) throw selectionExecutionError(this.collections.getSnapshot().mutationError?.code ?? 'destination-unconfirmed', true);
        this.active(); this.chosen = this.collections.getSnapshot().result?.resourceId;
      }
      if (!this.chosen) throw selectionExecutionError('destination-unconfirmed');
      this.creating = false; await this.choose(this.chosen);
    } catch (error) {if (this.current()) this.update({phase: 'uncertain', error: info(error)});}
  }
  private async destinationRevision(id: string): Promise<number> {
    this.active();
    if (this.state.kind === 'playlist') {
      await this.playlist.select({view: 'playlist', playlistId: id}); this.active();
      const state = this.playlist.getSnapshot(), resource = state.playlist;
      if (state.phase !== 'ready' || !resource || !resource.actions.includes('add')) throw selectionExecutionError(state.error?.code ?? 'destination-not-permitted');
      // Saved Reading groups are opaque references, never ordinary media artwork.
      const artwork = (state.projection?.sections.flatMap(section => section.entries) ?? []).flatMap(entry => entry.kind === 'playlist_entry' ? entry.hidden ? [] : [entry.media] : entry.kind === 'playlist' || entry.kind === 'reading_group' ? [] : [entry]).slice(0, 4);
      this.retainDestination({id, name: resource.name, count: resource.entryCount, revision: resource.revision, canAdd: true, artwork});
      return resource.revision;
    }
    await this.collection.select({view: 'resource', resourceId: id}); this.active();
    const state = this.collection.getSnapshot(), resource = state.resource;
    if (state.error || !resource || !resource.actions.includes('entries')) throw selectionExecutionError(state.error?.code ?? 'destination-not-permitted');
    this.retainDestination({id, name: resource.name, count: resource.entryCount, revision: resource.revision, canAdd: true, artwork: state.entries.flatMap(entry => entry.media ? [entry.media] : []).slice(0, 4)});
    return resource.revision;
  }
  private retainDestination(facts: HomeSelectionPickerRow) {
    this.destinationFacts = facts;
    this.update({rows: this.state.rows.map(row => row.id === facts.id ? facts : row), ...(this.state.destination?.id === facts.id ? {destination: {kind: this.state.kind, ...facts}} : {})});
  }
  async choose(id: string) {
    this.active(); if (this.tasks.length || this.creating) throw selectionExecutionError('finish-destination-retry');
    if (this.state.phase !== 'ready' && this.state.phase !== 'adding') throw selectionExecutionError('picker-busy');
    if (this.state.rows.find(row => row.id === id)?.canAdd === false) throw selectionExecutionError('destination-not-permitted');
    const revision = await this.destinationRevision(id); this.active(); this.chosen = id;
    const kind = this.state.kind;
    this.tasks = await selectionJobTasks(this.target, kind === 'playlist' ? 'playlist-add' : 'collection-add', kind === 'playlist' ? {playlistId: id, expectedRevision: revision, placement: 'end'} : {collectionId: id, expectedRevision: revision}, this.options.requestId);
    if (this.tasks.length > 200) {this.tasks = []; throw selectionExecutionError('destination-jobset-limit');}
    this.active(); await this.run();
  }
  private async run() {
    const id = this.chosen; if (!id) throw selectionExecutionError('destination-required');
    if (!this.destinationFacts || this.destinationFacts.id !== id) throw selectionExecutionError('destination-facts-required');
    this.update({phase: 'adding', destination: {kind: this.state.kind, ...this.destinationFacts}, error: null});
    try {
      await runSelectionJobs(this.options.api, this.tasks, this.signal, this.current, () => {this.rememberJobs(); this.update({jobs: [...this.jobHistory.values()]});}, async task => {
        if (this.state.rows.find(row => row.id === id)?.canAdd === false) throw selectionExecutionError('destination-not-permitted');
        const revision = await this.destinationRevision(id); this.active();
        task.request = {...task.request, args: {...task.request.args, expectedRevision: revision}};
      }, this.options.events, false);
      this.active();
      const receipt = await this.receipt(); this.active();
      await this.destinationRevision(id); this.active();
      const results = receipt.failures.map(f => ({id: f.itemId, ok: false, code: f.code}));
      const allLeaves = this.target.mode === 'explicit' && this.target.items.every(pick => ['movie', 'episode', 'song', 'audiobook_file'].includes(pick.item.kind));
      const knownCount = receipt.complete && !receipt.failed && allLeaves && this.target.mode === 'explicit' ? this.target.items.length : undefined;
      this.update({phase: !receipt.complete ? 'error' : receipt.failed ? 'partial' : 'added', receipt, results, jobs: receipt.jobs, error: receipt.complete ? null : {code: 'destination-outcome-unresolved', retryable: false}, rows: this.state.rows.map(row => {if (row.id !== id) return row; const {alreadyIn: _old, ...facts} = row; return {...facts, ...(knownCount === undefined ? {} : {alreadyIn: knownCount})};})});
    } catch (error) {
      if (!this.current()) return;
      this.rememberJobs();
      let receipt = this.state.receipt;
      try {if (this.jobHistory.size) receipt = await this.receipt();} catch {this.active();}
      this.active();
      const confirmed = this.tasks.some(task => task.rejected || task.job?.state === 'failed' && task.job.errorCode);
      this.update({phase: confirmed ? 'error' : 'uncertain', receipt, jobs: [...this.jobHistory.values()], error: {...info(error), retryable: !receipt?.unresolvedJobIds.length}});
    }
  }
  private rememberJobs() {for (const task of this.tasks) if (task.job) this.jobHistory.set(task.job.jobId, task.job);}
  private async receipt() {
    this.rememberJobs();
    const receipt = await fetchHomeDestinationReceipt(this.options.api, {kind: this.state.kind, destinationId: this.chosen!, jobIds: [...this.jobHistory.keys()]}, this.target.context.scope.serverId, this.target.context.scope.privacyFence, this.signal);
    this.active(); for (const job of receipt.jobs) this.jobHistory.set(job.jobId, job); return receipt;
  }
  async retry() {
    this.active();
    if (this.creating) {
      try {
        if (this.state.kind === 'playlist') {await this.list.retryMutation(); this.active(); this.chosen = this.list.getSnapshot().lastReceipt?.playlistId;}
        else {if (!await this.collections.retry()) throw selectionExecutionError('destination-unconfirmed', true); this.active(); this.chosen = this.collections.getSnapshot().result?.resourceId;}
        if (!this.chosen) throw selectionExecutionError('destination-unconfirmed', true);
        this.creating = false; this.update({phase: 'ready'}); await this.choose(this.chosen);
      } catch (error) {if (this.current()) this.update({phase: 'uncertain', error: info(error)});}
      return;
    }
    if (this.state.phase === 'partial') {
      if (this.target.mode === 'query') throw selectionExecutionError('query-failures-require-new-selection');
      // Each retry uses only the next bounded real unresolved failure window.
      // The next summary includes all earlier jobs and supplies remaining rows.
      const missing = this.state.receipt?.failures.map(f => f.itemId) ?? [];
      if (!missing.length || !this.chosen) throw selectionExecutionError('destination-failures-incomplete');
      const prior = this.tasks.at(-1)!.request;
      this.tasks = [];
      for (let offset = 0; offset < missing.length; offset += 200) this.tasks.push({request: {...prior, operationId: (await this.options.requestId()).replaceAll('-', ''), selector: {items: {ids: missing.slice(offset, offset + 200)}}}, failures: [], observed: false});
    }
    for (const task of this.tasks) if (task.rejected && !task.job) {task.request = {...task.request, operationId: (await this.options.requestId()).replaceAll('-', '')}; task.attempted = false; task.rejected = false;}
    if (this.jobHistory.size + this.tasks.filter(task => !task.job).length > 200) throw selectionExecutionError('destination-jobset-limit');
    if (this.tasks.length) await this.run(); else await this.load();
  }
  getSnapshot() {return this.state;}
  dispose() {this.closed = true; this.readController?.abort(); this.list.dispose(); this.playlist.dispose(); this.collections.dispose(); this.collection.dispose();}
}
