import {DetailService, readCreditPage, type DetailApi, type DetailScope, type PersonalIntent} from '../../../../../packages/client-core/src/detail';
import {ShowWorkspaceService, saveShowSettings, type ShowSettings, type ShowWorkspaceData} from '../../../../../packages/client-core/src/show-workspace';
import {getContainerState, setContainerState} from '../../../../../packages/client-core/src/container-state';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import type {NativeShellPlaybackAdapter} from '../../shell/playback';
import type {NativeStartArtworkOffer} from '../../platform/playback/start-presentation';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {NativeTitleMemory, type NativeTitleAnchor, type NativeTitleTab} from './title-memory';
import {NativeTitlePages} from './title-pages';
import {NativeTitleEpisodeDetails} from './title-episode';
import {NativeTitleCredits, type NativeTitleCreditsSnapshot} from './title-credits';
import {nativeRelatedRow, type NativeRelatedRow, type NativeRelatedRowSnapshot} from './title-related';

export type NativeTitleControllerOptions = Readonly<{
  entry: ContentEntry;
  api: DetailApi;
  scope: DetailScope;
  authority: ShellAuthority;
  current: () => ShellAuthority;
  subscribeCurrent: (listener: () => void) => () => void;
  requestId: () => Promise<string>;
  playback: Pick<NativeShellPlaybackAdapter, 'start'>;
  /** The exact host this page was rendered under: the layout's route revision, or
   * the playing session's fence. A retained page whose host has moved refuses its
   * settings, season and filter writes. */
  host?: () => NativeTitlePageHost | undefined;
  /** Fires when the route or the playback source changes, so a write in flight
   * from a page that has left is aborted on the wire. */
  subscribeHost?: (listener: () => void) => () => void;
}>;

/** Immutable identity of the page on screen, from the layout's own facts. */
export type NativeTitlePageHost = Readonly<{revision?: number; fence?: PlaybackFence}>;
/** One sheet's or control's page: the rendered workspace plus its exact host. */
export type NativeTitlePageLease = Readonly<{page: ShowWorkspaceData; host: NativeTitlePageHost}>;

/** Real lazy title/show services, retained independently of full-window/docked presentation. */
export class NativeTitleController {
  readonly detail: DetailService;
  readonly workspace: ShowWorkspaceService;
  readonly memory: NativeTitleMemory;
  readonly episode: NativeTitleEpisodeDetails;
  readonly entry: ContentEntry;
  private readonly pages = new NativeTitlePages();
  private ingested: unknown;
  private creditData?: object;
  private credits?: NativeTitleCredits;
  private related = new Map<string, NativeRelatedRow>();
  private creditReadHost?: NativeTitlePageHost;
  private creditRefresh = false;
  private readonly options: NativeTitleControllerOptions;
  private listeners = new Set<() => void>();
  private off: (() => void)[] = [];
  /** Owner-settings writes still on the wire, each with the lease it belongs to. */
  private saves = new Set<Readonly<{request: AbortController; lease: NativeTitlePageLease}>>();
  private readonly offHost: () => void;
  private retired = false;
  private snapshot: ReturnType<NativeTitleController['read']>;
  private opened = false;

  constructor(options: NativeTitleControllerOptions) {
    this.options = options; this.entry = options.entry;
    this.detail = new DetailService({api: options.api, scope: options.scope, requestId: options.requestId});
    this.workspace = new ShowWorkspaceService({api: options.api, scope: options.scope});
    this.memory = new NativeTitleMemory(['show', 'season', 'episode'].includes(this.entry.kind));
    this.episode = new NativeTitleEpisodeDetails({api: options.api, scope: options.scope, requestId: options.requestId,
      current: () => this.current() && !this.retired, play: (entry, target, startIntent) => this.options.playback.start({entry, target, startIntent})});
    this.snapshot = this.read();
    this.off = [this.detail.subscribe(this.changed), this.workspace.subscribe(this.changed), options.subscribeCurrent(() => {if (!this.current()) this.dispose();})];
    this.offHost = options.subscribeHost?.(() => {
      for (const save of this.saves) if (!this.hostOf(save.lease)) save.request.abort();
      if (this.creditReadHost && !this.sameHost(this.creditReadHost)) this.credits?.cancel();
    }) ?? (() => {});
  }
  private current(): boolean {return !this.retired && sameAuthority(this.options.authority, this.options.current());}
  /** The exact page and host a control was drawn under, captured once. */
  pageLease = (): NativeTitlePageLease | undefined => {
    const page = this.workspace.getSnapshot().data, host = this.options.host?.();
    return page && host ? Object.freeze({page, host}) : undefined;
  };
  private sameHost(expected: NativeTitlePageHost): boolean {
    const host = this.options.host?.();
    if (!host || host.revision !== expected.revision) return false;
    return host.fence === undefined ? expected.fence === undefined : !!expected.fence && sameFence(host.fence, expected.fence);
  }
  private hostOf(lease: NativeTitlePageLease): boolean {return this.sameHost(lease.host);}
  private onLease(lease: NativeTitlePageLease): boolean {
    return this.current() && this.hostOf(lease) && this.workspace.getSnapshot().data === lease.page;
  }
  private read() {
    const detail = this.retired ? undefined : this.detail.getSnapshot(), creditData = detail?.data ?? undefined;
    if (creditData !== this.creditData) {
      this.credits?.dispose(); this.credits = undefined; this.creditData = creditData ?? undefined;
      if (creditData) this.credits = new NativeTitleCredits({id: creditData.item.id, revision: creditData.revision,
        credits: creditData.metadata.credits, totals: creditData.metadata.creditTotals,
        current: () => this.current() && this.detail.getSnapshot().data === creditData,
        read: (group, cursor, signal) => readCreditPage(this.options.api, creditData.item.id, group, {cursor, limit: 37}, signal), changed: this.changed});
      for (const row of this.related.values()) row.dispose();
      this.related = new Map();
      // Each related row continues through the title's row route under this detail's fence and revision.
      if (creditData) for (const row of creditData.related?.rows ?? []) {
        const source = {libraryId: creditData.item.libraryId, itemId: creditData.item.id, kind: creditData.item.kind};
        const api = {request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
          if (!this.current() || this.detail.getSnapshot().data !== creditData) return Promise.reject(new DOMException('Title changed', 'AbortError'));
          return this.options.api.request<T>(path, method, body, signal);
        }};
        this.related.set(row.id, nativeRelatedRow(api, source, row, {viewerFence: creditData.scope.viewerFence, revision: creditData.revision}, this.changed));
      }
    }
    const workspace = this.retired ? undefined : this.workspace.getSnapshot(), data = workspace?.data;
    if (workspace?.phase === 'ready' && data && data !== this.ingested) {
      this.ingested = data;
      this.pages.ingest({key: JSON.stringify([data.show.id, data.selected.seasonId, data.selected.group, workspace.route?.playState ?? 'all']),
        fence: JSON.stringify([data.scope.serverId, data.scope.libraryId, data.scope.viewerFence, data.revision.catalog, data.revision.viewer]),
        episodeCursor: workspace.pagination.cursor, seasonCursor: workspace.seasonPagination.cursor, episodes: data.episodes.sections[0]?.entries ?? [], seasons: data.seasons});
    }
    const related: Record<string, NativeRelatedRowSnapshot> = {};
    for (const [id, row] of this.related) related[id] = row.getSnapshot();
    return Object.freeze({entry: this.entry, detail, credits: this.credits?.getSnapshot(), related: Object.freeze(related), workspace, ...this.pages.getSnapshot(), place: this.memory.getSnapshot(), retired: this.retired});
  }
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private changed = (): void => {
    if (!this.current()) {this.dispose(); return;}
    this.snapshot = this.read(); for (const listener of [...this.listeners]) listener();
  };
  open = async (): Promise<void> => {
    if (!this.current() || this.opened || !this.entry.libraryId) return;
    this.opened = true;
    const {kind, libraryId, id} = this.entry;
    const jobs: Promise<void>[] = [];
    if (kind === 'show' || kind === 'season' || kind === 'episode') {
      jobs.push(this.workspace.select({libraryId, ...(kind === 'show' ? {showId: id} : kind === 'season' ? {seasonId: id} : {episodeId: id})}).then(() => {
        if (!this.current()) return;
        const selected = this.workspace.getSnapshot().data?.selected;
        if (selected) this.memory.select({seasonId: selected.seasonId, group: selected.group ?? null});
        this.changed();
      }));
    }
    if (kind === 'movie' || kind === 'episode' || kind === 'extra') jobs.push(this.detail.select({libraryId, itemId: id}));
    await Promise.all(jobs);
  };
  tab = (tab: NativeTitleTab): void => {if (this.current()) {this.memory.select({tab}); this.changed();}};
  remember = (anchor: NativeTitleAnchor): void => {if (this.current()) {this.memory.remember(anchor); this.snapshot = this.read();}};
  /** A season pick belongs to the page it was drawn on: a replaced page refuses. */
  season = async (id: string, lease?: NativeTitlePageLease): Promise<void> => {
    const data = this.workspace.getSnapshot().data;
    if (!this.current() || !data || lease && !this.onLease(lease) || !this.snapshot.seasons.some(season => season.id === id)) return;
    await this.workspace.select({libraryId: data.scope.libraryId, showId: data.show.id, selectedSeasonId: id, playState: this.memory.getSnapshot().hideWatched ? 'unplayed' : 'all'});
    if (!this.current() || this.workspace.getSnapshot().data?.selected.seasonId !== id) return;
    this.memory.select({seasonId: id}); this.changed();
  };
  group = async (id: string): Promise<void> => {
    const data = this.workspace.getSnapshot().data;
    if (!this.current() || !data?.groups.some(group => group.id === id)) return;
    await this.workspace.select({libraryId: data.scope.libraryId, showId: data.show.id, group: id, playState: this.memory.getSnapshot().hideWatched ? 'unplayed' : 'all'});
    if (!this.current() || this.workspace.getSnapshot().data?.selected.group !== id) return;
    this.memory.select({group: id}); this.changed();
  };
  /** The whole-season filter, asked for by the page the switch was drawn on. */
  hideWatched = async (value: boolean, lease?: NativeTitlePageLease): Promise<void> => {
    const data = this.workspace.getSnapshot().data;
    if (!this.current() || !data || lease && !this.onLease(lease)) return;
    await this.workspace.select({libraryId: data.scope.libraryId, showId: data.show.id,
      ...(data.selected.seasonId ? {selectedSeasonId: data.selected.seasonId} : data.selected.group ? {group: data.selected.group} : {}), playState: value ? 'unplayed' : 'all'});
    if (!this.current() || this.workspace.getSnapshot().phase !== 'ready' || this.workspace.getSnapshot().route?.playState !== (value ? 'unplayed' : 'all')) return;
    this.memory.select({hideWatched: value}); this.changed();
  };
  /** The drawn season actions: play the season's real continuation, or mark it watched. */
  playSeason = async (seasonId: string): Promise<void> => {
    if (!this.current()) return;
    await this.season(seasonId);
    if (!this.current()) return;
    const data = this.workspace.getSnapshot().data;
    const target = data?.nextUp ?? data?.episodes.sections[0]?.entries[0];
    if (target) await this.play(target);
  };
  markSeason = async (seasonId: string, watched: boolean): Promise<void> => {
    if (!this.current()) return;
    const state = await getContainerState(this.options.api, 'season', seasonId);
    await setContainerState(this.options.api, 'season', seasonId, {expectedRevision: state.revision, watched});
    if (!this.current()) return;
    await this.workspace.refresh();
    this.changed();
  };
  /** A page boundary uses the exact credit snapshot it rendered and the current route/session host. */
  moreCredits = async (expected: NativeTitleCreditsSnapshot): Promise<void> => {
    const host = this.options.host?.(), owner = this.credits;
    if (!this.current() || this.creditRefresh || this.options.host && !host || !owner || owner.getSnapshot() !== expected) return;
    this.creditReadHost = host;
    const code = (expected.error as {code?: string} | undefined)?.code;
    if (['stale_continuation', 'cursor_expired', 'invalid_cursor', 'permission_changed'].includes(code ?? '')) {
      // A page from a new revision cannot be spliced into this row. Refresh the
      // actual Detail producer first, then use only its replacement cursor owner.
      this.creditRefresh = true;
      try {
        await this.detail.refresh();
        if (!this.current() || host && !this.sameHost(host) || this.credits === owner) return;
        if (this.credits) await this.credits.next(this.credits.getSnapshot());
      } finally {this.creditRefresh = false;}
      return;
    }
    await owner.next(expected);
  };
  /** A related row's page boundary, for the exact row snapshot it rendered. */
  moreRelated = (id: string, expected: NativeRelatedRowSnapshot): void => {if (this.current()) this.related.get(id)?.next(expected);};
  moreEpisodes = async (): Promise<void> => {if (this.current() && this.workspace.getSnapshot().phase === 'ready') await this.workspace.nextEpisodes();};
  moreSeasons = async (): Promise<void> => {if (this.current() && this.workspace.getSnapshot().phase === 'ready') await this.workspace.nextSeasons();};
  /** Owner show settings, bound to the exact page and host the sheet opened on.
   * A replaced page, a left route or session, or a retirement refuses the write;
   * a host that moves mid-write aborts it on the wire. */
  saveSettings = async (lease: NativeTitlePageLease, settings: ShowSettings): Promise<boolean> => {
    if (!this.onLease(lease)) return false;
    const data = lease.page;
    const request = new AbortController(), entry = Object.freeze({request, lease});
    this.saves.add(entry);
    const api = {request: <T,>(path: string, method?: string, body?: unknown) => this.options.api.request<T>(path, method, body, request.signal)};
    try {
      await saveShowSettings(api, data.scope.libraryId, data.show.id, settings);
      // Its page is still the one on screen: a refresh or replacement that happened
      // meanwhile is never overwritten or refreshed again from this write.
      if (!this.current() || request.signal.aborted || !this.hostOf(lease) || this.workspace.getSnapshot().data !== lease.page) return false;
      await this.workspace.refresh();
      // Its own refresh replaces the page, so the host is what must still hold.
      if (!this.current() || request.signal.aborted || !this.hostOf(lease)) return false;
      this.changed();
      return true;
    } finally {this.saves.delete(entry);}
  };
  play = async (entry = this.entry, fromStart = false, presentation?: NativeStartArtworkOffer): Promise<void> => {
    if (!this.current()) return;
    const data = this.detail.getSnapshot().data;
    const primary = data?.actions.find(action => action.enabled && action.playback && (fromStart ? ['start_over', 'play_from_start'].includes(action.id) : ['resume', 'play'].includes(action.id)));
    const target = entry.id === data?.item.id ? primary?.playback : entry.playback;
    if (fromStart && (!primary?.playback || entry.id !== data?.item.id)) return;
    // Shows play only the actual server-projected continuation. No guessed first episode.
    const actual = fromStart ? primary?.playback : target ?? (entry.id === this.entry.id ? this.workspace.getSnapshot().data?.nextUp?.playback ?? entry.playback : entry.playback);
    if (!actual || fromStart && entry.id !== data?.item.id) return;
    await this.options.playback.start({entry, target: actual, startIntent: fromStart ? 'start-over' : 'resume', ...(presentation ? {presentation} : {})});
  };
  personal = async (intent: PersonalIntent): Promise<void> => {if (this.current()) await this.detail.mutate(intent);};
  dispose = (): void => {
    if (this.retired) return;
    this.retired = true;
    this.offHost();
    for (const save of this.saves) save.request.abort();
    this.saves.clear();
    for (const off of this.off) off(); this.off = [];
    this.credits?.dispose(); this.credits = undefined;
    for (const row of this.related.values()) row.dispose();
    this.related = new Map();
    this.detail.dispose(); this.workspace.dispose(); this.pages.dispose(); this.episode.dispose(); this.snapshot = this.read();
    for (const listener of [...this.listeners]) listener(); this.listeners.clear();
  };
}
