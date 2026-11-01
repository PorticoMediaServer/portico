import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {homeRowReasonParts} from '../src/presentation/home-row-reason.ts';

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

test('genre, tag and person reasons bold the titles they name (B2)', () => {
  const titles = [{id: 'heat', name: 'Heat'}, {id: 'collateral', name: 'Collateral'}];
  const facet = homeRowReasonParts({why: {code: 'next.home.whyFacetTitles', params: {label: 'Heists', titles: 'Heat and Collateral'}, fallback: ''}, whyTitles: titles}, defaultI18n);
  assert.equal(facet.map(part => part.text).join(''), 'Because you often watch Heists, like Heat and Collateral.');
  assert.deepEqual(facet.filter(part => part.titleId).map(part => part.titleId), ['heat', 'collateral']);
  const person = homeRowReasonParts({why: {code: 'next.home.whyPersonTitles', params: {titles: 'Heat and Collateral', name: 'Michael Mann'}, fallback: ''}, whyTitles: titles}, defaultI18n);
  assert.equal(person.map(part => part.text).join(''), 'Because you’ve watched Heat and Collateral with Michael Mann.');
  assert.deepEqual(person.filter(part => part.titleId).map(part => part.text), ['Heat', 'Collateral']);
  const plain = homeRowReasonParts({why: {code: 'next.home.whyFacet', params: {label: 'Heists'}, fallback: ''}}, defaultI18n);
  assert.deepEqual(plain, [{text: 'Because you often watch Heists.'}]);
});
