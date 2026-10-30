import {test} from 'node:test';
import assert from 'node:assert/strict';
import {GroupSessionService,GroupFollower,type GroupPlayer} from '../src/group-session.ts';
import {SOCIAL_PROTOCOL} from '../src/social-playback.ts';

const T0=Date.parse('2026-09-16T20:00:00Z');
const sync={noCorrectionUnderMs:750,rateCorrectionMinimum:'0.90',rateCorrectionMaximum:'1.10',rateCorrectionMaxMs:4000,seekAtOrOverMs:3000};
const timeline={itemId:'item',currentEntryId:'ent_1',state:'playing',anchorPositionUs:'10000000',anchorAt:'2026-09-16T20:00:00Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,waitUntil:null};
const host={id:'mem_1',displayName:'Host',private:false,role:'host',state:'joined',readiness:'ready',positionUs:'0',reportedAt:'2026-09-16T20:00:00Z',presence:'connected',joinedAt:'2026-09-16T19:59:00Z'};
const guest={...host,id:'mem_2',displayName:'Guest',role:'member'};
const group=(over:Record<string,unknown>={})=>({
 id:'grp_1',name:'Movie night',state:'playing',hostAuthority:'host-only',hostMemberId:'mem_1',revision:'7',playbackRevision:'3',settingsRevision:'1',governanceRevision:'1',queueRevision:'2',reconnectGeneration:'1',
 lastCommand:'play',lastCommandId:'p',endedReason:'',eventOrdinal:'9',createdAt:'2026-09-16T19:59:00Z',updatedAt:'2026-09-16T20:00:00Z',
 permissions:{isHost:true,canControl:true,canManageQueue:true},authority:{deviceId:'dev_1',playbackId:'pb1',state:'bound'},
 host:{presence:'connected',lastSeenAt:'2026-09-16T20:00:00Z',pauseAt:null,endAt:null},timeline,settings:{shuffleEnabled:false,repeatMode:'none'},sync,
 readiness:{aggregate:'ready',ready:2,buffering:0,lagging:0,stale:0,memberCount:2},members:[host,guest],queue:null,viewerMemberId:'mem_1',...over});
const snap=(over:Record<string,unknown>={},serverTime='2026-09-16T20:00:00Z')=>({protocolVersion:SOCIAL_PROTOCOL,serverTime,group:group(over)});
const frame=(kind:string,data:unknown,id?:string)=>(id?`id: ${id}\n`:'')+`event: ${kind}\ndata: ${JSON.stringify(data)}\n\n`;
const settle=()=>new Promise(r=>setTimeout(r,15));

/** A server whose event stream the test writes to by hand. */
function server(){
 const calls:{path:string;method:string;body?:any}[]=[];const streams:{lastEventId:string;write(text:string):void;close():void}[]=[];
 let current=snap();let refuse:Error|undefined;
 const api={baseUrl:'https://s',request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{
  calls.push({path,method,body});
  if(refuse&&method==='POST'&&path.endsWith('/transport')){const e=refuse;refuse=undefined;throw e;}
  if(path.endsWith('/transport'))return {protocolVersion:SOCIAL_PROTOCOL,groupId:'grp_1',idempotencyKey:(body as any).idempotencyKey,disposition:'accepted',groupState:'paused',command:(body as any).command,revision:'8',playbackRevision:'4',queueRevision:'2',serverTime:'2026-09-16T20:00:01Z',recordedAt:'2026-09-16T20:00:01Z',timeline:{...timeline,state:'paused'},settings:{shuffleEnabled:false,repeatMode:'none'},override:null} as T;
  if(path.endsWith('/queue'))return {protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',groupId:'grp_1',queue:{revision:'3',position:0,entries:[{entryId:'ent_1',position:0,itemId:'item',unavailable:false,addedBy:'mem_1'}],eligibility:null}} as T;
  if(path.endsWith('/invites'))return {protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',invite:{id:'inv_1',groupId:'grp_1',code:'ABCDEFGH',expiresAt:'2026-09-16T20:15:00Z',maxUses:8,uses:0,recipientProfileId:''}} as T;
  if(path==='/v1/groups'&&method==='GET')return {protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',groups:[current.group]} as T;
  return current as T;
 }};
 const stream=async(_path:string,lastEventId:string,signal:AbortSignal)=>{
  let controller!:ReadableStreamDefaultController<Uint8Array>;const encoder=new TextEncoder();
  const body=new ReadableStream<Uint8Array>({start(c){controller=c;}});
  signal.addEventListener('abort',()=>{try{controller.close();}catch{}},{once:true});
  streams.push({lastEventId,write:t=>controller.enqueue(encoder.encode(t)),close:()=>{try{controller.close();}catch{}}});
  return new Response(body,{status:200});
 };
 return {api,stream,calls,streams,set:(next:ReturnType<typeof snap>)=>{current=next;},refuseTransport:(e:Error)=>{refuse=e;}};
}
const make=(s:ReturnType<typeof server>,now=()=>T0)=>{let n=0;return new GroupSessionService({api:s.api,stream:s.stream,key:()=>'key-'+(++n),now,heartbeatMs:3600000});};

test('joining tidies the code, enters the room and follows the stream', async()=>{
 const s=server(),service=make(s);
 assert.equal(await service.join(' abcd-efgh ','Sam'),true);
 assert.equal(s.calls[0].body.code,'ABCDEFGH');
 assert.equal(service.getSnapshot().phase,'live');
 await settle();assert.equal(s.streams.length,1);
 s.set(snap({state:'paused',revision:'8',timeline:{...timeline,state:'paused',anchorPositionUs:'42000000',anchorAt:'2026-09-16T20:00:30Z'}}));
 s.streams[0].write(frame('group.transport',{command:'pause',state:'paused',itemId:'item',currentEntryId:'ent_1',anchorPositionUs:'42000000',anchorAt:'2026-09-16T20:00:30Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,revision:'8',playbackRevision:'4'},'10'));
 await settle();
 const g=service.getSnapshot().group!;
 assert.equal(g.timeline.state,'paused');assert.equal(g.timeline.anchorPositionUs,'42000000');
 service.dispose();
});

test('a dropped stream keeps the room and resumes from the last ordinal', async()=>{
 const s=server(),service=make(s);
 await service.open('grp_1');await settle();
 s.streams[0].write(frame('group.members',{revision:'8'},'11'));await settle();
 s.streams[0].close();await settle();
 assert.equal(service.getSnapshot().phase,'reconnecting');
 assert.ok(service.getSnapshot().group,'the room is still on screen');
 await new Promise(r=>setTimeout(r,900));
 assert.equal(s.streams.length,2);assert.equal(s.streams[1].lastEventId,'11');
 // Heartbeats carry no id and must not move the resume point. A pushed heartbeat has no round trip,
 // so it does not override the clock a request measured (BE-MEDIA-14).
 s.streams[1].write(frame('heartbeat',{serverTime:'2026-09-16T20:00:05Z'}));await settle();
 assert.equal(service.getSnapshot().phase,'live');assert.equal(service.getSnapshot().clockOffsetMs,0);
 service.dispose();
});

test('the last frame ends the room without discarding it', async()=>{
 const s=server(),service=make(s);
 await service.open('grp_1');await settle();
 s.set(snap({state:'ended',endedReason:'host-ended',timeline:{...timeline,state:'stopped'}}));
 s.streams[0].write(frame('group.ended',{reason:'host-ended'},'12'));await settle();
 assert.equal(service.getSnapshot().phase,'ended');assert.equal(service.getSnapshot().group?.endedReason,'host-ended');
 service.dispose();
});

test('a command carries the revision it saw; a conflict re-reads and is not resent', async()=>{
 const s=server(),service=make(s);
 await service.open('grp_1');
 s.set(snap({state:'paused',revision:'8',timeline:{...timeline,state:'paused'}}));
 assert.equal(await service.transport('pause'),true);
 const sent=s.calls.find(c=>c.path.endsWith('/transport'))!;
 assert.equal(sent.body.expectedRevision,'3');assert.equal(sent.body.idempotencyKey,'key-1');
 assert.equal(service.getSnapshot().group!.timeline.state,'paused');await settle();
 s.refuseTransport(Object.assign(new Error('The group changed.'),{code:'revision_conflict',status:409}));
 assert.equal(await service.transport('play'),false);
 assert.equal(s.calls.filter(c=>c.path.endsWith('/transport')).length,2,'never resent blindly');
 assert.match(service.getSnapshot().error!,/changed/);
 service.dispose();
});

test('creating posts the group itself and sends no lane: the server binds this device', async()=>{
 const s=server(),service=make(s);
 assert.equal(await service.create({name:' Night ',displayName:'Sam',hostAuthority:'anyone'}),true);
 const created=s.calls.find(c=>c.path==='/v1/groups'&&c.method==='POST')!;
 assert.deepEqual(created.body,{protocolVersion:SOCIAL_PROTOCOL,name:'Night',displayName:'Sam',hostAuthority:'anyone'});
 assert.equal(service.getSnapshot().phase,'live');
 service.dispose();
});

test('being made host heartbeats at once to bind this device; handing over names no lane', async()=>{
 const s=server();
 s.set(snap({permissions:{isHost:false,canControl:false,canManageQueue:false},viewerMemberId:'mem_2'}));
 const service=make(s);
 await service.open('grp_1');await settle();
 s.set(snap({hostMemberId:'mem_2',viewerMemberId:'mem_2',revision:'8',members:[{...host,role:'member'},{...guest,role:'host'}]}));
 s.streams[0].write(frame('group.host',{revision:'8'},'13'));await settle();
 const beats=s.calls.filter(c=>c.path==='/v1/groups/grp_1/heartbeat'&&c.method==='POST');
 assert.equal(beats.length,1);assert.equal(beats[0].body,undefined);
 await service.makeHost('mem_1');
 const handed=s.calls.filter(c=>c.path.endsWith('/host-transfer')).at(-1)!;
 assert.deepEqual(Object.keys(handed.body).sort(),['expectedRevision','memberId','protocolVersion']);
 service.dispose();
});

test('invites and the queue are read through their envelopes', async()=>{
 const s=server(),service=make(s);
 await service.open('grp_1');
 assert.equal(await service.invite(),true);assert.equal(service.getSnapshot().invite?.code,'ABCDEFGH');
 assert.equal(await service.queueAdd(['item']),true);await settle();
 assert.equal(service.getSnapshot().queue?.entries[0].itemId,'item');
 assert.equal(s.calls.find(c=>c.method==='POST'&&c.path.endsWith('/queue'))!.body.expectedRevision,'2');
 service.dispose();
});

function player(start:{itemId:string;positionSeconds:number;playing?:boolean}){
 const log:string[]=[];const state={itemId:start.itemId,positionSeconds:start.positionSeconds,playing:start.playing??true,buffering:false,ready:true};
 const p:GroupPlayer={state:()=>({...state}),load:(id,at,playing)=>{log.push(`load ${id} ${at} ${playing}`);},play:()=>{log.push('play');state.playing=true;},pause:()=>{log.push('pause');state.playing=false;},seek:at=>{log.push('seek '+at);state.positionSeconds=at;},setRate:r=>{log.push('rate '+r);}};
 return {p,log,state};
}

test('the follower loads, nudges, then seeks, in the bands the server publishes', async()=>{
 const s=server();const now=T0+20000;s.set(snap({},'2026-09-16T20:00:20Z'));const service=make(s,()=>now);
 await service.open('grp_1');
 // The group is 20 s past its anchor of 10 s, so the target is 30 s.
 const other=player({itemId:'other',positionSeconds:0});const a=new GroupFollower(service,other.p,()=>now);a.start();a.stop();
 assert.equal(other.log[0],'load item 30 true');
 const near=player({itemId:'item',positionSeconds:29.8});const b=new GroupFollower(service,near.p,()=>now);b.start();b.stop();
 assert.deepEqual(near.log,['rate 1'],'inside the quiet band nothing is touched; stop restores speed');
 const behind=player({itemId:'item',positionSeconds:28.5});const c=new GroupFollower(service,behind.p,()=>now);c.start();c.stop();
 assert.equal(behind.log[0],'rate 1.1');
 const lost=player({itemId:'item',positionSeconds:5});const d=new GroupFollower(service,lost.p,()=>now);d.start();d.stop();
 assert.ok(lost.log.includes('seek 30'));
 const reports=s.calls.filter(c=>c.path.endsWith('/readiness')).map(c=>c.body.readiness);
 assert.deepEqual(reports,['buffering','ready','ready','lagging']);
 service.dispose();
});

/** A player whose seeks take time to land where they were asked to (clamped to the title's length).
 *  `says` is what it tells the follower meanwhile:
 *  - 'seeking': a seek is pending, and the position it is seeking to (the web player);
 *  - 'target': only the position it is seeking to, with no word that a seek is pending;
 *  - 'position': nothing about the seek at all; it goes on reporting where it was, still moving.
 *  `latency` is one delay for every seek, or one per seek in turn. `fails` makes a seek give up
 *  after that long and leave the player where it was. Reaching the end of the title ends it. */
function slowPlayer(clock:()=>number,o:{latency:number|number[];says:'seeking'|'target'|'position';start?:number;duration?:number;fails?:number;playing?:boolean}){
 const log:number[]=[];let base=o.start??5,since=clock(),pending:{until:number;target:number;fails:boolean}|undefined,buffering=false,playing=o.playing??true;
 const moving=()=>playing&&!buffering;
 const where=()=>Math.min(o.duration??Infinity,moving()?base+(clock()-since)/1000:base);
 const settle=()=>{if(pending&&clock()>=pending.until){if(!pending.fails){base=pending.target;since=pending.until;}pending=undefined;}};
 const position=()=>{settle();return pending&&o.says!=='position'?pending.target:where();};
 const p:GroupPlayer={
  state:()=>({itemId:'item',positionSeconds:position(),playing,buffering,ready:true,...(o.duration?{durationSeconds:o.duration}:{}),...(o.says==='seeking'?{seeking:(settle(),!!pending)}:{})}),
  load:()=>{},play:()=>{base=where();since=clock();playing=true;},pause:()=>{base=where();since=clock();playing=false;},setRate:()=>{},
  seek:at=>{settle();const n=log.length;log.push(at);const latency=Array.isArray(o.latency)?o.latency[Math.min(n,o.latency.length-1)]!:o.latency;
   // The player keeps playing from where it was until the seek lands (or fails).
   pending={until:clock()+(o.fails??latency),target:Math.min(o.duration??Infinity,at),fails:o.fails!==undefined};if(latency===0&&o.fails===undefined)settle();},
 };
 return {p,log,position:where,landed:()=>(settle(),!pending),ended:()=>(settle(),!pending&&o.duration!==undefined&&where()>=o.duration),stall:(on:boolean)=>{base=where();since=clock();buffering=on;}};
}
/** A room whose clock the test moves: the group is at ten seconds at T0 and playing. */
async function room(t:import('node:test').TestContext,startSeconds=20){
 const s=server();const clock={now:T0+startSeconds*1000};
 s.set(snap({},new Date(clock.now).toISOString().replace('.000Z','Z')));const service=make(s,()=>clock.now);
 t.after(()=>service.dispose());
 await service.open('grp_1');
 return {s,service,clock};
}
/** One follower tick a second for `seconds`. Counts requests made while the last one was still pending. */
function run(f:GroupFollower,player:ReturnType<typeof slowPlayer>,clock:{now:number},seconds:number,each?:()=>void){
 let restarted=0;const drift:number[]=[];
 for(let second=0;second<seconds;second++){
  const landed=player.landed(),before=player.log.length;
  f.start();f.stop();
  if(!landed&&player.log.length>before)restarted++;
  drift.push(Math.abs(f.target()!-player.position()));
  each?.();
  clock.now+=1000;
 }
 return {restarted,drift};
}

test('a follower whose seeks take eight seconds reaches the group and stays with it', async t=>{
 const {s,service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency:8000,says:'seeking',duration:7200}),f=new GroupFollower(service,slow.p,()=>clock.now);
 const {restarted,drift}=run(f,slow,clock,60);
 assert.equal(restarted,0,'a seek that has not landed is never restarted');
 // The first seek lands eight seconds behind a group that kept moving. The second aims that far
 // ahead and lands with it; nothing follows.
 assert.deepEqual(slow.log,[30,46]);
 assert.ok(drift.slice(25).every(d=>d<0.75),'in step from then on: '+drift.slice(25).map(d=>d.toFixed(1)).join(' '));
 const reports=s.calls.filter(c=>c.path.endsWith('/readiness')).map(c=>c.body.readiness);
 assert.equal(reports[0],'lagging');assert.ok(reports.includes('buffering'),'a member whose seek is pending says so');assert.equal(reports.at(-1),'ready');
});

test('a seek as slow as the follower will wait for is still caught up with', async t=>{
 const {service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency:17000,says:'seeking',duration:7200}),f=new GroupFollower(service,slow.p,()=>clock.now);
 const {restarted,drift}=run(f,slow,clock,90);
 assert.equal(restarted,0);
 assert.deepEqual(slow.log,[30,64],'the second seek is aimed the whole seventeen seconds ahead');
 assert.ok(drift.slice(40).every(d=>d<0.75),drift.slice(40).map(d=>d.toFixed(1)).join(' '));
});

test('near the end of a title a seek is never aimed ahead, so this member does not finish before the group', async t=>{
 // A hundred-second title with the group at ninety. The first seek takes eight seconds; later ones are quick.
 const {service,clock}=await room(t,80);
 const slow=slowPlayer(()=>clock.now,{latency:[8000,0],says:'seeking',duration:100}),f=new GroupFollower(service,slow.p,()=>clock.now);
 let early=0;
 run(f,slow,clock,9,()=>{if(slow.ended()&&f.target()!<99)early++;});
 assert.equal(early,0,'the title ended here while the group was still playing it');
 assert.deepEqual(slow.log,[90,98],'the second seek goes to where the group is, not eight seconds past the end');
 assert.ok(slow.log.every(at=>at<100));
 assert.ok(Math.abs(f.target()!-slow.position())<0.75,'and they reach the end together');
});

test('a seek is aimed ahead only where it leaves room, and only when the length of the title is known', async t=>{
 // Eight-second seeks, as above, but the group is half a minute from the end of a two-minute title.
 let {service,clock}=await room(t,70);
 const long=slowPlayer(()=>clock.now,{latency:8000,says:'seeking',duration:120}),f=new GroupFollower(service,long.p,()=>clock.now);
 run(f,long,clock,20);
 assert.deepEqual(long.log,[80,96],'room to spare: aimed ahead');
 // The same player without a length: where the end is cannot be known, so nothing is aimed ahead.
 ({service,clock}=await room(t,70));
 const unknown=slowPlayer(()=>clock.now,{latency:8000,says:'seeking'}),g=new GroupFollower(service,unknown.p,()=>clock.now);
 run(g,unknown,clock,12);
 assert.deepEqual(unknown.log,[80,88]);
});

for(const says of ['target','position'] as const)test('a player that does not say when it is seeking is never asked again while a seek could still be pending ('+(says==='target'?'it reports where it is seeking to':'it goes on reporting where it was')+')', async t=>{
 const {service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency:8000,says,duration:7200}),f=new GroupFollower(service,slow.p,()=>clock.now);
 const {restarted}=run(f,slow,clock,61);
 assert.equal(restarted,0,'its position moving is not taken for the seek having landed');
 // Without the player's word the follower waits out the whole seek before judging again, and
 // does not guess how slow the player is: one seek every twenty seconds, each to where the group is.
 assert.deepEqual(slow.log,[30,50,70,90]);
});

test('a paused group does not restart a pending seek either', async t=>{
 const s=server();const clock={now:T0+60000};
 s.set(snap({state:'paused',timeline:{...timeline,state:'paused'}}));const service=make(s,()=>clock.now);
 t.after(()=>service.dispose());
 await service.open('grp_1');
 for(const says of ['seeking','position'] as const){
  const slow=slowPlayer(()=>clock.now,{latency:8000,says,start:40,playing:false}),f=new GroupFollower(service,slow.p,()=>clock.now);
  const {restarted}=run(f,slow,clock,12);
  assert.equal(restarted,0,says);assert.deepEqual(slow.log,[10],says+': one seek to the anchor');
 }
});

test('a seek that fails teaches the follower nothing about how slow this player is', async t=>{
 const {service,clock}=await room(t);
 // The player gives up after fifteen seconds and is still where it was.
 const failing=slowPlayer(()=>clock.now,{latency:0,says:'seeking',fails:15000,duration:7200}),f=new GroupFollower(service,failing.p,()=>clock.now);
 const {restarted}=run(f,failing,clock,31);
 assert.equal(restarted,0);
 assert.deepEqual(failing.log,[30,45,60],'each new seek goes to where the group is, with nothing added for the failures');
});

test('a seek the host makes while this member is still seeking is followed at once, and is not mistaken for this player being slow', async t=>{
 const {s,service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency:8000,says:'seeking',duration:7200}),f=new GroupFollower(service,slow.p,()=>clock.now);
 for(let second=0;second<40;second++){
  // Four seconds into the first seek the host jumps the group to ten minutes in.
  if(second===4){s.set(snap({revision:'8',timeline:{...timeline,anchorPositionUs:'600000000',anchorAt:'2026-09-16T20:00:24Z'}},'2026-09-16T20:00:24Z'));s.streams[0].write(frame('group.transport',{command:'seek',state:'playing',itemId:'item',currentEntryId:'ent_1',anchorPositionUs:'600000000',anchorAt:'2026-09-16T20:00:24Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,revision:'8',playbackRevision:'4'},'10'));await settle();}
  f.start();f.stop();clock.now+=1000;
 }
 // The pending seek was for somewhere the group no longer is, so the jump is followed straight
 // away and at face value (600, not 600 plus a made-up lead). That seek's own eight seconds are
 // then learned, and the third lands with the group.
 assert.deepEqual(slow.log,[30,600,616]);
 assert.ok(Math.abs(f.target()!-slow.position())<0.75);
});

test('a buffering member is not corrected until it recovers', async t=>{
 const {s,service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency:0,says:'seeking',start:29.9}),f=new GroupFollower(service,slow.p,()=>clock.now);
 slow.stall(true);
 run(f,slow,clock,10);
 assert.deepEqual(slow.log,[],'ten seconds behind and buffering: no seek, no speed change');
 assert.deepEqual([...new Set(s.calls.filter(c=>c.path.endsWith('/readiness')).map(c=>c.body.readiness))],['buffering']);
 slow.stall(false);f.start();f.stop();
 assert.equal(slow.log.length,1,'corrected once it can play');
});

test('a paused group holds the follower at the anchor', async()=>{
 const s=server();s.set(snap({state:'paused',timeline:{...timeline,state:'paused'}}));
 const service=make(s,()=>T0+60000);await service.open('grp_1');
 const local=player({itemId:'item',positionSeconds:40});const f=new GroupFollower(service,local.p,()=>T0+60000);f.start();f.stop();
 assert.ok(local.log.includes('pause'));assert.ok(local.log.includes('seek 10'),'no extrapolation while paused');
 service.dispose();
});

test('service messages come from the catalogue with their IDs, never from error.message (X-04)',async()=>{
 const {createI18n,resolveRegion}=await import('../../i18n/src/index.ts');
 const failing={request:async()=>{throw new Error('ECONNRESET socket hang up');}};
 const s=new GroupSessionService({api:failing as never,stream:(()=>{throw new Error('no stream');}) as never,heartbeatMs:3600000});
 assert.equal(await s.join('ABCD','Me'),false);
 const snap=s.getSnapshot();
 assert.equal(snap.errorId,'together.error.join');
 assert.equal(snap.error,'That code didn’t work.');
 assert.ok(!/socket/.test(snap.error!));
 const canada=new GroupSessionService({api:failing as never,stream:(()=>{throw new Error('no');}) as never,heartbeatMs:3600000,i18n:createI18n(resolveRegion({locale:'en-CA'},{locales:['en-CA']}))});
 assert.equal(await canada.create({name:'x',displayName:'y',hostAuthority:'device'} as never),false);
 assert.equal(canada.getSnapshot().errorId,'together.error.create');
});

test('BE-MEDIA-14: with a 300 ms round trip and slow outliers, two members keep the group clock within the 750 ms band', async()=>{
 // True time runs on `real`; the server's clock is real + 5 s. Each member's own clock is off by its
 // own amount, and its requests take up/down legs that the test chooses per call.
 let real=T0;const serverOffset=5000;
 const member=(localSkew:number,legs:[number,number][])=>{
  let call=0;const calls:string[]=[];
  const api={baseUrl:'https://s',request:async<T,>(p:string):Promise<T>=>{
   calls.push(p);const [up,down]=legs[Math.min(call++,legs.length-1)]!;
   real+=up;const serverTime=new Date(real+serverOffset).toISOString();real+=down;
   return snap({},serverTime) as T;
  }};
  const stream=async()=>new Response(new ReadableStream<Uint8Array>({start(){}}),{status:200});
  const service=new GroupSessionService({api,stream,key:()=>'k',now:()=>real+localSkew,heartbeatMs:3600000});
  return {service,calls,beat:()=>(service as any).beat((service as any).room)};
 };
 // Member A: 300 ms round trip, mostly on the way up; then two congested answers (2 s on the way back).
 const a=member(-1000,[[250,50],[100,2000],[100,2500]]);
 // Member B: a quick network.
 const b=member(2000,[[10,10]]);
 await a.service.open('grp_1');await b.service.open('grp_1');
 a.beat();await settle();a.beat();await settle();b.beat();await settle();
 const truth=real+serverOffset;
 const errA=Math.abs(a.service.serverNow()-truth),errB=Math.abs(b.service.serverNow()-truth);
 assert.ok(errA<=150,`member A within half its best round trip (${errA} ms)`);
 assert.ok(errB<=10,`member B within half its round trip (${errB} ms)`);
 assert.ok(Math.abs(a.service.serverNow()-b.service.serverNow())<750,'the two members stay within the 750 ms band');
 // The old rule (serverTime − receive time of the latest answer) would be 2.5 s off for A.
 a.service.dispose();b.service.dispose();
});

test('slow heartbeats stay single flight and leaving cancels the outstanding update', async () => {
 const s=server();let heartbeats=0;let heartbeatSignal:AbortSignal|undefined;
 const api={...s.api,request:async<T,>(path:string,method='GET',body?:unknown,signal?:AbortSignal):Promise<T>=>{
  if(path.endsWith('/heartbeat')){heartbeats++;heartbeatSignal=signal;return new Promise<T>((_resolve,reject)=>signal!.addEventListener('abort',()=>reject(new Error('cancelled')),{once:true}));}
  return s.api.request<T>(path,method,body);
 }};
 const service=new GroupSessionService({api,stream:s.stream,heartbeatMs:5});
 try{
  await service.open('grp_1');await new Promise(r=>setTimeout(r,40));
  assert.equal(heartbeats,1,'several timer ticks share one outstanding request');
  assert.equal(heartbeatSignal?.aborted,false);
  await service.leave();await settle();
  assert.equal(heartbeatSignal?.aborted,true,'the request stops with the room');
  const before=heartbeats;await new Promise(r=>setTimeout(r,25));assert.equal(heartbeats,before);
 }finally{service.dispose();}
});

test('heartbeat retry honours server overload delay while keeping the room visible', async()=>{
 const s=server();let now=0,heartbeats=0;
 const api={...s.api,request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{
  if(path.endsWith('/heartbeat')){heartbeats++;if(heartbeats===1)throw Object.assign(new Error('busy'),{status:503,retryAfterSeconds:60});}
  return s.api.request<T>(path,method,body);
 }};
 const service=new GroupSessionService({api,stream:s.stream,now:()=>now,heartbeatMs:5});
 try{await service.open('grp_1');await new Promise(r=>setTimeout(r,25));assert.equal(heartbeats,1);assert.equal(service.getSnapshot().group?.id,'grp_1');now=59999;await settle();assert.equal(heartbeats,1);now=60000;await settle();assert.ok(heartbeats>1,'heartbeats resume after the server delay');}finally{service.dispose();}
});

test('a hidden current title pauses the old title and reports buffering for the current playback revision',async()=>{
 const s=server();s.set(snap({timeline:{...timeline,itemId:''}}));const service=make(s);
 await service.open('grp_1');
 const local=player({itemId:'previous',positionSeconds:25});const follower=new GroupFollower(service,local.p,()=>T0);
 follower.start();follower.stop();
 assert(local.log.includes('pause'));assert(!local.log.some(x=>x.startsWith('load')));
 const report=s.calls.find(x=>x.path.endsWith('/readiness'))!.body;
 assert.equal(report.readiness,'buffering');assert.equal(report.playbackRevision,'3');
 service.dispose();
});

test('a waiting group pauses and readies at the frozen seek target without starting playback',async()=>{
 const s=server();s.set(snap({state:'waiting',timeline:{...timeline,state:'paused',anchorPositionUs:'30000000',waitUntil:'2026-09-16T20:00:05Z'}}));const service=make(s);
 await service.open('grp_1');
 const local=player({itemId:'item',positionSeconds:30});const follower=new GroupFollower(service,local.p,()=>T0);
 follower.start();follower.stop();
 assert(local.log.includes('pause'));assert(!local.log.includes('play'));
 assert.equal(s.calls.find(x=>x.path.endsWith('/readiness'))?.body.readiness,'ready');
 assert.equal(follower.target(),30);service.dispose();
});

test('an older reread cannot overwrite a newer transport event',async()=>{
 const s=server(),service=make(s);await service.open('grp_1');await settle();
 s.streams[0].write(frame('group.transport',{command:'seek',groupState:'waiting',state:'paused',itemId:'item',currentEntryId:'ent_1',anchorPositionUs:'60000000',anchorAt:'2026-09-16T20:00:01Z',rate:{numerator:'1',denominator:'1'},queuePosition:0,revision:'9',playbackRevision:'4',waitUntil:'2026-09-16T20:00:06Z'},'10'));
 await settle();
 assert.equal(service.getSnapshot().group?.timeline.anchorPositionUs,'60000000');
 assert.equal(service.getSnapshot().group?.state,'waiting');assert.equal(service.getSnapshot().group?.playbackRevision,'4');service.dispose();
});

test('a host whose player reaches the end pauses the group once and clamps its target to the local duration',async()=>{
 const s=server();s.set(snap({},'2026-09-16T20:01:40Z'));const service=make(s,()=>T0+100000);await service.open('grp_1');
 const local=player({itemId:'item',positionSeconds:60,playing:false});const p={...local.p,state:()=>({...local.p.state(),durationSeconds:60,ready:false})};
 const follower=new GroupFollower(service,p,()=>T0+100000);follower.start();follower.stop();follower.start();follower.stop();
 assert.equal(follower.target(),60);assert.equal(s.calls.filter(c=>c.path.endsWith('/readiness')&&c.body.ended).length,1);
 assert(!local.log.some(x=>x.startsWith('seek')));service.dispose();
});

for (const latency of [1200, 2000, 5000, 12000, 19000]) test(`native follower converges with ${latency} ms seeks and no rate adjustment`, async t => {
 const {service,clock}=await room(t);
 const slow=slowPlayer(()=>clock.now,{latency,says:'seeking',duration:7200});
 const follower=new GroupFollower(service,slow.p,()=>clock.now);
 const {restarted,drift}=run(follower,slow,clock,120);
 assert.equal(restarted,0,'never supersede a pending correction');
 assert.ok(slow.log.length<=3,`correction loop: ${slow.log.length} seeks`);
 assert.ok(Math.abs(drift.at(-1)!)<0.75,`did not settle: ${drift.at(-1)} seconds`);
});

test('SUP-006: only the precise join refusal identifies an invalid invite code', async () => {
 for (const failure of [{code:'invite_not_found',status:404},{code:'invalid_response',status:404},{code:'not_found',status:404},{code:'invite_expired',status:410},{status:404}]) {
  const s=server(),service=new GroupSessionService({api:{request:async()=>{throw Object.assign(new Error('That invitation code is not valid.'),failure);}},stream:s.stream});
  try {
   assert.equal(await service.join(' abcd-efgh ','Sam'),false);
   assert.deepEqual(service.getSnapshot().joinFailure,'code' in failure&&failure.code==='invite_not_found'?{code:'invite_not_found',inviteCode:'ABCDEFGH'}:undefined);
   service.clearJoinFailure();assert.equal(service.getSnapshot().joinFailure,undefined);assert.equal(service.getSnapshot().error,undefined);
  } finally {service.dispose();}
 }
});

test('SUP-006: retry, replacement and form edits retire only their failed join attempt', async () => {
 const s=server();let reject!:(error:unknown)=>void,hold=false;
 const service=new GroupSessionService({api:{request:async<T,>()=>{if(hold)return new Promise<T>((_resolve,no)=>{reject=no;});throw Object.assign(new Error('wrong code'),{code:'invite_not_found',status:404});}},stream:s.stream});
 try {
  await service.join('ABCDEFGH','Sam');assert.ok(service.getSnapshot().joinFailure);
  hold=true;const retry=service.join('IJKLMNOP','Sam');assert.equal(service.getSnapshot().joinFailure,undefined);
  service.clearJoinFailure();reject(Object.assign(new Error('old refusal'),{code:'invite_not_found',status:404}));
  assert.equal(await retry,false);assert.equal(service.getSnapshot().joinFailure,undefined);assert.equal(service.getSnapshot().error,undefined);
  hold=false;await service.join('ABCDEFGH','Sam');assert.ok(service.getSnapshot().joinFailure);
  const cancel=new AbortController();hold=true;const replacement=service.join('IJKLMNOP','Sam',{signal:cancel.signal,isCurrent:()=>true});
  assert.equal(service.getSnapshot().joinFailure,undefined);cancel.abort();reject(Object.assign(new Error('late refusal'),{code:'invite_not_found',status:404}));
  assert.equal(await replacement,false);assert.equal(service.getSnapshot().joinFailure,undefined);
  hold=false;await service.join('ABCDEFGH','Sam');assert.ok(service.getSnapshot().joinFailure);
  await service.open('other');assert.equal(service.getSnapshot().joinFailure,undefined,'non-join failures never retain a join discriminator');
 } finally {service.dispose();}
});
