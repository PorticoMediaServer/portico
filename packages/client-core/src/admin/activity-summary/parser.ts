import {parseHardwareExecution} from '../../delivery.ts';
import {currentUTCTimestamp, utcOrderKey} from '../../local-session.ts';
import type {OwnerActivitySummary, DashboardSessionsPage} from './types.ts';
import {parseDashboardStatus} from '../dashboard.ts';
import {adminSessionFromServer} from '../server-now-playing.ts';

type Reader = (value: unknown) => unknown;
type ObjectValue = Record<string, unknown>;
export class ActivitySummaryContractError extends Error {
  readonly code = 'invalid_activity_summary';
  constructor() { super('invalid_activity_summary'); this.name = 'ActivitySummaryContractError'; }
}
function invalid(): never { throw new ActivitySummaryContractError(); }
const object = (v: unknown): v is ObjectValue => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (max: number, nonempty = false): Reader => v => typeof v === 'string' && v.length <= max && (!nonempty || v.length > 0) && !/[\x00-\x1f\x7f]/.test(v) ? v : invalid();
const id = text(512, true), label = text(256, true), words = text(4096);
const integer = (max = Number.MAX_SAFE_INTEGER, min = 0): Reader => v => typeof v === 'number' && Number.isSafeInteger(v) && v >= min && v <= max ? v : invalid();
const count = integer();
const bool: Reader = v => typeof v === 'boolean' ? v : invalid();
/** #682: a frame observation not yet made is `null`; it is left out (unknown), never read as false. */
const observed: Reader = v => v === null ? undefined : bool(v);
const timestamp: Reader = v => currentUTCTimestamp(v) ? v : invalid();
/** Reject accidental capability/private additions rather than carrying opaque server objects. */
function record(required: Record<string, Reader>, optional: Record<string, Reader> = {}): Reader {
  return value => {
    if (!object(value) || Object.keys(value).some(k => !Object.hasOwn(required, k) && !Object.hasOwn(optional, k))) invalid();
    const out: ObjectValue = {};
    for (const [key, read] of Object.entries(required)) {
      if (!Object.hasOwn(value, key)) invalid();
      out[key] = read(value[key]);
    }
    for (const [key, read] of Object.entries(optional)) if (Object.hasOwn(value, key)) {const read_ = read(value[key]); if (read_ !== undefined) out[key] = read_;}
    return Object.freeze(out);
  };
}
function list(read: Reader, max = Number.MAX_SAFE_INTEGER, uniqueKey?: string): Reader {
  return value => {
    if (!Array.isArray(value) || value.length > max) invalid();
    const out = value.map(read);
    if (uniqueKey && new Set(out.map(v => (v as ObjectValue)[uniqueKey])).size !== out.length) invalid();
    return Object.freeze(out);
  };
}
const named = record({id, name: words});
const decision = record({action: label, reasons: list(text(512, true), 128)}, {to: label, channels: integer(256, 1)});
const decisions = record({}, {video: decision, audio: decision, subtitles: decision});
const art = record({id, kind: label, role: label, revision: id});
const airDate: Reader = v => typeof v === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v) && currentUTCTimestamp(v + 'T00:00:00Z') ? v : invalid();
const item = record({id, title: words, kind: label, art: list(art, 4)}, {
  showId: id, showTitle: words, seasonNumber: count, episodeNumber: count,
  episodeNumbering: label, airDate, year: integer(9999, 1), artist: words, album: words, author:words, bookTitle:words,
});
const qualityShape = record({mode: label}, {id: label, maxHeight: integer(8640, 1), maxVideoBitrateKbps: integer(2_000_000, 1), maxAudioBitrateKbps: integer(10_000, 1)});
const quality: Reader = v => {
  const out = qualityShape(v) as ObjectValue;
  const numbers = ['maxHeight', 'maxVideoBitrateKbps', 'maxAudioBitrateKbps'].some(k => Object.hasOwn(out, k));
  // Every mode the server publishes (playbackv1/quality.go). CD quality, like original, carries no ladder id or bounds (OW-2).
  if (!['original', 'cd-quality', 'fixed', 'automatic', 'limit'].includes(out.mode as string) ||
      (out.mode === 'original' || out.mode === 'cd-quality') && (numbers || out.id !== undefined) ||
      out.mode === 'fixed' && (numbers || out.id === undefined) ||
      out.mode === 'automatic' && numbers || out.mode === 'limit' && out.id !== undefined) invalid();
  return out;
};
const choice = (values: readonly string[]): Reader => v => typeof v === 'string' && values.includes(v) ? v : invalid();
const finite: Reader = v => typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : invalid();
const conversion = record({speed:finite, observedAt:timestamp, cadenceMs:integer(1000,1000)});
const problemCode=choice(['buffering','behind']);
const channel=record({name:words},{number:words});
const programme=record({title:words,endsAt:timestamp});
function qualifyProblem(out:ObjectValue,converting:boolean):void {
 if (out.problem==='buffering' && (out.state!=='buffering' || !out.bufferingSince)) invalid();
 if (out.problem==='behind' && (!['playing','buffering'].includes(out.state as string) || !converting || !(out.transcode as {speed?:number}|undefined) || ((out.transcode as {speed:number}).speed>=1))) invalid();
 if (out.transcode && !converting) invalid();
}
/** The rebuilt server's owner summary (`GET /v1/admin/activity`, server-next/internal/api/activity_summary.go): the
 * first three streams of Now playing in its order and how many there are, the Dashboard's scans and tasks, its major
 * alerts and update_available, and when a scan last finished. Streams are checked as Now playing checks them; scans,
 * tasks and alerts as the Dashboard checks them, with the same subject and reason words. */
export function parseActivitySummary(value: unknown): OwnerActivitySummary {
  if (!object(value) || !object(value.streams) || !Array.isArray(value.streams.items)) invalid();
  const streams = value.streams as ObjectValue, items = streams.items as unknown[];
  timestamp(value.observedAt); words(value.serverName);
  if (value.lastScanAt !== undefined) timestamp(value.lastScanAt);
  const total = count(streams.count) as number, converting = count(streams.converting) as number;
  if (items.length !== Math.min(total, 3) || converting > total) invalid();
  try {for (const item of items) {adminSessionFromServer(item); if (!['original', 'repackaged', 'converted'].includes((item as ObjectValue).route as string)) invalid();}} catch {invalid();}
  if (new Set(items.map(item => (item as ObjectValue).id)).size !== items.length ||
      converting < items.filter(item => (item as ObjectValue).route === 'converted').length) invalid();
  let board: ReturnType<typeof parseDashboardStatus>;
  try {board = parseDashboardStatus({observedAt: value.observedAt, alerts: value.alerts, scans: value.scans, tasks: value.tasks});} catch {invalid();}
  return Object.freeze({observedAt: value.observedAt as string, serverName: value.serverName as string,
    streams: Object.freeze({count: total, converting, items: Object.freeze(items as OwnerActivitySummary['streams']['items'])}),
    scans: board.scans, tasks: board.tasks, alerts: board.alerts, ...(value.lastScanAt !== undefined ? {lastScanAt: value.lastScanAt as string} : {})});
}

function freshSession(item:ObjectValue,observedAt:string):void {
 const at=Date.parse(observedAt);
 if (item.transcode) {const stamp=Date.parse((item.transcode as {observedAt:string}).observedAt);if(stamp>at || at-stamp>5000)invalid();}
 if (item.problem==='buffering' && at-Date.parse(item.bufferingSince as string)<10000)invalid();
}
const sourceVideo=record({id,codec:label},{interlaced:bool,profile:label,level:count,bitDepth:count,width:count,height:count,fps:finite,hdr:label,bitrateKbps:count,chromaSubsampling:label,frameRateNumerator:count,frameRateDenominator:count,frameRateMode:label,dolbyVisionProfile:count,dolbyVisionCompatibility:count,dolbyVisionRpu:observed,dolbyVisionBaseLayer:observed,dolbyVisionEnhancementType:label,dolbyVisionCmVersion:label,hdr10Plus:observed,masterDisplay:text(1024),maxCll:text(128)});
const sourceAudio=record({id,codec:label,atmos:bool,default:bool,commentary:bool,visualImpaired:bool},{profile:label,channels:count,layout:label,language:label,title:words,bitDepth:count,sampleRate:count,variant:label,objectAudio:label,dtsx:bool});
const sourceSubtitles=record({id,format:label},{language:label});
const media=record({},{video:sourceVideo,audio:sourceAudio,subtitles:sourceSubtitles,videoHardware:bool});
const hardwareExecution:Reader=v=>parseHardwareExecution(v)??invalid();
const hardware=record({backend:choice(['software','videotoolbox','vaapi','qsv','nvenc','amf']),stages:list(record({operation:choice(['decode','deinterlace','tone_map','subtitle_burn_in','scale','encode']),execution:choice(['hardware','software'])}),16)},{execution:hardwareExecution});
const delivery=record({streams:list(record({kind:label,inputCodec:text(128),outputCodec:text(128),action:label}),4)},{hardware,videoHeight:count,videoBitrateBps:count,audioBitrateBps:count,audioChannels:count});
/** Whitelist display facts; never retain a producer's capability or path. */
export function parseDashboardSessionFacts(value:unknown):Record<string,unknown>{
 if(!object(value))invalid();
 const fields={app:text(256),platform:label,address:text(256),currentBitrateKbps:(v:unknown)=>v===null?null:finite(v),rateWindowMs:integer(5000,5000),live:bool,media,delivery,item};
 const chosen=Object.fromEntries(Object.keys(fields).filter(k=>Object.hasOwn(value,k) && (k!=='item' || object(value.item) && Array.isArray(value.item.art))).map(k=>[k,value[k]]));
 return record({},fields)(chosen) as Record<string,unknown>;
}
const adminSession:Reader=v=>{
 const out=record({id,protocol:choice(['v1']),user:named,profile:named,device:named,kind:label,state:label,positionMs:count,decision:decisions,location:label,startedAt:timestamp},{item,durationMs:integer(Number.MAX_SAFE_INTEGER,1),bitrateKbps:integer(Number.MAX_SAFE_INTEGER,1),bandwidthKbps:integer(Number.MAX_SAFE_INTEGER,1),engine:label,quality,videoHeight:integer(Number.MAX_SAFE_INTEGER,1),problem:problemCode,bufferingSince:timestamp,transcode:conversion,channel,programme,app:text(256),platform:label,address:text(256),currentBitrateKbps:(v:unknown)=>v===null?null:finite(v),rateWindowMs:integer(5000,5000),live:bool,media,delivery})(v) as ObjectValue;
 const d=out.decision as {video?:{action:string};audio?:{action:string};subtitles?:{action:string}};
 qualifyProblem(out,['transcode','burn'].includes(d.video?.action??'') || d.audio?.action==='transcode' || d.subtitles?.action==='burn');
 return out;
};
/** Server-composed, bounded current page; never fetches or reorders hidden pages. */
export function parseDashboardSessions(value:unknown):DashboardSessionsPage {
 const out=record({scope:record({serverId:id,viewerFence:text(64,true)}),observedAt:timestamp,items:list(adminSession,200,'id'),page:record({limit:integer(200,1),total:count,start:count,revision:text(1024)},{nextCursor:text(2048,true)}),totals:record({streams:count,paused:count,converting:count,bandwidthKbps:count,people:count,atHome:count,away:count})})(value) as DashboardSessionsPage;
 if(!/^[a-f0-9]{64}$/.test(out.scope.viewerFence) || out.page.total!==out.totals.streams || out.items.length>out.page.limit || out.items.length>out.totals.streams || out.totals.paused>out.totals.streams || out.totals.converting>out.totals.streams || out.totals.people>out.totals.streams || out.totals.atHome+out.totals.away>out.totals.streams)invalid();
 let previous:ObjectValue|undefined;
 const priority=(v:ObjectValue)=>v.problem?0:(()=>{const d=v.decision as any;return ['transcode','burn'].includes(d.video?.action??'') || d.audio?.action==='transcode' || d.subtitles?.action==='burn'?1:2;})();
 for(const raw of out.items){const item=raw as ObjectValue;freshSession(item,out.observedAt);if(previous){const p=priority(previous),c=priority(item),pt=utcOrderKey(previous.startedAt as string),ct=utcOrderKey(item.startedAt as string);if(p>c || p===c && (pt<ct || pt===ct && (previous.id as string)>(item.id as string)))invalid();}previous=item;}
 return out;
}
