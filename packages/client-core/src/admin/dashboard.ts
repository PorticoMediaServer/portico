import {parseReportInterval,parseReportContext,verifyReportInterval,reportQuery,type ReportContext} from '../report-context.ts';
import {parseTelemetryReading} from '../telemetry.ts';
import {parsePlayHistory,parsePlaySummary} from '../play-history.ts';
import {decodeGetAdminDashboardReportResponse} from '../../../contracts/src/decoders.ts';
import type {GetAdminDashboardReportResponse} from '../../../contracts/src/types.ts';
import {decodeGetAdminPlayHistoryDashboardResponse,decodeGetAdminPlayHistoryPeopleResponse} from '../../../contracts/src/decoders.ts';
import type {GetAdminPlayHistoryDashboardResponse,GetAdminPlayHistoryPeopleResponse} from '../../../contracts/src/types.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';
import type {Dashboard} from '../../../contracts/src/next/api.ts';
import {callServer} from '../query/server-api.ts';
import type {PlayHistoryApi,PlayHistoryFilters} from '../play-history.ts';
import {dependsOn} from '../freshness-hub.ts';
import {queryKeys} from '../query/keys.ts';
import type {ViewerQueryOptions} from '../query/react.ts';

/** Owner analytics are separate from administrator People management. */
/** The rebuilt server's Dashboard read (`GET /v1/admin/dashboard`): alerts, scans, tasks and the totals of now. */
export type DashboardStatus=Dashboard;
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
const record=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const name=(v:unknown)=>typeof v==='string'&&v.length>0&&v.length<=4096;
const count=(v:unknown)=>typeof v==='number'&&whole(v);
const optionalWhole=(v:unknown)=>v===undefined||count(v);
/**
 * Checks the Dashboard read and words its alerts the way the page shows them: `subject` is the library or
 * remote source the alert is about, and `reason` is a remote source's problem as text (the server's own
 * `reason` there is a code: unreachable or sign_in).
 */
export function parseDashboardStatus(value:unknown):DashboardStatus {
 if(!record(value)||typeof value.observedAt!=='string'||!Number.isFinite(Date.parse(value.observedAt))||!Array.isArray(value.alerts)||value.alerts.length>24||
    !Array.isArray(value.scans)||value.scans.length>1000||!record(value.tasks)||!count(value.tasks.count)||!Array.isArray(value.tasks.items)||value.tasks.items.length>3)invalid();
 const alerts=(value.alerts as unknown[]).map(a=>{
  if(!record(a)||!name(a.code)||typeof a.severity!=='string'||typeof a.major!=='boolean'||!record(a.params)||!record(a.action)||!name(a.action.kind)||typeof a.action.target!=='string'||!optionalWhole(a.count))invalid();
  const params=a.params as Record<string,unknown>;
  if(Object.keys(params).length>8||Object.values(params).some(v=>typeof v!=='string'||v.length>1024))invalid();
  const p=params as Record<string,string>,subject=p.library??p.source,reason=a.code==='remote_unreachable'?p.problem:p.reason;
  return {...a,params:{...p,...(subject?{subject}:{}),...(reason?{reason}:{})}} as Dashboard['alerts'][number];
 });
 for(const s of value.scans as unknown[])if(!record(s)||!name(s.libraryId)||typeof s.libraryName!=='string'||typeof s.phase!=='string'||!count(s.discovered)||!count(s.analyzed)||!optionalWhole(s.total))invalid();
 for(const t of value.tasks.items as unknown[])if(!record(t)||!name(t.id)||!name(t.kind)||typeof t.state!=='string'||!optionalWhole(t.processed)||!optionalWhole(t.total))invalid();
 return {...(value as unknown as Dashboard),alerts};
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
  return parseDashboardStatus(await callServer(this.api,'GET /v1/admin/dashboard',{signal}));
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

/** What the Dashboard's status and report are built from: sessions, jobs, scans and history the server hints about. */
const statusMoved=['admin.sessions','job.updated','library.scan.updated','operation.updated','server.updated'].map(type=>dependsOn.event(type));
const reportMoved=['playback.history','session.updated'].map(type=>dependsOn.event(type));
/** The Dashboard's status (alerts and tasks) in the viewer's cache. */
export function dashboardStatusQuery(client:Pick<DashboardDataClient,'status'>):ViewerQueryOptions<DashboardStatus>{
 return {queryKey:queryKeys.adminDashboardStatus(),queryFn:({signal})=>client.status(signal),meta:{dependencies:statusMoved}};
}
/** The Dashboard's report for one set of filters, which carry the interval (time zone and end) it was captured for. */
export function dashboardReportQuery(client:Pick<DashboardDataClient,'report'>,filters:DashboardReportFilters):ViewerQueryOptions<DashboardReport>{
 return {queryKey:queryKeys.adminDashboardReport(filters),queryFn:({signal})=>client.report(filters,signal),meta:{dependencies:reportMoved}};
}
