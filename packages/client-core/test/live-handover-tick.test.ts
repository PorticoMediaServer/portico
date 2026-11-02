import test from 'node:test';
import assert from 'node:assert/strict';
import {tunerHandover, tunerHandoverLines, tunerHandoverTick} from '../src/live-handover.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

test('the handover line names the time, then counts the last minute down by the second (LT-8)', () => {
  const at = 1_000_000_000;
  assert.equal(tunerHandoverTick(undefined, at), undefined);
  assert.equal(tunerHandoverTick(at, at), undefined, 'nothing to redraw once the server takes over');
  assert.equal(tunerHandoverTick(at, at - 5 * 60_000), 4 * 60_000, 'a fixed time redraws only when the countdown starts');
  assert.equal(tunerHandoverTick(at, at - 30_500), 500, 'then on each whole second');
  assert.equal(tunerHandoverTick(at, at - 30_000), 1000);
  const later = tunerHandover(at, at - 5 * 60_000)!, countdown = tunerHandover(at, at - 30_000)!;
  const [first] = tunerHandoverLines(later, defaultI18n.t, () => '9:00 PM');
  assert.match(first, /9:00 PM/);
  assert.match(tunerHandoverLines(countdown, defaultI18n.t, () => '')[0], /30 seconds/);
});
