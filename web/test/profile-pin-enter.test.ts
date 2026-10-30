import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {enterContinuesProfile, profileContinueEnabled} from '../src/shell/profile-pin-enter.ts';

test('Enter continues a PIN profile only with four digits and nothing in flight', () => {
  const enter = {key: 'Enter'};
  assert.equal(enterContinuesProfile(enter, true, '1234', false), true);
  assert.equal(enterContinuesProfile(enter, true, '123', false), false);
  assert.equal(enterContinuesProfile(enter, true, '1234', true), false);
  assert.equal(enterContinuesProfile({key: 'Enter', isComposing: true}, true, '1234', false), false);
  assert.equal(enterContinuesProfile({key: 'a'}, true, '1234', false), false);
});

test('the error retry of a PIN-less profile continues on Enter too, matching the Continue button', () => {
  assert.equal(profileContinueEnabled(false, '', false), true);
  assert.equal(profileContinueEnabled(undefined, '', true), false);
});

test('the shell dialog wires Enter on the PIN field to the same choose call as Continue', () => {
  const shell = readFileSync(new URL('../src/shell/Shell.tsx', import.meta.url), 'utf8');
  assert.match(shell, /onKeyDown=\{event=>\{if\(profileChoice&&enterContinuesProfile\(/);
  assert.match(shell, /disabled=\{!profileContinueEnabled\(/);
});
