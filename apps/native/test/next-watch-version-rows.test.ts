import test from 'node:test';
import assert from 'node:assert/strict';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeVersionChoices, nativeVersionNames} from '../src-next/screens/watch/version-options.ts';
const audio = (id: string, extra = {}) => ({id, codec: 'ac3', layout: '5.1', ...extra});
const version = (id: string, extra = {}) => ({id, label: 'Never a row label', container: 'mkv', bitrateKbps: 80000, video: [{id: 'picture', codec: 'hevc', width: 3840, height: 2160, hdr: 'dolby_vision'}], audio: [audio('first')], ...extra});
const options = (versions: unknown[]) => parseOptions({itemId: 'movie', versions});
const names = nativeVersionNames(defaultI18n.t, 'en-US');

test('native version rows use conditional two-line file-default facts and no generic tags', () => {
  const offered = options([
    version('edition', {edition: 'Director’s Cut', sizeBytes: 62500000000, audio: [audio('first'), audio('file-default', {codec: 'truehd', layout: '7.1', atmos: true, default: true, language: 'en', title: 'Not a format label'})]}),
    version('picture', {audio: [audio('first'), audio('later', {codec: 'aac', layout: 'stereo'})]}),
  ]);
  const rows = nativeVersionChoices(offered, 'movie', names);
  assert.deepEqual(rows, [
    {id: 'edition', heading: 'Director’s Cut', detail: '4K Dolby Vision, Dolby TrueHD with Atmos 7.1, 58.2 GB'},
    {id: 'picture', heading: '4K Dolby Vision', detail: 'Dolby Digital 5.1'},
  ]);
  assert.deepEqual(rows.map(row => Object.keys(row)), [['id', 'heading', 'detail'], ['id', 'heading', 'detail']]);
});

test('failed offered version exclusion can leave one row while a duplicate or wrong item remains invalid', () => {
  const offered = options([version('first'), version('second')]);
  assert.deepEqual(nativeVersionChoices(offered, 'movie', names, 'first').map(row => row.id), ['second']);
  assert.equal(nativeVersionChoices(offered, 'other', names).length, 0);
  assert.equal(nativeVersionChoices(options([version('same'), version('same')]), 'movie', names).length, 0);
});

test('each version uses its first actual default, otherwise its first offered stream, with locale-aware size only', () => {
  const offered = options([version('v', {edition: 'Edition', sizeBytes: 62500000000, audio: [audio('first', {default: true}), audio('second', {default: true, codec: 'aac'})]})]);
  const row = nativeVersionChoices(offered, 'movie', nativeVersionNames(defaultI18n.t, 'de-DE'))[0]!;
  assert.equal(row.heading, 'Edition'); assert.equal(row.detail, '4K Dolby Vision, Dolby Digital 5.1, 58,2 GB');
});
