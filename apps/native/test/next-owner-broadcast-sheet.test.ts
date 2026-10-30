import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks, nodes, content, t, theme} from './helpers/owner-server-settings.mjs';

const panelLogic = await screenModule(new URL('../../../packages/client-core/src/server-admin/panel-logic.ts', import.meta.url), {
  react: {default: {}}, '../../../contracts/src/routes.ts': {routePath: (id: string) => id}, '../../../contracts/src/route-paths.ts': {routePath: (id: string) => id}, '../freshness-actions.ts': {ALREADY_DONE: Symbol('already-done')},
});
const ownerGuard = await screenModule(new URL('../src-next/screens/settings-live/server/storage/space-source.ts', import.meta.url), {
  react: {default: {}}, '../../../../../../../packages/contracts/src/decoders': {decodeGetAdminStorageUsageResponse: assert.fail, decodeGetDataStorageUsageResponse: assert.fail},
  '../../../../../../../packages/contracts/src/routes': {routePath: assert.fail}, '../../lifetime': {NativeScreenLifetime: class {constructor() {assert.fail('unused source');}}, screenScopeEnded: assert.fail},
});
const settle = () => new Promise(resolve => setImmediate(resolve));
function deferred() {let resolve!: (value?: unknown) => void, reject!: (error: unknown) => void;const promise = new Promise((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}
const button = (tree: unknown, label: string) => nodes(tree).find((node: any) => node.type === 'Button' && content(node) === label);
const field = (tree: unknown, label: string) => nodes(tree).find((node: any) => node.type === 'TextField' && node.props.label === label);

async function fixture() {
  const host = lifecycleHooks(), requests: any[] = [], closes: boolean[] = [], errors: unknown[] = [], scopes = new WeakMap();
  const retirement = new AbortController(); let open = false, operation = 0;
  const makeOwner = () => {
    let active = true;const listeners = new Set<() => void>();
    const authority = {serverId: 'server-fixture', accountId: 'owner-fixture', profileId: 'primary-fixture'};
    const context = {current: () => active, authority: () => authority, subscribeAuthority: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
      account: {serverId: authority.serverId, account: {id: authority.accountId, role: 'owner', primaryProfileId: authority.profileId, disabled: false}, capabilities: {manageServer: true}}};
    const scope = {lifetime: {current: () => active}, api: {request: (path: string, method: string, body: unknown, signal: AbortSignal) => {const next = deferred();requests.push({context, path, method, body, signal, ...next});return next.promise;}}};
    scopes.set(context, scope);
    const announce = () => {for (const listener of listeners) listener();};
    return {context, retire: () => {active = false;announce();}, revoke: () => {context.account.capabilities.manageServer = false;announce();}};
  };
  let owner = makeOwner(), ports = {context: owner.context, requestId: () => `operation-${++operation}`};
  const runtime = {text: {t}, retirementSignal: retirement.signal};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/BroadcastSheet.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../packages/client-core/src/server-admin/panel-logic': panelLogic,
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: (error: unknown) => {errors.push(error);return {body: 'Fixture error'};}},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../../../kit/overlays': {BottomSheet: 'BottomSheet'},
    '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme},
    '../use-settings-request-scope': {useSettingsRequestScope: (context: object) => scopes.get(context)}, './storage/space-source': ownerGuard,
  });
  const render = () => host.render(() => module.NativeBroadcastSheet({ports, runtime, open, onOpenChange: (next: boolean) => {closes.push(next);open = next;}}));
  const commit = (next = open) => {open = next;render();host.flush();return render();};
  const fill = (title = 'Title', body = 'Body') => {field(render(), 'web.broadcast.title').props.onChangeText(title);field(render(), 'web.broadcast.message').props.onChangeText(body);return render();};
  return {render, commit, fill, requests, closes, errors, dispose: host.dispose,
    replace: () => {owner = makeOwner();ports = {context: owner.context, requestId: ports.requestId};return commit();},
    retire: () => owner.retire(), revoke: () => owner.revoke(), retireRuntime: () => retirement.abort()};
}

test('broadcast callbacks cannot edit, send or close another draft or a later opening', async () => {
  const f = await fixture();f.commit(true);
  const old = f.fill('Old title', 'Old body');
  const stale = () => {
    button(old, 'feedback.send').props.onPress();button(old, 'action.cancel').props.onPress();old.props.onOpenChange(false);
    field(old, 'web.broadcast.title').props.onChangeText('Stale title');field(old, 'web.broadcast.message').props.onChangeText('Stale body');
    button(old, 'web.broadcast.adminOnly').props.onPress();button(old, 'web.broadcast.urgent').props.onPress();
  };
  field(f.render(), 'web.broadcast.title').props.onChangeText('Edited title');stale();
  assert.equal(field(f.render(), 'web.broadcast.title').props.value, 'Edited title');
  assert.deepEqual(f.requests, []);assert.deepEqual(f.closes, []);
  button(f.render(), 'action.cancel').props.onPress();stale();
  assert.deepEqual(f.closes, [false], 'close retires the opening before the parent commits');
  f.commit(false);f.commit(true);f.fill('New title', 'New body');stale();
  const current = f.render();assert.equal(current.props.open, true);
  assert.equal(field(current, 'web.broadcast.title').props.value, 'New title');assert.equal(field(current, 'web.broadcast.message').props.value, 'New body');
  assert.equal(button(current, 'web.together.everyone').props.variant, 'primary');assert.equal(button(current, 'web.broadcast.info').props.variant, 'primary');
  assert.deepEqual(f.requests, []);assert.deepEqual(f.closes, [false]);f.dispose();
});

test('only an explicit valid broadcast sends, with one flight and dismissal blocked until it completes', async () => {
  const f = await fixture();f.commit(true);
  button(f.render(), 'feedback.send').props.onPress();assert.equal(f.requests.length, 0);
  f.fill('é'.repeat(101));button(f.render(), 'feedback.send').props.onPress();assert.equal(f.requests.length, 0, 'the title limit counts UTF-8 bytes');
  f.fill('  Current title  ', 'é'.repeat(501));button(f.render(), 'feedback.send').props.onPress();assert.equal(f.requests.length, 0, 'the body limit counts UTF-8 bytes');
  field(f.render(), 'web.broadcast.message').props.onChangeText('  Current body  ');
  button(f.render(), 'web.broadcast.adminOnly').props.onPress();button(f.render(), 'web.broadcast.urgent').props.onPress();
  const ready = f.render(), send = button(ready, 'feedback.send').props.onPress;
  send();send();assert.equal(f.requests.length, 1);
  const request = f.requests[0];assert.equal(request.path, '/v1/admin/notifications/broadcast');assert.equal(request.method, 'POST');
  assert.deepEqual(request.body, {operationId: 'operation-1', audience: 'account-admin', severity: 'critical', dedupeKey: 'broadcast:operation-1', title: 'Current title', body: 'Current body', actions: [], expiresInDays: 14});
  const pending = f.render();assert.equal(pending.props.dismissOnBack, false);assert.equal(pending.props.dismissOnGesture, false);assert.equal(pending.props.dismissOnOutsidePress, false);assert.equal(pending.props.showClose, false);
  button(ready, 'action.cancel').props.onPress();pending.props.onOpenChange(false);field(pending, 'web.broadcast.title').props.onChangeText('Wrong title');
  assert.deepEqual(f.closes, []);assert.equal(field(f.render(), 'web.broadcast.title').props.value, '  Current title  ');
  request.resolve();await settle();assert.deepEqual(f.closes, [false]);assert.equal(f.render().props.open, false);
  send();assert.equal(f.requests.length, 1);f.dispose();
});

test('source replacement aborts a broadcast and its late failure cannot affect a successor flight', async () => {
  const f = await fixture();f.commit(true);const old = f.fill('Old source');button(old, 'feedback.send').props.onPress();
  const first = f.requests[0];f.replace();assert.equal(first.signal.aborted, true);
  f.fill('New source');button(f.render(), 'feedback.send').props.onPress();assert.equal(f.requests.length, 2);
  first.reject(new Error('old request failed'));await settle();
  button(old, 'action.cancel').props.onPress();field(old, 'web.broadcast.title').props.onChangeText('Wrong');button(old, 'feedback.send').props.onPress();
  assert.equal(field(f.render(), 'web.broadcast.title').props.value, 'New source');assert.equal(button(f.render(), 'feedback.send').props.loading, true);
  assert.deepEqual(f.errors, []);assert.deepEqual(f.closes, []);assert.equal(f.requests.length, 2);
  f.requests[1].resolve();await settle();assert.deepEqual(f.closes, [false]);f.dispose();
});

for (const reason of ['retire', 'revoke', 'retireRuntime', 'dispose'] as const) {
  test(`broadcast ${reason} aborts its request and fences retained handlers and completion`, async () => {
    const f = await fixture();f.commit(true);const old = f.fill();button(old, 'feedback.send').props.onPress();
    f[reason]();assert.equal(f.requests[0].signal.aborted, true);
    button(old, 'feedback.send').props.onPress();button(old, 'action.cancel').props.onPress();old.props.onOpenChange(false);
    field(old, 'web.broadcast.title').props.onChangeText('Stale');button(old, 'web.broadcast.adminOnly').props.onPress();
    f.requests[0].resolve();await settle();
    assert.equal(f.requests.length, 1);assert.deepEqual(f.closes, []);assert.deepEqual(f.errors, []);
    if (reason !== 'dispose') {assert.equal(f.render().props.open, false);f.dispose();}
  });
}
