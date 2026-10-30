import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {audioTrackModels, subtitleTrackModels, versionModels, fileInfoModel, playbackInfoModel, sessionFactParts, qualityControlModel, type FileFacts, type FactPart, type KitTranslate, type PlaybackFacts, type VersionInput} from '../src/presentation/kit-model/index.ts';
const t: KitTranslate = (id, ...args) => formatMessage(shared[id], args[0]);
const env = {t, locale: 'en-US', timeZone: 'UTC', hourCycle: 'h12'} as const;
const texts = (parts: readonly FactPart[]) => parts.map(x => x.text);

test('audio menu name, channel layout and codec are separate; protocol LABEL stays byte-for-byte', () => {
  const result = audioTrackModels([{id: 'rendition-0', resourceId: 'resource.0', streamIndex: 0, renditionLabel: 'English · AAC · 5.1', language: 'eng', codec: 'aac', channels: 6, title: 'English'}, {id: 'r1', language: 'es-419', channels: 2, codec: 'aac'}], env);
  assert.deepEqual(result[0], {id: 'rendition-0', resourceId: 'resource.0', streamIndex: 0, renditionLabel: 'English · AAC · 5.1', name: {id: 'name', text: 'English', role: 'identity'}, detail: [{id: 'layout', text: '5.1 surround', role: 'context'}], technical: [{id: 'codec', text: 'AAC', role: 'value'}]});
  assert.equal(result[1]!.name.text, 'Spanish (Latin America)');
  assert.deepEqual(texts(result[1]!.detail), ['Stereo']);
});
test('audio layouts honor explicit layout; zero and nonfinite channel metadata are omitted', () => {
  const rows = audioTrackModels([{id: 'a', language: 'en', channelLayout: '7.1', channels: 2}, {id: 'b', language: 'fr', channels: 1}, {id: 'c', channels: NaN}, {id: 'd', channels: -1}, {id: 'e', channels: 3}], env);
  assert.deepEqual(rows.map(r => texts(r.detail)), [['7.1 surround'], ['Mono'], [], [], ['3 channels']]);
});
test('commentary remains a distinct genuine fact and names preserve arbitrary provider punctuation', () => {
  const tracks = audioTrackModels([{id: 'a', language: 'en', channels: 2}, {id: 'b', language: 'en', channels: 2, commentary: true}, {id: 'c', language: 'en', title: 'Director’s commentary · restored', commentary: true}], env);
  assert.deepEqual(texts(tracks[0]!.detail), ['Stereo']);
  assert.deepEqual(texts(tracks[1]!.detail), ['Stereo', 'Commentary']);
  assert.deepEqual(texts(tracks[2]!.detail), ['Director’s commentary · restored']);
});
test('duplicate audio choices gain ordinals without putting codecs into menu copy', () => {
  const tracks = audioTrackModels([{id: 'a', language: 'en', channels: 2, codec: 'aac'}, {id: 'b', language: 'en', channels: 2, codec: 'ac3'}], env);
  assert.deepEqual(tracks.map(r => texts(r.detail)), [['Stereo', '(1)'], ['Stereo', '(2)']]);
  assert.deepEqual(tracks.map(r => texts(r.technical)), [['AAC'], ['Dolby Digital']]);
});
test('subtitle language, forced, SDH, title and exact resources stay separate', () => {
  const models = subtitleTrackModels([{id: 'en', resourceId: 'sub-en', language: 'en', format: 'srt'}, {id: 'fr', language: 'fr', title: 'Français', format: 'srt'}, {id: 'sdh', language: 'en', title: 'SDH', format: 'srt', hearingImpaired: true}, {id: 'forced', language: 'en', title: 'Forced', format: 'srt', forced: true}], env);
  assert.deepEqual(models.map(m => [m.name.text, texts(m.detail)]), [['English', []], ['French', []], ['English', ['For deaf and hard of hearing viewers']], ['English', ['Forced']]]);
  assert.equal(models[0]!.resourceId, 'sub-en'); assert.deepEqual(models.map(m => m.id), ['en', 'fr', 'sdh', 'forced']);
  assert.deepEqual(texts(models[0]!.technical), ['SRT']);
});
test('duplicate subtitle languages gain format then ordinal only where needed', () => {
  const m = subtitleTrackModels([{id: '1', language: 'en', format: 'srt'}, {id: '2', language: 'en', format: 'srt'}, {id: '3', language: 'en', format: 'ass'}, {id: '4', language: 'fr', format: 'srt'}], env);
  assert.deepEqual(m.map(x => [x.name.text, texts(x.detail)]), [['English', ['SRT', '(1)']], ['English', ['SRT', '(2)']], ['English', ['ASS']], ['French', []]]);
  const sdh = subtitleTrackModels([{id: 'a', language: 'en', title: 'SDH', hearingImpaired: true, format: 'srt'}, {id: 'b', language: 'en', hearingImpaired: true, format: 'srt'}], env);
  assert.deepEqual(sdh.map(x => texts(x.detail)), [['For deaf and hard of hearing viewers', 'SRT', '(1)'], ['For deaf and hard of hearing viewers', 'SRT', '(2)']]);
});
test('stock language-prefixed forced/SDH wording is said once; custom descriptions stay intact', () => {
  const rows = subtitleTrackModels([{id: 'a', language: 'en', title: 'English (SDH)', hearingImpaired: true}, {id: 'b', language: 'en', title: 'English - Forced', forced: true}, {id: 'c', language: 'en', title: 'English SDH restoration · original', hearingImpaired: true}], env);
  assert.deepEqual(rows.map(x => texts(x.detail)), [['For deaf and hard of hearing viewers'], ['Forced'], ['English SDH restoration · original', 'For deaf and hard of hearing viewers']]);
});
test('unnamed/unknown tracks remain distinct without inventing a language', () => {
  assert.deepEqual(subtitleTrackModels([{id: 'u', language: 'und', title: ''}, {id: 'z', language: 'zxx'}, {id: 'q', language: 'not a language'}], env).map(x => [x.id, x.name.text]), [['u', 'Track 1'], ['z', 'Track 2'], ['q', 'Track 3']]);
  assert.equal(subtitleTrackModels([{id: 'custom', title: 'A provider · name'}], env)[0]!.name.text, 'A provider · name');
  assert.throws(() => subtitleTrackModels([{id: 'same'}, {id: 'same'}], env), /duplicate_track_identity/);
});

test('version resolution, codecs, container and edition are individual semantic facts', () => {
  const models = versionModels([{id: '4k', height: 2160, videoCodec: 'hevc', container: 'mkv', edition: 'Director’s Cut', dynamicRange: 'dolby_vision'}, {id: 'hd', height: 1080, videoCodec: 'h264', container: 'mp4'}], env);
  assert.deepEqual(models.map(m => [m.id, texts(m.name), texts(m.detail)]), [['4k', ['4K', 'HEVC', 'Dolby Vision', 'MKV'], ['Director’s Cut']], ['hd', ['1080p', 'H.264', 'MP4'], []]]);
});
test('only colliding versions gain bitrate, then size, then version number', () => {
  const same: readonly VersionInput[] = [{id: 'a', height: 360, videoCodec: 'h264', container: 'mp4'}, {id: 'b', height: 360, videoCodec: 'h264', container: 'mp4'}];
  assert.deepEqual(versionModels(same.map((v, i) => ({...v, bitRate: i ? 3_200_000 : 1_400_000, sizeBytes: i ? 17_000_000 : 7_300_000})), env).map(v => texts(v.detail)), [['1.4 Mbps'], ['3.2 Mbps']]);
  assert.deepEqual(versionModels(same.map((v, i) => ({...v, bitRate: i ? undefined : 2_000_000, sizeBytes: i ? 17_000_000 : 7_300_000})), env).map(v => texts(v.detail)), [['7 MB'], ['16.2 MB']]);
  assert.deepEqual(versionModels(same, env).map(v => texts(v.detail)), [['Version 1'], ['Version 2']]);
  assert.deepEqual(versionModels([...same, {id: 'c', height: 2160}], env).map(v => texts(v.detail)), [['Version 1'], ['Version 2'], []]);
  assert.deepEqual(versionModels(same.map((v, i) => ({...v, edition: i ? 'Extended' : 'Theatrical'})), env).map(v => texts(v.detail)), [['Theatrical'], ['Extended']]);
});
test('version names preserve provider punctuation and IDs; invalid measurements add nothing', () => {
  assert.deepEqual(versionModels([{id: 'real-id', name: 'Restoration · Part I'}], env)[0]!.name, [{id: 'name', text: 'Restoration · Part I', role: 'identity'}]);
  assert.deepEqual(texts(versionModels([{id: 'a', height: NaN, bitRate: -1, sizeBytes: Infinity}], env)[0]!.name), ['Version 1']);
  assert.throws(() => versionModels([{id: 'a'}, {id: 'a'}], env), /duplicate_version_identity/);
});

const file: FileFacts = {id: 'file-real', available: true, height: 1080, width: 1920, videoCodec: 'h264', container: 'mkv', durationSeconds: 9960, sizeBytes: 1024 ** 3, bitRate: 12_000_000, frameRate: 23.976, bitDepth: 10, audio: [{id: 'a', resourceId: 'audio-real', language: 'en', codec: 'aac', channels: 6}], subtitles: [{id: 's', resourceId: 'subtitle-real', language: 'en', forced: true}]};
test('File info contains ordered technical rows and no page/picker or viewer ownerfacts', () => {
  const secretFacts = {locations: [{id: 'file-real', path: '/private/media/secret.mkv', canDelete: true}], canSplit: true, canMerge: true};
  const input = {...file, path: '/private/also-secret.mkv', ownerFacts: secretFacts};
  const access = {audience: 'viewer', ownerFacts: secretFacts} as const;
  const model = fileInfoModel([input], access, env);
  assert.deepEqual(model.files[0]!.rows.map(r => [r.id, texts(r.parts)]), [['resolution', ['1920 × 1080']], ['video', ['H.264', '10-bit']], ['container', ['MKV']], ['size', ['1 GB']], ['length', ['2 h 46 min']], ['bitrate', ['12 Mbps']], ['frame-rate', ['23.976 fps']]]);
  assert.equal(model.heading, 'File info'); assert.equal('owner' in model, false);
  assert.doesNotMatch(JSON.stringify(model), /private|secret|path|manage|Delete|Split|picker/);
  assert.equal(model.files[0]!.audio[0]!.resourceId, 'audio-real'); assert.equal(model.files[0]!.subtitles[0]!.resourceId, 'subtitle-real');
});
test('owner path/manage projection is explicit and authorized; TV has no manage actions', () => {
  const ownerFacts = {locations: [{id: 'file-real', path: '/private/movie.mkv', canDelete: true}, {id: 'unrelated', path: '/private/unrelated.mkv', canDelete: true}], canSplit: true, canMerge: true};
  const model = fileInfoModel([file], {audience: 'owner', ownerFacts, manageSurface: 'phone'}, env);
  assert.deepEqual(model.owner, {heading: 'Location and manage', locations: [{id: 'file-real', versionId: 'file-real', path: '/private/movie.mkv', actions: [{id: 'delete-version', label: 'Delete this version…'}]}], actions: [{id: 'split-versions', label: 'Split versions'}, {id: 'merge-title', label: 'Merge with another title…'}]});
  const tv = fileInfoModel([file], {audience: 'owner', ownerFacts}, env);
  assert.deepEqual(tv.owner!.actions, []); assert.deepEqual(tv.owner!.locations[0]!.actions, []);
  assert.equal('owner' in fileInfoModel([file], {audience: 'owner'}, env), false);
});
test('unknown file metadata has no invented facts, clocks, codecs, or availability', () => {
  const model = fileInfoModel([{id: 'unknown', available: false, height: NaN, width: -1, sizeBytes: 0, durationSeconds: -1, bitRate: Infinity, frameRate: NaN, bitDepth: 0, audio: [], subtitles: []}], {audience: 'viewer'}, env);
  assert.deepEqual(model.files[0]!.rows, []); assert.equal(model.files[0]!.available, false);
});

test('Playback info retains row order and structured conversion from/to; unknown reasons are omitted', () => {
  const facts: PlaybackFacts = {decision: {video: {action: 'transcode', reasons: ['video_codec_unsupported', 'not_known', 'subtitle_burn_in'] , to: 'h264'}, audio: {action: 'transcode', reasons: ['audio_codec_unsupported', 'subtitle_image_needs_burn'], to: 'aac', channels: 2}}, source: {container: 'mkv', videoCodec: 'hevc', audioCodec: 'truehd', width: 3840, height: 2160}, shown: {width: 1920, height: 1080}, bitrateKbps: 8000, hdr: {source: 'Dolby Vision', delivered: 'SDR', toneMapped: true}, droppedFrames: 0, totalFrames: 100, bufferSeconds: 12.4, subtitleOrigin: {kind: 'sidecar'}};
  const rows = playbackInfoModel(facts, 'viewer', env);
  assert.deepEqual(rows.map(r => r.id), ['delivery', 'why', 'video', 'hdr', 'audio', 'container', 'bitrate', 'dropped', 'buffer', 'subtitle-origin']);
  assert.deepEqual(rows[1]!.parts.map(p => p.role), ['reason', 'reason', 'reason']);
  assert.deepEqual(texts(rows[1]!.parts), ['This device needs a different video format.', 'These subtitles need to be drawn into the picture.', 'This device needs a different audio format.']);
  assert.deepEqual(rows.find(r => r.id === 'video'), {id: 'video', label: 'Video', parts: [], from: [{id: 'codec', text: 'HEVC', role: 'value'}, {id: 'dimensions', text: '3840 × 2160', role: 'value'}], to: [{id: 'codec', text: 'H.264', role: 'value'}, {id: 'dimensions', text: '1920 × 1080', role: 'value'}]});
  assert.deepEqual(texts(rows.find(r => r.id === 'audio')!.to!), ['AAC', '2 channels']);
  assert.deepEqual(texts(rows.find(r => r.id === 'dropped')!.parts), ['0 of 100']);
  assert.deepEqual(texts(rows.at(-1)!.parts), ['Beside the file']);
});
test('Playback info omits absent diagnostics instead of inventing original/dropped/unknown reason', () => {
  assert.deepEqual(playbackInfoModel({}, 'viewer', env), []);
  const unknown = playbackInfoModel({decision: {video: {action: 'transcode', reasons: ['unknown_reason']}}, totalFrames: 100, bufferSeconds: -1, bitrateKbps: NaN}, 'viewer', env);
  assert.deepEqual(unknown.map(r => r.id), ['delivery']);
  assert.equal(playbackInfoModel({delivery: 'repackaged'}, 'viewer', env)[0]!.parts[0]!.text, 'Repackaging');
  assert.equal(playbackInfoModel({delivery: 'direct'}, 'viewer', env)[0]!.parts[0]!.text, 'Playing the original file');
  assert.deepEqual(playbackInfoModel({shown: {width: 1920, height: 1080}, droppedFrames: NaN, totalFrames: Infinity}, 'viewer', env), [{id: 'video', label: 'Video', parts: [{id: 'shown-dimensions', text: '1920 × 1080', role: 'value'}], from: [], to: []}]);
});
test('subtitle origins require genuinely supplied contracts and do not infer from tracks', () => {
  assert.deepEqual(texts(playbackInfoModel({subtitleOrigin: {kind: 'provider', name: 'Provider · Original'}}, 'viewer', env)[0]!.parts), ['Provider · Original']);
  assert.deepEqual(playbackInfoModel({subtitleOrigin: {kind: 'provider', name: ''}}, 'viewer', env), []);
});
test('members never receive away-limit wording in reasons or quality footer', () => {
  const facts = {decision: {video: {action: 'transcode', reasons: ['admin_cap_remote_bitrate']}}};
  assert.deepEqual(playbackInfoModel(facts, 'viewer', env).map(r => r.id), ['delivery']);
  assert.equal(playbackInfoModel(facts, 'owner', env).find(r => r.id === 'why')!.parts[0]!.text, 'The server limits quality away from home.');
  assert.deepEqual(qualityControlModel({choice: {kind: 'automatic'}, policy: {network: 'away', maxHeight: 1080}}, 'viewer', env).footer, []);
});

test('admin session facts retain viewer/device/state/position/delivery/location/rate/start order', () => {
  const base = {profile: 'Alex', user: 'Account', device: 'Living room TV', state: 'paused', kind: 'vod', positionMs: 754000, durationMs: 3600000, decision: {video: {action: 'direct'}}, location: 'local', bandwidthKbps: 8000, bitrateKbps: 12000, startedAt: '2026-10-21T15:00:00Z'} as const;
  const parts = sessionFactParts(base, env);
  assert.deepEqual(parts.map(p => [p.id, p.text, p.role]), [['viewer', 'Alex', 'identity'], ['account', 'Account', 'context'], ['device', 'on Living room TV', 'context'], ['state', 'Paused', 'status'], ['position', '12:34 of 1:00:00', 'supplement'], ['delivery', 'Playing the original file', 'status'], ['location', 'Home network', 'context'], ['bitrate', '8.0 Mbps', 'supplement'], ['started', 'Started 3:00 PM', 'supplement']]);
  assert.equal(sessionFactParts({...base, kind: 'channel'}, env).some(p => p.id === 'position'), false);
  assert.equal(sessionFactParts({...base, kind: 'audio', durationMs: undefined}, env).find(p => p.id === 'position')!.text, 'At 12:34');
  assert.equal(sessionFactParts({...base, startedAt: 'bad', bandwidthKbps: Infinity, bitrateKbps: -1}, env).some(p => p.id === 'started' || p.id === 'bitrate'), false);
  assert.deepEqual(sessionFactParts({kind: 'channel'}, env), []);
  assert.equal(sessionFactParts({...base, startedAt: '2026-10-21T15:00:59.999Z'}, env).at(-1)!.text, 'Started 3:00 PM');
});
test('session zero is real while negative/nonfinite/unknown position is omitted', () => {
  assert.equal(sessionFactParts({kind: 'audio', positionMs: 0}, env)[0]!.text, 'At 0:00');
  for (const positionMs of [undefined, -1, NaN, Infinity]) assert.deepEqual(sessionFactParts({kind: 'vod', positionMs}, env), []);
});

test('Auto 1080p uses observed picture; ceiling/source resolution is never current quality', () => {
  const model = qualityControlModel({choice: {kind: 'automatic'}, observedHeight: 1080, policy: {network: 'home', maxHeight: 2160}}, 'viewer', env);
  assert.equal(model.label, 'Auto 1080p'); assert.equal(model.optionName, 'Automatic');
  assert.deepEqual(model.parts, [{id: 'mode', text: 'Auto', role: 'context'}, {id: 'observed-quality', text: '1080p', role: 'value'}]);
  assert.deepEqual(texts(model.detail), ['1080p now, adjusts to your connection']);
  assert.equal(qualityControlModel({choice: {kind: 'automatic'}, observedHeight: 720, policy: {network: 'away', maxHeight: 1080}}, 'viewer', env).label, 'Auto 720p');
  const unknown = {choice: {kind: 'automatic'}, sourceHeight: 2160, originalHeight: 2160, policy: {network: 'away', maxHeight: 1080}} as const;
  assert.equal(qualityControlModel(unknown, 'viewer', env).label, 'Auto');
  for (const observedHeight of [0, -1, NaN, Infinity, 0.5, 1079.7]) assert.equal(qualityControlModel({...unknown, observedHeight}, 'viewer', env).label, 'Auto');
});
test('quality supports Original and fixed choices; owners alone get away-limit explanation', () => {
  assert.equal(qualityControlModel({choice: {kind: 'original'}, observedHeight: 1080}, 'viewer', env).label, 'Original');
  assert.equal(qualityControlModel({choice: {kind: 'fixed', height: 720}, observedHeight: 1080}, 'viewer', env).label, '720p');
  assert.equal(qualityControlModel({choice: {kind: 'fixed'}}, 'viewer', env).label, '');
  assert.deepEqual(texts(qualityControlModel({choice: {kind: 'automatic'}, policy: {network: 'away', maxHeight: 1080}}, 'owner', env).footer), ['Away from home, up to 1080p']);
  assert.deepEqual(qualityControlModel({choice: {kind: 'automatic'}, policy: {network: 'unknown', maxHeight: 1080}}, 'owner', env).footer, []);
});
