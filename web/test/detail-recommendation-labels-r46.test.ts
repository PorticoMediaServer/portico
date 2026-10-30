import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {defaultI18n} from '@i18n';
import {serviceI18n, setServiceI18n} from '@core/presentation/service-text.ts';
import {validateItemRecommendations} from '@core/recommendations.ts';
import type {DetailBindings} from '../src/screens/detail/bindings.ts';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});

const {TitleRecommendationRow, TitleRow} = await import('../src/screens/detail/Detail.tsx');
const {createDetailSpecimen} = await import('../src/dev/entries/detail-specimen.tsx');
const specimen = createDetailSpecimen();
const binding: Pick<DetailBindings, 'context'> = {context: {
  api: specimen.api, events: specimen.events, scope: {serverId: specimen.serverId, viewerId: specimen.personalFence},
  isCurrent: () => true, artwork: specimen.artwork,
  preferences: {getSnapshot: () => specimen.preferences, subscribe: specimen.subscribePreferences},
  effects: {openEntry() {}, openSettings() {}, async play() {}, reportError(error) { throw error; }},
}};
const [first, second] = validateItemRecommendations(specimen.recommendations(), specimen.itemId).rows;

test('recommendation labels use the current UI catalogue and retain the original row and guard', () => {
  const before = serviceI18n(), guard = () => {};
  setServiceI18n({...defaultI18n, t: (id, values) => `ui:${defaultI18n.t(id, values)}`});
  try {
    const row = {...first, titleText: {code: 'next.controls.moreLikeThis', fallback: 'server heading'},
      subtitleText: {code: 'next.controls.details', fallback: 'server subtitle'}};
    const result = TitleRecommendationRow({row, binding, guard}).props;
    assert.equal(result.heading, 'ui:More like this');
    assert.equal(result.subtitle, 'ui:Details');
    assert.equal(result.entries, first.entries);
    assert.equal(result.guard, guard);
  } finally { setServiceI18n(before); }
});

test('unknown labels preserve server fallback and an absent subtitle stays absent', () => {
  const labelled = TitleRecommendationRow({row: second, binding}).props;
  assert.equal(labelled.heading, 'Viewers also watched');
  assert.equal(labelled.subtitle, 'What people on this server played after it');
  const unlabelled = TitleRecommendationRow({row: {...first, titleText: undefined, subtitleText: undefined}, binding}).props;
  assert.equal(unlabelled.heading, first.title);
  assert.equal(unlabelled.subtitle, '');
});

test('the handheld row keeps its drawn heading without the desktop subtitle', () => {
  const html = renderToStaticMarkup(createElement(TitleRow, {
    heading: second.title, subtitle: second.subtitleText?.fallback, entries: [], binding,
  }));
  assert.match(html, /Viewers also watched/);
  assert.doesNotMatch(html, /What people on this server played after it/);
});

test('the retained compact body preserves its existing subtitle-free presentation', () => {
  const result = TitleRecommendationRow({row: second, binding, subtitleVisible: false}).props;
  assert.equal(result.heading, second.title);
  assert.equal(result.subtitle, undefined);
  assert.equal(result.entries, second.entries);
});
