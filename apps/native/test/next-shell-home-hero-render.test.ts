import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {homeTitleFactParts, type HomeTitlePresentationEntry} from '../../../packages/client-core/src/presentation/home-spotlight';
import type {TextEnvironment} from '../../../packages/client-core/src/presentation/kit-model';
import {homeCardText} from '../src-next/screens/home/model';
import {createTheme} from '../src-next/kit/theme';
import {screenModule} from './helpers/screen-harness.mjs';

const env = {t: defaultI18n.t, locale: 'en-US'} as TextEnvironment;
const elements = (node: any): React.ReactElement<any>[] => Array.isArray(node) ? node.flatMap(elements) : React.isValidElement<any>(node) ? [node, ...elements(node.props.children)] : [];
const text = (node: any): string[] => Array.isArray(node) ? node.flatMap(text) : React.isValidElement<any>(node) ? text(node.props.children) : typeof node === 'string' ? [node] : [];
async function renderer(tv = true) {
  const theme = createTheme({isTV: tv, width: tv ? 1920 : 390});
  return screenModule(new URL('../src-next/screens/home/HomeHeroFacts.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/theme': {useTheme: () => theme},
  });
}
const episode: HomeTitlePresentationEntry = {id: 'episode', libraryId: 'tv', kind: 'episode', title: 'Episode title', show: {id: 'show', name: 'Show title'},
  year: 2026, genres: ['Episode genre'], seasonNumber: 1, episodeNumber: 2, duration: 4200, progressSeconds: 600,
  homeTitleContext: {kind: 'show', year: 2023, genres: ['Drama', 'Mystery'], seasonCount: 1}};

test('TV focus renders genuine parent facts whole and separate actual continuation timing', async () => {
  const {HomeHeroFacts} = await renderer();
  const facts = homeTitleFactParts(episode, env), continuation = homeCardText(episode, true, env);
  const tree = HomeHeroFacts({facts, continuation}), labels = text(tree);
  assert.deepEqual(labels.slice(0, facts.length), facts.map(part => part.text));
  assert.equal(labels.includes('2026'), false);
  assert.equal(labels.includes('Episode genre'), false);
  assert.equal(labels.filter(value => value === 'Drama, Mystery').length, 1);
  assert.equal(labels.includes(continuation.detail.find(part => part.id === 'remaining')!.text), true);
  assert.equal(elements(tree).filter(node => node.props.style?.width === 160).length, 1);
});

test('sparse display context never invents parent fields or a progress/remaining row', async () => {
  const {HomeHeroFacts} = await renderer();
  const sparse = {...episode, homeTitleContext: {kind: 'show' as const, genres: []}, duration: undefined, progressSeconds: undefined};
  const tree = HomeHeroFacts({facts: homeTitleFactParts(sparse, env), continuation: homeCardText(sparse, true, env)});
  assert.deepEqual(text(tree), []);
  assert.equal(elements(tree).some(node => node.props.style?.width === 160), false);
});

test('phone hero retains canonical badges without adding the TV continuation row', async () => {
  const {HomeHeroFacts} = await renderer(false);
  const facts = homeTitleFactParts(episode, env);
  const tree = HomeHeroFacts({facts, continuation: homeCardText(episode, true, env)});
  assert.deepEqual(text(tree), facts.map(part => part.text));
  assert.equal(elements(tree).some(node => node.props.style?.width === 160), false);
  assert.equal(elements(tree).filter(node => node.props.style?.borderWidth === 1).length, 1);
});

test('remote hints preserve actual Resume versus Play with both drawn keycaps', async () => {
  const {HomeRemoteHint} = await renderer();
  for (const resume of [false, true]) {
    const tree = HomeRemoteHint({resume});
    assert.deepEqual(text(tree), [defaultI18n.t('next.layout.ok'), defaultI18n.t(resume ? 'next.home.resume' : 'next.home.play'), defaultI18n.t('next.layout.holdOk'), defaultI18n.t('next.layout.moreOptions')]);
    assert.equal(elements(tree).filter(node => node.props.style?.minWidth === 44 && node.props.style?.height === 36).length, 2);
  }
});
