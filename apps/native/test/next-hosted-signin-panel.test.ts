import {FormError, formErrorModule} from './helpers/form-error.mjs';
import test from 'node:test';
import * as hostedSecurity from '../../../packages/client-core/src/hosted-security';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';
import * as securityScope from '../src-next/screens/settings-live/native-account-security.ts';
import * as accountWork from '../src-next/platform/account-work.ts';
import {parseSecurityState} from '../../../packages/client-core/src/hosted-security.ts';
import {readPorticoAccountSecurity} from '../../../packages/client-core/src/portico-services-native.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree?.props ? [tree, ...nodes(tree.props.children), ...nodes(tree.props.control)] : [];

async function fixture({hosted = true, matching = true, hasPassword = true, providerReady = true, kind = 'password'}: {
  hosted?: boolean; matching?: boolean; hasPassword?: boolean; providerReady?: boolean; kind?: 'password' | 'twoStep';
} = {}) {
  const host = hooks(), effects: (() => void)[] = [];
  let deps: any[] | undefined, cleanup: (() => void) | undefined;
  const useLayoutEffect = (effect: () => (() => void) | undefined, next: any[]) => {
    if (!deps || next.some((value, index) => value !== deps![index])) {deps = next; effects.push(() => {cleanup?.(); cleanup = effect();});}
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  const listeners = new Set<() => void>(), navigationListeners = new Set<() => void>(), authorityListeners = new Set<() => void>();
  let session: any = {account: {id: matching ? 'portico-account' : 'someone-else'}, familyId: 'portico-family', accessToken: 'fixture-token'};
  let navigation: any = {revision: 1, route: {kind: 'shell', destination: {kind: 'settings'}}};
  const localSession = {sessionFamilyId: 'server-family', viewer: {serverId: 'server', accountId: 'member', profileId: 'main'}};
  const context: any = {
    account: {serverId: 'server', account: {id: 'member', username: 'member-name', primaryProfileId: 'main', disabled: false, ...(hosted ? {hostedAccountId: 'portico-account'} : {})}, canManage: true, capabilities: {manageAccount: true}},
    current: () => true, session: () => localSession,
    authority: () => ({serverId: 'server', accountId: 'member', profileId: 'main', epoch: 'epoch', viewerFence: 'fence'}),
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => authorityListeners.delete(listener);},
    navigation: {getSnapshot: () => navigation, subscribe: (listener: () => void) => {navigationListeners.add(listener); return () => navigationListeners.delete(listener);}},
    api: {request: () => {throw new Error('Hosted credentials must not use the server transport');}},
  };
  const lifetime = new NativeScreenLifetime(context), calls: any[] = [], controllers: any[] = [], presented: any[] = [];
  let credentialReads = 0, reply: () => Promise<unknown> = async () => ({accountId: 'portico-account', familyId: 'portico-family', contactAddress: '', privateEmail: false,
    hasPassword, mfaEnabled: true, recoveryCodesRemaining: 3, providers: hasPassword ? [] : [{id: 'provider-link', provider: 'google'}]});
  const credentials = {authorityOrigin: () => 'https://accounts.example.invalid', getSnapshot: () => ({session}),
    subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    synchronize: async () => {}, accessSession: async () => session};
  const bridge = {canOpenBrowser: providerReady}, theme = {isTV: false, colors: {danger: 'red', textSecondary: 'grey'}};
  const shared = await screenModule(new URL('../src-next/screens/accounts/PhoneAccountSecurity.tsx', import.meta.url), {
    react: host.react, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}, Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/hosted-security': hostedSecurity, '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'presented'})},
    '../../kit/controls': {FormError, Button: 'Button', TextField: 'TextField'}, '../../kit/overlays': {BottomSheet: 'BottomSheet'},
    '../../kit/theme': {useTheme: () => theme}, '../../platform/clipboard': {copyText: async () => {}}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'},
  });
  class Controller {
    disposed = false; readonly security: any; readonly current: () => boolean; opened: string[] = [];
    constructor(_owner: unknown, readonly scope: unknown, state: unknown, current: () => boolean) {this.security = state; this.current = current; controllers.push(this);}
    dispose() {this.disposed = true;}
    open(task: string) {this.opened.push(task);}
  }
  const opened: string[] = [];
  let openFails = false;
  const {NativeHostedSignInPanel} = await screenModule(new URL('../src-next/screens/settings-live/NativeHostedSignInPanel.tsx', import.meta.url), {
    react: host.react, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}, Text: 'Text', View: 'View', Linking: {openURL: async (url: string) => {if (openFails) throw new Error('no_browser'); opened.push(url);}}},
    '../../../../../packages/client-core/src/hosted-security': hostedSecurity, '../../../../../packages/client-core/src/presentation/errors': {presentError: (error: unknown, audience: string) => {presented.push([error, audience]); return {body: 'Security read failed'};}},
    '../../../../../packages/client-core/src/portico-services-native': {readPorticoAccountSecurity},
    '../../kit/controls': {FormError, Button: 'Button'}, '../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => theme},
    '../../platform/accounts': {nativeAccounts: {accountCredentials: () => {credentialReads++; return credentials;}}},
    '../../platform/account-authority': {hostedUrl: 'https://accounts.example.invalid'}, '../../platform/account-security': {NativeAccountSecurity: Controller},
    '../../platform/account-work': accountWork,  '../../platform/server-connections': {nativeNetworkFetch: async (input: string, init: RequestInit) => {
      const url = new URL(input), token = new Headers(init.headers).get('Authorization')?.replace(/^Bearer /, '');
      calls.push([url.origin, token, url.pathname + url.search, init.method, undefined, init.signal]);
      const state: any = await reply();
      const answer = url.pathname === '/v1/account' ? {id: state.accountId, email: state.contactAddress, hasPassword: state.hasPassword, providers: state.providers.map((value: any) => value.provider)}
        : url.pathname === '/v1/account/two-step' ? {on: state.mfaEnabled, recoveryCodesLeft: state.recoveryCodesRemaining} : undefined;
      assert(answer, 'no retired Services routes');
      return new Response(JSON.stringify(answer), {status: 200, headers: {'Content-Type': 'application/json'}});
    }},
    '../accounts/PhoneAccountSecurity': {AccountSecuritySheet: 'AccountSecuritySheet', accountSecurityAvailability: shared.accountSecurityAvailability}, './native-account-security': securityScope,
  });
  let tree: any;
  const render = () => {tree = host.render(() => NativeHostedSignInPanel({kind, context, lifetime, t: (key: string) => key, runtime: {}})); for (const effect of effects.splice(0)) effect(); return tree;};
  const settle = async () => {await new Promise(resolve => setImmediate(resolve)); render();};
  render(); await settle();
  return {calls, controllers, presented, opened, credentials: () => credentialReads, tree: () => tree, find: (id: string) => nodes(tree).find(node => node.props.testID === id),
    sheet: () => nodes(tree).find(node => node.type === 'AccountSecuritySheet'), settle,
    session: (value: any) => {session = value; for (const listener of [...listeners]) listener(); render();},
    route: () => {navigation = {...navigation, revision: navigation.revision + 1}; for (const listener of [...navigationListeners]) listener(); render();},
    reply: (value: typeof reply) => {reply = value;}, failOpening: () => {openFails = true;}, dispose: () => {cleanup?.(); lifetime.dispose();},
  };
}

test('Hosted Settings reads the matching Portico account, opens the shared task, and refreshes after completion', async () => {
  const f = await fixture();
  try {
    assert.equal(f.calls.length, 2); assert.deepEqual(f.calls.map(call => call.slice(0, 4)), ['/v1/account', '/v1/account/two-step'].map(path => ['https://accounts.example.invalid', 'fixture-token', path, 'GET']));
    assert.deepEqual(f.controllers[0].scope, {accountId: 'portico-account', familyId: 'portico-family'});
    assert.equal(f.find('settings-panel-password').props.label, 'settings.row.password');
    f.find('account-security-password').props.onPress(); assert.deepEqual(f.controllers[0].opened, ['password']);
    f.sheet().props.onFinished(); await f.settle(); assert.equal(f.calls.length, 4); assert.equal(f.controllers[0].disposed, true);
  } finally {f.dispose();}
});

test('Local accounts draw only the label and never consult the Portico credential owner', async () => {
  const f = await fixture({hosted: false});
  try {
    assert.equal(f.calls.length, 0); assert.equal(f.controllers.length, 0); assert.equal(f.sheet(), undefined);
    const row = f.find('settings-panel-password'); assert.equal(row.props.label, 'settings.row.password');
    assert.equal(row.props.control, undefined); assert.equal(row.props.onPress, undefined); assert.equal(row.props.description, undefined);
    assert.equal(f.credentials(), 0);
  } finally {f.dispose();}
});

test('H09: without this account’s Portico session the app says who the server knows and opens the Portico Account, with zero Portico requests', async () => {
  for (const kind of ['password', 'twoStep'] as const) {
    const f = await fixture({matching: false, kind});
    try {
      assert.equal(f.calls.length, 0); assert.equal(f.controllers.length, 0); assert.equal(f.sheet(), undefined);
      assert.equal(f.find('settings-panel-password'), undefined, 'no dead Password or Two-step rows');
      assert.equal(f.find('settings-panel-twoStep'), undefined);
      const row = f.find('settings-panel-portico-account');
      if (kind === 'twoStep') {assert.equal(row, undefined, 'the one Portico Account row sits under Password'); continue;}
      // The row has a Manage control, so the name goes under the label: a row draws its value only without a control.
      assert.equal(row.props.label, 'settings.row.porticoAccount');
      assert.equal(row.props.description, 'member-name'); assert.equal(row.props.value, undefined);
      const manage = f.find('settings-manage-portico-account'); manage.props.onPress(); await new Promise(resolve => setImmediate(resolve));
      assert.deepEqual(f.opened, ['https://accounts.example.invalid/account?tab=security']);
      assert.equal(f.calls.length, 0);
    } finally {f.dispose();}
  }
});

test('Password-less Settings actions are unavailable even when a provider bridge is ready', async () => {
  for (const providerReady of [false, true]) {
    const f = await fixture({hasPassword: false, providerReady, kind: 'twoStep'});
    try {
      assert.equal(f.find('settings-panel-twoStep').props.value, 'web.hostedSecurity.on');
      assert.equal(f.find('account-security-mfa-off'), undefined);
      assert.equal(f.find('account-security-codes'), undefined);
      assert(JSON.stringify(f.tree()).includes('account.security.appUnavailable'));
      assert.deepEqual(f.controllers[0].opened, []);
    } finally {f.dispose();}
  }
});

test('A Portico family change disposes the controller and never adopts a return to the original family', async () => {
  const f = await fixture();
  try {
    const oldAction = f.find('account-security-password').props.onPress, oldFinish = f.sheet().props.onFinished;
    f.session({account: {id: 'portico-account'}, familyId: 'other-family', accessToken: 'other-token'});
    assert.equal(f.controllers[0].disposed, true); assert.equal(f.sheet(), undefined);
    f.session({account: {id: 'portico-account'}, familyId: 'portico-family', accessToken: 'fixture-token'});
    oldAction(); oldFinish(); await f.settle(); assert.equal(f.calls.length, 2); assert.deepEqual(f.controllers[0].opened, []);
  } finally {f.dispose();}
});

test('Security read failures use presentError and remain visible', async () => {
  const f = await fixture(), failure = new Error('fixture failure');
  try {
    f.reply(async () => {throw failure;}); f.sheet().props.onFinished(); await f.settle();
    assert.equal(f.presented.length, 1);
    assert.equal(f.presented[0][0].code, 'network'); assert.equal(f.presented[0][1], 'portico-account');
    assert.ok(nodes(f.tree()).some(node => (node.props.accessibilityRole === 'alert' || node.type === FormError) && [node.props.children].flat().includes('Security read failed')));
    // Try again sits in a focus region, which needs a stable testID of its own: without one, or with the
    // same one in Password and Two-step side by side, the app stopped with "Something went wrong".
    assert.ok(f.find('account-security-retry-password'), 'Try again has its own testID');
  } finally {f.dispose();}
  const twoStep = await fixture({kind: 'twoStep'});
  try {
    twoStep.reply(async () => {throw failure;}); twoStep.sheet().props.onFinished(); await twoStep.settle();
    assert.ok(twoStep.find('account-security-retry-twoStep'), 'Two-step\'s Try again is a different focus child');
  } finally {twoStep.dispose();}
});

test('R-21: a Portico Account page that cannot open says so instead of doing nothing', async () => {
  const f = await fixture({matching: false, kind: 'password'});
  try {
    f.failOpening(); f.find('settings-manage-portico-account').props.onPress(); await f.settle();
    assert.ok(nodes(f.tree()).some(node => (node.type === FormError || node.type === 'Text' && node.props.accessibilityRole === 'alert') && [node.props.children].flat().includes('Security read failed')));
    assert.equal(f.presented.at(-1)?.[1], 'portico-account');
  } finally {f.dispose();}
});

// Justin, 1 Nov: a Google- or Apple-only account never sees Set a password in Settings either.
test('A Google-only Settings account shows an unavailable Password row after More sign-in options', async () => {
  const f = await fixture({hasPassword: false, kind: 'password'});
  try {
    assert.equal(f.find('settings-panel-password'), undefined, 'no Password row until it is asked for');
    assert.equal(f.find('account-security-password'), undefined);
    const more = f.find('settings-more-sign-in');
    assert.ok(more); assert.equal(more.props.variant, 'text');
    more.props.onPress(); await f.settle();
    assert.equal(f.find('settings-more-sign-in'), undefined, 'the link goes away');
    const row = f.find('settings-panel-password');
    assert.equal(row.props.label, 'settings.row.password'); assert.equal(row.props.value, 'web.hostedSecurity.notSet');
    assert.equal(f.find('account-security-password'), undefined);
    assert(JSON.stringify(row).includes('account.security.appUnavailable'));
    assert.deepEqual(f.controllers.at(-1)!.opened, []);
  } finally {f.dispose();}
});
