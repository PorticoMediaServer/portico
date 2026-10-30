import {test} from 'node:test';
import assert from 'node:assert/strict';
import {ReceiverHost,parseReceiverInboxEvent} from '../src/remote-playback.ts';
import {SOCIAL_PROTOCOL} from '../src/social-playback.ts';
import {EventsClient,type RawStreamEvent} from '../src/playback-v1/events.ts';

const receiver={id:'rcv_1',kind:'portico',deviceId:'tv-1',displayName:'Living room',platform:'tvos',keyFingerprint:'A'.repeat(43),supportedCommands:['load','play','pause','seek','stop'],grantPolicy:'per-device',authorizationRevision:'1',state:'active',presence:'online',lastSeenAt:'x',createdAt:'x',updatedAt:'x'};
const grant=(state:string)=>({id:'grt_1',receiverId:'rcv_1',controllerDeviceId:'phone-1',controllerDisplayName:'Phone',receiverKeyFingerprint:'A'.repeat(43),allowedCommands:['load','play','pause','seek','stop'],authorizationRevision:'1',state,expiresAt:'x',createdAt:'x',decidedAt:state==='pending'?null:'x'});
const env=(body:Record<string,unknown>)=>({protocolVersion:SOCIAL_PROTOCOL,serverTime:'x',...body});
const device={deviceId:'tv-1',displayName:'Living room',platform:'tvos',keyFingerprint:'A'.repeat(43)};
const player={load:()=>{},state:()=>({itemId:'',playbackId:'',playing:false,positionSeconds:0})};
const settle=async()=>{for(let i=0;i<100;i++)await new Promise(setImmediate);};

/** The shared feed is owned by the app; ReceiverHost only subscribes. */
function fakeStream(){
 let send:((event:RawStreamEvent)=>void)|undefined,close:(()=>void)|undefined,opens=0,id=0;
 const client=new EventsClient({
  http:{request:async()=>({status:200,headers:{},body:{events:[],nextAfter:String(id)}})},
  random:()=>0.5,backoffMs:[5000],
  openStream:async(_after,onEvent,signal)=>{
   opens++;send=onEvent;
   onEvent({data:JSON.stringify({variant:'change',id:String(++id),type:'stream.opened'})});
   await new Promise<void>(resolve=>{close=resolve;signal.addEventListener('abort',()=>resolve(),{once:true});});
   send=undefined;
  },
 });
 client.start();
 return {
  client,
  opens:()=>opens,
  send:(event:string,data:string)=>send?.({event,data}),
  hint:(kind:string,receiverId?:string)=>send?.({data:JSON.stringify({variant:'change',id:String(++id),type:'receiver.inbox_changed',data:receiverId===undefined?{kind}:{kind,receiverId}})}),
  resync:()=>send?.({data:JSON.stringify({variant:'change',id:String(++id),type:'stream.resync'})}),
  drop:()=>close?.(),
 };
}

function hostWithStream(box:{grants:unknown[];handoffs:unknown[]},t:{after(fn:()=>void):void}){
 const calls:{path:string;method:string}[]=[];
 const api={request:async<T,>(path:string,method='GET'):Promise<T>=>{
  calls.push({path,method});
  if(path==='/v1/receivers')return env({receiver}) as T;
  if(path.endsWith('/inbox'))return env({receiverId:'rcv_1',grants:box.grants,handoffs:box.handoffs}) as T;
  return env({}) as T;
 }};
 const events=fakeStream();
 const host=new ReceiverHost({api,device,player,pollMs:30000,heartbeatMs:45000,rand:()=>0.5,events:events.client});
 t.after(()=>{host.dispose();events.client.stop();});
 const inbox=()=>calls.filter(c=>c.path.endsWith('/inbox')).length;
 return {host,calls,inbox,events};
}

test('receiver inbox hints accept the three kinds and skip anything newer or malformed',()=>{
 assert.deepEqual(parseReceiverInboxEvent({kind:'grant',receiverId:'rcv_1'}),{kind:'grant',receiverId:'rcv_1'});
 assert.deepEqual(parseReceiverInboxEvent({kind:'handoff',receiverId:'rcv_1'}),{kind:'handoff',receiverId:'rcv_1'});
 assert.deepEqual(parseReceiverInboxEvent({kind:'resync'}),{kind:'resync'});
 for(const bad of [null,undefined,'grant',[],{},{kind:'change',revision:3},'GRANT',{kind:'GRANT'},{kind:'grant',receiverId:''},{kind:'grant',receiverId:42},{kind:'grant',receiverId:'x'.repeat(201)}])
  assert.equal(parseReceiverInboxEvent(bad),undefined);
});

test('a grant event triggers exactly one inbox fetch; other frames are ignored',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const box:{grants:unknown[];handoffs:unknown[]}={grants:[],handoffs:[]};
  const {host,inbox,events}=hostWithStream(box,t);
  host.start();await settle();
  assert.equal(events.opens(),1);
  const before=inbox();
  events.send('heartbeat','{"revision":7}');
  events.send('change','{"revision":8,"resync":false}');
  events.hint('grant','another_receiver');
  events.hint('grant','rcv_1');
  await settle();
  assert.equal(inbox()-before,1,'one fetch for the grant, none for notification frames');
  host.dispose();events.client.stop();
 }finally{t.mock.timers.reset();}
});

test('stopping the receiver releases subscriptions and leaves the app feed connected',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const {host,inbox,events}=hostWithStream({grants:[],handoffs:[]},t);
  host.start();await settle();
  host.stop();const before=inbox();
  events.hint('grant','rcv_1');events.resync();await settle();
  assert.equal(inbox(),before);
  assert.equal(events.client.getStatus().connected,true);
  assert.equal(events.opens(),1);
  events.client.stop();
 }finally{t.mock.timers.reset();}
});

test('resync refreshes the inbox (a waiting grant is asked about)',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const box:{grants:unknown[];handoffs:unknown[]}={grants:[],handoffs:[]};
  const {host,events}=hostWithStream(box,t);
  host.start();await settle();
  assert.equal(host.getSnapshot().asking,undefined);
  box.grants=[grant('pending')];
  events.resync();
  await settle();
  assert.equal(host.getSnapshot().asking?.controllerDisplayName,'Phone');
  host.dispose();events.client.stop();
 }finally{t.mock.timers.reset();}
});

test('the connected stream suppresses the 30 s poll, keeping the 60 s safety poll',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const {host,inbox,events}=hostWithStream({grants:[],handoffs:[]},t);
  host.start();await settle();
  assert.equal(inbox(),1,'polls once immediately on start');
  t.mock.timers.tick(30000);await settle();
  assert.equal(inbox(),1,'no 30 s poll while the stream is connected');
  assert.equal(events.opens(),1,'the stream was not dropped');
  t.mock.timers.tick(30000);await settle();
  assert.equal(inbox(),2,'the 60 s safety poll still runs');
  host.dispose();events.client.stop();
 }finally{t.mock.timers.reset();}
});

test('a dropped stream resumes the 30 s poll, then backs off again on reconnect',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const {host,inbox,events}=hostWithStream({grants:[],handoffs:[]},t);
  host.start();await settle();
  assert.equal(inbox(),1);
  events.drop();await settle();
  assert.equal(events.opens(),1,'reconnect waits out its backoff');
  // The drop wakes the poll loop: one immediate refresh, then the 30 s cadence (not the 60 s wait).
  assert.equal(inbox(),2,'a dropped stream refreshes at once');
  t.mock.timers.tick(5000);await settle();
  assert.equal(events.opens(),2,'the stream reconnects');
  t.mock.timers.tick(25000);await settle();
  assert.equal(inbox(),3,'reconnection refreshed immediately and resumed the safety interval');
  t.mock.timers.tick(30000);await settle();
  assert.equal(inbox(),3,'reconnected: the safety poll backs off again');
  t.mock.timers.tick(30000);await settle();
  assert.equal(inbox(),4);
  host.dispose();events.client.stop();
 }finally{t.mock.timers.reset();}
});

test('playing keeps inbox hints and polls live without opening another stream',async(t)=>{
 t.mock.timers.enable({apis:['setTimeout','Date']});
 try{
  const {host,inbox,events}=hostWithStream({grants:[],handoffs:[]},t);
  host.start();await settle();
  host.setPlayerActive(true);
  const before=inbox();
  events.hint('grant','rcv_1');
  events.hint('resync');
  t.mock.timers.tick(90000);await settle();
  assert.ok(inbox()>before,'inbox hints and polls remain reachable during playback');
  assert.equal(events.opens(),1,'the stream itself stays up while paused');
  host.setPlayerActive(false);
  t.mock.timers.tick(60000);await settle();
  assert.ok(inbox()>before,'polling resumes when idle again');
  host.dispose();events.client.stop();
 }finally{t.mock.timers.reset();}
});
