import type {ReactNode, RefObject} from 'react';
import {Platform, type View} from 'react-native';
import {sameFence} from '../../../../../packages/player-core/src';
import {type DetailApi, type DetailProjection, type PersonalIntent} from '../../../../../packages/client-core/src/detail';
import {deletionAllowed, versionDeletionService, type VersionDeletePreview} from '../../../../../packages/client-core/src/administration';
import {SearchService} from '../../../../../packages/client-core/src/search';
import {canMergeTitles, mergeTitles, separateFileInfoOccurrence, SPLITTABLE_KINDS} from '../../../../../packages/client-core/src/title-identity';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';
import type {NativeScreenServices} from '../screen-services';
import type {NativeWatchCapture, NativeWatchMenuActions, NativeWatchMenuEntry, NativeVersionDestination, NativeVersionsOpening} from './factory';
import {nativeFileInfoFile, nativeFileInfoMergeGroups, nativeSplitCandidates, type NativeFileInfoOwner} from './file-info';
import type {NativeDiscTitlesController} from './disc-titles-controller';
import {nativeTogetherSession} from '../settings-live/together-session';

type FileOwner = NativeFileInfoOwner;
export type NativeWatchOverlay = Readonly<{
  capture: NativeWatchCapture;
  phase: 'menu' | 'file-pending' | 'file-info' | 'split-pick' | 'split' | 'delete' | 'merge' | 'versions-pending' | 'versions' | 'disc-titles';
  actions?: NativeWatchMenuActions;
  together?: Readonly<{groupId: string; viewer: string}>;
  destination?: NativeVersionDestination;
  /** The disc's own titles, opened by the owner's menu row. */
  disc?: NativeDiscTitlesController;
  anchorRef?: RefObject<View | null>;
  /** The captured title's own live detail projection: real personal actions. */
  detail?: DetailProjection;
  content?: ReactNode;
  owner?: FileOwner;
  fileId?: string;
  preview?: VersionDeletePreview;
  operationId?: string;
  confirmation: string;
  busy: boolean;
  error?: unknown;
  candidates: readonly ContentEntry[];
  searchReady?: boolean;
  other?: ContentEntry;
  keep?: string;
}>;

const cancelled = () => Object.assign(new Error('watch_capture_ended'), {name: 'AbortError'});

/** The presenter borrows the real captured member. Closing it retires requests;
 * navigation, authority and output replacement retire the same owned lifetime. */
export class NativeWatchPresenters {
  private value?: NativeWatchOverlay;
  private listeners = new Set<() => void>();
  private requests = new Set<AbortController>();
  private off: (() => void)[];
  private searchService?: SearchService;
  private menuEntry?: NativeWatchMenuEntry;
  private menuOff?: () => void;
  private disposed = false;
  private readonly context: NativeShellBootstrapContext;
  private readonly common: NativeScreenServices;
  constructor(context: NativeShellBootstrapContext, common: NativeScreenServices) {
    this.context = context; this.common = common;
    this.off = [context.navigation.subscribe(this.invalidate), context.subscribeAuthority(this.invalidate), context.playback.subscribe(this.invalidate)];
    if (Platform.isTV) this.off.push(nativeTogetherSession(context).subscribe(() => {if (this.value?.phase === 'menu' && !this.value.busy) this.patch(this.value, {});}));
  }
  getSnapshot = () => this.value;
  getMenuEntry = () => this.value?.phase === 'menu' ? this.value.capture.entry : undefined;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private emit() {for (const listener of [...this.listeners]) listener();}
  hostCurrent = (capture: NativeWatchCapture) => {
    const route = this.context.navigation.getSnapshot();
    return !this.disposed && this.context.current() && capture.current() && sameAuthority(capture.authority, this.context.authority())
      && sameAuthority(route.authority, capture.authority) && route.revision === capture.routeRevision;
  };
  current = (capture: NativeWatchCapture) => {
    const actual = this.context.playback.getSnapshot().fence;
    return this.hostCurrent(capture) && (capture.expectedFence ? sameFence(actual, capture.expectedFence) : !actual);
  };
  overlayCurrent = (value: NativeWatchOverlay) => value.phase === 'versions' && value.destination ? value.destination.isCurrent() : this.current(value.capture);
  private invalidate = () => {if (this.value && !this.overlayCurrent(this.value)) this.close();};
  private releaseMenuEntry = () => {this.menuOff?.(); this.menuOff = undefined; this.menuEntry?.dispose(); this.menuEntry = undefined;};
  private menuChanged = () => {
    const value = this.value, data = this.menuEntry?.current();
    if (!value || value.phase !== 'menu' || !data || data === value.detail || !this.current(value.capture)) return;
    this.replace({...value, detail: data});
  };
  close = () => {
    this.openingEpoch++;
    // File info's windows and leases go with the overlay that showed them.
    this.value?.owner?.session.retire();
    this.value?.disc?.dispose();
    this.value?.destination?.dispose();
    this.releaseMenuEntry();
    for (const request of this.requests) request.abort(); this.requests.clear();
    this.searchService?.dispose(); this.searchService = undefined;
    if (this.value) {this.value = undefined; this.emit();}
  };
  closeMenu = (value: NativeWatchOverlay) => {if (this.value === value && value.phase === 'menu') this.close();};
  private patch(value: NativeWatchOverlay, change: Partial<NativeWatchOverlay>): NativeWatchOverlay | undefined {
    if (this.value !== value || !this.current(value.capture)) return;
    this.value = Object.freeze({...value, ...change}); this.emit(); return this.value;
  }
  private openingEpoch = 0;
  private replace(value: NativeWatchOverlay) {this.openingEpoch++; this.value = Object.freeze(value); this.emit();}
  openMenu = (capture: NativeWatchCapture, actions: NativeWatchMenuActions, anchorRef?: RefObject<View | null>) => {
    this.close();
    if (!this.current(capture)) {actions.entry?.dispose(); return;}
    this.menuEntry = actions.entry;
    this.menuOff = actions.entry?.subscribe(this.menuChanged);
    const detail = actions.entry?.current();
    const room = Platform.isTV ? nativeTogetherSession(this.context).getSnapshot() : undefined;
    const together = room?.phase === 'live' && room.group ? {groupId: room.group.id, viewer: room.group.viewerMemberId} : undefined;
    this.replace({capture, actions, anchorRef, phase: 'menu', confirmation: '', busy: false, candidates: [], together, ...(detail ? {detail} : {})});
  };
  togetherActions = (value: NativeWatchOverlay) => {
    const state = nativeTogetherSession(this.context).getSnapshot(), group = state.phase === 'live' ? state.group : undefined;
    const valid = this.current(value.capture) && !value.busy && !!value.capture.entry.playback && ['movie', 'episode'].includes(value.capture.entry.kind)
      && group?.id === value.together?.groupId && group?.viewerMemberId === value.together?.viewer;
    return {queue: valid && group?.permissions.canManageQueue === true, watch: valid && group?.permissions.canManageQueue === true && group.permissions.canControl};
  };
  togetherEntry = async (value: NativeWatchOverlay, watch: boolean) => {
    const allowed = this.togetherActions(value);
    if (this.value !== value || !(watch ? allowed.watch : allowed.queue)) return;
    const target = value.capture.entry.playback!.itemId, pending = this.patch(value, {busy: true});if (!pending) return;
    const service = nativeTogetherSession(this.context);
    try {
      const done = watch ? await service.watch(target) : await service.queueAdd([target]);
      if (done && this.value === pending && this.current(value.capture)) this.close();
    } finally {if (this.value === pending) this.patch(pending, {busy: false});}
  };
  playEntry = (value: NativeWatchOverlay) => {
    const entry = value.actions?.entry;
    if (this.value !== value || value.phase !== 'menu' || !entry?.play || value.busy || !this.current(value.capture)
      || !value.detail?.actions.some(action => action.enabled && action.playback && action.id !== 'start_over')) return;
    this.close();
    void entry.play().catch(error => {if (!this.disposed && this.context.current()) this.common.reportError(error, 'detail');});
  };
  personal = async (value: NativeWatchOverlay, intent: PersonalIntent) => {
    const entry = value.actions?.entry, live = this.value;
    if (!entry || live?.phase !== 'menu' || live.busy || live !== value) return;
    const pending = this.patch(value, {busy: true, error: undefined}); if (!pending) return;
    try {await entry.mutate(intent);}
    catch (error) {if (this.value === pending && this.current(value.capture)) this.common.reportError(error, 'detail');}
    finally {if (this.value === pending) this.patch(pending, {busy: false});}
  };
  startRadio = (value: NativeWatchOverlay) => {
    const entry = value.actions?.entry;
    if (!entry?.startRadio || value.busy || !this.current(value.capture)) return;
    this.close();
    void entry.startRadio().catch(error => {if (!this.disposed && this.context.current()) this.common.reportError(error, 'detail');});
  };
  enqueue = async (value: NativeWatchOverlay, placement: 'next' | 'end') => {
    const entry = value.actions?.entry;
    if (!entry?.enqueue || !this.current(value.capture)) return;
    try {await entry.enqueue(placement);}
    catch (error) {if (this.context.current()) this.common.reportError(error, 'detail');}
  };
  addToPlaylist = (value: NativeWatchOverlay) => {
    const entry = value.actions?.entry;
    if (!entry?.addToPlaylist || value.busy || !this.current(value.capture)) return;
    this.close(); entry.addToPlaylist();
  };
  goToArtist = (value: NativeWatchOverlay) => {
    const entry = value.actions?.entry;
    if (!entry?.goToArtist || value.busy || !this.current(value.capture)) return;
    this.close(); entry.goToArtist();
  };
  selectEntry = (value: NativeWatchOverlay) => {
    const select = value.actions?.select;
    if (!select || this.value !== value || value.phase !== 'menu' || value.busy || !this.current(value.capture)) return;
    try {select();}
    finally {if (this.value === value) this.close();}
  };
  extraEntry = (value: NativeWatchOverlay, id: string) => {
    const action = value.actions?.extraActions?.find(item => item.id === id);
    if (!action || action.disabled || this.value !== value || value.phase !== 'menu' || value.busy || !this.current(value.capture)) return;
    this.close();
    action.onSelect();
  };
  showDetails = (value: NativeWatchOverlay) => {
    const entry = value.actions?.entry;
    if (!entry?.details || value.busy || !this.current(value.capture)) return;
    this.close(); entry.details();
  };
  /** Called where Versions is dispatched, before any inventory is read. The ticket
   * dies as soon as the presenter presents anything else or closes. */
  beginVersions = (capture: NativeWatchCapture): NativeVersionsOpening | undefined => {
    if (!this.current(capture)) return undefined;
    // A fresh reservation per opening, taken synchronously: two entries never share
    // an epoch, so an older answer cannot win over a newer opening. An unrelated
    // overlay already presented refuses instead of being replaced by this ticket.
    if (this.value && (this.value.capture !== capture || this.value.phase !== 'versions-pending')) return undefined;
    const epoch = ++this.openingEpoch, owner = this.value;
    return Object.freeze({capture, live: () => this.openingEpoch === epoch && this.value === owner && this.current(capture)});
  };
  differentVersion = async (value: NativeWatchOverlay) => {
    if (!value.actions?.differentVersion) return;
    const pending = this.patch(value, {phase: 'versions-pending', busy: true});
    if (!pending) return;
    this.releaseMenuEntry();
    try {await value.actions.differentVersion();}
    catch (error) {if (this.value === pending && this.current(pending.capture)) this.common.reportError(error, 'detail');}
    // Only this opening's own pending overlay is closed here. An adopted chooser
    // replaced it, and any newer overlay is not this one to close.
    finally {if (this.value === pending) this.close();}
  };
  /** The owner's Titles row: one physical version's own titles, never a synthetic
   * alternate version of the film. It refuses unless the menu that drew the row is
   * still the current one, owns the controller's identity, and disposes any
   * predecessor it replaces. */
  openDiscTitles = (capture: NativeWatchCapture, dispatch: () => void, create: (opening: () => boolean) => NativeDiscTitlesController) => {
    const value = this.value;
    if (!this.current(capture) || !value || value.phase !== 'menu' || value.capture !== capture || value.actions?.discTitles !== dispatch) return;
    let controller: NativeDiscTitlesController | undefined;
    const opening = () => this.current(capture) && this.value?.disc === controller;
    controller = create(opening);
    this.value?.disc?.dispose();
    this.replace({...value, confirmation: '', candidates: [], phase: 'disc-titles', disc: controller, busy: false});
  };
  /** A delayed close from a sheet that is no longer the presented one does nothing. */
  closeDisc = (value: NativeWatchOverlay) => {if (this.value === value) this.close();};
  openVersions = (capture: NativeWatchCapture, destination: NativeVersionDestination, opening: NativeVersionsOpening) => {
    // The ticket is the permission, whether a More menu or the failed output's own
    // entry began it. A closed or replaced sheet retires it, and the route being
    // current is not enough.
    if (!opening.live() || opening.capture !== capture || !destination.isCurrent()
      || this.value && (this.value.capture !== capture || this.value.phase !== 'versions-pending')) {destination.dispose(); return;}
    // Install the destination's own lifetime before the emitting replace: a listener
    // that renders the new value must already see a presented chooser, not a spent
    // ticket with no predicate to fall back on.
    destination.present(() => this.value?.destination === destination && this.value.phase === 'versions' && this.value.capture === capture);
    this.replace({confirmation: '', candidates: [], ...this.value, capture, phase: 'versions', destination, busy: false});
  };
  /** The sheet's own row and close: a retained callback from a chooser that is no
   * longer presented cannot choose or close its successor. */
  closeVersions = (value: NativeWatchOverlay) => {if (this.value === value) this.close();};
  chooseVersion = async (value: NativeWatchOverlay, versionId: string) => {
    if (this.value !== value) return;
    const destination = value.destination;
    if (value.phase !== 'versions' || !destination || value.busy || !destination.isCurrent() || !destination.rows.some(row => row.id === versionId)) return;
    this.replace({...value, busy: true});
    try {
      // A disc title that is no longer ready, or whose facts moved, is not played:
      // the sheet stays open instead of closing on a choice that did nothing.
      const started = await destination.choose(versionId);
      if (this.value?.destination !== destination) return;
      if (started) this.close(); else this.replace({...this.value, busy: false});
    } catch (error) {
      if (this.value?.destination === destination) {
        if (!destination.isCurrent()) this.close();
        else {this.replace({...this.value, busy: false}); this.common.reportError(error, 'detail');}
      }
    }
  };
  fileInfo = async (value: NativeWatchOverlay) => {
    if (!value.actions || !this.patch(value, {phase: 'file-pending', busy: true})) return;
    this.releaseMenuEntry();
    try {await value.actions.fileInfo(value.detail);}
    catch (error) {if (this.value?.capture === value.capture && this.current(value.capture)) {this.close(); this.common.reportError(error, 'detail');}}
  };
  openFileInfo = (capture: NativeWatchCapture, content: ReactNode, owner: FileOwner) => {
    if (!this.current(capture) || this.value?.capture !== capture || this.value.phase !== 'file-pending') {owner.session.retire(); return;}
    this.replace({...this.value, phase: 'file-info', content, owner, busy: false});
  };
  /** A stale opening cannot be continued: retire it, refresh the title, and read File info again
   * from the newest accepted Detail. */
  reopenFileInfo = async (capture: NativeWatchCapture) => {
    const value = this.value, owner = value?.owner;
    if (!value || value.capture !== capture || value.phase !== 'file-info' || !owner || !value.actions || !this.current(capture)) return;
    owner.session.retire();
    const pending = this.patch(value, {phase: 'file-pending', busy: true, owner: undefined, content: undefined}); if (!pending) return;
    try {await owner.changed(); if (this.value === pending) await pending.actions!.fileInfo(pending.detail);}
    catch (error) {if (this.value?.capture === capture && this.current(capture)) {this.close(); this.common.reportError(error, 'detail');}}
  };
  private async run(value: NativeWatchOverlay, action: (api: DetailApi, signal: AbortSignal, check: () => void) => Promise<void>) {
    const pending = this.patch(value, {busy: true, error: undefined}); if (!pending) return;
    const request = new AbortController(); this.requests.add(request);
    const current = () => !request.signal.aborted && this.value === pending && this.current(pending.capture);
    const check = () => {if (!current()) throw cancelled();};
    // This is the same authenticated API, with the presenter lifetime carried
    // into every request and checked after response parsing, before mutation.
    const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      check(); if (signal?.aborted) throw cancelled();
      const result = await this.context.api.request<T>(path, method, body, request.signal); check(); return result;
    }};
    try {await action(api, request.signal, check);}
    catch (error) {if (current()) this.patch(pending, {busy: false, error});}
    finally {this.requests.delete(request);}
  }
  /** A File info command names the exact occurrence of the opening it was chosen from. The
   * server rechecks owner, custody and selection live and refuses a stale opening (409) first. */
  private proof(value: NativeWatchOverlay, occurrence: string) {
    const owner = value.owner;
    if (!owner || !this.current(value.capture)) throw cancelled();
    return Object.freeze({opening: owner.session.opening.token, occurrence});
  }
  manageFile = (capture: NativeWatchCapture, action: 'delete-version' | 'split-versions' | 'merge-title', fileId?: string) => {
    const value = this.value, owner = value?.owner;
    if (!value || value.capture !== capture || value.phase !== 'file-info' || !owner || !this.current(capture)) return;
    const snapshot = owner.session.getSnapshot(), manage = snapshot.surface?.manage;
    if (action === 'delete-version' && manage?.delete && fileId && nativeFileInfoFile(snapshot, fileId)) {
      const next = this.patch(value, {phase: 'delete', fileId, confirmation: '', error: undefined, preview: undefined, operationId: undefined});
      if (next) void this.previewDelete(next);
    } else if (action === 'split-versions' && manage?.split && snapshot.surface?.canSplit && SPLITTABLE_KINDS.includes(owner.kind)) {
      this.patch(value, {phase: 'split-pick', fileId: undefined, error: undefined});
    } else if (action === 'merge-title' && manage?.merge && nativeFileInfoMergeGroups[capture.entry.kind]) {
      this.patch(value, {phase: 'merge', other: undefined, keep: capture.entry.id, candidates: [], error: undefined});
    }
  };
  backToInfo = () => {const value = this.value; if (value && !value.busy && value.owner) {
    this.searchService?.dispose(); this.searchService = undefined;
    this.patch(value, {phase: 'file-info', error: undefined});
  }};
  /** `occurrence` is one of the versions that is one file on its own (nativeSplitCandidates). */
  chooseSplit = (occurrence: string) => {
    const value = this.value, owner = value?.owner;
    if (value?.phase === 'split-pick' && owner && nativeSplitCandidates(owner.session.getSnapshot()).some(candidate => candidate.occurrence === occurrence)) this.patch(value, {phase: 'split', fileId: occurrence, error: undefined});
  };
  split = async () => {
    const value = this.value, entry = value?.capture.entry;
    if (value?.phase !== 'split' || !value.fileId || !entry?.libraryId || value.busy) return;
    await this.run(value, async (api, signal, check) => {
      await separateFileInfoOccurrence(api, entry.id, this.proof(value, value.fileId!));
      await value.owner!.changed(); check(); this.close();
    });
  };
  previewDelete = async (value = this.value) => {
    if (!value || value.phase !== 'delete' || !value.fileId || value.busy) return;
    await this.run(value, async api => {
      const listed = value.owner && nativeFileInfoFile(value.owner.session.getSnapshot(), value.fileId!);
      if (!listed) throw Object.assign(new Error('file_not_listed'), {code: 'file_not_listed'});
      const preview = await versionDeletionService(api.request).preview(listed.file.itemId, listed.file.id, this.proof(value, listed.file.occurrence));
      if (preview.assetId !== listed.file.id || preview.targets.length !== 1 || preview.targets[0]?.itemId !== listed.file.itemId) throw Object.assign(new Error('file_changed'), {code: 'file_changed'});
      const operationId = value.operationId ?? await this.common.requestId();
      const pending = this.value;
      if (pending?.capture === value.capture && this.current(value.capture)) this.patch(pending, {preview, operationId, busy: false});
    });
  };
  confirmation = (confirmation: string) => {const value = this.value; if (value?.phase === 'delete' && !value.busy) this.patch(value, {confirmation});};
  deleteVersion = async () => {
    const value = this.value;
    if (value?.phase !== 'delete' || !value.preview || !value.operationId || value.busy || !deletionAllowed(value.preview) || value.confirmation.trim() !== value.preview.confirmation) return;
    await this.run(value, async (api, signal, check) => {
      const result = await versionDeletionService(api.request).remove(value.preview!, value.confirmation.trim(), value.operationId!, this.proof(value, value.fileId!));
      const kept = result.receipts.find(receipt => !receipt.removed);
      if (kept) throw Object.assign(new Error('file_not_removed'), {code: kept.code === 'file_changed' ? 'file_changed' : 'file_not_removed'});
      await value.owner!.changed(); check(); this.close();
    });
  };
  search = async (words: string) => {
    const value = this.value, viewer = this.context.session()?.viewer, group = value && nativeFileInfoMergeGroups[value.capture.entry.kind];
    if (value?.phase !== 'merge' || !viewer || !group || value.busy || !this.current(value.capture)) return;
    if (!this.searchService) this.searchService = new SearchService({api: this.context.api, scope: {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])}, pageSize: 12});
    const service = this.searchService;
    if (!words.trim()) {service.cancel(); this.patch(value, {candidates: [], searchReady: false, error: undefined}); return;}
    const pending = this.patch(value, {error: undefined, other: undefined, searchReady: false}); if (!pending) return;
    try {
      await service.select(words.trim(), {group, libraryIds: [value.capture.entry.libraryId!], record: false});
      if (this.searchService !== service || this.value !== pending || !this.current(value.capture)) return;
      const snapshot = service.getSnapshot();
      if (snapshot.error) throw snapshot.error;
      const candidates = snapshot.groups.flatMap(result => result.items).filter((entry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel')
        .filter(entry => canMergeTitles(value.capture.entry, entry));
      this.patch(pending, {candidates, searchReady: true});
    } catch (error) {if (this.value === pending) this.patch(pending, {error});}
  };
  chooseMerge = (other: ContentEntry) => {const value = this.value; if (value?.phase === 'merge' && value.candidates.includes(other)) this.patch(value, {other, keep: value.capture.entry.id});};
  keep = (id: string) => {const value = this.value; if (value?.phase === 'merge' && !value.busy && [value.capture.entry.id, value.other?.id].includes(id)) this.patch(value, {keep: id});};
  merge = async () => {
    const value = this.value, other = value?.other;
    if (value?.phase !== 'merge' || !other || value.busy || !canMergeTitles(value.capture.entry, other)) return;
    const from = value.keep === other.id ? value.capture.entry : other, into = value.keep === other.id ? other : value.capture.entry;
    await this.run(value, async (api, signal, check) => {
      await mergeTitles(api, from.id, into.id);
      await value.owner!.changed(); check(); this.close();
      if (into === other) this.common.navigate(other);
    });
  };
  dispose = () => {if (this.disposed) return; this.close(); this.disposed = true; for (const off of this.off) off(); this.off = []; this.listeners.clear();};
}
