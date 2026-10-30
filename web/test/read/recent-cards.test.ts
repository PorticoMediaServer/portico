import assert from 'node:assert/strict';
import {test} from 'node:test';
import {recentCards, type RecentReadingItem} from '../../src/screens/read/recent-cards.ts';

const item = (overrides: Partial<RecentReadingItem> = {}): RecentReadingItem => ({
  id: 'book-1', sourceId: 'source-1', title: 'The Great Gatsby',
  author: {value: 'F. Scott Fitzgerald'}, inventoryAvailable: true, ...overrides,
});

const actions = (calls: string[]) => ({
  read: 'Read',
  open: (entry: RecentReadingItem) => {calls.push(`open:${entry.id}`);},
  play: (entry: RecentReadingItem) => {calls.push(`play:${entry.id}:${entry.sourceId}`);},
});

test('the recently-added row keeps the server order and the accepted author fact', () => {
  const calls: string[] = [];
  const cards = recentCards([item(), item({id: 'book-2', sourceId: 'source-2', title: 'Dune', author: {value: 'Frank Herbert'}})], actions(calls));
  assert.deepEqual(cards.map(card => card.id), ['book-1', 'book-2']);
  assert.equal(cards[1]!.card.model.title, 'Dune');
  assert.deepEqual(cards[1]!.card.model.caption, [{id: 'author', text: 'Frank Herbert', role: 'identity'}]);
});

test('an item without an accepted author carries no invented caption', () => {
  const [card] = recentCards([item({author: undefined})], actions([]));
  assert.deepEqual(card!.card.model.caption, []);
});

test('every card has the drawn generated fallback, in the shelf presentation', () => {
  const [card] = recentCards([item()], actions([]));
  assert.equal(card!.card.context, 'shelf');
  assert.equal(card!.card.density, 'shelf');
  assert.equal(card!.card.generated?.title, 'The Great Gatsby');
  assert.equal(card!.card.generated?.glyph, 'T');
});

test('opening and reading act on the item the server sent', () => {
  const calls: string[] = [];
  const [card] = recentCards([item()], actions(calls));
  card!.card.onNavigate();
  card!.card.onPlay?.();
  assert.deepEqual(calls, ['open:book-1', 'play:book-1:source-1']);
});

test('an item whose file is not available opens but does not offer Read', () => {
  const calls: string[] = [];
  const [card] = recentCards([item({inventoryAvailable: false})], actions(calls));
  assert.equal(card!.card.disabled, true);
  assert.equal(card!.card.onPlay, undefined);
});

test('an empty row produces no cards', () => {
  assert.deepEqual(recentCards([], actions([])), []);
});
