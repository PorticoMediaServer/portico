import type {DiscMediaFacts} from './disc-media-facts.ts';
import type {Credit} from '../../contracts/src/next/api.ts';
import {entryFromItem,readItem,readItemCredits,setItemState,type Item,type ItemState,type ItemStateChange} from './items.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {parseLocalBook,parseLocalAudioPolicy,type LocalAudioPolicy,type LocalBookMetadata} from "./music-metadata.ts";
/** Server-authored detail and explicit personal-state commands; no playback or ranking owner. */
import type { MediaItem } from './index.ts';
import {validateRelatedMovies,type RelatedMovieProjection} from './related-movies.ts';
import {parseSegmentMarkers,type SegmentMarker} from './segment-markers.ts';
import {validateContentEntry,type ContentEntry} from './library-content.ts';
import {firstOfEach,keepReadable,noteSkipped} from './tolerant.ts';
import {QueryObserver} from '@tanstack/query-core';
import type {ViewerQueries} from './query/viewer-queries.ts';
import type {ViewerQueryOptions} from './query/react.ts';
import {queryKeys} from './query/keys.ts';
import {dependsOn, personalChangedHere, titlesWithPersonalState} from './freshness-hub.ts';
import {linkAbortSignals} from './abort-lifetime.ts';
export type DetailExtraType = 'trailer' | 'featurette' | 'deleted_scene' | 'behind_the_scenes' | 'interview' | 'scene' | 'short' | 'other';
export type DetailExtra = Readonly<{ type: DetailExtraType; label: string; items: readonly ContentEntry[] }>;
export const detailExtraTypes: readonly DetailExtraType[] = ['trailer','featurette','deleted_scene','behind_the_scenes','interview','scene','short','other'];
export type DetailScope = Readonly<{ serverId: string; viewerId: string }>;
export type DetailTarget = Readonly<{ libraryId: string; itemId: string }>;
export interface DetailApi { request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> }
/** A person's own state on a title. `rating` is out of ten, or null for none. */
export type PersonalState = Readonly<{ watchlisted:boolean;favorite:boolean;rating:number|null;watched:boolean;progressSeconds:number;lastPlayedAt:string }>;
export type DetailAction = Readonly<{ id: string; labelKey: string; enabled: boolean; min?: number; max?: number; step?: number; playback?: Readonly<{ itemId: string; startSeconds: number }> }>;
export type DetailMetadata = Readonly<{
  sources?: readonly Readonly<{provider:string;sourceUrl:string;observedAt:string}>[];
  attributions?: readonly string[];
  status: 'available' | 'unavailable';
  ratings: readonly Readonly<{ provider: string; value: number; max: number; votes?: number; sourceUrl: string; observedAt: string }>[];
  genres: readonly Readonly<{ id: string; name: string; provider: string }>[];
  /** The cast's first page and the key crew; `creditTotals` counts both groups, and parseCreditPage reads the rest. */
  credits: readonly DetailCredit[];
  creditTotals?: Readonly<{ cast: number; crew: number }>;
}>;
export type DetailCredit = Readonly<{ id: string; name: string; role: string; department: string; provider: string; portraitUrl?:string; personId?:string}>;
/** One page of a title's cast or crew (GET /v1/items/{id}/credits). */
export type CreditPage = Readonly<{ group: 'cast' | 'crew'; credits: readonly DetailCredit[]; total: number; nextCursor?: string }>;
export type DetailEpisodeInfo = Readonly<{ showId: string; showTitle: string; seasonId?: string; /** When it first aired (YYYY-MM-DD), when known. */ airDate?: string; seasonNumber?: number | null; numbering: string; number: number; localIdentityStatus: string; providerMatchStatus: string; orderingBasis: string; sourceBoundary: string }>;
export type DetailSongInfo = Readonly<{ albumId: string; albumTitle: string; albumArtist: string; artist: string; discNumber?: number | null; trackNumber?: number | null; providerMatchStatus: string; localMetadataIssue?: string; providerTitle?:string;providerArtist?:string;providerRecordingId?:string;providerReleaseId?:string;providerReleaseGroupId?:string;providerTrackId?:string;providerReleaseStatus?:string;providerObservedAt?:string }>;
export type DetailBookFileInfo = Readonly<{ bookId: string; bookTitle: string; discNumber?: number | null; partNumber?: number | null; sourceBoundary: string; localMetadataIssue?: string;localMetadata?:LocalBookMetadata;localPolicy?:LocalAudioPolicy }>;
export type DetailItem = MediaItem & { /** An episode's own still, never inherited. */ stillUrl?: string; addedAt?: string | null; episode?: DetailEpisodeInfo; song?: DetailSongInfo; bookFile?: DetailBookFileInfo };
/** The title's own facts (the editor's General fields): the facts line and the Details section. */
export type TitleFacts = Readonly<{ contentRating?: string; studio?: string; network?: string; tagline?: string; releaseDate?: string; originalTitle?: string; country?: string; originalLanguage?: string; edition?: string }>;
/** One audio or subtitle track of a file. `external` is a subtitle file beside the media. */
export type TitleFileTrack = Readonly<{ language?: string; codec: string; title?: string; channels?: number; channelLayout?: string; objectAudio?: string; bitRate?: number; default?: boolean; forced?: boolean; external?: boolean }>;
/** One file returned by lazy File info. `path` is sent to owners only. */
export type TitleFile = Readonly<{
  disc?: DiscMediaFacts;
  id: string; itemId?:string; path?: string; size: number; container: string; duration: number; bitRate?: number; available: boolean;
  /** 0 for a whole title. Files with the same part are versions of one another; the first listed is played when none is chosen. */
  partIndex?: number;
  /** The version's edition label, where its name or local metadata gave one. */
  edition?: string;
  video?: Readonly<{ codec: string; width: number; height: number; dynamicRange?: string; hdr10Plus?: boolean; frameRate?: number; bitDepth?: number; bitRate?: number }>;
  audio: readonly TitleFileTrack[]; subtitles: readonly TitleFileTrack[];
}>;
export type DetailProjection = Readonly<{
  facts?: TitleFacts;
  collections?: readonly Readonly<{id:string;name:string;memberCount:number;position:number;yearFrom?:number;yearTo?:number;posterUrls:readonly string[]}>[];
  episodeDetails?: Readonly<{contentRating?:string;previous?:Readonly<{id:string;number:number;title:string}>;next?:Readonly<{id:string;number:number;title:string}>}>;
  languages: Readonly<{audio: readonly string[]; subtitles: readonly string[]}>;
  /** When the title's last file went from disk (RFC 3339). Present only while the title has no file: it is shown, cannot be played, and comes back when a file that is the same title appears. */
  missingSince?: string;
  scope: Readonly<{ serverId: string; libraryId: string; itemId: string }>;
  /** Opaque opening for this exact Detail's File info lanes; present once the title is ready. */
  fileInfoOpening?: string;
  item: Readonly<DetailItem>;
  personal: PersonalState;
  actions: readonly DetailAction[];
  metadata: DetailMetadata;
  markers: readonly SegmentMarker[];
  related?:RelatedMovieProjection;
  extras?:readonly DetailExtra[];
}>;
export type PersonalIntent = Readonly<{ action: 'watchlist' | 'favorite' | 'watched'; value: boolean } | { action: 'rating'; value: number | null }>;
export type DetailError = Readonly<{ code: string; message: string; retryable: boolean }>;
export type DetailSnapshot = Readonly<{
  generation: number; scope: DetailScope; target: DetailTarget | null;
  phase: 'idle' | 'loading' | 'ready' | 'error'; data: DetailProjection | null; error: DetailError | null;
  pending: readonly Readonly<{ intent: PersonalIntent; phase: 'queued' | 'sending' | 'retry-required' }>[];
  mutationError: DetailError | null;
}>;
type Command = { intent: PersonalIntent; phase: 'queued' | 'sending' | 'retry-required'; promise: Promise<void>; resolve: () => void };
const obj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => str(v, 256) && v.length > 0;
const integer = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;
const rating = (v: unknown): v is number | null => v === null || typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 10 && Number.isInteger(v);
class Failure extends Error { code: string; retryable: boolean; constructor(code: string, message: string, retryable = false) { super(message); this.code = code; this.retryable = retryable; } }
function invalid(): never { throw new Failure('invalid_detail', unreadableServerResponse); }
function checkScope(value: DetailScope): DetailScope {
  if (!obj(value) || !id(value.serverId) || !str(value.viewerId, 1024) || !value.viewerId) throw new Error('A bound detail viewer/server scope is required.');
  return Object.freeze({ serverId: value.serverId, viewerId: value.viewerId });
}
function checkTarget(value: DetailTarget): DetailTarget {
  if (!obj(value) || !id(value.libraryId) || !id(value.itemId)) throw new Error('A library and item are required.');
  return Object.freeze({ libraryId: value.libraryId, itemId: value.itemId });
}
function checkIntent(value:PersonalIntent):PersonalIntent {
 if(!obj(value))throw new Error('Invalid personal-state intent.');
 if(!((['watchlist','favorite','watched'].includes(value.action)&&typeof value.value==='boolean')||(value.action==='rating'&&rating(value.value))))throw new Error('Invalid personal-state intent.');
 return Object.freeze({...value});
}
const seconds = (ms: number | undefined): number => ms ? Math.round(ms / 1000) : 0;
const castRoles = ['actor', 'guest'];
const credit = (row: Credit, index: number): DetailCredit => Object.freeze({id: row.personId + ':' + row.role + ':' + index, name: row.name, role: row.character || row.role,
  department: castRoles.includes(row.role) ? 'cast' : row.role, provider: '', ...(row.image ? {portraitUrl: row.image} : {}), personId: row.personId});
export const personalState = (state: ItemState): PersonalState => Object.freeze({watchlisted: state.inList, favorite: state.favorite, rating: state.rating ?? null, watched: state.watched,
  progressSeconds: seconds(state.positionMs), lastPlayedAt: state.lastPlayedAt ?? ''});

/** A title's page from the server's item: what is known about it, the person's own state, and the actions the page offers. A fact the title lacks is left out. */
export function detailProjection(item: Item, scope: DetailScope): DetailProjection {
  if (!id(item.id) || !id(item.libraryId) || !id(item.kind)) invalid();
  const card = entryFromItem(item), kind = card?.kind ?? item.kind, state = item.state, plays = !item.counts;
  const media: DetailItem = {id: item.id, libraryId: item.libraryId, title: item.title, kind, progressSeconds: seconds(state.positionMs), available: item.available !== false,
    ...(item.durationMs ? {duration: seconds(item.durationMs)} : {}), ...(item.summary ? {overview: item.summary} : {}), ...(item.year ? {year: item.year} : {}), addedAt: item.addedAt || null,
    ...(card?.posterUrl ? {posterUrl: card.posterUrl} : {}), ...(card?.backdropUrl ? {backdropUrl: card.backdropUrl} : {}), ...(card?.stillUrl ? {stillUrl: card.stillUrl} : {})};
  if (item.kind === 'episode' && item.root) media.episode = Object.freeze({showId: item.root.id, showTitle: item.root.title, ...(item.parent ? {seasonId: item.parent.id} : {}), ...(item.releaseDate ? {airDate: item.releaseDate} : {}),
    seasonNumber: item.parentIndex ?? null, numbering: '', number: item.index ?? 0, localIdentityStatus: '', providerMatchStatus: '', orderingBasis: '', sourceBoundary: ''});
  if (item.kind === 'track' && item.parent) media.song = Object.freeze({albumId: item.parent.id, albumTitle: item.parent.title, albumArtist: item.root?.title ?? '', artist: item.root?.title ?? '',
    discNumber: item.parentIndex ?? null, trackNumber: item.index ?? null, providerMatchStatus: ''});
  const tag = (kind: string) => item.tags?.find(each => each.kind === kind)?.name;
  const facts: TitleFacts = Object.freeze(Object.fromEntries(Object.entries({contentRating: item.contentRating, studio: tag('studio'), network: tag('network'), tagline: item.tagline, releaseDate: item.releaseDate,
    originalTitle: item.originalTitle, edition: item.edition}).filter(([, value]) => !!value)));
  const position = seconds(state.positionMs), playable = plays && media.available;
  const actions: DetailAction[] = [{id: 'watchlist', labelKey: 'action.watchlist', enabled: true}, {id: 'favorite', labelKey: 'action.favorite', enabled: true}, {id: 'rating', labelKey: 'action.rating', enabled: true, min: 0, max: 10, step: 1}];
  if (plays && item.kind !== 'track') actions.push({id: 'watched', labelKey: 'action.watched', enabled: true});
  if (playable && position > 0) actions.push({id: 'resume', labelKey: 'action.resume', enabled: true, playback: {itemId: item.id, startSeconds: position}}, {id: 'start_over', labelKey: 'action.start_over', enabled: true, playback: {itemId: item.id, startSeconds: 0}});
  else if (playable) actions.push({id: 'play', labelKey: 'action.play', enabled: true, playback: {itemId: item.id, startSeconds: 0}});
  const all = (item.credits ?? []).map(credit), cast = all.filter(each => each.department === 'cast').length;
  const streams = (item.versions ?? []).flatMap(version => version.parts.flatMap(part => part.streams)), languages = (kind: string) => Object.freeze([...new Set(streams.filter(stream => stream.kind === kind && stream.language).map(stream => stream.language!))]);
  const ratings = [...(item.ratings?.community ? [{provider: 'community', value: item.ratings.community, max: 10, sourceUrl: '', observedAt: ''}] : []), ...(item.ratings?.critic ? [{provider: 'critic', value: item.ratings.critic, max: 10, sourceUrl: '', observedAt: ''}] : [])];
  const genres = (item.tags ?? []).filter(each => each.kind === 'genre').map(each => Object.freeze({id: each.id, name: each.name, provider: ''}));
  const markers = (item.versions?.[0]?.markers ?? []).flatMap((marker, index) => { try { return parseSegmentMarkers([{id: marker.kind + ':' + index, kind: marker.kind, startSeconds: marker.startMs / 1000, endSeconds: marker.endMs / 1000, ...(marker.title ? {title: marker.title} : {})}], media.duration); } catch { return []; } });
  return Object.freeze({...(Object.keys(facts).length ? {facts} : {}), languages: Object.freeze({audio: languages('audio'), subtitles: languages('subtitle')}), markers,
    scope: Object.freeze({serverId: scope.serverId, libraryId: item.libraryId, itemId: item.id}), item: Object.freeze(media), personal: personalState(state), actions: Object.freeze(actions.map(action => Object.freeze(action))),
    metadata: Object.freeze({status: ratings.length || genres.length || all.length ? 'available' : 'unavailable', ratings: Object.freeze(ratings), genres: Object.freeze(genres), credits: Object.freeze(all),
      creditTotals: Object.freeze({cast: item.creditTotal === undefined ? cast : Math.max(cast, item.creditTotal - (all.length - cast)), crew: all.length - cast})})});
}
function errorInfo(error: unknown): DetailError {
  const e = error as { code?: unknown; retryable?: unknown; status?:number } | null;
  return Object.freeze({ code: id(e?.code) ? e.code : 'request_failed', message: error instanceof Error ? error.message : 'The detail request failed.', retryable: e?.status!==401&&e?.status!==403&&(e?.retryable===true||error instanceof TypeError||(e?.status??0)>=500||e?.status===408||e?.status===429) });
}

/** Codes that are the title's answer, not a failure to read it: the page shows them, with nothing kept. */
const FINAL_DETAIL_CODES = ['unauthorized','forbidden','not_found','detail_scope_changed','invalid_detail'];
const DETAIL_DEADLINE_MS = 15000;

/**
 * The title's page as the server answers it, for any owner that needs it (the page, its route
 * guard, a card menu, a picker): one keyed document in the viewer's cache, read once however many
 * ask, kept current by the hub. Its library is the one the server names; a caller that knows the
 * library it expects checks it.
 */
export function itemDetailQuery(queries: ViewerQueries, itemId: string, related: 'all' | 'none' = 'all'): ViewerQueryOptions<DetailProjection> {
  if (!id(itemId)) throw new Error('An item is required.');
  if (related !== 'all' && related !== 'none') throw new Error('Invalid detail related option.');
  const queryKey = queryKeys.itemDetail(itemId, related);
  return {
    queryKey,
    queryFn: async ({signal}) => {
      const deadline = linkAbortSignals([signal], DETAIL_DEADLINE_MS);
      let item: Item;
      try { item = await readItem(queries.api, itemId, deadline.signal); }
      catch (error) { if (deadline.signal.aborted && !signal.aborted) throw new Failure('timeout', 'The server took too long to respond.', true); throw error; }
      finally { deadline.dispose(); }
      return detailProjection(item, queries.scope);
    },
    meta: {dependencies: data => {
      const libraryId = (data as DetailProjection | undefined)?.scope.libraryId;
      return [dependsOn.libraries, ...(libraryId ? titlesWithPersonalState(libraryId) : []), dependsOn.event('stream.resync')];
    }},
  };
}

/** Read a title's page through the viewer's cache: the cached page when it is current, else one shared read. */
export function readItemDetail(queries: ViewerQueries, itemId: string, related: 'all' | 'none' = 'none'): Promise<DetailProjection> {
  return queries.client.fetchQuery(itemDetailQuery(queries, itemId, related));
}

export class DetailService {
  private readonly queries: ViewerQueries;
  private api: DetailApi; private bound: DetailScope; private timeoutMs: number;
  private generation = 0; private mutationController?: AbortController;
  private listeners = new Set<() => void>(); private state: DetailSnapshot; private disposed = false;
  private commands: Command[] = []; private running = false;
  private related: 'all' | 'none' = 'all';
  private observer?: QueryObserver<DetailProjection, Error>; private stopObserving?: () => void;
  constructor(options: { queries: ViewerQueries; api: DetailApi; scope: DetailScope; requestId?: () => Promise<string>; timeoutMs?: number }) {
    this.queries = options.queries; this.api = options.api; this.bound = checkScope(options.scope); this.timeoutMs = options.timeoutMs ?? DETAIL_DEADLINE_MS;
    if (this.bound.serverId !== this.queries.scope.serverId) throw new Error('A title is read from its own server\'s cache.');
    if (!Number.isFinite(this.timeoutMs) || this.timeoutMs <= 0 || this.timeoutMs > 120000) throw new Error('Invalid detail deadline.');
    this.state = this.empty();
  }
  private empty(): DetailSnapshot { return Object.freeze({ generation: this.generation, scope: this.bound, target: null, phase: 'idle', data: null, error: null, pending: Object.freeze([]), mutationError: null }); }
  getSnapshot = (): DetailSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { if (this.disposed) return () => {}; this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<DetailSnapshot> = {}): void {
    if (this.disposed) return;
    const pending = this.commands.map(c => Object.freeze({ intent: c.intent, phase: c.phase }));
    const next = { ...this.state, ...patch, pending: Object.freeze(pending) };
    const same = (Object.keys(next) as (keyof DetailSnapshot)[]).every(key => key === 'pending' ? JSON.stringify(next.pending) === JSON.stringify(this.state.pending) : next[key] === this.state[key]);
    if (same) return;
    this.state = Object.freeze(next);
    for (const listener of this.listeners) listener();
  }
  private active(): void { if (this.disposed) throw new Error('Detail service is disposed.'); }
  /** The page's state from the cache's answer for its title. */
  private derive(): void {
    const target = this.state.target, result = this.observer?.getCurrentResult();
    if (!target || !result) return;
    let data = result.data ?? null, error = result.error ? errorInfo(result.error) : null;
    if (data && (data.item.id !== target.itemId || data.scope.libraryId !== target.libraryId)) { data = null; error = Object.freeze({code: 'not_found', message: 'This title is not in this library.', retryable: false}); }
    if (error && FINAL_DETAIL_CODES.includes(error.code)) data = null;
    // A failed read of a page already shown keeps it; the hub says so quietly once the server stays out of reach.
    if (data && error) error = null;
    this.publish({ data, error, phase: data ? 'ready' : error ? 'error' : 'loading' });
  }
  private observe(target: DetailTarget): void {
    this.stopObserving?.();
    this.observer = new QueryObserver<DetailProjection, Error>(this.queries.client, itemDetailQuery(this.queries, target.itemId, this.related));
    this.stopObserving = this.observer.subscribe(() => this.derive());
  }
  private fence(): void {
    this.generation++; this.mutationController?.abort();
    for (const c of this.commands) c.resolve();
    this.commands = []; this.running = false;
    this.stopObserving?.(); this.stopObserving = undefined; this.observer = undefined;
  }
  /** `related: 'none'` skips the embedded recommendation rows, for a page that reads them itself.
   * `fresh` reads the title from the server even when the cache holds it: for a check that gates a
   * write (its grants must be the server's current ones, not the page read when the screen opened). */
  select(input: DetailTarget, options: Readonly<{related?: 'all' | 'none'; fresh?: boolean}> = {}): Promise<void> {
    this.active(); const target = checkTarget(input), related = options.related ?? 'all';
    if (related !== 'all' && related !== 'none') throw new Error('Invalid detail related option.');
    const current = this.state.target;
    if (!this.observer || !current || current.itemId !== target.itemId || current.libraryId !== target.libraryId || this.related !== related) {
      this.fence(); this.related = related;
      this.publish({ ...this.empty(), target, generation: this.generation });
      this.observe(target);
      this.derive();
    }
    return this.settled(options.fresh === true);
  }
  /** Resolves once the page has an answer (the cached one, or the shared read's). Never rejects: the state says how it went. */
  private async settled(fresh: boolean): Promise<void> {
    const target = this.state.target; if (!target) return;
    const options = itemDetailQuery(this.queries, target.itemId, this.related);
    await this.queries.client.fetchQuery(fresh ? {...options, staleTime: 0} : options).catch(() => undefined);
    this.derive();
  }
  /** Read the title again; what is shown stays shown while it is read. */
  refresh(): Promise<void> {
    this.active(); const observer = this.observer;
    if (!this.state.target || !observer) return Promise.resolve();
    return observer.refetch().then(() => { if (this.observer === observer) this.derive(); });
  }
  retryRead(): Promise<void> { return this.refresh(); }
  retry(): Promise<void> { return this.refresh(); }
  mutate(input: PersonalIntent): Promise<void> {
    this.active(); const intent = checkIntent(input), data = this.state.data;
    if (!data || !data.actions.some(a => a.id === intent.action && a.enabled)) throw new Error('This personal action is not currently available.');
    const last = this.commands.at(-1);
    if (last && JSON.stringify(last.intent) === JSON.stringify(intent)) return last.promise;
    if (this.commands.length >= 8) throw new Error('Wait for pending personal actions to finish.');
    let resolve!: () => void; const promise = new Promise<void>(r => { resolve = r; });
    this.commands.push({ intent, phase: 'queued', promise, resolve }); this.publish(); void this.pump(); return promise;
  }
  retryMutation(): Promise<void> {
    this.active(); const command = this.commands[0];
    if (!command || this.running || command.phase === 'queued' || command.phase === 'sending') return command?.promise ?? Promise.resolve();
    let resolve!: () => void; command.promise = new Promise<void>(r => { resolve = r; }); command.resolve = resolve; command.phase = 'queued';
    this.publish({ mutationError: null }); void this.pump(); return command.promise;
  }
  /** Give up on an action the server refused. */
  dismissMutation():void {this.active();if(this.running)return;for(const c of this.commands)c.resolve();this.commands=[];this.publish({mutationError:null});}
  /** The state the server answered with (or the tap that is kept for later), written into every cached page of the title. */
  private applyPersonal(itemId: string, personal: PersonalState): void {
    for (const related of ['all', 'none'] as const) this.queries.client.setQueryData<DetailProjection>(queryKeys.itemDetail(itemId, related), held =>
      !held ? held : Object.freeze({ ...held, item: Object.freeze({ ...held.item, progressSeconds: personal.progressSeconds }), personal }));
  }
  cancel(): void { this.active(); this.fence(); this.publish(this.empty()); }
  dispose(): void { this.fence(); this.disposed = true; this.listeners.clear(); }
  private async pump(): Promise<void> {
    if (this.running || this.disposed || !this.commands.length || this.commands[0].phase !== 'queued') return;
    const command = this.commands[0], data = this.state.data;
    if (!data) { command.phase = 'retry-required'; command.resolve(); this.publish({ mutationError: Object.freeze({ code: 'detail_required', message: 'Reload details before retrying this action.', retryable: true }) }); return; }
    this.running = true; command.phase = 'sending'; this.publish({ mutationError: null });
    const generation = this.generation, api = this.api, controller = new AbortController(); this.mutationController = controller;
    try {
      const {action, value} = command.intent;
      const change: ItemStateChange = action === 'rating' ? {rating: value === null ? null : value / 2} : action === 'watchlist' ? {inList: value} : action === 'favorite' ? {favorite: value} : {watched: value};
      const kept = (): PersonalState => Object.freeze({...data.personal, ...(action === 'rating' ? {rating: value} : action === 'watchlist' ? {watchlisted: value} : action === 'favorite' ? {favorite: value} : {watched: value, progressSeconds: 0})});
      let applied: PersonalState;
      try { applied = personalState((await this.deadline(setItemState(api, data.scope.itemId, change, controller.signal), controller)).state); }
      catch (error) {
        // Nothing answered: the tap is kept on this device and sent later, so the page shows it now.
        if (errorInfo(error).code !== 'sync_pending' || this.disposed || generation !== this.generation) throw error;
        this.applyPersonal(data.scope.itemId, kept()); this.commands.shift(); command.resolve(); this.publish({ mutationError: errorInfo(error) }); return;
      }
      if (this.disposed || generation !== this.generation) return;
      this.applyPersonal(data.scope.itemId, applied);
      this.commands.shift(); command.resolve(); this.publish({ mutationError: null });
      // Every other page showing this title (its show's episode list, a Home row, a grid) reads again in place now.
      this.queries.moved(personalChangedHere(data.scope.libraryId, [data.item.id, data.item.episode?.showId, data.item.episode?.seasonId]));
      // What the change touched elsewhere on this page (Up Next, counts) is read in place, with no loading state.
      await this.refresh().catch(() => {});
    } catch (error) {
      if (this.disposed || generation !== this.generation) return;
      command.phase = 'retry-required'; command.resolve();
      this.publish({ mutationError: errorInfo(error) });
    } finally {
      if (generation === this.generation) { this.running = false; if (this.commands[0]?.phase === 'queued') void this.pump(); }
    }
  }
  private async deadline<T>(operation: Promise<T>, controller: AbortController): Promise<T> {
    let timer: ReturnType<typeof setTimeout> | undefined, onAbort: (() => void) | undefined;
    try {
      const abort = new Promise<never>((_, reject) => { onAbort = () => reject(new Failure('cancelled', 'Detail request cancelled.')); if (controller.signal.aborted) onAbort(); else controller.signal.addEventListener('abort', onAbort, { once: true }); });
      const timeout = new Promise<never>((_, reject) => { timer = setTimeout(() => { reject(new Failure('timeout', 'The server took too long to respond.', true)); controller.abort(); }, this.timeoutMs); });
      return await Promise.race([operation, abort, timeout]);
    } finally { if (timer) clearTimeout(timer); if (onAbort) controller.signal.removeEventListener('abort', onAbort); }
  }
}

/** A title's cast or crew, whole: the server lists every credit in billing order and the page is cut from that list. */
export async function readCreditPage(api:DetailApi,itemId:string,group:'cast'|'crew',_options:{cursor?:string;limit?:number}={},signal?:AbortSignal):Promise<CreditPage> {
 const all:Credit[]=[];
 for(let cursor:string|undefined;all.length<10000;){
  const page=await readItemCredits(api,itemId,{cursor,limit:200},signal);
  all.push(...page.items);
  if(!page.nextCursor||!page.items.length)break;
  cursor=page.nextCursor;
 }
 const credits=all.map(credit).filter(row=>(row.department==='cast')===(group==='cast'));
 return Object.freeze({group,credits:Object.freeze(credits),total:credits.length});
}
export type FileInfoDiscTitle = Readonly<{number: number; durationMs: number; chapters: number; angles: number}>;
export type FileInfoDisc = Readonly<{fileId: string; kind: 'bluray' | 'dvd'; titles: readonly FileInfoDiscTitle[]}>;
