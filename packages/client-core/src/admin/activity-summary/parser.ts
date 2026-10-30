import {parseHardwareExecution} from '../../delivery.ts';
import {currentUTCTimestamp, utcOrderKey} from '../../local-session.ts';
import type {OwnerActivitySummary, DashboardSessionsPage} from './types.ts';

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
    for (const [key, read] of Object.entries(optional)) if (Object.hasOwn(value, key)) out[key] = read(value[key]);
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
  if (!['original', 'fixed', 'automatic', 'limit'].includes(out.mode as string) ||
      out.mode === 'original' && (numbers || out.id !== undefined) ||
      out.mode === 'fixed' && (numbers || out.id === undefined) ||
      out.mode === 'automatic' && numbers || out.mode === 'limit' && out.id !== undefined) invalid();
  return out;
};
const username: Reader = value => {
  const out = text(256, true)(value) as string;
  return out.trim() ? out : invalid();
};
const choice = (values: readonly string[]): Reader => v => typeof v === 'string' && values.includes(v) ? v : invalid();
const finite: Reader = v => typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : invalid();
const conversion = record({speed:finite, observedAt:timestamp, cadenceMs:integer(1000,1000)});
const problemCode=choice(['buffering','behind']);
const channel=record({name:words},{number:words});
const programme=record({title:words,endsAt:timestamp});
const sessionShape = record({id, user: record({id}, {username}), profile: named, device: named, kind: label, state: label,
  startedAt: timestamp, converting: bool, positionMs: count, decision: decisions},
  {durationMs: integer(Number.MAX_SAFE_INTEGER, 1), item, quality, videoHeight: integer(Number.MAX_SAFE_INTEGER, 1), problem: problemCode, bufferingSince:timestamp, transcode:conversion, channel, programme});
function qualifyProblem(out:ObjectValue,converting:boolean):void {
 if (out.problem==='buffering' && (out.state!=='buffering' || !out.bufferingSince)) invalid();
 if (out.problem==='behind' && (!['playing','buffering'].includes(out.state as string) || !converting || !(out.transcode as {speed?:number}|undefined) || ((out.transcode as {speed:number}).speed>=1))) invalid();
 if (out.transcode && !converting) invalid();
}
const session: Reader = value => {
  const out = sessionShape(value) as ObjectValue;
  const decision = out.decision as {video?: {action: string}; audio?: {action: string}; subtitles?: {action: string}};
  const converting = ['transcode', 'burn'].includes(decision.video?.action ?? '') || decision.audio?.action === 'transcode' || decision.subtitles?.action === 'burn';
  if (out.converting !== converting) invalid();
  qualifyProblem(out,converting);
  return out;
};
const streams: Reader = value => {
  const out = record({count, convertingCount: count, overflow: record({count, convertingCount: count}), items: list(session, 3, 'id')})(value) as ObjectValue;
  const items = out.items as ObjectValue[], total = out.count as number, converting = out.convertingCount as number;
  const overflow = out.overflow as {count: number; convertingCount: number};
  if (items.length !== Math.min(total, 3) || converting > total || overflow.count !== total - items.length ||
      overflow.convertingCount !== converting - items.filter(item => item.converting).length || overflow.convertingCount > overflow.count) invalid();
  const priority = (item: ObjectValue) => item.problem ? 0 : item.converting ? 1 : 2;
  for (let n = 1; n < items.length; n++) {
    const previous = items[n - 1], current = items[n], p = priority(previous), c = priority(current);
    const pt = utcOrderKey(previous.startedAt as string), ct = utcOrderKey(current.startedAt as string);
    if (p > c || p === c && (pt < ct || pt === ct && (previous.id as string) > (current.id as string))) invalid();
  }
  return out;
};
const relativeFile: Reader = v => {
  const path = text(4096, true)(v) as string;
  if (/^(?:[/\\]|[A-Za-z]:)/.test(path) || path.split(/[/\\]/).some(p => p === '..') || path === '.') invalid();
  return path;
};
const scanShape = record({libraryId: id, libraryName: words, kind: label, discovered: count, analyzed: count, phase: label}, {total: count, current: relativeFile});
const scan: Reader = v => {
  const out = scanShape(v) as ObjectValue;
  if ((out.analyzed as number) > (out.discovered as number) || out.total !== undefined && out.total !== out.discovered) invalid();
  return out;
};
const taskShape = record({id, kind: label, state: label, phase: words}, {libraryId: id, libraryName: words, processed: count, total: count, unit: label});
const task: Reader = v => {
  const out = taskShape(v) as ObjectValue;
  if (out.total !== undefined && (out.processed === undefined || (out.processed as number) > (out.total as number))) invalid();
  return out;
};
const capped = (read: Reader, cap: number): Reader => value => {
  const out = record({count, items: list(read, cap, 'id')})(value) as ObjectValue;
  if ((out.items as unknown[]).length !== Math.min(out.count as number, cap)) invalid();
  return out;
};
const updateShape = record({available: bool, state: label, current: text(256)}, {latest: text(256, true)});
const update: Reader = v => {
  const out = updateShape(v) as ObjectValue;
  if (!['unconfigured', 'unavailable', 'current', 'available'].includes(out.state as string) || out.available !== (out.state === 'available') || out.available && out.latest === undefined) invalid();
  return out;
};
const majorIssueShape=record({kind:choice(['library','storage','backups','networking','remote-source']),code:choice(['library_unreachable','storage_nearly_full','backup_failed','secure_renewal_failed','remote_unreachable','remote_program_unavailable']),severity:choice(['playback_stop','data_risk']),subject:words,destination:record({kind:label,target:words})});
const majorIssue:Reader=v=>{
 const out=majorIssueShape(v) as ObjectValue,d=out.destination as {kind:string;target:string};
 const expected:Record<string,readonly[string,string,string,string|undefined]>={library_unreachable:['library','playback_stop','library-source',undefined],remote_unreachable:['remote-source','playback_stop','remote-source',undefined],remote_program_unavailable:['remote-source','playback_stop','remote-source',undefined],storage_nearly_full:['storage','data_risk','storage','state'],backup_failed:['backups','data_risk','backups',''],secure_renewal_failed:['networking','data_risk','networking','certificate']};
 const e=expected[out.code as string];if(out.kind!==e[0] || out.severity!==e[1] || d.kind!==e[2] || e[3]===undefined && !d.target || e[3]!==undefined && d.target!==e[3])invalid();return out;
};
const summary = record({
  scope: record({serverId: id, viewerFence: text(64, true)}), observedAt: timestamp, serverName: words,
  streams, scans: list(scan, Number.MAX_SAFE_INTEGER, 'libraryId'), tasks: capped(task, 3),
  update, health: record({degraded: bool}),
 majorIssues:list(majorIssue,20),
}, {lastScanFinishedAt: timestamp});

/** One current owner document; no tolerant defaults, joined captions or invented denominators. */
export function parseActivitySummary(value: unknown): OwnerActivitySummary {
  const out = summary(value) as OwnerActivitySummary;
  if (!/^[a-f0-9]{64}$/.test(out.scope.viewerFence)) invalid();
  for (const item of out.streams.items) freshSession(item as ObjectValue,out.observedAt);
  return out;
}

function freshSession(item:ObjectValue,observedAt:string):void {
 const at=Date.parse(observedAt);
 if (item.transcode) {const stamp=Date.parse((item.transcode as {observedAt:string}).observedAt);if(stamp>at || at-stamp>5000)invalid();}
 if (item.problem==='buffering' && at-Date.parse(item.bufferingSince as string)<10000)invalid();
}
const sourceVideo=record({id,codec:label},{interlaced:bool,profile:label,level:count,bitDepth:count,width:count,height:count,fps:finite,hdr:label,bitrateKbps:count,chromaSubsampling:label,frameRateNumerator:count,frameRateDenominator:count,frameRateMode:label,dolbyVisionProfile:count,dolbyVisionCompatibility:count,dolbyVisionRpu:bool,dolbyVisionBaseLayer:bool,dolbyVisionEnhancementType:label,dolbyVisionCmVersion:label,hdr10Plus:bool,masterDisplay:text(1024),maxCll:text(128)});
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
