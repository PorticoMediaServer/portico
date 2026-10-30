import test from 'node:test';import assert from 'node:assert/strict';
import {PlaybackDock,type RemoteDock} from '../src/index.ts';import {dock,fence,channelFence,scope,queue} from './fixtures.ts';
const music=dock('music',{fence:{...fence,sessionId:'music',itemId:'song',entryId:'song'},queue:queue() as never,positionSeconds:107});
const video=dock('video',{fence:{...fence,sessionId:'video',itemId:'film',playback:{...fence.playback,run:2}},positionSeconds:845});
const remote=(over:Partial<RemoteDock>={}):RemoteDock=>({id:'cast',kind:'cast',phase:'connecting',sequence:'1',playback:video,positionSeconds:1872,...over});
test('starting video pauses music and close restores the same queue and position paused on every platform',()=>{
 for(const kind of ['music','audiobook'] as const){const d=new PlaybackDock(scope);d.start({...music,kind});const start=d.start(video);
 assert.equal(start.effects[0].kind,'pause');assert.equal(d.occupant()?.mode,'video');assert.equal(start.state.parkedAudio?.playing,false);
 const close=d.close(video.fence);assert.deepEqual(close.effects.map(e=>[e.kind,e.playing]),[['stop',false],['restore',false]]);assert.equal(d.occupant()?.mode,kind);assert.equal(close.state.local?.positionSeconds,107);assert.equal(close.state.local?.queue?.id,'queue');assert.equal(close.state.local?.playing,false);}
});
test('starting music stops video at its place and explicit Resume restores video paused, parking audio',()=>{
 const d=new PlaybackDock(scope);d.start(video);const start=d.start(music);assert.equal(start.effects[0].kind,'stop');assert.equal(start.state.resumeVideo?.positionSeconds,845);assert.equal(d.occupant()?.mode,'music');
 const restored=d.restoreVideo();assert.equal(restored.state.local?.positionSeconds,845);assert.equal(restored.state.local?.playing,false);assert.equal(restored.state.parkedAudio?.positionSeconds,107);assert.ok(restored.effects.every(e=>!e.playing));
 const back=d.restoreAudio();assert.equal(back.state.local?.playing,false);assert.equal(d.occupant()?.mode,'music');
});
test('minimise and expand keep the same source and session; stale callbacks cannot close or update it',()=>{
 const d=new PlaybackDock(scope);d.start(video);const f=d.getSnapshot().local!.fence;d.expand(f);assert.equal(d.occupant(),null);d.collapse(f);assert.deepEqual(d.getSnapshot().local?.fence,f);
 d.close({...f,sessionId:'old'});d.update({...video,fence:{...f,generation:2},positionSeconds:1});assert.equal(d.getSnapshot().local?.positionSeconds,845);
});
test('Cast and Play on always take the single dock, while connecting leaves local playback alone',()=>{
 for(const kind of ['cast','play-on'] as const){const d=new PlaybackDock(scope);d.start(music);d.start(video);const connecting=d.beginRemote(remote({kind}),0);assert.equal(d.occupant()?.mode,kind);assert.deepEqual(connecting.effects,[]);assert.equal(connecting.state.local?.playing,true);
 const playing=d.remote(remote({kind,phase:'playing',sequence:'2'}),1000);assert.equal(playing.effects[0].kind,'stop');assert.equal(playing.state.local?.playing,false);assert.equal(playing.state.parkedAudio?.playing,false);
 const cleared=d.dismissRemote('cast');assert.equal(d.occupant()?.mode,'video');assert.equal(cleared.state.local?.playing,false);assert.deepEqual(cleared.effects,[]);}
});
test('stopped remote offers Continue here for fifteen seconds and restoration never autoplays',()=>{
 const d=new PlaybackDock(scope);d.start(video);d.beginRemote(remote(),0);d.remote(remote({phase:'playing',sequence:'2'}),1000);d.remote(remote({phase:'stopped',sequence:'3'}),2000);assert.equal(d.occupant()?.remote?.phase,'stopped');d.tick(16999);assert.equal(d.occupant()?.mode,'cast');
 const continued=d.continueHere('cast');assert.equal(continued.state.local?.positionSeconds,1872);assert.equal(continued.effects[0].kind,'restore');assert.equal(continued.effects[0].playing,false);
 const q=new PlaybackDock(scope);q.beginRemote(remote({phase:'stopped'}),0);q.tick(15000);assert.equal(q.occupant(),null);
});
test('remote states retain pairing, waiting, replaced and error; stale frames cannot resurrect a dismissed bar',()=>{
 const d=new PlaybackDock(scope);d.beginRemote(remote({phase:'pairing'}),0);d.remote(remote({phase:'waiting',sequence:'2'}),1);assert.equal(d.occupant()?.remote?.phase,'waiting');
 d.remote(remote({phase:'playing',sequence:'1'}),2);assert.equal(d.occupant()?.remote?.phase,'waiting');d.remote(remote({id:'other',phase:'playing',sequence:'3'}),3);assert.equal(d.occupant()?.remote?.phase,'waiting');
 d.remote(remote({phase:'replaced',sequence:'3'}),4);d.remote(remote({phase:'playing',sequence:'4'}),5);assert.equal(d.occupant()?.remote?.phase,'replaced');d.dismissRemote('cast');d.remote(remote({phase:'error',sequence:'5'}),6);assert.equal(d.occupant(),null);
});
test('profile and controller changes discard all parked state and stop live engines',()=>{
 const d=new PlaybackDock(scope);d.start(music);d.start(video);d.beginRemote(remote(),0);const reset=d.reset({...scope,profileId:'other'});assert.deepEqual(reset.state,{local:null,parkedAudio:null,resumeVideo:null,remote:null});assert.ok(reset.effects.every(e=>!e.playing));assert.throws(()=>d.start(music));
});
test('live video docks under the same rule and malformed positions, queues and sequences are rejected',()=>{
 const d=new PlaybackDock(scope);d.start(dock('live'));assert.equal(d.occupant()?.mode,'video');assert.throws(()=>d.start({...video,positionSeconds:NaN}));assert.throws(()=>d.beginRemote(remote({sequence:'01'}),0));assert.throws(()=>d.start({...music,queue:{...queue(),entries:Array.from({length:1001},(_,i)=>({id:'a'+i,hidden:true,removed:false}))} as never}));
});
test('late retired engine facts cannot turn a restored bookmark or remote-controlled local player back on',()=>{
 const d=new PlaybackDock(scope);d.start(music);d.start(video);d.close(video.fence);d.update({...music,playing:true,positionSeconds:999});assert.equal(d.getSnapshot().local?.playing,false);assert.equal(d.getSnapshot().local?.positionSeconds,107);
 d.start(video);d.beginRemote(remote(),0);d.remote(remote({phase:'playing',sequence:'2'}),1000);d.update({...video,playing:true});d.dismissRemote('cast');d.update({...video,playing:true});assert.equal(d.getSnapshot().local?.playing,false);
});

test('live channels park both audio kinds and close restores the exact queue and place paused',()=>{
 for(const kind of ['music','audiobook'] as const){
  const d=new PlaybackDock(scope),live=dock('live');d.start({...music,kind});const started=d.start(live);
  assert.equal(started.state.local?.kind,'live');assert.equal(started.state.local?.fence.kind,'channel');assert.equal(started.state.local?.queue,null);assert.equal(d.occupant()?.mode,'video');
  assert.deepEqual(started.effects.map(e=>[e.kind,e.playing]),[['pause',false]]);
  d.expand(live.fence);assert.equal(d.occupant(),null);d.collapse(live.fence);
  const closed=d.close(live.fence);assert.equal(d.occupant()?.mode,kind);assert.equal(closed.state.local?.queue?.id,'queue');assert.equal(closed.state.local?.positionSeconds,107);assert.equal(closed.state.local?.playing,false);
  assert.deepEqual(closed.effects.map(e=>[e.kind,e.playing]),[['stop',false],['restore',false]]);
  d.update({...music,kind,playing:true,positionSeconds:999});assert.equal(d.getSnapshot().local?.positionSeconds,107);assert.equal(d.getSnapshot().local?.playing,false);
 }
});

test('retunes and source rollovers reject captured live updates, close and layout callbacks',()=>{
 const d=new PlaybackDock(scope),live=dock('live');d.start(live);
 const closeCaptured=()=>d.close(live.fence),expandCaptured=()=>d.expand(live.fence),updateCaptured=()=>d.update({...live,positionSeconds:999});
 for(const nextFence of [
  {...channelFence,generation:2},{...channelFence,channelGeneration:'g2'},{...channelFence,sourceId:'other'},
  {...channelFence,channelId:'other'},{...channelFence,sessionId:'other'},{...channelFence,playback:{...channelFence.playback,run:2}},
 ]){
  const current={...live,fence:nextFence,positionSeconds:42};d.start(current);const saved=d.getSnapshot();
  closeCaptured();expandCaptured();updateCaptured();assert.deepEqual(d.getSnapshot(),saved);
  d.update({...current,positionSeconds:43});assert.equal(d.getSnapshot().local?.positionSeconds,43);
 }
 const queueCollision={...fence,itemId:channelFence.channelId,sessionId:channelFence.sessionId};
 d.start(live);d.close(queueCollision);d.expand(queueCollision);d.update({...video,fence:queueCollision});assert.equal(d.getSnapshot().local?.kind,'live');assert.equal(d.getSnapshot().local?.expanded,false);
});

test('starting audio parks live as a paused bookmark and explicit restore cannot revive its retired engine',()=>{
 const d=new PlaybackDock(scope),live=dock('live');d.start(live);const started=d.start(music);
 assert.equal(started.effects[0].kind,'stop');assert.equal(started.state.resumeVideo?.kind,'live');assert.deepEqual(started.state.resumeVideo?.fence,channelFence);
 const restored=d.restoreVideo();assert.equal(restored.state.local?.kind,'live');assert.equal(restored.state.local?.playing,false);assert.equal(restored.state.parkedAudio?.playing,false);assert.ok(restored.effects.every(e=>!e.playing));
 d.update({...live,playing:true,positionSeconds:999});assert.equal(d.getSnapshot().local?.playing,false);assert.equal(d.getSnapshot().local?.positionSeconds,107);
});

test('Cast and Play on fence live takeover, terminal sequences and Continue here without autoplay',()=>{
 for(const kind of ['cast','play-on'] as const){
  const d=new PlaybackDock(scope),live=dock('live'),r=remote({kind,playback:live});d.start(music);d.start(live);
  assert.deepEqual(d.beginRemote(r,0).effects,[]);assert.equal(d.occupant()?.mode,kind);
  d.remote({...r,phase:'playing',sequence:'2'},1);assert.equal(d.getSnapshot().local?.playing,false);
  const active=d.getSnapshot();assert.throws(()=>d.remote({...r,sequence:'3',playback:{...live,fence:{...channelFence,playback:{...channelFence.playback,scope:{...scope,profileId:'other'}}}}},2));assert.equal(d.getSnapshot(),active);
  d.update({...live,playing:true});assert.equal(d.getSnapshot().local?.playing,false);
  d.remote({...r,phase:'stopped',sequence:'3'},2);const saved=d.getSnapshot();
  d.remote({...r,phase:'playing',sequence:'2'},3);d.remote({...r,phase:'playing',sequence:'4'},4);assert.deepEqual(d.getSnapshot(),saved);
  const continued=d.continueHere(r.id);assert.equal(continued.state.local?.kind,'live');assert.equal(continued.state.local?.queue,null);assert.equal(continued.state.local?.playing,false);assert.equal(continued.state.local?.positionSeconds,r.positionSeconds);
  d.update({...live,playing:true});assert.equal(d.getSnapshot().local?.playing,false);d.remote({...r,sequence:'5'},5);assert.equal(d.occupant()?.mode,'video');
 }
});

test('dock rejects fabricated live queues, catalogue channel fences and cross-profile live occupants',()=>{
 const d=new PlaybackDock(scope),live=dock('live');
 for(const value of [{...live,fence},{...live,queue:queue()},{...live,kind:'music'},{...video,fence:channelFence},{...live,fence:{...channelFence,playback:{...channelFence.playback,scope:{...scope,profileId:'other'}}}}])assert.throws(()=>d.start(value as never));
 d.start(music);d.start(live);d.reset({...scope,controllerEpoch:'new'});assert.deepEqual(d.getSnapshot(),{local:null,parkedAudio:null,resumeVideo:null,remote:null});assert.throws(()=>d.start(live));
});

test('a real v1 dotted queue occurrence docks with no invented strict snapshot and keeps its exact callback fence',()=>{
 const d=new PlaybackDock(scope),current={...video,fence:{...fence,entryId:'g1.0'},queue:null};d.start(current);
 assert.deepEqual(d.getSnapshot().local?.fence,current.fence);assert.equal(d.getSnapshot().local?.queue,null);
 d.update({...current,fence:{...current.fence,entryId:'g1.1'},positionSeconds:99});assert.equal(d.getSnapshot().local?.positionSeconds,current.positionSeconds);
 d.close({...current.fence,entryId:'g1.1'});assert.equal(d.occupant()?.mode,'video');d.close(current.fence);assert.equal(d.occupant(),null);
});
