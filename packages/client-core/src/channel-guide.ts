import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {GUIDE_RATE_LIMIT_ATTEMPTS, isRateLimited, rateLimitDelay} from './guide/rate-limit.ts';
import {HOUR_MS, MINUTE_MS, localDayStart, zoneOffset} from './guide/time.ts';
import {keepReadable, noteSkipped, oneOfOr} from './tolerant.ts';
import {UserMutationError} from './user-mutations.ts';
/** Server-authored linear guide. Clients choose a viewport, never a schedule. */
export const GUIDE_PROTOCOL = '1.0' as const;
export type ChannelKind = 'live-source' | 'library-channel';
export type LinearUnavailableReason = '' | 'delivery-unavailable' | 'recording-unavailable' | 'not-recordable' | 'source-disabled' | 'source-unavailable' | 'capacity-unavailable' | 'permission-denied' | 'no-schedule' | 'decoder_confinement_unavailable' | 'ffmpeg_not_configured' | 'decoder_dependencies_unavailable' | 'schedule_preparing' | 'delivery_unavailable' | 'ffprobe_not_configured' | 'channel_runtime_unavailable';
export type GuideProgrammeEpisode = Readonly<{season?: number; number?: number; display?: string}>;
export type GuideProgrammeRating = Readonly<{system: string; value: string}>;
export type GuideProgrammeFlags = Readonly<{live?: boolean; premiere?: boolean; new?: boolean; repeat?: boolean}>;
/** Authored by the server from the complete authorized source, before paging. */
export type GuideChannelMark = Readonly<{text: string; seed: string}>;
export type GuideProgramme = Readonly<{
 id: string; channelId: string; title: string; start: string; end: string;
 lineage: 'provider-id' | 'interval-only' | 'generated'; seriesId: string; episodeId: string;
 newEvidence: 'new' | 'repeat' | 'unknown'; description: string; recordingId?:string; recordingState?:string;
 subtitle?: string; episode?: GuideProgrammeEpisode; categories?: readonly string[];
 rating?: GuideProgrammeRating; year?: number; starRating?: string;
 flags?: GuideProgrammeFlags; image?: string;
}>;
export type GuideChannel = Readonly<{
 id: string; sourceId: string; provenance: ChannelKind; name: string; number: string; group: string;
 generation: string; logoPath?: string; mark?: GuideChannelMark; programmes: readonly GuideProgramme[]; tuneAvailable: boolean; recordAvailable: boolean;
 tuneUnavailableReason: LinearUnavailableReason; recordUnavailableReason: LinearUnavailableReason;
 favorite: boolean; hidden: boolean; serverHidden?: boolean; preferenceRevision: number;
}>;
export type GuideSource = Readonly<{id: string; name: string; generation: string; publishedAt: string; availableStart: string; availableEnd: string; provenance: ChannelKind; refreshState: string}>;
export type ChannelGuide = Readonly<{state: 'ready' | 'no-sources' | 'no-channels' | 'no-guide-data' | 'filter-empty' | 'guide-stale' | 'continuation-required'; viewerFence: string; start: string; end: string; timezone: string; channels: readonly GuideChannel[]; sources: readonly GuideSource[]; nextCursor: string; observedAt: string; /** Guide.days: how many calendar days from now are covered (today counts as 1), at most 31. Absent on older servers. */ days?: number}>;
export type GuideRoute = Readonly<{kind: ChannelKind; start: string; end: string; timezone: string; search: string; sourceId: string; favorites?: boolean; includeHidden?: boolean; includeServerHidden?: boolean; group?: string}>;
export interface ChannelApi { acknowledgeUserMutation?(intentId: string): Promise<void>; request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> }
export type GuideSnapshot = Readonly<{phase: 'idle' | 'loading' | 'retained-refreshing' | 'ready' | 'error'; guide: ChannelGuide | null; route: GuideRoute | null; error: string | null; hasPrevious: boolean}>;
const object = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const bodyText = (v: unknown, max = 4096): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b-\x1f\x7f]/.test(v);
const id = (v: unknown): v is string => text(v, 256) && v.length > 0;
/** Millisecond schedule boundaries must survive decoding, including non-168h DST weeks. */
const instant = (v: unknown): v is string => {
 if (typeof v !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(v)) return false;
 const time = Date.parse(v);
 return Number.isFinite(time) && new Date(time).toISOString().slice(0, 19) === v.slice(0, 19);
};
const kind = (v: unknown): v is ChannelKind => v === 'live-source' || v === 'library-channel';
const reasons: readonly string[] = ['', 'delivery-unavailable', 'recording-unavailable', 'not-recordable', 'source-disabled', 'source-unavailable', 'capacity-unavailable', 'permission-denied', 'no-schedule', 'decoder_confinement_unavailable', 'ffmpeg_not_configured', 'decoder_dependencies_unavailable', 'schedule_preparing', 'delivery_unavailable', 'ffprobe_not_configured', 'channel_runtime_unavailable'];
function invalid(): never { throw new Error(unreadableServerResponse); }
function parseChannelMark(v: unknown): GuideChannelMark | undefined {
 if (v === undefined) return undefined;
 if (object(v) && text(v.text, 1024) && v.text.trim() && text(v.seed, 513) && v.seed.trim()) return {text: v.text, seed: v.seed};
 noteSkipped('guide', 'channel.mark');
 return undefined;
}
/**
 * Guide programme facts (FEAT-04, Channels spec §8.1): subtitle, episode, categories, rating,
 * year, starRating, flags and image. Every value is optional (older servers send none of them);
 * unknown or malformed values are dropped, never fatal — only the core programme fields above
 * can fail the guide.
 */
function parseSubtitle(v: unknown): string | undefined {
 return typeof v === 'string' && text(v, 512) && v.length > 0 ? v : undefined;
}
function parseEpisode(v: unknown): GuideProgrammeEpisode | undefined {
 if (v === undefined || v === null) return undefined;
 if (!object(v)) return undefined;
 const out: {season?: number; number?: number; display?: string} = {};
 if (Number.isSafeInteger(v.season) && Number(v.season) >= 1 && Number(v.season) <= 9999) out.season = Number(v.season);
 if (Number.isSafeInteger(v.number) && Number(v.number) >= 1 && Number(v.number) <= 9999) out.number = Number(v.number);
 if (typeof v.display === 'string' && text(v.display, 64) && v.display.length > 0) out.display = v.display;
 return out.season !== undefined || out.number !== undefined || out.display !== undefined ? out : undefined;
}
function parseCategories(v: unknown): readonly string[] | undefined {
 if (v === undefined) return undefined;
 if (!Array.isArray(v)) return [];
 const seen = new Set<string>();
 const out: string[] = [];
 for (const item of v) {
  if (typeof item !== 'string' || !text(item, 128) || !item.trim()) continue;
  const key = item.toLowerCase();
  if (seen.has(key) || out.length >= 16) continue;
  seen.add(key);
  out.push(item);
 }
 return out;
}
function parseRating(v: unknown): GuideProgrammeRating | undefined {
 if (v === undefined || v === null) return undefined;
 if (!object(v)) return undefined;
 if (typeof v.value !== 'string' || !text(v.value, 32) || !v.value.trim()) return undefined;
 const system = v.system === undefined ? '' : v.system;
 if (typeof system !== 'string' || !text(system, 64)) return undefined;
 return {system, value: v.value};
}
function parseYear(v: unknown): number | undefined {
 if (v === undefined || v === null) return undefined;
 return Number.isSafeInteger(v) && Number(v) >= 1000 && Number(v) <= 9999 ? Number(v) : undefined;
}
function parseStarRating(v: unknown): string | undefined {
 return typeof v === 'string' && text(v, 16) && v.length > 0 ? v : undefined;
}
function parseFlags(v: unknown): GuideProgrammeFlags | undefined {
 if (v === undefined || v === null) return undefined;
 if (!object(v)) return undefined;
 return {live: v.live === true, premiere: v.premiere === true, new: v.new === true, repeat: v.repeat === true};
}
const programmeImagePath = /^\/v1\/(?:items\/[A-Za-z0-9_-]{1,128}\/art\/poster|guide\/programme-images\/[a-f0-9]{48}\/[a-f0-9]{64})$/;
function parseImage(v: unknown): string | undefined {
 return typeof v === 'string' && v.length <= 512 && programmeImagePath.test(v) ? v : undefined;
}
/** Guide.days (0–31). Absent on older servers; malformed values are dropped, never fatal. */
function parseDays(v: unknown): number | undefined {
 return Number.isSafeInteger(v) && Number(v) >= 0 && Number(v) <= 31 ? Number(v) : undefined;
}
export function parseGuideProgramme(raw: unknown, channelId: string): GuideProgramme {
 if (!object(raw) || !id(raw.id) || raw.channelId !== channelId || !text(raw.title, 512) || !instant(raw.start) || !instant(raw.end) || Date.parse(raw.end) <= Date.parse(raw.start)) invalid();
 // How a programme was identified, and whether it is new, are evidence: a kind this app does not
 // know reads as the weakest (known only by its time slot; not known to be new or a repeat).
 const lineage = oneOfOr('guide', 'programme.lineage', raw.lineage, ['provider-id', 'interval-only', 'generated'] as const, 'interval-only');
 const seriesId = raw.seriesId ?? '', episodeId = raw.episodeId ?? '', newEvidence = oneOfOr('guide', 'programme.newEvidence', raw.newEvidence || undefined, ['new', 'repeat', 'unknown'] as const, 'unknown'), description = bodyText(raw.description ?? '') ? raw.description ?? '' : '';
 if (!text(seriesId, 256) || !text(episodeId, 256)) invalid();
 const recordingId=raw.recordingId??'',recordingState=raw.recordingState??'';if(typeof recordingId!=='string'||recordingId!==''&&!/^[a-f0-9]{64}$/.test(recordingId)||!text(recordingState,64))invalid();
 const subtitle = parseSubtitle(raw.subtitle), episode = parseEpisode(raw.episode), categories = parseCategories(raw.categories), rating = parseRating(raw.rating), year = parseYear(raw.year), starRating = parseStarRating(raw.starRating), flags = parseFlags(raw.flags), image = parseImage(raw.image);
 return {recordingId,recordingState,id: raw.id, channelId, title: raw.title, start: raw.start, end: raw.end, lineage, seriesId, episodeId, newEvidence, description: description as string, ...(subtitle !== undefined ? {subtitle} : {}), ...(episode !== undefined ? {episode} : {}), ...(categories !== undefined ? {categories} : {}), ...(rating !== undefined ? {rating} : {}), ...(year !== undefined ? {year} : {}), ...(starRating !== undefined ? {starRating} : {}), ...(flags !== undefined ? {flags} : {}), ...(image !== undefined ? {image} : {})};
}
/** Shared native row validation for time windows and the rows-only directory. A source that
 * cannot be read is left out alone (and its channels with it); the bound on the list is exact. */
export function parseGuideSources(value: unknown, expectedKind?: ChannelKind): GuideSource[] {
 if (!Array.isArray(value)||value.length>2000) invalid();
 const values: unknown[]=value;
 const sourceIDs = new Set<string>();
 const sources: GuideSource[] = keepReadable('guide', 'sources', values, (v: unknown) => {
  if (!object(v) || !id(v.id) || sourceIDs.has(String(v.provenance)+'\0'+v.id) || !text(v.name, 120) || !id(v.generation) || !instant(v.publishedAt) || !kind(v.provenance) || expectedKind !== undefined && v.provenance !== expectedKind || !text(v.refreshState, 64) || !(v.availableStart === '' && v.availableEnd === '' || instant(v.availableStart) && instant(v.availableEnd) && Date.parse(v.availableEnd) > Date.parse(v.availableStart))) invalid();
  sourceIDs.add(String(v.provenance)+'\0'+v.id);
  return {id: v.id, name: v.name, generation: v.generation, publishedAt: v.publishedAt, availableStart: v.availableStart, availableEnd: v.availableEnd, provenance: v.provenance, refreshState: v.refreshState} as GuideSource;
 });
 return sources;
}
/**
 * The channels of a guide page. The bounds (50 channels, 10,000 programmes) are exact. A channel
 * that cannot be read, or whose source could not be, is left out alone; so is a programme that
 * cannot be read, repeats an id, overlaps the one before it or lies outside the window asked for.
 * A reason for being unavailable that this app does not know reads as plainly unavailable.
 */
export function parseGuideChannels(value: unknown, sources: readonly GuideSource[], route?: GuideRoute): GuideChannel[] {
 if (!Array.isArray(value)||value.length>50) invalid();
 if (value.reduce((n: number, v: unknown) => n + (object(v) && Array.isArray(v.programmes) ? v.programmes.length : 0), 0) > 10000) invalid();
 const unavailable = (part: string, available: boolean, reason: unknown): LinearUnavailableReason => (available ? '' : (reason === '' ? 'delivery-unavailable' : oneOfOr('guide', part, reason, reasons as readonly LinearUnavailableReason[], 'delivery-unavailable')));
 const values: unknown[]=value;
 const sourceIDs=new Set(sources.map(s=>s.provenance+'\0'+s.id));
 const ids = new Set<string>(), programmeIDs = new Set<string>();
 let total = 0;
 const channels: GuideChannel[] = keepReadable('guide', 'channels', values, (v: unknown) => {
  if (!object(v) || !id(v.id) || ids.has(v.id) || !id(v.sourceId) || !sourceIDs.has(String(v.provenance)+'\0'+v.sourceId) || !kind(v.provenance) || route !== undefined && v.provenance !== route.kind || !text(v.name, 256) || !text(v.number ?? '', 32) || !text(v.group ?? '', 120) || !id(v.generation) || !Number.isSafeInteger(v.preferenceRevision) || Number(v.preferenceRevision) < 0 || !Array.isArray(v.programmes) || v.programmes.length > 10000) invalid();
  // A channel can be watched or recorded only when the server says so outright and gives no reason against; a library channel is never recorded.
  const tuneAvailable = v.tuneAvailable === true && (v.tuneUnavailableReason ?? '') === '', recordAvailable = v.recordAvailable === true && (v.recordUnavailableReason ?? '') === '' && v.provenance !== 'library-channel';
  const tuneUnavailableReason = unavailable('channel.tuneUnavailableReason', tuneAvailable, v.tuneUnavailableReason ?? ''), recordUnavailableReason = unavailable('channel.recordUnavailableReason', recordAvailable, v.recordUnavailableReason ?? '');
  // A logo that is not one of this server's own pictures is not shown.
  let logoPath=v.logoPath??'';if(typeof logoPath!=='string'||logoPath!==''&&!/^\/v1\/items\/[A-Za-z0-9_-]{1,128}\/art\/poster$/.test(logoPath)){noteSkipped('guide','channel.logoPath');logoPath='';}
  const mark = parseChannelMark(v.mark);
  ids.add(v.id);
  const source=sources.find(s=>s.id===v.sourceId&&s.provenance===v.provenance)!;
  if (source.generation!==v.generation||source.provenance!==v.provenance||!route&&v.programmes.length!==0) invalid();
  let previousEnd = 0;
  const programmes = keepReadable('guide', 'programmes', v.programmes, (raw: unknown) => {
   const p = parseGuideProgramme(raw, v.id as string);
   if (programmeIDs.has(p.id) || Date.parse(p.start) < previousEnd || route !== undefined && (Date.parse(p.end) <= Date.parse(route.start) || Date.parse(p.start) >= Date.parse(route.end)) || ++total > 10000) invalid();
   previousEnd = Date.parse(p.end); programmeIDs.add(p.id); return p;
  });
  return {id: v.id, sourceId: v.sourceId, provenance: v.provenance, name: v.name, number: (v.number ?? '') as string, group: (v.group ?? '') as string, generation: v.generation, logoPath: logoPath as string, ...(mark ? {mark} : {}), programmes, tuneAvailable, recordAvailable, tuneUnavailableReason, recordUnavailableReason, favorite: v.favorite === true, hidden: v.hidden === true, serverHidden: v.serverHidden === true, preferenceRevision: v.preferenceRevision as number} as GuideChannel;
 }); return channels;
}
export function parseChannelGuide(raw: unknown, serverId: string, route: GuideRoute): ChannelGuide {
 if (!object(raw) || raw.protocolVersion !== GUIDE_PROTOCOL || raw.serverId !== serverId || !object(raw.guide)) invalid();
 const g = raw.guide;
 if (!id(g.viewerFence) || g.start !== route.start || g.end !== route.end || g.timezone !== route.timezone || !instant(g.observedAt) || !text(g.nextCursor, 2048) || !Array.isArray(g.channels) || g.channels.length > 50 || !Array.isArray(g.sources) || g.sources.length > 2000) invalid();
 const sources=parseGuideSources(g.sources,route.kind);
 const channels=parseGuideChannels(g.channels,sources,route);
 // A state this app does not know reads from what arrived: a guide when there are channels to show, otherwise no guide data.
 const state = oneOfOr('guide', 'state', g.state, ['ready', 'no-sources', 'no-channels', 'no-guide-data', 'filter-empty', 'guide-stale', 'continuation-required'] as const, channels.length ? 'ready' : 'no-guide-data');
 return {state, viewerFence: g.viewerFence, start: g.start, end: g.end, timezone: g.timezone, sources, channels, nextCursor: g.nextCursor, observedAt: g.observedAt, ...(parseDays(g.days) !== undefined ? {days: parseDays(g.days)} : {})} as ChannelGuide;
}
function path(route: GuideRoute, cursor: string): string {
 const q = new URLSearchParams({kind: route.kind, start: route.start, end: route.end, timezone: route.timezone, search: route.search, sourceId: route.sourceId, limit: '30', cursor});
 if (route.favorites !== undefined) q.set('favorites', String(route.favorites));
 if (route.includeServerHidden) q.set('includeServerHidden', 'true');
 if (route.includeHidden !== undefined) q.set('includeHidden', String(route.includeHidden));
 if (route.group) q.set('group', route.group);
 return routePath("get_guide") + "?" + (q.toString());
}
/**
 * Why a channel can't be played or recorded, in plain words for viewers (US English). Server
 * codes that describe the server's setup (decoder, sandbox, delivery) say only that Live TV isn't
 * available here yet; owners see the precise cause in the server's diagnostics.
 */
export function unavailableMessage(reason: LinearUnavailableReason): string {
 switch (reason) {
  case '': return '';
  case 'recording-unavailable': return 'Recording isn\u2019t available on this server yet.';
  case 'not-recordable': return 'Library Channels play your own movies and shows, so they can\u2019t be recorded.';
  case 'source-disabled': return 'This channel\u2019s source is turned off.';
  case 'source-unavailable': return 'This channel\u2019s source isn\u2019t responding. Try again in a moment.';
  case 'capacity-unavailable': return 'Every tuner is busy right now. Try again when a recording or another viewer finishes.';
  case 'permission-denied': return 'You don\u2019t have access to this channel.';
  case 'schedule_preparing': return 'This channel\u2019s schedule is being prepared. Try again in a moment.';
  case 'no-schedule': return 'Nothing is scheduled on this channel right now.';
  case 'delivery-unavailable':
  case 'decoder_confinement_unavailable':
  case 'ffprobe_not_configured':
  case 'ffmpeg_not_configured':
  case 'decoder_dependencies_unavailable':
  case 'channel_runtime_unavailable':
  case 'delivery_unavailable': return 'Live TV isn\u2019t available on this server yet.';
 }
 return 'This channel isn\u2019t available right now.';
}
/** Reasons that describe the server rather than one channel: show one notice, not a reason on every row. */
const serverWideReasons: ReadonlySet<string> = new Set(['delivery-unavailable', 'decoder_confinement_unavailable', 'ffprobe_not_configured', 'ffmpeg_not_configured', 'decoder_dependencies_unavailable', 'channel_runtime_unavailable', 'delivery_unavailable', 'recording-unavailable']);
/** True when a reason is about the server's setup, not this channel (see `unavailableMessage`). */
export function unavailableIsServerWide(reason: LinearUnavailableReason | string): boolean {
 return serverWideReasons.has(reason);
}
/** A retry uses the same request ID and expected revision, never an optimistic local toggle. */
export async function saveChannelPreference(api: ChannelApi, serverId: string, channel: GuideChannel, requestId: string, values: {favorite: boolean; hidden: boolean}, signal?: AbortSignal): Promise<number> {
 const raw = await api.request<unknown>(channel.provenance === 'live-source' ? routePath("post_channels_preferences") : routePath("post_library_channels_preferences"), 'POST', routeBody("post_channels_preferences", {requestId, sourceId: channel.sourceId, channelId: channel.id, expectedRevision: channel.preferenceRevision, ...values}), signal);
 // A compact receipt proves acceptance, not the historical preference revision
 // needed by this caller. Leave it for current-state review in Pending changes.
 if (object(raw) && object(raw.mutationReceipt)) throw new UserMutationError('mutation_needs_review');
 if (!object(raw) || raw.protocolVersion !== GUIDE_PROTOCOL || raw.serverId !== serverId || !Number.isSafeInteger(raw.revision) || Number(raw.revision) <= channel.preferenceRevision) invalid();
 await api.acknowledgeUserMutation?.(requestId);
 return Number(raw.revision);
}
/** Native's secure UUID generator supplies the same 192-bit-shaped operation key as web. */
export async function channelOperationId(uuid: () => Promise<string>): Promise<string> {
 const values = [await uuid(), await uuid()];
 if (values.some(v => !/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i.test(v))) throw new Error('Could not create an operation identifier.');
 return values.map(v => v.replace(/-/g, '').toLowerCase()).join('').slice(0, 48);
}
export class ChannelGuideService{
 private snapshot:GuideSnapshot={phase:'idle',guide:null,route:null,error:null,hasPrevious:false};private listeners=new Set<()=>void>();private epoch=0;private disposed=false;private request:AbortController|null=null;private pages=[''];private fence='';
 private api:ChannelApi;private serverId:string;private timeoutMs:number;
 constructor(api:ChannelApi,serverId:string,timeoutMs=12000){this.api=api;this.serverId=serverId;this.timeoutMs=timeoutMs>0&&timeoutMs<=60000?timeoutMs:12000;}
 getSnapshot=()=>this.snapshot;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn)};
 private publish(s:GuideSnapshot){if(this.disposed)return;this.snapshot=Object.freeze(s);for(const fn of this.listeners)fn();}
 dispose(){this.disposed=true;this.epoch++;this.request?.abort();this.listeners.clear();}
 resetAccess(){this.request?.abort();this.epoch++;this.fence='';this.pages=[''];this.publish({phase:'idle',guide:null,route:null,error:null,hasPrevious:false});}
  async load(route:GuideRoute,mode:'replace'|'refresh'|'next'|'previous'='replace'):Promise<void>{
   if(this.disposed)return;const old=this.snapshot;const same=old.route&&JSON.stringify(route)===JSON.stringify(old.route);if(mode!=='replace'&&!same)return;if(mode==='next'&&!old.guide?.nextCursor)return;if(mode==='previous'&&this.pages.length<2)return;
   const pages=mode==='replace'?['']:mode==='next'?[...this.pages,old.guide!.nextCursor]:mode==='previous'?this.pages.slice(0,-1):this.pages.slice();const cursor=pages.at(-1)!;const epoch=++this.epoch;this.request?.abort();const abort=new AbortController();this.request=abort;const retained=same?old.guide:null;this.publish({phase:retained?'retained-refreshing':'loading',guide:retained,route,error:null,hasPrevious:pages.length>1});
   let overall:ReturnType<typeof setTimeout>|undefined;
   try{const raw=await Promise.race([this.fetchWithRateLimitRetry(path(route,cursor),abort.signal),new Promise<never>((_,reject)=>{overall=setTimeout(()=>{abort.abort();reject(new Error())},this.timeoutMs)})]);if(this.disposed||epoch!==this.epoch)return;if(abort.signal.aborted)throw new Error();const guide=parseChannelGuide(raw,this.serverId,route);if(this.fence&&guide.viewerFence!==this.fence){this.publish({phase:'error',guide:null,route,error:'Your channel access changed. Reopen Channels to continue.',hasPrevious:false});return}this.fence=guide.viewerFence;this.pages=pages;this.publish({phase:'ready',guide,route,error:null,hasPrevious:pages.length>1});}
   catch(error){if(!this.disposed&&epoch===this.epoch){const denied=object(error)&&(error.status===401||error.status===403);this.publish({phase:'error',guide:denied?null:retained,route,error:denied?'Your channel access changed. Reopen Channels to continue.':'The guide could not be refreshed. Try again.',hasPrevious:!denied&&this.pages.length>1});}}
   finally{if(overall)clearTimeout(overall);if(epoch===this.epoch)this.request=null;}
  }
  /**
   * M25-2: one `429` while loading a guide block retries that block
   * automatically, honouring `Retry-After` (seconds or HTTP-date) with bounded
   * backoff and jitter (at most a few attempts). Only then is the error shown.
   */
  private async fetchWithRateLimitRetry(requestPath:string,signal:AbortSignal):Promise<unknown>{
   let attempt=0;
   for(;;){
    try{
     return await this.api.request<unknown>(requestPath,'GET',undefined,signal);
    }catch(error){
     attempt++;
     if(attempt>=GUIDE_RATE_LIMIT_ATTEMPTS||!isRateLimited(error)||signal.aborted||this.disposed)throw error;
     const delay=rateLimitDelay(error,attempt);
     await new Promise<void>((resolve,reject)=>{
      const retryTimer=setTimeout(resolve,delay);
      signal.addEventListener('abort',()=>{clearTimeout(retryTimer);reject(new Error());},{once:true});
     });
     if(signal.aborted||this.disposed)throw error;
    }
   }
  }
}
export function guideWindow(date:Date=new Date()):Pick<GuideRoute,'start'|'end'>{const start=new Date(Math.floor(date.getTime()/3600000)*3600000);return{start:start.toISOString().replace('.000Z','Z'),end:new Date(start.getTime()+3*3600000).toISOString().replace('.000Z','Z')};}
/**
 * FEAT-04: group chips — the top `limit` channel groups by channel count (ties keep server
 * order), plus All. The chips bind to the route's `group` parameter; a filtered guide stays windowed.
 */
export function topChannelGroups(channels:readonly{group:string}[],limit=6):string[]{
 const counts=new Map<string,number>();
 for(const c of channels){if(!c.group)continue;counts.set(c.group,(counts.get(c.group)??0)+1);}
 return [...counts.entries()].sort((a,b)=>b[1]-a[1]).map(([g])=>g).slice(0,Math.max(0,limit));
}
/** FEAT-04: the day strip — Today, then the next `count-1` days as local day starts (DST-safe). */
export function stripDays(nowMs:number,timeZone:string,count=7):number[]{
 const out=[localDayStart(nowMs,timeZone)];
 while(out.length<count){const prev=out[out.length-1] as number;out.push(localDayStart(prev+26*HOUR_MS,timeZone));}
 return out;
}
/**
 * FEAT-04: the strip's jump target — 18:00 local that day (prime time, §3.4). The offset is
 * re-read at the candidate so a 23/25-hour DST day still lands on the local 18:00 hour.
 */
export function dayPrimeTime(dayStartMs:number,timeZone:string,hour=18):number{
 let guess=dayStartMs+hour*HOUR_MS;
 for(let i=0;i<3;i++){
  const wall=guess+zoneOffset(guess,timeZone);
  const d=new Date(wall);
  const wallMs=d.getUTCHours()*HOUR_MS+d.getUTCMinutes()*MINUTE_MS+d.getUTCSeconds()*1000+d.getUTCMilliseconds();
  const diff=hour*HOUR_MS-wallMs;
  if(!diff)break;
  guess+=diff;
 }
 return guess;
}
