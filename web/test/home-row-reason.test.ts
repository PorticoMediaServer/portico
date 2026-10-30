import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {homeRowReasonParts} from '../src/screens/browse/home-row-reason.ts';

test('reason emphasis follows the actual parameter even when a title also occurs in the prefix', () => {
  const parts = homeRowReasonParts({why: {code: 'next.home.whyRecommendedFromActivity',
    params: {titles: 'Because'}, fallback: ''}, whyTitles: [{id: 'actual', name: 'Because'}]}, defaultI18n);
  assert.equal(parts.map(part => part.text).join(''), 'Because you’ve been watching Because. Titles you’ve finished are left out.');
  assert.deepEqual(parts.filter(part => part.titleId), [{text: 'Because', titleId: 'actual'}]);
  assert.equal(parts[0]?.text, 'Because you’ve been watching ');
});

test('reason emphasis preserves full stored titles, overlaps, punctuation and unknown fallback copy', () => {
  const titles = [{id: 'special', name: 'Hunger Games: Special Edition'}, {id: 'base', name: 'Hunger Games'}];
  const params = {titles: 'Hunger Games: Special Edition and Hunger Games'};
  const why = {code: 'next.home.whyRecommendedFromActivity', params, fallback: ''};
  const parts = homeRowReasonParts({why, whyTitles: titles}, defaultI18n);
  assert.equal(parts.map(part => part.text).join(''), defaultI18n.t('next.home.whyRecommendedFromActivity', params));
  assert.deepEqual(parts.filter(part => part.titleId), titles.map(title => ({text: title.name, titleId: title.id})));
  assert.deepEqual(homeRowReasonParts({why: {...why, code: 'future.message', fallback: params.titles}, whyTitles: titles}, defaultI18n), [{text: params.titles}]);
});
