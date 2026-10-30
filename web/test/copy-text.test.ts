import test from 'node:test';
import assert from 'node:assert/strict';
import {copyText} from '../src/platform/copy-text.ts';

// Deterministic DOM/clipboard ports establish gesture ordering and cleanup,
// not native Safari clipboard acceptance.
function fixture(t: import('node:test').TestContext, legacy: boolean | Error = true, modern = false) {
  const events: string[] = [], writes: string[] = [], fields: Field[] = [];
  let selected: Field | undefined;
  const modal = {appendChild(field: Field) {events.push('append:modal');field.isConnected = true;}};
  class Element {
    isConnected = true;
    focus() {events.push(this === focused ? 'focus:original' : 'focus:field');}
    closest() {return modal;}
  }
  class Field extends Element {
    value = '';readOnly = false;tabIndex = 0;style = {cssText: ''};
    selectionStart = 2;selectionEnd = 5;selectionDirection = 'backward';
    select() {selected = this;events.push('select');}
    setSelectionRange(start: number, end: number, direction?: string) {this.selectionStart = start;this.selectionEnd = end;this.selectionDirection = direction ?? 'none';}
    remove() {this.isConnected = false;events.push('remove');}
  }
  const focused = new Field(), originalRange = {cloneRange() {return this;}};
  const selection = {rangeCount: 1, getRangeAt: () => originalRange, removeAllRanges() {events.push('clear:ranges');}, addRange(range: unknown) {assert.equal(range, originalRange);events.push('restore:range');}};
  const document = {activeElement: focused, getSelection: () => selection, body: {appendChild() {assert.fail('must stay within modal');}},
    createElement(name: string) {assert.equal(name, 'textarea');const field = new Field();fields.push(field);return field;},
    execCommand(command: string) {
      events.push(command);assert.equal(command, 'copy');assert.ok(selected?.isConnected);assert.equal(selected?.readOnly, true);
      assert.equal(selected?.selectionStart, 0);assert.equal(selected?.selectionEnd, selected?.value.length);
      if (legacy instanceof Error) throw legacy;
      if (legacy) writes.push(selected!.value);
      return legacy;
    }};
  const values = {document, navigator: {clipboard: modern ? {writeText(text: string) {events.push('modern');writes.push(text);return Promise.resolve();}} : undefined}, HTMLElement: Element, HTMLInputElement: Field, HTMLTextAreaElement: Field};
  for (const [key, value] of Object.entries(values)) {
    const before = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, {configurable: true, value});
    t.after(() => {if (before) Object.defineProperty(globalThis, key, before);else delete (globalThis as any)[key];});
  }
  return {events, writes, fields, focused, document, modal};
}

test('HTTP selection copy runs in the gesture, erases temporary text and restores focus/selection', async t => {
  const f = fixture(t);
  const pending = copyText('https://example.test/join#capability');
  assert.deepEqual(f.writes, ['https://example.test/join#capability'], 'copy must occur before the first promise turn');
  assert.deepEqual(f.events, ['append:modal', 'focus:field', 'select', 'copy', 'remove', 'focus:original', 'clear:ranges', 'restore:range']);
  assert.equal(f.fields[0].value, '');assert.equal(f.fields[0].isConnected, false);
  assert.deepEqual([f.focused.selectionStart, f.focused.selectionEnd, f.focused.selectionDirection], [2, 5, 'backward']);
  await pending;
});

for (const legacy of [false, new Error('command refused')]) test(`modern write is invoked in the same gesture after legacy ${legacy === false ? 'false' : 'throw'}`, async t => {
  const f = fixture(t, legacy, true), pending = copyText('prepared');
  assert.deepEqual(f.writes, ['prepared']);assert.equal(f.events.at(-1), 'modern');
  assert.ok(f.events.indexOf('copy') < f.events.indexOf('modern'));assert.equal(f.fields[0].value, '');
  await pending;
});

test('absent legacy command invokes modern write directly', async t => {
  const f = fixture(t, false, true);
  (f.document as any).execCommand = undefined;
  const pending = copyText('prepared');assert.deepEqual(f.events, ['modern']);await pending;
});

test('refusal is reported rather than claiming success and still erases temporary text', async t => {
  const f = fixture(t, false);
  await assert.rejects(copyText('private'), /clipboard_unavailable/);
  assert.equal(f.fields[0].value, '');assert.equal(f.fields[0].isConnected, false);assert.deepEqual(f.writes, []);
});

test('an unfocused Safari button keeps the fallback inside its own modal', async t => {
  const f = fixture(t);
  f.document.activeElement.closest = () => undefined as any;
  const source = {closest: () => f.modal} as unknown as HTMLElement;
  const pending = copyText('ready', source);
  assert.equal(f.events[0], 'append:modal');assert.deepEqual(f.writes, ['ready']);await pending;
});

test('async clipboard rejection is preserved without a late selection attempt', async t => {
  const f = fixture(t, false, true), refusal = new Error('permission denied');
  Object.defineProperty(navigator, 'clipboard', {value: {writeText: () => Promise.reject(refusal)}});
  await assert.rejects(copyText('ready'), error => error === refusal);
  assert.equal(f.events.filter(event => event === 'copy').length, 1);
  assert.equal(f.fields[0].value, '');
});
