import test from 'node:test';
import assert from 'node:assert/strict';
import {CastController,castChoiceFields,parseCastEvent,type CastCommand,type CastTransport} from '../src/cast/index.ts';
import {captureCastChoices} from '../src/cast/protocol.ts';
const settle=async()=>{for(let i=0;i<8;i++)await new Promise(setImmediate);};
const stamp={bindingId:'channel-one',controllerGeneration:1,connectionGeneration:1};
const occurrence={sessionId:'playing-one',itemId:'film',generation:1};
function receiver(){
 const sent:CastCommand[]=[],messages=new Set<(raw:unknown)=>void>(),ends=new Set<()=>void>();
 const transport:CastTransport={deviceName:'Living Room TV',deviceId:'sdk-device',send:async m=>{sent.push(m);},onMessage:fn=>{messages.add(fn);return()=>{messages.delete(fn);};},onSessionEnd:fn=>{ends.add(fn);return()=>{ends.delete(fn);};},end(){}};
 return{transport,sent,reply:(m:unknown)=>{for(const fn of [...messages])fn(m);},listeners:()=>messages.size};
}
function harness(options:Partial<ConstructorParameters<typeof CastController>[0]>={}){
 const calls:{path:string;method?:string;body?:unknown}[]=[],timers=new Map<number,{fn:()=>void;ms:number}>();let timer=0,current=true;
 const controller=new CastController({origin:'https://server.example',displayName:'Phone',isCurrent:()=>current,api:{request:async<T>(path:string,method?:string,body?:unknown)=>{calls.push({path,method,body});return{code:'CODE'}as T;}},setTimer:(fn,ms)=>{timers.set(++timer,{fn,ms});return timer;},clearTimer:key=>{timers.delete(key as number);},...options});
 return{controller,calls,retire:()=>{current=false;},fire:(ms:number)=>{for(const [id,held]of [...timers])if(held.ms===ms){timers.delete(id);held.fn();}}};
}
async function start(h:ReturnType<typeof harness>,r:ReturnType<typeof receiver>,choices?:Parameters<CastController['start']>[4]){
 const result=h.controller.start(r.transport,'film',{title:'Film'},12,choices);await settle();r.reply({type:'paired',controller:stamp});await settle();const load=r.sent.findLast(v=>v.type==='load')!;r.reply({type:'loaded',requestId:(load as any).requestId,itemId:'film'});assert.equal(await result,true);return load;
}
function status(r:ReturnType<typeof receiver>,fields:Record<string,unknown>={}){r.reply({type:'status',controller:stamp,occurrence,itemId:'film',positionSeconds:12,durationSeconds:120,paused:false,audioTracks:[],textTracks:[],...fields});}
test('a fresh code pairs this channel, then the load carries metadata and selected tracks',async()=>{
 const h=harness(),r=receiver();const load=await start(h,r,{audioTrackId:'audio',subtitleTrackId:null,versionId:'version'});
 assert.deepEqual(h.calls,[{path:'/v1/cast/codes',method:'POST',body:undefined}]);assert.deepEqual(r.sent[0],{type:'pair',code:'CODE',origin:'https://server.example',displayName:'Phone'});
 assert.deepEqual(load,{type:'load',requestId:'load-1',itemId:'film',startSeconds:12,title:'Film',audioTrackId:'audio',subtitleTrackId:'off',versionId:'version',controller:stamp});
 assert.equal(h.controller.getSnapshot().phase,'casting');
});
test('the original source stays pending until a current receiver confirms the load',async()=>{
 const h=harness(),r=receiver(),source={sessionId:'source',generation:2,queueId:'queue',entryId:'entry',queueRevision:'3'};
 const result=h.controller.start(r.transport,'film',{title:'Film'},0,{queue:{queueId:'queue',entryId:'entry'},source});await settle();
 assert.deepEqual(h.calls[0].body,{sourceSessionId:'source'});assert.equal(h.controller.getSnapshot().transferPending,true);assert.equal(h.controller.getSnapshot().acceptedSource,undefined);
 r.reply({type:'paired',controller:stamp});await settle();r.reply({type:'loaded',requestId:'load-1',itemId:'film'});assert.equal(await result,true);assert.deepEqual(h.controller.getSnapshot().acceptedSource,source);
});
test('retiring the caller while its code read is held never sends that code to the TV',async()=>{
 let resolve!:(v:any)=>void;const held=new Promise(resolvePromise=>{resolve=resolvePromise;});const h=harness({api:{request:async<T>()=>await held as T}}),r=receiver();
 const result=h.controller.start(r.transport,'film',{title:'Film'},0);h.retire();resolve({code:'CODE'});assert.equal(await result,false);assert.equal(r.sent.length,0);
});
test('a paired message without its private channel stamp is refused',async()=>{
 const h=harness(),r=receiver();const result=h.controller.start(r.transport,'film',{title:'Film'},0);await settle();r.reply({type:'paired'});assert.equal(await result,false);assert(!r.sent.some(v=>v.type==='load'));
});
test('stale status description and stale controls cannot retarget a newer occurrence',async()=>{
 let resolve!:(value:string)=>void;const held=new Promise<string>(fn=>{resolve=fn;});let reads=0;
 const h=harness({describe:async()=>++reads===1?held:'New film'}),r=receiver();await start(h,r);status(r);status(r,{occurrence:{...occurrence,sessionId:'playing-two',itemId:'new'},itemId:'new'});await settle();
 const remote=h.controller.getSnapshot().remoteIdentity;assert.equal(remote?.occurrence.sessionId,'playing-two');resolve('Old film');await settle();assert.equal(h.controller.getSnapshot().title,'New film');
 const count=r.sent.length;h.controller.seek(20,{controller:stamp,occurrence});assert.equal(r.sent.length,count);h.controller.pause(remote);assert.equal(r.sent.at(-1)?.type,'pause');
});
test('a stop stays pending until the exact command and target are confirmed',async()=>{
 const h=harness(),r=receiver();await start(h,r);status(r);const target=h.controller.getSnapshot().remoteIdentity!;
 h.controller.stop(target,'stop-one');assert.equal(h.controller.getSnapshot().controlPending,true);
 r.reply({type:'stopped',requestId:'stop-one',target:{controller:stamp,occurrence:{...occurrence,sessionId:'wrong'}}});assert.equal(h.controller.getSnapshot().controlPending,true);
 r.reply({type:'stopped',requestId:'stop-one',target});assert.equal(h.controller.getSnapshot().remoteStopped,true);assert.equal(h.controller.getSnapshot().controlPending,false);
});
test('a resumed SDK session proves this viewer again and reads the title before adoption',async()=>{
 const h=harness({describe:async()=>'Permitted film'}),r=receiver();const adopted=h.controller.adopt(r.transport);await settle();assert.equal(h.calls[0].path,'/v1/cast/codes');r.reply({type:'paired',controller:stamp});await settle();status(r);assert.equal(await adopted,true);assert.equal(h.controller.getSnapshot().title,'Permitted film');h.controller.dispose();assert.equal(r.listeners(),0);
});
test('takeover answers and replacement remain confined to the current SDK channel',async()=>{
 const h=harness(),r=receiver();await start(h,r);r.reply({type:'takeover-requested',requester:'Another phone'});const prompt=h.controller.getSnapshot().takeover;h.controller.answerTakeover(false,prompt);assert.equal(r.sent.at(-1)?.type,'takeover');r.reply({type:'replaced'});assert.equal(h.controller.getSnapshot().phase,'replaced');assert.equal(r.listeners(),0);
});
test('malformed wire clocks are discarded and malformed source choices never become an item-only load',()=>{
 assert.equal(parseCastEvent({type:'status',itemId:'film',positionSeconds:'12'}),undefined);assert.deepEqual(castChoiceFields({subtitleTrackId:null}),{subtitleTrackId:'off'});assert.throws(()=>captureCastChoices({source:{sessionId:'source',generation:1,queueId:'q',entryId:'e',queueRevision:'1'}}));
});

test('a lost load ACK can be confirmed by that exact attempt’s observed playing status',async()=>{
 const h=harness(),r=receiver();const result=h.controller.start(r.transport,'film',{title:'Film'},0);await settle();r.reply({type:'paired',controller:stamp});await settle();
 status(r,{attemptId:'load-1'});assert.equal(await result,true);assert.equal(h.controller.getSnapshot().phase,'casting');
});
