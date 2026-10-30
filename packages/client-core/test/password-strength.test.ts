import assert from 'node:assert/strict';
import test from 'node:test';
import {passwordStrength} from '../src/password-strength.ts';

test('the only gate is 8 characters and the 72-byte cap', () => {
  assert.equal(passwordStrength('short12').acceptable, false);
  assert.equal(passwordStrength('abcdefgh').acceptable, true);
  assert.equal(passwordStrength('password').acceptable, true, 'weak passwords are allowed');
  assert.equal(passwordStrength('ééééééé').acceptable, false, 'counts characters, not bytes');
  assert.equal(passwordStrength('é'.repeat(37)).acceptable, false, '74 bytes is over the bcrypt limit');
});

test('strength is advisory and ordered', () => {
  assert.equal(passwordStrength('password').level, 1);
  assert.equal(passwordStrength('11111111').level, 1);
  assert.equal(passwordStrength('meadow42').level, 1);
  assert.ok(passwordStrength('Meadow-42-lamp').level >= 3);
  assert.equal(passwordStrength('orchard riverside candle marigold').level, 4);
  assert.equal(passwordStrength('orchard riverside candle marigold').hint, undefined);
});
