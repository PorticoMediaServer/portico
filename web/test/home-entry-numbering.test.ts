import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n} from '../../packages/i18n/src/index.ts';
import type {ContentEntry} from '../../packages/client-core/src/library-content.ts';
import {contentEntrySubjectVectors} from '../../packages/client-core/test/fixtures/content-entry-subject.ts';
import {homeCardModel, homeCardSubject} from '../src/screens/home/model.ts';

const episode: ContentEntry = {id: 'episode', kind: 'episode', title: 'Actual title', show: {id: 'show', name: 'Actual show'}, seasonNumber: 2, episodeNumber: 3};
const env = {t: createI18n().t, locale: 'en-US'};
test('explicit date-based numbering wins over seasonal, absolute and special facts without inventing a date', () => {
  const dateBased = {...episode, dateBased: true, airDate: '2026-10-23', absoluteNumber: 77, special: true};
  assert.deepEqual(homeCardSubject(dateBased).episode, {mode: 'date', day: '2026-10-23'});
  const absent = homeCardSubject({...dateBased, airDate: undefined});
  assert.deepEqual(absent.episode, {mode: 'date', day: undefined});
  const caption = homeCardModel({...dateBased, airDate: undefined}, false, env).caption;
  assert.ok(!caption.some(part => part.id === 'episode-label'));
});
test('absolute, date fallback, explicit special and seasonal modes retain the actual server facts in order', () => {
  assert.deepEqual(homeCardSubject({...episode, absoluteNumber: 77, special: true}).episode, {mode: 'absolute', number: 77});
  assert.deepEqual(homeCardSubject({...episode, episodeNumber: undefined, airDate: '2026-10-23', special: true}).episode, {mode: 'date', day: '2026-10-23'});
  assert.deepEqual(homeCardSubject({...episode, special: true}).episode, {mode: 'special'});
  assert.deepEqual(homeCardSubject({...episode, seasonNumber: 0}).episode, {mode: 'special'});
  assert.deepEqual(homeCardSubject(episode).episode, {mode: 'seasonal', season: 2, episode: 3});
  assert.equal(homeCardSubject({...episode, seasonNumber: undefined}).seasonNumber, undefined);
  assert.deepEqual(homeCardSubject({...episode, kind: 'season', seasonNumber: 0}).episode, undefined);
  assert.equal(homeCardSubject({...episode, kind: 'season', seasonNumber: 0}).seasonNumber, 0);
});

for (const vector of contentEntrySubjectVectors) test(`shared Home subject: ${vector.name}`, () => {
  assert.deepEqual(homeCardSubject(vector.entry), vector.subject);
});
