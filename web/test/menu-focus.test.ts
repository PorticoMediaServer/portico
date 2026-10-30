import test from 'node:test';
import assert from 'node:assert/strict';
import {menuInitialFocus, recoverMenuChildFocus} from '../src/kit/overlays/menu-focus.ts';

function fixture() {
  const body = {}, outside = {}, old = {};
  const focused: string[] = [];
  const row = {hasAttribute: () => true, focus() {focused.push('row');}};
  const heading = {hasAttribute: () => false, tabIndex: 0, focus() {focused.push('heading');}};
  const doc = {body, activeElement: body};
  const state = {retained: false, heading: true};
  const root = {ownerDocument: doc, contains: (node: unknown) => node === old && state.retained,
    querySelector: (selector: string) => selector.startsWith('h1') ? state.heading ? heading : null : row};
  return {root: root as unknown as HTMLElement, old: old as HTMLElement, doc, state, outside, row, heading, focused};
}
test('every menu opening chooses the first enabled action regardless of input modality', () => {
  const f = fixture();assert.equal(menuInitialFocus(f.root), f.row);
});
test('replacing a focused menu row hands focus to the child heading, then falls back to an action', () => {
  const f = fixture();recoverMenuChildFocus(f.root, f.old);
  assert.deepEqual(f.focused, ['heading']);assert.equal(f.heading.tabIndex, -1);
  f.state.heading = false;recoverMenuChildFocus(f.root, f.old);
  assert.deepEqual(f.focused, ['heading', 'row']);
});
test('retained focus, an outside target, and an unopened menu do not get focus stolen', () => {
  const f = fixture();f.state.retained = true;recoverMenuChildFocus(f.root, f.old);
  f.state.retained = false;f.doc.activeElement = f.outside;recoverMenuChildFocus(f.root, f.old);
  f.doc.activeElement = f.doc.body;recoverMenuChildFocus(f.root, null);
  assert.deepEqual(f.focused, []);
});
