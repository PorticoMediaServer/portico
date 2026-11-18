import {test} from 'node:test';
import assert from 'node:assert/strict';
import {captureReportContext,parseReportContext,parseReportInterval} from '../src/report-context.ts';
import {ConsoleClient} from '../src/console.ts';
import {fetchPlaySummary} from '../src/play-history.ts';

const context=captureReportContext('Asia/Kathmandu',new Date('2026-10-22T18:29:59.999Z'));
const interval={period:'today',timeZone:'Asia/Kathmandu',from:'2026-10-22T18:15:00Z',until:'2026-10-22T18:15:00.000Z',buckets:[{start:'2026-10-22T18:15:00Z',end:'2026-10-22T18:15:00Z',partial:true}]};
const summary={...interval,plays:0,people:0,watchedMs:0,converted:0,mostPlayed:[],mostActive:[]};
const metricNames=['cpu','memory','diskRead','diskWrite','netIn','netOut','gpuUsage','gpuMemory','gpuEncoder','processCPU','processMemory','homeMbps','awayMbps','hardwareConversions','processorConversions'];
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
test('the aggregate summary carries the exact context and explicit Today',async()=>{
 const seen:string[]=[];
 const api={request:async(path:string)=>{seen.push(path);return summary;}} as any;
 const filters={...context,period:'today' as const};
 await fetchPlaySummary(api,filters);
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
