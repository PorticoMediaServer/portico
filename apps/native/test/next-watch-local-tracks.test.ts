import test from 'node:test';
import assert from 'node:assert/strict';
import {localPlaybackFence} from '../../../packages/player-core/src/local-playback-identity.ts';
import {nativeWatchTracks, type NativeWatchTrackSource} from '../src-next/screens/watch/track-identity.ts';
import {nativeWatchSubtitleTiming} from '../src-next/screens/watch/subtitle-timing.ts';
import type {NativePlaybackRuntimeSnapshot} from '../src-next/platform/playback/types';

const fence = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'}, run: 1},
  sessionId: 'local:copy:1', generation: 1, itemId: 'movie', entryId: 'copy', playId: 'play', artifact: {sha256: 'a'.repeat(64), bytes: 100}});
function source(): NativeWatchTrackSource {
  return {fence, retired: false, dock: {local: {fence}}, playback: {intentId: 7},
    localTracks: {binding: {intentId: 7, sessionId: fence.sessionId, generation: 1}, audioTrackId: 'audio-2', subtitleTrackId: 'sub-en', subtitleOffsetMs: 250, subtitleTimingSupported: true,
      audio: [{id: 'audio-1', codec: 'aac', title: 'First', default: true, commentary: false, visualImpaired: false, atmos: false},
        {id: 'audio-2', codec: 'aac', title: 'Selected', default: false, commentary: false, visualImpaired: false, atmos: false}],
      subtitles: [{id: 'sub-en', format: 'vtt', source: 'sidecar', language: 'en', title: 'English', default: false, sdh: true, forced: false},
        {id: 'sub-missing', format: 'ass', source: 'sidecar', default: false, sdh: false, forced: false, unavailable: true}]}};
}

test('downloaded choices use their actual local binding without a server session or options', () => {
  const current = source(), tracks = nativeWatchTracks(current);
  assert.strictEqual(tracks.local, current.localTracks);
  assert.equal(tracks.version, undefined);
  assert.equal(tracks.audio?.id, 'audio-2');
  assert.equal(tracks.subtitle?.id, 'sub-en');
  assert.equal(tracks.subtitlesOff, false);
});

test('local selected identities never fall back to defaults, unknown tracks or unavailable saved files', () => {
  const current = source(), local = current.localTracks!;
  const none = nativeWatchTracks({...current, localTracks: {...local, audioTrackId: undefined, subtitleTrackId: null}});
  assert.equal(none.audio, undefined);assert.equal(none.subtitle, undefined);assert.equal(none.subtitlesOff, true);
  const unknown = nativeWatchTracks({...current, localTracks: {...local, audioTrackId: 'other', subtitleTrackId: 'sub-missing'}});
  assert.equal(unknown.audio, undefined);assert.equal(unknown.subtitle, undefined);assert.equal(unknown.subtitlesOff, false);
  const duplicate = nativeWatchTracks({...current, localTracks: {...local, audio: [local.audio[1]!, local.audio[1]!], subtitles: [local.subtitles[0]!, local.subtitles[0]!]}});
  assert.equal(duplicate.audio, undefined);assert.equal(duplicate.subtitle, undefined);
});

test('retired or mismatched local bindings publish no selected tracks or Off fact', () => {
  const current = source(), local = current.localTracks!;
  for (const patch of [
    {retired: true}, {localTracks: undefined}, {playback: undefined}, {playback: {intentId: 8}},
    {dock: {local: null}}, {dock: {local: {fence: {...fence, playId: 'other-play'}}}},
    {localTracks: {...local, binding: {...local.binding, sessionId: 'other'}}},
    {localTracks: {...local, binding: {...local.binding, generation: 2}}},
  ]) assert.deepEqual(nativeWatchTracks({...current, ...patch}), {subtitlesOff: false});
});

test('downloaded subtitle timing applies one bounded session correction to the current saved track', () => {
  const current = source() as NativePlaybackRuntimeSnapshot;
  assert.deepEqual(nativeWatchSubtitleTiming(current, 500), {subtitleTrackId: 'sub-en', subtitleOffsetMs: 750});
  assert.deepEqual(nativeWatchSubtitleTiming(current, -500), {subtitleTrackId: 'sub-en', subtitleOffsetMs: -250});
  assert.equal(nativeWatchSubtitleTiming(current, Number.NaN), undefined);
  assert.equal(nativeWatchSubtitleTiming(current, 600000), undefined);
  assert.equal(nativeWatchSubtitleTiming({...current, localTracks: {...current.localTracks!, subtitleTrackId: null}}, 500), undefined);
  assert.equal(nativeWatchSubtitleTiming({...current, playback: {...current.playback, intentId: 8}}, 500), undefined);
});

test('a local renderer without timing support keeps the saved track usable without offering a false correction', () => {
  const held = source() as NativePlaybackRuntimeSnapshot;
  const current = {...held, localTracks: {...held.localTracks!, subtitleTimingSupported: false}};
  assert.equal(nativeWatchTracks(current).subtitle?.id, 'sub-en');
  assert.equal(nativeWatchSubtitleTiming(current, 500), undefined);
  assert.equal(nativeWatchSubtitleTiming(current, -500), undefined);
});
