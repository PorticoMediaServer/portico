import assert from 'node:assert/strict';
import test from 'node:test';
import type {PlaybackFence} from '../../packages/player-core/src/index.ts';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../src/platform/playback';
import type {BrowserAudioFacts} from '../src/platform/playback/audio/output';
import {readWatchMusicAudioFacts} from '../src/screens/watch/music/audio-facts.ts';

// Authored UNRUN source vectors for the actual read-only port guard. No engine,
// AudioContext, decoder probe, device or media is started by these fixtures.
const fence: PlaybackFence = Object.freeze<PlaybackFence>({kind: 'queue', playback: {run: 1, scope: {
  serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile',
  controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane',
}}, sessionId: 'session', generation: 1, sourceId: 'source', entryId: 'entry', itemId: 'song'});
const facts: BrowserAudioFacts = Object.freeze({engine: 'web-audio', bitPerfect: false, sourceRate: 96000,
  contextRate: 48000, resampledToContext: true, deviceRate: null, browserMayResample: true,
  sourceChannels: 6, outputChannels: 2, eqApplied: false, levelingApplied: false, clock: 'unmeasured-carrier'});
function fixture() {
  // Only the view fields this guard observes; the production caller supplies the
  // real immutable runtime snapshot, and the port still accepts the full fence.
  const shown = {disposed: false, fence, presentation: {kind: 'music'}, session: {kind: 'audio'},
    playback: {audioEffects: {rendering: true}}, dock: {}} as unknown as WebPlaybackRuntimeSnapshot;
  let current = shown, reads = 0, changedDuringRead = false, throws = false;
  const port: Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'audioEngineFacts'> = {
    getSnapshot: () => current,
    audioEngineFacts: original => {
      reads++;assert.equal(original, fence);
      if (throws) throw new Error('playback_fence_changed');
      if (changedDuringRead) current = {...shown};
      return facts;
    },
  };
  return {shown, port, get reads() {return reads;}, replace: () => {current = {...shown};},
    changeDuringRead: () => {changedDuringRead = true;}, refuse: () => {throws = true;},
    display: (snapshot: WebPlaybackRuntimeSnapshot) => {current = snapshot;}};
}

test('the original local music render reads facts through its exact full fence', () => {
  const f = fixture();assert.equal(readWatchMusicAudioFacts(f.port, f.shown), facts);assert.equal(f.reads, 1);
  assert.equal(facts.deviceRate, null);assert.equal(facts.bitPerfect, false);
});

test('a successor snapshot with the same fence cannot lend facts to the old render', () => {
  const f = fixture();f.replace();assert.equal(readWatchMusicAudioFacts(f.port, f.shown), undefined);assert.equal(f.reads, 0);
});

test('a change during the guarded port read suppresses the returned observation', () => {
  const f = fixture();f.changeDuringRead();assert.equal(readWatchMusicAudioFacts(f.port, f.shown), undefined);assert.equal(f.reads, 1);
});

test('a retired or refused port does not report an old observation', () => {
  const f = fixture();f.refuse();assert.equal(readWatchMusicAudioFacts(f.port, f.shown), undefined);
});

test('remote playback, non-music, no current audio render and disposed snapshots make zero fact reads', () => {
  const f = fixture();
  const unavailable = [
    {...f.shown, dock: {...f.shown.dock, remote: {phase: 'playing'}}},
    {...f.shown, presentation: {...f.shown.presentation, kind: 'video'}},
    {...f.shown, playback: {...f.shown.playback, audioEffects: {...f.shown.playback.audioEffects, rendering: false}}},
    {...f.shown, fence: undefined}, {...f.shown, disposed: true},
  ] as unknown as WebPlaybackRuntimeSnapshot[];
  for (const snapshot of unavailable) {
    f.display(snapshot);assert.equal(readWatchMusicAudioFacts(f.port, snapshot), undefined);
  }
  assert.equal(f.reads, 0);
});
