import test from 'node:test';
import assert from 'node:assert/strict';
import {nextProfileArt} from '../src/profile-management.ts';

// The colour a screen shows for a new profile nobody coloured is the one the server will give it:
// the first in palette order not used on the account, else blue (server-next db.profiles).
test('a new profile is shown the first colour unused on its account, in palette order', () => {
  assert.equal(nextProfileArt([]), 'blue');
  assert.equal(nextProfileArt([{art: 'blue'}]), 'violet');
  assert.equal(nextProfileArt([{art: 'blue'}, {art: 'mint'}, {art: 'violet'}]), 'coral');
  assert.equal(nextProfileArt(['blue', 'violet', 'mint', 'coral', 'gold', 'slate', 'rose', 'sky'].map(art => ({art: art as 'blue'}))), 'blue', 'all used: back to blue');
});
