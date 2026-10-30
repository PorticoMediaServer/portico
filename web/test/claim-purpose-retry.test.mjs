import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tree, all, textOf} from './helpers/interaction-harness.mjs';

// FX-207: the blocker's route shape, on the repository's own hook harness. The
// real ClaimApprovalRoute source is transpiled (compiled.code, never raw TS),
// its hooks and servicesI18n are the local imports a browser would use, and a
// fake lookup seam drives only that captured opening.

const raw = readFileSync(new URL('../src/services/ServicesApp.tsx', import.meta.url), 'utf8');
const source = raw.slice(raw.indexOf('/** The scrubbed code is an intake locator only.'));
const compiled = transformSync('claim-review.tsx', source, {jsx: {runtime: 'classic'}});
assert.deepEqual(compiled.errors, []);
const labeller = (key, values) => values?.time ? `${key}:${values.time}` : key;

function build(react) {
  return new Function('modules', `
    const React = modules.react.default;
    const {useState, useEffect, useLayoutEffect, useRef, useSyncExternalStore} = modules.react;
    const InvalidLink = modules.InvalidLink;
    const ServicesAuthStage = modules.ServicesAuthStage;
    const ServicesHeading = modules.ServicesHeading;
    const ServicesLead = modules.ServicesLead;
    const ServicesActions = modules.ServicesActions;
    const Button = modules.Button;
    const SetupApprovalPage = modules.SetupApprovalPage;
    const OwnerApprovalPage = modules.OwnerApprovalPage;
    const HostedSetupApproval = modules.HostedSetupApproval;
    const servicesI18n = modules.servicesI18n;
    const hostedClaimExpired = modules.hostedClaimExpired;
    ${compiled.code}
    return ClaimApprovalRoute;
  `)({
    react,
    InvalidLink: 'InvalidLink', ServicesAuthStage: 'Stage', ServicesHeading: 'Heading', ServicesLead: 'Lead',
    ServicesActions: 'Actions', Button: 'Button', SetupApprovalPage: 'SetupApproval', OwnerApprovalPage: 'OwnerApproval',
    HostedSetupApproval: 'HostedSetupApproval', servicesI18n: () => ({t: labeller}),
    hostedClaimExpired: error => error?.code === 'claim_expired',
  });
}

const session = (familyId = 'family') => ({account: {id: 'account'}, familyId, accessToken: 'access-' + familyId});

function accountFor(initial = session()) {
  const listeners = new Set();
  const state = {session: initial};
  const access = [];
  let accessHandler = async () => {};
  return {
    service: {
      subscribe: fn => {listeners.add(fn); return () => listeners.delete(fn);},
      getSnapshot: () => state,
      accessSession: (force, expected) => {access.push({force, expected}); return accessHandler(force, expected);},
    },
    access,
    setAccess: handler => {accessHandler = handler;},
    publish: next => {state.session = next; for (const fn of [...listeners]) fn();},
  };
}

function bootstrapFor(handler) {
  const signals = [];
  let reads = 0;
  return {
    get reads() {return reads;},
    signals,
    approvalPurpose: (received, signal) => {reads++; signals.push({signal, account: received}); return handler(reads, received, signal);},
  };
}

function mount({account, bootstrap}) {
  const h = createRenderer();
  const cleanups = new Set();
  const track = run => () => {
    const cleanup = run();
    if (typeof cleanup === 'function') cleanups.add(cleanup);
    return () => {cleanups.delete(cleanup); if (typeof cleanup === 'function') cleanup();};
  };
  const react = {
    ...h.react,
    useEffect: (run, deps) => h.react.useEffect(track(run), deps),
    useLayoutEffect: (run, deps) => h.react.useLayoutEffect(track(run), deps),
  };
  const Component = build(react);
  let props = {account, bootstrap};
  const render = () => tree(h.render(() => Component({account: props.account, bootstrap: props.bootstrap})));
  const settle = async (turns = 8) => {let value; for (let i = 0; i < turns; i++) {value = render(); await new Promise(resolve => setTimeout(resolve, 0));} return value;};
  return {render, settle, cleanups, set: next => {props = {...props, ...next};}, props: () => props};
}

const buttons = value => all(value, 'Button');
const oneButton = value => {
  assert.equal(buttons(value).length, 1);
  return buttons(value)[0];
};

test('one transient failure shows the drawn Retry and one press reaches the authored setup review', async () => {
  const account = accountFor();
  const bootstrap = bootstrapFor(reads => reads === 1 ? Promise.reject({status: 503, code: 'unavailable', retryable: true}) : Promise.resolve('setup'));
  const app = await mount({account, bootstrap});
  let value = await app.settle();
  assert.equal(all(value, 'InvalidLink').length, 0);
  assert.equal(all(value, 'SetupApproval').length, 0);
  assert.equal(textOf(oneButton(value)), 'next.states.tryAgain');
  assert.equal(bootstrap.signals[0].account, account);

  oneButton(value).props.onClick();
  value = app.render();                                   // claimed synchronously: the Retry is gone
  assert.equal(buttons(value).length, 0);
  assert.equal(all(value, 'SetupApproval').length, 0);
  value = await app.settle();
  assert.equal(bootstrap.reads, 2);
  assert.equal(all(value, 'SetupApproval').length, 1);
  assert.equal(all(value, 'OwnerApproval').length, 0);    // no setup -> switch fallback
});

test('concurrent and retained Retry clicks are one flight', async () => {
  const account = accountFor();
  let release;
  const held = new Promise(resolve => {release = resolve;});
  const bootstrap = bootstrapFor(reads => reads === 1 ? Promise.reject({status: 503, code: 'unavailable', retryable: true}) : held);
  const app = await mount({account, bootstrap});
  let value = await app.settle();
  const retry = oneButton(value);
  retry.props.onClick();
  retry.props.onClick();                                  // same retained node, second press
  value = await app.settle(1);
  assert.equal(bootstrap.reads, 2);
  assert.equal(buttons(value).length, 0);
  release('switch');
  value = await app.settle();
  assert.equal(bootstrap.reads, 2);
  assert.equal(all(value, 'OwnerApproval').length, 1);
});

test('a retained Retry cannot act under a newer bootstrap with the same scope', async () => {
  const account = accountFor();
  const first = bootstrapFor(() => Promise.reject({status: 503, code: 'unavailable', retryable: true}));
  const app = await mount({account, bootstrap: first});
  const stale = oneButton(await app.settle());

  let release;
  const held = new Promise(resolve => {release = resolve;});
  const second = bootstrapFor(() => held);
  app.set({bootstrap: second});
  let value = await app.settle(1);
  assert.equal(second.reads, 1);                           // the successor owns a new opening
  assert.equal(all(value, 'OwnerApproval').length, 0);
  assert.equal(all(value, 'SetupApproval').length, 0);     // no purpose from the retired opening

  stale.props.onClick();                                   // the retired closure
  await app.settle(1);
  assert.equal(first.reads, 1);
  assert.equal(second.reads, 1);
  release('switch');
  value = await app.settle();
  assert.equal(all(value, 'OwnerApproval').length, 1);
});

test('a replaced account opening retires its in-flight lookup and unmount aborts it', async () => {
  const first = accountFor(session('first-family'));
  let releaseFirst;
  const held = new Promise(resolve => {releaseFirst = resolve;});
  const firstBootstrap = bootstrapFor(() => held);
  const app = await mount({account: first, bootstrap: firstBootstrap});
  await app.settle(1);
  assert.equal(firstBootstrap.signals[0].signal.aborted, false);

  const second = accountFor(session('second-family'));
  const secondBootstrap = bootstrapFor(() => Promise.resolve('setup'));
  app.set({account: second, bootstrap: secondBootstrap});
  let value = await app.settle();
  assert.equal(firstBootstrap.signals[0].signal.aborted, true);   // layout cleanup
  releaseFirst('switch');                                         // a late original answer
  value = await app.settle();
  assert.equal(all(value, 'SetupApproval').length, 1);
  assert.equal(all(value, 'OwnerApproval').length, 0);            // never the retired purpose

  const third = accountFor(session('third-family'));
  const thirdBootstrap = bootstrapFor(() => new Promise(() => {}));
  app.set({account: third, bootstrap: thirdBootstrap});
  await app.settle(1);
  for (const cleanup of [...app.cleanups]) cleanup();
  assert.equal(thirdBootstrap.signals[0].signal.aborted, true);
});

test('a 401 reconciles the exact original family once and retries the lookup', async () => {
  const account = accountFor();
  const bootstrap = bootstrapFor(reads => reads === 1 ? Promise.reject({status: 401, code: 'account_signed_out', retryable: false}) : Promise.resolve('switch'));
  const app = await mount({account, bootstrap});
  let value = await app.settle();
  assert.deepEqual(account.access, [{force: true, expected: {accountId: 'account', familyId: 'family', accessToken: 'access-family'}}]);
  assert.equal(bootstrap.reads, 2);
  assert.equal(all(value, 'OwnerApproval').length, 1);
  assert.equal(all(value, 'InvalidLink').length, 0);
  assert.equal(buttons(value).length, 0);
});

test('a 401 that FX200 already renewed for the same family only repeats the lookup', async () => {
  const account = accountFor();
  let rejectFirst;
  const held = new Promise((_, reject) => {rejectFirst = reject;});
  const bootstrap = bootstrapFor(reads => reads === 1 ? held : Promise.resolve('setup'));
  const app = await mount({account, bootstrap});
  await app.settle(1);
  account.publish({...session('family'), accessToken: 'renewed-family'});   // canonical rotation committed
  rejectFirst({status: 401, code: 'account_signed_out', retryable: false});
  const value = await app.settle();
  assert.deepEqual(account.access, []);                    // no second rotation
  assert.equal(bootstrap.reads, 2);
  assert.equal(all(value, 'SetupApproval').length, 1);
  assert.equal(all(value, 'InvalidLink').length, 0);
});

test('a refused 401 rotation publishes no link error and lets the signed-out route take over', async () => {
  const account = accountFor();
  account.setAccess(async () => {account.publish(null); throw new Error('revoked');});
  const bootstrap = bootstrapFor(() => Promise.reject({status: 401, code: 'account_signed_out', retryable: false}));
  const app = await mount({account, bootstrap});
  const value = await app.settle();
  assert.deepEqual(account.access, [{force: true, expected: {accountId: 'account', familyId: 'family', accessToken: 'access-family'}}]);
  assert.equal(all(value, 'InvalidLink').length, 0);
  assert.equal(buttons(value).length, 0);
  assert.equal(all(value, 'SetupApproval').length + all(value, 'OwnerApproval').length, 0);
});

test('a 401 answered after a family replacement is never reconciled under the successor', async () => {
  const account = accountFor(session('family'));
  let rejectFirst;
  const held = new Promise((_, reject) => {rejectFirst = reject;});
  const bootstrap = bootstrapFor(reads => reads === 1 ? held : Promise.resolve('switch'));
  const app = await mount({account, bootstrap});
  await app.settle(1);
  account.publish(session('successor-family'));
  let value = await app.settle(1);
  assert.equal(bootstrap.reads, 2);
  rejectFirst({status: 401, code: 'account_signed_out', retryable: false});
  value = await app.settle();
  assert.deepEqual(account.access, []);                    // the retired opening never reconciles
  assert.equal(all(value, 'OwnerApproval').length, 1);
  assert.equal(all(value, 'InvalidLink').length, 0);
});

test('only a terminal capability answer is an Invalid link', async () => {
  for (const error of [
    {status: 400, code: 'invalid_return'},
    {status: 403, code: 'forbidden'},
    {status: 404, code: 'not_found'},
    {status: 400, code: 'invalid_link'},
  ]) {
    const account = accountFor();
    const bootstrap = bootstrapFor(() => Promise.reject(error));
    const app = await mount({account, bootstrap});
    const value = await app.settle();
    if (error.code === 'invalid_return') {
      assert.equal(all(value, 'InvalidLink').length, 1, error.code);
      assert.equal(buttons(value).length, 0, error.code);
    } else {
      assert.equal(all(value, 'InvalidLink').length, 0, error.code);
      assert.equal(textOf(oneButton(value)), 'next.states.tryAgain', error.code);
    }
  }
});

function fakeClock() {
  const real = {now: Date.now, setInterval: globalThis.setInterval, clearInterval: globalThis.clearInterval};
  let now = 1_000_000_000_000, id = 0;
  const timers = new Map();
  Date.now = () => now;
  globalThis.setInterval = fn => {const key = ++id; timers.set(key, fn); return key;};
  globalThis.clearInterval = key => {timers.delete(key);};
  return {
    at: () => now,
    advance: ms => {now += ms;},
    tick: () => {for (const fn of [...timers.values()]) fn();},
    timers,
    restore: () => {Date.now = real.now; globalThis.setInterval = real.setInterval; globalThis.clearInterval = real.clearInterval;},
  };
}

test('a 429 deadline is honoured exactly and never shortened', async () => {
  const clock = fakeClock();
  try {
    const account = accountFor();
    const bootstrap = bootstrapFor(reads => reads === 1
      ? Promise.reject({status: 429, code: 'rate_limited', retryable: true, retryAfterSeconds: 900})
      : Promise.resolve('setup'));
    const app = await mount({account, bootstrap});
    let value = await app.settle();
    const first = oneButton(value);
    assert.equal(first.props.disabled, true);
    assert.equal(textOf(first), 'services.signin.tryIn:15:00');

    clock.advance(899000);
    clock.tick();
    value = app.render();
    assert.equal(oneButton(value).props.disabled, true);   // a valid 900s hint is not capped to 300s
    oneButton(value).props.onClick();
    await app.settle(1);
    assert.equal(bootstrap.reads, 1);

    clock.advance(1000);
    clock.tick();
    value = app.render();
    assert.equal(oneButton(value).props.disabled, false);
    oneButton(value).props.onClick();
    value = await app.settle();
    assert.equal(bootstrap.reads, 2);
    assert.equal(all(value, 'SetupApproval').length, 1);
    assert.ok(bootstrap.signals.every(call => call.account === account));   // exact original port
    for (const cleanup of [...app.cleanups]) cleanup();
    assert.equal(clock.timers.size, 0);                    // the countdown timer was disposed
  } finally { clock.restore(); }
});

test('an unusable retry hint fails closed instead of shortening a deadline', async () => {
  const clock = fakeClock();
  try {
    for (const hint of [-5, 1e15, Number.POSITIVE_INFINITY, Number.NaN]) {
      const account = accountFor();
      const bootstrap = bootstrapFor(() => Promise.reject({status: 429, code: 'rate_limited', retryable: true, retryAfterSeconds: hint}));
      const app = await mount({account, bootstrap});
      const value = await app.settle();
      const blocked = oneButton(value);
      assert.equal(blocked.props.disabled, true, String(hint));
      assert.equal(textOf(blocked), 'next.states.tryAgain', String(hint));
      clock.advance(900000);
      blocked.props.onClick();                             // no representable deadline: no IO
      await app.settle(1);
      assert.equal(bootstrap.reads, 1, String(hint));
      for (const cleanup of [...app.cleanups]) cleanup();
    }
  } finally { clock.restore(); }
});

test('an old 503 Retry cannot bypass a newer 429 deadline on the same opening', async () => {
  const clock = fakeClock();
  try {
    const account = accountFor();
    let releaseSecond;
    const second = new Promise((_, reject) => {releaseSecond = reject;});
    const bootstrap = bootstrapFor(reads => reads === 1
      ? Promise.reject({status: 503, code: 'unavailable', retryable: true})
      : reads === 2 ? second : Promise.resolve('setup'));
    const app = await mount({account, bootstrap});
    const stale = oneButton(await app.settle());            // attempt 1: 503, retryAt 0
    stale.props.onClick();                                  // attempt 2 begins
    const settling = app.settle(1);
    releaseSecond({status: 429, code: 'rate_limited', retryable: true, retryAfterSeconds: 5});
    await settling;
    let value = await app.settle();
    assert.equal(bootstrap.reads, 2);
    assert.equal(oneButton(value).props.disabled, true);

    stale.props.onClick();                                  // attempt 1's closure, retryAt 0
    await app.settle(1);
    assert.equal(bootstrap.reads, 2);                       // the newer deadline is bound to attempt 2

    clock.advance(5000);
    clock.tick();
    value = app.render();
    oneButton(value).props.onClick();
    value = await app.settle();
    assert.equal(bootstrap.reads, 3);
    assert.equal(all(value, 'SetupApproval').length, 1);
  } finally { clock.restore(); }
});

test('a foreign family retires the opening even when the original family is published back', async () => {
  const account = accountFor();
  const bootstrap = bootstrapFor(() => Promise.reject({status: 503, code: 'unavailable', retryable: true}));
  const app = await mount({account, bootstrap});
  const stale = oneButton(await app.settle());
  assert.equal(bootstrap.signals.length, 1);

  account.publish(session('family-b'));                    // no render or layout cleanup between
  account.publish(session('family'));                      // B -> A batched back to the same scope
  stale.props.onClick();                                   // the held Retry, before any cleanup
  await app.settle(1);
  assert.equal(bootstrap.reads, 1);
  assert.equal(bootstrap.signals[0].signal.aborted, true); // exact pending request retired
  assert.equal(all(app.render(), 'InvalidLink').length, 0);
  for (const cleanup of [...app.cleanups]) cleanup();
});

test('a replaced parent opening shows no error of its own', async () => {
  const account = accountFor();
  const bootstrap = {approvalPurpose: () => {throw {code: 'claim_stale'};}};
  const app = await mount({account, bootstrap});
  const value = await app.settle();
  assert.equal(buttons(value).length, 0);
  assert.equal(all(value, 'InvalidLink').length, 0);
  assert.equal(all(value, 'SetupApproval').length + all(value, 'OwnerApproval').length, 0);
});
