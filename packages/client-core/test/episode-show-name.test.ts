import test from 'node:test';
import assert from 'node:assert/strict';
import {entryFromItem, type Item} from '../src/items.ts';
import {cardText} from '../src/presentation/kit-model/facts.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const episode = (over: Partial<Item> = {}) => ({id: 'itm_ep', kind: 'episode', title: 'The Letters We Left', libraryId: 'lib_tv', addedAt: '2026-11-12T00:00:00Z',
  rootId: 'itm_show', parentIndex: 1, index: 3, images: {}, state: {watched: false, inList: false, favorite: false, notInterested: false, playCount: 0}, ...over}) as Item;

test('an episode on a list names its show from rootTitle, and a hidden show is named by nothing', () => {
  assert.deepEqual(entryFromItem(episode({rootTitle: 'The Extraordinary Observatory'}))?.show, {id: 'itm_show', name: 'The Extraordinary Observatory'});
  assert.equal(entryFromItem(episode())?.show, undefined, 'the server leaves rootTitle out when the show is hidden from the profile');
  // The title's own page still names it from root.
  assert.deepEqual(entryFromItem(episode({root: {id: 'itm_show', title: 'From the page'}} as Partial<Item>))?.show, {id: 'itm_show', name: 'From the page'});
});

test('the shared episode card names its show beside the season and episode', () => {
  const entry = entryFromItem(episode({rootTitle: 'The Extraordinary Observatory'}))!;
  const card = cardText({id: entry.id, kind: 'episode', title: entry.title, show: {id: entry.show!.id, title: entry.show!.name}, episode: {mode: 'seasonal', season: 1, episode: 3}}, 'ordinary', {t: defaultI18n.t});
  assert.ok(card.caption.some(part => part.text === 'The Extraordinary Observatory'));
});
