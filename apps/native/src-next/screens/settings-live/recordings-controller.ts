import {DVRClient, mutableRecording, validRecordingOptions, type DeletePreview, type DVRPage, type DVRSummary, type DVRView, type Occurrence, type Recording, type RecordingDetail, type RecordingDraft, type RecordingGroups, type RecordingOptions, type RecordingRule, type RecordingStorage, type RuleConfig, type RulePreview, type StoragePolicy} from '../../../../../packages/client-core/src/dvr';
import type {ChannelApi} from '../../../../../packages/client-core/src/channel-guide';
import type {DVRCommandLifetime} from '../../../../../packages/client-core/src/dvr';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from './lifetime';
import {DashboardCursorWindow, type DashboardCursorSnapshot} from './dashboard-cursor-window';

export type NativeRecordingQuery = Readonly<{view: DVRView; cursor?: string; sort?: 'recent' | 'title' | 'oldest'; group?: string}>;
type ViewerRow = {kind: 'recording'; value: Recording} | {kind: 'rule'; value: RecordingRule} | {kind: 'group'; value: RecordingGroups['groups'][number]};
type ViewerPage = DVRPage | RecordingGroups;
export type NativeRecordingsSnapshot = Readonly<{
  query: NativeRecordingQuery; page?: DVRPage; groups?: RecordingGroups; storage?: RecordingStorage; summary?: DVRSummary;
  viewerWindow?: DashboardCursorSnapshot<ViewerRow, ViewerPage>;
  loading: boolean; busy: boolean; error?: unknown; accessDenied: boolean;
}>;
export type NativeRecordingService = Pick<DVRClient, 'list' | 'groups' | 'summary' | 'storage' | 'detail' | 'deletePreview' | 'preview' | 'newRuleID' | 'schedule' | 'update' | 'cancel' | 'keep' | 'delete' | 'saveRule' | 'deleteRule' | 'saveStorage' | 'onAccessChanged' | 'dispose'>;

/** One genuine DVR client per viewer lifetime. Never activate/reset its unknown-outcome receipts
 * on a view change. Cursor pages stay pages; no fabricated random offsets or whole-DVR scan. */
export class NativeRecordingsController extends NativeScreenStore<NativeRecordingsSnapshot> {
  readonly lifetime: NativeScreenLifetime;
  private service: NativeRecordingService;
  private disposed = false;
  private epoch = 0;
  private pending?: Readonly<{query: NativeRecordingQuery; epoch: number; opening?: object}>;
  private worker?: Promise<void>;
  private facts?: Promise<void>;
  private viewerWindow?: DashboardCursorWindow<ViewerRow, ViewerPage>;
  private viewerOpening?: object;
  private detailScope?: {dispose: () => void};
  constructor(lifetime: NativeScreenLifetime, service: NativeRecordingService) {
    super({query: {view: 'upcoming'}, loading: false, busy: false, accessDenied: false});
    this.lifetime = lifetime; this.service = service;
    service.onAccessChanged(() => {
      this.detailScope?.dispose(); this.pending = undefined; this.epoch++; this.viewerWindow?.dispose(); this.viewerWindow = undefined;
      if (this.current()) this.publish({query: this.getSnapshot().query, loading: false, busy: false, accessDenied: true, error: {status: 403}});
    });
  }
  private current = () => !this.disposed && this.lifetime.current();
  private assert() {this.lifetime.assert(); if (this.disposed) throw screenScopeEnded();}
  load(query: NativeRecordingQuery = this.getSnapshot().query, opening?: object): Promise<void> {
    const predecessor = this.viewerWindow, priorEpoch = this.epoch;
    if (opening && !this.viewerOpeningCurrent(opening)) return Promise.resolve();
    this.assert();
    if (this.epoch !== priorEpoch || this.viewerWindow !== predecessor || this.getSnapshot().accessDenied || opening && !this.viewerOpeningCurrent(opening) || this.epoch !== priorEpoch || this.viewerWindow !== predecessor) return Promise.resolve();
    this.viewerWindow = undefined; predecessor?.dispose();
    if (this.epoch !== priorEpoch || this.viewerWindow || opening && !this.viewerOpeningCurrent(opening) || this.epoch !== priorEpoch || this.viewerWindow) return Promise.resolve();
    const original = Object.freeze({...query});
    const epoch = ++this.epoch, previous = this.getSnapshot();
    const same = JSON.stringify(previous.query) === JSON.stringify(original);
    const pending = {query: original, epoch, opening}; this.pending = pending;
    this.publish({...previous, query: original, page: same ? previous.page : undefined, groups: same ? previous.groups : undefined, viewerWindow: undefined, loading: true, error: undefined});
    if (this.pending !== pending || epoch !== this.epoch || opening && !this.viewerOpeningCurrent(opening) || this.pending !== pending || epoch !== this.epoch) {
      if (this.pending === pending) {this.pending = undefined; if (epoch === this.epoch && this.getSnapshot().loading) this.publish({...this.getSnapshot(), loading: false});}
      return this.worker ?? Promise.resolve();
    }
    // Moving focus quickly across the five chips replaces pending work, instead of queuing
    // private reads for every intermediate selection. At most one page read is in flight.
    if (!this.worker) {
      let resolve!: () => void, reject!: (error: unknown) => void;
      const worker = new Promise<void>((yes, no) => {resolve = yes; reject = no;});
      this.worker = worker;
      void this.drain().then(() => {if (this.worker === worker) this.worker = undefined; resolve();}, error => {if (this.worker === worker) this.worker = undefined; reject(error);});
    }
    return this.worker;
  }
  private async drain(): Promise<void> {
    while (this.pending && this.current()) {
      const {query, epoch, opening} = this.pending; this.pending = undefined;
      const admitted = () => this.current() && epoch === this.epoch && !this.getSnapshot().accessDenied && (!opening || this.viewerOpeningCurrent(opening)) && epoch === this.epoch && (!opening || this.viewerOpening === opening);
      if (!admitted()) continue;
      try {
        const result = query.view === 'storage' ? {storage: await this.service.storage()}
          : query.view === 'recorded' && !query.group ? {groups: await this.service.groups(query.sort ?? 'recent', query.cursor)}
          : {page: await this.service.list(query.view, query.cursor, query.sort ?? 'recent', query.group)};
        if (admitted()) {this.publish({...this.getSnapshot(), ...result, loading: false, error: undefined}); if (admitted() && ('page' in result && result.page || 'groups' in result && result.groups)) this.installViewerWindow(query, epoch, ('page' in result ? result.page : result.groups)!);}
      } catch (error) {if (admitted()) this.publish({...this.getSnapshot(), loading: false, error});}
    }
  }
  private installViewerWindow(query: NativeRecordingQuery, epoch: number, first: ViewerPage): void {
    if (query.view === 'storage') return;
    const original = query, revision = first.revision;
    const current = () => this.current() && epoch === this.epoch && !this.getSnapshot().accessDenied;
    const rows = (page: ViewerPage): ViewerRow[] => 'groups' in page ? page.groups.map(value => ({kind: 'group', value})) : original.view === 'rules' ? page.rules.map(value => ({kind: 'rule', value})) : page.recordings.map(value => ({kind: 'recording', value}));
    const key = (row: ViewerRow): string => row.kind === 'group' ? row.value.key : row.value.id;
    const window = new DashboardCursorWindow<ViewerRow, ViewerPage>(async (cursor, signal) => {
      if (!current() || signal.aborted) throw screenScopeEnded();
      const page = original.view === 'recorded' && !original.group ? await this.service.groups(original.sort ?? 'recent', cursor) : await this.service.list(original.view as Exclude<DVRView, 'storage'>, cursor, original.sort ?? 'recent', original.group);
      if (!current() || signal.aborted) throw screenScopeEnded();
      if (page.revision !== revision) throw new Error('dashboard_refresh_required');
      return {items: rows(page), nextCursor: page.nextCursor, metadata: page};
    }, key, current, value => {if (current() && this.viewerWindow === window) this.publish({...this.getSnapshot(), viewerWindow: value});});
    this.viewerWindow = window;
    window.seed(original.cursor ?? '', {items: rows(first), nextCursor: first.nextCursor, metadata: first});
  }
  beginViewerOpening = (opening: object): void => {const previous = this.viewerOpening; this.viewerOpening = opening; if (previous && previous !== opening) this.viewerWindow?.cancelPending();};
  endViewerOpening = (opening: object): void => {if (this.viewerOpening !== opening) return; const window = this.viewerWindow; this.viewerOpening = undefined; window?.cancelPending(); if (!this.viewerOpening && this.viewerWindow === window) window?.focus(undefined);};
  viewerOpeningCurrent = (opening: object): boolean => this.current() && this.viewerOpening === opening && !this.getSnapshot().accessDenied;
  private mayPage(opening?: object): boolean {return opening ? this.viewerOpeningCurrent(opening) : !this.viewerOpening && this.current();}
  nextViewerPage = (expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>, opening?: object): Promise<void> => this.mayPage(opening) ? this.viewerWindow?.next(expected, opening ? () => this.viewerOpeningCurrent(opening) : undefined) ?? Promise.resolve() : Promise.resolve();
  previousViewerPage = (expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>, opening?: object): Promise<void> => this.mayPage(opening) ? this.viewerWindow?.previous(expected, opening ? () => this.viewerOpeningCurrent(opening) : undefined) ?? Promise.resolve() : Promise.resolve();
  retryViewerPage = (expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>, opening?: object): Promise<void> => this.mayPage(opening) ? this.viewerWindow?.retry(expected, opening ? () => this.viewerOpeningCurrent(opening) : undefined) ?? Promise.resolve() : Promise.resolve();
  viewerFocusToken = (id: string): object | undefined => this.viewerWindow?.focusToken(id);
  focusViewerRow = (id: string, expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>): object | undefined => {const window = this.viewerWindow; if (!window || !this.current() || this.getSnapshot().accessDenied || this.viewerWindow !== window || !window.isWitness(expected)) return undefined; const row = expected.items.find(item => item.kind === 'group' ? item.value.key === id : item.value.id === id); if (!row || !window.snapshot().items.includes(row) || !this.current() || this.viewerWindow !== window || this.getSnapshot().accessDenied || !window.isWitness(expected) || !window.snapshot().items.includes(row)) return undefined; return window.focus(id);};
  blurViewerRow = (id: string, token: object, expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>): void => {const window = this.viewerWindow; if (window && window.snapshot().scope === expected.scope) window.blur(id, token);};
  focusViewerHost = (expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>): void => {const window = this.viewerWindow; if (window && window.snapshot() === expected && this.current() && this.viewerWindow === window && !this.getSnapshot().accessDenied && window.snapshot() === expected) window.focus(undefined);};
  pinViewerRow = (id: string, expected: DashboardCursorSnapshot<ViewerRow, ViewerPage>): object | undefined => {const window = this.viewerWindow; if (!window || !this.current() || this.getSnapshot().accessDenied || this.viewerWindow !== window || window.snapshot() !== expected) return undefined; return window.pin(id);};
  unpinViewerRow = (token: object, scope: object): void => {const window = this.viewerWindow; if (window && window.snapshot().scope === scope) window.unpin(token);};
  loadFacts(): Promise<void> {
    this.assert();
    if (this.getSnapshot().accessDenied) return Promise.resolve();
    if (this.facts) return this.facts;
    // Header counts and storage come from authoritative aggregate reads, never the loaded page.
    return this.facts = (async () => {
      const summary = await this.service.summary();
      if (!this.current() || this.getSnapshot().accessDenied) return;
      this.publish({...this.getSnapshot(), summary});
      const storage = await this.service.storage();
      if (this.current() && !this.getSnapshot().accessDenied) this.publish({...this.getSnapshot(), storage});
    })().catch(error => {if (this.current() && !this.getSnapshot().accessDenied) this.publish({...this.getSnapshot(), error});}).finally(() => {this.facts = undefined;});
  }
  private async read<T>(work: () => Promise<T>): Promise<T> {this.assert(); const result = await work(); this.assert(); if (this.getSnapshot().accessDenied) throw screenScopeEnded(); return result;}
  detail = (id: string, after?: string): Promise<RecordingDetail> => this.read(() => this.service.detail(id, after));
  deletePreview = (recording: Recording): Promise<DeletePreview> => this.read(() => this.service.deletePreview(recording));
  private requireCommand(lifetime?: DVRCommandLifetime): void {
    if (lifetime && (!this.current() || lifetime.signal.aborted || !lifetime.isCurrent())) throw screenScopeEnded();
  }
  async act<T>(work: (service: NativeRecordingService) => Promise<T>, lifetime?: DVRCommandLifetime): Promise<Readonly<{value: T}> | undefined> {
    const original = lifetime ? Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent}) : undefined;
    this.requireCommand(original);
    this.assert();
    if (this.getSnapshot().busy || this.getSnapshot().accessDenied) return undefined;
    this.publish({...this.getSnapshot(), busy: true, error: undefined});
    try {
      this.requireCommand(original);
      const value = await this.read(() => work(this.service));
      this.requireCommand(original);
      // A receipt is the success boundary. Refreshing another current view never changes
      // the mutation's expectedRevision or creates a replacement retry request id.
      // A Watch command returns its real receipt to that opening. Do not start
      // an unscoped refresh that could adopt into a replacement route; the DVR
      // screen keeps its ordinary authoritative reads when entered.
      if (!original) {void this.load(); void this.loadFacts();}
      this.requireCommand(original);
      return {value};
    } catch (error) {
      this.requireCommand(original);
      if (this.current() && !this.getSnapshot().accessDenied) this.publish({...this.getSnapshot(), error});
      this.requireCommand(original);
      if (original) throw error;
      return undefined;
    }
    finally {if (this.current()) this.publish({...this.getSnapshot(), busy: false});this.requireCommand(original);}
  }
  schedule = (draft: RecordingDraft, options: RecordingOptions, lifetime?: DVRCommandLifetime) => {
    const original = lifetime ? Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent}) : undefined;
    this.requireCommand(original);
    const capturedDraft = original ? Object.freeze({channel: Object.freeze({...draft.channel}), programme: Object.freeze({...draft.programme}), series: draft.series}) : draft;
    const capturedOptions = original ? Object.freeze({...options}) : options;
    return this.act(service => service.schedule(capturedDraft, capturedOptions, original), original);
  };
  cancel = (recording: Recording) => this.act(service => service.cancel(recording));
  keep = (recording: Recording) => this.act(service => service.keep(recording));
  remove = (recording: Recording, preview: DeletePreview) => this.act(service => service.delete(recording, preview));
  storage = (): Promise<RecordingStorage> => this.read(() => this.service.storage());
  saveStorage = (policy: StoragePolicy) => this.act(service => service.saveStorage(policy));
  /** Resolve authority belongs only to the actual first detail page for one opening. */
  detailOpening(recording: Recording, current: () => boolean) {
    const previous = this.detailScope; this.detailScope = undefined; previous?.dispose();
    const original = Object.freeze({id: recording.id, revision: recording.revision}), abort = new AbortController();
    let seed: RecordingDetail | undefined;
    const dispose = () => {if (this.detailScope === scope) this.detailScope = undefined; seed = undefined; this.lifetime.signal.removeEventListener('abort', dispose); abort.abort();};
    const scope = {dispose};
    const unclaimed = () => !this.disposed && !abort.signal.aborted && !this.lifetime.signal.aborted && !this.detailScope;
    if (unclaimed() && this.current() && unclaimed() && current() && unclaimed()) {this.detailScope = scope; this.lifetime.signal.addEventListener('abort', dispose, {once: true}); if (this.lifetime.signal.aborted) dispose();}
    else abort.abort();
    const own = () => !this.disposed && !this.lifetime.signal.aborted && !abort.signal.aborted && this.detailScope === scope;
    const live = () => {
      if (!own()) return false;
      const root = this.current(); if (!own()) return false; if (!root) {dispose(); return false;}
      const visible = current(); if (!own()) return false; if (!visible) {dispose(); return false;}
      const stillRoot = this.current(); if (!own()) return false; if (!stillRoot) {dispose(); return false;}
      return own();
    };
    const eligible = (value: RecordingDetail): boolean => {
      if (!live() || value !== seed || value.recording.id !== original.id || value.recording.revision !== original.revision || value.nextOverlap !== '' || !mutableRecording(value.recording) || !validRecordingOptions(value.recording.options)) return false;
      const priorities = value.overlaps.map(overlap => overlap.priority);
      return priorities.every(Number.isFinite) && Math.max(value.recording.options.priority, ...priorities) < 1000 && live();
    };
    return {
      read: async (): Promise<RecordingDetail> => {
        if (!live()) throw screenScopeEnded();
        const value = await this.detail(original.id);
        if (!live() || value.recording.id !== original.id || value.recording.revision !== original.revision) throw screenScopeEnded();
        const item = Object.freeze({...value.recording, options: Object.freeze({...value.recording.options})});
        const overlaps = Object.freeze(value.overlaps.map(overlap => Object.freeze({...overlap})));
        seed = Object.freeze({...value, recording: item, overlaps}) as unknown as RecordingDetail;
        return seed;
      },
      canResolve: (value: RecordingDetail): boolean => eligible(value),
      resolve: (value: RecordingDetail) => {
        if (!eligible(value)) return Promise.resolve(undefined);
        const priority = Math.max(value.recording.options.priority, ...value.overlaps.map(overlap => overlap.priority)) + 1;
        const lifetime = {signal: abort.signal, isCurrent: () => eligible(value)};
        return this.act(service => {if (!eligible(value)) throw screenScopeEnded(); return service.update(value.recording, {...value.recording.options, priority}, lifetime);}, lifetime);
      },
      dispose,
    };
  }
  saveRule = (rule: RecordingRule, config: RuleConfig) => this.act(service => service.saveRule(rule.id, rule.revision, config));
  /** A new rule is a client-named canonical id with expected revision zero; the receipt is the success boundary. */
  newRuleID = async (lifetime: DVRCommandLifetime): Promise<string> => {
    const original = Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent});this.requireCommand(original);
    try {const id = await this.service.newRuleID(original);this.requireCommand(original);return id;}
    catch (error) {this.requireCommand(original);throw error;}
  };
  createRule = (config: RuleConfig, anchor: Occurrence, lifetime?: DVRCommandLifetime, preparedId?: Promise<string>) => {
    const original = lifetime ? Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent}) : undefined;this.requireCommand(original);
    const captured = original ? Object.freeze({...config, allowedChannels: [...config.allowedChannels], blockedChannels: [...config.blockedChannels],
      keywords: [...config.keywords], blockedKeywords: [...config.blockedKeywords], options: Object.freeze({...config.options})}) : config;
    const capturedAnchor = original ? Object.freeze({...anchor}) : anchor;
    return this.act(async service => {
      const id = await (preparedId ?? service.newRuleID(original));this.requireCommand(original);
      return service.saveRule(id, 0, captured, capturedAnchor, original);
    }, original);
  };
  previewRule = async (config: RuleConfig, lifetime?: DVRCommandLifetime): Promise<RulePreview> => {
    const original = lifetime ? Object.freeze({signal: lifetime.signal, isCurrent: lifetime.isCurrent}) : undefined;this.requireCommand(original);
    try {const preview = await this.read(() => this.service.preview(config, original));this.requireCommand(original);return preview;}
    catch (error) {this.requireCommand(original);throw error;}
  };
  deleteRule = (rule: RecordingRule, future: 'keep' | 'cancel') => this.act(service => service.deleteRule(rule, future));
  dispose(): void {if (this.disposed) return; this.disposed = true; this.detailScope?.dispose(); this.epoch++; this.pending = undefined; this.viewerWindow?.dispose(); this.viewerWindow = undefined; this.service.dispose(); this.publish({query: this.getSnapshot().query, loading: false, busy: false, accessDenied: true}); this.clearListeners();}
}

export function createNativeRecordingsController(lifetime: NativeScreenLifetime, api: ChannelApi, requestId: () => Promise<string>): NativeRecordingsController {
  return new NativeRecordingsController(lifetime, new DVRClient(api, lifetime.authority.serverId, requestId));
}
