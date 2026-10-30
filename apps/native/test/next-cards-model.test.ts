import assert from 'node:assert/strict';
import {test} from 'node:test';
import {activateCard, cardAccessibilityLabel, cardProgress, holdCard, type CardActions} from '../src-next/kit/cards/model.ts';

test('title navigation and server-chosen playback keep independent callback identities', () => {
  const calls: string[] = [];
  const actions = {onNavigate: () => calls.push('title:show'), onPlay: () => calls.push('play:next-episode')} satisfies CardActions;
  activateCard(actions);
  activateCard({...actions, primaryAction: 'play'});
  assert.deepEqual(calls, ['title:show', 'play:next-episode']);
});

test('selection toggles by the caller identity and never starts playback or navigation', () => {
  const calls: string[] = [];
  const selection = {active: true, selected: false, onToggle: () => calls.push('toggle:item-id'), onEnter: () => calls.push('enter:item-id')};
  const actions: CardActions = {primaryAction: 'play', onPlay: () => calls.push('play'), onNavigate: () => calls.push('navigate'), selection, onMore: () => calls.push('menu')};
  activateCard(actions);
  holdCard(actions, false);
  holdCard(actions, true);
  assert.deepEqual(calls, ['toggle:item-id', 'toggle:item-id', 'menu']);
});

test('TV menu remains a menu even when selection is active, and never invents Select', () => {
  const calls: string[] = [];
  const actions: CardActions = {onNavigate: () => calls.push('navigate'), selection: {active: true, selected: true, onToggle: () => calls.push('toggle'), onEnter: () => calls.push('enter')}};
  holdCard(actions, true);
  assert.equal(calls.length, 0);
  holdCard({...actions, onMore: () => calls.push('more')}, true);
  assert.deepEqual(calls, ['more']);
});

test('a phone card without selectable media retains its genuine long-press menu', () => {
  let menus = 0;
  holdCard({onNavigate() {}, onMore: () => menus++}, false);
  assert.equal(menus, 1);
});

test('unknown and nonfinite progress stays unknown; measured fractions remain bounded', () => {
  assert.equal(cardProgress(undefined), undefined);
  assert.equal(cardProgress(Number.NaN), undefined);
  assert.equal(cardProgress(Infinity), undefined);
  assert.equal(cardProgress(-1), 0);
  assert.equal(cardProgress(0), 0);
  assert.equal(cardProgress(0.38), 0.38);
  assert.equal(cardProgress(3), 1);
});


test('Q6 an unselected touch card opens More without entering selection', () => {
  const calls: string[] = [];
  holdCard({onNavigate() {}, onMore: () => calls.push('more'), selection: {
    active: false, selected: false, onToggle: () => calls.push('toggle'), onEnter: () => calls.push('enter'),
  }}, false);
  assert.deepEqual(calls, ['more']);
});


test('same-title cards retain their own context and watched/new state in spoken identity', () => {
  const original = cardAccessibilityLabel('The Thing', [{text: '1951'}], [{text: '87 min'}], ['Watched']);
  const remake = cardAccessibilityLabel('The Thing', [{text: '1982'}], [{text: '109 min'}], ['New']);
  assert.equal(original, 'The Thing, 1951, 87 min, Watched');
  assert.equal(remake, 'The Thing, 1982, 109 min, New');
  assert.notEqual(original, remake);
});

test('spoken identities preserve track/episode/session facts without blank or repeated values', () => {
  assert.equal(cardAccessibilityLabel('Song', [{text: 'Artist A'}, {text: 'Album'}], [{text: '4:32'}], ['Playing']), 'Song, Artist A, Album, 4:32, Playing');
  assert.equal(cardAccessibilityLabel('Song', [{text: 'Artist B'}]), 'Song, Artist B');
  assert.equal(cardAccessibilityLabel('Pilot', [{text: 'S1 E1'}, {text: 'Show A'}], [], ['32:12']), 'Pilot, S1 E1, Show A, 32:12');
  assert.equal(cardAccessibilityLabel('Title', [{text: 'Profile'}, {text: 'On Bedroom TV'}], [], ['Paused']), 'Title, Profile, On Bedroom TV, Paused');
  assert.equal(cardAccessibilityLabel('Title', [{text: ' Title '}], [{text: ''}], [undefined, ' ', 'New', 'New']), 'Title, New');
  assert.equal(cardAccessibilityLabel('Book', [], [], ['Author']), 'Book, Author');
});
