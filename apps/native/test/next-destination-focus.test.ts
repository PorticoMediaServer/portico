import test from 'node:test';
import assert from 'node:assert/strict';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createDestinationEntryFocus, nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';
import {tvFocusIds} from '../src-next/shell/tv-focus.ts';

test('entry waits for the real native card host, then requests it once for the navigation intent', () => {
  const registry = createFocusRegistry(), request = createDestinationEntryFocus(registry);
  const activation = {visible: true, intent: 'enter', revision: 1} as const;
  const address = {regionId: 'actual-library-region', childId: JSON.stringify(['movie', 'actual-library', 'real-id'])};
  let requests = 0;
  assert.equal(request(activation, undefined), false); assert.equal(request(activation, address), false);
  registry.configure({id: address.regionId, entry: address.childId, entryPolicy: 'entry'});
  registry.attach(address, {requestTVFocus: () => {requests++;}});
  assert.equal(request(activation, address), true); assert.equal(requests, 1);
  assert.equal(request(activation, address), false); assert.equal(requests, 1);
  assert.equal(request({...activation, revision: 2, intent: 'restore'}, address), false);
  assert.equal(request({...activation, revision: 2, intent: 'restore'}, {...address, childId: 'new-content'}), false);
  assert.equal(requests, 1, 'Back and content updates preserve the retained page');
  assert.equal(request({...activation, revision: 3, visible: false, intent: 'top'}, address), false);
  assert.equal(request({...activation, revision: 3, intent: 'top'}, address), true); assert.equal(requests, 2);
});

test('destination boundaries use the actual authority rail and keep real library identities distinct', () => {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home-fence'};
  const ids = nativeDestinationFocusIds(authority);
  assert.equal(ids.rail, tvFocusIds(authority).rail);
  assert.notEqual(ids.libraryCard('one', 'same-id'), ids.libraryCard('two', 'same-id'));
  assert.notEqual(ids.searchKeyboard, nativeDestinationFocusIds({...authority, epoch: 'new-login'}).searchKeyboard);
  assert.ok(!ids.searchKeyboard.includes('.home.'));
});

test('retained Search roots mount independently and share the actual authority rail', () => {
  const authority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'f'};
  const first = nativeDestinationFocusIds(authority, 'search:1'), second = nativeDestinationFocusIds(authority, 'search:2');
  const registry = createFocusRegistry(), releases: (() => void)[] = [];
  for (const ids of [first, second]) for (const id of [ids.searchKeyboard, ids.searchChips, ids.searchResults]) releases.push(registry.mountRegion(id, {}));
  assert.notEqual(first.searchKeyboard, second.searchKeyboard);
  assert.equal(first.rail, second.rail); assert.equal(first.rail, tvFocusIds(authority).rail);
  assert.throws(() => registry.mountRegion(first.searchKeyboard, {}), /Duplicate live focus region/);
  releases.forEach(release => release());
});
