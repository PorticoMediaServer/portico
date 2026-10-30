import test from 'node:test';
import assert from 'node:assert/strict';
import {createWebScreenRegistry} from '../src/route-registry.ts';
import {defineWebScreen} from '../src/route-registry.ts';

const screen = (id: 'home' | 'item', phoneChrome?: 'shell' | 'standalone') => ({default: defineWebScreen({id, component: () => null, ...(phoneChrome ? {phoneChrome} : {})})});

test('a screen owns its compact-phone chrome only when it asks to', () => {
  const registry = createWebScreenRegistry({'a.route.tsx': screen('home'), 'b.route.tsx': screen('item', 'standalone')});
  assert.equal(registry.phoneChrome('home'), 'shell');
  assert.equal(registry.phoneChrome('item'), 'standalone');
});

test('an unknown phone chrome value is rejected at registration', () => {
  assert.throws(() => defineWebScreen({id: 'item', component: () => null, phoneChrome: 'drawer' as never}), /invalid-phone-chrome|unknown/);
});
