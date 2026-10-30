import test from 'node:test';
import assert from 'node:assert/strict';
import {bindShellRouteMotion, finishRouteMotion, pageViewTransition, routeMotionOwnedByBrowser} from '../src/shell/route-motion.ts';

function browser(userAgent: string) {
  const events = new EventTarget();
  Object.assign(events, {navigator: {userAgent}, navigation: new EventTarget()});
  return events as unknown as Window;
}
test('an iPhone traversal suppresses both router and fallback until the content commit', () => {
  const updates: boolean[] = [], page = browser('iPhone Version/18.0 Mobile Safari/604.1');
  const off = bindShellRouteMotion({update: value => { updates.push(value.defaultViewTransition !== false); }}, page);
  page.dispatchEvent(new Event('popstate'));
  assert.equal(routeMotionOwnedByBrowser(), true);
  assert.deepEqual(updates, [false]);
  finishRouteMotion();
  assert.equal(routeMotionOwnedByBrowser(), false);
  assert.deepEqual(updates, [false, true]);
  off();
});
test('explicit browser visual ownership suppresses motion on any supported platform', () => {
  const updates: boolean[] = [], page = browser('Desktop Firefox');
  const off = bindShellRouteMotion({update: value => { updates.push(value.defaultViewTransition !== false); }}, page);
  (page as Window & {navigation: EventTarget}).navigation.dispatchEvent(Object.assign(new Event('navigate'), {hasUAVisualTransition: true, navigationType: 'traverse'}));
  assert.equal(routeMotionOwnedByBrowser(), true);
  finishRouteMotion();
  assert.deepEqual(updates, [false, true]);
  off();
});
test('normal history on desktop retains View Transitions and cleanup removes all listeners', () => {
  const updates: boolean[] = [], page = browser('Desktop Safari');
  const off = bindShellRouteMotion({update: value => { updates.push(value.defaultViewTransition !== false); }}, page);
  page.dispatchEvent(new Event('popstate'));
  assert.equal(routeMotionOwnedByBrowser(), false);
  assert.deepEqual(updates, []);
  off();
  const after = updates.length;
  page.dispatchEvent(Object.assign(new Event('popstate'), {hasUAVisualTransition: true}));
  assert.equal(updates.length, after);
  assert.equal(routeMotionOwnedByBrowser(), false);
});

test('initial motion waits for an actual rendered route and retires its listener', () => {
  const updates: boolean[] = [];let rendered: (() => void) | undefined;
  const router = {state: {matches: [] as unknown[]}, update: (value: {defaultViewTransition: unknown}) => {updates.push(value.defaultViewTransition !== false);},
    subscribe: (_event: 'onRendered', listener: () => void) => {rendered = listener;return () => {rendered = undefined;};}};
  const off = bindShellRouteMotion(router, browser('Desktop Chrome'));
  assert.deepEqual(updates, []);router.state.matches.push({routeId: '__root__'});rendered?.();
  assert.deepEqual(updates, [true]);off();assert.equal(rendered, undefined);
});
test('motion binding after the first real commit still enables later native transitions', () => {
  const updates: boolean[] = [];
  const off = bindShellRouteMotion({state: {matches: [{}]}, update: value => {updates.push(value.defaultViewTransition !== false);}}, browser('Desktop Chrome'));
  assert.deepEqual(updates, [true]);off();
});
test('page changes run the page transition; a same-page change (a tab, a section, a query) keeps the page still', () => {
  const values: unknown[] = [];
  const off = bindShellRouteMotion({state: {matches: [{}]}, update: value => {values.push(value.defaultViewTransition);}}, browser('Desktop Chrome'));
  assert.equal(values.at(-1), pageViewTransition);
  assert.deepEqual(pageViewTransition.types({pathChanged: true}), ['page']);
  assert.equal(pageViewTransition.types({pathChanged: false}), false);
  off();
});
