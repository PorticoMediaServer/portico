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
