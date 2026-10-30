import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tick} from './helpers/interaction-harness.mjs';

/**
 * FX-156 / BLK-WEB-001 focused receipt test. It loads the actual `ChildRouteError` source through
 * the existing component harness, with controlled router hooks and the existing `StateLayout` port
 * (the presenter's primary/secondary actions), so the exact committed Retry/Home closures are
 * captured without React internals. The router's invalidate is a real deferred promise, and the
 * reset port is an explicit spy.
 *
 * This is the proof that an old receipt cannot reset a successor after a scope or error replacement.
 * The genuine router specimen keeps the natural-recovery smoke cases; the installed CatchBoundary
 * auto-clears a changed match there, so that specimen cannot prove the original reset stayed at zero.
 * Authored only — not executed.
 */

class Component {
  constructor(props) { this.props = props; this.state = {}; }
  setState(next) { this.state = {...this.state, ...(typeof next === 'function' ? next(this.state) : next)}; }
}

async function harness() {
  const h = createRenderer();
  const actions = [];
  const resets = [];
  const navigations = [];
  const invalidations = [];
  let location = {href: '/search?probe=1', state: {__TSR_key: 'entry-1'}};
  let matches = [];
  let scope = 'scope-1';
  let current = true;
  const router = {
    get state() { return {location, matches}; },
    invalidate(options) { return new Promise((resolve, reject) => invalidations.push({options, resolve, reject})); },
    navigate(target) { navigations.push(target); return Promise.resolve(); },
  };
  const binding = {get scopeKey() { return scope; }};
  const screen = {binding, context: {isCurrent: () => current}};
  const StateLayout = props => {
    actions.push({retry: props.primary?.onPress, home: props.secondary?.onPress});
    return {type: 'StateLayout', props};
  };
  const {ChildRouteError} = await componentModule(new URL('../src/shell/DestinationError.tsx', import.meta.url), {
    react: {...h.react, Component},
    '@tanstack/react-router': {useRouter: () => router, useRouterState: ({select}) => select(router.state)},
    '@i18n': {defaultI18n: {t: key => key}},
    '../kit/states': {StateLayout},
    '../route-registry-view': {useWebScreenContext: () => screen},
    '../router': {router},
  });
  // Two passes: the first mints/publishes the layout receipt, the second exposes the committed
  // closures bound to that published receipt.
  const render = (error, reset) => {
    h.render(() => ChildRouteError({error, reset}));
    h.render(() => ChildRouteError({error, reset}));
    return actions.at(-1);
  };
  return {
    actions, resets, navigations, invalidations, render,
    setMatches: value => { matches = value; },
    setScope: value => { scope = value; },
    setCurrent: value => { current = value; },
  };
}

const stored = (error) => [{id: 'search', routeId: '/search', status: 'error', error}];
const recovered = [{id: 'search', routeId: '/search', status: 'success'}];

test('a successor error at the same location and scope refuses the old receipt before layout', async () => {
  const h = await harness();
  const errorA = new Error('validation-fault-a');
  const resetA = () => { h.resets.push('A'); };
  h.setMatches(stored(errorA));
  const old = h.render(errorA, resetA);

  const errorB = new Error('validation-fault-b');
  // The router acquires successor error B while A's published receipt is still the live render.
  h.setMatches(stored(errorB));
  old.retry();
  old.home();
  assert.equal(h.invalidations.length, 0, 'the old Retry never starts an invalidation for successor B');
  assert.equal(h.navigations.length, 0, 'the old Home never navigates for successor B');
  assert.deepEqual(h.resets, [], 'the old reset stays at zero');

  const resetB = () => { h.resets.push('B'); };
  const fresh = h.render(errorB, resetB);
  fresh.retry();
  assert.equal(h.invalidations.length, 1, 'the successor page starts its own invalidation');
  h.setMatches(recovered);
  h.invalidations[0].resolve();
  await tick();
  assert.deepEqual(h.resets, ['B'], 'only the successor receipt resets');
  fresh.home();
  assert.equal(h.navigations.length, 1, 'the successor Home navigates');
});

test('a deferred invalidation across a scope replacement cannot reset the successor', async () => {
  const h = await harness();
  const errorA = new Error('validation-fault-a');
  const resetA = () => { h.resets.push('A'); };
  h.setMatches(stored(errorA));
  const old = h.render(errorA, resetA);

  old.retry();
  assert.equal(h.invalidations.length, 1, 'the old Retry starts a deferred invalidation');

  // The scope is replaced while the invalidation is pending: the old receipt is dead before layout.
  h.setScope('scope-2');
  const fresh = h.render(errorA, resetA);
  h.invalidations[0].resolve();
  await tick();
  assert.deepEqual(h.resets, [], 'the old settlement never resets the successor scope');

  old.retry();
  old.home();
  assert.equal(h.invalidations.length, 1, 'the old Retry cannot start another invalidation');
  assert.equal(h.navigations.length, 0, 'the old Home cannot navigate for the successor scope');

  fresh.retry();
  assert.equal(h.invalidations.length, 2, 'the successor page starts its own invalidation');
  h.setMatches(recovered);
  h.invalidations[1].resolve();
  await tick();
  assert.deepEqual(h.resets, ['A'], 'only the successor receipt resets');
  fresh.home();
  assert.equal(h.navigations.length, 1, 'the successor Home navigates');
});

test('a deferred invalidation across an error replacement cannot reset the successor', async () => {
  const h = await harness();
  const errorA = new Error('validation-fault-a');
  const resetA = () => { h.resets.push('A'); };
  h.setMatches(stored(errorA));
  const old = h.render(errorA, resetA);

  old.retry();
  assert.equal(h.invalidations.length, 1, 'the old Retry starts a deferred invalidation');

  // The router acquires successor error B and the page re-renders with it before the settlement.
  const errorB = new Error('validation-fault-b');
  h.setMatches(stored(errorB));
  const resetB = () => { h.resets.push('B'); };
  const fresh = h.render(errorB, resetB);
  h.invalidations[0].resolve();
  await tick();
  assert.deepEqual(h.resets, [], 'the old settlement never resets the successor error');

  old.retry();
  old.home();
  assert.equal(h.invalidations.length, 1, 'the old Retry cannot start another invalidation');
  assert.equal(h.navigations.length, 0, 'the old Home cannot navigate for the successor error');

  fresh.retry();
  assert.equal(h.invalidations.length, 2, 'the successor page starts its own invalidation');
  h.setMatches(recovered);
  h.invalidations[1].resolve();
  await tick();
  assert.deepEqual(h.resets, ['B'], 'only the successor receipt resets');
  fresh.home();
  assert.equal(h.navigations.length, 1, 'the successor Home navigates');
});
