import test from 'node:test';
import assert from 'node:assert/strict';
import type {Room} from '@core/room-protocol.ts';
import type {RoomSessionService} from '@core/room-session.ts';
import type {PlaybackFence} from '../../packages/player-core/src/index.ts';
import type {WebPlaybackScreenLifetime} from '../src/platform/playback/types.ts';
import {bindRuntimeGroup} from '../src/platform/playback/group-binding.ts';

test('same-title occurrences admit separately and a cancelled load cannot adopt a successor', async () => {
  const at = new Date().toISOString();
  let room: Room = {id:'g1', name:'Night', control:'host', you:{memberId:'me', host:true, mayControl:true, mayBecomeHost:false},
    clock:{entryId:'e1', positionMs:8000, at, playing:false, ready:1, waitingFor:0}, host:{present:true}, members:[],
    queue:[{entryId:'e1', itemId:'movie', available:true}, {entryId:'e2', itemId:'movie', available:true}], inStepMs:750, jumpMs:3000, reportEveryMs:5000};
  const listeners = new Set<() => void>();
  const emit = () => {for (const listener of [...listeners]) listener();};
  const service = {getSnapshot:() => ({phase:'live', group:room}), serverNow:() => Date.parse(at),
    subscribe:(listener:() => void) => {listeners.add(listener);return () => listeners.delete(listener);}, report:async () => true} as unknown as RoomSessionService;
  const local = {itemId:'movie', positionSeconds:0, playing:true, buffering:false, ready:true, seeking:false};
  const loads: {life:WebPlaybackScreenLifetime; finish:() => void}[] = [];
  const errors: unknown[] = [];
  const binding = bindRuntimeGroup({service, isCurrent:() => true, changed() {}, reportError:error => errors.push(error),
    player:{state:() => local, play:() => {local.playing=true;}, pause:() => {local.playing=false;}, seek:seconds => {local.positionSeconds=seconds;}, setRate() {}},
    load:(_item, _seconds, _playing, life) => new Promise<void>(resolve => loads.push({life, finish:resolve})),
  });
  const fence = {kind:'queue', itemId:'movie'} as PlaybackFence;
  try {
    assert.equal(loads.length,1);assert.equal(binding.matches(fence),false);
    room = {...room, clock:{...room.clock, entryId:'e2', positionMs:12000}};emit();
    assert.equal(loads.length,2);assert.equal(loads[0]!.life.signal.aborted,true);
    loads[0]!.finish();await Promise.resolve();assert.equal(binding.matches(fence),false);
    loads[1]!.finish();await Promise.resolve();assert.equal(binding.matches(fence),true);
    // Apply the latest pause and seek after the asynchronous source load, not its old intent.
    emit();assert.equal(local.playing,false);assert.equal(local.positionSeconds,12);
    assert.deepEqual(errors,[]);
    room = {...room, id:'g2'};emit();assert.equal(binding.matches(fence),false);
    assert.equal(loads.length,3);
  } finally {binding.dispose();}
  assert.equal(loads.at(-1)!.life.signal.aborted,true);
});
