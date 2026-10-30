import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile, readdir} from 'node:fs/promises';
import {formatMessage, messageArguments, parseMessage} from '../../i18n/src/messageformat.ts';
import {shared} from '../../i18n/src/catalog/en-US/next/shared.ts';
import {cardText, titleFactParts, collectionCaptionParts, episodeLabel, playbackIdentity, appliedFilterParts, programmeFactParts, fileInfoModel, versionModels, audioTrackModels, subtitleTrackModels, playbackInfoModel, sessionFactParts, qualityControlModel, type KitTranslate, type KitMessageValues} from '../src/presentation/kit-model/index.ts';

test('all owned generated wording parses as ICU and uses sentence case without middle-dot joins', () => {
  for (const [id, message] of Object.entries(shared)) {
    assert.doesNotThrow(() => parseMessage(message), id);
    assert.doesNotMatch(message, /·|Portico Cloud|Ask Dana|\bLIVE\b/, id);
  }
  assert.equal(shared['next.kit.live'], 'Live');
  assert.equal(formatMessage(shared['next.kit.quality.autoNow'], {quality: '1080p'}), 'Auto 1080p');
  assert.equal(formatMessage(shared['next.kit.range'], {start: '3:30', end: '4:30 PM'}), '3:30 to 4:30 PM');
  assert.equal(shared['next.kit.sdh'], 'For deaf and hard of hearing viewers');
  assert.equal(formatMessage(shared['next.kit.episodeCode'], {season: 1, episode: 1043}), 'S1 E1043');
  assert.equal(formatMessage(shared['next.kit.songs'], {count: 1}), '1 song');
  assert.equal(formatMessage(shared['next.kit.songs'], {count: 4}), '4 songs');
  assert.equal(formatMessage(shared['next.kit.books'], {count: 1}), '1 book');
  assert.equal(formatMessage(shared['next.kit.books'], {count: 2}), '2 books');
  assert.deepEqual(messageArguments(shared['next.kit.collection.filmsOnServer']), ['count']);
  assert.deepEqual(messageArguments(shared['next.kit.collection.filmsFromTo']), ['count', 'title', 'year']);
  assert.deepEqual(messageArguments(shared['next.kit.genres.append']), ['genre', 'list']);
});

test('model translation calls supply exactly the declared ICU arguments', () => {
  const called = new Set<string>();
  const t: KitTranslate = (id, ...args) => {
    assert.ok(id in shared, id);
    assert.deepEqual(Object.keys(args[0] ?? {}).sort(), messageArguments(shared[id]), id);
    called.add(id);
    return formatMessage(shared[id], args[0]);
  };
  const env = {t, locale: 'en-US', timeZone: 'UTC', hourCycle: 'h12'} as const;
  titleFactParts({year: 2024, durationSeconds: 9960, seasonCount: 3}, env);
  for (const seconds of [1, 60, 3600, 3660]) titleFactParts({durationSeconds: seconds}, env);
  for (const kind of ['artist', 'category', 'album', 'book'] as const) cardText({id: 'a', kind, title: 'Title', artist: 'Artist', author: 'Author', narrator: 'Narrator', chapterNumber: 2, chapterCount: 3, songCount: 4, albumCount: 1, itemCount: 10000, countIsLowerBound: true, durationSeconds: 1000, positionSeconds: 60}, 'continuation', env);
  cardText({id: 'author', kind: 'author', title: 'Author', bookCount: 2}, 'ordinary', env);
  titleFactParts({author: 'Author', narrator: 'Narrator', durationSeconds: 1000, chapterCount: 3}, env, 'audiobook');
  titleFactParts({chapterNumber: 2, chapterCount: 3}, env, 'audiobook-chapters');
  titleFactParts({year: 2001, genres: ['Animation', 'Family'], durationSeconds: 7500}, env, 'home-movie');
  titleFactParts({year: 2023, genres: ['Drama'], seasonCount: 1}, env, 'home-show');
  collectionCaptionParts({mediaKind: 'movie', itemCount: 4}, env);
  collectionCaptionParts({mediaKind: 'movie', itemCount: 4, firstYear: 1995, lastTitle: 'Actual last title'}, env);
  episodeLabel({mode: 'absolute', number: 37}, 'in-show', env);
  episodeLabel({mode: 'seasonal', season: 1, episode: 2}, 'search', env);
  episodeLabel({mode: 'special'}, 'in-show', env);
  playbackIdentity({kind: 'channel', sourceId: 's', id: 'c', channelName: 'Channel', live: true}, 'dock', env);
  appliedFilterParts({field: 'decade', operator: 'between', value: [1990, 2000]}, String, env);
  appliedFilterParts({field: 'flag', operator: 'in', value: [true, false]}, String, env);
  programmeFactParts({startMs: 0, endMs: 3600_000, flags: {live: true, new: true, premiere: true}, recording: true}, env, 1800_000);
  versionModels([{id: 'a', height: 1080, bitRate: 1e6}, {id: 'b', height: 1080, bitRate: 2e6}], env);
  audioTrackModels([{id: 'a', channels: 6}, {id: 'b', channels: 2}, {id: 'c', channels: 1}, {id: 'd', channels: 3}], env);
  subtitleTrackModels([{id: 'a', language: 'en', hearingImpaired: true, forced: true}, {id: 'b', language: 'en', hearingImpaired: true, forced: true}], env);
  fileInfoModel([{id: 'f', available: true, width: 1920, height: 1080, videoCodec: 'h264', sizeBytes: 1000, bitRate: 800000, frameRate: 24, bitDepth: 10, durationSeconds: 3660, audio: [], subtitles: []}], {audience: 'viewer'}, env);
  playbackInfoModel({delivery: 'converted', chosenQuality: '720p', decision: {audio: {action: 'transcode', to: 'aac', channels: 2, reasons: ['audio_codec_unsupported']}}, totalFrames: 100, droppedFrames: 1, bufferSeconds: 5, hdr: {source: 'HDR10', toneMapped: true}}, 'viewer', env);
  sessionFactParts({kind: 'vod', positionMs: 1000, durationMs: 10000, device: 'TV', startedAt: '2026-10-21T12:00:00Z'}, env);
  qualityControlModel({choice: {kind: 'automatic'}, observedHeight: 1080, policy: {network: 'home', maxHeight: 2160}}, 'viewer', env);
  assert.ok(called.size > 45, `covered ${called.size} catalogue keys`);
});

test('new models have a strict renderer/endpoint/state-free import boundary', async () => {
  const directory = new URL('../src/presentation/kit-model/', import.meta.url);
  const names = await readdir(directory);
  // browse.ts orders its facets with the existing pure toolbar model. Keep that
  // dependency honest: it may not become a renderer, endpoint or state module.
  const toolbar = await readFile(new URL('../src/presentation/browse-toolbar.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(toolbar, /(?:from\s*|import\s*\()\s*['"][^'"]*(?:\.tsx|\.css|react|web\/|native\/|contracts\/)/, 'browse-toolbar.ts: pure model');
  assert.doesNotMatch(toolbar, /\b(?:fetch|XMLHttpRequest|WebSocket|setTimeout|setInterval|require)\s*\(|\b(?:document|window|process|localStorage|sessionStorage)\s*\.|Date\.now\s*\(/, 'browse-toolbar.ts: pure model');
  for (const name of names) {
    assert.match(name, /^[a-z-]+\.ts$/, name);
    const source = await readFile(new URL(name, directory), 'utf8');
    assert.doesNotMatch(source, /(?:from\s*|import\s*\()\s*['"][^'"]*(?:\.tsx|\.css|react|web\/|native\/|contracts\/)/, name);
    assert.doesNotMatch(source, /\b(?:fetch|XMLHttpRequest|WebSocket|setTimeout|setInterval|require)\s*\(|\b(?:document|window|process|localStorage|sessionStorage)\s*\.|Date\.now\s*\(|\/v[12]\//, name);
    assert.doesNotMatch(source, /\.join\(['"]\s*·\s*['"]\)|\.split\(['"]\s*·\s*['"]\)|\b(?:cardCaption|continueCaption|homeHero|titleFileLines|versionNames|mediaVersionNames|subtitleTrackNames|playbackInfoRows|appliedFilterLabel|toolbarModel|sessionFacts|audioTrackLabel|trackName)\s*\(/, name);
    for (const [, dependency] of source.matchAll(/(?:from\s+|import\s*\()\s*['"]([^'"]+)['"]/g)) {
      if ((name === 'filters.ts' || name === 'browse.ts') && dependency === '../../browse.ts') {
        assert.match(source, /^import type \{[^}]+\} from '\.\.\/\.\.\/browse\.ts';/m, `${name}: canonical browse data types only`);
        assert.equal([...source.matchAll(/['"]\.\.\/\.\.\/browse\.ts['"]/g)].length, 1, `${name}: no additional runtime import/reexport of browse`);
        continue;
      }
      assert.ok(/^\.\/[a-z-]+\.ts$/.test(dependency!) || name === 'tracks.ts' && dependency === '../language.ts' || name === 'text.ts' && dependency === '../../../../i18n/src/catalog/en-US/next/shared.ts' || (name === 'browse.ts' || name === 'filters.ts') && dependency === '../browse-toolbar.ts', `${name}: ${dependency}`);
    }
  }
  const language = await readFile(new URL('../src/presentation/language.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(language, /^(?:import|export).*from\s/m, 'scalar language identity dependency remains leaf-only');
});

// These compile-time obligations are exercised by the explicit test TypeScript check.
function translationTypes(t: KitTranslate): void {
  const values: KitMessageValues<'next.kit.episodeCode'> = {season: 1, episode: 2};
  t('next.kit.episodeCode', values);
  t('next.kit.live');
  t('next.kit.books', {count: 2});
  const collectionValues: KitMessageValues<'next.kit.collection.filmsFromTo'> = {count: 4, year: 1995, title: 'Actual last title'};
  t('next.kit.collection.filmsFromTo', collectionValues);
  t('next.kit.collection.filmsOnServer', {count: 4});
  t('next.kit.genres.append', {list: 'Drama', genre: 'Mystery'});
  // @ts-expect-error catalogue punctuation requires the next supplied genre
  t('next.kit.genres.append', {list: 'Drama'});
  // @ts-expect-error the range requires both genuine endpoints
  t('next.kit.collection.filmsFromTo', {count: 4, year: 1995});
  // @ts-expect-error a collection count is numeric, never the rendered count words
  t('next.kit.collection.filmsOnServer', {count: 'Four'});
  // @ts-expect-error the year remains a genuine ungrouped number
  t('next.kit.collection.filmsFromTo', {count: 4, year: '1995', title: 'Actual last title'});
  // @ts-expect-error a book count requires its genuine numeric count
  t('next.kit.books');
  // @ts-expect-error a book count cannot be an item-label string
  t('next.kit.books', {count: '2 items'});
  // @ts-expect-error an episode requires its number
  t('next.kit.episode');
  // @ts-expect-error height cannot be an arbitrary string
  t('next.kit.height', {height: '1080'});
  // @ts-expect-error no old keys enter the new semantic translator
  t('card.episodeCode', {season: 1, episode: 2});
}
void translationTypes;
