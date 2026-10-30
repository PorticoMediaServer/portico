import test from 'node:test';
import assert from 'node:assert/strict';
import {drawnActionAllowed, openingActionAllowed, sourceAdmissionAllowed} from '../src/screens/live/owner/dialog-lifetime.ts';

// Authored UNRUN vectors. They model the rendered flows through the same guard functions the
// components use; they are not executed here and claim no runtime or browser result.

type Token = {live: boolean};
const opening = (token: Token) => ({token});
const source = (current: boolean) => ({current: () => current});

test('retained LAN Confirm cannot authorize the newer roots it did not draw', () => {
  const drawn = {confirm: ['192.168.1.20'], preview: undefined as {channels: number} | undefined};
  const newer = {confirm: ['192.168.1.99'], preview: undefined as {channels: number} | undefined};
  // The render drew `drawn`; the controller published `newer` before the click.
  assert.equal(drawnActionAllowed(newer, drawn, newer.confirm === drawn.confirm), false);
  assert.equal(drawnActionAllowed(drawn, drawn, drawn.confirm === drawn.confirm), true);
});

test('retained Save cannot commit the successor preview in the same opening', () => {
  const drawn = {confirm: undefined as string[] | undefined, preview: {channels: 4}};
  const newer = {confirm: undefined as string[] | undefined, preview: {channels: 9}};
  assert.equal(drawnActionAllowed(newer, drawn, newer.preview === drawn.preview), false);
  // Upload submit carries the same rule; a submit drawn with no preview may only check.
  assert.equal(drawnActionAllowed(newer, drawn, false), false);
  assert.equal(drawnActionAllowed(drawn, drawn, drawn.preview === drawn.preview), true);
});

test('same-tick A to B opening: A may not act once B is the event-owned modal', () => {
  const tokenA: Token = {live: true}, tokenB: Token = {live: true};
  const openA = opening(tokenA), openB = opening(tokenB);
  tokenA.live = false; // the parent's replace event retires A before setModal(B)
  assert.equal(openingActionAllowed(openA, openB), false);
  assert.equal(openingActionAllowed(openA, openA), false);
  assert.equal(openingActionAllowed(openB, openB), true);
});

test('old close/saved completion cannot clear or act on a successor modal', () => {
  const tokenA: Token = {live: true}, tokenB: Token = {live: true};
  const openA = opening(tokenA), openB = opening(tokenB);
  // Opening A was current, then the parent replaced it with B and retired A.
  tokenA.live = false;
  assert.equal(openingActionAllowed(openA, openB), false);
  assert.equal(openingActionAllowed(openA, openA), false);
  assert.equal(openingActionAllowed(openB, openB), true);
});

test('source replacement retires the parent view admission, including old open/close handlers', () => {
  const first = source(true), second = source(true);
  const admissionA = {token: {} as object, source: first};
  const admissionB = {token: {} as object, source: second};
  assert.equal(sourceAdmissionAllowed(admissionA, admissionA.token, first), true);
  // A retained open/close handler runs after the source was replaced but before passive disposal:
  // the live token now belongs to B, so A's admission is dead even though first.current() is true.
  assert.equal(sourceAdmissionAllowed(admissionA, admissionB.token, second), false);
  assert.equal(first.current(), true);
  assert.equal(sourceAdmissionAllowed(admissionB, admissionB.token, second), true);
});

test('Strict replay renews the source admission and refuses the spent one', () => {
  const view = source(true);
  const spent = {token: {} as object, source: view};
  // Layout cleanup clears the live token; setup mints a new admission for the same source.
  const renewed = {token: {} as object, source: view};
  assert.equal(sourceAdmissionAllowed(spent, undefined, view), false);
  assert.equal(sourceAdmissionAllowed(renewed, renewed.token, view), true);
  assert.equal(sourceAdmissionAllowed(spent, renewed.token, view), false);
});

test('a retired opening token cannot be revived by an old render', () => {
  const token: Token = {live: true};
  const open = opening(token);
  token.live = false;
  assert.equal(openingActionAllowed(open, open), false);
  // A successor has its own token; adopting the old modal object cannot make it live again.
  const successor = opening({live: true});
  assert.equal(openingActionAllowed(open, successor), false);
  assert.equal(openingActionAllowed(successor, successor), true);
});
