import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {homeDocument} from '../../packages/client-core/src/home.ts';
import {continueBookFacts} from '../../packages/client-core/src/presentation/continue-books.ts';
import {homeContinuationSource} from '../../packages/client-core/src/presentation/home-continuation.ts';
import {homeCardModel} from '../src/screens/home/model.ts';

const t = defaultI18n.t.bind(defaultI18n);

// Home's Continue your books, as the server sends it (decision Q24): an audiobook in progress
// draws the book resume card with the chapter it is in and the time left (tablet-read frame 11),
// computed as Home.tsx's VisibleBookCard does.
test('an audiobook in Continue your books shows its chapter and the time left', () => {
  const state = {watched: false, positionMs: 2_400_000, playCount: 0, favorite: false, inList: false, notInterested: false, lastPlayedAt: '2026-11-16T16:49:19Z'};
  const home = homeDocument({rows: [{id: 'continue_books', title: 'Continue your books', shape: 'poster', items: [
    {id: 'itm_scarlet', kind: 'audiobook', title: 'A Study in Scarlet', durationMs: 15_238_827, libraryId: 'lib_a', images: {}, state,
      chapter: {number: 3, title: 'Chapter 3', startMs: 2_000_000, endMs: 3_000_000}},
  ]}]} as never);
  const row = home.rows.find(value => value.id === 'continue_books');
  assert.ok(row, 'the books row is kept');
  const entry = row.entries[0]!;
  const facts = continueBookFacts(entry, t, homeCardModel(homeContinuationSource(entry, entry), true, {t, locale: 'en'}));
  assert.equal(facts.shape, 'audiobook');
  assert.equal(facts.where, 'Chapter 3');
  assert.match(facts.left ?? '', /3 h 34 min left/);
});
