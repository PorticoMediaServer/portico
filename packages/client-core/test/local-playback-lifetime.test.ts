import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService,type PlaybackSession,type PlaybackOptions} from '../src/index.ts';
import {serviceText} from '../src/presentation/service-text.ts';
import {FakePlaybackServer,fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import type {V1Request} from '../src/playback-v1/http.ts';
import type {PlaybackApi,PlaybackJournalEntry} from '../src/index.ts';

function deferred<T>(){let resolve!:(value:T)=>void;const promise=new Promise<T>(r=>{resolve=r;});return {promise,resolve};}
const tick=()=>new Promise<void>(resolve=>setImmediate(resolve));
function local(id='local:entry:1'):PlaybackSession{return {id,generation:1,streamUrl:'app-private-source',mode:'direct',duration:120,resumeSeconds:12};}
function setup(options:PlaybackOptions={}){
 const stopped:string[]=[];let remoteCreates=0;
 const service=new PlaybackService({createPlayback:async()=>{remoteCreates++;throw Error('unexpected remote create');},stopPlayback:async id=>{stopped.push(id);},progressPlayback:async()=>{}},()=> 'request',options);
 return {service,stopped,remoteCreates:()=>remoteCreates};
}

test('a canceled or retired local caller cannot replace accepted playback or call its maker',async()=>{
 const x=setup();try{
  await x.service.playLocal('original',()=>local());const before=x.service.getSnapshot();let made=0;
  const aborted=new AbortController();aborted.abort();
  for(const context of [{signal:aborted.signal},{isCurrent:()=>false},{isCurrent:()=>{throw Error('retired');}}]){
   await assert.rejects(x.service.playLocal('foreign',()=>{made++;return local('foreign');},0,context),{name:'AbortError'});
   assert.equal(x.service.getSnapshot(),before);
  }
  assert.equal(made,0);assert.equal(x.remoteCreates(),0);
 }finally{x.service.leave();}
});

test('aborting journal recovery settles the local command without canceling recovery or a newer source',async()=>{
 const journal=deferred<[]>();let loads=0,made=0;
 const x=setup({deferRecovery:true,journal:{load:()=>{loads++;return journal.promise;},save:async()=>{}}});
 const caller=new AbortController();
 try{
  const pending=x.service.playLocal('old',()=>{made++;return local();},undefined,{signal:caller.signal});
  const rejected=assert.rejects(pending,{name:'AbortError'});caller.abort();await rejected;
  assert.equal(made,0);assert.equal(x.service.getSnapshot().phase,'idle');assert.equal(x.service.getSnapshot().error,undefined);
  const newer=x.service.playLocal('new',()=>local('local:new:2'),33);journal.resolve([]);await newer;
  assert.equal(loads,1);assert.equal(x.service.getSnapshot().session?.id,'local:new:2');
  assert.equal(x.service.getSnapshot().pendingSeek?.positionSeconds,33);assert.equal(x.remoteCreates(),0);
 }finally{journal.resolve([]);x.service.leave();}
});

test('host retirement during recovery refuses the maker; an older command never stops newer Play',async()=>{
 const journal=deferred<[]>();let current=true,made=0;
 const x=setup({deferRecovery:true,journal:{load:()=>journal.promise,save:async()=>{}}});
 try{
  const old=x.service.playLocal('old',()=>{made++;return local();},undefined,{isCurrent:()=>current});
  const rejected=assert.rejects(old,{name:'AbortError'});current=false;
  const next=x.service.playLocal('new',()=>local('local:new:2'));journal.resolve([]);await Promise.all([rejected,next]);
  assert.equal(made,0);assert.equal(x.service.getSnapshot().session?.id,'local:new:2');assert.equal(x.service.getSnapshot().error,undefined);
 }finally{journal.resolve([]);x.service.leave();}
});

test('local factory failures use catalogue text and Retry retains real target, paused intent and caller authority',async()=>{
 const x=setup();let made=0,current=true;
 try{
  await x.service.playLocal('item',()=>{if(++made===1)throw Error('private /path credential');return local('local:item:2');},42,{isCurrent:()=>current});
  assert.equal(x.service.getSnapshot().error,serviceText('playback.message.internal_error'));
  assert.equal(x.service.getSnapshot().session,undefined);
  await x.service.retry();assert.equal(made,2);assert.equal(x.service.getSnapshot().pendingSeek?.positionSeconds,42);
  x.service.pause();x.service.fail(x.service.getSnapshot().intentId,'controlled engine failure');
  current=false;const before=x.service.getSnapshot();await assert.rejects(x.service.retry(),{name:'AbortError'});
  assert.equal(x.service.getSnapshot(),before);assert.equal(made,2);
  current=true;await x.service.retry();assert.equal(x.service.getSnapshot().intent,'paused');assert.equal(x.service.getSnapshot().pendingSeek?.positionSeconds,42);
  assert.equal(x.remoteCreates(),0);
 }finally{x.service.leave();}
});

test('a maker that aborts then throws cannot publish a stale local error',async()=>{
 const x=setup(),caller=new AbortController();
 try{
  await assert.rejects(x.service.playLocal('old',()=>{caller.abort();throw Error('private native exception');},0,{signal:caller.signal}),{name:'AbortError'});
  assert.equal(x.service.getSnapshot().phase,'idle');assert.equal(x.service.getSnapshot().error,undefined);assert.equal(x.service.getSnapshot().session,undefined);
 }finally{x.service.leave();}
});

test('a reentrant local maker cannot adopt or fail over a newer accepted source',async()=>{
 const x=setup();let next:Promise<void>|undefined;
 try{
  await assert.rejects(x.service.playLocal('old',()=>{next=x.service.playLocal('new',()=>local('local:new:2'));throw Error('old maker failed');},undefined,{isCurrent:()=>true}),{name:'AbortError'});
  await next;assert.equal(x.service.getSnapshot().session?.id,'local:new:2');assert.equal(x.service.getSnapshot().error,undefined);
 }finally{x.service.leave();}
});

test('caller cancellation after accepted local admission does not stop that genuine source',async()=>{
 const x=setup(),caller=new AbortController();
 try{
  await x.service.playLocal('item',()=>local(),undefined,{signal:caller.signal,isCurrent:()=>true});
  const before=x.service.getSnapshot();caller.abort();await tick();assert.equal(x.service.getSnapshot(),before);assert.deepEqual(x.stopped,[]);
  x.service.fail(before.intentId,'controlled engine error');await assert.rejects(x.service.retry(),{name:'AbortError'});
 }finally{x.service.leave();}
});

test('local startup timeout uses the canonical service message and retires only its real session',async()=>{
 const x=setup({startupTimeoutMs:5});
 try{
  await x.service.playLocal('item',()=>local());await new Promise(r=>setTimeout(r,15));
  assert.equal(x.service.getSnapshot().phase,'error');assert.equal(x.service.getSnapshot().error,serviceText('playback.message.timeout'));
  assert.equal(x.service.getSnapshot().session,undefined);await tick();assert.deepEqual(x.stopped,['local:entry:1']);
 }finally{x.service.leave();}
});


test('a genuine session returned after synchronous host retirement is cleaned without adoption',async()=>{
 const x=setup();let current=true;
 try{
  await assert.rejects(x.service.playLocal('old',()=>{current=false;return local();},undefined,{isCurrent:()=>current}),{name:'AbortError'});
  await tick();assert.equal(x.service.getSnapshot().phase,'idle');assert.equal(x.service.getSnapshot().error,undefined);
  assert.equal(x.service.getSnapshot().session,undefined);assert.deepEqual(x.stopped,['local:entry:1']);
 }finally{x.service.leave();}
});


test('three-argument local starts retain quiet supersession compatibility',async()=>{
 const journal=deferred<[]>();let oldMaker=0;
 const x=setup({deferRecovery:true,journal:{load:()=>journal.promise,save:async()=>{}}});
 try{
  const old=x.service.playLocal('old',()=>{oldMaker++;return local();});
  const next=x.service.playLocal('new',()=>local('local:new:2'));journal.resolve([]);await Promise.all([old,next]);
  assert.equal(oldMaker,0);assert.equal(x.service.getSnapshot().session?.id,'local:new:2');
 }finally{journal.resolve([]);x.service.leave();}
});


test('paused local admission is paused before the maker and throughout adoption until explicit Play',async()=>{
 const x=setup(),states:string[]=[];let made=0;
 const off=x.service.subscribe(()=>{const s=x.service.getSnapshot();if(s.itemId==='parked')states.push(s.intent);});
 try{
  await x.service.playLocal('parked',()=>{made++;assert.equal(x.service.getSnapshot().intent,'paused');return local();},42,{initialIntent:'paused',isCurrent:()=>true});
  let s=x.service.getSnapshot();assert.equal(s.intent,'paused');assert.equal(s.pendingSeek?.positionSeconds,42);
  const session=s.session,run=s.playbackRun;
  x.service.ready(s.intentId);assert.equal(x.service.seekApplied(s.intentId,s.pendingSeek!.revision,42,'paused'),true);
  assert.ok(states.length>0);assert.ok(states.every(intent=>intent==='paused'),'no transient playing intent was published for the new source');
  x.service.resume();s=x.service.getSnapshot();assert.equal(s.intent,'playing');assert.equal(s.session,session);assert.equal(s.playbackRun,run);
  assert.equal(made,1);assert.equal(x.remoteCreates(),0);
 }finally{off();x.service.leave();}
});

test('paused initial intent is not a sticky preference: local Retry preserves the actual latest intent',async()=>{
 const x=setup();let made=0;
 try{
  await x.service.playLocal('parked',()=>{if(++made===1)throw Error('controlled missing local file');return local('local:parked:'+made);},37,{initialIntent:'paused'});
  assert.equal(x.service.getSnapshot().phase,'error');assert.equal(x.service.getSnapshot().intent,'paused');
  await x.service.retry();let s=x.service.getSnapshot();assert.equal(s.intent,'paused');assert.equal(s.pendingSeek?.positionSeconds,37);
  x.service.ready(s.intentId);x.service.seekApplied(s.intentId,s.pendingSeek!.revision,37,'paused');
  x.service.resume();x.service.fail(s.intentId,'controlled decoder error');await x.service.retry();s=x.service.getSnapshot();
  assert.equal(s.intent,'playing');assert.equal(s.pendingSeek?.positionSeconds,37);assert.equal(made,3);assert.equal(x.remoteCreates(),0);
 }finally{x.service.leave();}
});

test('absent local initial intent stays playing and invalid values cannot replace a genuine source',async()=>{
 const x=setup();try{
  await x.service.playLocal('original',()=>local());assert.equal(x.service.getSnapshot().intent,'playing');
  const before=x.service.getSnapshot();let made=0;
  for(const value of ['invalid',null,1])await assert.rejects(Reflect.apply(x.service.playLocal,x.service,['foreign',()=>{made++;return local('foreign');},0,{initialIntent:value}]),/invalid_local_start_intent/);
  assert.equal(x.service.getSnapshot(),before);assert.equal(made,0);
  await x.service.playLocal('with-context',()=>local('local:context:2'),0,{isCurrent:()=>true});assert.equal(x.service.getSnapshot().intent,'playing');
 }finally{x.service.leave();}
});


function localSource(id:string):PlaybackSession{return {...local(id),mode:'local'};}
function acknowledge(s:PlaybackService){const p=s.getSnapshot();s.ready(p.intentId);if(p.pendingSeek)s.seekApplied(p.intentId,p.pendingSeek.revision,p.pendingSeek.positionSeconds,p.intent);}

test('real V1 transport never receives local cleanup, progress or marker identities',async()=>{
 const server=new FakePlaybackServer(),inner=fakeHttp(server),requests:V1Request[]=[];
 const http={send:async(request:V1Request)=>{requests.push(request);return inner.send(request);}};
 const api=new V1PlaybackApi(http,{timers:{setTimer:()=>0,clearTimer:()=>{}},key:()=> 'custody-key'});
 const journal:PlaybackJournalEntry[][]=[];
 const playback=new PlaybackService(api,()=> 'request',{journal:{load:async()=>[],save:async rows=>{journal.push(structuredClone(rows));}}});
 const queue=new V1QueuePlayer({http,api,playback,viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile'}});
 try{
  for(const id of ['local:session:one','opaque-session-without-prefix']){
   await playback.playLocal('movie',()=>localSource(id));acknowledge(playback);playback.pause();playback.resume();playback.markerSkipped('marker','manual');
   await assert.rejects(playback.confirmQueueEnd(playback.getSnapshot().intentId),/not confirmed/);
  }
  playback.leave();await tick();await tick();
  assert.deepEqual(requests,[],'no local identity may reach any server session route');
  assert.ok(journal.length>0);assert.ok(journal.every(rows=>rows.length===0),'the server recovery journal cannot claim local ownership');
 }finally{queue.dispose();playback.leave();}
});

test('explicit app-local custody receives progress and cleanup for the exact accepted resource',async()=>{
 const serverStops:string[]=[],serverProgress:string[]=[],localStops:string[]=[],localProgress:{id:string;state:string}[]=[];
 const journal:PlaybackJournalEntry[][]=[];
 const api:PlaybackApi={createPlayback:async()=>local('server'),stopPlayback:async id=>{serverStops.push(id);},progressPlayback:async id=>{serverProgress.push(id);},
  localPlayback:{stopPlayback:async id=>{localStops.push(id);},progressPlayback:async(id,payload)=>{localProgress.push({id,state:payload.state});}}};
 const s=new PlaybackService(api,()=> 'request',{journal:{load:async()=>[],save:async rows=>{journal.push(structuredClone(rows));}}});
 try{
  await s.playLocal('movie',()=>localSource('opaque-local-resource'));acknowledge(s);s.pause();await tick();s.leave();await tick();await tick();
  assert.ok(localProgress.length>0);assert.ok(localProgress.every(p=>p.id==='opaque-local-resource'));assert.equal(localProgress.at(-1)?.state,'paused');
  assert.deepEqual(localStops,['opaque-local-resource']);assert.deepEqual(serverStops,[]);assert.deepEqual(serverProgress,[]);
  assert.ok(journal.every(rows=>rows.length===0));
 }finally{s.leave();}
});

test('local startup timeout and canceled late maker retire only through app-local custody',async()=>{
 const server:string[]=[],stops:string[]=[],caller=new AbortController();
 const api:PlaybackApi={createPlayback:async()=>local('server'),stopPlayback:async id=>{server.push(id);},progressPlayback:async id=>{server.push(id);},localPlayback:{stopPlayback:async id=>{stops.push(id);},progressPlayback:async()=>{}}};
 const s=new PlaybackService(api,()=> 'request',{startupTimeoutMs:5});
 try{
  await assert.rejects(s.playLocal('movie',()=>{caller.abort();return localSource('late-local');},0,{signal:caller.signal}),{name:'AbortError'});
  await tick();assert.deepEqual(stops,['late-local']);
  await s.playLocal('movie',()=>localSource('timed-out-local'));await new Promise(r=>setTimeout(r,15));await tick();
  assert.deepEqual(stops,['late-local','timed-out-local']);assert.deepEqual(server,[]);assert.equal(s.getSnapshot().phase,'error');
 }finally{s.leave();}
});

test('failed local cleanup retains its original owner for retry and never falls back to a newer owner or server',async()=>{
 const server:string[]=[],first:string[]=[],second:string[]=[];let fail=true;
 const api:PlaybackApi={createPlayback:async()=>local('server'),stopPlayback:async id=>{server.push(id);},progressPlayback:async id=>{server.push(id);},
  localPlayback:{stopPlayback:async id=>{first.push(id);if(fail)throw Error('native cleanup ACK pending');},progressPlayback:async()=>{}}};
 const s=new PlaybackService(api,()=> 'request');
 try{
  await s.playLocal('one',()=>localSource('one'));api.localPlayback={stopPlayback:async id=>{second.push(id);},progressPlayback:async()=>{}};
  s.leave();await tick();assert.equal(s.getSnapshot().cleanupPending,1);fail=false;s.retryCleanup();await tick();
  assert.deepEqual(first,['one','one']);assert.equal(s.getSnapshot().cleanupPending,0);
  await s.playLocal('two',()=>localSource('two'));s.leave();await tick();
  assert.deepEqual(second,['two']);assert.deepEqual(server,[]);
 }finally{s.leave();}
});

test('server cleanup custody is preserved independently of any local-looking session ID',async()=>{
 const stopped:string[]=[],journal:PlaybackJournalEntry[][]=[];
 const s=new PlaybackService({createPlayback:async()=>local('local:server-owned'),stopPlayback:async id=>{stopped.push(id);},progressPlayback:async()=>{}},()=> 'request',{journal:{load:async()=>[],save:async rows=>{journal.push(structuredClone(rows));}}});
 try{
  await s.play('movie');acknowledge(s);s.pause();await tick();
  assert.ok(journal.some(rows=>rows.some(row=>row.id==='local:server-owned')));
  s.leave();await tick();assert.deepEqual(stopped,['local:server-owned']);
 }finally{s.leave();}
});
