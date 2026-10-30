import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryAdminDocument} from '../../../packages/client-core/src/administration';
import type {SettingsRow} from '../../../packages/client-core/src/presentation/settings-spec';
import {libraryListState, libraryNumberValue, libraryRowOptions, libraryRowVisible} from '../src-next/screens/settings-live/owner-libraries/library-settings-model.ts';

test('empty libraries is shown only after a successful directory read', () => {
  assert.equal(libraryListState('loading', 0), 'loading');
  assert.equal(libraryListState('idle', 0), 'loading');
  assert.equal(libraryListState('error', 0), 'none');
  assert.equal(libraryListState('ready', 0), 'empty');
  assert.equal(libraryListState('loading', 3), 'rows');
});

test('kind filters cannot grant a library control absent from producer eligibility', () => {
  assert.equal(libraryRowVisible('libraries.collections', {libraryKind: 'movie', eligibleControls: []}), false);
  const eligibleControls = ['libraries.collections', 'libraries.lyrics', 'libraries.acoustid', 'libraries.episodeNumbering'];
  assert.equal(libraryRowVisible('libraries.collections', {libraryKind: 'movie', eligibleControls}), true);
  assert.equal(libraryRowVisible('libraries.collections', {libraryKind: 'tv', eligibleControls}), false);
  assert.equal(libraryRowVisible('libraries.lyrics', {libraryKind: 'music', eligibleControls}), true);
  assert.equal(libraryRowVisible('libraries.acoustid', {libraryKind: 'audiobook', eligibleControls}), false);
  assert.equal(libraryRowVisible('libraries.episodeNumbering', {libraryKind: 'anime', eligibleControls}), true);
});

test('provider choices respect the actual media-kind capabilities and local-tag vocabulary', () => {
  const document: Pick<LibraryAdminDocument, 'libraryKind' | 'availableProviders'> = {libraryKind: 'anime', availableProviders: [{id: 'tmdb', name: 'TMDB', mediaKinds: ['movie'], supportsApiKey: true, supportsLocale: true, attributionNote: ''}, {id: 'anilist', name: 'AniList', mediaKinds: ['show'], supportsApiKey: false, supportsLocale: false, attributionNote: ''}]};
  const providers: SettingsRow = {id: 'libraries.titlesFrom', label: 'profile.name', control: {type: 'select', options: [{value: 'tmdb', label: 'profile.name'}, {value: 'anilist', label: 'profile.name'}, {value: 'local', label: 'profile.name'}]}};
  assert.deepEqual(libraryRowOptions(providers, document).map(option => option.value), ['anilist']);
  const local: SettingsRow = {id: 'libraries.localTags', label: 'profile.name', control: {type: 'select', options: [{value: 'prefer', label: 'profile.name'}, {value: 'supplement', label: 'profile.name'}, {value: 'off', label: 'profile.name'}]}};
  assert.deepEqual(libraryRowOptions(local, document).map(option => option.value), ['prefer', 'off']);
  assert.equal(libraryRowOptions(local, {...document, libraryKind: 'music'}).length, 3);
});

test('preview values reject blanks, fractional steps, overflow and out-of-range input', () => {
  const width = {control: {type: 'number', min: 120, max: 640, step: 40}} as SettingsRow;
  for (const value of ['', '121', '-1', '641', '200.5', '1e3', 'Infinity']) assert.equal(libraryNumberValue(value, width), undefined, value);
  assert.equal(libraryNumberValue('320', width), 320);
  assert.equal(libraryNumberValue(' 640 ', width), 640);
});
