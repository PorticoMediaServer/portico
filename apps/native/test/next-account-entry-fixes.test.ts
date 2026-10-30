// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {porticoPublicLinks} from '../../../packages/client-core/src/public-links';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import {act as fabricAct, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {act as driverAct, createHookHost} from './hook-driver.ts';
const act = async (work: () => void | Promise<void>) => {await driverAct(work); await fabricAct(() => {});};
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const words = (node: any): string => Array.isArray(node) ? node.map(words).join(' ') : node?.props ? words(node.props.children) : typeof node === 'string' ? node : '';
const copy = {t: (key: string, values?: Record<string, string>) => key === 'services.signup.agreement' ? `By continuing, you agree to the ${values!.terms} and ${values!.privacy}.` : key};
const theme = {platform: 'phone', isTV: false, textScale: 1, type: {body: {}, bodyStrong: {}, caption: {}, captionStrong: {}},
  colors: {text: '#edf5fa', textSecondary: '#afc4d2', textTertiary: '#7995a6', danger: '#f38b88', accent: '#2f86bb', accentStrong: '#409fd4', line: '#29414f'}};
const offered = {password: true, registration: true, recovery: true, providers: []};
const registration = {kind: 'registration', registrationId: 'registration-original', status: 'contact_pending', revision: '1',
  maskedAddress: 'a***@example.test', expiresAt: '2099-01-01T00:10:00Z', contactVerified: false};

/** Actual AccountEntry hook effects and handlers. Native hosts and IO are ports;
 * missing paths throw instead of manufacturing a successful command. */
async function fixture(context: import('node:test').TestContext, initialStage: 'welcome' | 'address' = 'welcome', invitationRole?: 'admin' | 'member', createHost: (body: () => void) => any = createFabricHookHost) {
  const calls = {prepare: 0, constructed: 0, synchronize: 0, links: [] as string[], register: [] as unknown[][],
    resend: [] as any[], verify: [] as any[], local: 0, factor: [] as string[], complete: 0};
  let linkFailure: Error | undefined;
  let prepare: () => Promise<any> = async () => offered;
  let tree: any, session: any;
  const source = {id: 'local-server', name: 'Local server', signIn: invitationRole ? 'portico' : 'local'};
  const entry = {raw: 'invitation', server: 'https://local.example', id: source.id, fingerprint: 'server-pin', signIn: 'portico', expiresAt: '2099-01-01T00:10:00Z'};
  const preview = {serverName: source.name, role: invitationRole, allLibraries: true, libraries: []};
  class Portico {
    constructor() {calls.constructed++;}
    async prepare() {calls.prepare++; return prepare();}
    async password() {session = {account: {id: 'account', username: 'person'}, familyId: 'family'};}
    async register(...input: unknown[]) {calls.register.push(input); return registration;}
    async resendRegistration(original: any, caller: {isCurrent: () => boolean}) {
      assert.equal(caller.isCurrent(), true); calls.resend.push(original);
      return {...original, revision: '2', expiresAt: '2099-01-01T00:20:00Z'};
    }
    async verifyRegistration(code: string, original: any, caller: {isCurrent: () => boolean}) {
      assert.equal(caller.isCurrent(), true); calls.verify.push({code, original});
    }
    dispose() {}
  }
  class Server {
    static parseInvitationEntry() {return invitationRole ? entry : undefined;}
    static invitationUnavailable() {return false;}
    async inspect() {return source;}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    async previewPorticoInvitation() {return {entry, preview};}
    async preparePorticoInvitation() {return {username: 'person', preview: async () => preview, failure: () => undefined, dispose() {}};}
    serverDescriptor() {return source;}
    serverOrigin() {return 'https://local.example';}
    async signInLocal() {calls.local++; return {kind: 'challenge', expiresAt: '2099-01-01T00:10:00Z'};}
    directoryChallengePending() {return false;}
    async completeSecondFactor(code: string) {calls.factor.push(code); return {kind: 'complete'};}
    dispose() {}
  }
  const credentials = {synchronize: async () => {calls.synchronize++;}, getSnapshot: () => ({session})};
  const legal = await screenModule(new URL('../src-next/screens/accounts/LegalAgreement.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text'},
    '../../../../../packages/i18n/src': {defaultI18n: copy},
    '../../../../../packages/client-core/src/public-links': {porticoPublicLinks},
  });
  const modules: Record<string, any> = {
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: {addEventListener: () => ({remove() {}})},
      Platform: {isTV: false, OS: 'ios'}, NativeModules: {PorticoAccountAuth: {canOpenBrowser: false}}, View: 'View', Text: 'Text', Image: 'Image',
      Linking: {openURL: async (url: string) => {calls.links.push(url); if (linkFailure) throw linkFailure;}}},
    'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/i18n/src': {defaultI18n: copy},
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({silent: false, body: 'Request failed'})},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin: (value: string) => value},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateLayout: 'StateLayout'}, '../../kit/controls': {Button: 'Button', TextField: 'TextField', Focusable: 'Focusable'},
    '../../platform/accounts': {nativeAccounts: {accountCredentials: () => credentials}}, '../../platform/server-sign-in': {NativeServerSignIn: Server},
    '../../platform/portico-sign-in': {NativePorticoSignIn: Portico, porticoUsernameUnavailable: () => false},
    '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-menu': {useTVMenuCapture() {}},
    './PhoneFrontDoor': {PhoneFrontDoor: 'FrontDoor'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'AccountSignIn'},
    './LegalAgreement': legal, './PhoneAccountForm': {PhoneAccountForm: 'AccountForm'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'Strength'},
    '../../platform/negotiation': {negotiationRefusal: () => undefined}, './ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'},
  };
  const strict = new Proxy(modules, {get: (all, name: string) => all[name] ?? new Proxy({}, {
    get: (_, exported: string) => function unvisited() {throw new Error('Unexpected account-entry boundary ' + name + ':' + exported);},
  })});
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), strict);
  const previousRequire = Object.getOwnPropertyDescriptor(globalThis, 'require');
  if (invitationRole) {
    Object.defineProperty(globalThis, 'require', {configurable: true, value: () => 'wordmark'});
    context.after(() => {if (previousRequire) Object.defineProperty(globalThis, 'require', previousRequire); else Reflect.deleteProperty(globalThis, 'require');});
  }
  const incomingInvitation = invitationRole ? {raw: entry.raw, current: () => true, onRetired: () => () => {}} : undefined;
  const host = createHost(() => {tree = AccountEntry({initialStage, incomingInvitation, onComplete: () => {calls.complete++;}});});
  context.after(() => host.unmount()); await act(() => host.mount());
  const fields = () => nodes(tree.props.beforeActions).filter(node => node.type === 'TextField');
  return {host, calls, tree: () => tree, fields, failLinks: (error: Error) => {linkFailure = error;}, prepare: (work: typeof prepare) => {prepare = work;},
    local: async () => {
      if (tree.type === 'FrontDoor') await act(() => tree.props.onLocal());
      await act(() => fields()[0].props.onChangeText('https://local.example'));
      await act(() => tree.props.primaryAction.onPress());
      assert.equal(tree.props.title, 'Local server');
    }};
}

// Preserve this manual replay vector: production Fabric cannot replay a
// captured committed setup/cleanup pair as the development renderer can.
test('welcome, effect replay, local entry and Back never prepare or synchronize a Portico account', async context => {
  const f = await fixture(context, 'welcome', undefined, createHookHost);
  assert.equal(f.tree().type, 'FrontDoor');
  const retainedPortico = f.tree().props.onPortico;
  await act(() => {f.host.replay(); f.host.rerender();});
  assert.equal(f.calls.prepare, 0); assert.equal(f.calls.constructed, 0); assert.equal(f.calls.synchronize, 0);
  await f.local(); await act(() => retainedPortico());
  assert.equal(f.tree().props.title, 'Local server'); assert.equal(f.calls.prepare, 0);
  const forgot = nodes(f.tree().props.children).find(node => node.type === 'Button' && node.props.children === 'auth.forgotPassword');
  assert.ok(forgot, 'regular local sign-in exposes recovery');
  await act(() => forgot.props.onPress()); assert.deepEqual(f.calls.links, ['https://local.example/sign-in']);
  await act(() => f.tree().props.secondaryAction.onPress());
  assert.equal(f.tree().type, 'FrontDoor');
  await act(() => forgot.props.onPress()); assert.equal(f.calls.links.length, 1, 'the retired local form cannot launch recovery');
  assert.equal(f.calls.prepare, 0); assert.equal(f.calls.synchronize, 0);
});

test('one explicit Portico Account tap prepares once, while retained front-door actions retire', async context => {
  const f = await fixture(context), old = f.tree().props;
  await act(() => {old.onPortico(); old.onPortico();});
  assert.equal(f.calls.prepare, 1); assert.equal(f.tree().type, 'AccountSignIn');
  await act(() => old.onLocal()); assert.equal(f.tree().type, 'AccountSignIn');
  await act(() => f.tree().props.onLocal()); assert.equal(f.tree().type, 'AccountForm');
  await act(() => old.onPortico()); assert.equal(f.calls.prepare, 1);
});

test('failed explicit preparation stays at the front door and retries only on another tap', async context => {
  const f = await fixture(context); f.prepare(async () => {throw new Error('offline');});
  await act(() => f.tree().props.onPortico());
  assert.equal(f.tree().type, 'FrontDoor'); assert.equal(f.calls.prepare, 1); assert.ok(f.tree().props.error);
  await act(() => f.host.rerender()); assert.equal(f.calls.prepare, 1);
  f.prepare(async () => offered); await act(() => f.tree().props.onPortico());
  assert.equal(f.tree().type, 'AccountSignIn'); assert.equal(f.calls.prepare, 2);
});

test('registration preserves API argument order while drawing approved fields, password reveal, legal copy and a guarded resend', async context => {
  const f = await fixture(context); await act(() => f.tree().props.onPortico()); await act(() => f.tree().props.onCreate());
  assert.equal(f.tree().type, 'AccountForm');
  assert.deepEqual(f.fields().map(node => node.props.label), ['auth.email', 'auth.username', 'auth.yourName · field.optional', 'auth.password']);
  assert.equal(f.tree().props.primaryAction.label, 'action.continue');
  const agreement = nodes(f.tree().props.children).find(node => node.type.name === 'LegalAgreement');
  const footer = agreement.type(agreement.props);
  assert.ok(words(footer).includes('auth.terms')); assert.ok(words(footer).includes('auth.privacyPolicy'));
  await act(() => nodes(footer).find(node => node.props.children === 'auth.terms').props.onPress());
  assert.deepEqual(f.calls.links, [porticoPublicLinks.terms]);
  f.failLinks(new Error('browser unavailable'));
  await act(() => nodes(footer).find(node => node.props.children === 'auth.privacyPolicy').props.onPress());
  assert.ok(words(f.tree().props.beforeActions).includes('Request failed'), 'legal failures use the account status line');
  const password = () => f.fields()[3].props;
  assert.equal(password().autoComplete, 'new-password'); assert.equal(password().textContentType, 'newPassword'); assert.equal(password().secureTextEntry, true);
  await act(() => password().trailing.props.onPress()); assert.equal(password().secureTextEntry, false);
  await act(() => {const fields = f.fields(); fields[0].props.onChangeText('a@example.test'); fields[1].props.onChangeText('person'); fields[2].props.onChangeText('Person'); fields[3].props.onChangeText('correct horse battery staple');});
  assert.equal(nodes(f.tree().props.beforeActions).find(node => node.type === 'Strength').props.password, 'correct horse battery staple');
  await act(() => f.tree().props.primaryAction.onPress());
  assert.deepEqual(f.calls.register, [['person', 'Person', 'correct horse battery staple', 'a@example.test']]);
  assert.equal(f.tree().props.primaryAction.label, 'auth.createAccount');
  assert.equal(f.fields()[0].props.keyboardType, 'number-pad'); assert.equal(f.fields()[0].props.autoComplete, 'one-time-code');
  assert.ok(words(f.tree().props.children).includes('services.signup.codeHelp'));
  const resend = () => nodes(f.tree().props.children).find(node => node.props.testID === 'accounts-registration-resend');
  await act(() => f.fields()[0].props.onChangeText('123456'));
  const oldResend = resend().props.onPress;
  await act(() => oldResend()); assert.deepEqual(f.calls.resend, [registration]); assert.equal(f.fields()[0].props.value, '');
  await act(() => oldResend()); assert.equal(f.calls.resend.length, 1, 'a retained resend cannot act on a successor registration');
  await act(() => f.fields()[0].props.onChangeText('654321')); await act(() => f.tree().props.primaryAction.onPress());
  assert.equal(f.calls.verify[0].code, '654321'); assert.equal(f.calls.verify[0].original.revision, '2');
});

test('local two-step uses numeric one-time-code autofill and keeps the local completion path', async context => {
  const f = await fixture(context); await f.local();
  await act(() => {f.fields()[0].props.onChangeText('person'); f.fields()[1].props.onChangeText('password');});
  await act(() => f.tree().props.primaryAction.onPress());
  assert.equal(f.tree().props.title, 'auth.enterVerificationCode');
  const code = f.fields()[0].props;
  assert.equal(code.keyboardType, 'number-pad'); assert.equal(code.inputMode, 'numeric');
  assert.equal(code.textContentType, 'oneTimeCode'); assert.equal(code.autoComplete, 'one-time-code');
  await act(() => code.onChangeText('001122')); await act(() => f.tree().props.primaryAction.onPress());
  assert.deepEqual(f.calls.factor, ['001122']); assert.equal(f.calls.complete, 1); assert.equal(f.calls.prepare, 0);
});

for (const [role, sentence] of [['admin', 'accounts.invitation.admin'], ['member', 'accounts.invitation.member']] as const) {
  test(`both invitation previews disclose the actual ${role} role before joining`, async context => {
    const f = await fixture(context, 'welcome', role);
    assert.equal(f.tree().props.testID, 'accounts-invitation-entry');
    await act(() => f.tree().props.primaryAction.onPress());
    assert.equal(f.tree().props.testID, 'accounts-invitation-account');
    assert.ok(words(f.tree().props.beforeActions).includes(sentence));
    await act(() => f.tree().props.primaryAction.onPress());
    assert.equal(f.tree().type, 'AccountSignIn');
    await act(() => {f.tree().props.onUsernameChange('person'); f.tree().props.onPasswordChange('password');});
    await act(() => f.tree().props.onContinue());
    assert.equal(f.tree().props.testID, 'accounts-portico-invitation');
    assert.ok(words(f.tree().props.beforeActions).includes(sentence));
    assert.equal(f.calls.complete, 0, 'preview does not accept the invitation');
  });
}

test.todo('Fabric account entry effect replay: TypeError: f.host.replay is not a function');
