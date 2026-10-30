import test from 'node:test';
import assert from 'node:assert/strict';
import {bindShellRouteMotion, finishRouteMotion, routeMotionOwnedByBrowser} from '../src/shell/route-motion.ts';

/** Minimal actual Window shape the binder uses: navigator identity, popstate and
 * the optional Navigation API event target. Nothing here invents UA acceptance. */
function browser(userAgent: string) {
  const page = new EventTarget();
  const navigation = new EventTarget();
  Object.assign(page, {navigator: {userAgent}, navigation});
  return {page: page as unknown as Window, navigation};
}

function router() {
  const updates: boolean[] = [], rendered: Array<() => void> = [];
  return {
    updates,
    rendered: () => rendered.slice(),
    fireRendered: () => {for (const listener of [...rendered]) listener();},
    router: {
      state: {matches: [] as readonly unknown[]},
      update: (value: {defaultViewTransition: unknown}) => {updates.push(value.defaultViewTransition !== false);},
      subscribe: (_event: 'onRendered', listener: () => void) => {
        rendered.push(listener);
        return () => {const index = rendered.indexOf(listener); if (index >= 0) rendered.splice(index, 1);};
      }
    }
  };
}

const traversal = (hasUAVisualTransition?: boolean, navigationType = 'traverse') =>
  Object.assign(new Event('navigate'), {hasUAVisualTransition, navigationType});

test('UA-owned traversal suppresses each owned route and the layout finish releases it', () => {
  const {page, navigation} = browser('iPhone Version/18.0 Mobile Safari/604.1'), fixture = router();
  let off: (() => void) | undefined;
  try {
    off = bindShellRouteMotion(fixture.router, page);
    page.dispatchEvent(new Event('popstate'));
    assert.equal(routeMotionOwnedByBrowser(), true);
    assert.deepEqual(fixture.updates, [false], 'the owned traversal disables Portico motion before any render');
    assert.equal(fixture.rendered().length, 1, 'one captured rendered subscription exists');
    navigation.dispatchEvent(traversal(true, 'push'));
    assert.deepEqual(fixture.updates, [false, false], 'each owned navigation asks for default motion off, without stacking an owner');
    assert.equal(routeMotionOwnedByBrowser(), true);
    fixture.fireRendered();
    assert.deepEqual(fixture.updates, [false, false], 'an owned rendered commit does not release; the layout finish does');
    assert.equal(routeMotionOwnedByBrowser(), true);
    finishRouteMotion();
    assert.equal(routeMotionOwnedByBrowser(), false);
    assert.deepEqual(fixture.updates, [false, false, true], 'the actual layout finish restores default view transitions once');
  } finally {
    off?.();
    finishRouteMotion();
  }
});

test('ordinary history and an unowned rendered commit follow the binder source', () => {
  const {page} = browser('Desktop Firefox'), fixture = router();
  let off: (() => void) | undefined;
  try {
    off = bindShellRouteMotion(fixture.router, page);
    page.dispatchEvent(new Event('popstate'));
    assert.equal(routeMotionOwnedByBrowser(), false);
    assert.deepEqual(fixture.updates, [], 'a normal push/replace history entry leaves Portico motion alone');
    fixture.fireRendered();
    assert.deepEqual(fixture.updates, [true], 'an unowned rendered commit does release default view transitions');
  } finally {
    off?.();
    finishRouteMotion();
  }
});

test('a captured old rendered callback cannot release a successor owner', () => {
  const {page, navigation} = browser('Desktop Safari'), older = router(), newer = router();
  let offOlder: (() => void) | undefined, offNewer: (() => void) | undefined;
  try {
    offOlder = bindShellRouteMotion(older.router, page);
    navigation.dispatchEvent(traversal(true));
    assert.equal(routeMotionOwnedByBrowser(), true);
    assert.deepEqual(older.updates, [false]);
    const oldRendered = older.rendered()[0];
    assert.ok(oldRendered, 'the older binder registered one rendered callback');

    offNewer = bindShellRouteMotion(newer.router, page);
    navigation.dispatchEvent(traversal(true));
    assert.deepEqual(newer.updates, [false], 'the successor suppresses for its own route');
    assert.equal(routeMotionOwnedByBrowser(), true);

    offOlder();
    oldRendered();
    assert.deepEqual(newer.updates, [false], 'the captured old callback cannot release the owned successor');
    assert.equal(routeMotionOwnedByBrowser(), true);
    assert.deepEqual(older.rendered(), [], 'the older subscription was actually removed');

    newer.fireRendered();
    assert.deepEqual(newer.updates, [false], 'the successor rendered commit does not release while it is owned');
    finishRouteMotion();
    assert.equal(routeMotionOwnedByBrowser(), false);
    assert.deepEqual(newer.updates, [false, true], 'the successor layout finish releases its own suppression once');
  } finally {
    offOlder?.();
    offNewer?.();
    finishRouteMotion();
  }
});

test('retiring the final binder removes its signals and leaves ownership released', () => {
  const {page, navigation} = browser('Desktop Safari'), fixture = router();
  let off: (() => void) | undefined;
  try {
    off = bindShellRouteMotion(fixture.router, page);
    navigation.dispatchEvent(traversal(true));
    assert.equal(routeMotionOwnedByBrowser(), true);
    assert.deepEqual(fixture.updates, [false]);
    off();
    off = undefined;
    const released = fixture.updates.slice();
    page.dispatchEvent(new Event('popstate'));
    navigation.dispatchEvent(traversal(true));
    finishRouteMotion();
    assert.deepEqual(fixture.updates, released, 'a retired final binder emits no further updates');
    assert.equal(routeMotionOwnedByBrowser(), false);
    assert.equal(fixture.rendered().length, 0, 'no rendered subscription survives retirement');
  } finally {
    off?.();
    finishRouteMotion();
  }
});
