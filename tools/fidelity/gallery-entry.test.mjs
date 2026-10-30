import test from 'node:test';
import assert from 'node:assert/strict';
import {controlledEntry, verifyGalleryEntry, settleMotion} from './gallery-entry.mjs';

test('only exact controlled Kit entries are admitted, with no external/auth/default case fallback', async () => {
  const entry = '/dev/kit?family=live-rules&case=editor&capture=true';
  assert.equal(controlledEntry(entry).gallery, true);
  for (const path of ['https://other.invalid/dev/kit?family=x&case=y&capture=true', '/live?tab=recordings', '/dev/kit?capture=true', entry + '&server=private']) assert.throws(() => controlledEntry(path));
  await verifyGalleryEntry({evaluate: async () => ({families: 1, cases: 1, width: 1440, height: 950})}, entry, {width: 1440, height: 950});
  await assert.rejects(verifyGalleryEntry({evaluate: async () => ({families: 1, cases: 0})}, entry, {width: 1440, height: 950}));
  await assert.rejects(verifyGalleryEntry({evaluate: async () => ({families: 1, cases: 1, width: 320, height: 240})}, entry, {width: 1440, height: 950}));
});
test('motion readiness observes animations rather than mutating the specimen', async () => {
  await settleMotion({evaluate: async expression => {
    assert.ok(expression.includes('document.getAnimations()')); assert.ok(expression.includes('value.finished'));
    assert.ok(!expression.includes('.style=') && !expression.includes('.pause(') && !expression.includes('.cancel('));
  }});
});
test('finite motion readiness is bounded and scoped to the selected frame, not other contact-sheet frames', async () => {
  const target = {}, outside = {}, awaited = [];
  const root = {contains: value => value === target};
  const animation = (node, endTime) => ({playState: 'running', effect: {target: node, getComputedTiming: () => ({endTime})},
    get finished() {awaited.push(node); return Promise.resolve();}});
  const document = {querySelectorAll: selector => selector === '#selected' ? [root] : [], getAnimations: () => [animation(target, 11000), animation(outside, Infinity)]};
  const browser = {evaluate: expression => Function('document', 'requestAnimationFrame', `return ${expression}`)(document, callback => queueMicrotask(callback))};
  await settleMotion(browser, {selector: '#selected', timeoutMs: 15000});
  assert.deepEqual(awaited, [target]);
  await assert.rejects(settleMotion(browser, {selector: '#missing'}), /unique/);
  await assert.rejects(settleMotion(browser, {selector: '#selected', timeoutMs: 30001}), /bounded/);
  document.getAnimations = () => [animation(target, Infinity)];
  await assert.rejects(settleMotion(browser, {selector: '#selected'}), /infinite/);
});
