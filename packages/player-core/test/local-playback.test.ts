import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock,PlayerSessionChoices,localPlaybackFence,localPlaybackIdentity,playbackFence,sameFence,samePlayback,type LocalPlaybackFence,type LocalPlaybackScope,type DockPlayback} from '../src/index.ts';
import {scope,fence,queue} from './fixtures.ts';

const localScope:LocalPlaybackScope={serverId:scope.serverId,authority:scope.authority,accountId:scope.accountId,profileId:scope.profileId,sessionFamilyId:'actual-family'};
const local:LocalPlaybackFence={kind:'local',playback:{kind:'local',scope:localScope,run:1},sessionId:'local:download-entry:1',generation:1,itemId:'downloaded-item',entryId:'download-entry',playId:'play_journal-id',artifact:{sha256:'a'.repeat(64),bytes:4500}};
const video:DockPlayback={kind:'video',fence:local,queue:null,positionSeconds:17,playing:true,expanded:false};

test('local identity retains the real colon session, journal play and receipt artifact without admitting server or queue substitutes',()=>{
  const parsed=playbackFence(local);
  assert.deepEqual(parsed,local);assert.ok(Object.isFrozen(parsed));assert.ok(Object.isFrozen(parsed.artifact));
  for(const value of [
    {...local,sessionId:'server-session'}, {...local,sessionId:'local:other-entry:1'},
    {...local,sessionId:'local:download-entry:0'}, {...local,sessionId:'local:download-entry:01'},
    {...local,sessionId:'local:download-entry:9007199254740992'},
    {...local,playId:'local:download-entry:1'}, {...local,artifact:{sha256:'a'.repeat(63),bytes:4500}},
    {...local,artifact:{sha256:'a'.repeat(64),bytes:NaN}}, {...local,artifact:{sha256:'a'.repeat(64),bytes:0}},
    {...local,artifact:{sha256:'a'.repeat(64),bytes:4500,fileUrl:'file:///private/file'}},
    {...local,sourceId:'made-up-server-source'}, {...local,entryBytes:4500},
    {...local,playback:{scope,run:1}}, {...local,playback:{...local.playback,scope:{...localScope,controllerId:'fake'}}},
  ])assert.throws(()=>playbackFence(value as never));
  assert.throws(()=>playbackFence({...fence,sessionId:local.sessionId}));
  assert.throws(()=>localPlaybackIdentity({...local.playback,scope:{...localScope,sessionFamilyId:''}}));
  assert.equal(samePlayback(local.playback,fence.playback),false);
});

test('every captured local family, journal, session, artifact and run fact fences callbacks independently',()=>{
  assert.equal(sameFence(localPlaybackFence(local),local),true);
  const changes:LocalPlaybackFence[]=[
    {...local,playback:{...local.playback,run:2}},
    {...local,playback:{...local.playback,scope:{...localScope,sessionFamilyId:'replacement-family'}}},
    {...local,playback:{...local.playback,scope:{...localScope,profileId:'replacement-profile'}}},
    {...local,sessionId:'local:download-entry:2'}, {...local,generation:2},
    {...local,playId:'play_other'}, {...local,itemId:'other-item'},
    {...local,entryId:'other-entry',sessionId:'local:other-entry:1'},
    {...local,artifact:{...local.artifact,sha256:'b'.repeat(64)}},
    {...local,artifact:{...local.artifact,bytes:4501}},
  ];
  for(const changed of changes)assert.equal(sameFence(local,localPlaybackFence(changed)),false);
});

test('the same dock accepts a receipt-bound local occupant only with explicit captured family and never as a remote or queued source',()=>{
  assert.throws(()=>new PlaybackDock(scope).start(video));
  const d=new PlaybackDock(scope,localScope);
  const music:DockPlayback={kind:'music',fence,queue:queue() as never,positionSeconds:107,playing:true,expanded:false};
  d.start(music);const next=d.start(video);assert.equal(next.effects[0].kind,'pause');assert.equal(d.occupant()?.mode,'video');
  d.expand(local);assert.equal(d.occupant(),null);d.collapse(local);assert.equal(d.occupant()?.mode,'video');
  const before=d.getSnapshot();
  for(const changed of [
    {...video,queue:queue()}, {...video,fence:{...local,playback:{...local.playback,scope:{...localScope,sessionFamilyId:'different-family'}}}},
    {...video,fence:{...local,artifact:{...local.artifact,bytes:-1}}},
  ]){assert.throws(()=>d.start(changed as never));assert.equal(d.getSnapshot(),before);}
  assert.throws(()=>d.beginRemote({id:'cast',kind:'cast',phase:'connecting',sequence:'1',playback:video,positionSeconds:17},0));
  assert.equal(d.getSnapshot(),before);
  const closed=d.close(local);assert.equal(closed.state.local?.playing,false);assert.equal(closed.state.local?.positionSeconds,107);assert.equal(closed.state.local?.queue?.id,'queue');
  d.start(video);d.reset(scope,{...localScope,sessionFamilyId:'replacement-family'});const empty=d.getSnapshot();
  d.update(video);d.close(local);assert.equal(d.getSnapshot(),empty);assert.throws(()=>d.start(video));
});

test('local playback uses the same fenced session choices and cannot resurrect choices after stop or source retirement',()=>{
  const choices=new PlayerSessionChoices();choices.begin(local.playback);assert.equal(choices.bindSource(local),true);
  assert.equal(choices.set('speed',local.playback,1.25),true);
  assert.equal(choices.set('audioTrackId',local.playback,'a0',local),true);
  assert.equal(choices.set('subtitleOffsetMs',local.playback,250,local),true);
  const changed=localPlaybackFence({...local,artifact:{...local.artifact,sha256:'b'.repeat(64)}});
  assert.equal(choices.bindSource(changed),true);assert.equal(choices.has('audioTrackId',local.playback),false);assert.equal(choices.has('subtitleOffsetMs',local.playback),false);
  assert.equal(choices.get('speed',local.playback,1),1.25);
  assert.equal(choices.set('audioTrackId',local.playback,'a1',local),false);
  assert.equal(choices.stop(local.playback),true);assert.equal(choices.set('speed',local.playback,2),false);assert.equal(choices.bindSource(local),false);
});
