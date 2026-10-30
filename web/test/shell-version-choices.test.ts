import test from 'node:test';
import assert from 'node:assert/strict';
import type {AudioStream, MediaVersion} from '@core/playback-v1/types.ts';
import {defaultI18n} from '@i18n';
import {canonicalVersionChoiceFormatter, versionChoiceRows, type VersionChoiceFormatter} from '../src/shell/version-choices.ts';

const audio = (id: string, isDefault = false): AudioStream => Object.freeze({id, codec: 'actual-codec', default: isDefault, atmos: false, commentary: false, visualImpaired: false});
const version = (id: string, values: Partial<MediaVersion> = {}): MediaVersion => Object.freeze({id, label: 'Not a chooser label', durationMs: 0, parts: [], video: [], audio: [], subtitles: [], ...values});
function formatters(pictures: Readonly<Record<string, string>> = {}, sounds: Readonly<Record<string, string>> = {}) {
  const seen: {pictures: MediaVersion[]; audio: AudioStream[]; sizes: number[]} = {pictures: [], audio: [], sizes: []};
  const formatter: VersionChoiceFormatter = {
    pictureName(value) {seen.pictures.push(value);return pictures[value.id];},
    audioFormatName(value) {seen.audio.push(value);return sounds[value.id];},
    formatBytes(value) {seen.sizes.push(value);return `${value} locale bytes`;},
  };
  return {formatter, seen};
}

test('each row names the first marked file default, not another version or viewer choice', () => {
  const first = audio('first'), marked = audio('marked', true), laterMarked = audio('later-marked', true);
  const otherDefault = audio('other-default', true);
  const versions = [version('a', {audio: [first, marked, laterMarked]}), version('b', {audio: [otherDefault, marked]})];
  const {formatter, seen} = formatters({}, {marked: 'Dolby TrueHD with Atmos 7.1', 'other-default': 'AAC stereo'});
  const rows = versionChoiceRows(versions, formatter);
  assert.deepEqual(seen.audio, [marked, otherDefault]);
  assert.deepEqual(rows.map(row => row.detail), ['Dolby TrueHD with Atmos 7.1', 'AAC stereo']);
});

test('without a marked default, use the first actual stream in its supplied order', () => {
  const first = audio('stream-z'), second = audio('stream-a');
  const {formatter, seen} = formatters({}, {'stream-z': 'First file stream'});
  assert.equal(versionChoiceRows([version('v', {audio: [first, second]})], formatter)[0]?.detail, 'First file stream');
  assert.deepEqual(seen.audio, [first]);
});

test('edition is the heading; picture, audio and actual locale size remain in the second line', () => {
  const file = version('cut', {edition: 'Director’s Cut', audio: [audio('sound')], sizeBytes: 58_200_000_000});
  const {formatter, seen} = formatters({cut: '4K Dolby Vision'}, {sound: 'Dolby TrueHD with Atmos 7.1'});
  assert.deepEqual(versionChoiceRows([file], formatter), [{id: 'cut', heading: 'Director’s Cut', detail: '4K Dolby Vision, Dolby TrueHD with Atmos 7.1, 58200000000 locale bytes'}]);
  assert.equal(seen.pictures[0], file);
  assert.deepEqual(seen.sizes, [58_200_000_000]);
});

test('without an edition, picture is only in the heading and producer order is unchanged', () => {
  const files = Object.freeze([version('z', {audio: [audio('sound')], sizeBytes: 0}), version('a')]);
  const {formatter} = formatters({z: '1080p', a: '720p'}, {sound: 'AAC stereo'});
  const rows = versionChoiceRows(files, formatter);
  assert.deepEqual(rows, [{id: 'z', heading: '1080p', detail: 'AAC stereo, 0 locale bytes'}, {id: 'a', heading: '720p', detail: ''}]);
  assert.equal(Object.isFrozen(rows), true);assert.equal(Object.isFrozen(rows[0]), true);
  assert.deepEqual(files.map(file => file.id), ['z', 'a']);
});

test('unknown or empty facts are omitted without generic labels, track titles or language prefixes', () => {
  const actualTrack = {...audio('unknown'), title: 'Viewer language label', language: 'en'};
  const {formatter, seen} = formatters({empty: ' '}, {unknown: ''});
  assert.deepEqual(versionChoiceRows([version('empty', {edition: ' ', audio: [actualTrack]}), version('absent')], formatter), [
    {id: 'empty', heading: '', detail: ''}, {id: 'absent', heading: '', detail: ''},
  ]);
  assert.equal(seen.audio[0], actualTrack);assert.equal(seen.audio.length, 1);assert.deepEqual(seen.sizes, []);
});

test('the canonical formatter controls exact names and locale bytes without adapter spelling changes', () => {
  const {formatter} = formatters({localized: '4K HDR10+'}, {sound: 'DTS-HD Master Audio 5.1'});
  const localized: VersionChoiceFormatter = {...formatter, formatBytes: () => '58,2 Go'};
  assert.deepEqual(versionChoiceRows([version('localized', {edition: 'Édition cinéma', audio: [audio('sound')], sizeBytes: 58_200_000_000})], localized), [
    {id: 'localized', heading: 'Édition cinéma', detail: '4K HDR10+, DTS-HD Master Audio 5.1, 58,2 Go'},
  ]);
});

test('the actual MAIN technical names bind without copying their dictionaries or introducing track language', () => {
  const file = version('actual-group', {edition: 'Director’s Cut', sizeBytes: Math.round(58.2 * 1024 ** 3),
    video: [{id: 'video', codec: 'hevc', width: 3840, height: 2160, hdr: 'dolbyvision'}],
    audio: [{...audio('first'), codec: 'aac', layout: 'stereo'}, {...audio('file-default', true), codec: 'truehd', atmos: true, layout: '7.1', language: 'ja', title: 'Not the row label'}]});
  const rows = versionChoiceRows([file], canonicalVersionChoiceFormatter(defaultI18n.t, 'en-US'));
  assert.deepEqual(rows, [{id: file.id, heading: 'Director’s Cut', detail: '4K Dolby Vision, Dolby TrueHD with Atmos 7.1, 58.2 GB'}]);
});

test('actual locale bytes and file-default fallback preserve conditional rows and unknown scan', () => {
  const file = version('actual-group', {sizeBytes: Math.round(58.2 * 1024 ** 3), video: [{id: 'video', codec: 'h264', width: 1920, height: 1080}],
    audio: [{...audio('first'), codec: 'dts', variant: 'dts_hd_ma', dtsx: true, layout: '7.1'}, {...audio('other'), codec: 'aac', layout: 'stereo'}]});
  const rows = versionChoiceRows([file], canonicalVersionChoiceFormatter(defaultI18n.t, 'de-DE'));
  assert.deepEqual(rows, [{id: file.id, heading: '', detail: 'DTS-HD Master Audio 7.1 (DTS:X), 58,2 GB'}]);
  assert.doesNotMatch(rows[0]!.heading, /1080p|1080i/);
});
