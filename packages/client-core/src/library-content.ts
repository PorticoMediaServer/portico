import {parseMusicTechnicalFacts, type MusicTechnicalFacts} from './music-facts.ts';
import {parseMusicBadges, type MusicFormatBadge} from './music-badges.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {wireDurations,wireDisplayDurations} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {listeningData,parseBookProgress,type BookProgress,type ListeningViewerScope,parseListeningJourney,type ListeningJourney} from './listening.ts';
import {parseBrowseNode,type BrowseNode,type BrowseSortSelection} from './browse.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
import {mergeRevalidatedList} from './freshness-actions.ts';
import {parseHomeActions, type HomeActions} from './home.ts';
import type {FetchPage} from './collections/windowed.ts';
import type {UserMutationCoordinator, UserMutationScope} from './user-mutations.ts';
/** Transport/state for server-authored semantic projections. No client ranking or grouping. */
export type ContentView = 'discover' | 'browse' | 'collections' | 'categories' | 'show' | 'season' | 'artist' | 'album' | 'book' | 'collection' | 'releases' | 'songs' | 'authors' | 'series' | 'author' | 'book_series' | 'disc';
export type ContentSurface = ContentView;
export type ContentRoute = Readonly<{ sort?: string; direction?: 'asc' | 'desc'; category?: string; q?: string } & { libraryId: string; view: ContentView; entityId?: string }>;
export type ContentScope = Readonly<{ viewerId: string; serverId: string }>;
/** One caller opening's signal and its own currentness predicate. */
export type ContentReadContext = Readonly<{ signal: AbortSignal; isCurrent: () => boolean }>;
export interface LibraryContentApi {
  request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>;
  acknowledgeUserMutation?(intentId: string): Promise<void>;
  awaitUserMutationsReady?(): Promise<void>;
  getUserMutationCoordinator?(): UserMutationCoordinator | undefined;
  getUserMutationScope?(): UserMutationScope | undefined;
  requestRaw?(path: string, method: string, options?: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal; deadlineMs?: number; singleAttempt?: boolean}>): Promise<import('./read-preparation.ts').PreparationResponse>;
}
export type ContentEntryKind = 'ebook' | 'comic' | 'movie' | 'show' | 'season' | 'episode' | 'artist' | 'album' | 'song' | 'book' | 'audiobook_file' | 'chapter' | 'collection' | 'category' | 'author' | 'book_series' | 'disc' | 'extra' | 'playlist' | 'radio';
/** `params` fill a parameterised heading (a Discover personal row: "{genre} for you"); name it with `serverTextLabel`. */
export type ContentHeading = Readonly<{ key: string; fallback: string; params?: Readonly<Record<string, string>> }>;
/** Explicit display fact on an already admitted Reading leaf. Commands still
 * obtain their own viewer-fenced mark response; progress never supplies this. */
export type ReadingEntryMark = Readonly<{itemId: string; read: boolean; revision: number}>;
export type ContentEntry = Readonly<{
  /** A displayed container's aggregate selection offers on Browse and content pages (MOB-030). An offer grants nothing by itself. */
  homeActions?: HomeActions;
  /** The original admitted radio seed; a radio card is not a catalogue song. */
  radioSeed?: Readonly<{id: string; kind: 'artist' | 'album' | 'song'}>;
  pageCount?: number;
  readingMark?: ReadingEntryMark;
  audioFacts?: MusicTechnicalFacts;
  badges?: readonly MusicFormatBadge[];
  publicationType?: 'book' | 'magazine' | 'newspaper';
  dateBased?:boolean;special?:boolean;new?:boolean; show?:Readonly<{id:string;name:string}>;
  favorite?:boolean;releaseDate?:string;artistType?:'Group'|'Person';albumCount?:number;displayTitle?:string;narrators?:readonly string[];series?:readonly Readonly<{name:string;position?:string}>[];publisher?:string;language?:string;edition?:string;publishedYear?:number;bookProgress?:BookProgress;
  id: string; libraryId?: string; kind: ContentEntryKind; title: string; subtitle?: string; posterUrl?: string; backdropUrl?: string; /** An episode's own still, never inherited from the season or show (absent when it has none). */ stillUrl?: string; overview?: string;
  /** The letter a browse page files the entry under (its sort title's, articles already set aside): a key of the page's `positionIndex`. */
  sortLetter?: string;
  missing?:boolean; available?: boolean; watched?:boolean;discNumber?:number;trackNumber?:number;episodeNumber?:number;seasonNumber?:number; duration?: number; progressSeconds?: number; addedAt?: string | null; count?: number;
  /** A container's watched tally, when the server publishes one. Absent is not zero. */
  watchedCount?: number; unwatchedCount?: number;
  year?: number; contentRating?: string; genres?: readonly string[]; watchlisted?: boolean;
  /** The album artist; album entities only (M24, behind presence). Absent on older servers. */
  artist?: Readonly<{ id: string; name: string }>;
  /** Accepted Reading author or a book/audiobook part's semantic author, never parsed from subtitle. */
  author?: string;
  /** How many times the viewer has played a song to the end; song entries only, absent when never played. */
  plays?: number;
  /** A song's album; song entries only. */
  album?: Readonly<{ id: string; name: string }>;
  /** The release's relationship to the artist; artist releases only. Unknown values are dropped. */
  role?: 'album' | 'ep' | 'single' | 'compilation' | 'appearance';
  /** Up to 4 poster paths for a 2x2 mosaic; category rows only. Absent when there is nothing to show. */
  /** `count` is a lower bound: a grouping under a broad filter counts each group up to 10,000 ("10,000+"). */
  countIsLowerBound?: boolean;
  removed?:boolean;
  artworkPaths?: readonly string[];
  yearMin?:number; yearMax?:number; origin?:'automatic'|'owner';
  navigation?: Readonly<{ view: ContentView | 'item' | 'playlist' | 'radio'; entityId?: string; category?: string; browse?: ContentSeeAll }>;
  playback?: Readonly<{ itemId: string; startSeconds?: number }>;
  /** An episode's air date (YYYY-MM-DD): show workspace pages, and episode rows of a browse page. */
  airDate?: string;
  /** A show's most recently aired episode, on a Recently aired page: the card is the show's, its caption the episode's. */
  latestEpisode?: Readonly<{airDate: string; seasonNumber?: number; episodeNumber: number; title?: string; count: number}>;
  /** An episode's number across the show's seasons, when the show is set to absolute numbering: it reads "Episode 1043". */
  absoluteNumber?: number;
  /** How many seasons a show has, on a Recently Added shelf when the whole show (several seasons) was the addition: "6 seasons". */
  seasonCount?: number;
  /** Community rating (0–10) and the best file's picture class; browse pages only (the list view's columns). */
  rating?: number;
 userRating?: number;
  resolution?: '4k' | '1080p' | '720p' | 'sd';
  /** Where a provider biography came from ("Wikipedia") and its article; artist entities only. */
  overviewSource?: string;
  overviewSourceUrl?: string;
  /** The artist's country code and active years (no end year: still active); artist entities only. */
  country?: string;
  activeBeginYear?: number;
  activeEndYear?: number;
  /** The album's record label and release-group type (Album, EP, Single, Compilation); album entities only. */
  label?: string;
  albumType?: string;
}>;
/** A Discover personal section's See all (Recommendations P6): the library's browse with exactly this pivot, query and sort (For you). */
export type ContentSeeAll = Readonly<{ pivot: string; query?: BrowseNode; sort: readonly BrowseSortSelection[] }>;
export type ContentSection = Readonly<{ id: string; type: 'rail' | 'grid' | 'list'; heading: ContentHeading; entries: readonly ContentEntry[]; totalCount: number; nextCursor: string; /** First entry's index in the whole section (M20): the requested `start`, a listening continuation's resume offset, else 0. Optional so older projections still parse. */ start?: number; seeAll?: ContentSeeAll }>;
export type ContentFilterOption = Readonly<{ id: string; label: string; count: number }>;
export type ContentFilter = Readonly<{ id: string; labelKey: string; options: readonly ContentFilterOption[] }>;
export type ContentProjection = Readonly<{
  heading: ContentHeading;
  entity?: ContentEntry;
  listening?: ListeningJourney;
  scope: Readonly<{ serverId: string; libraryId: string; libraryKind: string; view: ContentSurface; entityId: string; viewerFence: string }>;
  revision: Readonly<{ catalog: number; viewer: number }>;
  navigation: readonly Readonly<{ id: string; labelKey: string; view: ContentView }>[];
  query: Readonly<{ sort: string; direction: string; category: string; q: string; limit: number; searchMode: 'none' | 'title_prefix' }>;
  sorts: readonly Readonly<{ id: string; labelKey: string; directions: readonly ('asc' | 'desc')[] }>[];
  filters: readonly ContentFilter[];
  sections: readonly ContentSection[];
  empty?: ContentHeading;
}>;
export type LibraryContentSnapshot = Readonly<{
  generation: number; scope: ContentScope; route: ContentRoute | null;
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  projection: ContentProjection | null;
  sections: readonly ContentSection[];
  pagination: Readonly<{ cursor: string | null; canPrevious: boolean; next: readonly Readonly<{ sectionId: string; cursor: string }>[] }>;
  error: Readonly<{ code: string; message: string; retryable: boolean }> | null;
  /**
   * The first page as the server has it now, read again quietly while the screen was open
   * (`revalidate`). `serial` changes with each such read. A screen that has appended later pages
   * merges this into what it shows (`mergeRevalidatedList`) instead of starting over.
   */
  head?: Readonly<{ serial: number; sections: readonly ContentSection[] }>;
  /**
   * Everything read so far, as one list per section: the first page, each later page of a
   * section appended to it (duplicates dropped), and quiet reads merged in place. A screen that
   * shows "More" inside a section renders this; `sections` is only the page last read.
   */
  appended: readonly ContentSection[];
}>;
const views: readonly string[] = ['discover','browse','collections','categories','show','season','artist','album','book','collection','releases','songs','authors','series','author','book_series','disc'];
const entityViews = new Set(['show','season','artist','album','book','collection','author','book_series','disc']);
const entryKinds: readonly string[] = ['ebook','comic','movie','show','season','episode','artist','album','song','book','audiobook_file','chapter','collection','category','author','book_series','disc','extra','playlist','radio'];
/** Unknown string kinds are future server cards; only their entries are omitted. */
export const unknownContentEntryKind = (v: unknown): boolean => record(v) && typeof v.kind === 'string' && !entryKinds.includes(v.kind);
/** The closed set, as a guard: a validated kind is never narrowed by a cast. */
export const contentEntryKind = (v: unknown): v is ContentEntryKind => typeof v === 'string' && entryKinds.includes(v);
const optional = <K extends string, V>(key: K, value: V | undefined): Partial<Record<K, V>> => (value === undefined ? {} : { [key]: value } as Record<K, V>);
const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const string = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const synopsis = (v: unknown): v is string => typeof v === 'string' && v.length <= 16384 && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => string(v, 256) && v.length > 0;
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;
class ContentFailure extends Error {
  code: string; retryable: boolean;
  constructor(code: string, message: string, retryable = false) { super(message); this.code = code; this.retryable = retryable; }
}
function invalid(): never { throw new ContentFailure('invalid_projection', unreadableServerResponse); }
function boundedArray(v: unknown, max: number): unknown[] { if (!Array.isArray(v) || v.length > max) invalid(); return v; }
/** A part of a projection that could not be read was left out; an absent or null value is not counted. */
function left(part: string, value: unknown): void { if (value !== undefined && value !== null) noteSkipped('content', part); }
/** A heading keeps its words when its parameters cannot be read; the fallback then names it. */
function heading(v: unknown): ContentHeading {
  if (!record(v) || !id(v.key) || !string(v.fallback, 512)) invalid();
  if (v.params === undefined || v.params === null) return Object.freeze({ key: v.key, fallback: v.fallback });
  if (!record(v.params) || Object.keys(v.params).length > 16) { left('heading.params', v.params); return Object.freeze({ key: v.key, fallback: v.fallback }); }
  const params: Record<string, string> = {};
  for (const [name, value] of Object.entries(v.params)) {
    if (!id(name) || !string(value, 1024)) { left('heading.params', value); continue; }
    params[name] = value as string;
  }
  return Object.freeze({ key: v.key, fallback: v.fallback, params: Object.freeze(params) });
}
/** A heading that cannot be read is blank under `key`, so its section or page is still shown. */
function headingOr(v: unknown, key: string): ContentHeading {
  try { return heading(v); } catch { left('heading', v); return Object.freeze({ key, fallback: '' }); }
}
/** A See all destination that cannot be read is left out, never its section or row. */
function seeAll(v: unknown): ContentSeeAll | undefined {
  if (v === undefined || v === null) return undefined;
  try {
    if (!record(v) || !id(v.pivot) || !Array.isArray(v.sort) || v.sort.length < 1 || v.sort.length > 3) invalid();
    const sort = (v.sort as unknown[]).map(s => {
      if (!record(s) || !id(s.field) || (s.direction !== 'asc' && s.direction !== 'desc')) invalid();
      return Object.freeze({ field: s.field, direction: s.direction as 'asc' | 'desc' });
    });
    const query = v.query === undefined || v.query === null ? undefined : parseBrowseNode(v.query);
    return Object.freeze({ pivot: v.pivot, ...(query ? { query } : {}), sort: Object.freeze(sort) });
  } catch { noteSkipped('content', 'seeAll'); return undefined; }
}
/**
 * One entry. Its id, kind, library and destination are exact: an entry whose kind or destination
 * this app does not know is refused, and the caller leaves out that entry alone. Every other
 * field is optional and is left out when it cannot be read.
 */
function entry(v: unknown): ContentEntry {
 v=wireDisplayDurations(v,{duration:"durationMs",progressSeconds:"progressMs"},left);
  if (!record(v) || !id(v.id) || !entryKinds.includes(v.kind as string) || !string(v.title ?? '', 2048)) invalid();
  if ((v.kind==='ebook'||v.kind==='comic') && (v.playback!=null||v.durationMs!=null||v.duration!=null)) invalid();
  const row: { -readonly [K in keyof ContentEntry]: ContentEntry[K] } = { id: v.id, kind: v.kind as ContentEntryKind, title: (v.title ?? '') as string };
  if (v.kind === 'radio') {
    const seed = v.radioSeed;
    if (!record(seed) || !id(seed.id) || !['artist', 'album', 'song'].includes(seed.kind as string)
      || v.id !== `radio:${seed.id}` || v.playback != null) invalid();
    row.radioSeed = Object.freeze({id: seed.id as string, kind: seed.kind as 'artist' | 'album' | 'song'});
  }
  if ((v.kind === 'ebook' || v.kind === 'comic') && (v.publicationType === 'book' || v.publicationType === 'magazine' || v.publicationType === 'newspaper')) row.publicationType = v.publicationType;
  if (v.kind === 'ebook' || v.kind === 'comic') {
    if (count(v.pageCount) && v.pageCount > 0) row.pageCount = v.pageCount; else left('entry.pageCount', v.pageCount);
    const mark = v.readingMark;
    if (mark !== undefined) {
      if (record(mark) && mark.itemId === row.id && typeof mark.read === 'boolean' && count(mark.revision)) {
        row.readingMark = Object.freeze({itemId: row.id, read: mark.read, revision: mark.revision});
      } else left('entry.readingMark', mark);
    }
  }
  if (v.audioFacts !== undefined && v.kind === 'song') {
    const facts = parseMusicTechnicalFacts(v.audioFacts);
    if (facts) row.audioFacts = facts; else left('entry.audioFacts', v.audioFacts);
  }
  if (v.badges !== undefined && v.kind === 'album') {
    const badges = parseMusicBadges(v.badges);
    if (badges) row.badges = badges; else left('entry.badges', v.badges);
  }
  if (v.libraryId !== undefined) { if (!id(v.libraryId)) invalid(); row.libraryId = v.libraryId; }
  if (v.homeActions !== undefined && ['show', 'season', 'album', 'artist', 'book', 'collection'].includes(v.kind as string)) {
    const actions = parseHomeActions(v.homeActions);
    if (actions) row.homeActions = actions; else left('entry.homeActions', v.homeActions);
  }
  if (synopsis(v.overview)) row.overview = v.overview; else left('entry.overview', v.overview);
  for (const field of ['subtitle', 'posterUrl', 'backdropUrl', 'stillUrl'] as const) {
    if (string(v[field], 4096)) row[field] = v[field] as string; else left('entry.' + field, v[field]);
  }
  if (v.addedAt === null || string(v.addedAt, 128)) row.addedAt = v.addedAt as string | null; else left('entry.addedAt', v.addedAt);
  if (string(v.sortLetter, 8) && v.sortLetter) row.sortLetter = v.sortLetter as string; else left('entry.sortLetter', v.sortLetter);
  if (count(v.count)) row.count = v.count; else left('entry.count', v.count);
  if (count(v.totalAtLeast) && row.count !== undefined) row.countIsLowerBound = true; else left('entry.totalAtLeast', v.totalAtLeast);
  // A container's tallies stay exact nonnegative counts; absence stays absent, never zero.
  for (const field of ['watchedCount', 'unwatchedCount'] as const) {
    if (count(v[field])) row[field] = v[field] as number; else left('entry.' + field, v[field]);
  }
  for(const flag of ['dateBased','special'] as const)if(typeof v[flag]==='boolean')row[flag]=v[flag] as boolean;
  if(typeof v.watched==='boolean')row.watched=v.watched;else left('entry.watched',v.watched);
  if(typeof v.new==='boolean')row.new=v.new;else left('entry.new',v.new);
  if(record(v.show)&&id(v.show.id)&&string(v.show.name,2048))row.show=Object.freeze({id:v.show.id,name:v.show.name});else left('entry.show',v.show);
  for(const field of ['discNumber','trackNumber','episodeNumber','seasonNumber'] as const){if(count(v[field]))row[field]=v[field] as number;else left('entry.'+field,v[field]);}
  if (typeof v.missing === 'boolean') row.missing=v.missing;
  if (typeof v.available === 'boolean') row.available = v.available; else left('entry.available', v.available);
  for (const field of ['duration', 'progressSeconds'] as const) {
    if (finite(v[field])) row[field] = v[field] as number; else left('entry.' + field, v[field]);
  }
  if(v.userRating!==undefined){if(typeof v.userRating=== "number"&&Number.isInteger(v.userRating)&&v.userRating>=0&&v.userRating<=10)row.userRating=v.userRating;else left("entry.userRating",v.userRating);}
  // A year of 0 is how a server says it has none.
  if (typeof v.year === 'number' && Number.isSafeInteger(v.year) && v.year > 0) row.year = v.year; else if (v.year !== 0) left('entry.year', v.year);
  if (string(v.contentRating, 64)) row.contentRating = v.contentRating as string; else left('entry.contentRating', v.contentRating);
  // Genres degrade (§4a): be/pages sends up to three for artists and more for albums; the
  // readable ones are kept (at most 8), anything else is dropped, never fatal.
  if (Array.isArray(v.genres)) {
    const genres = (v.genres as unknown[]).filter(g => string(g, 128) && (g as string).length > 0) as string[];
    if (genres.length) row.genres = Object.freeze(genres);
  }
  if(typeof v.favorite==='boolean')row.favorite=v.favorite;else left('entry.favorite',v.favorite);
  if(typeof v.albumCount==='number'&&count(v.albumCount))row.albumCount=v.albumCount;else left('entry.albumCount',v.albumCount);
  for(const key of ['releaseDate','displayTitle','publisher','language','edition'] as const){if(string(v[key],2048)&&v[key])row[key]=v[key] as string;else left('entry.'+key,v[key]);}
  if(v.artistType==='Group'||v.artistType==='Person')row.artistType=v.artistType;else left('entry.artistType',v.artistType);
  if(count(v.publishedYear)&&v.publishedYear>0&&v.publishedYear<=9999)row.publishedYear=v.publishedYear;else left('entry.publishedYear',v.publishedYear);
  if(Array.isArray(v.narrators))row.narrators=Object.freeze(v.narrators.filter((n):n is string=>string(n,1024)&&!!n).slice(0,128));
  if(Array.isArray(v.series)){row.series=Object.freeze(v.series.filter(n=>record(n)&&string(n.name,1024)&&!!n.name&&(n.position===undefined||string(n.position,128))).slice(0,64).map(n=>Object.freeze({name:n.name as string,...(n.position?{position:n.position as string}:{})})));}
  if(v.bookProgress!==undefined){try{row.bookProgress=parseBookProgress(v.bookProgress);if(row.kind==='book'&&row.bookProgress.bookId!==row.id||row.kind==='audiobook_file'&&row.bookProgress.itemId!==row.id)throw new Error('Wrong book identity');}catch{delete row.bookProgress;left('entry.bookProgress',v.bookProgress);}}
  if (typeof v.watchlisted === 'boolean') row.watchlisted = v.watchlisted; else left('entry.watchlisted', v.watchlisted);
  // M24 (behind presence): album artist link, release role and category mosaic
  // paths. All three are optional; malformed or unknown values are dropped,
  // never fatal, so older and newer servers both parse.
  if (v.artist !== undefined) {
    const a = v.artist;
    if (record(a) && id(a.id) && string(a.name, 2048) && (a.name as string).length > 0) {
      row.artist = Object.freeze({ id: a.id as string, name: a.name as string });
    }
  }
  if (string(v.author, v.kind === 'ebook' || v.kind === 'comic' ? 4096 : 512) && (v.author as string).length > 0) row.author = v.author as string;
  if (typeof v.plays === 'number' && Number.isSafeInteger(v.plays) && v.plays >= 1) row.plays = v.plays; else if (v.plays !== 0) left('entry.plays', v.plays);
  if (v.album !== undefined) {
    const a = v.album;
    if (record(a) && id(a.id) && string(a.name, 2048) && (a.name as string).length > 0) {
      row.album = Object.freeze({ id: a.id as string, name: a.name as string });
    }
  }
  if (v.role !== undefined) {
    if (v.role === 'album' || v.role === 'ep' || v.role === 'single' || v.role === 'compilation' || v.role === 'appearance') row.role = v.role;
  }
  for(const field of ['yearMin','yearMax'] as const)if(typeof v[field]==='number'&&Number.isInteger(v[field])&&(v[field] as number)>0)row[field]=v[field] as number;
  if(v.origin==='automatic'||v.origin==='owner')row.origin=v.origin;
  if(v.removed===true)row.removed=true;
  if (v.artworkPaths !== undefined) {
    if (Array.isArray(v.artworkPaths)) {
      const paths = (v.artworkPaths as unknown[]).filter(p => string(p, 4096) && (p as string).length > 0).slice(0, 4) as string[];
      if (paths.length) row.artworkPaths = Object.freeze(paths);
    }
  }
  // Page-content facts (Spec — Page Content by Media Type): all optional; a malformed value is dropped, never fatal.
  if (string(v.airDate, 10) && /^\d{4}-\d{2}-\d{2}$/.test(v.airDate as string)) row.airDate = v.airDate as string;
  if (typeof v.latestEpisode === 'object' && v.latestEpisode !== null) {
    const l = v.latestEpisode as Record<string, unknown>;
    if (typeof l.airDate === 'string' && /^\d{4}-\d{2}-\d{2}/.test(l.airDate) && Number.isSafeInteger(l.episodeNumber) && Number.isSafeInteger(l.count) && (l.count as number) > 0) {
      row.latestEpisode = Object.freeze({airDate: l.airDate.slice(0, 10), episodeNumber: l.episodeNumber as number, count: l.count as number, ...(Number.isSafeInteger(l.seasonNumber) ? {seasonNumber: l.seasonNumber as number} : {}), ...(typeof l.title === 'string' && l.title.length <= 2048 && l.title ? {title: l.title} : {})});
    }
  }
  if (typeof v.absoluteNumber === 'number' && Number.isSafeInteger(v.absoluteNumber) && v.absoluteNumber > 0) row.absoluteNumber = v.absoluteNumber;
  if (typeof v.seasonCount === 'number' && Number.isSafeInteger(v.seasonCount) && v.seasonCount > 0) row.seasonCount = v.seasonCount;
  if (typeof v.rating === 'number' && Number.isFinite(v.rating) && v.rating > 0 && v.rating <= 10) row.rating = v.rating;
  if (v.resolution === '4k' || v.resolution === '1080p' || v.resolution === '720p' || v.resolution === 'sd') row.resolution = v.resolution;
  if (string(v.overviewSource, 128) && (v.overviewSource as string).length > 0) row.overviewSource = v.overviewSource as string;
  if (string(v.overviewSourceUrl, 4096) && /^https:\/\//.test(v.overviewSourceUrl as string)) row.overviewSourceUrl = v.overviewSourceUrl as string;
  if (string(v.country, 8) && (v.country as string).length > 0) row.country = v.country as string;
  if (typeof v.activeBeginYear === 'number' && Number.isSafeInteger(v.activeBeginYear) && v.activeBeginYear > 0) row.activeBeginYear = v.activeBeginYear;
  if (typeof v.activeEndYear === 'number' && Number.isSafeInteger(v.activeEndYear) && v.activeEndYear > 0) row.activeEndYear = v.activeEndYear;
  if (string(v.label, 256) && (v.label as string).length > 0) row.label = v.label as string;
  if (string(v.albumType, 64) && (v.albumType as string).length > 0) row.albumType = v.albumType as string;
  // Where the entry leads is exact: an entry that cannot say it is not shown.
  if (v.navigation !== undefined && v.navigation !== null) {
    const n = v.navigation;
    if (!record(n) || !(views.includes(n.view as string) || n.view === 'item' || n.view === 'playlist' || n.view === 'radio') || (n.entityId !== undefined && !string(n.entityId, 256)) || (n.category !== undefined && !string(n.category, 256))) invalid();
    if (v.kind === 'radio' && (n.view !== 'radio' || n.entityId !== row.radioSeed?.id) || v.kind !== 'radio' && n.view === 'radio') invalid();
    const browse = seeAll(n.browse);
    if (n.browse !== undefined && !browse) invalid();
    row.navigation = Object.freeze({ view: n.view as ContentView | 'item' | 'playlist' | 'radio', ...(n.entityId === undefined ? {} : { entityId: n.entityId as string }), ...(n.category === undefined ? {} : { category: n.category as string }), ...(browse ? {browse} : {}) });
  }
  // Playback that cannot be read is left out when the entry still opens something; otherwise the entry is refused.
  if (v.playback !== undefined && v.playback !== null) {
    const p = wireDisplayDurations(v.playback,{startSeconds:"startMs"},left);
    if (record(p) && id(p.itemId)) row.playback = Object.freeze({ itemId: p.itemId, ...(finite(p.startSeconds) ? { startSeconds: p.startSeconds } : {}) });
    else if (row.navigation) left('entry.playback', p);
    else invalid();
  }
  return Object.freeze(row);
}
function route(input: ContentRoute): ContentRoute {
  if (!record(input) || !id(input.libraryId) || !views.includes(input.view) || (entityViews.has(input.view) && !id(input.entityId))) throw new Error('Invalid library content route.');
  for (const field of ['entityId', 'sort', 'category', 'q'] as const) if (input[field] !== undefined && !string(input[field], field === 'q' ? 512 : 256)) throw new Error('Invalid library content query.');
  if (input.direction !== undefined && input.direction !== 'asc' && input.direction !== 'desc') throw new Error('Invalid library sort direction.');
  return Object.freeze({ libraryId: input.libraryId, view: input.view, ...(input.entityId === undefined ? {} : { entityId: input.entityId }), ...(input.sort === undefined ? {} : { sort: input.sort }), ...(input.direction === undefined ? {} : { direction: input.direction }), ...(input.category === undefined ? {} : { category: input.category }), ...(input.q === undefined ? {} : { q: input.q }) });
}
function scope(input: ContentScope): ContentScope {
  if (!record(input) || !id(input.serverId) || !string(input.viewerId, 1024) || !input.viewerId) throw new Error('A bound server and viewer scope is required.');
  return Object.freeze({ serverId: input.serverId, viewerId: input.viewerId });
}
function projection(raw: unknown, expected: ContentRoute, bound: ContentScope, pageSize: number): ContentProjection {
  if (!record(raw) || !record(raw.scope) || !record(raw.revision) || !record(raw.query)) invalid();
  const s = raw.scope, r = raw.revision, q = raw.query;
  if (s.serverId !== bound.serverId || s.libraryId !== (expected.libraryId ?? '') || s.view !== expected.view || s.entityId !== (expected.entityId ?? '') || !id(s.viewerFence) || !id(s.libraryKind)) invalid();
  if (!count(r.catalog) || !count(r.viewer)) invalid();
  // A search mode this app does not know reads as no search.
  const searchMode = oneOfOr('content', 'query.searchMode', q.searchMode, ['none', 'title_prefix'] as const, 'none');
  if (!string(q.sort) || !string(q.direction) || !string(q.category) || !string(q.q) || !count(q.limit) || q.limit < 1 || q.limit > pageSize) invalid();
  for (const field of ['sort','direction','category','q'] as const) if (expected[field] !== undefined && expected[field] !== q[field]) invalid();
  // The lists below keep what can be read: a tab, sort, filter, option, section or entry this app
  // cannot read is left out alone, and a repeated id keeps its first appearance.
  const navigation = firstOfEach('content', 'navigation', keepReadable('content', 'navigation', boundedArray(raw.navigation ?? [], 16), n => {
    if (!record(n) || !id(n.id) || !id(n.labelKey) || !views.includes(n.view as string)) invalid();
    return Object.freeze({ id: n.id, labelKey: n.labelKey, view: n.view as ContentView });
  }), n => n.id);
  const sorts = firstOfEach('content', 'sorts', keepReadable('content', 'sorts', boundedArray(raw.sorts ?? [], 32), n => {
    if (!record(n) || !id(n.id) || !id(n.labelKey)) invalid();
    const directions = [...new Set(boundedArray(n.directions, 2).filter(v => v === 'asc' || v === 'desc'))];
    return Object.freeze({ id: n.id, labelKey: n.labelKey, directions: Object.freeze(directions as ('asc' | 'desc')[]) });
  }), n => n.id);
  const filters = firstOfEach('content', 'filters', keepReadable('content', 'filters', boundedArray(raw.filters ?? [], 16), n => {
    if (!record(n) || !id(n.id) || !id(n.labelKey)) invalid();
    const options = firstOfEach('content', 'filter.options', keepReadable('content', 'filter.options', boundedArray(n.options, 100), o => {
      if (!record(o) || !id(o.id) || !string(o.label ?? '')) invalid();
      return Object.freeze({ id: o.id, label: (o.label ?? '') as string, count: count(o.count) ? o.count : 0 });
    }), o => o.id);
    return Object.freeze({ id: n.id, labelKey: n.labelKey, options: Object.freeze(options) });
  }), n => n.id);
  // Discover carries Continue, Recommended, the personal rows, Trending and Recently added.
  const sections = firstOfEach('content', 'sections', keepReadable('content', 'sections', boundedArray(raw.sections, 24), n => {
    if (!record(n) || !id(n.id) || !string(n.nextCursor ?? '', 4096)) invalid();
    // A layout this app does not know is drawn as a grid, which holds any number of entries.
    const type = oneOfOr('content', 'section.type', n.type, ['rail', 'grid', 'list'] as const, 'grid');
    const entries = firstOfEach('content', 'entries', keepReadable('content', 'entries', boundedArray(n.entries, type === 'rail' ? 12 : 100), v => {
      const row = entry(v);
      if (row.libraryId !== undefined && row.libraryId !== expected.libraryId) invalid();
      return row;
    }), e => e.id);
    // The count is at least the entries kept.
    return Object.freeze({ id: n.id, type, heading: headingOr(n.heading, n.id), entries: Object.freeze(entries), totalCount: Math.max(count(n.totalCount) ? n.totalCount : 0, entries.length), nextCursor: (n.nextCursor ?? '') as string, ...(typeof n.start === 'number' && Number.isSafeInteger(n.start) && (n.start as number) >= 0 ? { start: n.start as number } : {}), ...optional('seeAll', seeAll(n.seeAll)) });
  }), n => n.id);
  // A page none of whose sections could be read is refused rather than shown as empty.
  if (!sections.length && (raw.sections as unknown[]).length) invalid();
  let entity: ContentEntry | undefined;
  // The page's own entity is exact (it is what was asked for); it never plays, so playback on it is ignored.
  if(raw.entity!==undefined && raw.entity!==null && !unknownContentEntryKind(raw.entity)){entity=entry(raw.entity);if(!['artist','album','book','disc','author','book_series'].includes(expected.view)||entity.kind!==expected.view||entity.id!==expected.entityId||entity.libraryId!==expected.libraryId)invalid();if(entity.playback!==undefined){const{playback:_ignored,...rest}=entity;entity=Object.freeze(rest);}}
  // The listening actions and the empty-state words are optional: unreadable ones are left out.
  let listening: ListeningJourney | undefined;
  if (raw.listening !== undefined && raw.listening !== null) { try { listening = parseListeningJourney(raw.listening, s.libraryId as string); } catch { noteSkipped('content', 'listening'); } }
  let empty: ContentHeading | undefined;
  if (raw.empty !== undefined && raw.empty !== null) { try { empty = heading(raw.empty); } catch { noteSkipped('content', 'empty'); } }
  return Object.freeze({ ...(entity?{entity}:{}), ...(listening?{listening}:{}), heading: headingOr(raw.heading, s.view as string), scope: Object.freeze({ serverId: s.serverId as string, libraryId: s.libraryId as string, libraryKind: s.libraryKind as string, view: s.view as ContentSurface, entityId: s.entityId as string, viewerFence: s.viewerFence as string }), revision: Object.freeze({ catalog: r.catalog, viewer: r.viewer }), query: Object.freeze({ sort: q.sort, direction: q.direction, category: q.category, q: q.q, limit: q.limit, searchMode }), navigation: Object.freeze(navigation), sorts: Object.freeze(sorts), filters: Object.freeze(filters), sections: Object.freeze(sections), ...(empty ? { empty } : {}) });
}
type Page = { cursor: string | null; sectionId?: string };
const seenEntries = new WeakMap<ContentSection, Set<string>>();
const sameEntry = (a: ContentEntry, b: ContentEntry) => a === b || JSON.stringify(a) === JSON.stringify(b);
/** One more page of one section, appended to that section; the other sections stay as they are. */
function appendSectionPage(previous: readonly ContentSection[], page: readonly ContentSection[]): readonly ContentSection[] {
  if (page.length !== 1 || !previous.length) return page;
  const next = page[0], index = previous.findIndex(s => s.id === next.id);
  if (index < 0) return page;
  const current = previous[index];
  // The seen set follows the merged section, so a page keys only its own entries.
  const seen = seenEntries.get(current) ?? new Set(current.entries.map(e => e.id));
  const added: ContentEntry[] = [];
  for (const e of next.entries) if (!seen.has(e.id)) { seen.add(e.id); added.push(e); }
  const merged: ContentSection = Object.freeze({ ...current, entries: added.length ? Object.freeze(current.entries.concat(added)) : current.entries, totalCount: next.totalCount, nextCursor: next.nextCursor });
  seenEntries.set(merged, seen);
  return Object.freeze(previous.map((s, i) => (i === index ? merged : s)));
}
/** A fresh first page merged into what is shown: sections follow the server, unchanged ones keep their identity, paged ones keep their tail. */
function mergeHead(previous: readonly ContentSection[], fresh: readonly ContentSection[]): readonly ContentSection[] {
  const before = new Map(previous.map(s => [s.id, s]));
  let changed = previous.length !== fresh.length;
  const sections = fresh.map((section, index) => {
    const old = before.get(section.id);
    if (!old) { changed = true; return section; }
    const entries = mergeRevalidatedList(old.entries, section.entries, e => e.id, { same: sameEntry, complete: !section.nextCursor });
    const keptTail = entries.length > section.entries.length;
    const nextCursor = keptTail ? old.nextCursor : section.nextCursor;
    const { entries: _a, nextCursor: _b, ...oldRest } = old, { entries: _c, nextCursor: _d, ...freshRest } = section;
    if (entries === old.entries && nextCursor === old.nextCursor && JSON.stringify(oldRest) === JSON.stringify(freshRest)) { if (previous[index] !== old) changed = true; return old; }
    changed = true;
    return Object.freeze({ ...section, entries: entries === old.entries ? entries : Object.freeze([...entries]), nextCursor }) as ContentSection;
  });
  return changed ? Object.freeze(sections) : previous;
}
/** Views whose grid section the server opens at any index (`start`), so a page that left memory can be read again. */
const startViews: ReadonlySet<ContentView> = new Set<ContentView>(['browse', 'collection', 'collections', 'artist']);
export const contentViewRehydratable = (view: ContentView): boolean => startViews.has(view);
/**
 * LR05: the page source for a bounded window (`createWindowedCollection` with `maxResidentPages`)
 * over the `items` grid of a start-addressable view. Every page is read by its own index, so the
 * window may evict any page and read it again on back-scroll; nothing is appended without bound.
 * The first page pins the viewer fence and catalogue revision. A later page from another one is a
 * changed library (`stale_continuation`), never spliced into the old offsets. Other views keep
 * `LibraryContentService`'s appended sections: their earlier pages cannot be read again.
 * `onPage` receives each accepted page's projection (heading, sorts, query, scope and revision,
 * without its sections), so a screen can draw its header from the same read as its window.
 */
export function libraryContentPages(options: Readonly<{ api: LibraryContentApi; scope: ContentScope; route: ContentRoute; pageSize?: number; onPage?: (projection: ContentProjection) => void }>): FetchPage<ContentEntry> {
  const bound = scope(options.scope), selected = route(options.route), pageSize = options.pageSize ?? 40;
  if (!contentViewRehydratable(selected.view) || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) throw new Error('Invalid content window.');
  const changed = () => new ContentFailure('stale_continuation', 'The library changed. Refresh to continue.', true);
  let pinned: Readonly<{ viewerFence: string; catalog: number }> | undefined;
  return async (start, count, signal) => {
    if (!Number.isSafeInteger(start) || start < 0 || !Number.isInteger(count) || count < 1 || count > pageSize) throw new Error('Invalid content window page.');
    const query = new URLSearchParams({ view: selected.view, limit: String(count) });
    for (const field of ['entityId','sort','direction','category','q'] as const) if (selected[field] !== undefined) query.set(field, selected[field]!);
    if (start > 0) query.set('start', String(start));
    const raw = await options.api.request<unknown>(encodedRoutePath("get_libraries_id_content", {"id": encodeURIComponent(selected.libraryId)}) + "?" + query, 'GET', undefined, signal);
    const result = projection(raw, selected, bound, pageSize);
    if (pinned && (result.scope.viewerFence !== pinned.viewerFence || result.revision.catalog !== pinned.catalog)) throw changed();
    // The server leaves out an empty section: an empty library at the top, or a list that shrank under a later page.
    const grid = result.sections.find(section => section.id === 'items' && section.type === 'grid');
    if (!grid) { if (start > 0) throw changed(); pinned ??= Object.freeze({ viewerFence: result.scope.viewerFence, catalog: result.revision.catalog }); options.onPage?.(Object.freeze({ ...result, sections: Object.freeze([]) })); return { items: Object.freeze([]), total: 0 }; }
    if ((grid.start ?? 0) !== start || grid.entries.length > count) invalid();
    pinned ??= Object.freeze({ viewerFence: result.scope.viewerFence, catalog: result.revision.catalog });
    options.onPage?.(Object.freeze({ ...result, sections: Object.freeze([]) }));
    return { items: grid.entries, total: grid.totalCount };
  };
}
type Operation = { route: ContentRoute; page: Page; history: Page[]; fence: ContentProjection | null };
type ContentFlight = { id: object; generation: number; controller: AbortController; context?: ContentReadContext; committed: boolean; onAbort?: () => void; settle?: () => void };
type ContentCheckpoint = Readonly<{ snapshot: LibraryContentSnapshot; history: Page[]; current: Page; operation?: Operation }>;
export class LibraryContentService {
  private api: LibraryContentApi;
  private bound: ContentScope;
  private pageSize: number;
  private timeoutMs: number;
  private listeners = new Set<() => void>();
  private generation = 0;
  private controller?: AbortController;
  private pending?: Promise<void>;
  private activeKey = '';
  private operation?: Operation;
  private history: Page[] = [];
  private current: Page = { cursor: null };
  private disposed = false;
  private quiet?: AbortController;
  private headSerial = 0;
  private snapshot: LibraryContentSnapshot;
  private continuationRecovery: 'restart' | 'refresh-required';
  private flight?: ContentFlight;
  private checkpoint: ContentCheckpoint;
  constructor(options: { api: LibraryContentApi; scope: ContentScope; pageSize?: number; timeoutMs?: number; continuationRecovery?: 'restart' | 'refresh-required' }) {
    this.api = options.api; this.bound = scope(options.scope); this.pageSize = options.pageSize ?? 40; this.timeoutMs = options.timeoutMs ?? 15_000; this.continuationRecovery = options.continuationRecovery ?? 'restart';
    if (!Number.isInteger(this.pageSize) || this.pageSize < 1 || this.pageSize > 100 || !Number.isFinite(this.timeoutMs) || this.timeoutMs <= 0 || this.timeoutMs > 120_000) throw new Error('Invalid content request bounds.');
    if (this.continuationRecovery !== 'restart' && this.continuationRecovery !== 'refresh-required') throw new Error('Invalid continuation recovery policy.');
    this.snapshot = this.empty();
    this.checkpoint = Object.freeze({ snapshot: this.snapshot, history: [], current: { cursor: null } });
  }
  private empty(): LibraryContentSnapshot { return Object.freeze({ generation: this.generation, scope: this.bound, route: null, phase: 'idle', projection: null, sections: Object.freeze([]), appended: Object.freeze([]), pagination: Object.freeze({ cursor: null, canPrevious: false, next: Object.freeze([]) }), error: null }); }
  getSnapshot = (): LibraryContentSnapshot => this.snapshot;
  subscribe = (listener: () => void): (() => void) => { if (this.disposed) return () => {}; this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<LibraryContentSnapshot>): void { if (this.disposed) return; this.snapshot = Object.freeze({ ...this.snapshot, ...patch }); if (this.snapshot.phase !== 'loading') this.checkpoint = Object.freeze({ snapshot: this.snapshot, history: [...this.history], current: this.current, ...(this.operation ? { operation: this.operation } : {}) }); for (const listener of this.listeners) listener(); }
  private retired(context: ContentReadContext): boolean { if (context.signal.aborted) return true; try { return !context.isCurrent(); } catch { return true; } }
  private admission(context?: ContentReadContext, generation = this.generation, flight = this.flight): 'current' | 'retired' | 'superseded' {
    if (this.disposed || generation !== this.generation || flight !== this.flight) return 'superseded';
    const retired = context && this.retired(context);
    // A caller predicate is external code: even returning true can replace us.
    if (this.disposed || generation !== this.generation || flight !== this.flight) return 'superseded';
    return retired ? 'retired' : 'current';
  }
  private admissionRefusal(context?: ContentReadContext): Promise<void> | undefined {
    const state = this.admission(context);
    return state === 'current' ? undefined : state === 'retired' ? Promise.reject(new Error('Content opening retired.')) : Promise.resolve();
  }
  private detach(flight: ContentFlight): void { if (flight.onAbort) flight.context?.signal.removeEventListener('abort', flight.onAbort); flight.onAbort = undefined; }
  private release(flight: ContentFlight): void {
    if (this.flight === flight) {
      this.flight = undefined; this.pending = undefined; this.activeKey = '';
      if (this.controller === flight.controller) this.controller = undefined;
    }
    this.detach(flight);
  }
  private restore(checkpoint: ContentCheckpoint, generation: number): void {
    if (this.disposed || this.generation !== generation || this.flight) return;
    this.history = [...checkpoint.history]; this.current = checkpoint.current; this.operation = checkpoint.operation;
    this.publish({ ...checkpoint.snapshot, generation });
  }
  private retire(flight: ContentFlight): void {
    const owns = this.flight === flight && flight.generation === this.generation && !this.disposed;
    if (!owns) { this.detach(flight); flight.controller.abort(); flight.settle?.(); return; }
    const checkpoint = this.checkpoint;
    this.generation++; const generation = this.generation;
    this.release(flight); flight.controller.abort();
    flight.settle?.();
    if (!flight.committed) this.restore(checkpoint, generation);
  }
  private assertActive(): void { if (this.disposed) throw new Error('Content service is disposed.'); }
  select(input: ContentRoute, options: { cursor?: string | null; context?: ContentReadContext } = {}): Promise<void> {
    this.assertActive();
    const refusal = this.admissionRefusal(options.context); if (refusal) return refusal;
    const selected = route(input.q === undefined ? input : { ...input, q: input.q.trim() }), cursor = options.cursor ?? null;
    if (cursor !== null && (!string(cursor, 4096) || !cursor)) throw new Error('Invalid content cursor.');
    return this.load({ route: selected, page: { cursor }, history: [], fence: null }, options.context);
  }
  /** Explicit refresh always restarts the first page, including after stale_continuation. */
  refresh(context?: ContentReadContext): Promise<void> { this.assertActive(); const refusal = this.admissionRefusal(context); if (refusal) return refusal; return this.snapshot.route ? this.select(this.snapshot.route, { context }) : Promise.resolve(); }
  retry(context?: ContentReadContext): Promise<void> {
    this.assertActive(); const refusal = this.admissionRefusal(context); if (refusal) return refusal;
    if (this.snapshot.phase === 'refresh-required') return this.refresh(context);
    return this.operation ? this.load(this.operation, context) : Promise.resolve();
  }
  /**
   * Read the first page again without touching `phase`, and show it in place. What is on screen
   * stays until the answer is in; a failed read rejects and changes nothing. A service that has
   * moved on to a later page keeps that page and publishes the fresh first page as `head`.
   */
  async revalidate(context?: { signal?: AbortSignal; settle?: () => Promise<void> }): Promise<void> {
    this.assertActive();
    const route = this.snapshot.route;
    if (!route || this.snapshot.phase !== 'ready' || !this.snapshot.projection) return;
    this.quiet?.abort(); const controller = new AbortController(); this.quiet = controller;
    const stop = () => controller.abort();
    context?.signal?.addEventListener('abort', stop, { once: true });
    const generation = this.generation, api = this.api, bound = this.bound;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      const query = new URLSearchParams({ view: route.view, limit: String(this.pageSize) });
      for (const field of ['entityId','sort','direction','category','q'] as const) if (route[field] !== undefined) query.set(field, route[field]!);
      const raw = await Promise.race([
        api.request<RouteResponse<"get_libraries_id_content">>(encodedRoutePath("get_libraries_id_content", {"id": encodeURIComponent(route.libraryId)}) + "?" + (query), 'GET', undefined, controller.signal),
        new Promise<never>((_, reject) => { timer = setTimeout(() => { reject(new ContentFailure('timeout', 'The server took too long to respond. Try again.', true)); controller.abort(); }, this.timeoutMs); }),
      ]);
      if (this.disposed || generation !== this.generation || controller.signal.aborted) return;
      const result = projection(raw, route, bound, this.pageSize);
      await context?.settle?.();
      if (this.disposed || generation !== this.generation || controller.signal.aborted || this.snapshot.phase !== 'ready') return;
      if (result.scope.viewerFence !== this.snapshot.projection?.scope.viewerFence) {
        // An old tail and its cursors belong to the previous visibility policy.
        this.history = []; this.current = { cursor: null };
        this.operation = { route, page: this.current, history: [], fence: null };
        this.publish({ projection: result, sections: result.sections, appended: result.sections, error: null,
          head: Object.freeze({ serial: ++this.headSerial, sections: result.sections }),
          pagination: Object.freeze({ cursor: null, canPrevious: false, next: Object.freeze(result.sections.filter(s => s.nextCursor).map(s => Object.freeze({ sectionId: s.id, cursor: s.nextCursor }))) }) });
        return;
      }
      // Nothing changed on the first page: nothing is published, so nothing re-renders.
      if (this.current.cursor === null && JSON.stringify(result) === JSON.stringify(this.snapshot.projection)) return;
      const head = Object.freeze({ serial: ++this.headSerial, sections: result.sections });
      const appended = mergeHead(this.snapshot.appended, result.sections);
      // A section that did not change is the same object as before, so a list built on it is left alone.
      const before = new Map(this.snapshot.sections.map(s => [s.id, s]));
      const sections = Object.freeze(result.sections.map(s => { const old = before.get(s.id); return old && JSON.stringify(old) === JSON.stringify(s) ? old : s; }));
      if (this.current.cursor === null) this.publish({ projection: result, sections, appended, head, pagination: Object.freeze({ cursor: null, canPrevious: false, next: Object.freeze(result.sections.filter(s => s.nextCursor).map(s => Object.freeze({ sectionId: s.id, cursor: s.nextCursor }))) }) });
      else this.publish({ head, appended });
    } catch (error) {
      if (this.disposed || generation !== this.generation || controller.signal.aborted) return;
      const e = error as { status?: unknown; code?: unknown } | null;
      // The server answered that this is gone or not allowed: that is the screen's state now, not a failed refresh.
      if (e?.status === 401 || e?.status === 403 || e?.status === 404) { await this.refresh(); return; }
      throw error;
    } finally {
      if (timer) clearTimeout(timer);
      context?.signal?.removeEventListener('abort', stop);
      if (this.quiet === controller) this.quiet = undefined;
    }
  }
  next(sectionId: string, context?: ContentReadContext): Promise<void> {
    this.assertActive();
    const refusal = this.admissionRefusal(context); if (refusal) return refusal;
    if (this.snapshot.phase !== 'ready' || !this.snapshot.projection || !this.snapshot.route) return Promise.resolve();
    // The section as shown (every page of it read so far), so More works on any section of the page, not only the one paged last.
    const section = this.snapshot.appended.find(s => s.id === sectionId) ?? this.snapshot.sections.find(s => s.id === sectionId);
    if (!section?.nextCursor) return Promise.resolve();
    return this.load({ route: this.snapshot.route, page: { cursor: section.nextCursor, sectionId }, history: [...this.history, this.current].slice(-63), fence: this.snapshot.projection }, context);
  }
  previous(context?: ContentReadContext): Promise<void> {
    this.assertActive();
    const refusal = this.admissionRefusal(context); if (refusal) return refusal;
    if (this.snapshot.phase !== 'ready' || !this.snapshot.route || !this.history.length) return Promise.resolve();
    return this.load({ route: this.snapshot.route, page: this.history[this.history.length - 1], history: this.history.slice(0, -1), fence: this.snapshot.projection }, context);
  }
  /** A new scoped API must be bound to the new viewer/server; tokens never enter this service. */
  setScope(next: ContentScope, api: LibraryContentApi): void {
    this.assertActive(); const checked = scope(next), old = this.flight; const generation = ++this.generation;
    if (old) this.release(old);
    this.bound = checked; this.api = api;
    this.operation = undefined; this.history = []; this.current = { cursor: null }; this.activeKey = ''; this.pending = undefined;
    this.snapshot = this.empty(); this.checkpoint = Object.freeze({ snapshot: this.snapshot, history: [], current: this.current });
    old?.controller.abort(); old?.settle?.();
    if (!this.disposed && this.generation === generation && !this.flight) this.publish(this.snapshot);
  }
  cancel(): void {
    this.assertActive(); const old = this.flight, route = this.snapshot.route; const generation = ++this.generation;
    if (old) this.release(old);
    this.pending = undefined; this.activeKey = ''; this.operation = undefined; this.history = []; this.current = { cursor: null };
    this.snapshot = Object.freeze({ ...this.empty(), route }); this.checkpoint = Object.freeze({ snapshot: this.snapshot, history: [], current: this.current });
    old?.controller.abort(); old?.settle?.();
    if (!this.disposed && this.generation === generation && !this.flight) this.publish(this.snapshot);
  }
  dispose(): void {
    const old = this.flight, quiet = this.quiet;
    this.disposed = true; this.generation++; this.listeners.clear(); this.quiet = undefined;
    if (old) this.release(old);
    old?.controller.abort(); old?.settle?.(); quiet?.abort();
  }
  private load(op: Operation, context?: ContentReadContext): Promise<void> {
    const refusal = this.admissionRefusal(context); if (refusal) return refusal;
    const key = JSON.stringify([op.route, op.page]);
    if (this.snapshot.phase === 'loading' && this.activeKey === key && this.pending && this.flight?.context === context) return this.pending;
    // Capture and retire the predecessor's exact identity before its abort can reenter.
    const checkpoint = this.checkpoint;
    const old = this.flight;
    let generation = this.generation;
    if (old) {
      generation = ++this.generation;
      this.release(old);
      old.controller.abort();
      old.settle?.();
      // Scope changes, destructive cancellation and disposal win even without a new flight.
      if (this.disposed || this.generation !== generation || this.flight) return Promise.resolve();
    }
    // Recheck the new opening after predecessor cleanup and after any reentrant predicate.
    const admitted = this.admission(context, generation, undefined);
    if (admitted !== 'current') {
      if (admitted === 'retired') { this.restore(checkpoint, generation); return Promise.reject(new Error('Content opening retired.')); }
      return Promise.resolve();
    }
    const controller = new AbortController();
    const flight: ContentFlight = { id: {}, generation: ++this.generation, controller, context, committed: false };
    let settle!: () => void;
    const completion = new Promise<void>(resolve => { settle = resolve; });
    flight.settle = settle;
    this.controller = controller; this.activeKey = key; this.operation = op; this.flight = flight; this.pending = completion;
    const previous = checkpoint.snapshot.route;
    const retain = previous?.view === op.route.view && previous?.libraryId === op.route.libraryId && previous?.entityId === op.route.entityId;
    if (context) {
      flight.onAbort = () => this.retire(flight);
      context.signal.addEventListener('abort', flight.onAbort, { once: true });
      if (context.signal.aborted || this.retired(context) || this.flight !== flight || flight.generation !== this.generation) { this.retire(flight); return completion; }
    }
    this.publish({ generation: flight.generation, route: op.route, phase: 'loading', projection: retain ? checkpoint.snapshot.projection : null, sections: retain ? checkpoint.snapshot.sections : Object.freeze([]), appended: retain ? checkpoint.snapshot.appended : Object.freeze([]), error: null, pagination: retain ? checkpoint.snapshot.pagination : Object.freeze({ cursor: op.page.cursor, canPrevious: false, next: Object.freeze([]) }) });
    // A synchronous subscriber can leave the route, call currentness, or install a successor.
    if (this.admission(context, flight.generation, flight) !== 'current') { this.retire(flight); return completion; }
    const task = this.fetch(op, this.api, this.bound, controller, flight.generation, flight, context, checkpoint);
    const done = () => settle();
    void task.then(done, done);
    return completion;
  }
  private async fetch(op: Operation, api: LibraryContentApi, bound: ContentScope, controller: AbortController, generation: number, flight: ContentFlight, context: ContentReadContext | undefined, checkpoint: ContentCheckpoint): Promise<void> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let onAbort: (() => void) | undefined;
    const owns = () => !this.disposed && generation === this.generation && this.flight === flight;
    const check = () => { if (!owns() || controller.signal.aborted) return false; if (context && this.retired(context)) { if (owns()) this.retire(flight); return false; } return owns(); };
    const checkFailure = () => { if (!owns()) return false; if (context && this.retired(context)) { if (owns()) this.retire(flight); return false; } return owns(); };
    const query = new URLSearchParams({ view: op.route.view, limit: String(this.pageSize) });
    for (const field of ['entityId','sort','direction','category','q'] as const) if (op.route[field] !== undefined) query.set(field, op.route[field]!);
    if (op.page.cursor) query.set('cursor', op.page.cursor);
    const path = encodedRoutePath("get_libraries_id_content", {"id": encodeURIComponent(op.route.libraryId)}) + "?" + (query);
    try {
      if (!check()) return;
      const cancelled = new Promise<never>((_, reject) => { onAbort = () => reject(new ContentFailure('cancelled', 'Library request cancelled.')); controller.signal.addEventListener('abort', onAbort, { once: true }); });
      const raw = await Promise.race([api.request<unknown>(path, 'GET', undefined, controller.signal), cancelled, new Promise<never>((_, reject) => { timer = setTimeout(() => { reject(new ContentFailure('timeout', 'The server took too long to respond. Try again.', true)); controller.abort(); }, this.timeoutMs); })]);
      if (!check()) return;
      const result = projection(raw, op.route, bound, this.pageSize);
      if (op.page.cursor && op.fence && result.scope.viewerFence !== op.fence.scope.viewerFence) throw new ContentFailure('stale_continuation', 'The library changed. Refresh to continue.', true);
      if (op.page.sectionId && (result.sections.length !== 1 || result.sections[0].id !== op.page.sectionId)) invalid();
      const repeats = (c: string) => c === op.page.cursor || op.history.some(p => p.cursor === c);
      const sections = result.sections.some(s2 => s2.nextCursor && repeats(s2.nextCursor)) ? Object.freeze(result.sections.map(s2 => (s2.nextCursor && repeats(s2.nextCursor) ? Object.freeze({ ...s2, nextCursor: '' }) : s2))) : result.sections;
      if (!check()) return;
      flight.committed = true;
      this.release(flight);
      this.history = op.history; this.current = op.page;
      this.publish({ phase: 'ready', projection: result, sections, appended: op.page.cursor === null ? sections : appendSectionPage(checkpoint.snapshot.appended, sections), head: op.page.cursor === null ? undefined : checkpoint.snapshot.head, pagination: Object.freeze({ cursor: op.page.cursor, canPrevious: this.history.length > 0, next: Object.freeze(sections.filter(s => s.nextCursor).map(s => Object.freeze({ sectionId: s.id, cursor: s.nextCursor }))) }) });
    } catch (error) {
      if (!checkFailure()) return;
      // An internal deadline abort is still this flight's retryable timeout; an
      // opening retirement has already invalidated the flight before this point.
      if (error instanceof ContentFailure && error.code === 'cancelled' && !controller.signal.aborted) return;
      const e = error as { code?: unknown; retryable?: unknown; message?: unknown; status?: unknown } | null;
      const stale = e?.code === 'stale_continuation' || (!!op.page.cursor && e?.code === 'invalid_cursor');
      if (stale && op.page.cursor && this.continuationRecovery === 'refresh-required') {
        this.history = [...checkpoint.history]; this.current = checkpoint.current; this.operation = op;
        const code = string(e?.code) && e?.code ? e.code : 'stale_continuation';
        this.publish({ phase: 'refresh-required', projection: checkpoint.snapshot.projection, sections: checkpoint.snapshot.sections, appended: checkpoint.snapshot.appended, head: checkpoint.snapshot.head, error: Object.freeze({ code, message: 'The library changed. Refresh to continue.', retryable: true }), pagination: Object.freeze({ cursor: checkpoint.current.cursor, canPrevious: false, next: Object.freeze([]) }) });
        return;
      }
      // The existing restart policy keeps the supplied opening; a dead opening cannot start an unscoped fallback.
      if (stale && op.page.cursor) {
        if (!checkFailure()) return;
        // This recovery belongs to the original next() promise. Retire its flight
        // before abort can reenter, but let fetch settle that promise only after
        // its own replacement head finishes; load() must not settle it as a
        // superseded independent request while the screen is still loading.
        const recoveryGeneration = ++this.generation;
        this.release(flight);
        controller.abort();
        if (this.disposed || this.generation !== recoveryGeneration || this.flight) return;
        await this.load({ route: op.route, page: { cursor: null }, history: [], fence: null }, context);
        return;
      }
      const code = string(e?.code) && e?.code ? e.code : 'request_failed';
      const transient = !stale && code !== 'invalid_projection' && e?.status !== 401 && e?.status !== 403 && (e?.retryable === true || code === 'timeout' || error instanceof TypeError || typeof e?.status === 'number' && (e.status >= 500 || e.status === 408 || e.status === 429));
      this.publish({ phase: stale ? 'refresh-required' : 'error', projection: transient ? checkpoint.snapshot.projection : null, sections: transient ? checkpoint.snapshot.sections : Object.freeze([]), appended: transient ? checkpoint.snapshot.appended : Object.freeze([]), error: Object.freeze({ code, message: stale ? 'The library changed. Refresh to continue.' : error instanceof Error ? error.message : 'Could not load this library.', retryable: stale || transient }), pagination: Object.freeze({ cursor: op.page.cursor, canPrevious: false, next: Object.freeze([]) }) });
    } finally {
      if (timer) clearTimeout(timer);
      if (onAbort) controller.signal.removeEventListener('abort', onAbort);
      this.release(flight);
    }
  }
}

/** Reuse the canonical strict semantic DTO validation in composed server workspaces. */
export { projection as validateContentProjection };

/** Shared media-entry validation for server-authored cross-library search. It throws for an entry that cannot be shown; callers leave out that entry alone. */
export { entry as validateContentEntry };

/** A See all destination (`{pivot, query?, sort}`), or undefined when it is absent or cannot be read. Never throws. */
export { seeAll as parseContentSeeAll };

/** A server ranking, read lazily when its title/artist page asks for it, a page at a time: `cursor`
 * continues it from the page that returned `nextCursor` (relation-rows.ts `listeningSimilarPager` reads it to its end).
 * This read-only path needs only the viewer facts a scope carries; a caller's fuller queue
 * scope is accepted structurally and its controller identity is never read. */
export async function readListeningSimilar<S extends ListeningViewerScope>(api:LibraryContentApi,scope:S,itemId:string,options:{limit?:number;by?:'sound';cursor?:string;signal?:AbortSignal}={}){
 const q=new URLSearchParams();if(options.limit!==undefined)q.set('limit',String(options.limit));if(options.by!==undefined){if(options.by!=='sound')invalid();q.set('by',options.by);}if(options.cursor)q.set('cursor',options.cursor);
 const raw=listeningData(await api.request(encodedRoutePath('get_items_id_similar',{id:encodeURIComponent(itemId)})+(q.size?'?'+q:''),'GET',undefined,options.signal),scope);
 if(!record(raw)||raw.itemId!==itemId||!id(raw.libraryId)||!record(raw.revision)||!count(raw.revision.catalog)||!count(raw.revision.viewer)||typeof raw.bySound!=='boolean'||!Array.isArray(raw.entries)||raw.entries.length>24||raw.nextCursor!==undefined&&(typeof raw.nextCursor!=='string'||!raw.nextCursor||raw.nextCursor.length>4096))invalid();
 const entries=raw.entries.map(entry);if(entries.some(e=>!['artist','album','song','book'].includes(e.kind)||raw.bySound&&e.kind==='book'||e.playback!==undefined))invalid();
 return Object.freeze({itemId,libraryId:raw.libraryId,bySound:raw.bySound,revision:Object.freeze({catalog:raw.revision.catalog,viewer:raw.revision.viewer}),entries:Object.freeze(entries),...(raw.hasMore===true&&raw.nextCursor?{nextCursor:raw.nextCursor as string}:{})});
}

