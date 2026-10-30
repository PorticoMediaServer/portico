import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeOfflineTracks} from '../src-next/platform/playback/offline-tracks.ts';
import type {StoredSubtitle} from '../platform/offline-subtitle-files.ts';

const binding = {intentId: 1, sessionId: 'local:copy:1', generation: 1};
const directory = 'file:///Downloads/copy/';
const subtitle = (patch: Partial<StoredSubtitle> = {}): StoredSubtitle => ({id: 'captions', revision: 4, language: 'en', title: 'English',
  default: true, forced: false, format: 'vtt', url: directory + 'extras/sub-captions-4.vtt', ...patch});
function tracks(subtitles: readonly StoredSubtitle[] = [subtitle()]) {
  return new NativeOfflineTracks({entryId: 'copy', itemId: 'movie', playId: 'play', fileUrl: directory + 'media', durationSeconds: 100, startSeconds: 0,
    extras: {subtitles, backdropUrl: ''}}, binding);
}

test('saved VTT selection and timing use the local file and remain one playback choice', () => {
  const local = tracks(); local.configure('native', 'one', ['text']);
  assert.equal(local.getSnapshot().subtitles.length, 1); assert.equal(local.getSnapshot().subtitleTrackId, null);
  local.choose({subtitleTrackId: 'captions', subtitleOffsetMs: 1500});
  const command = local.subtitleCommand({size: 'medium'}, true);
  assert.equal(command.documentUrl, subtitle().url); assert.equal(command.offsetUs, '1500000'); assert.equal(command.localSessionOffsetUs, '1500000'); assert.equal(command.resourceRevision, 4);
  assert.deepEqual(command.binding, binding); assert.equal(command.origin, 'sidecar');
  assert.equal(local.subtitleCommand({}, false).mode, 'off');
  assert.equal(local.subtitleCommand({}, true).documentUrl, subtitle().url);
  local.choose({subtitleTrackId: null}); assert.equal(local.subtitleCommand({}, true).mode, 'off'); assert.equal(local.getSnapshot().subtitleOffsetMs, 0);
  assert.equal(tracks().getSnapshot().subtitleTrackId, null, 'nothing is remembered per title');
});

test('a saved rich original retains its plain reading for native and its fonts for mpv', () => {
  const local = tracks([subtitle({format: 'ass', url: directory + 'extras/sub.ass', textUrl: directory + 'extras/sub.vtt',
    fonts: [{name: 'font0', url: directory + 'extras/font0'}, {name: 'foreign', url: 'https://invalid.test/font'}]})]);
  local.configure('native', 'one', ['text']); local.choose({subtitleTrackId: 'captions'});
  assert.equal(local.subtitleCommand({}, true).documentUrl, directory + 'extras/sub.vtt');
  assert.equal(local.requiredRendering(), 'ass');
  local.configure('mpv', 'two', ['text', 'ass']);
  assert.equal(local.subtitleCommand({}, true).localSessionOffsetUs, undefined, 'mpv keeps its existing offset command');
  assert.deepEqual(local.subtitleCommand({}, true).render, {kind: 'ass', url: directory + 'extras/sub.ass', fonts: [{name: 'font0', url: directory + 'extras/font0'}]});
  assert.equal(local.subtitleCommand({}, true).documentUrl, directory + 'extras/sub.vtt', 'the saved plain reading remains available if the rich renderer refuses');
});

test('an engine without local timing support keeps the saved correction and refuses an ignored offset', () => {
  const local = tracks(); local.configure('native', 'apple', ['text'], ['text'], false);
  local.choose({subtitleTrackId: 'captions'});
  assert.equal(local.getSnapshot().subtitleTimingSupported, false);
  assert.equal(local.subtitleCommand({}, true).offsetUs, '0');
  assert.throws(() => local.choose({subtitleOffsetMs: 500}));
  local.choose({subtitleOffsetMs: 0});
  assert.equal(local.getSnapshot().subtitleOffsetMs, 0);
});

test('a failed local subtitle is reported without failing media and clears on success or Off', () => {
  const local = tracks(); local.configure('native', 'one', ['text']);
  assert.equal(local.accept({type: 'subtitleStatus', error: 'not loaded'}), undefined);
  local.choose({subtitleTrackId: 'captions'});
  assert.equal(local.accept({type: 'subtitleStatus', error: 'not loaded'}), 'changed');
  assert.equal(local.getSnapshot().subtitleError, true);
  assert.equal(local.getSnapshot().subtitleTrackId, 'captions');
  local.accept({type: 'subtitleStatus', error: ''}); assert.equal(local.getSnapshot().subtitleError, undefined);
  local.accept({type: 'subtitleStatus', error: 'not loaded'});
  local.choose({subtitleTrackId: null}); assert.equal(local.getSnapshot().subtitleError, undefined);
});

test('image captions require a real available renderer and wait for its engine before applying', () => {
  const local = tracks([subtitle({format: 'vobsub', url: directory + 'extras/sub.idx'})]);
  local.configure('native', 'one', ['text']); assert.equal(local.getSnapshot().subtitles[0]!.unavailable, true);
  assert.throws(() => local.choose({subtitleTrackId: 'captions'}));
  local.configure('native', 'one', ['text'], ['text', 'vobsub']); local.choose({subtitleTrackId: 'captions'});
  assert.equal(local.requiredRendering(), 'vobsub'); assert.equal(local.subtitleCommand({}, true).mode, 'off');
  local.configure('mpv', 'two', ['text', 'vobsub']);
  assert.deepEqual(local.subtitleCommand({}, true).render, {kind: 'vobsub', url: directory + 'extras/sub.idx', companionUrl: directory + 'extras/sub.sub'});
});

test('files outside the admitted download and ambiguous identities are never offered', () => {
  const local = tracks([subtitle(), subtitle(), subtitle({id: 'remote', url: 'https://invalid.test/sub.vtt'}),
    subtitle({id: 'other-copy', url: 'file:///Downloads/other/sub.vtt'}), subtitle({id: 'query', url: directory + 'extras/sub.vtt?request=1'}),
    subtitle({id: 'encoded-escape', url: directory + 'extras/%2f..%2f..%2fother/sub.vtt'}),
    subtitle({id: 'valid', textUrl: 'https://invalid.test/fallback.vtt'})]);
  local.configure('native', 'one', ['text']); assert.deepEqual(local.getSnapshot().subtitles.map(track => track.id), ['valid']);
  for (const choice of [{subtitleTrackId: 'remote'}, {subtitleTrackId: 'captions'}, {quality: {mode: 'original'}}, {subtitleOffsetMs: Infinity}, {subtitleOffsetMs: 600_001}])
    assert.throws(() => local.choose(choice as any));
});

test('offline mpv audio uses actual engine indices and native applied readback', () => {
  const local = tracks(); local.configure('mpv', 'one', ['text']);
  local.accept({type: 'mediaInfo', audioTracks: [{streamIndex: 1, codec: 'aac', selected: true}, {streamIndex: 7, codec: 'ac3', title: 'Commentary'}]});
  assert.equal(local.getSnapshot().audioTrackId, 'a1');
  local.choose({audioTrackId: 'a7'}); const command = local.audioCommand()!;
  assert.equal(command.streamIndex, 7); assert.equal(local.getSnapshot().audioTrackId, 'a1', 'a request is not applied output');
  assert.equal(local.accept({type: 'audioTrackApplied', audioTrackRevision: command.revision, streamIndex: 1}), undefined);
  assert.equal(local.accept({type: 'audioTrackApplied', audioTrackRevision: command.revision, streamIndex: 7}), 'changed');
  assert.equal(local.getSnapshot().audioTrackId, 'a7'); assert.equal(local.localAudioCommand(), null);
  local.configure('mpv', 'replacement', ['text']);
  const restored = local.audioCommand()!;
  assert.equal(restored.streamIndex, 7); assert.ok(restored.revision > command.revision); assert.deepEqual(local.getSnapshot().audio, []);
  assert.equal(local.getSnapshot().audioTrackId, undefined, 'a retained request is not replacement-engine readback');
  assert.equal(local.accept({type: 'audioTrackApplied', audioTrackRevision: command.revision, streamIndex: 7}), undefined);
  assert.equal(local.accept({type: 'audioTrackApplied', audioTrackRevision: restored.revision, streamIndex: 7}), 'changed');
  local.configure('native', 'different-engine', ['text']); assert.equal(local.audioCommand(), null, 'no cross-engine index mapping is guessed');
});

test('offline Media3 selection names its exact catalogue and an opaque actual track', () => {
  const local = tracks(); local.configure('native', 'one', ['text']);
  const fact = {type: 'mediaInfo', audioTrackCatalogRevision: 3, audioTracks: [{id: 'track-10', codec: 'aac', selected: true}, {id: 'track-11', codec: 'aac'}]};
  local.accept(fact); local.choose({audioTrackId: 'track-11'}); const command = local.localAudioCommand()!;
  assert.equal(command.catalogRevision, 3); assert.equal(command.trackId, 'track-11'); assert.equal(local.audioCommand(), null);
  const applied = {type: 'localAudioTrackApplied', audioTrackRevision: command.revision, catalogRevision: 3, trackId: 'track-11'};
  assert.equal(local.accept({...applied, catalogRevision: 2}), undefined); assert.equal(local.getSnapshot().audioTrackId, 'track-10');
  assert.equal(local.accept(applied), 'changed'); assert.equal(local.getSnapshot().audioTrackId, 'track-11');
  local.accept({...fact, audioTrackCatalogRevision: 4}); assert.equal(local.localAudioCommand(), null);
  assert.equal(local.accept(applied), undefined, 'old inventory cannot settle a new catalogue');
});

test('audio failures are surfaced and invalid or duplicate native tracks cannot select by ordinal', () => {
  const local = tracks(); local.configure('mpv', 'one', ['text']);
  local.accept({type: 'mediaInfo', audioTracks: [{streamIndex: 1}, {streamIndex: 7}]}); local.choose({audioTrackId: 'a7'});
  const pending = local.audioCommand()!;
  assert.equal(local.accept({type: 'audioTrackFailed', audioTrackRevision: pending.revision, streamIndex: 7}), 'failed'); assert.equal(local.audioCommand(), null);
  assert.throws(() => local.choose({audioTrackId: 'a0'}));
  const before = local.getSnapshot(); local.accept({type: 'mediaInfo', audioTracks: [{streamIndex: 3}, {streamIndex: 3}]}); assert.equal(local.getSnapshot(), before);
  local.configure('native', 'new', ['text']);
  const native = local.getSnapshot();
  for (const id of ['track\u0000-id', 'x'.repeat(513)]) {
    local.accept({type: 'mediaInfo', audioTrackCatalogRevision: 1, audioTracks: [{id}]});
    assert.equal(local.getSnapshot(), native, 'an opaque native identity must never be changed by display-text sanitization');
  }
});

const sourceTrack = (trackId: string) => ({container: 'isobmff', trackId});
const nativeInventory = (revision = 3) => ({type: 'mediaInfo', audioTrackCatalogRevision: revision, audioTracks: [
  {id: 'native-first', selected: true, language: 'en', title: 'Same', sourceTrackIdentity: sourceTrack('137')},
  {id: 'native-second', language: 'en', title: 'Same', sourceTrackIdentity: sourceTrack('902')},
]});
const mpvInventory = () => ({type: 'mediaInfo', audioTracks: [
  {streamIndex: 7, selected: true, language: 'en', title: 'Same', sourceTrackIdentity: sourceTrack('137')},
  {streamIndex: 1, language: 'en', title: 'Same', sourceTrackIdentity: sourceTrack('902')},
]});

test('a proven source choice crosses both engines with new commands and revision-bound readback', () => {
  const local = tracks(); local.configure('native', 'native-one', ['text']); local.accept(nativeInventory());
  local.choose({audioTrackId: 'native-second'}); const original = local.localAudioCommand()!;
  local.accept({type: 'localAudioTrackApplied', audioTrackRevision: original.revision, catalogRevision: 3, trackId: original.trackId});
  local.configure('mpv', 'mpv-two', ['ass']);
  assert.equal(local.audioCommand(), null, 'no command uses an old engine token');
  local.accept(mpvInventory()); const mpv = local.audioCommand()!;
  assert.equal(mpv.streamIndex, 1, 'container ID 902 maps to real stream index 1, never ID or audio order');
  assert.ok(mpv.revision > original.revision); assert.equal(local.localAudioCommand(), null);
  assert.equal(local.getSnapshot().audioTrackId, undefined);
  local.accept({type: 'audioTrackApplied', audioTrackRevision: original.revision, streamIndex: 1});
  assert.equal(local.getSnapshot().audioTrackId, undefined, 'retired command cannot acknowledge a replacement');
  local.accept({type: 'audioTrackApplied', audioTrackRevision: mpv.revision, streamIndex: 7});
  assert.equal(local.getSnapshot().audioTrackId, undefined);
  local.accept({type: 'audioTrackApplied', audioTrackRevision: mpv.revision, streamIndex: 1});
  assert.equal(local.getSnapshot().audioTrackId, 'a1');
  local.configure('native', 'native-three', ['text']);
  local.accept({type: 'mediaInfo', audioTrackCatalogRevision: 1, audioTracks: []});
  assert.equal(local.localAudioCommand(), null, 'empty preparing catalogue is not an identity mapping');
  local.accept({...nativeInventory(1), audioTracks: nativeInventory().audioTracks.slice().reverse().map((track, i) => ({...track, id: `replacement-${i}`}))});
  const replacement = local.localAudioCommand()!;
  assert.ok(replacement.revision > mpv.revision); assert.equal(replacement.catalogRevision, 1); assert.equal(replacement.trackId, 'replacement-0');
  assert.equal(local.audioCommand(), null); assert.equal(local.getSnapshot().audioTrackId, undefined);
  local.accept({type: 'localAudioTrackApplied', audioTrackRevision: replacement.revision, catalogRevision: 3, trackId: replacement.trackId});
  assert.equal(local.getSnapshot().audioTrackId, undefined);
  local.accept({type: 'localAudioTrackApplied', audioTrackRevision: replacement.revision, catalogRevision: 1, trackId: replacement.trackId});
  assert.equal(local.getSnapshot().audioTrackId, replacement.trackId);
  assert.equal(tracks().localAudioCommand(), null, 'a new playback has no title-level memory');
});

test('proof is optional and language, title, ordinal, engine IDs or malformed source IDs never substitute for it', () => {
  for (const bad of [undefined, null, '902', {container: 'matroska', trackId: '902'}, sourceTrack('0902'), sourceTrack('2147483648'),
    sourceTrack('0'), sourceTrack('902\u0000'), {...sourceTrack('902'), ordinal: 1}]) {
    const local = tracks(); local.configure('native', 'one', ['text']);
    local.accept({...nativeInventory(), audioTracks: nativeInventory().audioTracks.map(track => ({...track, sourceTrackIdentity: bad}))});
    local.choose({audioTrackId: 'native-second'}); local.configure('mpv', 'two', ['ass']);
    assert.equal(local.accept(mpvInventory()), 'changed'); assert.equal(local.audioCommand(), null);
    assert.equal(local.getSnapshot().audioTrackId, 'a7', 'the replacement reports its actual selection without claiming retention');
  }
});

test('missing or ambiguous replacement proof fails honestly and cannot fall through to a default command', () => {
  for (const offered of [
    [{streamIndex: 7, selected: true, sourceTrackIdentity: sourceTrack('137')}],
    [{streamIndex: 1, sourceTrackIdentity: sourceTrack('902')}, {streamIndex: 7, sourceTrackIdentity: sourceTrack('902')}],
    [{streamIndex: 1, sourceTrackIdentity: sourceTrack('902')}, {streamIndex: 1, sourceTrackIdentity: sourceTrack('137')}],
    Array.from({length: 65}, (_, streamIndex) => ({streamIndex, sourceTrackIdentity: sourceTrack('902')})),
  ]) {
    const local = tracks(); local.configure('native', 'one', ['text']); local.accept(nativeInventory()); local.choose({audioTrackId: 'native-second'});
    local.configure('mpv', 'two', ['ass']);
    assert.equal(local.accept({type: 'mediaInfo', audioTracks: offered}), 'failed'); assert.equal(local.audioCommand(), null);
    assert.equal(local.getSnapshot().audioTrackId, undefined);
    local.configure('native', 'three', ['text']); local.accept(nativeInventory()); assert.equal(local.localAudioCommand(), null, 'failed proof is retired');
  }
});

test('same-engine reload and native catalogue changes re-resolve source proof instead of reusing a stream index', () => {
  const local = tracks(); local.configure('mpv', 'one', ['ass']); local.accept(mpvInventory()); local.choose({audioTrackId: 'a1'});
  const old = local.audioCommand()!; local.configure('mpv', 'two', ['ass']); assert.equal(local.audioCommand(), null);
  local.accept({type: 'mediaInfo', audioTracks: [{streamIndex: 20, selected: true, sourceTrackIdentity: sourceTrack('902')}]});
  const next = local.audioCommand()!; assert.equal(next.streamIndex, 20); assert.ok(next.revision > old.revision);
  assert.equal(local.getSnapshot().audioTrackId, undefined, 'even already-selected inventory waits for this revision acknowledgement');
  local.accept({type: 'audioTrackApplied', audioTrackRevision: next.revision, streamIndex: 20});
  local.configure('native', 'three', ['text']); local.accept(nativeInventory()); const native = local.localAudioCommand()!;
  local.accept(nativeInventory(4)); const refreshed = local.localAudioCommand()!;
  assert.equal(refreshed.catalogRevision, 4); assert.ok(refreshed.revision > native.revision);
  local.accept({type: 'localAudioTrackApplied', audioTrackRevision: native.revision, catalogRevision: 3, trackId: native.trackId});
  assert.equal(local.getSnapshot().audioTrackId, undefined);
  assert.equal(local.accept({type: 'localAudioTrackFailed', audioTrackRevision: refreshed.revision, catalogRevision: 4, trackId: refreshed.trackId}), 'failed');
  local.configure('mpv', 'four', ['ass']); local.accept(mpvInventory()); assert.equal(local.audioCommand(), null);
});

test('an empty settled replacement inventory fails a retained choice after preparation', () => {
  const local = tracks(); local.configure('mpv', 'one', ['ass']); local.accept(mpvInventory()); local.choose({audioTrackId: 'a1'});
  local.configure('native', 'two', ['text']);
  const empty = {type: 'mediaInfo', audioTrackCatalogRevision: 1, audioTracks: []};
  assert.equal(local.accept(empty), 'changed'); assert.equal(local.localAudioCommand(), null);
  local.accept({type: 'ready'});
  assert.equal(local.accept(empty), 'failed'); assert.equal(local.localAudioCommand(), null);
});

test('loss of current producer proof retires cross-engine retention without inventing another identity', () => {
  const local = tracks(); local.configure('native', 'one', ['text']); local.accept(nativeInventory()); local.choose({audioTrackId: 'native-second'});
  local.accept({...nativeInventory(), audioTracks: nativeInventory().audioTracks.map(track => ({...track, sourceTrackIdentity: undefined}))});
  assert.equal(local.localAudioCommand()!.trackId, 'native-second', 'the current engine command still names its real token');
  local.configure('mpv', 'two', ['ass']); local.accept(mpvInventory());
  assert.equal(local.audioCommand(), null, 'a retired proof cannot be revived by another engine');
});
