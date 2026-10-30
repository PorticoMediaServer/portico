import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, all, textOf} from './helpers/interaction-harness.mjs';
import {AccountSessionReviewService} from '../../packages/client-core/src/account-sessions.ts';

// FX-201: the blocker's shape. Renders the real DevicesPanel and the real
// AccountSessionReviewService against a fake request seam. Every DELETE is
// explicit so a test can prove which family was asked for, and how many times.

const created = '2026-10-01T10:00:00Z', expires = '2026-11-01T10:00:00Z';
const observed = '2026-10-26T10:00:00Z', revokedAt = '2026-10-26T10:00:00Z';
const row = (id, current = false) => ({id, mode: current ? 'browser' : 'native', createdAt: created, expiresAt: expires, status: 'active', current});
const REVOKED = {status: 'revoked', revokedAt};

function makeFixture({rows, second, deleteRequest, othersRequest, family = 'current', accountId = 'account'}) {
  const items = rows.map(item => ({...item}));
  const more = second ? second.map(item => ({...item})) : undefined;
  const calls = {deletes: [], reads: [], others: []};
  const find = id => [...items, ...(more || [])].find(item => item.id === id);
  const revoke = id => Object.assign(find(id), REVOKED);
  const page = (list, nextCursor) => ({accountId, currentFamilyId: family, observedAt: observed, nextCursor, items: list.map(item => ({...item}))});
  const request = async (path, method, body) => {
    if (method === 'POST' && path === '/v1/account/sessions/revoke-others') {
      calls.others.push(body.operationId);
      return othersRequest(body, {revoke, items, more});
    }
    if (method !== 'DELETE') {
      calls.reads.push(path);
      return path.includes('cursor=') ? page(more || [], '') : page(items, more ? 'next' : '');
    }
    const id = decodeURIComponent(path.slice(path.lastIndexOf('/') + 1));
    calls.deletes.push(id);
    return deleteRequest(id, {revoke, items, more});
  };
  const service = new AccountSessionReviewService({scope: {accountId, familyId: family}, api: {request}});
  const api = {sessions: () => service};
  return {service, calls, revoke, api};
}

// The panel is rendered through its own hooks, not by copying them: the resource
// hook is transpiled from its real source with the renderer's React.
async function mount(fixture) {
  const h = createRenderer();
  const cleanups = new Set();
  const react = {...h.react, useLayoutEffect: (run, deps) => h.react.useLayoutEffect(() => {
    const cleanup = run();
    if (typeof cleanup === 'function') cleanups.add(cleanup);
    return () => {cleanups.delete(cleanup); if (typeof cleanup === 'function') cleanup();};
  }, deps)};
  const resource = await componentModule(new URL('../src/services/use-account-resource.ts', import.meta.url), {react});
  const m = await componentModule(new URL('../src/services/DevicesPanel.tsx', import.meta.url), {
    react,
    '@core/index.ts': {ApiError: class ApiError extends Error {}},
    './use-account-resource': resource,
    './i18n': {servicesI18n: () => ({t: key => key})},
    './time': {servicesRelativeTime: () => 'recently', servicesSignInDate: () => '1 Oct 2026'},
    './services.module.css': {default: {pager: 'pager', overlaySpacing: 'spacing'}},
    '../kit/accounts/ServicesAuth': {HostedError: 'HostedError', ServicesActions: 'Actions', ServicesQuiet: 'Quiet', ServicesNotice: 'Notice'},
    '../kit/accounts/ServicesWorkspace': {ServicesBadge: 'Badge', ServicesDangerAction: 'DangerAction', ServicesDeviceFacts: 'Facts', ServicesGroup: 'Group', ServicesWorkspaceRow: 'Row'},
    '../kit/controls': {Button: 'Button'},
    '../kit/overlays': {Dialog: props => ({type: 'Dialog', props: {title: props.title, open: props.open, children: props.open ? [props.footer] : null}})},
  });
  let api = fixture.api;
  const frame = (children, actions) => ({type: 'Frame', props: {children: [children, actions]}});
  const render = () => tree(h.render(() => m.DevicesPanel({api, frame})));
  const settle = async (turns = 12) => {let value; for (let i = 0; i < turns; i++) {value = render(); await new Promise(resolve => setTimeout(resolve, 0));} return value;};
  return {render, settle, cleanups, replace: next => {api = next.api;}};
}

const buttons = (rendered, label) => all(rendered, 'Button').filter(node => textOf(node) === label);
const one = (rendered, label) => {
  const found = buttons(rendered, label);
  assert.equal(found.length, 1, label);
  return found[0];
};
const openDialog = (rendered, title) => {
  const found = all(rendered, 'Dialog').filter(node => node.props.open && node.props.title === title);
  assert.equal(found.length, 1, title);
  return found[0];
};
const confirm = (rendered, title) => {
  const found = all(tree(openDialog(rendered, title)), 'DangerAction');
  assert.equal(found.length, 1, title);
  return found[0];
};
const signOutFirstRow = async app => {
  let value = await app.settle();
  buttons(value, 'action.signOut')[0].props.onClick();
  value = await app.settle(2);
  confirm(value, 'web.hostedAccount.signOutDeviceTitle').props.onClick();
  return app.settle();
};

test('one temporary revoke failure closes ordinary revokes until the authored Retry settles the same family', async () => {
  let failures = 1, finish;
  const fixture = makeFixture({
    rows: [row('current', true), row('other'), row('second')],
    deleteRequest: (id, {revoke}) => {
      if (failures > 0) { failures--; throw {status: 503, code: 'unavailable', retryable: true}; }
      return new Promise(resolve => {finish = () => {revoke(id); resolve({id, revoked: true, current: false});};});
    },
  });
  const app = await mount(fixture);
  let value = await signOutFirstRow(app);

  assert.deepEqual(fixture.calls.deletes, ['other']);
  assert.equal(fixture.service.getSnapshot().retryId, 'other');
  assert.ok(fixture.service.getSnapshot().mutationError);
  assert.equal(fixture.service.getSnapshot().items.find(item => item.id === 'current').status, 'active');
  // Ordinary individual and bulk revokes are closed while the family is unresolved.
  assert.deepEqual(buttons(value, 'action.signOut').map(node => node.props.disabled), [true, true]);
  assert.equal(one(value, 'web.hostedAccount.signOutOthers').props.disabled, true);

  one(value, 'next.states.tryAgain').props.onClick();
  value = await app.settle(2);
  assert.deepEqual(fixture.calls.deletes, ['other', 'other']);   // exactly the retained family, once
  assert.equal(typeof finish, 'function');
  assert.deepEqual(buttons(value, 'action.signOut').map(node => node.props.disabled), [true, true]); // pending
  assert.equal(one(value, 'web.hostedAccount.signOutOthers').props.disabled, true);

  finish();
  value = await app.settle();
  assert.equal(fixture.calls.deletes.length, 2);
  assert.equal(fixture.service.getSnapshot().retryId, null);
  assert.equal(fixture.service.getSnapshot().mutationError, null);
  assert.equal(buttons(value, 'next.states.tryAgain').length, 0);
  assert.deepEqual(buttons(value, 'action.signOut').map(node => node.props.disabled), [false]); // only "second" is left
  assert.equal(one(value, 'web.hostedAccount.signOutOthers').props.disabled, false);
  assert.equal(fixture.service.getSnapshot().items.find(item => item.id === 'current').status, 'active');
  assert.equal(fixture.calls.deletes.includes('current'), false);
});

test('a committed DELETE whose response was lost retries the same idempotent receipt', async () => {
  let lost = true;
  const fixture = makeFixture({
    rows: [row('current', true), row('other')],
    deleteRequest: (id, {revoke}) => {
      if (lost) { lost = false; revoke(id); throw {code: 'timeout', retryable: true}; }
      return {id, revoked: true, current: false};
    },
  });
  const app = await mount(fixture);
  let value = await signOutFirstRow(app);

  assert.deepEqual(fixture.calls.deletes, ['other']);
  assert.equal(fixture.service.getSnapshot().retryId, 'other');
  // The read-back already shows the committed revoke; the UI must not claim failure.
  assert.equal(fixture.service.getSnapshot().items.find(item => item.id === 'other').status, 'revoked');
  assert.ok(fixture.service.getSnapshot().mutationError);

  one(value, 'next.states.tryAgain').props.onClick();
  value = await app.settle();
  assert.deepEqual(fixture.calls.deletes, ['other', 'other']);
  assert.equal(fixture.service.getSnapshot().retryId, null);
  assert.equal(fixture.service.getSnapshot().mutationError, null);
  assert.equal(buttons(value, 'next.states.tryAgain').length, 0);
  assert.equal(fixture.service.getSnapshot().items.find(item => item.id === 'current').status, 'active');
});

test('sign out all other devices is one atomic request, and an unconfirmed outcome retries that same operation', async () => {
  let failures = 1;
  const fixture = makeFixture({
    rows: [row('current', true), row('a'), row('b')],
    othersRequest: ({operationId}, {revoke, items}) => {
      if (failures > 0) { failures--; throw {status: 503, code: 'unavailable', retryable: true}; }
      for (const item of items) if (!item.current) revoke(item.id);
      return {accountId: 'account', currentFamilyId: 'current', operationId, revokedCount: 2};
    },
  });
  const app = await mount(fixture);
  let value = await app.settle();
  one(value, 'web.hostedAccount.signOutOthers').props.onClick();
  value = await app.settle(2);
  confirm(value, 'web.hostedAccount.signOutOthersTitle').props.onClick();
  value = await app.settle();

  assert.deepEqual(fixture.calls.deletes, []);                 // never one device at a time
  assert.equal(fixture.calls.others.length, 1);
  assert.equal(fixture.service.getSnapshot().retryOthers, true);
  assert.deepEqual(buttons(value, 'action.signOut').map(node => node.props.disabled), [true, true]);
  assert.equal(one(value, 'web.hostedAccount.signOutOthers').props.disabled, true);

  one(value, 'next.states.tryAgain').props.onClick();
  value = await app.settle();
  assert.equal(fixture.calls.others.length, 2);
  assert.equal(fixture.calls.others[0], fixture.calls.others[1]);   // the same operation, replayed
  assert.equal(fixture.service.getSnapshot().retryOthers, false);
  assert.equal(fixture.service.getSnapshot().mutationError, null);
  assert.deepEqual(fixture.service.getSnapshot().items.filter(item => item.status === 'active').map(item => item.id), ['current']);
  assert.ok(all(value, 'Notice').length > 0);
  assert.deepEqual(fixture.calls.deletes, []);

  // A new request is a new operation.
  one(value, 'web.hostedAccount.signOutOthers').props.onClick();
  value = await app.settle(2);
  confirm(value, 'web.hostedAccount.signOutOthersTitle').props.onClick();
  await app.settle();
  assert.equal(fixture.calls.others.length, 3);
  assert.notEqual(fixture.calls.others[2], fixture.calls.others[0]);
});

test('a receipt that keeps another sign-in is not a confirmation', async () => {
  const fixture = makeFixture({
    rows: [row('current', true), row('other')],
    othersRequest: ({operationId}) => ({accountId: 'account', currentFamilyId: 'other', operationId, revokedCount: 1}),
  });
  const app = await mount(fixture);
  let value = await app.settle();
  one(value, 'web.hostedAccount.signOutOthers').props.onClick();
  value = await app.settle(2);
  confirm(value, 'web.hostedAccount.signOutOthersTitle').props.onClick();
  value = await app.settle();
  assert.equal(fixture.service.getSnapshot().mutationError?.code, 'invalid_session_review');
  assert.equal(fixture.service.getSnapshot().retryOthers, true);
  assert.equal(all(value, 'Notice').length, 0);
});

test('a nonretryable refusal offers no Retry and leaves ordinary revokes available', async () => {
  const fixture = makeFixture({
    rows: [row('current', true), row('other')],
    deleteRequest: () => {throw {status: 403, code: 'forbidden', retryable: false};},
  });
  const app = await mount(fixture);
  const value = await signOutFirstRow(app);

  assert.deepEqual(fixture.calls.deletes, ['other']);
  assert.equal(fixture.service.getSnapshot().retryId, null);
  assert.equal(fixture.service.getSnapshot().mutationError.code, 'forbidden');
  assert.equal(buttons(value, 'next.states.tryAgain').length, 0);
  assert.ok(all(value, 'HostedError').length > 0);
  assert.equal(one(value, 'action.signOut').props.disabled, false);
  assert.equal(one(value, 'web.hostedAccount.signOutOthers').props.disabled, false);
});

test('a stale Retry cannot act for a replaced account or an unmounted panel', async () => {
  const retired = makeFixture({
    rows: [row('current', true), row('other')],
    deleteRequest: () => {throw {status: 503, code: 'unavailable', retryable: true};},
  });
  const app = await mount(retired);
  const stale = one(await signOutFirstRow(app), 'next.states.tryAgain').props.onClick;
  assert.deepEqual(retired.calls.deletes, ['other']);

  const successor = makeFixture({
    // The successor owns the current row itself: scope familyId must match its id.
    rows: [row('next-family', true), row('other')],
    family: 'next-family',
    deleteRequest: () => ({id: 'other', revoked: true, current: false}),
  });
  app.replace(successor);
  const admitted = await app.settle();
  assert.equal(successor.service.getSnapshot().phase, 'ready');
  assert.deepEqual(
    [successor.service.getSnapshot().scope.accountId, successor.service.getSnapshot().scope.familyId],
    ['account', 'next-family'],
  );
  assert.equal(successor.service.getSnapshot().items.find(item => item.current)?.id, 'next-family');
  assert.equal(all(admitted, 'Row').length, 2);   // the successor panel is genuinely listed
  stale();                                    // the retired opening's handler
  await app.settle(2);
  assert.deepEqual(retired.calls.deletes, ['other']);
  assert.deepEqual(successor.calls.deletes, []);

  const unmounted = makeFixture({
    rows: [row('current', true), row('other')],
    deleteRequest: () => {throw {status: 503, code: 'unavailable', retryable: true};},
  });
  const panel = await mount(unmounted);
  const held = one(await signOutFirstRow(panel), 'next.states.tryAgain').props.onClick;
  for (const cleanup of [...panel.cleanups]) cleanup();
  held();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(unmounted.calls.deletes, ['other']);
});

test('an ordinary successful revoke and read-only pagination still work', async () => {
  const rows = [row('current', true), ...Array.from({length: 39}, (_, at) => row('other-' + at))];
  const fixture = makeFixture({
    rows,
    second: [row('tail')],
    deleteRequest: (id, {revoke}) => {revoke(id); return {id, revoked: true, current: false};},
  });
  const app = await mount(fixture);
  let value = await app.settle();
  assert.equal(all(value, 'Row').length, 40);

  value = await signOutFirstRow(app);
  assert.deepEqual(fixture.calls.deletes, ['other-0']);
  assert.equal(all(value, 'Row').length, 39);
  assert.equal(fixture.service.getSnapshot().mutationError, null);
  assert.equal(fixture.service.getSnapshot().retryId, null);
  assert.equal(fixture.calls.deletes.includes('current'), false);
  assert.ok(all(value, 'Notice').length > 0);

  one(value, 'web.hostedAccount.older').props.onClick();
  value = await app.settle();
  assert.equal(all(value, 'Row').length, 1);
  assert.equal(fixture.service.getSnapshot().hasPrevious, true);
  assert.equal(fixture.calls.reads.at(-1).includes('cursor='), true);

  one(value, 'web.hostedAccount.newer').props.onClick();
  value = await app.settle();
  assert.equal(all(value, 'Row').length, 39);
  assert.equal(fixture.service.getSnapshot().hasPrevious, false);
});
