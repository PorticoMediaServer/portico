import test from 'node:test';
import assert from 'node:assert/strict';
import {formatMessage, messageArguments} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {createI18n, enUS} from '../../i18n/src/index.ts';
import {fileInfoModel, audioTrackModels, subtitleTrackModels, versionModels, qualityControlModel, type FileFacts, type FactPart, type KitTranslate} from '../src/presentation/kit-model/index.ts';

const t: KitTranslate = (id, ...args) => formatMessage(shared[id], args[0]);
const env = {t, locale: 'en-US'};
const texts = (parts: readonly FactPart[]) => parts.map(p => p.text);
const text = (parts: readonly FactPart[]) => texts(parts).join('');
const file = (facts: Partial<FileFacts> = {}): FileFacts => ({id: 'source', available: true, audio: [], subtitles: [], ...facts});
const model = (facts: Partial<FileFacts> = {}, context: 'desktop' | 'phone' = 'desktop') => fileInfoModel([file(facts)], {audience: 'viewer'}, env, context).files[0]!;

test('cropped cinema keeps actual dimensions and gets a distinct horizontal source class', () => {
  const facts = [file({id: 'cinema-uhd', width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'hdr10'}), file({id: 'cinema-hd', width: 1920, height: 804, videoCodec: 'h264'})];
  const result = fileInfoModel(facts, {audience: 'viewer'}, env);
  assert.deepEqual(result.files.map(f => [f.id, f.display.heading, texts(f.display.summary)]), [
    ['cinema-uhd', {id: 'version', text: 'Version 1', role: 'identity'}, ['4K', 'HDR10']],
    ['cinema-hd', {id: 'version', text: 'Version 2', role: 'identity'}, ['1080p']],
  ]);
  assert.deepEqual(result.files.map(f => texts(f.rows[0]!.parts)), [['3840 × 1608'], ['1920 × 804']]);
  assert.deepEqual(result.files.map(f => texts(f.version.name)), [['1608p', 'HEVC', 'HDR10'], ['804p', 'H.264']]);
  assert.deepEqual(result.files.map(f => f.version), versionModels(facts, env));
});

for (const [width, expected] of [[1279, '536p'], [1280, '720p'], [1919, '720p'], [1920, '1080p'], [2559, '1080p'], [2560, '1440p'], [3839, '1440p'], [3840, '4K'], [4096, '4K'], [7679, '4K'], [7680, '8K']] as const) {
  test(`source width class boundary ${width}`, () => {
    const result = model({width, height: 536});
    assert.deepEqual(texts(result.display.summary), [expected]);
    assert.deepEqual(texts(result.rows[0]!.parts), [`${width} × 536`]);
  });
}

test('sparse, invalid and height-only sources retain truthful omission or actual source height', () => {
  assert.deepEqual(texts(model({height: 804}).display.summary), ['804p']);
  assert.deepEqual(texts(model({width: 3840}).display.summary), ['4K']);
  assert.deepEqual(model({width: 3840}).rows, []);
  for (const width of [0, -1920, NaN, Infinity, 1919.5]) {
    assert.deepEqual(texts(model({width, height: 804}).display.summary), ['804p']);
    assert.deepEqual(model({width, height: 804}).rows, []);
  }
  for (const height of [undefined, 0, -1, NaN, Infinity, 804.5]) {
    const result = model({width: -1, height});
    assert.deepEqual(result.display.summary, []);
    assert.deepEqual(result.rows, []);
  }
  const result = model({available: false});
  assert.equal(result.available, false);
  assert.deepEqual(result.display, {heading: {id: 'version', text: 'Version 1', role: 'identity'}, summary: [], video: [], audio: [], subtitles: []});
});

test('desktop whole video and audio parts follow approved ordering with compact layouts', () => {
  const source = file({width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'hdr10', bitDepth: 10, bitRate: 52_000_000, container: 'mkv', audio: [
    {id: 'truehd', language: 'eng', codec: 'truehd', channels: 8, objectAudio: 'atmos'},
    {id: 'ac3', language: 'en', codec: 'ac3', channels: 6},
    {id: 'french', language: 'fr', codec: 'eac3', channelLayout: '7.1(side)', channels: 2},
    {id: 'spanish', language: 'es', codec: 'eac3', channels: 8},
    {id: 'stereo', language: 'en', codec: 'aac', channels: 2},
  ]});
  const result = model(source);
  assert.deepEqual(texts(result.display.video), ['HEVC', 'HDR10', '10-bit', '52 Mbps', 'MKV']);
  assert.deepEqual(result.display.audio.map(a => [a.id, texts(a.parts)]), [
    ['truehd', ['English', 'Dolby TrueHD', '7.1', 'Atmos']],
    ['ac3', ['English', 'Dolby Digital', '5.1']],
    ['french', ['French', 'Dolby Digital Plus', '7.1']],
    ['spanish', ['Spanish', 'Dolby Digital Plus', '7.1']],
    ['stereo', ['English', 'AAC', 'Stereo']],
  ]);
  assert.deepEqual(result.audio, audioTrackModels(source.audio, env));
  assert.deepEqual(texts(result.audio[0]!.detail), ['7.1 surround']);
});

test('phone profile, HDR, bitrate and audio commas are complete canonical parts', () => {
  const source = file({width: 3840, height: 2160, videoCodec: 'hevc', videoProfile: 'Main 10', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 53_000_000, container: 'mkv', audio: [
    {id: 'truehd', language: 'en', codec: 'truehd', channels: 8},
    {id: 'ac3', language: 'en', codec: 'ac3', channels: 6},
  ]});
  const result = model(source, 'phone');
  assert.equal(text(result.display.video), 'HEVC Main 10, Dolby Vision, 53 Mbps');
  assert.deepEqual(result.display.video.find(p => p.id === 'codec-profile'), {id: 'codec-profile', text: 'Main 10', role: 'value'});
  assert.deepEqual(result.display.audio.map(a => text(a.parts)), ['English, Dolby TrueHD 7.1', 'English, Dolby Digital 5.1']);
  assert.equal(text(model({videoCodec: 'h264', videoProfile: 'High', bitRate: 12_000_000}, 'phone').display.video), 'H.264 High, 12 Mbps');
  assert.deepEqual(texts(result.display.summary), ['4K', 'Dolby Vision']);
  assert.deepEqual(result.rows, model(source, 'desktop').rows);
  assert.deepEqual(result.version, model(source, 'desktop').version);
});

test('profile is genuinely optional and never inferred from codec or bit depth', () => {
  for (const videoCodec of ['hevc', 'h264', 'unrecognized(codec)']) for (const videoProfile of [undefined, '', '   ']) {
    for (const context of ['desktop', 'phone'] as const) {
      const result = model({videoCodec, bitDepth: 10, videoProfile}, context);
      assert.equal(result.display.video.some(p => p.id === 'codec-profile'), false);
      assert.doesNotMatch(text(result.display.video), /Main|High/);
    }
  }
  assert.equal(text(model({videoProfile: 'High', bitRate: 12_000_000}, 'phone').display.video), '12 Mbps');
  const profile = 'Provider Main (10), original · v2';
  assert.equal(model({videoCodec: 'provider.v1', videoProfile: profile}).display.video.find(p => p.id === 'codec-profile')!.text, profile);
  assert.deepEqual(texts(model({videoCodec: 'h264', videoProfile: 'High'}).rows[0]!.parts), ['H.264']);
});

test('subtitle list keeps language, compact SDH and catalogue punctuation without technical clutter', () => {
  const source = file({subtitles: [
    {id: 'en', language: 'en', format: 'srt', default: true},
    {id: 'sdh', language: 'eng', title: 'English (SDH)', hearingImpaired: true, format: 'srt'},
    {id: 'fr', language: 'fr', title: 'Français', format: 'srt', external: true},
    {id: 'es', language: 'es', format: 'srt'},
  ]});
  for (const context of ['desktop', 'phone'] as const) {
    const result = model(source, context);
    assert.equal(text(result.display.subtitles), 'English, English SDH, French, Spanish');
    assert.equal(new Set(result.display.subtitles.map(p => p.id)).size, result.display.subtitles.length);
    assert.deepEqual(result.subtitles, subtitleTrackModels(source.subtitles, env));
    assert.deepEqual(texts(result.subtitles[1]!.detail), ['For deaf and hard of hearing viewers']);
    assert.doesNotMatch(text(result.display.subtitles), /SRT|Default|External/);
  }
});

test('unknown languages, custom titles, forced and commentary stay honest in compact context', () => {
  const result = model({audio: [
    {id: 'custom', language: 'en', title: 'Director’s commentary · restored', commentary: true, codec: 'provider.audio(v2)', channels: 1},
    {id: 'unknown', language: 'und', channels: NaN},
    {id: 'count', language: 'es-419', channels: 3},
  ], subtitles: [
    {id: 'forced', language: 'en', title: 'English - Forced', forced: true},
    {id: 'custom', language: 'en', title: 'English SDH restoration · original', hearingImpaired: true},
    {id: 'unknown', language: 'not a language'},
  ]}, 'phone');
  assert.deepEqual(result.display.audio.map(a => text(a.parts)), ['English, provider.audio(v2) Mono Director’s commentary · restored', 'Track 2', 'Spanish (Latin America), 3 channels']);
  assert.equal(text(result.display.subtitles), 'English Forced, English English SDH restoration · original SDH, Track 3');
});

test('punctuation respects the injected catalogue and never creates leading/trailing separators', () => {
  const alternate: KitTranslate = (id, ...args) => id === 'next.kit.fileInfo.comma' ? ' ; ' : id === 'next.kit.fileInfo.space' ? '_' : formatMessage(shared[id], args[0]);
  const result = fileInfoModel([file({videoCodec: 'hevc', videoProfile: 'Main 10', bitRate: 12_000_000, subtitles: [{id: '1', language: 'en', hearingImpaired: true}, {id: '2', language: 'fr'}]})], {audience: 'viewer'}, {t: alternate}, 'phone').files[0]!;
  assert.equal(text(result.display.video), 'HEVC_Main 10 ; 12 Mbps');
  assert.equal(text(result.display.subtitles), 'English_SDH ; French');
  assert.equal(text(model({bitRate: 12_000_000}, 'phone').display.video), '12 Mbps');
  assert.equal(text(model({dynamicRange: 'hdr10'}, 'phone').display.video), 'HDR10');
  assert.deepEqual(messageArguments(shared['next.kit.fileInfo.comma']), []);
  assert.deepEqual(messageArguments(shared['next.kit.fileInfo.space']), []);
});

test('new File info context copy is registered and works through the actual i18n entry', () => {
  const i18n = createI18n();
  for (const id of ['next.kit.fileInfo.sdh', 'next.kit.fileInfo.comma', 'next.kit.fileInfo.space', 'next.kit.fileInfo.8k'] as const) {
    assert.equal(enUS[id], shared[id]);
    assert.equal(i18n.t(id), shared[id]);
    assert.deepEqual(messageArguments(shared[id]), []);
  }
  const translate: KitTranslate = (id, ...args) => i18n.t(id, args[0]);
  const result = fileInfoModel([file({width: 7680, height: 3216, videoCodec: 'hevc', videoProfile: 'Main 10', bitRate: 53_000_000, subtitles: [{id: 's', language: 'en', hearingImpaired: true}]})], {audience: 'viewer'}, {t: translate}, 'phone').files[0]!;
  assert.deepEqual(texts(result.display.summary), ['8K']);
  assert.equal(text(result.display.video), 'HEVC Main 10, 53 Mbps');
  assert.equal(text(result.display.subtitles), 'English SDH');
});

test('unrelated player height, generic facts, identities, access and order are unchanged', () => {
  const source = file({id: 'file-1', name: 'Restoration · part I', width: 3840, height: 1608, videoProfile: 'Main 10', audio: [{id: 'a', resourceId: 'r', streamIndex: 3, renditionLabel: 'English · AAC · 5.1', language: 'en', channels: 6}], subtitles: [{id: 's', language: 'en', hearingImpaired: true}]});
  const result = fileInfoModel([source, file({id: 'file-2'})], {audience: 'viewer'}, env);
  assert.deepEqual(result.files.map(f => f.id), ['file-1', 'file-2']);
  assert.deepEqual(result.files[0]!.audio[0], audioTrackModels(source.audio, env)[0]);
  assert.equal(result.files[0]!.audio[0]!.renditionLabel, source.audio[0]!.renditionLabel);
  const quality = qualityControlModel({choice: {kind: 'automatic'}, observedHeight: 804, policy: {network: 'away', maxHeight: 2160}}, 'viewer', env);
  assert.equal(quality.label, 'Auto 804p');
  assert.doesNotMatch(JSON.stringify(quality), /4K|1080p|2160p/);
  const ownerFacts = {locations: [{id: source.id, path: '/private/source.mkv', canDelete: true}], canSplit: true};
  const owner = fileInfoModel([source], {audience: 'owner', ownerFacts, manageSurface: 'phone'}, env, 'phone');
  assert.equal(owner.owner!.locations[0]!.path, '/private/source.mkv');
  assert.doesNotMatch(JSON.stringify(result), /private|path|delete-version/);
  assert.throws(() => fileInfoModel([source, source], {audience: 'viewer'}, env), /duplicate_version_identity/);
  assert.throws(() => model({audio: [{id: 'duplicate'}, {id: 'duplicate'}]}), /duplicate_track_identity/);
  assert.ok(Object.isFrozen(result.files[0]!.display));
  assert.ok(Object.isFrozen(result.files[0]!.display.audio));
});
