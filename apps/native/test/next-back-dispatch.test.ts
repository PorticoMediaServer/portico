import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// TV-FOCUS-REVIEW step 3: Back has one listener and walks scopes from the top of the screen down
// (observer, overlay, surface, page, shell), newest first inside a scope, page steps only while their page is
// on screen. React Native's own order (last subscribed first) let a hidden page swallow Back (NT-1).

async function dispatcher() {
  const subscriptions: (() => boolean)[] = [];
  const BackHandler = {addEventListener: (_name: string, handler: () => boolean) => {subscriptions.push(handler); return {remove: () => {subscriptions.splice(subscriptions.indexOf(handler), 1);}};}};
  const module = await screenModule(new URL('../src-next/kit/navigation/back.ts', import.meta.url), {react: {default: {}}, 'react-native': {BackHandler}});
  const press = () => subscriptions.reduceRight((handled, handler) => handled || handler(), false);
  return {...module, press, subscriptions};
}

test('one platform subscription however many steps register, released with the last', async () => {
  const back = await dispatcher();
  const off = [back.registerBack(() => false, {scope: 'shell'}), back.registerBack(() => false, {scope: 'overlay'}), back.registerBack(() => false, {scope: 'page', active: () => true})];
  assert.equal(back.subscriptions.length, 1);
  off.forEach(release => release());
  assert.equal(back.subscriptions.length, 0);
  assert.equal(back.backRegistrations(), 0);
});

test('scopes run from the top of the screen down whatever order they registered in; newest first within a scope', async () => {
  const back = await dispatcher(), calls: string[] = [];
  const step = (name: string, consumes: boolean) => () => {calls.push(name); return consumes;};
  // Registration order is deliberately the reverse of screen order.
  back.registerBack(step('shell', true), {scope: 'shell'});
  back.registerBack(step('page', false), {scope: 'page', active: () => true});
  back.registerBack(step('player', false), {scope: 'surface'});
  back.registerBack(step('menu', false), {scope: 'overlay'});
  back.registerBack(step('newer menu', false), {scope: 'overlay'});
  back.registerBack(step('seek stops', true), {scope: 'observer'});
  assert.equal(back.press(), true);
  assert.deepEqual(calls, ['seek stops', 'newer menu', 'menu', 'player', 'page', 'shell'], 'an observer never consumes');
});

test('NT-1: a retained page that is not on screen never takes Back from the visible one', async () => {
  const back = await dispatcher(), calls: string[] = [];
  let liveVisible = true;
  back.registerBack(() => {calls.push('shell'); return true;}, {scope: 'shell'});
  // Live TV on a tablet with a programme selected; then the viewer switches to Libraries.
  back.registerBack(() => {calls.push('live clears its hidden selection'); return true;}, {scope: 'page', active: () => liveVisible});
  liveVisible = false;
  back.press();
  assert.deepEqual(calls, ['shell'], 'the visible page\'s Back runs on the first press');
  liveVisible = true; calls.length = 0;
  back.press();
  assert.deepEqual(calls, ['live clears its hidden selection']);
});

test('a page step must say when its page is on screen', async () => {
  const back = await dispatcher();
  assert.throws(() => back.registerBack(() => true, {scope: 'page'}), /on screen/);
});

test('a step released during dispatch is skipped; nothing consuming lets the platform Back run', async () => {
  const back = await dispatcher(), calls: string[] = [];
  let releaseLower: () => void = () => {};
  back.registerBack(() => {calls.push('upper'); releaseLower(); return false;}, {scope: 'overlay'});
  releaseLower = back.registerBack(() => {calls.push('lower'); return true;}, {scope: 'surface'});
  assert.equal(back.press(), false);
  assert.deepEqual(calls, ['upper']);
});
