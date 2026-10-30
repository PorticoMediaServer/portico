import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DashboardDataClient,parseDashboardHistory,parseDashboardPeople,DASHBOARD_LOAD_RANGES,DASHBOARD_LOAD_SERIES} from '../src/admin/dashboard.ts';
import {parseDashboardSessionFacts} from '../src/admin/activity-summary/parser.ts';
const context={timeZone:'UTC',until:'2026-10-22T12:00:00Z'};
function interval(){const start=Date.parse('2026-10-16T00:00:00Z');return {period:'7d',timeZone:'UTC',from:'2026-10-16T00:00:00Z',until:context.until,buckets:Array.from({length:7},(_,i)=>({start:new Date(start+i*86400000).toISOString(),end:new Date(Math.min(start+(i+1)*86400000,Date.parse(context.until))).toISOString(),partial:i===6}))};}
test('Dashboard analytics use account IDs, page bounds and exact kind totals',()=>{
 const row={accountId:'server-account',username:'person',profiles:['Kids'],plays:2,watchedMs:12000,kinds:[{kind:'movies',plays:2,watchedMs:12000}]};
 assert.equal(parseDashboardPeople({...interval(),items:[row],nextCursor:''}).items[0].accountId,'server-account');
 assert.throws(()=>parseDashboardPeople({...interval(),items:[{...row,watchedMs:12345}],nextCursor:''}));
 assert.throws(()=>parseDashboardPeople({...interval(),items:Array(101).fill(row),nextCursor:''}));
 assert.throws(()=>parseDashboardPeople({...interval(),items:[row,row],nextCursor:''}));
 assert.equal(parseDashboardPeople({...interval(),items:[{...row,kinds:[{kind:'audiobooks',plays:1,watchedMs:6000},{kind:'live',plays:1,watchedMs:6000}]}],nextCursor:''}).items[0].kinds.length,2);
 assert.equal(parseDashboardPeople({...interval(),items:[{...row,kinds:[{kind:'other',plays:2,watchedMs:12000}]}],nextCursor:''}).items[0].kinds[0].kind,'other');
});
test('history preserves actual empty points, partial endpoints and music count units',()=>{
 const raw={...interval(),buckets:interval().buckets.map(b=>({...b,kinds:[]})),busiestTimes:Array.from({length:7},()=>Array(24).fill(0)),
 quietestStartHour:null,top:[],topKind:'music'};
 const out=parseDashboardHistory(raw);
 assert.equal(out.buckets[0].kinds.length,0);assert.equal(out.quietestStartHour,null);
 assert.throws(()=>parseDashboardHistory({...raw,top:[{itemId:'album',title:'Album',plays:2,people:3,countUnit:'track_plays'}]}));
 assert.deepEqual(DASHBOARD_LOAD_RANGES,['live','12h','24h','7d','30d','90d','1y','all']);
 assert.deepEqual(DASHBOARD_LOAD_SERIES.bandwidth.metrics,['awayMbps','homeMbps']);
});
test('session source projection rejects nested capabilities and never fabricates hardware',()=>{
 const raw={media:{video:{id:'v0',codec:'hevc',profile:'Main 10',height:2160,hdr:'hdr10'}},currentBitrateKbps:0,rateWindowMs:5000,live:false};
 assert.equal((parseDashboardSessionFacts(raw).media as any).videoHardware,undefined);
 assert.throws(()=>parseDashboardSessionFacts({...raw,media:{...raw.media,url:'/private-capability'}}));
 assert.throws(()=>parseDashboardSessionFacts({...raw,rateWindowMs:2000}));
});
test('client requests the same server-account/media/range seam as the history table',async()=>{
 let seen='';
 const client=new DashboardDataClient({request:async(path:string)=>{seen=path;return {...interval(),items:[],nextCursor:''} as never;}});
 await client.people({...context,period:'7d',accountId:'server-account',media:'music'},'cursor',100);
 const url=new URL(seen,'https://server.invalid');
 assert.equal(url.pathname,'/v1/admin/play-history/people');
 assert.equal(url.searchParams.get('timeZone'),context.timeZone);assert.equal(url.searchParams.get('until'),context.until);
 assert.equal(url.searchParams.get('accountId'),'server-account');assert.equal(url.searchParams.get('media'),'music');assert.equal(url.searchParams.get('limit'),'100');
});
test('selected-source facts accept the checked normalized breadth DTO without retaining capabilities',()=>{
 const video={id:'v0',codec:'hevc',profile:'main 10',height:2160,chromaSubsampling:'4:2:0',frameRateNumerator:24000,frameRateDenominator:1001,frameRateMode:'constant',dolbyVisionProfile:8,dolbyVisionCompatibility:1,dolbyVisionRpu:true,dolbyVisionBaseLayer:true,dolbyVisionEnhancementType:'mel',dolbyVisionCmVersion:'4.0',hdr10Plus:false,masterDisplay:'G(13250,34500)',maxCll:'1000,400'};
 const audio={id:'a1',codec:'dts',variant:'dtsx',objectAudio:'dtsx',dtsx:true,channels:8,atmos:false,default:true,commentary:false,visualImpaired:true};
 const raw={media:{video,audio},currentBitrateKbps:0,rateWindowMs:5000,live:false};
 const out=parseDashboardSessionFacts(raw).media as {video:typeof video;audio:typeof audio};
 assert.deepEqual(out.video,video);assert.deepEqual(out.audio,audio);
 const {objectAudio,...ma}=audio;
 const hdma={...ma,variant:'dts_hd_ma',dtsx:false};
 assert.deepEqual((parseDashboardSessionFacts({...raw,media:{video,audio:hdma}}).media as any).audio,hdma);
 assert.throws(()=>parseDashboardSessionFacts({...raw,media:{video:{...video,presentationUrl:'/private-grant'},audio}}));
 assert.throws(()=>parseDashboardSessionFacts({...raw,media:{video,audio:{...audio,url:'/private-grant'}}}));
 assert.throws(()=>parseDashboardSessionFacts({...raw,media:{video,audio:{...audio,visualImpaired:'yes'}}}));
});

test('source interlace preserves known false and true while unknown stays absent',()=>{
 for(const interlaced of [undefined,false,true]) {
  const video={id:'v0',codec:'h264',height:1080,...(interlaced===undefined?{}:{interlaced})};
  const parsed=parseDashboardSessionFacts({media:{video}}).media as {video:typeof video};
  assert.deepEqual(parsed.video,video);
  assert.equal(Object.hasOwn(parsed.video,'interlaced'),interlaced!==undefined);
 }
 for(const interlaced of [null,0,1,'false','progressive']) assert.throws(()=>parseDashboardSessionFacts({media:{video:{id:'v0',codec:'h264',interlaced}}}));
 assert.throws(()=>parseDashboardSessionFacts({media:{video:{id:'v0',codec:'h264',interlaced:false,url:'/private-grant'}}}));
});

test('DTS profile and explicit object facts survive the safe projection independently',()=>{
 const audio={id:'a1',codec:'dts',profile:'DTS-HD MA',variant:'dtsx',objectAudio:'dtsx',dtsx:true,atmos:false,default:true,commentary:false,visualImpaired:false};
 assert.deepEqual((parseDashboardSessionFacts({media:{audio}}).media as {audio:typeof audio}).audio,audio);
 assert.throws(()=>parseDashboardSessionFacts({media:{audio:{...audio,profile:42}}}));
});
