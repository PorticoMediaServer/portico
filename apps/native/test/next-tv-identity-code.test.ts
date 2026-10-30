import test from 'node:test';
import {readFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {appendIdentityCode, formatIdentityCode, identityCodeCharacters, identityCodeEntry, identityCodeRows} from '../src-next/tv/auth/identity-code-input';

test('frame 2a draws A–Z, the dash, then 0–9 on nine-key rows', () => {
  assert.equal(identityCodeCharacters.join(''), 'ABCDEFGHIJKLMNOPQRSTUVWXYZ-1234567890');
  assert.deepEqual(identityCodeRows.map(row => row.join('')), ['ABCDEFGHI', 'JKLMNOPQR', 'STUVWXYZ-', '123456789', '0']);
  assert.deepEqual(identityCodeEntry, {region: 'tv.identity-code.keyboard.2', child: 'V'});
  assert.ok(identityCodeRows[2].includes('V'));
  assert.ok(!identityCodeRows[1].includes('V'));
});

test('typing keeps twelve symbols, redraws the dashes and ignores spaces', () => {
  assert.equal(appendIdentityCode('', 'G'), 'G');
  assert.equal(appendIdentityCode('G39EBZ', 'V'), 'G39EBZV');
  assert.equal(appendIdentityCode('G39E', 'space'), 'G39E');
  assert.equal(appendIdentityCode('G39E', '-'), 'G39E');
  assert.equal(appendIdentityCode('G39EBZVDSEJ', 'C'), 'G39EBZVDSEJC');
  assert.equal(appendIdentityCode('G39EBZVDSEJC', 'X'), 'G39EBZVDSEJC');
  assert.equal(appendIdentityCode('G39EBZVDSEJC', 'space'), 'G39EBZVDSEJC');
  assert.equal(appendIdentityCode('G39EBZVDSEJC', ''), 'G39EBZVDSEJC');
  assert.equal(appendIdentityCode('G39E', 'delete'), 'G39');
  assert.equal(appendIdentityCode('G39E', 'clear'), '');
  assert.equal(formatIdentityCode('G39EBZVDSEJC'), 'G39E-BZVD-SEJC');
  assert.equal(formatIdentityCode('G39'), 'G39');
  // separators never count toward the twelve symbols
  assert.equal(appendIdentityCode('G39E-BZVD-SE', '-').length, 10);
  assert.equal(appendIdentityCode('clear-then-full', 'clear'), '');
  assert.equal(appendIdentityCode('G39EBZVDSEJ', '-').length, 11);
});

test('the rendered keyboard requests the derived V row, not an assumed one', () => {
  const source = readFileSync(new URL('../src-next/tv/auth/TVIdentityCodeForm.tsx', import.meta.url), 'utf8');
  assert.match(source, /useDestinationEntryFocus\(\{[^}]*\}, \{regionId: identityCodeEntry\.region, childId: identityCodeEntry\.child\}\)/);
  assert.equal(source.includes('keyboard.1'), false);
});
