import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultSeriesChoice, seriesRule} from '../src/screens/live/programme-bindings';

// The server makes a series rule from the programme it names; the panel sends only the editor's choices.
test('Record series sends the programme with the editor\'s choices: new episodes only or all, how many to keep, padding, and any channel', () => {
  assert.deepEqual(seriesRule('pg_1', {...defaultSeriesChoice, episodes: 'all', keep: '5', startEarly: 1, endLate: 2, anyChannel: true}),
    {programme: 'pg_1', newOnly: false, keepNewest: 5, anyChannel: true, padBeforeMs: 60_000, padAfterMs: 120_000});
});

test('the drawn defaults are new episodes, every episode kept, no padding, this channel only', () => {
  assert.deepEqual(seriesRule('pg_1', defaultSeriesChoice), {programme: 'pg_1', newOnly: true});
  assert.equal(defaultSeriesChoice.startEarly, 0);
  assert.equal(defaultSeriesChoice.endLate, 0);
  assert.equal(defaultSeriesChoice.anyChannel, false);
});
