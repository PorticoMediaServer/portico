import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';
import * as security from '../src-next/screens/settings-live/native-account-security.ts';

const securityParts = new Set(['SecuritySheet', 'SecurityText', 'PasswordMeter', 'EnrolmentCode']);
const nodes = (tree: any): any[] => {
  if (Array.isArray(tree)) return tree.flatMap(nodes);
  if (!tree || typeof tree !== 'object') return [];
  if (securityParts.has(tree.type?.name)) return nodes(tree.type(tree.props));
  return [tree, ...nodes(tree.props?.children), ...nodes(tree.props?.footer)];
};
const find = (tree: any, id: string) => nodes(tree).find(node => node.props?.testID === id);
const scopes = ['read', 'playback', 'library', 'admin'];

/** The member's own personal access tokens: only their real list, create and revoke calls. */
async function fixture(options: {hosted?: boolean; tokensAllowed?: boolean} = {}) {
  const host = hooks(), pendingEffects: (() => void)[] = []; let deps: any[] | undefined, cleanup: (() => void) | undefined;
  const useLayoutEffect = (effect: () => () => void, next: any[]) => {
    if (!deps || next.some((value, index) => value !== deps![index])) {deps = next; pendingEffects.push(() => {cleanup?.(); cleanup = effect();});}
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  const navigation: any = {revision: 1, route: {kind: 'shell', destination: {kind: 'settings'}}};
  const session = {sessionFamilyId: 'family', installationId: 'installation', viewer: {serverId: 'server', accountId: 'account', profileId: 'main'}};
  const context: any = {account: {serverId: 'server', account: {id: 'account', primaryProfileId: 'main', disabled: false, ...(options.hosted ? {hostedAccountId: 'hosted-account'} : {})}, canManage: true, capabilities: {manageAccount: true}},
    session: () => session, current: () => true, authority: () => ({serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'epoch', viewerFence: 'fence'}),
    subscribeAuthority: () => () => {},
    navigation: {getSnapshot: () => navigation, subscribe: () => () => {}}, api: {request: async () => ({})},
  };
  const lifetime = new NativeScreenLifetime(context), calls: any[] = [], copied: string[] = [];
  const token = {id: 'token-1', name: 'Laptop script', scopes: ['read'], createdAt: '2026-10-01T09:00:00Z', lastUsedAt: '2026-10-20T10:00:00Z'};
  let page: {items: typeof token[]; tokensAllowed?: boolean} = {items: [token], ...(options.tokensAllowed === false ? {tokensAllowed: false} : {})};
  const apiAccess = {
    apiScopes: scopes,
    getApiAccessOptions: async () => undefined,
    listAccessTokens: async () => page,
    createAccessToken: async (_transport: unknown, input: any) => {calls.push(['createAccessToken', input]); return {key: {...token, id: 'token-2', name: input.name, scopes: input.scopes}, secret: 'pk_fixture-secret'};},
    revokeAccessToken: async (_transport: unknown, id: string) => {calls.push(['revokeAccessToken', id]); page = {items: []};},
  };
  let sequence = 0, current: {key: string; id: string} | undefined;
  const theme = {isTV: false, platform: 'phone', width: 390, type: {rowLabel: {}, rowHelp: {}}, space: {sm: 12, xs: 4, hairline: 1}, baseRadii: {control: 12}, colors: new Proxy({}, {get: (_t, key) => String(key)})};
  const kit = await screenModule(new URL('../src-next/kit/settings/SecurityParts.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../overlays': {Dialog: 'Dialog', BottomSheet: 'BottomSheet'}, '../overlays/surface': {dialogSheetGutter: 20}, '../theme': {useTheme: () => theme},
  });
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useAccessTokens.ts', import.meta.url), {
    react: host.react,
    '../../../../../../packages/client-core/src/api-access': apiAccess,
    '../../../../../../packages/client-core/src/playback-v1/http': {idempotencyKey: () => 'fixture-operation'},
    '../../../../../../packages/client-core/src/server-admin/api-words': {EXPIRY_DAYS: [0, 30, 90, 365],
      chosenScopes: (ticked: ReadonlySet<string>) => ticked.has('admin') ? ['admin'] : scopes.filter(scope => ticked.has(scope)),
      expiryInstant: (days: number, now: number) => days > 0 ? new Date(now + days * 86_400_000).toISOString() : undefined,
      expiryLabel: (_t: unknown, days: number) => `expiry-${days}`, scopeTicked: (ticked: ReadonlySet<string>, scope: string) => ticked.has(scope)},
    '../../../../../../packages/client-core/src/server-admin/server-forms': {createOperationIds: () => ({
      forPayload: (key: string) => {if (current?.key === key) return current.id; current = {key, id: `operation-${++sequence}`}; return current.id;},
      release: () => {current = undefined;}})},
    '../../../platform/clipboard': {copyText: async (value: string) => {copied.push(value);}},
    '../../../screens/settings-live/native-account-security': security,
  });
  const {AccessTokensPanel} = await screenModule(new URL('../src-next/pages/touch/settings/AccessTokensPanel.tsx', import.meta.url), {
    react: host.react,
    '../../../../../../packages/client-core/src/api-access': apiAccess,
    '../../../../../../packages/client-core/src/server-admin/api-words': {EXPIRY_DAYS: [0, 30, 90, 365], expiryLabel: (_t: unknown, days: number) => `expiry-${days}`},
    '../../../../../../packages/i18n/src': {defaultI18n: {date: (value: string) => `date(${value.slice(0, 10)})`}},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField', Select: 'Select', Switch: 'Switch'},
    '../../../kit/shell': {SettingsGroup: 'SettingsGroup', SettingsRow: 'SettingsRow'},
    '../../../kit/settings/SecurityParts': kit, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/theme': {useTheme: () => theme},
    '../../shared/settings/useAccessTokens': model,
  });
  let tree: any;
  const render = () => {tree = host.render(() => AccessTokensPanel({context, lifetime, t: (id: string) => id, runtime: {error: () => ({title: 'Failure'})}})); for (const effect of pendingEffects.splice(0)) effect(); return tree;};
  const settle = async () => {await new Promise(resolve => setImmediate(resolve)); render();};
  render(); render();
  return {calls, copied, render, find: (id: string) => find(tree, id), tree: () => tree,
    open: async () => {find(tree, 'settings-panel-access-tokens').props.onPress(); await settle();},
    edit: (id: string, value: string) => {find(tree, id).props.onChangeText(value); render();},
    press: async (id: string) => {const node = find(tree, id); assert.ok(node, id); assert.ok(!node.props.disabled, id); node.props.onPress(); await settle();},
    dispose: () => {cleanup?.(); lifetime.dispose();},
  };
}

test('The token list names each token, when it was made, when it was last used and what it may do', async () => {
  const f = await fixture();
  try {
    await f.open();
    const row = f.find('access-token-token-1');
    assert.equal(row.props.label, 'Laptop script');
    assert.deepEqual(row.props.description, 'api.scope.read · api.credential.created · api.credential.lastUsed');
    assert.equal(f.find('access-tokens').props.open, true);
    await f.press('access-token-close-list');
    assert.equal(f.find('access-tokens').props.open, false);
  } finally {f.dispose();}
});

test('Creating a token needs a name and a scope, then shows the one-time token in a sheet only its acknowledgement closes', async () => {
  const f = await fixture();
  try {
    await f.open(); await f.press('access-token-add');
    assert.equal(f.find('access-token-create').props.disabled, true);
    f.edit('access-token-name', 'Laptop script');
    assert.equal(f.find('access-token-create').props.disabled, true);
    f.find('access-token-scope-read').props.onCheckedChange(true); f.render();
    const create = f.find('access-token-create').props.onPress; create(); await new Promise(resolve => setImmediate(resolve)); f.render();
    assert.deepEqual(f.calls.map(call => [call[0], call[1].name, call[1].scopes, call[1].expiresAt]), [['createAccessToken', 'Laptop script', ['read'], undefined]]);
    assert.equal(typeof f.calls[0][1].operationId, 'string');
    const sheet = f.find('access-tokens');
    assert.equal(sheet.props.title, 'api.tokens.copyTitle');
    assert.equal(sheet.props.dismissOnBack, false); assert.equal(sheet.props.dismissOnGesture, false); assert.equal(sheet.props.dismissOnOutsidePress, false); assert.equal(sheet.props.showClose, false);
    assert.deepEqual(f.find('access-token-secret').props.children.flat(), ['pk_fixture-secret']);
    await f.press('access-token-copy'); assert.deepEqual(f.copied, ['pk_fixture-secret']);
    await f.press('access-token-copied');
    assert.equal(f.find('access-tokens').props.open, false); assert.equal(f.find('access-token-secret'), undefined);
  } finally {f.dispose();}
});

test('A token is revoked only from its own confirmation, and the list reloads afterwards', async () => {
  const f = await fixture();
  try {
    await f.open(); f.find('access-token-token-1').props.onPress(); f.render();
    assert.equal(f.find('access-tokens').props.title, 'web.access.revokeTitle'); assert.deepEqual(f.calls, []);
    await f.press('access-token-revoke');
    assert.deepEqual(f.calls, [['revokeAccessToken', 'token-1']]);
    assert.equal(f.find('access-token-token-1'), undefined);
  } finally {f.dispose();}
});

test('A Hosted account keeps its credentials on the account site, so no token panel is drawn', async () => {
  const f = await fixture({hosted: true});
  try {
    assert.equal(f.tree(), null);
    assert.deepEqual(f.calls, []);
  } finally {f.dispose();}
});

test('An account the owner keeps from making tokens is told so up front, and is offered no Create', async () => {
  const f = await fixture({tokensAllowed: false});
  try {
    await f.open();
    assert.ok(JSON.stringify(f.tree()).includes('api.tokens.off'));
    assert.equal(f.find('access-token-add'), undefined);
  } finally {f.dispose();}
});
