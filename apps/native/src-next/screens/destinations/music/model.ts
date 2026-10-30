import {DetailService} from '../../../../../../packages/client-core/src/detail';
import type {ContentProjection, ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {
  BrowseQueryBuilder, browseCapabilitiesPath, browsePath, parseBrowseCapabilities, parseBrowseResult, type BrowseCapabilities,
} from '../../../../../../packages/client-core/src/browse';
import {getContainerState, setContainerFlag, type ContainerState} from '../../../../../../packages/client-core/src/container-state';
import {readListeningSimilar, LibraryContentService} from '../../../../../../packages/client-core/src/library-content';
import {nativeSimilarRow, type NativeRelatedRow, type NativeRelatedRowSnapshot} from '../../watch/title-related';
import {encodedRoutePath} from '../../../../../../packages/contracts/src/routes';
import {unreadableServerResponse} from '../../../../../../packages/client-core/src/server-messages';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellPlaybackAdapter} from '../../../shell/playback';
import {nativeDestinationLifetime} from '../lifetime';
import {MusicSectionPager, emptyMusicSection, type MusicSection} from './paging';

export type MusicAlbumRole = 'album' | 'ep' | 'single' | 'compilation' | 'appearance';
/** `homeActions` is the release's aggregate selection offer (MOB-030), carried as sent: the
 * catalogue selection adapter validates it, and an unreadable offer grants nothing. */
export type MusicAlbumRow = Readonly<{id: string; libraryId: string; title: string; artist?: Readonly<{id: string; name: string}>; year?: number; posterUrl?: string; role?: MusicAlbumRole; homeActions?: ContentEntry['homeActions']}>;
export type MusicPageEntry = Readonly<{kind: 'album' | 'artist'; entry: ContentEntry}>;
export type NativeTVMusicSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  entry?: ContentEntry;
  songs: readonly ContentEntry[];
  songTotal?: number;
  songDurationSeconds?: number;
  albums: readonly MusicAlbumRow[];
  songPage: MusicSection<ContentEntry>;
  releases: MusicSection<MusicAlbumRow>;
  similar: readonly ContentEntry[];
  similarBySound?: boolean;
  similarProjection?: ContentProjection;
  similarError?: unknown;
  /** The similar row's continuation (TV-005): its pages as the shelf's paging contract. */
  similarPaging?: NativeRelatedRowSnapshot;
  favorite: boolean;
  allSongs: MusicSection<ContentEntry> & Readonly<{songs: readonly ContentEntry[]}>;
  artistDetails?: ContentEntry;
  albumDetails?: ContentEntry;
  error?: unknown;
}>;

type Options = Readonly<{requestId?: () => Promise<string>; api: NativeShellBootstrapContext['api']; entry: ContentEntry; similarScope: Parameters<typeof readListeningSimilar>[1]; lifetime: ReturnType<typeof nativeDestinationLifetime>; playback?: NativeShellPlaybackAdapter}>;
const ALBUM_MEMBERSHIP_PAGE_LIMIT = 100;
const membershipFailure = (code: string) => Object.assign(new Error(code), {code, ...(code==='native_music_retired'?{name:'AbortError'}:{})});
/** A plain failure with no stable wire code came from this stage, not from the
 * person's request; keep it diagnosable without showing raw text or losing a
 * server/permission status the presentation layer already understands. */
const stageFailure = (stage: 'capabilities' | 'favorite' | 'similar' | 'songs', error: unknown, signal: AbortSignal, musicRead: Readonly<Record<string, unknown>>) => {
  if (signal.aborted) return Object.assign(new Error('native_music_retired'), {name: 'AbortError'}); // lint-strings-allow DOMException name
  const value = error && typeof error === 'object' ? error as {code?: unknown; status?: unknown; name?: unknown; retryable?: unknown; retryAfterSeconds?: unknown; path?: unknown} : undefined;
  if (value && value.name === 'AbortError') return error;
  if (error instanceof TypeError) { try { return Object.assign(error, {musicRead}); } catch { return error; } }
  if (!(value && typeof value.code === 'string' || value && typeof value.status === 'number'))
    return Object.assign(new Error(`music_album_${stage}_unavailable`), {code: `music_album_${stage}_unavailable`, cause: error, musicRead});
  const code = value && typeof value.code === 'string' ? value.code : `music_album_${stage}_unavailable`;
  return Object.assign(new Error(code), {code, musicRead, cause: error,
    ...(value && typeof value.status === 'number' ? {status: value.status} : {}),
    ...(value && typeof value.retryable === 'boolean' ? {retryable: value.retryable} : {}),
    ...(value && typeof value.retryAfterSeconds === 'number' ? {retryAfterSeconds: value.retryAfterSeconds} : {}),
    ...(value && typeof value.path === 'string' ? {path: value.path} : {})});
};
const diagnose = (read: 'capabilities' | 'favorite' | 'similar' | 'songs' | 'details', method: 'GET' | 'POST', path: string, error: unknown) => {
  const value = error && typeof error === 'object' ? error as {code?: unknown; status?: unknown; field?: unknown; path?: unknown} : undefined;
  const diagnostic = {read, method, path, ...(value && typeof value.status === 'number' ? {status: value.status} : {}),
    ...(value && typeof value.code === 'string' ? {code: value.code} : {}),
    ...(value && typeof value.field === 'string' ? {field: value.field} : {}),
    ...(value && typeof value.path === 'string' && typeof value.field !== 'string' ? {field: value.path} : {})};
  if (typeof __DEV__ !== 'undefined' && __DEV__) console.warn(`music-read-failed ${JSON.stringify(diagnostic)}`);
  return diagnostic;
};
const id = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\r\n]/.test(value);
const obj = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const list = (value: unknown, max: number): unknown[] => {
  if (!Array.isArray(value) || value.length > max) throw new Error(unreadableServerResponse);
  return value;
};
const text = (value: unknown, max = 4096): value is string => typeof value === 'string' && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
const album = (value: unknown): MusicAlbumRow | undefined => {
  if (!obj(value) || !id(value.id) || !id(value.libraryId) || !text(value.title, 2048)) return undefined;
  const artist = id(value.albumArtistId) && text(value.albumArtist, 2048) ? {id: value.albumArtistId, name: value.albumArtist} : undefined;
  const role = ['album', 'ep', 'single', 'compilation', 'appearance'].includes(value.role as string) ? value.role as MusicAlbumRole : undefined;
  return Object.freeze({id: value.id, libraryId: value.libraryId, title: value.title,
    ...(artist ? {artist} : {}), ...(typeof value.year === 'number' && Number.isSafeInteger(value.year) && value.year > 0 ? {year: value.year} : {}),
    ...(role ? {role} : {}), ...(text(value.posterUrl) && value.posterUrl.startsWith('/') ? {posterUrl: value.posterUrl} : {}),
    ...(obj(value.homeActions) ? {homeActions: value.homeActions as ContentEntry['homeActions']} : {})});
};
const request = async <T>(api: Pick<NativeShellBootstrapContext['api'], 'request'>, path: string, method: 'GET' | 'POST', body: unknown, signal: AbortSignal): Promise<T> => {
  const value = await api.request<T>(path, method, body, signal);
  return value;
};

/** One authority-local album or artist page. The server owns every list, count and order. */
export class NativeTVMusicController {
  private options: Options;
  private api: Pick<NativeShellBootstrapContext['api'], 'request'>;
  private songQuery?: string;
  private allSongQuery?: string;
  private topSongsRevision?: string;
  private entityWitness?: Readonly<{service: LibraryContentService; projection: ContentProjection; signal: AbortSignal}>;
  private similarEntries = new WeakSet<ContentEntry>();
  private similarRow?: NativeRelatedRow;
  private songFavorites = new WeakMap<ContentEntry, boolean>();
  private songFavoriteOperations = new Map<ContentEntry, {controller: AbortController; service: DetailService; current: () => boolean; desired: boolean; running: boolean}>();
  private releaseDTOs = new WeakMap<MusicAlbumRow,ContentEntry>();
  private releaseWitness = new WeakMap<ContentEntry,MusicAlbumRow>();
  private albumArtist?: Readonly<{source: ContentEntry; entry: ContentEntry}>;
  private songPager?: MusicSectionPager<ContentEntry>;
  private releasePager?: MusicSectionPager<MusicAlbumRow>;
  private allSongPager?: MusicSectionPager<ContentEntry>;
  private allSongSections = new WeakMap<object, MusicSection<ContentEntry>>();
  private state: NativeTVMusicSnapshot = Object.freeze({phase: 'idle', songs: [], albums: [], songPage: emptyMusicSection<ContentEntry>(), releases: emptyMusicSection<MusicAlbumRow>(), similar: [], favorite: false, allSongs: Object.freeze({...emptyMusicSection<ContentEntry>(), songs: []})});
  private listeners = new Set<() => void>();
  private controller?: AbortController;
  private playController?: AbortController;
  private membershipController?: AbortController;
  private allSongsController?: AbortController;
  private disposed = false;
  private generation = 0;
  private pending?: Promise<void>;
  constructor(options: Options) {
    this.options = options;
    this.api={request:async<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>=>{
      if(!this.current()||signal?.aborted)throw membershipFailure('native_music_retired');
      const value=await options.api.request<T>(path,method,body,signal);
      if(!this.current()||signal?.aborted)throw membershipFailure('native_music_retired');return value;
    }};
    this.state = Object.freeze({...this.state, entry: options.entry});
  }
  getSnapshot = (): NativeTVMusicSnapshot => this.state;
  /** Every selection stays in the exact Browse query/revision that supplied its tracks. */
  selectionPage = (group: 'songs' | 'all-songs'): Readonly<{generation: number; query: string; revision: string; offset: number}> | undefined => {
    if (!this.current()) return;
    const section = group === 'all-songs' ? this.state.allSongs : this.state.songPage;
    const query = group === 'all-songs' ? this.allSongQuery : this.songQuery;
    const revision = group === 'songs' && this.options.entry.kind === 'artist' ? this.topSongsRevision : section.revision;
    return query && revision && section.phase !== 'refresh-required' ? {generation: this.generation, query, revision, offset: section.offset} : undefined;
  };
  contains = (entry: ContentEntry): boolean => {
    if (!this.current()) return false;
    const state = this.state;
    if(this.similarEntries.has(entry))return this.similarCurrent(state,entry);
    return state.entry === entry || this.artistEntry() === entry || (this.options.entry.kind!=='album'||state.songPage.phase!=='refresh-required') && state.songs.includes(entry) || state.allSongs.phase!=='refresh-required' && state.allSongs.songs.includes(entry)
      || this.releaseCurrent(entry);
  };
  artistEntry = (): ContentEntry | undefined => {
    const source = this.state.albumDetails ?? this.state.entry;
    if (!this.current() || source?.kind !== 'album' || !source.artist?.id || !source.libraryId) return;
    if (this.albumArtist?.source !== source) this.albumArtist = {source, entry: Object.freeze({kind: 'artist', id: source.artist.id,
      title: source.artist.name, libraryId: source.libraryId})};
    return this.albumArtist.entry;
  };
  /** Selection borrows the page's actual admitted catalogue scope. Detail grants
   * still come from each explicitly selected item through the shared adapter. */
  selectionProjection = (): ContentProjection | undefined => {
    const witness = this.entityWitness;
    return witness && this.entityCurrent(witness) ? witness.projection : undefined;
  };
  songFavorite = (entry: ContentEntry): boolean => this.songFavorites.get(entry) ?? entry.favorite === true;
  songFavoritePending = (entry: ContentEntry): boolean => this.songFavoriteOperations.get(entry)?.running === true;
  toggleSongFavorite = async (entry: ContentEntry): Promise<void> => {
    if (!this.contains(entry) || entry.kind !== 'song' || !entry.libraryId || this.songFavoritePending(entry) || !this.options.requestId) return;
    let operation = this.songFavoriteOperations.get(entry);
    if (!operation) {
      const controller = new AbortController(), generation = this.generation;
      const current = () => !controller.signal.aborted && this.current() && generation === this.generation && this.contains(entry);
      const service = new DetailService({scope: this.options.lifetime.scope, requestId: this.options.requestId,
        api: {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
          if (!current()) throw membershipFailure('native_music_retired');
          const value = await this.api.request<T>(path, method, body, signal);
          if (!current()) throw membershipFailure('native_music_retired');
          return value;
        }}});
      controller.signal.addEventListener('abort', () => service.dispose(), {once: true});
      operation = {controller, service, current, desired: !this.songFavorite(entry), running: false};
      this.songFavoriteOperations.set(entry, operation);
    }
    const {service, current, desired} = operation;
    if (!current()) return;
    operation.running = true; this.publish({});
    try {
      // A failed receipt keeps the same Detail command. A deliberate retry must
      // not manufacture a second operation id for an ambiguous first mutation.
      if (service.getSnapshot().pending.length) await service.retryMutation();
      else {
        await service.select({itemId: entry.id, libraryId: entry.libraryId});
        if (!current()) return;
        const loaded = service.getSnapshot();
        if (loaded.phase !== 'ready' || !loaded.data) throw loaded.error ?? membershipFailure('music_song_favorite_unavailable');
        if (loaded.data.personal.favorite !== desired) await service.mutate({action: 'favorite', value: desired});
      }
      if (!current()) return;
      const applied = service.getSnapshot();
      if (applied.data) this.songFavorites.set(entry, applied.data.personal.favorite);
      if (applied.pending.length || applied.mutationError || applied.error) throw applied.mutationError ?? applied.error ?? membershipFailure('music_song_favorite_unconfirmed');
    } finally {
      operation.running = false;
      if (!current() || !service.getSnapshot().pending.length) {
        operation.controller.abort();
        if (this.songFavoriteOperations.get(entry) === operation) this.songFavoriteOperations.delete(entry);
      }
      this.publish({});
    }
  };
  /** The presenter borrows one DTO from the exact offered release object.
   * Equal IDs from a replacement section never revive an old card/menu. */
  releaseEntry = (row: MusicAlbumRow): ContentEntry | undefined => {
    if(!this.current()||this.state.releases.phase==='refresh-required'||!this.state.albums.includes(row))return undefined;
    const existing=this.releaseDTOs.get(row);if(existing)return existing;
    const entry:ContentEntry=Object.freeze({id:row.id,libraryId:row.libraryId,kind:'album',title:row.title,
      ...(row.artist?{artist:row.artist}:{}),...(row.year?{year:row.year}:{}),...(row.posterUrl?{posterUrl:row.posterUrl}:{}),...(row.homeActions?{homeActions:row.homeActions}:{})});
    this.releaseDTOs.set(row,entry);this.releaseWitness.set(entry,row);return entry;
  };
  private releaseCurrent(entry:ContentEntry):boolean{
    const row=this.releaseWitness.get(entry);
    return !!row&&this.state.releases.phase!=='refresh-required'&&this.state.albums.includes(row);
  }
  /** A card belongs to the original entity projection, not a later same-ID page. */
  /** The similar row's page boundary, for the exact row snapshot it rendered. */
  moreSimilar = (expected: NativeRelatedRowSnapshot): void => {if(this.current())this.similarRow?.next(expected);};
  /** The similar row's paging for a shelf; the waiting host is a cursor, never a title. */
  similarPage = (snapshot: NativeTVMusicSnapshot) => {
    const pages=snapshot.similarPaging;
    return pages?{hasMore:pages.hasMore,loading:pages.loading,failed:pages.failed,waitingId:pages.waitingId,fulfilled:pages.fulfilled,
      waitingItem:{id:pages.waitingId,kind:this.options.entry.kind,title:'',libraryId:this.options.entry.libraryId} as ContentEntry,requestNext:()=>this.moreSimilar(pages)}:undefined;
  };
  similarCurrent = (snapshot: NativeTVMusicSnapshot, entry: ContentEntry): boolean => {
    const witness=this.entityWitness;
    return !!witness&&this.entityCurrent(witness)&&snapshot.similar===this.state.similar&&
      snapshot.similarProjection===witness.projection&&snapshot.similar.includes(entry);
  };
  private entityCurrent(witness:NonNullable<NativeTVMusicController['entityWitness']>):boolean{
    const snapshot=witness.service.getSnapshot();
    return this.current()&&!witness.signal.aborted&&this.entityWitness===witness&&snapshot.phase==='ready'&&snapshot.projection===witness.projection;
  }
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  };
  private current(): boolean { return !this.disposed && this.options.lifetime.current(); }
  private publish(patch: Partial<NativeTVMusicSnapshot>): void {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const [entry, operation] of this.songFavoriteOperations) if (!this.contains(entry)) {
      operation.controller.abort(); this.songFavoriteOperations.delete(entry);
    }
    for (const listener of [...this.listeners]) listener();
  }
  load = (): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    const controller = new AbortController();
    this.controller?.abort();
    for (const operation of this.songFavoriteOperations.values()) operation.controller.abort();
    this.songFavoriteOperations.clear(); this.songFavorites = new WeakMap();
    this.controller = controller;
    const generation = ++this.generation;
    this.songPager?.dispose();this.releasePager?.dispose();this.allSongPager?.dispose();this.allSongsController?.abort();this.similarRow?.dispose();
    this.songPager=undefined;this.releasePager=undefined;this.allSongPager=undefined;this.similarRow=undefined;
    this.entityWitness?.service.dispose();this.entityWitness=undefined;
    this.songQuery = undefined; this.allSongQuery = undefined; this.topSongsRevision = undefined;
    this.publish({songs:[],albums:[],songPage:emptyMusicSection<ContentEntry>(),releases:emptyMusicSection<MusicAlbumRow>(),allSongs:Object.freeze({...emptyMusicSection<ContentEntry>(),songs:[]}),phase:'loading',error:undefined,albumDetails:undefined,similar:[],similarBySound:undefined,similarProjection:undefined,similarError:undefined,similarPaging:undefined});
    const task = (this.options.entry.kind === 'album' ? this.loadAlbum(controller.signal) : this.loadArtist(controller.signal))
      .catch(error => { if (this.current() && generation === this.generation && !controller.signal.aborted) this.publish({phase: 'error', error}); })
      .finally(() => { if (this.pending === task) this.pending = undefined; });
    this.pending = task;
    return task;
  };
  private async capabilities(pivot: string, signal: AbortSignal): Promise<BrowseCapabilities> {
    const libraryId = this.options.entry.libraryId;
    if (!libraryId) throw new Error('music_library_required');
    return parseBrowseCapabilities(await request(this.api, browseCapabilitiesPath(libraryId, pivot), 'GET', undefined, signal));
  }
  private async browse(capabilities: BrowseCapabilities, build: (builder: BrowseQueryBuilder) => BrowseQueryBuilder, signal: AbortSignal) {
    const libraryId = this.options.entry.libraryId;
    if (!libraryId) throw new Error('music_library_required');
    const body = build(new BrowseQueryBuilder(capabilities)).build();
    return parseBrowseResult(await request(this.api, browsePath(libraryId), 'POST', body, signal));
  }
  private songSection(capabilities: BrowseCapabilities, build: (builder: BrowseQueryBuilder) => BrowseQueryBuilder, generation: number, all = false) {
    const path=browsePath(this.options.entry.libraryId!),original=build(new BrowseQueryBuilder(capabilities)).build();
    if (all) this.allSongQuery = JSON.stringify([path, original]); else this.songQuery = JSON.stringify([path, original]);
    const pager = new MusicSectionPager<ContentEntry>({
      current: () => this.current() && generation === this.generation,
      read: async (cursor, signal) => {
        const page=parseBrowseResult(await request(this.api,path,'POST',{...original,...(cursor?{cursor}:{})},signal));
        if (page.entries.some(entry => entry.kind !== 'song')) throw membershipFailure('music_section_unreadable');
        return {items:page.entries,nextCursor:page.pageInfo.hasMore?page.pageInfo.nextCursor:'',revision:page.pageInfo.revision,
          offset:page.pageInfo.start,total:page.pageInfo.total,totalIsLowerBound:page.pageInfo.totalIsLowerBound};
      },
      publish: section => {
        if(all){const projection=Object.freeze({...section,songs:section.items});this.allSongSections.set(projection,section);this.publish({allSongs:projection});}
        else this.publish({songPage:section,songs:section.items,songTotal:this.state.albumDetails?.count??(section.totalIsLowerBound?undefined:section.total)});
      },
    });
    return pager;
  }
  nextSongs = (expected: MusicSection<ContentEntry>) => this.songPager?.next(expected) ?? Promise.resolve();
  previousSongs = (expected: MusicSection<ContentEntry>) => this.songPager?.previous(expected) ?? Promise.resolve();
  retrySongs = (expected: MusicSection<ContentEntry>) => this.songPager?.retry(expected) ?? Promise.resolve();
  nextReleases = (expected: MusicSection<MusicAlbumRow>) => this.releasePager?.next(expected) ?? Promise.resolve();
  previousReleases = (expected: MusicSection<MusicAlbumRow>) => this.releasePager?.previous(expected) ?? Promise.resolve();
  retryReleases = (expected: MusicSection<MusicAlbumRow>) => this.releasePager?.retry(expected) ?? Promise.resolve();
  nextAllSongs = (expected: MusicSection<ContentEntry>) => (this.allSongSections.has(expected)?this.allSongPager?.next(this.allSongSections.get(expected)!):undefined) ?? Promise.resolve();
  previousAllSongs = (expected: MusicSection<ContentEntry>) => (this.allSongSections.has(expected)?this.allSongPager?.previous(this.allSongSections.get(expected)!):undefined) ?? Promise.resolve();
  retryAllSongs = (expected: MusicSection<ContentEntry>) => expected===this.state.allSongs&&!this.allSongPager?this.loadAllSongs():(this.allSongSections.has(expected)?this.allSongPager?.retry(this.allSongSections.get(expected)!):undefined)??Promise.resolve();
  focusSong = (entry: ContentEntry, expected: MusicSection<ContentEntry>) => this.songPager?.focus(entry,expected);
  blurSong = (entry: ContentEntry, expected: MusicSection<ContentEntry>) => this.songPager?.blur(entry,expected);
  focusRelease = (entry: MusicAlbumRow, expected: MusicSection<MusicAlbumRow>) => this.releasePager?.focus(entry,expected);
  blurRelease = (entry: MusicAlbumRow, expected: MusicSection<MusicAlbumRow>) => this.releasePager?.blur(entry,expected);
  focusAllSong = (entry: ContentEntry, expected: MusicSection<ContentEntry>) => {const section=this.allSongSections.get(expected);if(section)this.allSongPager?.focus(entry,section);};
  blurAllSong = (entry: ContentEntry, expected: MusicSection<ContentEntry>) => {const section=this.allSongSections.get(expected);if(section)this.allSongPager?.blur(entry,section);};
  refreshSongs = (expected: MusicSection<ContentEntry>) => expected===this.state.songPage?this.load():Promise.resolve();
  refreshReleases = (expected: MusicSection<MusicAlbumRow>) => expected===this.state.releases?this.load():Promise.resolve();
  refreshAllSongs = (expected: MusicSection<ContentEntry>) => expected===this.state.allSongs?this.loadAllSongs():Promise.resolve();
  private async favoriteState(signal: AbortSignal): Promise<boolean> {
    const path = `/v1/containers/${this.options.entry.kind}/${encodeURIComponent(this.options.entry.id)}/personal-state`;
    try {
      const state = await getContainerState(this.api, this.options.entry.kind as 'album' | 'artist', this.options.entry.id, signal);
      return state.favorite;
    } catch (error) {
      if (!signal.aborted) diagnose('favorite', 'GET', path, error);
      return false;
    }
  }
  private async loadSimilar(signal:AbortSignal):Promise<void>{
    const witness=this.entityWitness,path=encodedRoutePath('get_items_id_similar',{id:encodeURIComponent(this.options.entry.id)})+'?limit=12&by=sound';
    try{
      if(!witness||!this.entityCurrent(witness))throw membershipFailure('invalid_projection');
      const value=await readListeningSimilar(this.api,this.options.similarScope,this.options.entry.id,{limit:12,by:'sound',signal});
      if(!this.entityCurrent(witness))return;
      const projection=witness.projection;
      if(value.libraryId!==projection.scope.libraryId||value.itemId!==projection.scope.entityId||
        value.revision.catalog!==projection.revision.catalog||value.revision.viewer!==projection.revision.viewer)throw membershipFailure('invalid_projection');
      // The row continues to its end on this page's basis and revision.
      this.similarRow?.dispose();
      const row:NativeRelatedRow=nativeSimilarRow(this.api,this.options.similarScope,value,()=>publish());
      const publish=()=>{
        if(this.similarRow!==row||!this.entityCurrent(witness))return;
        const pages=row.getSnapshot();
        const similar=Object.freeze(pages.entries.filter(entry=>entry.kind===this.options.entry.kind&&entry.id!==this.options.entry.id));
        for(const entry of similar)this.similarEntries.add(entry);
        this.publish({similar,similarBySound:value.bySound,similarProjection:projection,similarError:undefined,similarPaging:pages});
      };
      this.similarRow=row;publish();
    }catch(error){
      if(this.current()&&!signal.aborted&&(!witness||this.entityWitness===witness))
        this.publish({similar:[],similarBySound:undefined,similarProjection:undefined,similarError:stageFailure('similar',error,signal,diagnose('similar','GET',path,error)),similarPaging:undefined});
    }
  }
  private async loadAlbum(signal: AbortSignal): Promise<void> {
    const libraryId = this.options.entry.libraryId!, capabilitiesPath = browseCapabilitiesPath(libraryId, 'songs');
    const [capabilities, favorite, details] = await Promise.all([
      this.capabilities('songs', signal).catch(error => {throw stageFailure('capabilities', error, signal, diagnose('capabilities', 'GET', capabilitiesPath, error));}),
      this.favoriteState(signal), this.entityDetails('album', signal),
    ]);
    if (!this.current() || signal.aborted) return;
    const pager=this.songSection(capabilities,builder=>builder.where('memberOf','equals',this.options.entry.id).sort('groupOrder','asc')
      .limit(Math.min(200,capabilities.queryLimits.maximumLimit)),this.generation);
    this.songPager=pager;await pager.load();
    if (!this.current() || signal.aborted) return;
    const page=pager.getSnapshot();
    if(page.phase!=='ready')throw stageFailure('songs',page.error,signal,diagnose('songs','POST',browsePath(libraryId),page.error));
    const complete=!page.nextCursor&&page.items.length===page.total;
    const duration=details?.duration??(complete&&page.items.length>0&&page.items.every(entry=>entry.duration!==undefined)
      ?page.items.reduce((sum,entry)=>sum+entry.duration!,0):undefined);
    this.publish({phase:'ready',songTotal:details?.count??(page.totalIsLowerBound?undefined:page.total),songDurationSeconds:duration,albumDetails:details,favorite});
    // Optional recommendations have their own honest error, retaining ready tracks.
    if (!this.current() || signal.aborted) return;
    await this.loadSimilar(signal);
  }
  private async loadArtist(signal: AbortSignal): Promise<void> {
    // The Details producer is secondary: it may arrive after the songs and
    // releases, and its refusal leaves the real grid-entry facts in place.
    const [capabilities,favorite]=await Promise.all([
      this.capabilities('songs',signal),this.favoriteState(signal),this.artistAlbums(signal),
    ]);
    if (!this.current() || signal.aborted) return;
    const original = new BrowseQueryBuilder(capabilities).where('artist', 'equals', this.options.entry.title).sort('playCount', 'desc').limit(Math.min(5, capabilities.queryLimits.maximumLimit)).build();
    const path = browsePath(this.options.entry.libraryId!);
    const page = parseBrowseResult(await request(this.api, path, 'POST', original, signal));
    if (!this.current() || signal.aborted) return;
    if (page.entries.some(entry => entry.kind !== 'song')) throw new Error(unreadableServerResponse);
    this.songQuery = JSON.stringify([path, original]); this.topSongsRevision = page.pageInfo.revision;
    this.publish({phase:'ready',songs:page.entries,songTotal:page.pageInfo.totalIsLowerBound?undefined:page.pageInfo.total,favorite});
    // The optional entity projection supplies real Details and the Similar
    // revision; neither ever holds the artist's own songs and releases.
    if (!this.current() || signal.aborted) return;
    const details=await this.entityDetails('artist',signal);
    if (!this.current() || signal.aborted) return;
    if(details)this.publish({artistDetails:details});
    await this.loadSimilar(signal);
  }
  /** The page's own bounded entity projection, independent of its track pages.
   * Album format badges are composed by the server, never by browsing songs. */
  private async entityDetails(view: 'album' | 'artist', signal: AbortSignal): Promise<ContentEntry | undefined> {
    const libraryId = this.options.entry.libraryId;
    if (!libraryId) return undefined;
    const path = `/v1/libraries/${encodeURIComponent(libraryId)}/content?view=${view}&entityId=${encodeURIComponent(this.options.entry.id)}&limit=1`;
    const api = {request: async <T>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> => {
      if (!this.current() || signal.aborted) throw membershipFailure('native_music_retired');
      const value = await this.api.request<T>(path, method, body, input);
      if (!this.current() || signal.aborted) throw membershipFailure('native_music_retired');
      return value;
    }};
    const service = new LibraryContentService({api, scope: this.options.lifetime.scope, pageSize: 1});
    let retained=false;
    const abort = () => service.dispose();
    signal.addEventListener('abort', abort, {once: true});
    try {
      if (!this.current() || signal.aborted) return undefined;
      await service.select({view, libraryId, entityId: this.options.entry.id});
      if (!this.current() || signal.aborted) return undefined;
      const snapshot=service.getSnapshot(),projection=snapshot.projection,entry=projection?.entity;
      if(snapshot.phase!=='ready'||!projection||entry?.id!==this.options.entry.id||entry.kind!==view||entry.libraryId!==libraryId)return;
      this.entityWitness?.service.dispose();
      this.entityWitness=Object.freeze({service,projection,signal});retained=true;
      return entry;
    } catch (error) {
      if (!signal.aborted) diagnose('details', 'GET', path, error);
      return undefined;
    } finally {
      signal.removeEventListener('abort', abort);
      if(!retained)service.dispose();
    }
  }
  private async artistAlbums(signal: AbortSignal): Promise<void> {
    const generation=this.generation,originalPath=encodedRoutePath('get_artists_id_albums',{id:encodeURIComponent(this.options.entry.id)})+'?limit=100';
    const pager=new MusicSectionPager<MusicAlbumRow>({
      current:()=>this.current()&&generation===this.generation&&!signal.aborted,
      read:async(cursor,input)=>{
        const raw=await request<{albums?:unknown;nextCursor?:unknown}>(this.api,
          originalPath+(cursor?`&cursor=${encodeURIComponent(cursor)}`:''),'GET',undefined,input);
        if(!text(raw?.nextCursor,4096))throw membershipFailure('music_section_unreadable');
        const items=list(raw?.albums,100).flatMap(row=>{const value=album(row);return value?[value]:[];});
        return {items,nextCursor:raw.nextCursor};
      },
      publish:section=>this.publish({releases:section,albums:section.items}),
    });
    this.releasePager=pager;await pager.load();
  }
  /** Complete authorized album membership for a whole-album action. The page
   * viewport is not membership: every genuine server page is followed under one
   * revision and total fence, or the action refuses instead of adding a subset. */
  albumMembership = async (): Promise<readonly string[]> => {
    if (!this.current()) throw Object.assign(new Error('native_music_retired'), {name: 'AbortError'});
    const libraryId = this.options.entry.libraryId;
    if (this.options.entry.kind !== 'album' || !libraryId) throw membershipFailure('album_membership_required');
    const controller = new AbortController();
    this.membershipController?.abort();
    this.membershipController = controller;
    const signal = controller.signal, retired = () => Object.assign(new Error('native_music_retired'), {name: 'AbortError'});
    try {
      const capabilities = await this.capabilities('songs', signal);
      if (!this.current() || signal.aborted) throw retired();
      const limit = Math.min(200, Math.max(1, capabilities.queryLimits.maximumLimit));
      const builder = new BrowseQueryBuilder(capabilities).where('memberOf', 'equals', this.options.entry.id).sort('groupOrder', 'asc').limit(limit);
      const ids: string[] = [], seen = new Set<string>();
      let revision = '', total = 0, maxPages = 1;
      for (let pageNumber = 0; pageNumber < maxPages; pageNumber++) {
        if (!this.current() || signal.aborted) throw retired();
        const page = parseBrowseResult(await request(this.api, browsePath(libraryId), 'POST', builder.build(), signal));
        if (!this.current() || signal.aborted) throw retired();
        if (page.pageInfo.totalIsLowerBound) throw membershipFailure('album_membership_incomplete');
        if (pageNumber === 0) {
          revision = page.pageInfo.revision; total = page.pageInfo.total;
          maxPages = Math.min(ALBUM_MEMBERSHIP_PAGE_LIMIT, Math.max(1, Math.ceil(total / limit)));
        } else if (page.pageInfo.revision !== revision || page.pageInfo.total !== total) {
          throw membershipFailure('album_membership_changed');
        }
        for (const entry of page.entries) {
          if (entry.kind !== 'song' || seen.has(entry.id)) throw membershipFailure('album_membership_incomplete');
          seen.add(entry.id); ids.push(entry.id);
        }
        if (!page.pageInfo.hasMore) break;
        if (!page.pageInfo.nextCursor || pageNumber + 1 >= maxPages) throw membershipFailure('album_membership_incomplete');
        builder.cursor(page.pageInfo.nextCursor);
      }
      if (ids.length !== total) throw membershipFailure('album_membership_incomplete');
      if (!ids.length) throw membershipFailure('album_membership_empty');
      return Object.freeze([...ids]);
    } finally {
      if (this.membershipController === controller) this.membershipController = undefined;
    }
  };
  /** Open the artist's own All songs section; tail commands page only this query. */
  loadAllSongs = async (): Promise<void> => {
    if(!this.current()||this.options.entry.kind!=='artist'||this.state.allSongs.phase==='loading')return;
    this.allSongPager?.dispose();this.allSongPager=undefined;
    this.allSongsController?.abort();
    const controller=new AbortController(),signal=controller.signal,generation=this.generation;
    this.allSongsController=controller;
    this.publish({allSongs:Object.freeze({...emptyMusicSection<ContentEntry>(),phase:'loading',songs:[]})});
    try{
      const capabilities=await this.capabilities('songs',signal);
      if(!this.current()||signal.aborted||generation!==this.generation)return;
      const pager=this.songSection(capabilities,builder=>builder.where('artist','equals',this.options.entry.title).sort('title','asc')
        .limit(Math.min(200,capabilities.queryLimits.maximumLimit)),generation,true);
      this.allSongPager=pager;await pager.load();
    }catch(error){
      if(this.current()&&!signal.aborted&&generation===this.generation)this.publish({allSongs:Object.freeze({...emptyMusicSection<ContentEntry>(),phase:'error',songs:[],error})});
    }
  };
  play = (anchorItemId?: string, shuffle = false): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    const port = this.options.playback?.cataloguePlayback?.();
    if (!port) return Promise.reject(Object.assign(new Error('music_playback_unavailable'), {code: 'music_playback_unavailable'}));
    this.playController?.abort();
    const controller = new AbortController();
    this.playController = controller;
    const isCurrent = () => !controller.signal.aborted && this.current();
    return port.playSegments([{
      source: {container: {kind: this.options.entry.kind, id: this.options.entry.id}},
      ...(shuffle ? {order: {mode: 'shuffle' as const}} : {}),
      ...(anchorItemId ? {anchor: {itemId: anchorItemId}} : {}),
    }], {signal: controller.signal, isCurrent}).then(() => undefined);
  };
  toggleFavorite = async (): Promise<void> => {
    if (!this.current()) return;
    const controller = new AbortController();
    const state: ContainerState = await setContainerFlag(this.api, this.options.entry.kind as 'album' | 'artist', this.options.entry.id, 'favorite', !this.state.favorite, controller.signal);
    if (this.current()) this.publish({favorite: state.favorite});
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true;
    this.similarRow?.dispose(); this.similarRow = undefined;
    for (const operation of this.songFavoriteOperations.values()) operation.controller.abort();
    this.songFavoriteOperations.clear();
    this.generation++;
    this.controller?.abort();
    this.playController?.abort();
    this.membershipController?.abort();
    this.allSongsController?.abort();
    this.songPager?.dispose();this.releasePager?.dispose();this.allSongPager?.dispose();
    this.pending = undefined;
    this.entityWitness?.service.dispose();this.entityWitness=undefined;
    this.listeners.clear();
  };
}
