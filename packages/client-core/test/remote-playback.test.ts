import {test} from 'node:test';
import assert from 'node:assert/strict';
import {HandoffController,ReceiverHost,receiverPollDelay} from '../src/remote-playback.ts';
import {SOCIAL_PROTOCOL,parseReceiver} from '../src/social-playback.ts';

const receiver={id:'rcv_1',kind:'portico',deviceId:'tv-1',displayName:'Living room',platform:'tvos',keyFingerprint:'A'.repeat(43),supportedCommands:['load','play','pause','seek','stop'],grantPolicy:'per-device',authorizationRevision:'1',state:'active',presence:'online',lastSeenAt:'x',createdAt:'x'};
const grant=(state:string)=>({id:'grt_1',receiverId:'rcv_1',controllerDeviceId:'phone-1',controllerDisplayName:'Phone',receiverKeyFingerprint:'A'.repeat(43),allowedCommands:['load','play','pause','seek','stop'],authorizationRevision:'1',state,expiresAt:'x',createdAt:'x',decidedAt:state==='pending'?null:'x'});
const handoff=(over:Record<string,unknown>={})=>({id:'hdf_1',receiverId:'rcv_1',grantId:'grt_1',requestId:'r1',state:'prepared',outcome:'waiting',reason:'',revision:'1',itemId:'item',sourcePlaybackId:'pb_1',receiverPlaybackId:'',requestedPositionUs:'90000000',readyPositionUs:'0',committedPositionUs:'0',sourceRetired:false,createdAt:'x',expiresAt:new Date(Date.now()+60000).toISOString(),settledAt:null,...over});
const env=(body:Record<string,unknown>)=>({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',...body});

/** A server that walks a handoff through whatever the test scripts. */
function server(script:{grants:string[];handoffs:Record<string,unknown>[];commit?:Record<string,unknown>}){
 const calls:{path:string;method:string;body?:any}[]=[];let g=0,h=0;
 const api={request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{
  calls.push({path,method,body});
  if(path==='/v1/receivers'&&method==='GET')return env({receivers:[receiver]}) as T;
  if(path.endsWith('/grants')&&method==='POST')return env({grant:grant(script.grants[g])}) as T;
  if(path.endsWith('/grants'))return env({grants:[grant(script.grants[Math.min(++g,script.grants.length-1)])]}) as T;
  if(path==='/v1/handoffs')return env({handoff:handoff(script.handoffs[0])}) as T;
  if(path.endsWith('/commit'))return env({handoff:handoff(script.commit??{state:'committed',outcome:'accepted',revision:'3',sourceRetired:true,receiverPlaybackId:'pb_tv',readyPositionUs:'90400000',committedPositionUs:'90400000'})}) as T;
  if(path.endsWith('/rollback'))return env({handoff:handoff({state:'rolled_back',outcome:'rejected'})}) as T;
  if(path.startsWith('/v1/handoffs/'))return env({handoff:handoff(script.handoffs[Math.min(++h,script.handoffs.length-1)])}) as T;
  throw new Error('unexpected '+path);
 }};
 return {api,calls};
}
const controller=(s:ReturnType<typeof server>)=>new HandoffController({api:s.api,device:{deviceId:'phone-1',displayName:'Phone'},requestId:()=>'r1',pollMs:5});
const tv=parseReceiver(receiver);

test('a handoff commits only after the television proves it is playing', async()=>{
 const s=server({grants:['accepted'],handoffs:[{},{},{outcome:'pending',revision:'2',receiverPlaybackId:'pb_tv',readyPositionUs:'90400000'}]});
 const c=controller(s);
 assert.equal(await c.send(tv,{playbackId:'pb_1',positionSeconds:90}),true);
 assert.equal(c.getSnapshot().phase,'done');assert.equal(c.getSnapshot().handoffId,'hdf_1');
 const prepared=s.calls.find(x=>x.path==='/v1/handoffs')!;
 assert.equal(prepared.body.startPositionUs,'90000000');assert.equal(prepared.body.sourcePlaybackId,'pb_1');
 const commit=s.calls.find(x=>x.path.endsWith('/commit'))!;
 assert.equal(commit.body.expectedRevision,'2','commit is fenced on the revision the readiness produced');
 assert.ok(s.calls.findIndex(x=>x.path.endsWith('/commit'))>s.calls.filter(x=>x.path==='/v1/handoffs/hdf_1').length,'polled before committing');
});

test('a television that has not met this device is waited on, and a decline leaves the phone playing', async()=>{
 const accepted=server({grants:['pending','pending','accepted'],handoffs:[{outcome:'pending',revision:'2',receiverPlaybackId:'pb_tv',readyPositionUs:'90400000'}]});
 const c=controller(accepted);
 assert.equal(await c.send(tv,{playbackId:'pb_1',positionSeconds:0}),true);
 const declined=server({grants:['pending','declined'],handoffs:[{}]});
 const d=controller(declined);
 assert.equal(await d.send(tv,{playbackId:'pb_1',positionSeconds:0}),false);
 assert.equal(d.getSnapshot().phase,'failed');assert.match(d.getSnapshot().message!,/declined/);
 assert.ok(!declined.calls.some(x=>x.path==='/v1/handoffs'),'nothing was prepared');
});

test('expiry and cancelling never commit; cancelling rolls the proposal back', async()=>{
 const expired=server({grants:['accepted'],handoffs:[{},{state:'expired',outcome:'rejected'}]});
 const c=controller(expired);
 assert.equal(await c.send(tv,{playbackId:'pb_1',positionSeconds:0}),false);
 assert.match(c.getSnapshot().message!,/Still playing here/);
 assert.ok(!expired.calls.some(x=>x.path.endsWith('/commit')));
 const slow=server({grants:['accepted'],handoffs:[{}]});
 const d=controller(slow);const sending=d.send(tv,{playbackId:'pb_1',positionSeconds:0});
 await new Promise(r=>setTimeout(r,30));d.cancel();
 assert.equal(await sending,false);await new Promise(r=>setTimeout(r,10));
 assert.ok(slow.calls.some(x=>x.path.endsWith('/rollback')));assert.ok(!slow.calls.some(x=>x.path.endsWith('/commit')));
 assert.equal(d.getSnapshot().phase,'idle');
});

test('a television registers, asks about a new device, and reports readiness only once playing', async()=>{
 const calls:{path:string;method:string;body?:any}[]=[];let inbox:Record<string,unknown>={grants:[grant('pending')],handoffs:[]};
 const api={request:async<T,>(path:string,method='GET',body?:unknown):Promise<T>=>{
  calls.push({path,method,body});
  if(path==='/v1/receivers')return env({receiver}) as T;
  if(path.endsWith('/inbox'))return env({receiverId:'rcv_1',...inbox}) as T;
  return env({}) as T;
 }};
 const state={itemId:'',playbackId:'',playing:false,positionSeconds:0};const loads:string[]=[];
 const host=new ReceiverHost({api,device:{deviceId:'tv-1',displayName:'Living room',platform:'tvos',keyFingerprint:'A'.repeat(43)},player:{load:(id,at)=>{loads.push(id+'@'+at);},state:()=>({...state})},pollMs:5});
 host.start();await new Promise(r=>setTimeout(r,40));
 assert.equal(calls[0].body.grantPolicy,'per-device');
 assert.equal(host.getSnapshot().asking?.controllerDisplayName,'Phone');
 await host.decide(host.getSnapshot().asking!,'accept');
 assert.equal(calls.find(c=>c.path.endsWith('/decision'))!.body.decision,'accept');
 inbox={grants:[],handoffs:[handoff()]};await new Promise(r=>setTimeout(r,40));
 assert.deepEqual(loads,['item@90'],'loaded once, however many polls show the same handoff');
 assert.ok(!calls.some(c=>c.path.endsWith('/readiness')),'loading is not readiness');
 Object.assign(state,{itemId:'item',playbackId:'pb_tv',playing:true,positionSeconds:90.4});
 await new Promise(r=>setTimeout(r,700));
 const ready=calls.find(c=>c.path.endsWith('/readiness'))!;
 assert.equal(ready.body.readiness,'playing');assert.equal(ready.body.receiverPlaybackId,'pb_tv');assert.equal(ready.body.positionUs,'90400000');
 host.dispose();
});

/** PERF-16: the idle inbox interval, its jitter, the pause while playing, and the stop. */
const idleDevice={deviceId:'tv-1',displayName:'Living room',platform:'tvos',keyFingerprint:'A'.repeat(43)};
const idlePlayer={load:()=>{},state:()=>({itemId:'',playbackId:'',playing:false,positionSeconds:0})};
const settleImmediate=async()=>{for(let i=0;i<30;i++)await new Promise(setImmediate);};
function idleServer(calls:{path:string;t:number}[]){
 return {request:async<T,>(path:string):Promise<T>=>{
  calls.push({path,t:Date.now()});
  if(path==='/v1/receivers')return env({receiver}) as T;
  if(path.endsWith('/inbox'))return env({receiverId:'rcv_1',grants:[],handoffs:[]}) as T;
  return env({}) as T;
 }};
}
const inboxAt=(calls:{path:string;t:number}[])=>calls.filter(c=>c.path.endsWith('/inbox')).map(c=>c.t);
const beats=(calls:{path:string;t:number}[])=>calls.filter(c=>c.path.endsWith('/heartbeat')).map(c=>c.t);

test('the idle delay is the base ±20%',()=>{
 assert.equal(receiverPollDelay(30000,()=>0),24000);
 assert.equal(receiverPollDelay(30000,()=>0.5),30000);
 const top=receiverPollDelay(30000,()=>0.999999);
 assert.ok(top>=35999&&top<=36000,'top of the band: '+top);
 for(let i=0;i<200;i++){const d=receiverPollDelay(30000,Math.random);assert.ok(d>=24000&&d<=36000,'in band: '+d);}
});

test('idle inbox polls every 30 s and the heartbeat stays at 45 s',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const calls:{path:string;t:number}[]=[];
  const host=new ReceiverHost({api:idleServer(calls),device:idleDevice,player:idlePlayer,pollMs:30000,heartbeatMs:45000,rand:()=>0.5});
  host.start();await settleImmediate();
  assert.equal(inboxAt(calls).length,1,'polls once immediately on start');
  t.mock.timers.tick(30000);await settleImmediate();
  t.mock.timers.tick(30000);await settleImmediate();
  const times=inboxAt(calls);
  assert.deepEqual([times[1]-times[0],times[2]-times[1]],[30000,30000]);
  assert.equal(beats(calls).length,1,'first heartbeat at the first poll after 45 s');
  assert.ok(beats(calls)[0]-times[0]>=45000,'heartbeat keeps its 45 s cadence');
  host.dispose();
 }finally{t.mock.timers.reset();}
});

test('jitter spreads the interval across the ±20% band',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const calls:{path:string;t:number}[]=[];
  const seq=[0,0.999999];let i=0;
  const host=new ReceiverHost({api:idleServer(calls),device:idleDevice,player:idlePlayer,pollMs:30000,heartbeatMs:45000,rand:()=>seq[i++%seq.length]});
  host.start();await settleImmediate();
  t.mock.timers.tick(23999);await settleImmediate();
  assert.equal(inboxAt(calls).length,1,'nothing before the 24 s lower edge');
  t.mock.timers.tick(1);await settleImmediate();
  assert.equal(inboxAt(calls).length,2);
  t.mock.timers.tick(35999);await settleImmediate();
  assert.equal(inboxAt(calls).length,2,'nothing before the 36 s upper edge');
  t.mock.timers.tick(1);await settleImmediate();
  assert.equal(inboxAt(calls).length,3);
  host.dispose();
 }finally{t.mock.timers.reset();}
});

test('the inbox keeps polling while the player is in the foreground',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const calls:{path:string;t:number}[]=[];
  const host=new ReceiverHost({api:idleServer(calls),device:idleDevice,player:idlePlayer,pollMs:30000,heartbeatMs:45000,rand:()=>0.5});
  host.start();await settleImmediate();
  host.setPlayerActive(true);
  t.mock.timers.tick(90000);await settleImmediate();
  assert.equal(inboxAt(calls).length,2,'inbox remains available while playback is in front');
  assert.ok(beats(calls).length>=1,'the heartbeat keeps the registration alive while paused');
  host.setPlayerActive(false);
  t.mock.timers.tick(30000);await settleImmediate();
  assert.equal(inboxAt(calls).length,3,'polling continues on the next interval');
  host.dispose();
 }finally{t.mock.timers.reset();}
});

test('stop ends inbox and heartbeat polling (the app-background path)',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const calls:{path:string;t:number}[]=[];
  const host=new ReceiverHost({api:idleServer(calls),device:idleDevice,player:idlePlayer,pollMs:30000,heartbeatMs:45000,rand:()=>0.5});
  host.start();await settleImmediate();
  const before=calls.length;
  host.stop();
  t.mock.timers.tick(300000);await settleImmediate();
  assert.equal(calls.length,before,'nothing polled after stop');
  assert.equal(host.getSnapshot().phase,'off');
  host.dispose();
 }finally{t.mock.timers.reset();}
});

test('after a handoff the controller can pause, resume, seek and stop the destination session',async()=>{
 const s=server({grants:['accepted'],handoffs:[{},{outcome:'pending',revision:'2',receiverPlaybackId:'pb_tv',readyPositionUs:'90400000'}]});const calls:{id:string;change?:unknown;revision?:string}[]=[];
 let current:any={id:'pb_tv',revision:'1',state:'playing',presentation:{generation:1}};
 const sessions={get:async(id:string)=>{assert.equal(id,'pb_tv');return current;},patch:async(id:string,revision:string,change:any)=>{calls.push({id,revision,change});current={...current,revision:String(Number(current.revision)+1),state:change.state??current.state};return current;},stop:async(id:string)=>{calls.push({id});}};
 const c=new HandoffController({api:s.api,device:{deviceId:'phone-1',displayName:'Phone'},requestId:()=> 'remote-seek',pollMs:5,sessions});
 assert.equal(await c.send(tv,{playbackId:'pb_source',positionSeconds:42}),true);
 assert.equal(c.getSnapshot().receiverPlaybackId,'pb_tv');
 assert.equal(await c.pause(),true);assert.equal(await c.play(),true);assert.equal(await c.seek(19.5),true);assert.equal(await c.stop(),true);
 assert.deepEqual(calls.map(x=>x.change),[{state:'paused'},{state:'playing'},{seek:{positionMs:19500,id:'remote-seek'}},undefined]);
 assert(calls.every(x=>x.id==='pb_tv'));assert.equal(c.getSnapshot().receiverPlaybackId,undefined);c.dispose();
});
