import {SelectionModel, selectionActions, selectionKey, summarizeSelection, type SelectionAction, type SelectionContext, type SelectionRegistration, type SelectionEnvironment, type SelectionIdentity, type SelectionKind, type SelectionOperation, type SelectionTarget} from '../selection-model/index.ts';
import {selectionCapabilityNames} from '../selection-model/capabilities.ts';
import {homeChannelSelection, homeCollectionCapabilities, homeCollectionSelection, homeEntryCapabilities, homeEntrySelection, type HomeActionPlatform} from '../home-selection.ts';
import {entryActions, type EntryActionId} from '../presentation/entry-actions.ts';
import type {HomeDocument, HomeEntry, HomeRow, HomeSpotlight} from '../home.ts';
import {DetailService} from '../detail.ts';
import {channelOperationId, saveChannelPreference} from '../channel-guide.ts';
import {getContainerState, setContainerFlag, setContainerState, containerKinds, type ContainerKind} from '../container-state.ts';
import {deletionAllowed, parseDeletePreview, parseDeleteResult} from '../administration.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
import {validBulkEdit, validateBulkReceipt, type BulkEdit} from '../metadata-bulk.ts';
import {fetchBulkMetadataPreview, metadataPreviewTargets, type BulkMetadataPreviewRequest} from './metadata-preview.ts';
import {HomeSelectionDestinations} from './destinations.ts';
import {explicitIdentities, runSelectionJobs, selectionExecutionError, selectionJobTasks, type HomeJobTask} from './execution.ts';
import type {HomeSelectionActionState, HomeSelectionMember, HomeSelectionMenu, HomeSelectionOptions, HomeSelectionPlaybackItem, HomeSelectionSnapshot, HomeSelectionWindowInput} from './types.ts';

/** Removing a window is cleanup and must never throw: a cleanup that throws reaches the app's error boundary
 * (P2-74 E-tv-30, an unmount during a Live expiry re-read). An already removed or stale window is a no-op. */
const releaseWindow = (unregister: () => void) => {try {unregister();} catch { /* nothing left to remove */ }};

const idle = (): HomeSelectionActionState => ({phase: 'idle', action: null, progress: null, results: [], error: null, jobIds: []});
const errorInfo = (error: unknown) => ({code: String((error as {code?: unknown})?.code ?? 'home-action-failed'), retryable: (error as {retryable?: unknown})?.retryable === true});
const sameScope = (a: SelectionContext, b: SelectionContext) =>
  a.scope.serverId === b.scope.serverId && a.scope.authority === b.scope.authority && a.scope.accountId === b.scope.accountId && a.scope.profileId === b.scope.profileId && a.scope.viewId === b.scope.viewId && a.scope.privacyFence === b.scope.privacyFence;
const sameContext = (a: SelectionContext, b: SelectionContext) =>
  sameScope(a, b) && a.revision.catalogRevision === b.revision.catalogRevision && a.revision.viewerRevision === b.revision.viewerRevision;
const sameEnvironment = (a: SelectionEnvironment, b: SelectionEnvironment) => a.platform === b.platform && a.isOwner === b.isOwner && a.canEditMetadata === b.canEditMetadata;
export function homeSelectionContext(scope: Omit<SelectionContext['scope'], 'privacyFence'>, document: HomeDocument): SelectionContext {
  if (scope.serverId !== document.serverId) throw selectionExecutionError('home-source-mismatch');
  return {scope: {...scope, privacyFence: document.viewerFence}, revision: {catalogRevision: String(document.revision.catalog), viewerRevision: String(document.revision.viewer)}};
}
type WindowSource = Readonly<{kind: 'row'; id: string; slot: 'entries' | 'collection' | 'live'} | {kind: 'spotlight'}>;
type WindowOrigin = Readonly<{kind: 'row'; row: HomeRow; index: number} | {kind: 'spotlight'; spotlight: HomeSpotlight}>;
const sameSource = (a: WindowSource, b: WindowSource) => a.kind === 'spotlight' ? b.kind === 'spotlight' : b.kind === 'row' && a.id === b.id && a.slot === b.slot;
type CurrentMember = Readonly<{member: HomeSelectionMember; row?: HomeRow; spotlight?: HomeSpotlight; index: number}>;
type Ticket = {document: HomeDocument; epoch: number; context: SelectionContext; target: SelectionTarget; model?: SelectionOperation; controller: AbortController};
/** Shared execution only: all renderer, locale, key, gesture and haptic ownership stays in the shell. */
export class HomeSelectionController {
  readonly selection: SelectionModel;
  private state: HomeSelectionSnapshot;
  private environment: SelectionEnvironment;
  private listeners = new Set<() => void>();
  private cleanups: (() => void)[] = [];
  private windows = new Map<string, {token: object; members: Map<string, HomeSelectionMember>; unregister: () => void; order: number; start: number; source: WindowSource; origin: WindowOrigin}>();
  private held = new Map<string, HomeSelectionMember>();
  private operationMembers = new Map<string, HomeSelectionMember>();
  private epoch = 0;
  private disposed = false;
  private changing = false;
  private document?: HomeDocument;
  private menuWindow?: () => void;
  private menuSource?: WindowSource;
  private ticket?: Ticket;
  private pickerSession?: HomeSelectionDestinations;
  private jobs: HomeJobTask[] = [];
  private jobHistory = new Set<string>();
  private personalCommands: {done: boolean; run: () => Promise<void>}[] = [];
  private retry?: () => Promise<void>;
  private detail?: DetailService;
  private editorEdits: {body: BulkEdit; receipt?: ReturnType<typeof validateBulkReceipt>; attempted: boolean}[] = [];
  private editorReceipts: ReturnType<typeof validateBulkReceipt>[] = [];
  private editorOriginalTargets: ReturnType<typeof metadataPreviewTargets> = [];
  private editorRetryTargets?: readonly string[];
  private deleteBody?: Readonly<Record<string, unknown>>;
  private options: HomeSelectionOptions;
  constructor(options: HomeSelectionOptions) {
    this.options = options;
    if (!options.playback || ['play', 'enqueue', 'playChannel'].some(command => typeof options.playback[command as keyof typeof options.playback] !== 'function')) throw selectionExecutionError('playback-runtime-required');
    this.environment = {...options.environment};
    this.selection = new SelectionModel(options.context);
    this.state = {context: this.selection.getSnapshot().context, selection: this.selection.getSnapshot(), displayMembers: [], actions: selectionActions(this.selection.getSnapshot().summary, this.environment), menu: null, picker: null, editor: null, deletion: null, rating: null, mixedEdit: false, editExplanation: null, action: idle()};
    this.cleanups.push(this.selection.subscribe(() => {
      const snapshot = this.selection.getSnapshot();
      for (const [key, member] of this.allMembers()) if (snapshot.selectedKeys.includes(key)) this.held.set(key, member);
      for (const key of this.held.keys()) if (!snapshot.selectedKeys.includes(key)) this.held.delete(key);
      if (this.ticket?.model && !this.selection.isOperationCurrent(this.ticket.model)) this.closeDialogs();
      this.publish({selection: snapshot, actions: selectionActions(snapshot.summary, this.environment)});
    }));
    this.cleanups.push(options.home.subscribe(() => this.homeChanged()));
    const revalidateJobs = () => {
      // The durable job read remains authoritative; the shared feed never creates a second job.
      if (!this.options.isCurrent(this.state.context)) this.invalidate();
    };
    this.cleanups.push(options.events.on('job.updated', revalidateJobs));
    const offResync = options.events.onResync?.(revalidateJobs);
    if (offResync) this.cleanups.push(offResync);
    this.homeChanged();
  }
  getSnapshot = (): HomeSelectionSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<HomeSelectionSnapshot>) {
    if (this.disposed) return;
    const next = {...this.state, ...patch};
    const target = this.ticket && this.current(this.ticket) ? this.ticket.target : next.selection.target;
    const members = this.allMembers();
    const displayMembers = this.isHomeCurrent(next.context) && target?.mode === 'explicit' ? target.displayItems.flatMap(pick => {const member = members.get(pick.key); return member ? [member] : [];}) : [];
    this.state = Object.freeze({...next, displayMembers: Object.freeze(displayMembers)});
    if (!this.changing) for (const listener of this.listeners) listener();
  }
  getDisplayMembers = () => this.state.displayMembers;
  private isHomeCurrent(context: SelectionContext) {
    const document = this.options.home.getSnapshot().document;
    return !!document && this.options.isCurrent(context) && document.serverId === context.scope.serverId && document.viewerFence === context.scope.privacyFence && String(document.revision.catalog) === context.revision.catalogRevision && String(document.revision.viewer) === context.revision.viewerRevision;
  }
  private active() {if (this.disposed) throw selectionExecutionError('stale-home-context'); if (!this.isHomeCurrent(this.state.context)) {this.invalidate(); throw selectionExecutionError('stale-home-context');}}
  private platform(): HomeActionPlatform {return {platform: this.environment.platform === 'phone-app' ? 'phone-app' : this.environment.platform === 'tv' ? 'tv' : 'web', playbackAvailable: true, queueAvailable: true, nativeDownloadsAvailable: !!this.options.download};}
  private homeChanged() {
    const document = this.options.home.getSnapshot().document;
    if (!document || document.serverId !== this.state.context.scope.serverId || document.viewerFence !== this.state.context.scope.privacyFence) {this.invalidate(); return;}
    const next = homeSelectionContext(this.state.context.scope, document);
    if (!this.options.isCurrent(next)) {this.invalidate(); return;}
    if (document !== this.document || !sameContext(next, this.state.context)) this.reconcile(next, this.environment);
  }
  private invalidate(keep?: ReadonlySet<string>) {this.cancelAction(); this.selection.clear(); for (const [id, window] of this.windows) if (!keep?.has(id)) {releaseWindow(window.unregister); this.windows.delete(id);} this.menuWindow = undefined; this.menuSource = undefined; this.held.clear(); this.editorEdits = []; this.editorReceipts = []; this.editorOriginalTargets = []; this.editorRetryTargets = undefined; this.deleteBody = undefined; this.jobs = []; this.personalCommands = []; this.publish({menu: null, picker: null, editor: null, deletion: null, rating: null, mixedEdit: false, editExplanation: null, action: idle()});}
  /** Revision changes may retain a menu's semantic opener, never its old grants or in-flight work. */
  private reconcile(context: SelectionContext, environment: SelectionEnvironment) {
    const previous = this.state.context, menu = this.state.menu, source = this.menuSource, window = menu && this.windows.get(menu.windowId), menuWindow = this.menuWindow;
    const keep = new Set<string>();
    if (sameContext(previous, context) && sameEnvironment(this.environment, environment) && this.isHomeCurrent(context)) {
      const rows = this.rows(), spotlight = this.options.home.getSnapshot().document?.spotlight;
      for (const [id, registered] of this.windows) {
        const origin = registered.origin;
        // Genuine immutable sources at the same coordinates keep their original token and cleanup.
        // A changed source still goes through semantic menu reconciliation below.
        if (origin.kind === 'spotlight' ? spotlight?.[registered.start] === origin.spotlight
          : rows[origin.index] === origin.row && rows.filter(row => row.id === origin.row.id).length === 1
            && (!origin.row.live || Number.isFinite(Date.parse(origin.row.live.expiresAt)) && Date.parse(origin.row.live.expiresAt) > Date.now())) keep.add(id);
      }
    }
    const retainedMenuWindow = menu && keep.has(menu.windowId) ? menuWindow : undefined;
    this.changing = true;
    try {
      this.invalidate(keep);
      this.environment = {...environment}; this.selection.setContext(context);
      this.document = this.options.home.getSnapshot().document;
      this.publish({context: this.selection.getSnapshot().context, selection: this.selection.getSnapshot(), actions: selectionActions(this.selection.getSnapshot().summary, this.environment), action: idle()});
      if (!menu || !window || !source || !sameSource(source, window.source) || !sameScope(previous, context) || !this.isHomeCurrent(context)) {retainedMenuWindow?.(); return;}
      const current = this.resolveMember(source, menu.key);
      if (!current || !this.retainsGrants(menu, current.member)) {retainedMenuWindow?.(); return;}
      if (this.windows.get(menu.windowId) === window) {
        this.menuWindow = retainedMenuWindow; this.openMenu(menu.windowId, menu.key); return;
      }
      // Re-register only the proven current item. The shell owns physical order and visible windows.
      const registered = current.spotlight ? this.registerSpotlight({id: menu.windowId, spotlight: current.spotlight})
        : current.row ? this.registerWindow({id: menu.windowId, row: current.row, sectionOrder: window.order - 1,
          ...(source.kind === 'row' && source.slot === 'live' ? {channels: current.row.live!.channels.slice(current.index, current.index + 1)}
            : source.kind === 'row' && source.slot === 'entries' ? {entries: [current.member.entry!]} : {})}) : undefined;
      if (registered?.ok) {this.menuWindow = registered.unregister; this.openMenu(menu.windowId, menu.key);}
    } finally {
      this.changing = false;
      for (const listener of this.listeners) listener();
    }
  }
  setContext(context: SelectionContext, environment = this.environment) {
    if (this.disposed) throw selectionExecutionError('disposed');
    const before = this.state.context.scope, after = context.scope;
    if (['serverId', 'authority', 'accountId', 'profileId'].some(key => before[key as keyof typeof before] !== after[key as keyof typeof after])) {this.invalidate(); throw selectionExecutionError('recreate-home-controller-for-source');}
    if (!sameScope(this.state.context, context) || !this.isHomeCurrent(context)) {
      this.invalidate(); this.environment = {...environment}; this.selection.setContext(context);
      this.publish({context: this.selection.getSnapshot().context, selection: this.selection.getSnapshot(), actions: selectionActions(this.selection.getSnapshot().summary, this.environment), action: idle()}); return;
    }
    if (sameContext(this.state.context, context) && this.document === this.options.home.getSnapshot().document && sameEnvironment(this.environment, environment)) return;
    this.reconcile(context, environment);
  }
  private rows(): readonly HomeRow[] {
    const document = this.options.home.getSnapshot().document; if (!document) return [];
    const rows: HomeRow[] = []; const visit = (row: HomeRow) => {rows.push(row); for (const child of row.children ?? []) visit(child);};
    for (const row of document.rows) visit(row); return rows;
  }
  /** An expired Live row is refused without a throw: the shell drops that window and the next Home read
   * brings a current one. Callers can't avoid the race, since the clock can cross expiry between their check and this one. */
  registerWindow(input: HomeSelectionWindowInput): SelectionRegistration | Readonly<{ok: false; reason: 'stale-home-live-window'}> {
    this.active();
    const row = this.rows().find(value => value === input.row);
    if (!row || row.viewerFence !== this.state.context.scope.privacyFence) throw selectionExecutionError('unoffered-home-row');
    const members = new Map<string, HomeSelectionMember>(), platform = this.platform();
    let start = input.start ?? row.start;
    const put = (member: HomeSelectionMember) => members.set(this.key(member.item), member);
    if (input.channels !== undefined || row.live) {
      if (!row.live || !(Date.parse(row.live.expiresAt) > Date.now())) return Object.freeze({ok: false as const, reason: 'stale-home-live-window' as const});
      const channels = input.channels ?? row.live.channels;
      const offset = channels.length ? row.live.channels.indexOf(channels[0]) : 0;
      if (offset < 0 || channels.some((channel, index) => row.live!.channels[offset + index] !== channel) || input.start !== undefined && input.start !== row.start + offset) throw selectionExecutionError('home-window-order-mismatch');
      start = row.start + offset;
      for (const channel of channels) {
        if (!row.live.channels.includes(channel)) throw selectionExecutionError('unoffered-home-channel');
        put({item: homeChannelSelection(channel, platform), channel});
      }
    } else if (row.collection && input.entries === undefined) {
      put({item: homeCollectionSelection(row.collection, platform), collection: row.collection});
    } else {
      const entries = input.entries ?? row.entries, source = row.collection?.members ?? row.entries;
      const offset = entries.length ? source.indexOf(entries[0] as HomeEntry) : 0;
      if (offset < 0 || entries.some((entry, index) => source[offset + index] !== entry) || input.start !== undefined && input.start !== row.start + offset) throw selectionExecutionError('home-window-order-mismatch');
      start = row.start + offset;
      for (const entry of entries) {
      if (!row.entries.includes(entry) && !row.collection?.members.includes(entry)) throw selectionExecutionError('unoffered-home-entry');
      const item = homeEntrySelection(entry as HomeEntry, platform);
      if (item) put({item, entry, playbackTarget: row.resumes?.find(resume => resume.entryId === entry.id)?.target ?? entry});
    }
    }
    const ticket = this.selection.captureContext(), token = {};
    const result = this.selection.registerWindow(ticket, {id: input.id, sectionId: row.id, sectionOrder: input.sectionOrder + 1, start, items: [...members.values()].map(member => member.item)});
    if (!result.ok) return result;
    this.windows.set(input.id, {token, members, unregister: result.unregister, order: input.sectionOrder + 1, start, source: {kind: 'row', id: row.id, slot: input.channels !== undefined || row.live ? 'live' : row.collection && input.entries === undefined ? 'collection' : 'entries'}, origin: {kind: 'row', row, index: this.rows().indexOf(row)}});
    return {ok: true as const, unregister: () => {if (this.windows.get(input.id)?.token === token) this.windows.delete(input.id); releaseWindow(result.unregister);}};
  }
  registerSpotlight(input: Readonly<{id: string; spotlight: HomeSpotlight}>) {
    this.active();
    const document = this.options.home.getSnapshot().document, index = document?.spotlight.indexOf(input.spotlight) ?? -1;
    if (index < 0) throw selectionExecutionError('unoffered-home-spotlight');
    const item = homeEntrySelection(input.spotlight.entry, this.platform());
    if (!item) throw selectionExecutionError('spotlight-identity-required');
    const member: HomeSelectionMember = {item, entry: input.spotlight.entry, playbackTarget: input.spotlight.resumeTarget ?? input.spotlight.entry};
    const key = this.key(item), token = {}, members = new Map([[key, member]]);
    const result = this.selection.registerWindow(this.selection.captureContext(), {id: input.id, sectionId: 'home:spotlight', sectionOrder: 0, start: index, items: [item]});
    if (!result.ok) return result;
    this.windows.set(input.id, {token, members, unregister: result.unregister, order: 0, start: index, source: {kind: 'spotlight'}, origin: {kind: 'spotlight', spotlight: input.spotlight}});
    return {ok: true as const, unregister: () => {if (this.windows.get(input.id)?.token === token) this.windows.delete(input.id); releaseWindow(result.unregister);}};
  }
  private allMembers() {const members = new Map<string, HomeSelectionMember>(); for (const window of [...this.windows.values()].sort((a, b) => b.order - a.order || b.start - a.start)) for (const [key, member] of window.members) members.set(key, member); for (const [key, member] of this.held) members.set(key, member); for (const [key, member] of this.operationMembers) members.set(key, member); return members;}
  key(identity: SelectionIdentity) {return selectionKey(this.state.context.scope, identity);}
  toggle(windowId: string, key: string, range = false) {
    this.active(); const member = this.windows.get(windowId)?.members.get(key);
    if (member && !this.selection.isSelected(key)) this.held.set(key, member);
    return this.selection.toggle(windowId, key, range);
  }
  previewRange(windowId: string, key: string) {this.active(); return this.selection.previewRange(windowId, key);}
  selectRow() {this.active(); return this.selection.selectRow();}
  selectLoaded(sectionId?: string) {this.active(); return this.selection.selectLoaded(sectionId);}
  keepOnly(kind: SelectionKind) {this.closeDialogs(); return this.selection.keepOnly(kind);}
  clear() {this.closeDialogs(); this.selection.clear();}
  private begin(target?: SelectionTarget): Ticket {
    this.active(); const bindings = this.allMembers(); this.cancelAction();
    this.jobHistory.clear();
    if (target && !sameContext(target.context, this.state.context)) throw selectionExecutionError('stale-home-context');
    const model = target ? undefined : this.selection.captureOperation();
    const selected = target ?? model?.target; if (!selected) throw selectionExecutionError('selection-required');
    explicitIdentities(selected);
    const ticket = {document: this.options.home.getSnapshot().document!, epoch: ++this.epoch, context: this.state.context, target: selected, ...(model ? {model} : {}), controller: new AbortController()}; this.ticket = ticket;
    if (selected.mode === 'explicit') for (const pick of selected.items) {const member = bindings.get(pick.key); if (member) this.operationMembers.set(pick.key, member);}
    return ticket;
  }
  private liveCurrent(ticket: Ticket) {
    return ticket.target.mode === 'explicit' && ticket.target.items.every(pick => {
      if (pick.item.kind !== 'channel') return true;
      const member = this.operationMembers.get(pick.key);
      return !!member?.channel && this.rows().some(row => row.viewerFence === ticket.context.scope.privacyFence && row.live && Number.isFinite(Date.parse(row.live.expiresAt)) && Date.parse(row.live.expiresAt) > Date.now() && row.live.channels.includes(member.channel!))
        && selectionCapabilityNames.every(name => homeChannelSelection(member.channel!, this.platform()).capabilities[name] === pick.item.capabilities[name]);
    });
  }
  private current(ticket: Ticket) {return !this.disposed && this.ticket === ticket && this.epoch === ticket.epoch && !ticket.controller.signal.aborted && this.options.home.getSnapshot().document === ticket.document && this.isHomeCurrent(ticket.context) && this.liveCurrent(ticket) && (!ticket.model || this.selection.isOperationCurrent(ticket.model));}
  private check(ticket: Ticket) {if (!this.current(ticket)) {if (this.ticket === ticket) this.cancelAction(); throw selectionExecutionError('cancelled');}}
  cancelAction() {
    this.epoch++; this.ticket?.controller.abort(); this.ticket = undefined; this.pickerSession?.dispose(); this.pickerSession = undefined; this.detail?.dispose(); this.detail = undefined;
    this.retry = undefined; this.operationMembers.clear();
    const selected = new Set(this.selection.getSnapshot().selectedKeys); for (const key of this.held.keys()) if (!selected.has(key)) this.held.delete(key);
    if (this.state?.action.phase === 'working') this.publish({action: {...this.state.action, phase: 'cancelled'}});
  }
  private async guarded(ticket: Ticket, action: HomeSelectionActionState['action'], work: () => Promise<void>, retry?: () => Promise<void>) {
    this.check(ticket); if (retry) this.retry = retry;
    this.publish({action: {...idle(), phase: 'working', action, jobIds: [...this.jobHistory]}});
    try {await work(); this.check(ticket); this.publish({action: {...this.state.action, phase: this.state.action.phase === 'partial' ? 'partial' : 'complete'}});}
    catch (error) {
      if (!this.current(ticket)) return;
      const info = errorInfo(error); this.publish({action: {...this.state.action, phase: info.retryable ? 'uncertain' : 'error', error: info}}); this.options.reportError(error);
    }
  }
  private members(target: SelectionTarget, display = false) {
    if (target.mode !== 'explicit') {explicitIdentities(target); throw selectionExecutionError('home-explicit-selection-required');}
    const members = this.allMembers();
    return (display ? target.displayItems : target.items).map(pick => {
      const member = members.get(pick.key);
      const offered = member?.entry ? homeEntrySelection(member.entry as HomeEntry, this.platform()) : member?.collection ? homeCollectionSelection(member.collection, this.platform()) : member?.channel ? homeChannelSelection(member.channel, this.platform()) : undefined;
      const matches = (item: HomeSelectionMember['item']) => this.key(item) === pick.key && selectionCapabilityNames.every(name => item.capabilities[name] === pick.item.capabilities[name]);
      if (!member || this.key(pick.item) !== pick.key || !matches(member.item) || !offered || !matches(offered)) throw selectionExecutionError('home-offers-changed');
      return member;
    });
  }
  async execute(action: SelectionAction) {
    this.active(); const rule = this.state.actions[action];
    if (rule.state === 'explain') {if (action === 'edit') {this.publish({mixedEdit: true, editExplanation: rule.reason}); return;} if (action === 'delete') return this.openDelete();}
    if (rule.state !== 'ready') throw selectionExecutionError(rule.state === 'disabled' ? rule.reason : 'action-not-offered');
    if (action === 'playlist' || action === 'collection') return this.openPicker(action);
    if (action === 'edit') return this.openEditor(); if (action === 'delete') return this.openDelete();
    const ticket = this.begin(); return this.executeTarget(ticket, action);
  }
  private async executeTarget(ticket: Ticket, action: SelectionAction, startIntent?: 'resume' | 'start-over' | 'explicit') {
    const members = this.members(ticket.target, action === 'play');
    const execute = async () => {
      this.check(ticket);
      if (action === 'play' || action === 'play-next' || action === 'play-later') {
        if (members.some(member => member.channel)) {
          if (action !== 'play' || members.length !== 1 || !members[0].channel) throw selectionExecutionError('channel-bulk-play-unavailable');
          await this.options.playback.playChannel(members[0].channel, ticket.context, ticket.controller.signal); this.check(ticket); return;
        }
        const items: HomeSelectionPlaybackItem[] = members.map(member => {
          if (member.collection) return {identity: member.item, collection: member.collection};
          const entry = member.entry, target = member.playbackTarget?.playback;
          if (!entry || !target) throw selectionExecutionError('playback-target-unavailable');
          return {identity: member.item, entry, target: startIntent === 'start-over' ? {...target, startSeconds: 0} : target, startIntent: startIntent ?? (entry.kind === 'chapter' ? 'explicit' : 'resume')};
        });
        const request = {context: ticket.context, items, signal: ticket.controller.signal};
        const result = action === 'play' ? await this.options.playback.play(request) : await this.options.playback.enqueue({...request, placement: action === 'play-next' ? 'next' : 'end'});
        this.check(ticket); if (!result?.queue?.id || !result.queue.revision) throw selectionExecutionError('playback-receipt-required');
      } else if (action === 'download') {
        if (!this.options.download || members.some(member => !member.entry)) throw selectionExecutionError('download-runtime-required');
        await this.options.download(members.map(member => member.entry!), ticket.context, ticket.controller.signal);
      } else {
        const args = action === 'my-list' ? {watchlist: true} : action === 'remove-my-list' ? {watchlist: false} : action === 'favorite' ? {favorite: true} : action === 'mark-watched' ? {watched: true} : action === 'mark-unwatched' ? {watched: false} : {};
        this.personalCommands = [];
        const jobKeys = new Set<string>();
        for (const member of members) {
          const identity = member.item;
          if (identity.kind !== 'channel') {jobKeys.add(this.key(identity)); continue;}
          if (identity.kind === 'channel') {
            if (action !== 'favorite' || !member.channel) throw selectionExecutionError('channel-action-unavailable');
            const channel = member.channel, requestId = await channelOperationId(this.options.requestId); this.check(ticket);
            this.personalCommands.push({done: false, run: async () => {await saveChannelPreference(this.options.api, ticket.context.scope.serverId, channel.channel, requestId, {favorite: true, hidden: channel.channel.hidden}, ticket.controller.signal);}});
          }
        }
        if (ticket.target.mode !== 'explicit') throw selectionExecutionError('home-explicit-selection-required');
        const jobTarget = {...ticket.target, items: ticket.target.items.filter(item => jobKeys.has(item.key)), displayItems: ticket.target.displayItems.filter(item => jobKeys.has(item.key))};
        this.jobs = jobTarget.items.length ? await selectionJobTasks(jobTarget, action === 'refresh' ? 'refresh' : 'personal-state', args, this.options.requestId) : []; this.check(ticket);
        await this.runJobs(ticket);
      }
    };
    await this.guarded(ticket, action, execute, ['my-list', 'remove-my-list', 'mark-watched', 'mark-unwatched', 'favorite', 'refresh'].includes(action) ? () => this.guarded(ticket, action, () => this.runJobs(ticket)) : undefined);
  }
  private async runJobs(ticket: Ticket) {
    for (const command of this.personalCommands) {this.check(ticket); if (!command.done) {await command.run(); this.check(ticket); command.done = true;}}
    if (!this.jobs.length) return;
    await runSelectionJobs(this.options.api, this.jobs, ticket.controller.signal, () => this.current(ticket), (progress, jobIds) => {for (const id of jobIds) this.jobHistory.add(id); this.publish({action: {...this.state.action, progress, jobIds: [...this.jobHistory]}});}, undefined, this.options.events); this.check(ticket);
    const failures = this.jobs.flatMap(task => task.failures);
    for (const task of this.jobs) if (task.job) this.jobHistory.add(task.job.jobId);
    this.publish({action: {...this.state.action, phase: failures.length ? 'partial' : 'complete', results: failures.map(failure => ({id: failure.itemId, ok: false, code: failure.code})), jobIds: [...this.jobHistory]}});
  }
  async retryAction() {
    this.active(); if (!this.retry) throw selectionExecutionError('retry-unavailable');
    const ticket = this.ticket;
    if (this.state.action.phase === 'partial' && this.jobs.length) {
      const missing = [...new Set(this.jobs.flatMap(task => task.failures.map(failure => failure.itemId)))];
      if (!missing.length) throw selectionExecutionError('job-failures-incomplete');
      const prior = this.jobs.at(-1)!.request, retries: HomeJobTask[] = [];
      for (let offset = 0; offset < missing.length; offset += 200) {const operationId = (await this.options.requestId()).replaceAll('-', ''); if (ticket) this.check(ticket); retries.push({request: {...prior, operationId, selector: {items: {ids: missing.slice(offset, offset + 200)}}}, failures: [], observed: false});}
      this.jobs = retries;
    }
    await this.retry();
  }
  private menuGroups(member: HomeSelectionMember) {
    const platform = this.platform();
    return entryActions(member.entry ? homeEntryCapabilities(member.entry, platform) : member.collection ? homeCollectionCapabilities(member.collection, platform)
      : member.channel ? {kind: 'channel', playable: member.item.capabilities.play, ...(member.item.capabilities.favorite ? {favorite: member.channel.channel.favorite} : {})} : {});
  }
  private retainsGrants(menu: HomeSelectionMenu, member: HomeSelectionMember) {
    const normalize = (action: EntryActionId) => action === 'resume' || action === 'playFromStart' ? 'play' : action;
    const actions = new Set(this.menuGroups(member).flatMap(group => group.actions.map(action => normalize(action.id))));
    return selectionCapabilityNames.every(name => !menu.member.item.capabilities[name] || member.item.capabilities[name])
      && menu.groups.every(group => group.actions.every(action => actions.has(normalize(action.id))));
  }
  /** Resolve by full identity inside the original genuine source, never by card position. */
  private resolveMember(source: WindowSource, key: string): CurrentMember | undefined {
    const platform = this.platform();
    const entryMember = (entry: HomeEntry, target = entry): HomeSelectionMember | undefined => {
      const item = homeEntrySelection(entry, platform);
      if (!item || this.key(item) !== key || entry.available === false) return;
      if ((item.capabilities.play || homeEntryCapabilities(entry, platform).playable) && (target.available === false || !target.playback?.itemId || target.libraryId !== entry.libraryId)) return;
      return {item, entry, playbackTarget: target};
    };
    if (source.kind === 'spotlight') {
      const found = this.options.home.getSnapshot().document?.spotlight.flatMap((spotlight, index) => {
        const member = entryMember(spotlight.entry, spotlight.resumeTarget ?? spotlight.entry); return member ? [{member, spotlight, index}] : [];
      }) ?? [];
      return found.length === 1 ? found[0] : undefined;
    }
    const rows = this.rows().filter(row => row.id === source.id && row.viewerFence === this.state.context.scope.privacyFence);
    if (rows.length !== 1) return;
    const row = rows[0];
    if (source.slot === 'live') {
      if (!row.live || !Number.isFinite(Date.parse(row.live.expiresAt)) || Date.parse(row.live.expiresAt) <= Date.now()) return;
      const found = row.live.channels.flatMap((channel, index) => {const item = homeChannelSelection(channel, platform); return this.key(item) === key ? [{member: {item, channel}, row, index}] : [];});
      return found.length === 1 ? found[0] : undefined;
    }
    if (source.slot === 'collection') {
      if (!row.collection) return;
      const item = homeCollectionSelection(row.collection, platform);
      return this.key(item) === key ? {member: {item, collection: row.collection}, row, index: 0} : undefined;
    }
    const found = (row.collection?.members ?? row.entries).flatMap((entry, index) => {
      const member = entryMember(entry, row.resumes?.find(resume => resume.entryId === entry.id)?.target ?? entry); return member ? [{member, row, index}] : [];
    });
    return found.length === 1 ? found[0] : undefined;
  }
  private currentMenu(menu: HomeSelectionMenu) {
    const window = this.windows.get(menu.windowId);
    if (!window?.members.has(menu.key) || !this.menuSource || !sameSource(this.menuSource, window.source)) {this.closeMenu(); throw selectionExecutionError('unregistered-home-entry');}
    const current = this.resolveMember(this.menuSource, menu.key);
    if (!current || !this.retainsGrants(menu, current.member)) {this.closeMenu(); throw selectionExecutionError('home-offers-changed');}
    return {...menu, member: current.member, groups: this.menuGroups(current.member)};
  }
  openMenu(windowId: string, key: string) {
    this.active(); const window = this.windows.get(windowId), current = window?.members.has(key) ? this.resolveMember(window.source, key) : undefined;
    if (!current) throw selectionExecutionError('unregistered-home-entry');
    if (this.state.menu && (this.state.menu.windowId !== windowId || this.state.menu.key !== key)) this.closeMenu();
    this.menuSource = window!.source;
    this.publish({menu: {windowId, key, member: current.member, groups: this.menuGroups(current.member)}});
  }
  closeMenu() {this.menuSource = undefined; const cleanup = this.menuWindow; this.menuWindow = undefined; cleanup?.(); this.publish({menu: null});}
  async executeMenu(action: EntryActionId, value?: number | null) {
    this.active(); const shown = this.state.menu, menu = shown ? this.currentMenu(shown) : null;
    if (!menu || !menu.groups.some(group => group.actions.some(offered => offered.id === action))) throw selectionExecutionError('menu-action-not-offered');
    const {member, key} = menu;
    const target: SelectionTarget = {mode: 'explicit', context: this.state.context, items: [{key, item: member.item}], displayItems: [{key, item: member.item}]};
    this.closeMenu();
    if (action === 'details') {if (member.collection) await this.options.routes.openCollection(member.collection); else if (member.entry) await this.options.routes.openEntry(member.entry); else throw selectionExecutionError('details-unavailable'); return;}
    this.held.set(key, member);
    if (action === 'addToPlaylist' || action === 'addToCollection') return this.startPicker(action === 'addToPlaylist' ? 'playlist' : 'collection', target, true);
    if (action === 'editMetadata') return this.startEditor(target, true); if (action === 'delete') return this.startDelete(target, true);
    const ticket = this.begin(target);
    if (action === 'rate') return this.openRating(ticket, member, value);
    const mapped: Partial<Record<EntryActionId, SelectionAction>> = {play: 'play', resume: 'play', playFromStart: 'play', playNext: 'play-next', addToQueue: 'play-later', download: 'download', refreshMetadata: 'refresh'};
    if (mapped[action]) return this.executeTarget(ticket, mapped[action]!, action === 'playFromStart' ? 'start-over' : action === 'resume' ? 'resume' : undefined);
    await this.guarded(ticket, action, async () => {
      if (member.channel && action === 'favorite') {
        const requestId = await channelOperationId(this.options.requestId); this.check(ticket);
        const channel = member.channel;
        const run = async () => {await saveChannelPreference(this.options.api, ticket.context.scope.serverId, channel.channel, requestId, {favorite: !channel.channel.favorite, hidden: channel.channel.hidden}, ticket.controller.signal);};
        this.retry = () => this.guarded(ticket, action, run); await run(); return;
      }
      if (!member.entry || member.item.kind === 'channel' || member.item.kind === 'person') throw selectionExecutionError('personal-action-unavailable');
      const entry = member.entry as HomeEntry;
      if (containerKinds.includes(entry.kind as ContainerKind)) {
        const kind = entry.kind as ContainerKind;
        const state = await getContainerState(this.options.api, kind, entry.id, ticket.controller.signal); this.check(ticket);
        const run = async () => {
          if (action === 'watchlist' || action === 'favorite') await setContainerFlag(this.options.api, kind, entry.id, action === 'watchlist' ? 'watchlisted' : 'favorite', action === 'watchlist' ? !state.watchlisted : !state.favorite, ticket.controller.signal);
          else if (action === 'watched') await setContainerState(this.options.api, kind, entry.id, {expectedRevision: state.revision, watched: !state.watched}, ticket.controller.signal);
          else throw selectionExecutionError('personal-action-unavailable');
        };
        this.retry = () => this.guarded(ticket, action, run); await run();
      } else {
        const scope = ticket.context.scope;
        this.detail = new DetailService({api: this.options.api, scope: {serverId: scope.serverId, viewerId: JSON.stringify([scope.authority, scope.accountId, scope.profileId])}, requestId: this.options.requestId});
        await this.detail.select({libraryId: entry.libraryId!, itemId: entry.id}); this.check(ticket);
        const snapshot = this.detail.getSnapshot(), data = snapshot.data;
        if (snapshot.phase !== 'ready' || !data) throw selectionExecutionError(snapshot.error?.code ?? 'detail-unavailable');
        const intent = action === 'watchlist' ? {action: 'watchlist' as const, value: !data.personal.watchlisted} : action === 'favorite' ? {action: 'favorite' as const, value: !data.personal.favorite} : {action: 'watched' as const, value: !data.personal.watched};
        const detail = this.detail;
        this.retry = () => this.guarded(ticket, action, async () => {await detail.retryMutation(); this.check(ticket); const state = detail.getSnapshot(); if (state.pending.length || state.mutationError) throw selectionExecutionError(state.mutationError?.code ?? 'personal-result-unconfirmed', true);});
        await this.detail.mutate(intent); this.check(ticket);
        const result = this.detail.getSnapshot(); if (result.pending.length || result.mutationError) throw selectionExecutionError(result.mutationError?.code ?? 'personal-result-unconfirmed', true);
      }
    });
  }
  private async openRating(ticket: Ticket, member: HomeSelectionMember, supplied?: number | null) {
    const entry = member.entry;
    if (!entry?.libraryId) throw selectionExecutionError('rating-not-offered');
    this.publish({rating: {phase: 'loading', currentValue: undefined, value: undefined, error: null}});
    const scope = ticket.context.scope;
    this.detail = new DetailService({api: this.options.api, scope: {serverId: scope.serverId, viewerId: JSON.stringify([scope.authority, scope.accountId, scope.profileId])}, requestId: this.options.requestId});
    try {
      const detail = this.detail;
      await detail.select({libraryId: entry.libraryId, itemId: entry.id});
      this.ratingReadReady(ticket, detail);
      if (supplied !== undefined) {this.check(ticket); this.setRatingValue(supplied); this.check(ticket); await this.submitRating();}
    } catch (error) {if (this.current(ticket)) this.publish({rating: {...this.state.rating!, phase: 'error', error: errorInfo(error)}});}
  }
  private ratingReadReady(ticket: Ticket, detail: DetailService) {
    this.check(ticket);
    const members = this.members(ticket.target), entry = members.length === 1 ? members[0].entry as HomeEntry | undefined : undefined;
    const state = detail.getSnapshot(), data = state.data;
    if (this.detail !== detail || !entry?.libraryId || !entryActions(homeEntryCapabilities(entry, this.platform())).some(group => group.actions.some(action => action.id === 'rate')) || state.phase !== 'ready' || !data?.actions.some(action => action.id === 'rating' && action.enabled)) throw selectionExecutionError(state.error?.code ?? 'rating-not-offered');
    if (data.scope.serverId !== ticket.context.scope.serverId || data.scope.viewerFence !== ticket.context.scope.privacyFence || data.scope.libraryId !== entry.libraryId || data.scope.itemId !== entry.id || data.item.kind !== entry.kind || String(data.revision.catalog) !== ticket.context.revision.catalogRevision || String(data.revision.viewer) !== ticket.context.revision.viewerRevision) throw selectionExecutionError('stale-rating-context');
    this.publish({rating: {phase: 'ready', currentValue: data.personal.rating, value: data.personal.rating, error: null}});
  }
  setRatingValue(value: number | null) {
    this.active(); const rating = this.state.rating;
    if (rating?.phase !== 'ready' || value !== null && (!Number.isInteger(value) || value < 0 || value > 10)) throw selectionExecutionError('invalid-rating');
    this.publish({rating: {...rating, value}});
  }
  async submitRating() {
    const ticket = this.ticket, rating = this.state.rating, detail = this.detail;
    if (!ticket || !rating || rating.phase !== 'ready' || rating.value === undefined || !detail) throw selectionExecutionError('rating-not-ready'); this.check(ticket);
    this.publish({rating: {...rating, phase: 'saving', error: null}});
    await detail.mutate({action: 'rating', value: rating.value}); this.check(ticket); this.ratingOutcome(detail);
  }
  private ratingOutcome(detail: DetailService) {
    const snapshot = detail.getSnapshot();
    if (snapshot.pending.length || snapshot.mutationError) this.publish({rating: {...this.state.rating!, phase: 'uncertain', error: {code: snapshot.mutationError?.code ?? 'rating-unconfirmed', retryable: true}}});
    else this.publish({rating: {...this.state.rating!, phase: 'result', currentValue: snapshot.data?.personal.rating, value: snapshot.data?.personal.rating, error: null}});
  }
  async retryRating() {
    const ticket = this.ticket, detail = this.detail, rating = this.state.rating;
    if (!ticket || !detail || !rating || rating.phase !== 'uncertain' && rating.phase !== 'error') throw selectionExecutionError('rating-retry-unavailable'); this.check(ticket);
    if (rating.phase === 'error') {
      this.publish({rating: {...rating, phase: 'loading', error: null}});
      try {await detail.retryRead(); this.ratingReadReady(ticket, detail);}
      catch (error) {if (this.current(ticket) && this.detail === detail) this.publish({rating: {...this.state.rating!, phase: 'error', error: errorInfo(error)}});}
      return;
    }
    this.publish({rating: {...rating, phase: 'saving', error: null}});
    await detail.retryMutation(); this.check(ticket); this.ratingOutcome(detail);
  }
  closeRating() {this.cancelAction(); this.publish({rating: null});}
  async openPicker(kind: 'playlist' | 'collection', target?: SelectionTarget) {
    return this.startPicker(kind, target);
  }
  private async startPicker(kind: 'playlist' | 'collection', target?: SelectionTarget, menuOffered = false) {
    const ticket = this.begin(target); const summary = summarizeSelection(this.members(ticket.target).map(member => member.item));
    if (!menuOffered && selectionActions(summary, this.environment)[kind].state !== 'ready') throw selectionExecutionError('picker-action-not-offered');
    this.pickerSession = new HomeSelectionDestinations(this.options, ticket.target, kind, ticket.controller.signal, () => this.current(ticket), picker => this.publish({picker})); await this.pickerSession.load();
  }
  pickerSearch(query: string) {return this.pickerSession?.search(query) ?? Promise.reject(selectionExecutionError('picker-required'));}
  pickerNext() {return this.pickerSession?.page() ?? Promise.reject(selectionExecutionError('picker-required'));}
  pickerPrevious() {return this.pickerSession?.page(true) ?? Promise.reject(selectionExecutionError('picker-required'));}
  createDestination(name: string) {return this.pickerSession?.create(name) ?? Promise.reject(selectionExecutionError('picker-required'));}
  chooseDestination(id: string) {return this.pickerSession?.choose(id) ?? Promise.reject(selectionExecutionError('picker-required'));}
  retryPicker() {return this.pickerSession?.retry() ?? Promise.reject(selectionExecutionError('picker-required'));}
  async viewDestination() {this.active(); const destination = this.state.picker?.destination; if (!destination || this.state.picker?.phase !== 'added') throw selectionExecutionError('destination-unconfirmed'); await this.options.routes.openDestination(destination);}
  closePicker() {this.cancelAction(); this.publish({picker: null});}
  async openEditor(target?: SelectionTarget) {
    return this.startEditor(target);
  }
  private async startEditor(target?: SelectionTarget, menuOffered = false) {
    const ticket = this.begin(target), members = this.members(ticket.target), summary = summarizeSelection(members.map(member => member.item));
    const rule = selectionActions(summary, this.environment).edit;
    if (!menuOffered && rule.state === 'explain') {this.publish({mixedEdit: true, editExplanation: rule.reason}); return;}
    if (!menuOffered && rule.state !== 'ready') throw selectionExecutionError('metadata-not-offered');
    this.editorEdits = []; this.editorReceipts = []; this.editorRetryTargets = undefined; this.publish({editor: {phase: 'loading', preview: null, draft: {lockEdited: true}, receipts: [], error: null}});
    try {
      this.editorOriginalTargets = metadataPreviewTargets(explicitIdentities(ticket.target));
      const request: BulkMetadataPreviewRequest = {targets: this.editorOriginalTargets};
      const preview = await fetchBulkMetadataPreview(this.options.api, request, ticket.context.scope.serverId, ticket.controller.signal); this.check(ticket);
      if (preview.viewerFence !== ticket.context.scope.privacyFence) throw selectionExecutionError('metadata-authority-changed');
      this.publish({editor: {...this.state.editor!, phase: 'ready', preview}});
    } catch (error) {if (this.current(ticket)) this.publish({editor: {...this.state.editor!, phase: 'error', error: errorInfo(error)}});}
  }
  setEditorDraft(draft: NonNullable<HomeSelectionSnapshot['editor']>['draft']) {
    this.active(); if (this.state.editor?.phase !== 'ready') throw selectionExecutionError('editor-not-ready');
    this.publish({editor: {...this.state.editor, draft: {...draft, lockEdited: draft.lockEdited ?? true}}});
  }
  async saveEditor() {
    const ticket = this.ticket, editor = this.state.editor; if (!ticket || !editor?.preview || !['ready', 'uncertain'].includes(editor.phase)) throw selectionExecutionError('editor-not-ready'); this.check(ticket);
    if (!this.editorEdits.length) {
      const targets = editor.preview.targets;
      for (let start = 0; start < targets.length; start += 200) {
        const body: BulkEdit = {operationId: await this.options.requestId(), targets: targets.slice(start, start + 200).map(({kind, id, expectedRevision}) => ({kind, id, expectedRevision})), ...editor.draft}; this.check(ticket);
        if (!validBulkEdit(body, editor.preview.schema)) throw selectionExecutionError('invalid-bulk-edit'); this.editorEdits.push({body, attempted: false});
      }
    }
    this.publish({editor: {...editor, phase: 'saving', error: null}});
    try {
      for (const edit of this.editorEdits) {
        if (edit.receipt) continue; this.check(ticket); edit.attempted = true;
        const raw = await this.options.api.request(routePath('post_metadata_bulk'), 'POST', edit.body, ticket.controller.signal); this.check(ticket); edit.receipt = validateBulkReceipt(raw, edit.body); this.editorReceipts.push(edit.receipt);
        this.publish({editor: {...this.state.editor!, receipts: [...this.editorReceipts]}});
      }
      this.publish({editor: {...this.state.editor!, phase: 'result'}});
    } catch (error) {if (this.current(ticket)) this.publish({editor: {...this.state.editor!, phase: 'uncertain', error: errorInfo(error)}});}
  }
  async retryEditor(draft = this.state.editor?.draft, failedTargetIds?: readonly string[]) {
    const ticket = this.ticket, editor = this.state.editor;
    const retryingPreview = editor?.phase === 'error' && this.editorRetryTargets !== undefined;
    if (!ticket || !editor || editor.phase !== 'result' && !retryingPreview || !draft) throw selectionExecutionError('editor-result-required'); this.check(ticket);
    const latest = new Map<string, boolean>();
    for (const receipt of this.editorReceipts) for (const result of receipt.results) latest.set(result.id, result.ok);
    const failed = new Set([...latest].filter(([, ok]) => !ok).map(([id]) => id));
    if (failedTargetIds !== undefined && (!failedTargetIds.length || new Set(failedTargetIds).size !== failedTargetIds.length || failedTargetIds.some(id => !failed.has(id)))) throw selectionExecutionError('invalid-editor-retry-targets');
    const retained = this.editorRetryTargets;
    if (retryingPreview && failedTargetIds !== undefined && (failedTargetIds.length !== retained!.length || failedTargetIds.some(id => !retained!.includes(id)))) throw selectionExecutionError('invalid-editor-retry-targets');
    const requested = new Set(failedTargetIds ?? (retryingPreview ? retained : [...failed]));
    const targets = this.editorOriginalTargets.filter(target => requested.has(target.id));
    if (!targets.length) throw selectionExecutionError('editor-has-no-failed-targets');
    this.editorRetryTargets = [...requested];
    this.publish({editor: {...editor, phase: 'loading', draft, error: null}});
    try {
      const preview = await fetchBulkMetadataPreview(this.options.api, {targets}, ticket.context.scope.serverId, ticket.controller.signal); this.check(ticket);
      if (preview.viewerFence !== ticket.context.scope.privacyFence) throw selectionExecutionError('metadata-authority-changed');
      this.editorEdits = [];
      this.editorRetryTargets = undefined;
      this.publish({editor: {...this.state.editor!, phase: 'ready', preview, draft}});
      await this.saveEditor();
    } catch (error) {if (this.current(ticket)) this.publish({editor: {...this.state.editor!, phase: 'error', error: errorInfo(error)}});}
  }
  reloadEditor() {if (this.state.editor?.phase === 'uncertain') return this.saveEditor(); if (this.state.editor?.phase === 'result' || this.state.editor?.phase === 'error' && this.editorRetryTargets) return this.retryEditor(); const target = this.ticket?.target; return this.openEditor(target);}
  closeEditor() {this.cancelAction(); this.editorEdits = []; this.editorReceipts = []; this.editorRetryTargets = undefined; this.publish({editor: null, mixedEdit: false, editExplanation: null});}
  async openDelete(target?: SelectionTarget) {
    return this.startDelete(target);
  }
  private async startDelete(target?: SelectionTarget, menuOffered = false) {
    const ticket = this.begin(target), identities = explicitIdentities(ticket.target);
    const libraries = new Set(identities.map(identity => 'libraryId' in identity ? identity.libraryId : ''));
    const summary = summarizeSelection(this.members(ticket.target).map(member => member.item)), rule = selectionActions(summary, this.environment).delete;
    if (!menuOffered && rule.state === 'hidden' || libraries.size !== 1 || libraries.has('')) throw selectionExecutionError('one-library-required');
    this.deleteBody = undefined;
    this.publish({deletion: {phase: identities.length > 200 ? 'limit' : 'loading', preview: null, confirmation: '', removeFiles: true, result: null, error: null}});
    if (identities.length > 200) return;
    if (!menuOffered && !summary.every.delete) throw selectionExecutionError('delete-not-offered');
    try {
      const raw = await this.options.api.request<{protocolVersion: string; serverId: string; result: unknown}>(routePath('post_admin_media_delete_preview'), 'POST', {itemIds: identities.map(identity => 'id' in identity ? identity.id : '')}, ticket.controller.signal); this.check(ticket);
      if (raw.protocolVersion !== '1.0' || raw.serverId !== ticket.context.scope.serverId) throw selectionExecutionError('invalid-delete-preview');
      const preview = parseDeletePreview(raw.result), expected = new Set(identities.map(identity => 'id' in identity ? identity.id : ''));
      if (preview.targets.length !== expected.size || preview.targets.some(item => !expected.delete(item.itemId) || !libraries.has(item.libraryId)) || expected.size) throw selectionExecutionError('invalid-delete-targets');
      this.publish({deletion: {...this.state.deletion!, phase: deletionAllowed(preview) ? 'ready' : 'blocked', preview}});
    } catch (error) {if (this.current(ticket)) this.publish({deletion: {...this.state.deletion!, phase: 'error', error: errorInfo(error)}});}
  }
  setDeleteConfirmation(confirmation: string) {if (this.state.deletion?.phase === 'ready') this.publish({deletion: {...this.state.deletion, confirmation}});}
  setDeleteRemoveFiles(removeFiles: boolean) {if (this.state.deletion?.phase === 'ready') this.publish({deletion: {...this.state.deletion, removeFiles}});}
  async confirmDelete() {
    const ticket = this.ticket, deletion = this.state.deletion, preview = deletion?.preview;
    if (!ticket || !deletion || !preview || !['ready', 'uncertain'].includes(deletion.phase) || !deletionAllowed(preview) || deletion.confirmation !== preview.confirmation) throw selectionExecutionError('delete-confirmation-required'); this.check(ticket);
    if (!this.deleteBody) {const operationId = await this.options.requestId(); this.check(ticket); this.deleteBody = Object.freeze({itemIds: preview.targets.map(item => item.itemId), expectedRevision: preview.revision, confirmation: deletion.confirmation, deleteFiles: deletion.removeFiles, operationId});}
    this.publish({deletion: {...deletion, phase: 'deleting', error: null}});
    try {
      const raw = await this.options.api.request<{protocolVersion: string; serverId: string; result: unknown}>(routePath('post_admin_media_delete'), 'POST', this.deleteBody, ticket.controller.signal); this.check(ticket);
      if (raw.protocolVersion !== '1.0' || raw.serverId !== ticket.context.scope.serverId) throw selectionExecutionError('invalid-delete-result');
      const result = parseDeleteResult(raw.result), expected = new Set(preview.targets.map(item => item.itemId));
      if (result.receipts.length !== expected.size || result.receipts.some(item => !expected.delete(item.itemId)) || expected.size) throw selectionExecutionError('invalid-delete-result');
      this.publish({deletion: {...this.state.deletion!, phase: 'result', result}});
    } catch (error) {if (this.current(ticket)) this.publish({deletion: {...this.state.deletion!, phase: 'uncertain', error: errorInfo(error)}});}
  }
  reloadDelete() {return this.openDelete(this.ticket?.target);}
  closeDelete() {this.cancelAction(); this.deleteBody = undefined; this.publish({deletion: null});}
  private closeDialogs() {this.cancelAction(); this.closeMenu(); this.publish({menu: null, picker: null, editor: null, deletion: null, rating: null, mixedEdit: false, editExplanation: null});}
  dispose() {if (this.disposed) return; this.cancelAction(); for (const cleanup of this.cleanups) cleanup(); this.selection.dispose(); this.windows.clear(); this.menuWindow = undefined; this.menuSource = undefined; this.held.clear(); this.operationMembers.clear(); this.disposed = true; this.state = Object.freeze({...this.state, displayMembers: [], menu: null, picker: null, editor: null, deletion: null, rating: null}); this.listeners.clear();}
}
