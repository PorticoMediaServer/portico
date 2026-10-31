import test from 'node:test';
import assert from 'node:assert/strict';

// A browser tab's sessionStorage: one per tab, kept across reloads, never shared.
class TabStorage {
  values = new Map<string, string>();
  getItem(key: string) {return this.values.get(key) ?? null;}
  setItem(key: string, value: string) {this.values.set(key, value);}
  removeItem(key: string) {this.values.delete(key);}
}
const tab = new TabStorage();
(globalThis as {sessionStorage?: unknown}).sessionStorage = tab;
const gate = await import('../src/platform/tab-session-gate.ts');
const held = {serverId: 's1', familyId: 'f1', profileId: 'alex'};
const future = new Date(Date.now() + 86_400_000).toISOString();

test('a tab opens unasked only what it remembered or already opened in this page load', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  assert.equal(gate.tabAdmits(held), false, 'nothing remembered, nothing opened');
  gate.recordTabPick('s1', {...held, expiresAt: future});
  assert.equal(gate.tabAdmits(held), true);
  // A reload keeps sessionStorage and loses what the page load had opened.
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), true, 'remembered in this tab survives a reload');
  assert.equal(gate.tabAdmits({...held, familyId: 'f2'}), false, 'another sign-in (another tab picked) is not remembered here');
  assert.equal(gate.tabAdmits({...held, profileId: 'kids'}), false);
  assert.equal(gate.tabAdmits(held, Date.parse(future) + 1), false, 'not after it expires');
});

test('a page load follows later sign-ins on a server it opened, without asking again', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.admitServer('s1');
  assert.equal(gate.tabAdmits({...held, familyId: 'another-tab-picked'}), true);
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits({...held, familyId: 'another-tab-picked'}), false, 'the next reload asks');
});

test('a pick without remembering, a sign-out or a PIN asked again stop remembering in this tab', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.recordTabPick('s1', {...held, expiresAt: future});
  gate.recordTabPick('s1', undefined);
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false);
  gate.recordTabPick('s1', {...held, expiresAt: future});
  gate.forgetTabServer('s1');
  assert.equal(gate.tabAdmits(held), false);
});

test('an unreadable marker is not a marker, and is neither thrown nor erased (R-23)', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  tab.setItem('portico.remembered-profiles.v1', JSON.stringify({s1: {serverId: 's1', familyId: 'f1', profileId: 'alex', expiresAt: 'later', from: 'a newer app'}, s2: 7}));
  assert.equal(gate.tabAdmits(held), false);
  assert.equal(gate.tabAdmits({serverId: 's2', familyId: 'x', profileId: 'y'}), false);
  assert.ok(tab.getItem('portico.remembered-profiles.v1')?.includes('a newer app'), 'left as it was');
  tab.setItem('portico.remembered-profiles.v1', '{not json');
  assert.equal(gate.tabAdmits(held), false);
});

test('a new tab starts with nothing remembered', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.recordTabPick('s1', {...held, expiresAt: future});
  const other = new TabStorage();
  (globalThis as {sessionStorage?: unknown}).sessionStorage = other;
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false);
  (globalThis as {sessionStorage?: unknown}).sessionStorage = tab;
});
