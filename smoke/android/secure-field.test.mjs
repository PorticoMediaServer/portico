import {test} from 'node:test';
import assert from 'node:assert/strict';
import {securePasswordTarget} from './secure-field.mjs';
const appBundleId = 'tv.getportico.portico';
const options = {appBundleId, refsGeneration: 7};
const fixture = () => ({success: true, data: {appBundleId, refsGeneration: 7, truncated: false,
  nodes: [{ref: 'e9', bundleId: appBundleId, focused: true, editable: true, password: true,
    enabled: true, visibleToUser: true, hittable: true, rect: {x: 10, y: 20, width: 100, height: 40}}]}});
// Offline checkpoint fixtures, never a device capture or a credential-entry test.
test('authoritative secure field returns only ref, generation and bounds', () => {
  const value = fixture();
  value.data.nodes[0].label = 'ignored';
  value.data.nodes[0].value = 'ignored';
  assert.deepEqual(securePasswordTarget(value, options), {
    ref: 'e9', refsGeneration: 7, rect: {x: 10, y: 20, width: 100, height: 40},
  });
});
for (const key of ['focused', 'editable', 'password', 'enabled', 'visibleToUser', 'hittable']) {
  test(`missing native ${key} metadata fails closed`, () => {
    const value = fixture(); delete value.data.nodes[0][key];
    assert.throws(() => securePasswordTarget(value, options), /metadata unavailable or ambiguous/);
  });
}
for (const change of [
  value => { value.success = false; },
  value => { value.data.truncated = true; },
  value => { value.data.refsGeneration = 6; },
  value => { value.data.appBundleId = 'other.app'; },
  value => { value.data.nodes[0].bundleId = 'other.app'; },
  value => { value.data.nodes[0].password = false; },
  value => { value.data.nodes[0].interactionBlocked = true; },
  value => { value.data.nodes[0].rect.width = 0; },
  value => { value.data.nodes.push({...value.data.nodes[0], ref: 'e10'}); },
]) {
  test('incomplete, stale, foreign or ambiguous snapshot refuses selection', () => {
    const value = fixture(); change(value);
    assert.throws(() => securePasswordTarget(value, options), /metadata unavailable or ambiguous/);
  });
}
