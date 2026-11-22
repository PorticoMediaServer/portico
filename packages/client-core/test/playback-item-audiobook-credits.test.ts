import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackItem} from '../src/playback-v1/item.ts';
import {playbackIdentity} from '../src/presentation/kit-model/index.ts';
import type {Item} from '../../contracts/src/next/api.ts';

const env = {t: (key: string, values?: Record<string, unknown>) => key === 'next.kit.readBy' ? `Read by ${values?.name}` : key, locale: 'en-US'};
const book = (credits: Item['credits']) => ({id: 'itm_book', kind: 'audiobook', title: 'The Martian', libraryId: 'lib_a', available: true, credits}) as unknown as Item;

test("an audiobook's player names its author and narrator from the title's credits", () => {
  const item = playbackItem(book([{personId: 'p1', name: 'Andy Weir', role: 'author'}, {personId: 'p2', name: 'R.C. Bray', role: 'narrator'},
    {personId: 'p3', name: ' ', role: 'narrator'}, {personId: 'p4', name: 'Someone', role: 'director'}]));
  assert.equal(item.subject.kind, 'audiobook');
  const card = item.subject.kind === 'audiobook' ? item.subject.item : undefined;
  assert.equal(card?.author, 'Andy Weir');
  assert.equal(card?.narrator, 'R.C. Bray');
  const words = playbackIdentity(item.subject, 'full', env as never).subtitle.map(part => part.text);
  assert.ok(words.includes('Andy Weir'), words.join(' | '));
});

test('a title with no credits, or a song or film, gets no author or narrator', () => {
  const none = playbackItem(book(undefined));
  assert.equal(none.subject.kind === 'audiobook' ? none.subject.item.author : 'x', undefined);
  const film = playbackItem({id: 'itm_f', kind: 'movie', title: 'Film', libraryId: 'lib_f', available: true,
    credits: [{personId: 'p', name: 'Writer', role: 'author'}]} as unknown as Item);
  assert.equal(film.subject.kind === 'video' ? (film.subject.item as {author?: string}).author : 'x', undefined);
});
