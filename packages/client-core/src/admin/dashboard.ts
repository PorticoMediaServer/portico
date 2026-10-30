import {parseReportInterval,parseReportContext,verifyReportInterval,reportQuery,type ReportContext} from '../report-context.ts';
import {parseTelemetryReading} from '../telemetry.ts';
import {parsePlayHistory,parsePlaySummary} from '../play-history.ts';
import {decodeGetAdminDashboardReportResponse} from '../../../contracts/src/decoders.ts';
import type {GetAdminDashboardReportResponse} from '../../../contracts/src/types.ts';
import {decodeGetAdminDashboardStatusResponse,decodeGetAdminPlayHistoryDashboardResponse,decodeGetAdminPlayHistoryPeopleResponse} from '../../../contracts/src/decoders.ts';
import type {GetAdminDashboardStatusResponse,GetAdminPlayHistoryDashboardResponse,GetAdminPlayHistoryPeopleResponse} from '../../../contracts/src/types.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
import type {PlayHistoryApi,PlayHistoryFilters} from '../play-history.ts';

/** Owner analytics are separate from administrator People management. */
export type DashboardStatus=GetAdminDashboardStatusResponse;
export type DashboardHistory=GetAdminPlayHistoryDashboardResponse;
export type DashboardPeople=GetAdminPlayHistoryPeopleResponse;
export type DashboardReport=GetAdminDashboardReportResponse;
export type DashboardReportFilters=PlayHistoryFilters & Readonly<{window:(typeof DASHBOARD_LOAD_RANGES)[number]|'today';peoplePeriod:PlayHistoryFilters["period"];topPeriod:PlayHistoryFilters["period"];topKind:DashboardTopKind;topAccountId?:string;peopleCursor?:string;peopleLimit?:number;tableCursor?:string;tableLimit?:number}>;
export type DashboardTopKind='movies'|'tv'|'music';
export const DASHBOARD_LOAD_RANGES=['live','12h','24h','7d','30d','90d','1y','all'] as const;
export const DASHBOARD_LOAD_SERIES={
 bandwidth:{unit:'mbps',metrics:['awayMbps','homeMbps']},
 processor:{unit:'percent',metrics:['processCPU','cpu']},
 memory:{unit:'percent',metrics:['processMemory','memory']},
 conversions:{unit:'count',metrics:['hardwareConversions','softwareConversions']},
} as const;
const whole=(v:number)=>Number.isSafeInteger(v)&&v>=0;
function invalid():never {throw Object.assign(new Error('Invalid Dashboard data.'),{code:'invalid_dashboard_data'});}
const kinds=new Set(['movies','tv','music','audiobooks','live','other']);
function times(values:readonly {kind:string;plays:number;watchedMs:number}[]):void {
 if(values.length>6 || new Set(values.map(v=>v.kind)).size!==values.length)invalid();
 for(const v of values)if(!kinds.has(v.kind)||!whole(v.plays)||!whole(v.watchedMs))invalid();
}
export function parseDashboardPeople(value:unknown):DashboardPeople {
 const out=decodeGetAdminPlayHistoryPeopleResponse(value);parseReportInterval(out);
 if(out.items.length>100 || out.nextCursor.length>512 || new Set(out.items.map(v=>v.accountId)).size!==out.items.length)invalid();
 let previous:DashboardPeople['items'][number]|undefined;
 for(const p of out.items) {
  if(!p.accountId || p.accountId.length>128 || p.username.length>256 || p.profiles.length>8 || p.profiles.some(v=>v.length>256) || !whole(p.plays)||!whole(p.watchedMs))invalid();
  times(p.kinds);
  if(p.kinds.reduce((s,k)=>s+k.plays,0)!==p.plays || p.kinds.reduce((s,k)=>s+k.watchedMs,0)!==p.watchedMs)invalid();
  if(previous && (previous.watchedMs<p.watchedMs || previous.watchedMs===p.watchedMs && previous.accountId>=p.accountId))invalid();
  previous=p;
 }
 return out;
}
export function parseDashboardHistory(value:unknown):DashboardHistory {
 const out=decodeGetAdminPlayHistoryDashboardResponse(value);parseReportInterval(out);
 const from=Date.parse(out.from),until=Date.parse(out.until);
 if(!Number.isFinite(from)||!Number.isFinite(until)||from>until||out.buckets.length>1201||out.top.length>6||out.busiestTimes.length!==7)invalid();
 let end=from;
 for(const b of out.buckets) {
  const a=Date.parse(b.start),z=Date.parse(b.end);
  if(!Number.isFinite(a)||!Number.isFinite(z)||a!==end||a>z||z>until)invalid();
  times(b.kinds);end=z;
 }
 for(const day of out.busiestTimes)if(day.length!==24 || day.some(v=>!whole(v)))invalid();
 if(out.quietestStartHour!==null && (!whole(out.quietestStartHour)||out.quietestStartHour>21))invalid();
 if(new Set(out.top.map(v=>v.itemId)).size!==out.top.length)invalid();
 for(const item of out.top)if(!item.itemId || !whole(item.plays)||!whole(item.people)||item.people>item.plays||!['movie_plays','episode_plays','track_plays'].includes(item.countUnit))invalid();
 return out;
}
export function parseDashboardStatus(value:unknown):DashboardStatus {
 const out=decodeGetAdminDashboardStatusResponse(value);
 if(!Number.isFinite(Date.parse(out.observedAt)) || out.alerts.length>24 || out.scans.length>1000 || out.tasks.items.length>3)invalid();
 for(const a of out.alerts)if(!a.code||!a.action.kind||Object.keys(a.params).length>8||Object.values(a.params).some(v=>v.length>1024)||a.count!==undefined&&!whole(a.count))invalid();
 return out;
}
function query(filters:PlayHistoryFilters):URLSearchParams {
 const out=reportQuery(filters);out.set("period",filters.period);
 if(filters.accountId)out.set('accountId',filters.accountId);
 if(filters.libraryId)out.set('libraryId',filters.libraryId);
 if(filters.media)out.set('media',filters.media);
 if(filters.search)out.set('q',filters.search);
 return out;
}
/** One producer response from one actual request-bounded read transaction. */
export function parseDashboardReport(value:unknown):DashboardReport{
 const out=decodeGetAdminDashboardReportResponse(value);const context=parseReportContext(out.context);if(out.load.window==='10m'||out.load.window==='1h')invalid();
 parseDashboardHistory(out.history);parseDashboardPeople(out.people);parsePlayHistory(out.table);parsePlaySummary(out.summary);parseReportInterval(out.topPlayed);parseTelemetryReading(out.load);
 const intervals=new Map<string,string>();
 for(const panel of [out.history,out.people,out.summary,out.topPlayed,out.table,out.load]){
  if(panel.timeZone!==context.timeZone||panel.period!=='live'&&Date.parse(panel.until)!==Date.parse(context.until))invalid();
  if(panel.period==='live')continue;
  // Same-period panels must return identical producer intervals, including
  // All time's common start. This validates metadata; it resolves no calendar.
  const identity=JSON.stringify([Date.parse(panel.from),Date.parse(panel.until),panel.buckets.map(b=>[Date.parse(b.start),Date.parse(b.end),b.partial])]);
  const previous=intervals.get(panel.period);if(previous!==undefined&&previous!==identity)invalid();
  intervals.set(panel.period,identity);
 }
 if(out.table.total!==undefined&&out.table.total!==out.summary.plays)invalid();
 const plays=out.history.buckets.reduce((s,b)=>s+b.kinds.reduce((s,k)=>s+k.plays,0),0);
 const watched=out.history.buckets.reduce((s,b)=>s+b.kinds.reduce((s,k)=>s+k.watchedMs,0),0);
 if(plays!==out.summary.plays||watched!==out.summary.watchedMs||out.history.busiestTimes.flat().reduce((s,k)=>s+k,0)!==watched)invalid();
 return out;
}
/** Actual producer reads only. No graph generation, interpolation or name keys. */
export class DashboardDataClient {
 private api:PlayHistoryApi;
 constructor(api:PlayHistoryApi){this.api=api;}
 async report(filters:DashboardReportFilters,signal?:AbortSignal):Promise<DashboardReport>{
  const q=query(filters);
  for(const key of ['window','peoplePeriod','topPeriod','topKind','topAccountId','peopleCursor','peopleLimit','tableCursor','tableLimit','sort','direction'] as const){const v=filters[key];if(v!==undefined)q.set(key,String(v));}
  const out=parseDashboardReport(await this.api.request(routePath('get_admin_dashboard_report')+'?'+q,'GET',undefined,signal));
  if(out.context.timeZone!==filters.timeZone||Date.parse(out.context.until)!==Date.parse(filters.until)||out.topPlayed.topKind!==filters.topKind)invalid();
  for(const [panel,period] of [[out.history,filters.period],[out.summary,filters.period],[out.table,filters.period],[out.people,filters.peoplePeriod],[out.topPlayed,filters.topPeriod],[out.load,filters.window]] as const)verifyReportInterval(panel,filters,period,period==='live');
  return out;
 }
 async status(signal?:AbortSignal):Promise<DashboardStatus>{
  return parseDashboardStatus(await this.api.request(routePath('get_admin_dashboard_status'),'GET',undefined,signal));
 }
 async people(filters:PlayHistoryFilters,cursor='',limit=12,signal?:AbortSignal):Promise<DashboardPeople>{
  const q=query(filters);q.set('limit',String(Math.max(1,Math.min(100,limit))));if(cursor)q.set('cursor',cursor);
  const out=parseDashboardPeople(await this.api.request(routePath('get_admin_play_history_people')+'?'+q,'GET',undefined,signal));verifyReportInterval(out,filters,filters.period);return out;
 }
 async history(filters:PlayHistoryFilters,topKind:DashboardTopKind='movies',signal?:AbortSignal):Promise<DashboardHistory>{
  const q=query(filters);q.set('topKind',topKind);
  const out=parseDashboardHistory(await this.api.request(routePath('get_admin_play_history_dashboard')+'?'+q,'GET',undefined,signal));verifyReportInterval(out,filters,filters.period);return out;
 }
}
