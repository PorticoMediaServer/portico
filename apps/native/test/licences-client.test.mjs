import test from 'node:test';
import assert from 'node:assert/strict';
import {selectedClients} from '../scripts/licences.mts';

test('normal licence generation retains every client', () => {
  assert.deepEqual(selectedClients([]), ['android', 'apple', 'vega']);
});
test('Android packaging selects only Android notices and rejects an absent or unknown target', () => {
  assert.deepEqual(selectedClients(['--record', '--client', 'android']), ['android']);
  assert.throws(() => selectedClients(['--client']));
  assert.throws(() => selectedClients(['--client', 'unknown']));
});
