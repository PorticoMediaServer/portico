import test from 'node:test';
import assert from 'node:assert/strict';
import {applyControlledState, checkControlledState} from './controlled-state.mjs';

test('keyboard focus uses one ordinary Tab and refuses a different reached target', async () => {
  const calls = [], browser = {evaluate: async expression => expression.startsWith('document.activeElement') ? true :
    {count: 1, visible: true, disabled: false, focused: false}, call: async (...args) => calls.push(args)};
  await applyControlledState(browser, [{action: 'tab', selector: 'button'}]);
  assert.deepEqual(calls.map(value => [value[0], value[1].type, value[1].key]),
    [['Input.dispatchKeyEvent', 'keyDown', 'Tab'], ['Input.dispatchKeyEvent', 'keyUp', 'Tab']]);
  await assert.rejects(applyControlledState({...browser, evaluate: async expression => expression.startsWith('document.activeElement') ? false :
    {count: 1, visible: true, disabled: false}}, [{action: 'tab', selector: 'button'}]), /did not focus/);
});

test('public fixture state uses real click/text events and never rewrites DOM or invokes focus', async () => {
  const calls = [];
  const browser = {evaluate: async expression => {assert.ok(!expression.includes('.focus(')); return {count: 1, visible: true, disabled: false, focused: true, x: 5, y: 8};}, call: async (...args) => calls.push(args)};
  await applyControlledState(browser, [{action: 'click', selector: 'input'}, {action: 'insertText', selector: 'input', text: 'public-fixture'}]);
  assert.deepEqual(calls.map(value => value[0]), ['Input.dispatchMouseEvent', 'Input.dispatchMouseEvent', 'Input.insertText']);
  await assert.rejects(applyControlledState({...browser, evaluate: async () => ({count: 2})}, [{action: 'click', selector: 'input'}]));
  await assert.rejects(applyControlledState({...browser, evaluate: async () => ({count: 1, visible: true, focused: false})}, [{action: 'insertText', selector: 'input', text: 'public-fixture'}]));
  await assert.rejects(applyControlledState(browser, [{action: 'evaluate', selector: 'input'}]));
});
test('unrelated Kit states do not inherit auth4 password/caret requirements', () => {
  assert.deepEqual(checkControlledState({elements: []}), {differences: [], carets: []});
  const actual = {documentFocused: true, elements: [{frame: 'password', focused: true, input: {type: 'password', length: 10, selectionStart: 10, selectionEnd: 10}, style: {color: 'rgb(237,245,250)', caretColor: 'rgb(237,245,250)'}}]};
  const expected = [{frame: 'password', focused: true, input: {type: 'password', length: 10}, nativeCaret: {selection: 10}}];
  assert.equal(checkControlledState(actual, expected).differences.length, 0);
  actual.elements[0].input.type = 'text';
  assert.equal(checkControlledState(actual, expected).differences.length, 1);
});
