import test from 'node:test';
import assert from 'node:assert/strict';
import {audiobookRates, parseListeningPreferences} from '../src/listening.ts';

test('audiobook preferences accept only the approved speed steps', () => {
  assert.deepEqual([...audiobookRates], [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3]);
  const saved = (bookRate: number) => ({revision: 3, bookRate, passoutMinutes: 0});
  const cases: {name: string; book: number; ok: boolean}[] = [
    {name: 'a book at three times', book: 3, ok: true},
    {name: 'a book at two and a half', book: 2.5, ok: true},
    {name: 'a book at four times', book: 4, ok: false},
    {name: 'a speed between the steps', book: 2.2, ok: false},
  ];
  for (const c of cases) {
    if (c.ok) assert.equal(parseListeningPreferences(saved(c.book)).bookRate, c.book, c.name);
    else assert.throws(() => parseListeningPreferences(saved(c.book)), undefined, c.name);
  }
});
