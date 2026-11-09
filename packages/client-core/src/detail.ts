import type {DiscMediaFacts} from './disc-media-facts.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {wireDurations,wireDisplayDurations,durationIntent} from './wire-units.ts';
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
      let raw: unknown;
      try { raw = await queries.api.request<RouteResponse<'get_items_id_detail'>>(encodedRoutePath('get_items_id_detail', {id: encodeURIComponent(itemId)}) + '?related=' + related, 'GET', undefined, deadline.signal); }
      catch (error) { if (deadline.signal.aborted && !signal.aborted) throw new Failure('timeout', 'The server took too long to respond.', true); throw error; }
      finally { deadline.dispose(); }
      const libraryId = obj(raw) && obj(raw.scope) && id(raw.scope.libraryId) ? raw.scope.libraryId : invalid();
      const next = projection(raw, queries.scope, {libraryId, itemId});
      // An answer older than what is shown (a change made here has not reached this read yet) never replaces it.
      const held = queries.client.getQueryData<DetailProjection>(queryKey);
      if (held && held.scope.libraryId === next.scope.libraryId && (next.revision.catalog < held.revision.catalog || next.personal.revision < held.personal.revision)) return held;
      return next;
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
  private api: DetailApi; private bound: DetailScope; private requestId: () => Promise<string>; private timeoutMs: number;
  private generation = 0; private mutationController?: AbortController;
  private listeners = new Set<() => void>(); private state: DetailSnapshot; private disposed = false;
  private commands: Command[] = []; private running = false;
  private related: 'all' | 'none' = 'all';
  private observer?: QueryObserver<DetailProjection, Error>; private stopObserving?: () => void;
  constructor(options: { queries: ViewerQueries; api: DetailApi; scope: DetailScope; requestId: () => Promise<string>; timeoutMs?: number }) {
    this.queries = options.queries; this.api = options.api; this.bound = checkScope(options.scope); this.requestId = options.requestId; this.timeoutMs = options.timeoutMs ?? DETAIL_DEADLINE_MS;
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
  /** A receipt's personal state, written into every cached page of the title that is older than it. */
  private applyPersonal(itemId: string, personal: PersonalState): void {
    for (const related of ['all', 'none'] as const) this.queries.client.setQueryData<DetailProjection>(queryKeys.itemDetail(itemId, related), held =>
      !held || personal.revision < held.personal.revision ? held
        : Object.freeze({ ...held, item: Object.freeze({ ...held.item, progressSeconds: personal.progressSeconds }), personal }));
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
      if (!command.operationId) {
        const operationId = await this.deadline(this.requestId(), controller);
        if (!uuid.test(operationId)) throw new Failure('invalid_request_id', 'A UUIDv4 operation ID is required.');
        if (this.disposed || generation !== this.generation) return;
        command.operationId = operationId; command.expectedRevision = data.personal.revision;
      }
      const field = command.intent.action === 'watchlist' ? 'watchlisted' : command.intent.action;
      const body = { operationId: command.operationId, expectedRevision: command.expectedRevision, [field]: command.intent.value, ...(command.offline?{offline:command.offline}:{}) };
      const raw = await this.deadline(api.request<unknown>(encodedRoutePath("put_items_id_personal_state", {"id": encodeURIComponent(data.scope.itemId)}), 'PUT', durationIntent(body,{progressSeconds:'progressMs'}), controller.signal), controller);
      if (this.disposed || generation !== this.generation) return;
      const applied = receipt(raw, command.operationId, data);
      this.applyPersonal(data.scope.itemId, applied.personal);
      if (this.disposed || generation !== this.generation) return;
      this.commands.shift(); command.resolve(); this.publish({ mutationError: null });
      // Every other page showing this title (its show's episode list, a Home row, a grid) reads again in place now (N4-5).
      this.queries.moved(personalChangedHere(data.scope.libraryId, [data.item.id, data.item.episode?.showId, data.item.episode?.seasonId]));
      // What the change touched elsewhere on this page (Up Next, counts) is read in place, with no loading state.
      await this.refresh().catch(() => {});
    } catch (error) {
      if (this.disposed || generation !== this.generation) return;
      const info = errorInfo(error);
      // Nothing answered: the outbox keeps the edit and sends it later, so this command is finished here.
      if (info.code === 'sync_pending') { this.commands.shift(); command.resolve(); this.publish({ mutationError: info }); return; }
      // Keep the authored draft, ID and revision separate from refreshed server state.
      // A conflicting operation is reviewed explicitly; matching current values are not a receipt.
      command.phase = ['personal_state_conflict','personal_needs_resolution','personal_needs_review','operation_expired','idempotency_conflict','revision_mismatch'].includes(info.code) ? 'conflict' : 'retry-required'; command.resolve();
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

/** Keep the original transport promise for callers with their own admission and parsing order. */
export function readCreditPageResponse(api:DetailApi,itemId:string,group:'cast'|'crew',options:{cursor?:string;limit?:number}={},signal?:AbortSignal) {
 const query=new URLSearchParams({group,limit:String(options.limit??37)});if(options.cursor)query.set('cursor',options.cursor);
 return api.request<RouteResponse<'get_items_id_credits'>>(encodedRoutePath('get_items_id_credits',{id:encodeURIComponent(itemId)})+'?'+query,'GET',undefined,signal);
}
export async function readCreditPage(api:DetailApi,itemId:string,group:'cast'|'crew',options:{cursor?:string;limit?:number}={},signal?:AbortSignal):Promise<CreditPage> {
 return parseCreditPage(await readCreditPageResponse(api,itemId,group,options,signal),itemId,group);
}
export type FileInfoDiscTitle = Readonly<{number: number; durationMs: number; chapters: number; angles: number}>;
export type FileInfoDisc = Readonly<{fileId: string; kind: 'bluray' | 'dvd'; titles: readonly FileInfoDiscTitle[]}>;
