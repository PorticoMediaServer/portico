import test from 'node:test';import assert from 'node:assert/strict';
import {playbackFence,sameFence,parseQueueSnapshot,type ChannelPlaybackFence} from '../src/index.ts';
import {fence,channelFence,scope,queue} from './fixtures.ts';

test('queue and channel occurrences never collide even with equal IDs, scope, run, session and media generation',()=>{
 const live=playbackFence({...channelFence,sessionId:fence.sessionId,channelId:fence.itemId});
 assert.equal(sameFence(fence,live),false);assert.equal(sameFence(live,fence),false);
 const queue=playbackFence(fence);assert.deepEqual(queue,fence);assert.equal(sameFence(queue,{...fence}),true);
 assert.ok(Object.isFrozen(queue));assert.ok(Object.isFrozen(queue.playback));assert.ok(Object.isFrozen(queue.playback.scope));
 assert.equal('itemId' in live,false);assert.equal('entryId' in live,false);assert.ok(Object.isFrozen(live));
});

test('each real channel domain, reference, source, generation, session, run and scope field fences callbacks',()=>{
 const current=playbackFence(channelFence);assert.equal(sameFence(current,playbackFence({...channelFence})),true);
 const changed:ChannelPlaybackFence[]=[
  {...channelFence,channelKind:'library-channel',sourceId:channelFence.channelId},
  {...channelFence,sourceId:'other'}, {...channelFence,channelId:'other'},
  {...channelFence,channelGeneration:'g2'}, {...channelFence,generation:2},
  {...channelFence,sessionId:'replacement'}, {...channelFence,playback:{...channelFence.playback,run:2}},
 ];
 for(const key of Object.keys(scope))changed.push({...channelFence,playback:{...channelFence.playback,scope:{...scope,[key]:key==='authority'?'hosted':'other'}}});
 for(const next of changed){assert.equal(sameFence(current,playbackFence(next)),false);assert.equal(sameFence(playbackFence(next),current),false);}
 for(const next of [{...fence,itemId:'other'},{...fence,entryId:'other'},{...fence,sourceId:'other'},{...fence,sessionId:'other'},{...fence,generation:2},{...fence,playback:{...fence.playback,run:2}}])assert.equal(sameFence(fence,playbackFence(next)),false);
});

test('library references retain the source ID actually supplied, including an absent or empty source',()=>{
 const {sourceId:_,...base}=channelFence;
 const absent=playbackFence({...base,channelKind:'library-channel'});
 const empty=playbackFence({...base,channelKind:'library-channel',sourceId:''});
 const supplied=playbackFence({...base,channelKind:'library-channel',sourceId:base.channelId});
 assert.equal(Object.hasOwn(absent,'sourceId'),false);assert.equal(empty.sourceId,'');assert.equal(supplied.sourceId,base.channelId);
 assert.equal(sameFence(absent,empty),false);assert.equal(sameFence(empty,supplied),false);
 assert.equal(sameFence(absent,playbackFence({...base,channelKind:'library-channel'})),true);
 assert.throws(()=>playbackFence({...base,channelKind:'library-channel',sourceId:'invented'}));
});

test('constructors reject preparing sources, fake queue/channel hybrids and missing or malicious domain identity',()=>{
 for(const value of [
  {...fence,kind:undefined},{...fence,kind:'movie'},{...fence,entryId:''},{...fence,itemId:''},
  {...fence,channelId:'channel'},{...fence,channelKind:'live-source'},
  {...channelFence,itemId:'channel'},{...channelFence,entryId:'channel'},{...channelFence,showId:'show'},
  {...channelFence,channelKind:'vod'},{...channelFence,channelId:''},{...channelFence,channelGeneration:''},
  {...channelFence,channelGeneration:'../g1'},{...channelFence,sourceId:undefined},{...channelFence,sourceId:''},
  {...channelFence,sessionId:''},{...channelFence,generation:0},{...channelFence,generation:NaN},
  {...channelFence,generation:Number.MAX_SAFE_INTEGER+1},{...channelFence,playback:{...channelFence.playback,run:0}},
 ])assert.throws(()=>playbackFence(value as never),JSON.stringify(value));
});

test('real v1 dotted occurrence IDs are preserved without loosening other identity or strict queue domains',()=>{
 const current=playbackFence({...fence,entryId:'g1.0'});
 assert.equal(current.entryId,'g1.0');assert.equal(sameFence(current,playbackFence({...fence,entryId:'g1.0'})),true);
 assert.equal(sameFence(current,playbackFence({...fence,entryId:'g1.1'})),false);
 assert.equal(sameFence(current,{...channelFence,sessionId:fence.sessionId,channelId:fence.itemId}),false);
 for(const entryId of ['g1.0/next','g1.0\\next','g1.0?x','g1.0#x','g1.0%2Fnext','g1.0:next','g1.0 next','g1.0\u0000','g'.repeat(129)])assert.throws(()=>playbackFence({...fence,entryId}));
 for(const key of ['itemId','sourceId','sessionId'] as const)assert.throws(()=>playbackFence({...fence,[key]:'g1.0'}));
 assert.throws(()=>playbackFence({...channelFence,channelId:'g1.0'}));
 const snapshot=queue();snapshot.entries[0].id='g1.0';snapshot.currentEntryId='g1.0';assert.throws(()=>parseQueueSnapshot(snapshot,scope));
});
