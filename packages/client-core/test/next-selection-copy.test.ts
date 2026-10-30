import test from 'node:test';
import assert from 'node:assert/strict';
import {catalogue, createI18n, enUS, messageArguments, resolveRegion, supportedLocales} from '../../i18n/src/index.ts';

const argumentsById = {
  'next.selection.native.queueTitle': ['items'],
  'next.selection.native.queueOrder': [],
  'next.selection.native.queueNextDescription': [],
  'next.selection.native.queueLaterDescription': [],
  'next.selection.native.addedIndicator': ['count'],
  'next.selection.native.picker.playlist': ['items'],
  'next.selection.native.picker.collection': ['items'],
  'next.selection.native.updated': ['failed', 'updated'],
  'next.selection.native.savedKept': ['count', 'failed', 'titles'],
  'next.selection.native.conflict': [],
} as const;

test('native selection messages keep their ICU arguments and source fallback in every supported region', () => {
  for (const [id, args] of Object.entries(argumentsById) as [keyof typeof argumentsById, readonly string[]][]) {
    assert.deepEqual(messageArguments(enUS[id]), args, id);
    for (const locale of supportedLocales) {
      const regional = catalogue(locale)[id];
      assert.deepEqual(messageArguments(regional ?? enUS[id]), args, `${locale} ${id}`);
      if (locale !== 'en-US') assert.equal(regional, undefined, `${locale} ${id}`);
    }
  }
});

test('queue and picker headings use the supplied item label; queue choices keep their descriptions', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    for (const items of ['2 movies', 'Color {Live}']) {
      assert.equal(i18n.t('next.selection.native.queueTitle', {items}), `Add ${items} to the queue`, locale);
      assert.equal(i18n.t('next.selection.native.picker.playlist', {items}), `Add ${items} to a playlist`, locale);
      assert.equal(i18n.t('next.selection.native.picker.collection', {items}), `Add ${items} to a collection`, locale);
    }
    assert.equal(i18n.t('next.selection.native.queueOrder'), 'In the order you picked them', locale);
    assert.equal(i18n.t('next.selection.native.queueNextDescription'), 'After what’s playing now', locale);
    assert.equal(i18n.t('next.selection.native.queueLaterDescription'), 'At the end of the queue', locale);
  }
});

test('selection added and updated receipts format the supplied counts in every supported region', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(i18n.t('next.selection.native.addedIndicator', {count: 0}), '0 added', locale);
    assert.equal(i18n.t('next.selection.native.addedIndicator', {count: 1}), '1 added', locale);
    assert.equal(i18n.t('next.selection.native.addedIndicator', {count: 1234}), '1,234 added', locale);
    assert.equal(i18n.t('next.selection.native.updated', {updated: 0, failed: 1}), '0 updated, 1 failed', locale);
    assert.equal(i18n.t('next.selection.native.updated', {updated: 1234, failed: 2}), '1,234 updated, 2 failed', locale);
  }
});

test('partial-save receipts preserve failed titles and choose singular or plural without losing the saved count', () => {
  for (const locale of supportedLocales) {
    const i18n = createI18n(resolveRegion({locale}, {locales: ['en-US']}));
    assert.equal(i18n.t('next.selection.native.savedKept', {count: 0, titles: i18n.list(['Color {Live}']), failed: 1}),
      'The 0 that worked are saved. Color {Live} is unchanged.', locale);
    assert.equal(i18n.t('next.selection.native.savedKept', {count: 1234, titles: i18n.list(['Color {Live}', "Alex's Cut"]), failed: 2}),
      "The 1,234 that worked are saved. Color {Live} and Alex's Cut are unchanged.", locale);
    assert.equal(i18n.t('next.selection.native.conflict'),
      'Someone else changed this title while you were editing. Reopen it, then repeat the change.', locale);
  }
});
