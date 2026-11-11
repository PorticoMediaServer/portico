import {test} from 'node:test';
import assert from 'node:assert/strict';
import {restoreReady, sentAsConfirmed} from '../src/screens/settings/server/restore-gate.ts';

test('Restore is offered only with the word typed exactly, a password, and a backup that passed its check', () => {
  const ok = {checked: true, busy: false, password: 'pw', typed: 'restore', word: 'restore'};
  assert.equal(restoreReady(ok), true);
  for (const change of [{checked: false}, {busy: true}, {password: ''}, {typed: ''}, {typed: 'Restore'}, {typed: 'restore '}, {typed: 'delete'}])
    assert.equal(restoreReady({...ok, ...change}), false, JSON.stringify(change));
});
test('a backup this server did not make is sent as confirmed only once its warning has been shown', () => {
  assert.equal(sentAsConfirmed({madeHere: true, warned: false}), false);
  assert.equal(sentAsConfirmed({madeHere: false, warned: false}), true);  // the check already said so, and the warning is on screen
  assert.equal(sentAsConfirmed({madeHere: true, warned: true}), true);    // the server said so on the first press
});
