import test from 'node:test';
import assert from 'node:assert/strict';
import {cardText} from '../src/presentation/kit-model/index.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const env = {t: defaultI18n.t, locale: 'en-US'};
const show = {id: 'dark-matter', kind: 'show' as const, title: 'Dark Matter', year: 2024, seasonCount: 1};
const captions = (context: Parameters<typeof cardText>[1], subject: Parameters<typeof cardText>[0] = show) => cardText(subject, context, env).caption.map(part => part.text);

test('Search draws a show\'s year and season count, as desktop-search\'s Shows do (W2-A4)', () => {
  assert.deepEqual(captions('search'), ['2024', defaultI18n.t('next.kit.seasons', {count: 1})]);
  assert.deepEqual(captions('search', {...show, seasonCount: undefined}), ['2024'], 'only what is known');
});
test('elsewhere a published season count stands alone, and a show without one reads its year (Home)', () => {
  assert.deepEqual(captions('ordinary'), [defaultI18n.t('next.kit.seasons', {count: 1})]);
  assert.deepEqual(captions('ordinary', {...show, seasonCount: undefined}), ['2024']);
});
test('Saved names what each title is: "Show · 1 season", "Anime · 1 season", "1982 · Movie" (desktop-search Saved, W2-A13)', () => {
  const kind = (value: 'movie' | 'show' | 'anime') => defaultI18n.t('next.kit.kind', {kind: value});
  const seasons = defaultI18n.t('next.kit.seasons', {count: 1});
  assert.deepEqual(captions('saved', {...show, libraryKind: 'tv'}), [kind('show'), seasons]);
  assert.deepEqual(captions('saved', {...show, libraryKind: 'anime'}), [kind('anime'), seasons]);
  assert.deepEqual(captions('saved', {...show, seasonCount: undefined}), [kind('show')], 'only what is known');
  assert.deepEqual(captions('saved', {id: 'blade-runner', kind: 'movie', title: 'Blade Runner', year: 1982, libraryKind: 'anime'}), ['1982', kind('movie')], 'a film in an Anime library is a Movie');
  assert.deepEqual([kind('show'), kind('anime'), kind('movie')], ['Show', 'Anime', 'Movie']);
});
