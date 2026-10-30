import test from 'node:test';
import assert from 'node:assert/strict';
import {directAudioStreamIndex, NativeAudioTrackSelection} from '../src-next/platform/playback/audio-track-selection.ts';

const binding = {intentId: 2, sessionId: 'session-1', generation: 3};
const source = (patch: object = {}): any => ({id: 'session-1', itemId: 'item', versionId: 'version', role: 'local', kind: 'vod',
  presentation: {mode: 'direct', audioTrackId: 'a7'}, ...patch});
const options = (audio = [{id: 'a1'}, {id: 'a7'}]): any => ({itemId: 'item', versions: [{id: 'version', audio}]});

test('mpv uses the accepted absolute stream ID, including nonfirst tracks and index zero', () => {
  assert.equal(directAudioStreamIndex('mpv', source(), options()), 7);
  assert.equal(directAudioStreamIndex('mpv', source(), undefined), 7, 'the accepted canonical ID is not gated on an unrelated options read');
  assert.equal(directAudioStreamIndex('mpv', source({presentation: {mode: 'direct', audioTrackId: 'a0'}}), options([{id: 'a0'}])), 0);
});

test('other engines, remuxes and unmatched or ambiguous offers never receive a file track command', () => {
  assert.equal(directAudioStreamIndex('native', source(), options()), undefined);
  assert.equal(directAudioStreamIndex('mpv', source({presentation: {mode: 'stream', audioTrackId: 'a7'}}), options()), undefined);
  assert.equal(directAudioStreamIndex('mpv', source({versionId: 'old'}), options()), undefined);
  assert.equal(directAudioStreamIndex('mpv', source({itemId: 'other'}), options()), undefined);
  assert.equal(directAudioStreamIndex('mpv', source(), options([{id: 'a1'}])), undefined);
  assert.equal(directAudioStreamIndex('mpv', source(), options([{id: 'a7'}, {id: 'a7'}])), undefined);
  for (const id of ['commentary', 'a-1', 'a01', 'a65536'])
    assert.equal(directAudioStreamIndex('mpv', source({presentation: {mode: 'direct', audioTrackId: id}}), options([{id}])), undefined);
});

test('track choices keep one command revision until the selection or native source changes', () => {
  const tracks = new NativeAudioTrackSelection(), first = tracks.select(binding, 7)!;
  assert.equal(tracks.select(binding, 7), first);
  assert.equal(tracks.getSnapshot()?.state, 'pending');
  const next = tracks.select(binding, 1)!; assert.ok(next.revision > first.revision);
  const reloaded = tracks.select({...binding, generation: 4}, 1)!; assert.ok(reloaded.revision > next.revision);
  tracks.clear(); assert.equal(tracks.getSnapshot(), undefined);
  assert.ok(tracks.select(binding, 7)!.revision > reloaded.revision);
});

test('only a matching native acknowledgement changes pending to applied or failed', () => {
  const tracks = new NativeAudioTrackSelection(), command = tracks.select(binding, 7)!;
  const fact = {type: 'audioTrackApplied', binding, audioTrackRevision: command.revision, streamIndex: 7};
  for (const patch of [{binding: {...binding, generation: 2}}, {binding: {...binding, sessionId: 'old'}}, {audioTrackRevision: 0}, {streamIndex: 1}, {type: 'progress'}]) {
    assert.equal(tracks.accept({...fact, ...patch}), false); assert.equal(tracks.getSnapshot()?.state, 'pending');
  }
  assert.equal(tracks.accept(fact), true); assert.equal(tracks.getSnapshot()?.state, 'applied');
  const next = tracks.select(binding, 1)!;
  assert.equal(tracks.accept(fact), false);
  assert.equal(tracks.accept({...fact, type: 'audioTrackFailed', audioTrackRevision: next.revision, streamIndex: 1}), true);
  assert.equal(tracks.getSnapshot()?.state, 'failed');
});
