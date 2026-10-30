import test from 'node:test';
import assert from 'node:assert/strict';
import {GroupSessionService} from '../src/group-session.ts';
import {SOCIAL_PROTOCOL} from '../src/social-playback.ts';

const now=Date.parse('2026-09-16T20:00:00Z');
const snapshot={protocolVersion:SOCIAL_PROTOCOL,serverTime:'2026-09-16T20:00:00Z',group:{
 id:'room',name:'Night',state:'playing',hostAuthority:'host-only',hostMemberId:'host',revision:'1',playbackRevision:'1',settingsRevision:'1',governanceRevision:'1',queueRevision:'1',reconnectGeneration:'1',lastCommand:'play',lastCommandId:'play',endedReason:'',eventOrdinal:'1',createdAt:'2026-09-16T20:00:00Z',updatedAt:'2026-09-16T20:00:00Z',
 permissions:{isHost:true,canControl:true,canManageQueue:true},authority:{deviceId:'device',playbackId:'playback',state:'bound'},host:{presence:'connected',lastSeenAt:'2026-09-16T20:00:00Z',pauseAt:null,endAt:null},
 timeline:{itemId:'item',currentEntryId:'entry',state:'playing',anchorPositionUs:'0',anchorAt:'2026-09-16T20:00:00Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,waitUntil:null},settings:{shuffleEnabled:false,repeatMode:'none'},sync:{noCorrectionUnderMs:750,rateCorrectionMinimum:'0.90',rateCorrectionMaximum:'1.10',rateCorrectionMaxMs:4000,seekAtOrOverMs:3000},
 readiness:{aggregate:'ready',ready:1,buffering:0,lagging:0,stale:0,memberCount:1},members:[{id:'host',displayName:'Host',private:false,role:'host',state:'joined',readiness:'ready',positionUs:'0',reportedAt:'2026-09-16T20:00:00Z',presence:'connected',joinedAt:'2026-09-16T20:00:00Z'}],queue:null,viewerMemberId:'host'}};
const api={baseUrl:'https://example.invalid',request:async<T,>()=>snapshot as T};
const settle=async()=>{for(let i=0;i<20;i++)await Promise.resolve();};

test('CC-12: refused group streams honor both Retry-After forms as a floor',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});
 for(const after of ['5',new Date(now+5000).toUTCString()]){
  let calls=0;
  const service=new GroupSessionService({api,now:()=>now,heartbeatMs:3600000,stream:async()=>{calls++;return new Response(null,{status:429,headers:{'Retry-After':after}});}});
  try{
   assert.equal(await service.open('room'),true);await settle();assert.equal(calls,1);
   t.mock.timers.tick(4999);await settle();assert.equal(calls,1);
   t.mock.timers.tick(1);await settle();assert.equal(calls,2);
  }finally{service.dispose();}
 }
});

test('L-03: the group watchdog covers a stream opener that never supplies headers',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});
 let signal:AbortSignal|undefined;
 const service=new GroupSessionService({api,now:()=>now,heartbeatMs:3600000,stream:async(_path,_last,opening)=>{signal=opening;return new Promise((_resolve,reject)=>{opening.addEventListener('abort',()=>reject(Object.assign(new Error('aborted'),{name:'AbortError'})),{once:true});});}});
 try{
  assert.equal(await service.open('room'),true);await settle();assert.ok(signal);
  t.mock.timers.tick(39999);await settle();assert.equal(signal.aborted,false);
  t.mock.timers.tick(1);await settle();assert.equal(signal.aborted,true);assert.equal(service.getSnapshot().phase,'reconnecting');
 }finally{service.dispose();}
});

test('L-04: a malformed group stream cancels its reader and aborts its connection turn',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval']});
 let cancelled=0,signal:AbortSignal|undefined;
 const service=new GroupSessionService({api,now:()=>now,heartbeatMs:3600000,stream:async(_path,_last,opening)=>{signal=opening;return new Response(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode('data: '+'x'.repeat(300000)));},cancel(){cancelled++;}}));}});
 try{
  assert.equal(await service.open('room'),true);await settle();assert.equal(cancelled,1);assert.equal(signal?.aborted,true);assert.equal(service.getSnapshot().phase,'reconnecting');
 }finally{service.dispose();}
});
