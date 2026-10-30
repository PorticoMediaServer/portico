import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeExternalPresentationBinding} from '../src-next/platform/playback/external-presentation.ts';

const binding = {lease: 'lease', intentId: 7, sessionId: 'session', generation: 3, itemId: 'item'};
test('external acknowledgement distinguishes old binaries from explicit native unready and copies exact current identity', () => {
  assert.equal(nativeExternalPresentationBinding({binding}, binding), undefined);
  assert.equal(nativeExternalPresentationBinding({binding, externalPresentationBinding: null}, binding), null);
  const value = {...binding}, accepted = nativeExternalPresentationBinding({binding, externalPresentationBinding: value}, binding);
  assert.deepEqual(accepted, binding); assert.ok(Object.isFrozen(accepted));
  value.generation++; assert.equal(accepted?.generation, 3);
});
test('external acknowledgement cannot cross any lease, intent, session, generation or item boundary', () => {
  for (const [field, value] of Object.entries(binding)) {
    const changed = {...binding, [field]: typeof value === 'number' ? value + 1 : value + '-old'};
    assert.equal(nativeExternalPresentationBinding({binding, externalPresentationBinding: changed}, binding), null, field);
    assert.equal(nativeExternalPresentationBinding({binding: changed, externalPresentationBinding: binding}, binding), null, field + ' fact');
  }
  assert.equal(nativeExternalPresentationBinding({binding, externalPresentationBinding: binding}, undefined), null);
  assert.equal(nativeExternalPresentationBinding({binding, externalPresentationBinding: {...binding, possible: true}}, binding), null);
});
