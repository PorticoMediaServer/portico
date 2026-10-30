import assert from 'node:assert/strict';
import test from 'node:test';
import {createI18n} from '../../i18n/src/index.ts';
import {attentionCount, attentionCountText, attentionInProgress, attentionOffers, attentionReason, candidateFacts, candidateLine, hasAttentionList, typedProviderId} from '../src/server-admin/attention-words.ts';
import {artworkLimitBytes, artworkLimitText} from '../src/metadata-settings.ts';

const i18n = createI18n();
const t = i18n.t;
const has = (id: string) => i18n.has(id);
const entry = (reason: string) => ({entityId: reason, kind: 'item' as const, itemKind: 'movie', parentId: '', actions: ['dismiss', 'accept'], title: 'Ran', revision: 1, reason, confidence: 0.5, candidates: []});

test('a reason is a sentence, and one this app does not know is never shown as a code', () => {
  assert.equal(attentionReason(t, has, 'unmatched'), 'No close match was found, so it shows under its file name.');
  assert.equal(attentionReason(t, has, 'low_confidence'), 'There is one possible match. You can check it is the right one.');
  assert.equal(attentionReason(t, has, 'flux_capacitor'), 'This title needs a look.');
});

test('why a match was suggested is a few short facts, each once', () => {
  assert.deepEqual(candidateFacts(t, has, {reasons: ['exact_title_or_alias', 'release_year_agrees', 'media_kind_agrees', 'typed_identifier', 'validated_typed_identifier']}), ['Same title', 'Same year', 'Named by ID in your files']);
  assert.equal(candidateLine({title: 'The Matrix', year: 1999, identity: {provider: 'tmdb', type: 'movie', id: '603'}}), 'The Matrix (1999) · TMDB');
  assert.equal(candidateLine({title: 'Mushishi', year: 0, identity: {provider: 'anilist', type: 'anime', id: '457'}}), 'Mushishi · AniList');
});

test('the count leaves out work still in progress and says when there are more pages', () => {
  const page = {entries: [entry('unmatched'), entry('pending'), entry('searching'), entry('low_confidence')], nextCursor: ''};
  assert.deepEqual(attentionCount(page), {count: 2, more: false});
  assert.equal(attentionCountText(attentionCount(page), String), '2');
  assert.equal(attentionCountText(attentionCount({...page, nextCursor: 'abc'}), String), '2+');
  assert.equal(attentionCountText(attentionCount({entries: [entry('pending')], nextCursor: 'abc'}), String), '');
  assert.equal(attentionCountText(undefined, String), '');
  assert.equal(attentionInProgress('pending_children'), true);
  assert.equal(attentionInProgress('needs_selection'), false);
});

test('only the actions the server names are offered', () => {
  assert.equal(attentionOffers(entry('unmatched'), 'accept'), true);
  assert.equal(attentionOffers(entry('unmatched'), 'search'), false);
});

test('only film, TV and anime libraries have the list', () => {
  for (const kind of ['movie', 'tv', 'anime', 'music', 'audiobook']) assert.equal(hasAttentionList(kind), true, kind);
  for (const kind of ['home', '']) assert.equal(hasAttentionList(kind), false, kind);
});

test('a typed provider ID is trimmed and refused when it has spaces or is empty', () => {
  assert.equal(typedProviderId('  tt0133093 '), 'tt0133093');
  assert.equal(typedProviderId('https://www.themoviedb.org/movie/603-the-matrix'), 'https://www.themoviedb.org/movie/603-the-matrix');
  assert.equal(typedProviderId('603 604'), undefined);
  assert.equal(typedProviderId('   '), undefined);
});

test('the artwork limit is typed in gigabytes and stored in bytes', () => {
  assert.equal(artworkLimitText(2 * 2 ** 30), '2');
  assert.equal(artworkLimitText(1.5 * 2 ** 30), '1.5');
  assert.equal(artworkLimitText(2147483649), '2');
  assert.equal(artworkLimitBytes('2'), 2 * 2 ** 30);
  assert.equal(artworkLimitBytes(' 1,5 '), 1.5 * 2 ** 30);
  assert.equal(artworkLimitBytes('0.05'), undefined);
  assert.equal(artworkLimitBytes('lots'), undefined);
  assert.equal(artworkLimitBytes(''), undefined);
  assert.equal(artworkLimitBytes('2000000'), undefined);
});
