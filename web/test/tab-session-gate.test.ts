import test from 'node:test';
import assert from 'node:assert/strict';

// The tab’s own sessionStorage survives reloads without being shared with other tabs.
class DeviceStorage {
  values = new Map<string, string>();
  getItem(key: string) {return this.values.get(key) ?? null;}
  setItem(key: string, value: string) {this.values.set(key, value);}
  removeItem(key: string) {this.values.delete(key);}
}
const gate = await import('../src/platform/tab-session-gate.ts');
const held = {serverId: 's1', profileId: 'alex'};

test('a page load stays with the profile it opened, including across rotation', () => {
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false);
  gate.admitProfile('s1', 'alex');
  assert.equal(gate.tabAdmits(held), true);
  assert.equal(gate.tabAdmits({...held, profileId: 'parent'}), false);
  assert.equal(gate.admittedProfile('s1'), 'alex');
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false);
});

test('a reload keeps the profile this tab had open; signing out or another profile does not', () => {
  const own = new DeviceStorage();
  (globalThis as {sessionStorage?: unknown}).sessionStorage = own;
  try {
    gate.resetTabSessionGate();
    assert.equal(gate.tabHadOpen(held), false);
    gate.admitProfile('s1', 'alex');
    gate.resetTabSessionGate();
    assert.equal(gate.tabAdmits(held), false, 'the caller checks device memory or PIN policy at launch');
    assert.equal(gate.tabHadOpen(held), true);
    assert.equal(gate.tabHadOpen({...held, profileId: 'kids'}), false);
    gate.forgetTabServer('s1');
    assert.equal(gate.tabHadOpen(held), false);
  } finally {delete (globalThis as {sessionStorage?: unknown}).sessionStorage;}
});
