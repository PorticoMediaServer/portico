import type {DiscMediaFacts} from './disc-media-facts.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {wireDurations,wireDisplayDurations,durationIntent} from './wire-units.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {isUserMutationKey, parseUserMutationReceipt} from './user-mutations.ts';
import {parseLocalBook,parseLocalAudioPolicy,type LocalAudioPolicy,type LocalBookMetadata} from "./music-metadata.ts";
/** Server-authored detail and explicit personal-state commands; no playback or ranking owner. */
import type { MediaItem } from './index.ts';
import {validateRelatedMovies,type RelatedMovieProjection} from './related-movies.ts';
import {parseSegmentMarkers,type SegmentMarker} from './segment-markers.ts';
import {validateContentEntry,type ContentEntry} from './library-content.ts';
import {validateHomeRow,type HomeRow} from './home.ts';
import {firstOfEach,keepReadable,noteSkipped} from './tolerant.ts';
export type DetailExtraType = 'trailer' | 'featurette' | 'deleted_scene' | 'behind_the_scenes' | 'interview' | 'scene' | 'short' | 'other';
export type DetailExtra = Readonly<{ type: DetailExtraType; label: string; items: readonly ContentEntry[] }>;
export const detailExtraTypes: readonly DetailExtraType[] = ['trailer','featurette','deleted_scene','behind_the_scenes','interview','scene','short','other'];
export type DetailScope = Readonly<{ serverId: string; viewerId: string }>;
export type DetailTarget = Readonly<{ libraryId: string; itemId: string }>;
export interface DetailApi { acknowledgeUserMutation?(intentId: string): Promise<void>; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> }
export type PersonalConflict = Readonly<{id:string;field:string;baseRevision:number;baseValue:unknown;choices:readonly Readonly<{operationId:string;deviceId:string;authoredAt:string;value:unknown}>[]}>;
export type PersonalOfflineMutation = Readonly<{deviceId:string;deviceMutationId:string;sequence:number;baseRevision:number;authoredAt:string}>;
export type PersonalState = Readonly<{ watchlisted:boolean;favorite:boolean;rating:number|null;revision:number;watched:boolean;progressSeconds:number;lastPlayedAt:string;status:'current'|'needs-resolution';conflicts:readonly PersonalConflict[] }>;
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
export type CreditPage = Readonly<{ revision: Readonly<{catalog:number;viewer:number}>; group: 'cast' | 'crew'; credits: readonly DetailCredit[]; total: number; nextCursor?: string }>;
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
  scope: Readonly<{ serverId: string; libraryId: string; itemId: string; viewerFence: string }>;
  revision: Readonly<{ catalog: number; viewer: number }>;
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
export type PersonalIntent = Readonly<{ action: 'watchlist' | 'favorite' | 'watched'; value: boolean } | { action: 'rating'; value: number | null } | { action:'resolution';value:Readonly<{conflictId:string;choiceOperationId:string}> }>;
export type DetailError = Readonly<{ code: string; message: string; retryable: boolean }>;
export type DetailSnapshot = Readonly<{
  generation: number; scope: DetailScope; target: DetailTarget | null;
  phase: 'idle' | 'loading' | 'ready' | 'error'; data: DetailProjection | null; error: DetailError | null;
  pending: readonly Readonly<{ intent: PersonalIntent; phase: 'queued' | 'sending' | 'retry-required' | 'conflict' }>[];
  mutationError: DetailError | null;
}>;
type Receipt = { operationId: string; serverId: string; itemId: string; libraryId: string; viewerFence: string; personal: PersonalState };
type Command = { offline?:PersonalOfflineMutation; intent: PersonalIntent; operationId?: string; expectedRevision?: number; phase: 'queued' | 'sending' | 'retry-required' | 'conflict'; promise: Promise<void>; resolve: () => void };
const obj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => str(v, 256) && v.length > 0;
const integer = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const finite = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0;
const rating = (v: unknown): v is number | null => v === null || typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 10 && Number.isInteger(v);
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
class Failure extends Error { code: string; retryable: boolean; constructor(code: string, message: string, retryable = false) { super(message); this.code = code; this.retryable = retryable; } }
function invalid(): never { throw new Failure('invalid_detail', unreadableServerResponse); }
/** An optional part of a title that could not be read was left out; an absent or null value is not counted. */
function left(part: string, value: unknown): void { if (value !== undefined && value !== null) noteSkipped('detail', part); }
/** An optional part of a title: when it cannot be read it is left out and the page is kept. */
function part<T>(name: string, value: unknown, read: (value: unknown) => T): T | undefined {
  if (value === undefined || value === null) return undefined;
  try { return read(value); } catch { noteSkipped('detail', name); return undefined; }
}
/** One credit row as the detail and the credits pages carry it. A row needs its id; a portrait or person link that cannot be read is left out, and missing words are empty. */
function creditRow(row: unknown, itemId: string): DetailCredit {
  if (!obj(row) || !id(row.id) || !str(row.name ?? '') || !str(row.role ?? '') || !str(row.department ?? '') || !str(row.provider ?? '', 256)) invalid();
  const portrait = str(row.portraitUrl) && row.portraitUrl.startsWith(encodedRoutePath("get_metadata_kind_id_art_role", {"kind": "item", "id": encodeURIComponent(itemId), "role": "portrait"}) + "?") && !/[\\\r\n]/.test(row.portraitUrl) ? row.portraitUrl : undefined;
  if (!portrait && row.portraitUrl) left('credit.portraitUrl', row.portraitUrl);
  return Object.freeze({ id: row.id, name: (row.name ?? '') as string, role: (row.role ?? '') as string, department: (row.department ?? '') as string, provider: (row.provider ?? '') as string,...(portrait?{portraitUrl:portrait}:{}),...(id(row.personId)?{personId:row.personId}:{}) });
}
function array(value: unknown, max: number): unknown[] { if (!Array.isArray(value) || value.length > max) invalid(); return value; }
/**
 * The viewer's own state for a title. Its revision is exact: a change is sent against it. A flag
 * that is not sent reads as off, a rating that cannot be read as none, a status this app does not
 * know as current, and a conflict or choice that cannot be read is left out alone.
 */
function personal(value: unknown): PersonalState {
 value=wireDisplayDurations(value,{progressSeconds:"progressMs"},left);
  if (!obj(value) || !integer(value.revision)) invalid();
  const conflicts=keepReadable('detail','personal.conflicts',array(value.conflicts??[],8),c=>{if(!obj(c)||!id(c.id)||!id(c.field)||!integer(c.baseRevision))invalid();const choices=keepReadable('detail','personal.choices',array(c.choices,100),v=>{if(!obj(v)||!id(v.operationId)||!id(v.deviceId)||!str(v.authoredAt,128))invalid();return Object.freeze({operationId:v.operationId,deviceId:v.deviceId,authoredAt:v.authoredAt,value:v.value});});return Object.freeze({id:c.id,field:c.field,baseRevision:c.baseRevision,baseValue:c.baseValue,choices:Object.freeze(choices)});});
  return Object.freeze({watchlisted:value.watchlisted===true,favorite:value.favorite===true,rating:rating(value.rating)?value.rating:null,revision:value.revision,watched:value.watched===true,progressSeconds:finite(value.progressSeconds)?value.progressSeconds:0,lastPlayedAt:str(value.lastPlayedAt,128)?value.lastPlayedAt:'',status:value.status==='needs-resolution'?'needs-resolution':'current',conflicts:Object.freeze(conflicts)});
}
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
 if(value.action==='resolution'){if(!obj(value.value)||!id(value.value.conflictId)||!id(value.value.choiceOperationId))throw new Error('Choose a current conflict option.');return Object.freeze({action:'resolution',value:Object.freeze({...value.value})});}
 if(!((['watchlist','favorite','watched'].includes(value.action)&&typeof value.value==='boolean')||(value.action==='rating'&&rating(value.value))))throw new Error('Invalid personal-state intent.');
 return Object.freeze({...value});
}
/** The facts placing an episode, song or book part in its parent. A fact the page only shows is empty when missing and left out when it cannot be read. */
function hierarchy(value: unknown, required: string[], optional: string[], numbers: string[]): Readonly<Record<string, string | number | null>> {
  if (!obj(value)) invalid();
  const result: Record<string, string | number | null> = {};
  for (const key of required) { const v = value[key] ?? ''; if (!str(v, 2048)) invalid(); result[key] = v; }
  for (const key of optional) if (str(value[key], 2048)) result[key] = value[key] as string;
  for (const key of numbers) if (value[key] === null || integer(value[key])) result[key] = value[key] as number | null;
  return Object.freeze(result);
}
// Extras are ordinary playable items grouped by a server-published type. A group of a type this app
// does not know, or one left with no extra it can play, is not shown; the bound on groups is exact.
function extras(value: unknown, target: DetailTarget): readonly DetailExtra[] {
  const groups = keepReadable('detail', 'extras', array(value, detailExtraTypes.length), group => {
    if (!obj(group) || !detailExtraTypes.includes(group.type as DetailExtraType) || !str(group.label, 256) || !group.label) invalid();
    const items = firstOfEach('detail', 'extras.items', keepReadable('detail', 'extras.items', array(group.items, 100), entry => {
      const parsed = validateContentEntry(entry);
      if (parsed.kind !== 'extra' || parsed.id === target.itemId || parsed.playback?.itemId !== parsed.id) invalid();
      return parsed;
    }), item => item.id);
    if (items.length === 0) return undefined;
    return Object.freeze({ type: group.type as DetailExtraType, label: group.label, items: Object.freeze(items) });
  });
  return Object.freeze(firstOfEach('detail', 'extras', groups, group => group.type));
}
/**
 * A title's page. Its server, library, item, viewer fence, revisions and list bounds are exact,
 * and so is the viewer's own revision. Everything else is read part by part: an action, credit,
 * rating, genre, source, file or extra that cannot be read is left out alone, an optional part
 * (the episode, song or book facts, related rows, extras, markers) is left out whole, and what
 * the page is only drawn with falls to a neutral reading (no runtime, not playable, no metadata).
 */
function projection(raw: unknown, scope: DetailScope, target: DetailTarget): DetailProjection {
  if (!obj(raw) || !obj(raw.scope) || !obj(raw.revision) || !obj(raw.item)) invalid();
  const s = raw.scope, r = raw.revision, item = wireDisplayDurations(raw.item,{duration:"durationMs",progressSeconds:"progressMs"},left), m = obj(raw.metadata) ? raw.metadata : {};
  if (s.serverId !== scope.serverId || s.libraryId !== target.libraryId || s.itemId !== target.itemId || !id(s.viewerFence) || !integer(r.catalog) || !integer(r.viewer)) invalid();
  if (!obj(item)) invalid();
  if (item.id !== target.itemId || item.libraryId !== target.libraryId || !str(item.title ?? '', 2048) || !id(item.kind)) invalid();
  const media: DetailItem = { id: target.itemId, libraryId: target.libraryId, title: (item.title ?? '') as string, kind: item.kind, ...(finite(item.duration) ? {duration: item.duration} : {}), ...(item.durationState === 'unrepresentable' && item.duration === undefined ? {durationUnrepresentable: true as const} : {}), progressSeconds: finite(item.progressSeconds) ? item.progressSeconds : 0, available: item.available === true };
  for (const key of ['overview','posterUrl','backdropUrl','stillUrl'] as const) { const v = item[key]; if (key === 'overview' ? typeof v === 'string' && v.length <= 65536 && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v) : str(v, 4096)) media[key] = v as string; else left('item.' + key, v); }
  if (integer(item.year)) media.year = item.year; else left('item.year', item.year);
  if (item.addedAt === null || str(item.addedAt, 128)) media.addedAt = item.addedAt as string | null; else left('item.addedAt', item.addedAt);
  const episode = part('item.episode', item.episode, value => {
    const fields = hierarchy(value, ['showId','showTitle','numbering','localIdentityStatus','providerMatchStatus','orderingBasis','sourceBoundary'], ['seasonId','airDate'], ['seasonNumber','number']);
    if (!integer(fields.number) || !id(fields.showId) || fields.seasonId !== undefined && !id(fields.seasonId)) invalid();
    return fields as DetailEpisodeInfo;
  });
  if (episode) media.episode = episode;
  const song = part('item.song', item.song, value => {
    const fields = hierarchy(value, ['albumId','albumTitle','albumArtist','artist','providerMatchStatus'], ['localMetadataIssue','providerTitle','providerArtist','providerRecordingId','providerReleaseId','providerReleaseGroupId','providerTrackId','providerReleaseStatus','providerObservedAt'], ['discNumber','trackNumber']);
    if (!id(fields.albumId)) invalid();
    return fields as DetailSongInfo;
  });
  if (song) media.song = song;
  const bookFile = part('item.bookFile', item.bookFile, value => {
    const fields = hierarchy(value, ['bookId','bookTitle','sourceBoundary'], ['localMetadataIssue'], ['discNumber','partNumber']);
    if (!id(fields.bookId)) invalid();
    const localPolicy = part('item.bookFile.localPolicy', (value as Record<string, unknown>).localPolicy, parseLocalAudioPolicy), localMetadata = part('item.bookFile.localMetadata', (value as Record<string, unknown>).localMetadata, parseLocalBook);
    return Object.freeze({...fields,...(localPolicy?{localPolicy}:{}),...(localMetadata?{localMetadata}:{})}) as DetailBookFileInfo;
  });
  if (bookFile) media.bookFile = bookFile;
  const actions = firstOfEach('detail', 'actions', keepReadable('detail', 'actions', array(raw.actions ?? [], 32), action => {
    if (!obj(action) || !id(action.id) || !id(action.labelKey)) invalid();
    const result: { -readonly [K in keyof DetailAction]: DetailAction[K] } = { id: action.id, labelKey: action.labelKey, enabled: action.enabled === true };
    for (const field of ['min','max','step'] as const) if (finite(action[field])) result[field] = action[field] as number;
    // An action that plays says where it starts within this title; one that cannot is not offered.
    if (action.playback !== undefined && action.playback !== null) {
      const p = wireDurations(action.playback,{startSeconds:"startMs"});
      if (!obj(p) || p.itemId !== target.itemId || !finite(p.startSeconds) || media.duration !== undefined && p.startSeconds > media.duration) invalid();
      result.playback = Object.freeze({ itemId: target.itemId, startSeconds: p.startSeconds });
    }
    return Object.freeze(result);
  }), a => a.id);
  const sources = keepReadable('detail', 'metadata.sources', array(m.sources??[],16), row=>{if(!obj(row)||!id(row.provider)||!str(row.sourceUrl??'',4096)||!str(row.observedAt??'',128))invalid();if(row.sourceUrl){try{const url=new URL(row.sourceUrl as string);if(url.protocol!=='https:'||url.username||url.password)invalid();}catch{invalid();}}return Object.freeze({provider:row.provider,sourceUrl:(row.sourceUrl??'') as string,observedAt:(row.observedAt??'') as string});});
  const ratings = keepReadable('detail', 'metadata.ratings', array(m.ratings ?? [], 16), row => {
    if (!obj(row) || !id(row.provider) || !finite(row.value) || !finite(row.max) || row.max <= 0 || row.value > row.max || !str(row.sourceUrl ?? '', 4096) || !str(row.observedAt ?? '', 128)) invalid();
    return Object.freeze({ provider: row.provider, value: row.value, max: row.max, sourceUrl: (row.sourceUrl ?? '') as string, observedAt: (row.observedAt ?? '') as string, ...(integer(row.votes) ? { votes: row.votes } : {}) });
  });
  const genres = keepReadable('detail', 'metadata.genres', array(m.genres ?? [], 10000), row => { if (!obj(row) || !id(row.id) || !str(row.name ?? '') || !str(row.provider ?? '', 256)) invalid(); return Object.freeze({ id: row.id, name: (row.name ?? '') as string, provider: (row.provider ?? '') as string }); });
  const attributions=keepReadable('detail','metadata.attributions',array(m.attributions??[],12),v=>(str(v)?v:undefined));
  const credits = keepReadable('detail', 'metadata.credits', array(m.credits ?? [], 10000), row => creditRow(row, target.itemId));
  const creditTotals=obj(m.creditTotals)&&integer(m.creditTotals.cast)&&integer(m.creditTotals.crew)?Object.freeze({cast:m.creditTotals.cast,crew:m.creditTotals.crew}):undefined;
  // A metadata status this app does not know reads from what arrived: available when there is any, unavailable otherwise.
  const status: DetailMetadata['status'] = m.status === 'available' || m.status === 'unavailable' ? m.status : ratings.length || genres.length || credits.length ? 'available' : 'unavailable';
  let markers: readonly SegmentMarker[] = [];
  try { markers = parseSegmentMarkers(raw.markers ?? [], media.duration); } catch { noteSkipped('detail', 'markers'); }
  let facts: TitleFacts | undefined;
  if (obj(raw.facts)) { const f: {-readonly [K in keyof TitleFacts]: TitleFacts[K]} = {}; for (const key of ['contentRating','studio','network','tagline','releaseDate','originalTitle','country','originalLanguage','edition'] as const) { const v = raw.facts[key]; if (str(v, 2048) && v) f[key] = v; } if (Object.keys(f).length) facts = Object.freeze(f); }
  const languages = Object.freeze({audio: Object.freeze(obj(raw.languages) ? array(raw.languages.audio ?? [],256).filter((v): v is string => str(v,64)) : []), subtitles: Object.freeze(obj(raw.languages) ? array(raw.languages.subtitles ?? [],256).filter((v): v is string => str(v,64)) : [])});
  const collections=keepReadable('detail','collections',array(raw.collections??[],2),c=>{if(!obj(c)||!id(c.id)||!str(c.name,2048)||!integer(c.memberCount)||!integer(c.position))invalid();return Object.freeze({id:c.id,name:c.name,memberCount:c.memberCount,position:c.position,...(integer(c.yearFrom)&&c.yearFrom?{yearFrom:c.yearFrom}:{}),...(integer(c.yearTo)&&c.yearTo?{yearTo:c.yearTo}:{}),posterUrls:Object.freeze(array(c.posterUrls??[],4).filter((p):p is string=>str(p,4096)&&p.startsWith('/')))});});
  const episodeDetails=part('episodeDetails',raw.episodeDetails,d=>{if(!obj(d))invalid();const neighbours:Record<string,Readonly<{id:string;number:number;title:string}>>={};for(const key of ['previous','next'] as const){const n=d[key];if(n!==undefined){if(!obj(n)||!id(n.id)||!integer(n.number)||!str(n.title,2048))invalid();neighbours[key]=Object.freeze({id:n.id,number:n.number,title:n.title});}}return Object.freeze({...neighbours,...(str(d.contentRating,256)&&d.contentRating?{contentRating:d.contentRating}:{})});});
  const missingSince = str(raw.missingSince, 64) && raw.missingSince ? raw.missingSince : undefined;
  const related = part('related', raw.related, value => validateRelatedMovies(value, {...target,kind:media.kind}));
  const extra = part('extras', raw.extras, value => extras(value, target));
  const fileInfoOpening = typeof raw.fileInfoOpening === 'string' && /^[A-Za-z0-9._~-]{1,1024}$/.test(raw.fileInfoOpening) ? raw.fileInfoOpening : undefined;
  if (!fileInfoOpening) left('fileInfoOpening', raw.fileInfoOpening);
  return Object.freeze({ ...(facts ? { facts } : {}), ...(fileInfoOpening ? { fileInfoOpening } : {}), ...(collections.length?{collections:Object.freeze(collections)}:{}),...(episodeDetails?{episodeDetails}:{}),languages, ...(missingSince ? { missingSince } : {}), markers, scope: Object.freeze({ serverId: scope.serverId, libraryId: target.libraryId, itemId: target.itemId, viewerFence: s.viewerFence }), revision: Object.freeze({ catalog: r.catalog, viewer: r.viewer }), item: Object.freeze(media), personal: personal(raw.personal), actions: Object.freeze(actions), ...(related?{related}:{}), ...(extra?{extras:extra}:{}), metadata: Object.freeze({ ...(m.sources===undefined?{}:{sources:Object.freeze(sources)}), ...(m.attributions===undefined?{}:{attributions:Object.freeze(attributions)}),status, ratings: Object.freeze(ratings), genres: Object.freeze(genres), credits: Object.freeze(credits), ...(creditTotals?{creditTotals}:{}) }) });
}
function receipt(raw: unknown, operationId: string, data: DetailProjection): Receipt {
  if (!obj(raw) || raw.operationId !== operationId || raw.serverId !== data.scope.serverId || raw.libraryId !== data.scope.libraryId || raw.itemId !== data.scope.itemId || raw.viewerFence !== data.scope.viewerFence) invalid();
  return { operationId, serverId: data.scope.serverId, libraryId: data.scope.libraryId, itemId: data.scope.itemId, viewerFence: data.scope.viewerFence, personal: personal(raw.current ?? raw.personal) };
}
function errorInfo(error: unknown): DetailError {
  const e = error as { code?: unknown; retryable?: unknown; status?:number } | null;
  return Object.freeze({ code: id(e?.code) ? e.code : 'request_failed', message: error instanceof Error ? error.message : 'The detail request failed.', retryable: e?.status!==401&&e?.status!==403&&(e?.retryable===true||error instanceof TypeError||(e?.status??0)>=500||e?.status===408||e?.status===429) });
}

export class DetailService {
  private api: DetailApi; private bound: DetailScope; private requestId: () => Promise<string>; private timeoutMs: number;
  private generation = 0; private readGeneration = 0; private readController?: AbortController; private mutationController?: AbortController;
  private listeners = new Set<() => void>(); private state: DetailSnapshot; private disposed = false;
  private commands: Command[] = []; private running = false; private readPromise?: Promise<void>;
  constructor(options: { api: DetailApi; scope: DetailScope; requestId: () => Promise<string>; timeoutMs?: number }) {
    this.api = options.api; this.bound = checkScope(options.scope); this.requestId = options.requestId; this.timeoutMs = options.timeoutMs ?? 15000;
    if (!Number.isFinite(this.timeoutMs) || this.timeoutMs <= 0 || this.timeoutMs > 120000) throw new Error('Invalid detail deadline.');
    this.state = this.empty();
  }
  private empty(): DetailSnapshot { return Object.freeze({ generation: this.generation, scope: this.bound, target: null, phase: 'idle', data: null, error: null, pending: Object.freeze([]), mutationError: null }); }
  private related: 'all' | 'none' = 'all';
  getSnapshot = (): DetailSnapshot => this.state;
  subscribe = (listener: () => void): (() => void) => { if (this.disposed) return () => {}; this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  private publish(patch: Partial<DetailSnapshot> = {}): void { if (this.disposed) return; this.state = Object.freeze({ ...this.state, ...patch, pending: Object.freeze(this.commands.map(c => Object.freeze({ intent: c.intent, phase: c.phase }))) }); for (const listener of this.listeners) listener(); }
  private active(): void { if (this.disposed) throw new Error('Detail service is disposed.'); }
  private fence(): void { this.generation++; this.readGeneration++; this.readController?.abort(); this.mutationController?.abort(); for (const c of this.commands) c.resolve(); this.commands = []; this.running = false; this.readPromise = undefined; }
  /** `related: 'none'` skips the embedded recommendation rows, for a page that reads them itself. */
  select(input: DetailTarget, options: Readonly<{related?: 'all' | 'none'}> = {}): Promise<void> {
    this.active(); const target = checkTarget(input), related = options.related ?? 'all';
    if (related !== 'all' && related !== 'none') throw new Error('Invalid detail related option.');
    if (this.state.phase === 'loading' && this.state.target?.itemId === target.itemId && this.state.target.libraryId === target.libraryId && this.related === related && this.readPromise) return this.readPromise;
    this.fence(); this.related = related; this.publish({ ...this.empty(), target }); return this.refresh();
  }
  refresh(): Promise<void> {
    this.active(); if (!this.state.target) return Promise.resolve();
    this.readController?.abort(); const controller = new AbortController(); this.readController = controller;
    const generation = this.generation, read = ++this.readGeneration, target = this.state.target, bound = this.bound, api = this.api;
    this.publish({ phase: 'loading', error: null });
    const task = (async () => {
      try {
        const raw = await this.deadline(api.request<RouteResponse<"get_items_id_detail">>(encodedRoutePath("get_items_id_detail", {"id": encodeURIComponent(target.itemId)}) + "?related=" + this.related, 'GET', undefined, controller.signal), controller);
        if (this.disposed || generation !== this.generation || read !== this.readGeneration) return;
        const data = projection(raw, bound, target);
        if (this.state.data && (data.scope.viewerFence!==this.state.data.scope.viewerFence || data.revision.catalog<this.state.data.revision.catalog)) throw new Failure('detail_scope_changed','The catalog or your access changed. Reload these details before continuing.',true);
        if (this.state.data && data.personal.revision < this.state.data.personal.revision) { this.publish({ phase: 'ready' }); return; }
        this.publish({ phase: 'ready', data });
      } catch (error) {
        if (this.disposed || generation !== this.generation || read !== this.readGeneration) return;
        const info=errorInfo(error); this.publish({ phase: 'error', data: ['unauthorized','forbidden','not_found','detail_scope_changed','invalid_detail'].includes(info.code)?null:this.state.data, error: info });
      }
    })();
    this.readPromise = task; return task;
  }
  retryRead(): Promise<void> { return this.refresh(); }
  retry(): Promise<void> { return this.refresh(); }
  /**
   * Read the title again without touching `phase`, and show the answer in place. A failed read
   * rejects and changes nothing. An answer that the title is gone or no longer allowed is the
   * page's state from then on, exactly as a first load would have shown it.
   */
  async revalidate(context?: { signal?: AbortSignal; settle?: () => Promise<void> }): Promise<void> {
    this.active(); const target = this.state.target;
    if (!target || this.state.phase !== 'ready' || !this.state.data) return;
    const controller = new AbortController(), stop = () => controller.abort();
    context?.signal?.addEventListener('abort', stop, { once: true });
    const generation = this.generation, read = this.readGeneration, bound = this.bound, api = this.api;
    try {
      const raw = await this.deadline(api.request<RouteResponse<"get_items_id_detail">>(encodedRoutePath("get_items_id_detail", {"id": encodeURIComponent(target.itemId)}) + "?related=" + this.related, 'GET', undefined, controller.signal), controller);
      if (this.disposed || generation !== this.generation || read !== this.readGeneration) return;
      const data = projection(raw, bound, target);
      await context?.settle?.();
      if (this.disposed || generation !== this.generation || read !== this.readGeneration || controller.signal.aborted || !this.state.data) return;
      // Another viewer's answer, or one older than what is shown (a change made here has not reached this read), is not shown.
      if (data.scope.viewerFence !== this.state.data.scope.viewerFence) { await this.refresh(); return; }
      if (data.revision.catalog < this.state.data.revision.catalog || data.personal.revision < this.state.data.personal.revision) return;
      // Nothing on the page changed: nothing is published, so nothing re-renders.
      if (JSON.stringify(data) === JSON.stringify(this.state.data)) return;
      this.publish({ data });
    } catch (error) {
      if (this.disposed || generation !== this.generation || read !== this.readGeneration || controller.signal.aborted) return;
      const code = errorInfo(error).code;
      if (['unauthorized', 'forbidden', 'not_found', 'invalid_detail'].includes(code)) { await this.refresh(); return; }
      throw error;
    } finally { context?.signal?.removeEventListener('abort', stop); }
  }
  mutate(input: PersonalIntent): Promise<void> {
    this.active(); const intent = checkIntent(input), data = this.state.data;
    if (!data || (intent.action==='resolution' ? !data.personal.conflicts.some(c=>c.id===intent.value.conflictId) : !data.actions.some(a => a.id === intent.action && a.enabled))) throw new Error('This personal action is not currently available.');
    const last = this.commands.at(-1);
    if (last && JSON.stringify(last.intent) === JSON.stringify(intent)) return last.promise;
    if (this.commands.length >= 8) throw new Error('Wait for pending personal actions to finish.');
    let resolve!: () => void; const promise = new Promise<void>(r => { resolve = r; });
    this.commands.push({ intent, phase: 'queued', promise, resolve }); this.publish(); void this.pump(); return promise;
  }
  retryMutation(): Promise<void> {
    this.active(); const command = this.commands[0];
    if (!command || this.running || command.phase === 'queued' || command.phase === 'sending') return command?.promise ?? Promise.resolve();
    if (command.phase === 'conflict') throw new Error(command.offline ? 'This offline intent needs review; do not silently rebase it.' : 'This personal intent needs review before a new action.');
    let resolve!: () => void; command.promise = new Promise<void>(r => { resolve = r; }); command.resolve = resolve; command.phase = 'queued';
    this.publish({ mutationError: null }); void this.pump(); return command.promise;
  }
  /** Abandon retry only after an explicit user decision. Never manufacture a replacement receipt. */
  dismissMutation():void {this.active();if(this.running)return;for(const c of this.commands)c.resolve();this.commands=[];this.publish({mutationError:null});}
  /** Online reconciliation boundary for an existing device-authored operation. No device storage owner. */
  reconcile(input:Readonly<{operationId:string;offline:PersonalOfflineMutation;intent:PersonalIntent}>):Promise<void>{
    this.active();const intent=checkIntent(input.intent),o=input.offline;
    if(!uuid.test(input.operationId)||!id(o.deviceId)||!id(o.deviceMutationId)||!integer(o.sequence)||o.sequence===0||!integer(o.baseRevision)||!Number.isFinite(Date.parse(o.authoredAt))||intent.action==='resolution')throw new Error('Invalid device-authored operation.');
    if(!this.state.data||this.commands.length)throw new Error('Load the exact item and reconcile outstanding operations serially.');
    let resolve!:()=>void;const promise=new Promise<void>(r=>resolve=r);
    this.commands.push({intent,operationId:input.operationId,expectedRevision:o.baseRevision,offline:Object.freeze({...o}),phase:'queued',promise,resolve});this.publish();void this.pump();return promise;
  }
  setScope(next: DetailScope, api: DetailApi): void { this.active(); const checked = checkScope(next); this.fence(); this.bound = checked; this.api = api; this.publish(this.empty()); }
  cancel(): void { this.active(); this.fence(); this.publish(this.empty()); }
  dispose(): void { this.fence(); this.disposed = true; this.listeners.clear(); }
  private async pump(): Promise<void> {
    if (this.running || this.disposed || !this.commands.length || this.commands[0].phase !== 'queued') return;
    const command = this.commands[0], data = this.state.data;
    if (!data) { command.phase = 'retry-required'; command.resolve(); this.publish({ mutationError: Object.freeze({ code: 'detail_required', message: 'Reload details before retrying this action.', retryable: true }) }); return; }
    this.running = true; command.phase = 'sending'; this.publish({ mutationError: null });
    const generation = this.generation, api = this.api, bound = this.bound, controller = new AbortController(); this.mutationController = controller;
    try {
      if (!command.operationId) {
        const operationId = await this.deadline(this.requestId(), controller);
        if (!uuid.test(operationId) && !isUserMutationKey(operationId)) throw new Failure('invalid_request_id', 'A UUIDv4 or user-mutation operation ID is required.');
        if (this.disposed || generation !== this.generation) return;
        command.operationId = operationId; command.expectedRevision = data.personal.revision;
      }
      const field = command.intent.action === 'watchlist' ? 'watchlisted' : command.intent.action;
      const body = { operationId: command.operationId, expectedRevision: command.expectedRevision, [field]: command.intent.value, ...(command.offline?{offline:command.offline}:{}) };
      const raw = await this.deadline(api.request<unknown>(encodedRoutePath("put_items_id_personal_state", {"id": encodeURIComponent(data.scope.itemId)}), 'PUT', durationIntent(body,{progressSeconds:'progressMs'}), controller.signal), controller);
      if (this.disposed || generation !== this.generation) return;
      if (obj(raw) && raw.mutationReceipt !== undefined) {
        const marker = raw.mutationReceipt;
        if (!obj(marker) || typeof marker.key !== 'string' || !isUserMutationKey(marker.key)) invalid();
        const recovered = parseUserMutationReceipt(marker, {key: marker.key, operation: 'put_items_id_personal_state'});
        if (recovered.status !== 'applied' || recovered.targetId !== data.scope.itemId || !integer(recovered.revision)) invalid();
        // The tiny receipt proves the historical outcome. A separate, validated current
        // read supplies display state; its values cannot prove or disprove that outcome.
        const current = await this.deadline(api.request<RouteResponse<'get_items_id_detail'>>(encodedRoutePath('get_items_id_detail', {id: encodeURIComponent(data.scope.itemId)}) + '?related=' + this.related, 'GET', undefined, controller.signal), controller);
        if (this.disposed || generation !== this.generation) return;
        const next = projection(current, bound, {libraryId: data.scope.libraryId, itemId: data.scope.itemId});
        if (next.scope.viewerFence !== data.scope.viewerFence || next.personal.revision < recovered.revision || next.revision.catalog < data.revision.catalog) invalid();
        if (this.state.data && next.personal.revision >= this.state.data.personal.revision && next.revision.catalog >= this.state.data.revision.catalog) this.publish({data: next});
      } else {
        const applied = receipt(raw, command.operationId, data);
        if (this.state.data && applied.personal.revision >= this.state.data.personal.revision) this.publish({ data: Object.freeze({ ...this.state.data, item:Object.freeze({...this.state.data.item,progressSeconds:applied.personal.progressSeconds}), personal: applied.personal }) });
      }
      await api.acknowledgeUserMutation?.(command.operationId);
      if (this.disposed || generation !== this.generation) return;
      this.commands.shift(); command.resolve(); this.publish({ mutationError: null });
      // What the change touched elsewhere on the page (Up Next, counts) is read in place, with no loading state.
      if (this.state.phase === 'ready') await this.revalidate().catch(() => {}); else await this.refresh();
    } catch (error) {
      if (this.disposed || generation !== this.generation) return;
      const info = errorInfo(error);
      // Keep the authored draft, ID and revision separate from refreshed server state.
      // A conflicting operation is reviewed explicitly; matching current values are not a receipt.
      command.phase = ['personal_state_conflict','personal_needs_resolution','personal_needs_review','operation_expired','receipt_expired','idempotency_conflict','mutation_needs_review','revision_mismatch'].includes(info.code) ? 'conflict' : 'retry-required'; command.resolve();
      this.publish({ mutationError: info });
      if (command.phase === 'conflict') await this.refresh();
    } finally {
      if (generation === this.generation) { this.running = false; if (this.commands[0]?.phase === 'queued') void this.pump(); }
    }
  }
  private async deadline<T>(operation: Promise<T>, controller: AbortController): Promise<T> {
    let timer: ReturnType<typeof setTimeout> | undefined, onAbort: (() => void) | undefined;
    try {
      const abort = new Promise<never>((_, reject) => { onAbort = () => reject(new Failure('cancelled', 'Detail request cancelled.')); if (controller.signal.aborted) onAbort(); else controller.signal.addEventListener('abort', onAbort, { once: true }); });
      const timeout = new Promise<never>((_, reject) => { timer = setTimeout(() => { reject(new Failure('timeout', 'The server took too long to respond. Retry to reconcile the same operation.', true)); controller.abort(); }, this.timeoutMs); });
      return await Promise.race([operation, abort, timeout]);
    } finally { if (timer) clearTimeout(timer); if (onAbort) controller.signal.removeEventListener('abort', onAbort); }
  }
}

/** Validates one page of GET /v1/items/{itemId}/credits. The group asked for, the cursor and the bound are exact; a credit that cannot be read is left out alone and the total is at least the credits kept. */
export function parseCreditPage(raw: unknown, itemId: string, group: 'cast' | 'crew'): CreditPage {
  if (!obj(raw) || !obj(raw.revision) || !integer(raw.revision.catalog) || !integer(raw.revision.viewer) || raw.group !== group || raw.nextCursor !== undefined && (!str(raw.nextCursor, 4096) || !raw.nextCursor)) invalid();
  const credits = keepReadable('credits', 'credits', array(raw.credits, 10000), row => creditRow(row, itemId));
  return Object.freeze({ revision:Object.freeze({catalog:raw.revision.catalog,viewer:raw.revision.viewer}),group, credits: Object.freeze(credits), total: Math.max(integer(raw.total) ? raw.total : 0, credits.length), ...(raw.nextCursor ? { nextCursor: raw.nextCursor as string } : {}) });
}

/** A detail row retains its whole loaded prefix through exhaustion. */
export function appendCreditPage(current: CreditPage, next: CreditPage): CreditPage {
 if(current.group!==next.group) invalid();
 if(current.revision.catalog!==next.revision.catalog || current.revision.viewer!==next.revision.viewer) throw new Failure('stale_continuation','Reload credits after the title changes.');
 const seen=new Set(current.credits.map(credit=>credit.id));
 return Object.freeze({...next,credits:Object.freeze([...current.credits,...next.credits.filter(credit=>!seen.has(credit.id))])});
}
export async function readCreditPage(api:DetailApi,itemId:string,group:'cast'|'crew',options:{cursor?:string;limit?:number}={},signal?:AbortSignal):Promise<CreditPage> {
 const query=new URLSearchParams({group,limit:String(options.limit??37)});if(options.cursor)query.set('cursor',options.cursor);
 return parseCreditPage(await api.request<RouteResponse<'get_items_id_credits'>>(encodedRoutePath('get_items_id_credits',{id:encodeURIComponent(itemId)})+'?'+query,'GET',undefined,signal),itemId,group);
}
export async function readDetailMoreLike(api:DetailApi,itemId:string,options:{cursor?:string;limit?:number}={},signal?:AbortSignal):Promise<HomeRow> {
 const query=new URLSearchParams({limit:String(options.limit??37)});if(options.cursor)query.set('cursor',options.cursor);
 const row=validateHomeRow(await api.request<RouteResponse<'get_items_id_recommendations_row'>>(encodedRoutePath('get_items_id_recommendations_row',{id:encodeURIComponent(itemId),row:'more_like'})+'?'+query,'GET',undefined,signal));
 if(row.id!=='more_like:local:'+itemId || row.evidenceId!==itemId)invalid();
 return row;
}
export function appendDetailRecommendationPage(current:HomeRow,next:HomeRow):HomeRow {
 if(current.id!==next.id || current.endpoint!==next.endpoint || current.viewerFence!==next.viewerFence)invalid();
 if(current.revision.catalog!==next.revision.catalog || current.revision.viewer!==next.revision.viewer)throw new Failure('stale_continuation','Reload recommendations after the title changes.');
 const seen=new Set(current.entries.map(entry=>entry.id));
 return Object.freeze({...current,entries:Object.freeze([...current.entries,...next.entries.filter(entry=>!seen.has(entry.id))]),nextCursor:next.nextCursor,hasMore:next.hasMore,total:next.total});
}

export type FileInfoDiscTitle = Readonly<{number: number; durationMs: number; chapters: number; angles: number}>;
export type FileInfoDisc = Readonly<{fileId: string; kind: 'bluray' | 'dvd'; titles: readonly FileInfoDiscTitle[]}>;
