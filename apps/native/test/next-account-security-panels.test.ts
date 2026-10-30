import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';
import * as security from '../src-next/screens/settings-live/native-account-security.ts';
import {passwordStrength} from '../../../packages/client-core/src/password-strength.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children), ...nodes(tree.props?.footer)] : [];
const find = (tree: any, id: string) => nodes(tree).find(node => node.props?.testID === id);

async function fixture(kind: security.NativeAccountSecurityKind, enabled = false) {
  const host = hooks(), pendingEffects: (() => void)[] = []; let deps: any[] | undefined, cleanup: (() => void) | undefined;
  const useLayoutEffect = (effect: () => () => void, next: any[]) => {
    if (!deps || next.some((value, index) => value !== deps![index])) {deps = next; pendingEffects.push(() => {cleanup?.(); cleanup = effect();});}
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  const authListeners = new Set<() => void>(), navListeners = new Set<() => void>();
  const navigation: any = {revision: 1, route: {kind: 'shell', destination: {kind: 'settings'}}};
  const session = {sessionFamilyId: 'family', installationId: 'installation', viewer: {serverId: 'server', accountId: 'account', profileId: 'main'}};
  const context: any = {account: {serverId: 'server', account: {id: 'account', primaryProfileId: 'main', disabled: false}, canManage: true, capabilities: {manageAccount: true}},
    session: () => session, current: () => true, authority: () => ({serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'epoch', viewerFence: 'fence'}),
    subscribeAuthority: (fn: () => void) => {authListeners.add(fn); return () => authListeners.delete(fn);},
    navigation: {getSnapshot: () => navigation, subscribe: (fn: () => void) => {navListeners.add(fn); return () => navListeners.delete(fn);}}, api: {request: async () => ({})},
  };
  const lifetime = new NativeScreenLifetime(context), calls: any[] = [], copied: string[] = [], signedOut: string[][] = [];
  const devices = [{id: 'current', name: 'This phone', current: true, firstSeen: '2026-10-01', lastSeen: '2026-10-27', sessions: 1}, {id: 'other', name: 'TV', current: false, firstSeen: '2026-09-01', lastSeen: '2026-10-26', sessions: 2}];
  let passwordReply = async () => {};
  // Only the existing IdentityClient command boundary is substituted; no generated routes or contract parser is fabricated.
  class IdentityClient {
    readonly scope: any;
    constructor(scope: any) {this.scope = scope;}
    async twoFactor() {assert.equal(this.scope.current(), true); return {enabled, pendingEnrolment: false, recoveryCodesRemaining: 2};}
    async changePassword(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['changePassword', ...args]); await passwordReply();}
    async enrolTwoFactor(password: string) {assert.equal(this.scope.current(), true); calls.push(['enrolTwoFactor', password]); return {secret: 'ABCDEFGHIJKLMNOP', uri: 'otpauth://totp/Fixture?secret=ABCDEFGHIJKLMNOP', recoveryCodes: ['fixture-code-one', 'fixture-code-two']};}
    async verifyTwoFactor(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['verifyTwoFactor', ...args]); return {enabled: true, pendingEnrolment: false, recoveryCodesRemaining: 2};}
    async disableTwoFactor(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['disableTwoFactor', ...args]);}
    async replaceRecoveryCodes(...args: any[]) {calls.push(['replaceRecoveryCodes', ...args]); return ['replacement-one', 'replacement-two'];}
    async devices() {assert.equal(this.scope.current(), true); return devices;}
    async editDevice(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['editDevice', ...args]);}
    async signOutDevice(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['signOutDevice', ...args]);}
    async removeDevice(...args: any[]) {assert.equal(this.scope.current(), true); calls.push(['removeDevice', ...args]);}
    async signOutEverywhere() {assert.equal(this.scope.current(), true); calls.push(['signOutEverywhere']);}
  }
  const theme = {isTV: false, platform: 'phone', width: 390, colors: new Proxy({}, {get: (_t, key) => String(key)})};
  const {NativeAccountSecurityPanel} = await screenModule(new URL('../src-next/screens/settings-live/NativeAccountSecurityPanels.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', useWindowDimensions: () => ({width: 390, height: 844})}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../../../../../packages/client-core/src/identity-client': {IdentityClient}, '../../../../../packages/client-core/src/password-strength': {passwordStrength},
    '../../../../../packages/client-core/src/api-access': {apiScopes: ['read', 'playback', 'library', 'admin'], createAccessToken: async () => undefined, getApiAccessOptions: async () => undefined, listAccessTokens: async () => undefined, revokeAccessToken: async () => undefined},
    '../../../../../packages/client-core/src/playback-v1/http': {idempotencyKey: () => 'fixture-operation'},
    '../../../../../packages/client-core/src/server-admin/api-words': {EXPIRY_DAYS: [0], chosenScopes: () => [], credentialWorks: () => false, expiryInstant: () => undefined, expiryLabel: () => ''},
    '../../../../../packages/client-core/src/server-admin/server-forms': {createOperationIds: () => ({forPayload: () => 'fixture-operation', release: () => {}})},
    '../../../../../packages/i18n/src': {defaultI18n: {date: (value: string) => value}}, '../../platform/accounts': {nativeAccounts: {signOutServer: async (...values: string[]) => {signedOut.push(values);}}}, '../../platform/clipboard': {copyText: async (value: string) => {copied.push(value);}},
    '../../kit/controls': {Button: 'Button', TextField: 'TextField', Select: 'Select', Switch: 'Switch'}, '../../kit/overlays': {Dialog: 'Dialog', BottomSheet: 'BottomSheet'}, '../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../kit/states': {StateView: 'StateView'},
    '../../kit/shell/visual': {shellType: () => ({})}, '../../kit/theme': {useTheme: () => theme}, './native-account-security': security, './NativeHostedSignInPanel': {NativeHostedSignInPanel: 'NativeHostedSignInPanel'},
  });
  let tree: any;
  const render = () => {tree = host.render(() => NativeAccountSecurityPanel({kind, context, lifetime, t: (id: string) => id, runtime: {error: () => ({title: 'Failure'})}})); for (const effect of pendingEffects.splice(0)) effect(); return tree;};
  const settle = async () => {await new Promise(resolve => setImmediate(resolve)); render();};
  render(); render();
  return {calls, copied, signedOut, render, find: (id: string) => find(tree, id), tree: () => tree,
    open: async () => {find(tree, `settings-panel-${kind}`).props.onPress(); await settle();},
    edit: (id: string, value: string) => {find(tree, id).props.onChangeText(value); render();},
    press: async (id: string) => {const node = find(tree, id); assert.ok(node, id); assert.ok(!node.props.disabled, id); node.props.onPress(); await settle();},
    settle, delayPassword: () => {let resolve!: () => void; passwordReply = () => new Promise<void>(done => {resolve = done;}); return () => resolve();},
    dispose: () => {cleanup?.(); lifetime.dispose();},
  };
}

test('Password sends current password and required factor once; a weak-but-valid password remains allowed', async () => {
  const f = await fixture('password', true);
  try {
    await f.open();
    f.edit('account-security-current-password', 'current-fixture'); f.edit('account-security-new-password', 'password'); f.edit('account-security-confirm-password', 'password');
    assert.equal(f.find('account-security-save-password').props.disabled, true);
    f.edit('account-security-code', ' factor-fixture ');
    const finish = f.delayPassword(), save = f.find('account-security-save-password').props.onPress;
    save(); save(); await f.settle();
    assert.deepEqual(f.calls, [['changePassword', 'current-fixture', 'password', 'factor-fixture']]);
    finish(); await f.settle(); assert.equal(f.find('account-security-password').props.open, false);
  } finally {f.dispose();}
});

test('A late password result and retained close handler cannot close a newer form', async () => {
  const f = await fixture('password');
  try {
    await f.open(); f.edit('account-security-current-password', 'current-fixture'); f.edit('account-security-new-password', 'new-password-fixture'); f.edit('account-security-confirm-password', 'new-password-fixture');
    const sheet = f.find('account-security-password'), finish = f.delayPassword();
    f.find('account-security-save-password').props.onPress(); sheet.props.onOpenChange(false); f.render(); await f.open();
    sheet.props.onOpenChange(false); finish(); await f.settle();
    assert.equal(f.find('account-security-password').props.open, true); assert.equal(f.find('account-security-new-password').props.value, '');
  } finally {f.dispose();}
});

test('Two-step enrolment verifies the real QR secret and requires explicit recovery-code acknowledgement', async () => {
  const f = await fixture('twoStep');
  try {
    await f.open(); const oldClose = f.find('account-security-twoStep').props.onOpenChange;
    f.edit('account-security-current-password', 'current-fixture'); await f.press('account-security-enrol');
    const oldEnrol = f.calls.length;
    assert.equal(nodes(f.tree()).find(node => node.type === 'QRCode').props.value, 'otpauth://totp/Fixture?secret=ABCDEFGHIJKLMNOP');
    f.edit('account-security-code', '123 456'); const verify = f.find('account-security-verify').props.onPress; await f.press('account-security-verify');
    assert.deepEqual(f.calls.slice(oldEnrol), [['verifyTwoFactor', 'current-fixture', '123456']]);
    verify(); oldClose(false); f.render(); assert.equal(f.find('account-security-twoStep').props.open, true); assert.equal(f.calls.length, 2);
    const sheet = f.find('account-security-twoStep'); assert.equal(sheet.props.dismissOnBack, false); assert.equal(sheet.props.dismissOnGesture, false); assert.equal(sheet.props.showClose, false);
    await f.press('account-security-copy-codes'); assert.deepEqual(f.copied, ['fixture-code-one\nfixture-code-two']);
    await f.press('account-security-codes-saved'); assert.equal(f.find('account-security-twoStep').props.open, false); assert.equal(f.find('account-security-recovery-codes'), undefined);
  } finally {f.dispose();}
});

test('Turning two-step off waits for the second confirmation and preserves the original password and code', async () => {
  const f = await fixture('twoStep', true);
  try {
    await f.open(); await f.press('account-security-disable'); f.edit('account-security-current-password', 'current-fixture'); f.edit('account-security-code', 'recovery-fixture');
    await f.press('account-security-factor-submit'); assert.equal(f.calls.length, 0);
    await f.press('account-security-confirm-disable'); assert.deepEqual(f.calls, [['disableTwoFactor', 'current-fixture', 'recovery-fixture']]);
  } finally {f.dispose();}
});

test('Devices rename through the existing client, refuse current-device removal, and sign out only the captured family', async () => {
  const f = await fixture('devices');
  try {
    await f.open(); await f.press('account-security-device-other'); f.edit('account-security-device-name', 'Renamed TV'); await f.press('account-security-save-device');
    assert.deepEqual(f.calls, [['editDevice', 'other', {name: 'Renamed TV'}]]);
    await f.press('account-security-device-current'); assert.equal(f.find('account-security-remove-device'), undefined);
    await f.press('account-security-sign-out-device');
    assert.deepEqual(f.calls.at(-1), ['signOutDevice', 'current']); assert.deepEqual(f.signedOut, [['server', 'family']]);
  } finally {f.dispose();}
});

test('Sign out everywhere requires confirmation and no password or Hosted call', async () => {
  const f = await fixture('devices');
  try {
    await f.open(); await f.press('account-security-sign-out-all'); assert.deepEqual(f.calls, []);
    await f.press('account-security-confirm-all'); assert.deepEqual(f.calls, [['signOutEverywhere']]); assert.deepEqual(f.signedOut, [['server', 'family']]);
  } finally {f.dispose();}
});

test('Replacing recovery codes sends the current password and factor, then reveals only the new memory-only codes', async () => {
  const f = await fixture('twoStep', true);
  try {
    await f.open(); await f.press('account-security-replace-codes'); f.edit('account-security-current-password', 'current-fixture'); f.edit('account-security-code', 'factor-fixture');
    const submit = f.find('account-security-factor-submit').props.onPress; await f.press('account-security-factor-submit');
    assert.deepEqual(f.calls[0].slice(0, 3), ['replaceRecoveryCodes', 'current-fixture', 'factor-fixture']); assert.ok(f.calls[0][3] instanceof AbortSignal);
    submit(); await f.settle(); assert.equal(f.calls.length, 1);
    assert.deepEqual(f.find('account-security-recovery-codes').props.children, ['replacement-one\nreplacement-two']);
    await f.press('account-security-codes-saved'); assert.equal(f.find('account-security-recovery-codes'), undefined);
  } finally {f.dispose();}
});

test('Removing another device waits for its exact confirmation and cannot act again from a retained button', async () => {
  const f = await fixture('devices');
  try {
    await f.open(); await f.press('account-security-device-other'); await f.press('account-security-remove-device'); assert.deepEqual(f.calls, []);
    const remove = f.find('account-security-confirm-remove').props.onPress; await f.press('account-security-confirm-remove');
    assert.deepEqual(f.calls, [['removeDevice', 'other']]); remove(); await f.settle(); assert.equal(f.calls.length, 1); assert.deepEqual(f.signedOut, []);
  } finally {f.dispose();}
});
