import test from 'node:test';
import assert from 'node:assert/strict';

// The browser's localStorage: shared by its tabs, kept across reloads and restarts.
class DeviceStorage {
  values = new Map<string, string>();
  getItem(key: string) {return this.values.get(key) ?? null;}
  setItem(key: string, value: string) {this.values.set(key, value);}
  removeItem(key: string) {this.values.delete(key);}
}
const tab = new DeviceStorage();
(globalThis as {localStorage?: unknown}).localStorage = tab;
const gate = await import('../src/platform/tab-session-gate.ts');
const held = {serverId: 's1', familyId: 'f1', profileId: 'alex'};
const future = new Date(Date.now() + 86_400_000).toISOString();

test('a tab opens unasked only what it remembered or already opened in this page load', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  assert.equal(gate.tabAdmits(held), false, 'nothing remembered, nothing opened');
  gate.recordTabPick('s1', 'alex', {...held, expiresAt: future});
  assert.equal(gate.tabAdmits(held), true);
  // A reload keeps localStorage and loses what the page load had opened.
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), true, 'remembered on this device survives a reload');
  assert.equal(gate.tabAdmits({...held, familyId: 'f2'}), false, 'another sign-in (another tab picked) is not remembered here');
  assert.equal(gate.tabAdmits({...held, profileId: 'kids'}), false);
  assert.equal(gate.tabAdmits(held, Date.parse(future) + 1), false, 'not after it expires');
});

test('a page load follows later sign-ins to the profile it opened, and never to another profile', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.admitProfile('s1', 'alex');
  assert.equal(gate.tabAdmits({...held, familyId: 'another-tab-picked'}), true);
  assert.equal(gate.tabAdmits({...held, familyId: 'another-tab-picked', profileId: 'parent'}), false, 'a different profile asks');
  assert.equal(gate.admittedProfile('s1'), 'alex', 'and the picker can focus the profile this tab had open');
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits({...held, familyId: 'another-tab-picked'}), false, 'the next reload asks');
});

test('a pick without remembering, a sign-out or a PIN asked again stop remembering on this device', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.recordTabPick('s1', 'alex', {...held, expiresAt: future});
  gate.recordTabPick('s1', 'alex', undefined);
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false);
  gate.recordTabPick('s1', 'alex', {...held, expiresAt: future});
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

test('another tab, or the browser opened again, opens the remembered profile', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  gate.recordTabPick('s1', 'alex', {...held, expiresAt: future});
  gate.resetTabSessionGate(); // a new page load: nothing opened yet, the shared storage still holds it
  assert.equal(gate.tabAdmits(held), true);
});

test('Forget remembered profile clears the choice on this device and keeps the profile open now', () => {
  gate.resetTabSessionGate(); tab.values.clear();
  assert.equal(gate.deviceRemembers('s1', 'alex'), false);
  gate.recordTabPick('s1', 'alex', {...held, expiresAt: future});
  assert.equal(gate.deviceRemembers('s1', 'alex'), true);
  assert.equal(gate.deviceRemembers('s1', 'kids'), false, 'only the remembered profile offers Forget');
  assert.equal(gate.deviceRemembers('s1', 'alex', Date.parse(future) + 1), false, 'nor once it has expired');
  gate.forgetRememberedProfile('s1');
  assert.equal(gate.deviceRemembers('s1', 'alex'), false);
  assert.equal(gate.tabAdmits(held), true, 'this page load keeps the profile it opened');
  gate.resetTabSessionGate();
  assert.equal(gate.tabAdmits(held), false, 'the next visit shows the picker');
});

// PR-4: Forget remembered profile is offered on the server's clock, as the launch gate decides, never the device's.
test('a device clock that runs fast still offers Forget for a profile the server remembers', async () => {
  const {observeServerTime} = await import('@core/server-clock.ts');
  const skew = 20 * 60_000, device = Date.now();
  gate.resetTabSessionGate(); tab.values.clear();
  observeServerTime('http://server.test', device - skew, device, device); // the server is 20 minutes behind this device
  try {
    const marker = {...held, expiresAt: new Date(device - skew + 10 * 60_000).toISOString()}; // 10 minutes left on the server's clock
    gate.recordTabPick('s1', 'alex', marker);
    gate.resetTabSessionGate();
    assert.equal(gate.tabAdmits(held), true, 'the launch gate still opens it');
    assert.equal(gate.deviceRemembers('s1', 'alex'), true, 'and the avatar menu agrees');
  } finally {observeServerTime('http://server.test', Date.now(), Date.now(), Date.now());}
});
