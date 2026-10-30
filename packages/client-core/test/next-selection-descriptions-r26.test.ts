import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, messageArguments, resolveRegion, supportedLocales} from '../../i18n/src/index.ts';

test('phone More descriptions keep the approved copy, required ICU arguments and regional source fallback', () => {
  for (const [id, expected, args] of [
    ['next.selection.moreEditDescription', 'Fields shared by {items}, with Mixed where they differ', ['items']],
    ['next.selection.moreDownloadDescription', 'About {size} at {quality}', ['quality', 'size']],
  ] as const) {
    assert.equal(enUS[id], expected, id);
    assert.deepEqual(messageArguments(enUS[id]), args, id);
    for (const locale of supportedLocales) {
      const regional = catalogue(locale)[id];
      assert.deepEqual(messageArguments(regional ?? enUS[id]), args, `${locale} ${id}`);
      if (locale !== 'en-US') assert.equal(regional, undefined, `${locale} ${id}`);
    }
  }
});

test('More edit description composes the actual singular or plural item label in every supported region', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    for (const [count, expected] of [[1, '1 movie'], [4, '4 movies']] as const) {
      const items = i18n.t('next.selection.movie', {count});
      assert.equal(items, expected, locale);
      assert.equal(i18n.t('next.selection.moreEditDescription', {items}),
        `Fields shared by ${expected}, with Mixed where they differ`, locale);
    }
  }
});

test('More download description formats both supplied size and quality labels in every supported region', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(i18n.t('next.selection.moreDownloadDescription', {size: '2.5 GB', quality: 'Original'}),
      'About 2.5 GB at Original', locale);
    assert.equal(i18n.t('next.selection.moreDownloadDescription', {size: '980 MB', quality: '1080p'}),
      'About 980 MB at 1080p', locale);
  }
});

test('More description arguments retain quotes, braces and markup characters as literal text', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    const items = 'Alex\'s "Color" <Live> & {Programme}';
    assert.equal(i18n.t('next.selection.moreEditDescription', {items}),
      'Fields shared by Alex\'s "Color" <Live> & {Programme}, with Mixed where they differ', locale);
    assert.equal(i18n.t('next.selection.moreDownloadDescription', {
      size: '{size} & 2.5 GB', quality: 'Alex\'s "Original" <4K> {quality}',
    }), 'About {size} & 2.5 GB at Alex\'s "Original" <4K> {quality}', locale);
  }
});
