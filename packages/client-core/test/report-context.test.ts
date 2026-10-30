import {test} from 'node:test';
import assert from 'node:assert/strict';
import {captureReportContext,parseReportContext,parseReportInterval} from '../src/report-context.ts';
import {DashboardDataClient,parseDashboardReport} from '../src/admin/dashboard.ts';
import {ConsoleClient} from '../src/console.ts';
import {fetchPlayHistory,fetchPlaySummary} from '../src/play-history.ts';

const context=captureReportContext('Asia/Kathmandu',new Date('2026-10-22T18:29:59.999Z'));
const interval={period:'today',timeZone:'Asia/Kathmandu',from:'2026-10-22T18:15:00Z',until:'2026-10-22T18:15:00.000Z',buckets:[{start:'2026-10-22T18:15:00Z',end:'2026-10-22T18:15:00Z',partial:true}]};
const history={...interval,buckets:interval.buckets.map(b=>({...b,kinds:[]})),busiestTimes:Array.from({length:7},()=>Array(24).fill(0)),quietestStartHour:null,top:[],topKind:'movies'};
const people={...interval,items:[],nextCursor:''};
const table={...interval,entries:[],total:0,nextCursor:''};
const summary={...interval,plays:0,people:0,watchedMs:0,converted:0,mostPlayed:[],mostActive:[]};
const metricNames=['cpu','memory','diskRead','diskWrite','netIn','netOut','gpuUsage','gpuMemory','gpuEncoder','processCPU','processMemory','homeMbps','awayMbps','hardwareConversions','softwareConversions'];
const load={...interval,window:'today',observedAt:Date.parse(context.until),series:Object.fromEntries(metricNames.map(n=>[n,[]])),status:Object.fromEntries(metricNames.map(n=>[n,{status:'unavailable',value:0}]))};
test('canonical capture floors completed quarters once per transaction and requires device IANA',()=>{
 assert.deepEqual(context,{timeZone:'Asia/Kathmandu',until:'2026-10-22T18:15:00.000Z'});
 assert.ok(Object.isFrozen(context));assert.equal(captureReportContext('Pacific/Chatham',new Date('2026-10-22T18:30:00Z')).until,'2026-10-22T18:30:00.000Z');
 for(const timeZone of ['','Local','Mars/Olympus','../etc/passwd'])assert.throws(()=>captureReportContext(timeZone));
 assert.throws(()=>parseReportContext({...context,until:'2026-10-22T18:16:00Z'}));
 assert.throws(()=>parseReportContext({...context,until:'2026-10-22T18:15:00.000000001Z'}));
 assert.equal(parseReportInterval(interval).buckets[0].start,interval.buckets[0].start);
 assert.throws(()=>parseReportInterval({...interval,period:'24h'}));
 assert.throws(()=>parseReportInterval({...interval,buckets:[{...interval.buckets[0],partial:false}]}));
});
test('all aggregate wrappers/pages carry the exact same context and explicit Today',async()=>{
 const seen:string[]=[];
 const api={request:async(path:string)=>{seen.push(path);if(path.startsWith('/v1/admin/dashboard/report'))return {context,history,people,table,summary,topPlayed:{...interval,items:[],topKind:'movies'},load};if(path.startsWith('/v1/admin/play-history/summary'))return summary;if(path.startsWith('/v1/admin/play-history/people'))return people;if(path.startsWith('/v1/admin/play-history/dashboard'))return history;return table;}} as any;
 const filters={...context,period:'today' as const};const client=new DashboardDataClient(api);
 await client.people(filters,'opaque',100);await client.history(filters);await fetchPlayHistory(api,filters,'page2');await fetchPlaySummary(api,filters);
 const report=await client.report({...filters,window:'today',peoplePeriod:'today',topPeriod:'today',topKind:'movies'});
 assert.equal(report.table.total,report.summary.plays);
 for(const path of seen){const q=new URL(path,'https://server.invalid').searchParams;assert.equal(q.get('timeZone'),context.timeZone);assert.equal(q.get('until'),context.until);assert.equal(q.get('period'),'today');}
});

test('Console telemetry keeps context and existing scope disposal',async()=>{
 let path='';const c=new ConsoleClient({request:async(p:string)=>{path=p;return {scope:{serverId:'server',viewerFence:'a'.repeat(64)},data:load};}} as any,{serverId:'server',viewerId:'owner:main'});
 await c.telemetry('today',context);const q=new URL(path,'https://server.invalid').searchParams;
 assert.equal(q.get('timeZone'),context.timeZone);assert.equal(q.get('until'),context.until);assert.equal(q.get('window'),'today');
 c.dispose();await assert.rejects(()=>c.telemetry('today',context));
});
test('repeated DST hour identities survive canonical interval parsing',()=>{
 const from=Date.parse('2026-10-31T08:00:00Z'),until=Date.parse('2026-11-01T07:00:00Z');
 const repeated={period:'24h',timeZone:'America/New_York',from:new Date(from).toISOString(),until:new Date(until).toISOString(),buckets:Array.from({length:24},(_,i)=>({start:new Date(from+i*3600000).toISOString(),end:new Date(Math.min(from+(i+1)*3600000,until)).toISOString(),partial:i===23}))};
 const out=parseReportInterval(repeated);assert.equal(out.buckets.length,24);
 assert.ok(out.buckets.some(b=>b.start==='2026-11-01T05:00:00.000Z'));assert.ok(out.buckets.some(b=>b.start==='2026-11-01T06:00:00.000Z'));
});

test('common report rejects a filter-specific start for the same period',()=>{
 const report={context,history,people,table,summary,topPlayed:{...interval,items:[],topKind:'movies'},load};
 parseDashboardReport(report);
 const from='2026-10-21T18:15:00Z';
 const filteredPeople={...people,from,buckets:[{start:from,end:interval.until,partial:true}]};
 assert.throws(()=>parseDashboardReport({...report,people:filteredPeople}));
});
