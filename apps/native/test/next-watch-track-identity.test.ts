import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeWatchTracks, type NativeWatchTrackSource} from '../src-next/screens/watch/track-identity.ts';
import {playbackFence} from '../../../packages/player-core/src/identity.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';

const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version-2', entryId: 'entry', generation: 3});
function source(): NativeWatchTrackSource {
  return {fence, retired: false, dock: {local: {fence}},
    session: {id: 'session', revision: '1', kind: 'vod', role: 'local', state: 'playing', lease: {reportEveryMs: 5000}, itemId: 'item', versionId: 'version-2', queue: {queueId: 'queue', entryId: 'entry'},
      presentation: {generation: 3, mode: 'direct', url: '/v1/media/test/file', engine: 'native', startPositionMs: 0, subtitles: [], decision: {}, quality: {mode: 'original'}, audioTrackId: 'commentary', subtitle: {trackId: 'sidecar', kind: 'text', delivery: 'sidecar'}}},
    options: parseOptions({itemId: 'item', versions: [
      {id: 'version-1', audio: [{id: 'default', default: true}], subtitles: [{id: 'embedded', default: true}]},
      {id: 'version-2', audio: [{id: 'default', default: true}, {id: 'commentary'}], subtitles: [{id: 'embedded', default: true}, {id: 'sidecar'}]},
    ]}),
  };
}

test('selected tracks come only from exact current session/version IDs', () => {
  const current = nativeWatchTracks(source());
  assert.equal(current.version?.id, 'version-2'); assert.equal(current.audio?.id, 'commentary'); assert.equal(current.subtitle?.id, 'sidecar');
  assert.equal(current.subtitlesOff, false);
});

test('missing audio identity never selects a default or first row; subtitle absence is Off only on current session', () => {
  const current = source(), session = current.session!;
  const selected = nativeWatchTracks({...current, session: {...session, presentation: {...session.presentation, audioTrackId: undefined, subtitle: undefined}}});
  assert.equal(selected.audio, undefined); assert.equal(selected.subtitle, undefined); assert.equal(selected.subtitlesOff, true);
  assert.deepEqual(nativeWatchTracks({...current, session: undefined}), {subtitlesOff: false});
});

test('a stale generation, entry, version, options target or retired source yields no identity facts', () => {
  const current = source(), session = current.session!;
  for (const patch of [
    {retired: true}, {session: {...session, id: 'other'}}, {session: {...session, itemId: 'other'}},
    {session: {...session, versionId: 'version-1'}}, {session: {...session, queue: {...session.queue!, entryId: 'other'}}},
    {session: {...session, presentation: {...session.presentation, generation: 2}}}, {options: {...current.options!, itemId: 'other'}},
  ]) assert.deepEqual(nativeWatchTracks({...current, ...patch}), {subtitlesOff: false});
});

test('unknown and ambiguous identities never become selected rows', () => {
  const current = source(), session = current.session!, options = current.options!, version = options.versions[1]!;
  const missing = nativeWatchTracks({...current, session: {...session, presentation: {...session.presentation, audioTrackId: 'not-offered', subtitle: {...session.presentation.subtitle!, trackId: 'not-offered'}}}});
  assert.equal(missing.audio, undefined); assert.equal(missing.subtitle, undefined); assert.equal(missing.subtitlesOff, false);
  assert.deepEqual(nativeWatchTracks({...current, options: {...options, versions: [version, version]}}), {subtitlesOff: false});
});
