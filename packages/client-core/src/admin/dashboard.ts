import type {Dashboard} from '../../../contracts/src/next/api.ts';
import {callServer} from '../query/server-api.ts';
import type {PlayHistoryApi} from '../play-history.ts';
import {dependsOn} from '../freshness-hub.ts';
import {queryKeys} from '../query/keys.ts';
import type {ViewerQueryOptions} from '../query/react.ts';

/** Owner analytics are separate from administrator People management. */
/** The rebuilt server's Dashboard read (`GET /v1/admin/dashboard`): alerts, scans, tasks and the totals of now. */
export type DashboardStatus=Dashboard;
/** Rule 12: the load as it is now and over the last 24 hours; the charts start again after a restart. */
export const DASHBOARD_LOAD_RANGES=['live','24h'] as const;
export const DASHBOARD_LOAD_SERIES={
 bandwidth:{unit:'mbps',metrics:['awayMbps','homeMbps']},
 processor:{unit:'percent',metrics:['processCPU','cpu']},
 memory:{unit:'percent',metrics:['processMemory','memory']},
 // Rule 12: conversions split into hardware and processor (a software fallback counts as processor); the tile is their total.
 conversions:{unit:'count',metrics:['hardwareConversions','processorConversions']},
} as const;
const whole=(v:number)=>Number.isSafeInteger(v)&&v>=0;
function invalid():never {throw Object.assign(new Error('Invalid Dashboard data.'),{code:'invalid_dashboard_data'});}
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
/** Actual producer reads only. No graph generation, interpolation or name keys. */
export class DashboardDataClient {
 private api:PlayHistoryApi;
 constructor(api:PlayHistoryApi){this.api=api;}
 async status(signal?:AbortSignal):Promise<DashboardStatus>{
  return parseDashboardStatus(await callServer(this.api,'GET /v1/admin/dashboard',{signal}));
 }
}

/** What the Dashboard's status is built from: the sessions, jobs, scans and server changes the server hints about. */
const statusMoved=['admin.sessions','job.updated','library.scan.updated','operation.updated','server.updated'].map(type=>dependsOn.event(type));
/** The Dashboard's status (alerts and tasks) in the viewer's cache. */
export function dashboardStatusQuery(client:Pick<DashboardDataClient,'status'>):ViewerQueryOptions<DashboardStatus>{
 return {queryKey:queryKeys.adminDashboardStatus(),queryFn:({signal})=>client.status(signal),meta:{dependencies:statusMoved}};
}
