import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

async function shelf(view: Record<string, unknown>) {
  const {ReadingPeriodicalShelf} = await screenModule(new URL('../src-next/pages/touch/reading/ReadingPeriodicalShelf.tsx', import.meta.url), {
    react: {...React, default: React}, '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../kit/cards': {CoverCard: 'CoverCard'}, '../../../kit/controls': {Button: 'Button'}, '../../../kit/reading/PeriodicalShelf': {PeriodicalShelf: 'PeriodicalShelf'},
    '../../../kit/states': {StateView: 'StateView'}, '../../shared/reading/usePeriodicalShelf': {usePeriodicalShelf: () => view},
  });
  return (kind: string) => ReadingPeriodicalShelf({context: {}, runtime: {}, libraryId: 'lib', kind, opening: {}});
}

test('a library\'s Magazines are its publications as 3:4 covers with the latest date and issue count; each opens its archive', async () => {
  const opened: string[] = [];
  const render = await shelf({state: {phase: 'ready', groups: [{}]}, more: undefined,
    cards: [{key: 'g1', title: 'Scientific American', cover: {uri: 'x'}, caption: [{id: 'date', text: 'August 27, 1846', role: 'identity'}], open: () => opened.push('g1')}]});
  const tree = render('magazine'), cards = tree.props.cards(120);
  assert.equal(tree.type, 'PeriodicalShelf');
  assert.equal(cards[0].type, 'CoverCard'); assert.equal(cards[0].props.kind, 'periodical'); assert.equal(cards[0].props.width, 120);
  assert.equal(cards[0].props.title, 'Scientific American');
  cards[0].props.onNavigate(); assert.deepEqual(opened, ['g1']);
  assert.equal(render('newspaper').props.cards(100)[0].props.kind, 'newspaper');
});

test('a failed read offers Try again, and an empty one says nothing is here', async () => {
  let retried = 0;
  const failed = (await shelf({state: {phase: 'error', groups: []}, retry: () => {retried++;}, cards: []}))('magazine');
  assert.equal(failed.type, 'StateView'); failed.props.primaryAction.onPress(); assert.equal(retried, 1);
  assert.equal((await shelf({state: {phase: 'ready', groups: []}, cards: []}))('newspaper').props.title, 'title.nothingHere');
});
