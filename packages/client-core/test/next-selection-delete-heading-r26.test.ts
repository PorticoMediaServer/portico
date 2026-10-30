import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, messageArguments, resolveRegion, supportedLocales} from '../../i18n/src/index.ts';

const id = 'next.selection.native.deleteTitles';

test('count-based delete heading needs only count and preserves its arguments through regional fallback', () => {
  assert.deepEqual(messageArguments(enUS[id]), ['count']);
  for (const locale of supportedLocales) {
    const regional = catalogue(locale)[id];
    assert.deepEqual(messageArguments(regional ?? enUS[id]), ['count'], locale);
    if (locale !== 'en-US') assert.equal(regional, undefined, locale);
  }
});

test('delete heading uses singular at one and plural with formatted counts in every supported region', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    for (const [count, expected] of [
      [0, 'Delete 0 titles?'],
      [1, 'Delete 1 title?'],
      [2, 'Delete 2 titles?'],
      [4, 'Delete 4 titles?'],
      [1234, 'Delete 1,234 titles?'],
    ] as const) {
      assert.equal(i18n.t(id, {count}), expected, `${locale} ${count}`);
    }
  }
});
