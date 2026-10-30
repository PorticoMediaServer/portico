import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackSessionController, SessionsClient} from '../src/playback-v1/sessions.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import type {MediaPlayer, PlayerEvent, PlayerSource} from '../src/playback-v1/port.ts';
import type {V1Http, V1Response} from '../src/playback-v1/http.ts';

const tick = async () => { for (let i=0;i<8;i++) await new Promise(setImmediate); };
const deferred = <T>() => { let resolve!: (v:T)=>void; const promise=new Promise<T>(r=>resolve=r); return {promise,resolve}; };
function player() {
 let source: PlayerSource|undefined, pauses=0;
 const listeners=new Set<(e:PlayerEvent)=>void>();
 const port: MediaPlayer = {load:s=>{source=s;}, play:()=>{}, pause:()=>{pauses++;}, seek:()=>{}, setRate:()=>{}, selectSidecarSubtitle:()=>{},
  observe:()=>({generation:source?.generation??0,state:'playing',positionMs:5000,rate:1}), onEvent:fn=>{listeners.add(fn);return ()=>{listeners.delete(fn);};}, dispose:()=>{}};
 return {port,get source(){return source;},get pauses(){return pauses;},emit:()=>{for(const fn of listeners)fn({type:'time',observation:port.observe()});}};
}
async function setup() {
 const server=new FakePlaybackServer(), http=fakeHttp(server), client=new SessionsClient(http);
 const a=await client.start({itemId:'movie-a'},{},'lifetime_a'), b=await client.start({itemId:'movie-b'},{},'lifetime_b');
 return {http,a,b};
}
const timers={setTimer:()=>0, clearTimer:()=>{}};

for(const ended of [false,true])test(`a delayed session read cannot ${ended?'end':'replace'} a replacement`,async()=>{
 const {http,a,b}=await setup(), p=player(), answer=deferred<V1Response>();
 const wrapped:V1Http={send:r=>r.method==='GET'?answer.promise:http.send(r)};
 const ctl=new PlaybackSessionController({http:wrapped,player:p.port,timers});
 try {ctl.attach(a);const reading=ctl.sessionUpdated(a.id);ctl.attach(b);
  answer.resolve({status:200,headers:{},body:ended?{...a,state:'ended',end:{reason:'terminated'}}:a});await reading;await tick();
  assert.equal(ctl.getSnapshot().session?.id,b.id);assert.equal(ctl.getSnapshot().phase,'active');assert.equal(p.pauses,0);
 }finally{ctl.dispose();}
});

test('a delayed PATCH conflict cannot adopt or retry a replaced session',async()=>{
 const {http,a,b}=await setup(), p=player(), answer=deferred<V1Response>();let patches=0;
 const wrapped:V1Http={send:r=>{if(r.method==='PATCH'){patches++;return patches===1?answer.promise:Promise.resolve({status:200,headers:{},body:a});}return http.send(r);}};
 const ctl=new PlaybackSessionController({http:wrapped,player:p.port,timers});
 try {ctl.attach(a);const changing=ctl.setQuality({mode:'original'});await tick();ctl.attach(b);
  answer.resolve({status:412,headers:{},body:{error:{code:'precondition_failed',retry:'after_refresh',current:a}}});await changing;
  assert.equal(ctl.getSnapshot().session?.id,b.id);assert.equal(patches,1);
 }finally{ctl.dispose();}
});

test('a queued change stays bound to the title for which it was requested',async()=>{
 const {http,a,b}=await setup(), p=player();let patches=0;
 const ctl=new PlaybackSessionController({http:{send:r=>{if(r.method==='PATCH')patches++;return http.send(r);}},player:p.port,timers});
 try {ctl.attach(a);const changing=ctl.setAudio('a2');ctl.attach(b);await changing;assert.equal(patches,0);}finally{ctl.dispose();}
});

test('a stop waiting on the old timeline leaves the replacement playing',async()=>{
 const {http,a,b}=await setup(), p=player(), answer=deferred<V1Response>();
 const ctl=new PlaybackSessionController({http:{send:r=>r.path.endsWith('/timeline')?answer.promise:http.send(r)},player:p.port,timers});
 try {ctl.attach(a);p.emit();const stopping=ctl.stop();ctl.attach(b);const pauses=p.pauses;
  answer.resolve({status:204,headers:{}});await stopping;
  assert.equal(ctl.getSnapshot().session?.id,b.id);assert.equal(ctl.getSnapshot().phase,'active');assert.equal(p.pauses,pauses);
 }finally{ctl.dispose();}
});

test('a created session returned after disposal is stopped without loading the player',async()=>{
 const {http,a}=await setup(), p=player(), answer=deferred<V1Response>();const stopped:string[]=[];
 const ctl=new PlaybackSessionController({http:{send:r=>{if(r.method==='POST')return answer.promise;if(r.method==='DELETE')stopped.push(r.path);return http.send(r);}},player:p.port,timers});
 const start=ctl.start({itemId:'movie-a'});ctl.dispose();answer.resolve({status:201,headers:{},body:a});await start;
 assert.equal(p.source,undefined);assert.deepEqual(stopped,[`/v1/playback/sessions/${a.id}`]);
});

test('attaching a session that already ended never loads its media',async()=>{
 const {http,a}=await setup(),p=player(),ctl=new PlaybackSessionController({http,player:p.port,timers});
 try {ctl.attach({...a,state:'ended',end:{reason:'terminated'}});assert.equal(p.source,undefined);assert.equal(ctl.getSnapshot().phase,'ended');}finally{ctl.dispose();}
});
