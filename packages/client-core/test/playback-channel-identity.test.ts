import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {channelView,type ChannelPlayback,type ChannelViewSink} from '../src/playback-v1/channel-control.ts';
import type {ChannelReference} from '../src/playback/linear-protocol.ts';
import {playbackFence,sameFence,type ChannelPlaybackFence} from '../../player-core/src/identity.ts';

const scope={serverId:'server',authority:'local' as const,accountId:'account',profileId:'profile',controllerId:'controller',controllerEpoch:'epoch',commandLaneId:'lane'};
const channel:ChannelReference={kind:'live-source',sourceId:'source',channelId:'channel',generation:'g1'};
const projection=(reference:ChannelReference,id='server-session',generation=1)=>channelView({
 id,revision:'1',state:'playing',presentation:{generation,url:'/media/channel.m3u8',linear:{
  name:'News',originMs:0,windowStartMs:0,windowEndMs:60000,liveEdgeMs:60000,state:'active',
  seek:{id:'seek-'+generation,positionMs:60000,acknowledged:false},
 }},
},reference);
function setup(){
 const sinks:ChannelViewSink[]=[];
 const starts:('playing'|'paused')[]=[],controls:string[]=[];
 const channels:ChannelPlayback={
  open:async(reference,sink,options)=>{starts.push(options?.state??'playing');sinks.push(sink);sink.state(projection(reference));},
  recover:async()=>{},pause(){controls.push('pause');},resume(){controls.push('resume');},seek(){},goLive(){controls.push('go-live');},retry(){},observe:async()=>true,close(){},
 };
 const service=new PlaybackService({createPlayback:async()=>{throw new Error('Unexpected catalogue start');},stopPlayback:async()=>{},progressPlayback:async()=>{}},()=> 'request',{channels});
 const fence=():ChannelPlaybackFence=>{
  const snapshot=service.getSnapshot(),reference=snapshot.channel!,session=snapshot.session!;
  if(reference.kind==='live-source')return playbackFence({kind:'channel',playback:{scope,run:snapshot.playbackRun},sessionId:session.id,generation:session.generation,channelKind:reference.kind,sourceId:reference.sourceId,channelId:reference.channelId,channelGeneration:reference.generation});
  return playbackFence({kind:'channel',playback:{scope,run:snapshot.playbackRun},sessionId:session.id,generation:session.generation,channelKind:reference.kind,sourceId:reference.sourceId,channelId:reference.channelId,channelGeneration:reference.generation});
 };
 return {service,sinks,fence,starts,controls};
}

test('each genuine channel selection has a fresh service run, including retunes with identical server source IDs',async()=>{
 const {service,sinks,fence}=setup();
 try{
  assert.equal(service.getSnapshot().playbackRun,0);await service.playChannel(channel);
  const first=fence(),firstIntent=service.getSnapshot().intentId;assert.equal(first.playback.run,1);assert.equal(first.sessionId,'server-session');assert.equal(first.generation,1);
  assert.equal('entryId' in first,false);assert.equal('itemId' in first,false);
  service.ready(firstIntent);service.pause();service.goLive();assert.equal(service.getSnapshot().playbackRun,1);
  await service.playChannel(channel);const second=fence();assert.equal(second.playback.run,2);assert.equal(sameFence(first,second),false);
  const current=service.getSnapshot();sinks[0].state(projection({...channel,generation:'retired'},'retired',9));sinks[0].error('Retired source',true);service.ready(firstIntent);
  assert.equal(service.getSnapshot(),current);assert.equal(service.getSnapshot().phase,'starting');
  service.beginQueueIntent('catalogue-item',true);assert.equal(service.getSnapshot().playbackRun,3);
  await service.playChannel(channel);assert.equal(fence().playback.run,4);
 }finally{service.leave();}
});

test('server media rollover changes the channel fence without inventing a new local intent run',async()=>{
 const {service,sinks,fence}=setup();
 try{
  await service.playChannel(channel);const first=fence();
  sinks[0].state(projection(channel,'server-session',2));const changed=fence();
  assert.equal(changed.generation,2);assert.equal(changed.playback.run,first.playback.run);assert.equal(changed.channelGeneration,'g1');assert.equal(sameFence(first,changed),false);
  const intent=service.getSnapshot().intentId;service.ready(intent);assert.equal(service.getSnapshot().playbackRun,1);
  service.leave();const stopped=service.getSnapshot();sinks[0].state(projection(channel,'server-session',3));sinks[0].error('Late closure',true);assert.equal(service.getSnapshot(),stopped);
 }finally{service.leave();}
});

test('library channel fences preserve the exact service reference with an empty or supplied source',async()=>{
 const {service,fence}=setup();
 try{
  for(const sourceId of ['',channel.channelId]){
   const reference:ChannelReference={...channel,kind:'library-channel',sourceId};await service.playChannel(reference);
   const current=fence();assert.equal(current.channelKind,'library-channel');assert.equal(current.sourceId,sourceId);assert.equal(current.channelId,channel.channelId);assert.equal(current.channelGeneration,reference.generation);
  }
 }finally{service.leave();}
});

test('a restored channel starts genuinely paused before admission or engine attachment, with a fresh run',async()=>{
 const {service,sinks,fence,starts,controls}=setup(),facts:{phase:string;intent:string;run:number}[]=[];
 const unsubscribe=service.subscribe(()=>{const s=service.getSnapshot();facts.push({phase:s.phase,intent:s.intent,run:s.playbackRun});});
 try{
  await service.playChannel(channel,{state:'paused'});assert.deepEqual(starts,['paused']);assert.deepEqual(controls,[]);
  assert.ok(facts.some(s=>s.phase==='starting'&&s.intent==='paused'&&s.run===1));assert.ok(facts.every(s=>s.intent==='paused'));
  const first=fence();service.ready(service.getSnapshot().intentId);sinks[0].state(projection(channel,'server-session',2));assert.equal(service.getSnapshot().intent,'paused');assert.equal(fence().playback.run,1);
  service.resume();assert.deepEqual(controls,['resume']);sinks[0].state(projection(channel,'server-session',2));assert.equal(service.getSnapshot().intent,'playing');
  await service.playChannel(channel);assert.deepEqual(starts,['paused','playing']);assert.equal(fence().playback.run,2);assert.equal(sameFence(first,fence()),false);
 }finally{unsubscribe();service.leave();}
});

test('invalid channel start state cannot retire the current source or start another run',async()=>{
 const {service,starts}=setup();
 try{await service.playChannel(channel);const current=service.getSnapshot();await assert.rejects(service.playChannel(channel,{state:'invalid'} as never));assert.equal(service.getSnapshot(),current);assert.deepEqual(starts,['playing']);}
 finally{service.leave();}
});
