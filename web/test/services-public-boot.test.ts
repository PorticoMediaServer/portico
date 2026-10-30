import assert from 'node:assert/strict';
import test from 'node:test';
import {ApiError} from '../../packages/client-core/src/index.ts';
import {CredentialSessionService, type CredentialRecord} from '../../packages/client-core/src/credentials.ts';
import {ServicesBootstrap} from '../src/services/bootstrap.ts';
import {captureServicesEntry} from '../src/services/entry.ts';
import {servicesI18n} from '../src/services/i18n.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {ServicesLink, ServicesLinkStore} from '../src/services/intake.ts';
import {verifyEmail, resetPassword} from '../src/services/link-effects.ts';
import {handoverDocumentBoot} from '../src/shell/boot-handover.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, textOf, tree} from './helpers/interaction-harness.mjs';

const origin = 'https://account.example';
const token = Buffer.from(JSON.stringify(['1', 'registration', 'registration_123', '482917'])).toString('base64url');

function documentPort() {
  const element = () => {
    const attributes = new Map<string, string>();
    return {removed: false, setAttribute: (key: string, value: string) => {attributes.set(key, value);},
      hasAttribute: (key: string) => attributes.has(key), removeAttribute: (key: string) => {attributes.delete(key);},
      remove() {this.removed = true;}};
  };
  const boot = element(), root = element();
  const document = {documentElement: {dataset: {}}, body: {dataset: {}}, defaultView: {matchMedia: () => ({matches: false})},
    getElementById: (id: string) => id === 'boot' ? boot.removed ? null : boot : id === 'root' ? root : null} as unknown as Document;
  return {document, boot};
}

/** Actual Services root/route, link effects and credential coordinator. The harness
 * models commit hooks; layout, mail delivery and a real account remain separate. */
async function opening(path: string, savedSession: boolean) {
  let location = new URL(path, origin), stored: CredentialRecord<{familyId: string}> | undefined;
  const requests: string[] = [];
  const service = new CredentialSessionService<{familyId: string}>({
    authorityFromSession: session => ({familyId: session.familyId}),
    refresh: async () => {requests.push('refresh:401'); throw new ApiError(401, 'unauthorized', 'Sign in again.', false);},
    revoke: async () => {},
  }, {load: async () => stored && structuredClone(stored), save: async value => {stored = structuredClone(value);}});
  if (savedSession) await service.adopt({account: {id: 'account', username: 'person', displayName: 'Person'}, familyId: 'old_family',
    accessToken: 'expired-access', expiresAt: new Date(Date.now() - 1000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString()});
  const account = {service, client: {
    registration: async () => {requests.push('registration'); return null;},
    request: async (path: string) => {requests.push(path); return {kind: 'registration', status: 'confirmed'};},
  }} as unknown as ServicesAccount;
  let link: ServicesLink | undefined;
  const store: ServicesLinkStore = {read: async () => link, change: async update => {link = update(link);}};
  const page = {get href() {return location.href;}, get pathname() {return location.pathname;}, get search() {return location.search;}, get hash() {return location.hash;}, replace() {assert.fail('unexpected navigation');}};
  const entry = captureServicesEntry(page, {state: null, replaceState: (_state, _title, path) => {location = new URL(String(path), origin);}}, origin);
  const bootstrap = new ServicesBootstrap(account, () => store, entry.capture);
  const h = createRenderer(), {document, boot} = documentPort();
  const previousLocation = Object.getOwnPropertyDescriptor(globalThis, 'location'), previousDocument = Object.getOwnPropertyDescriptor(globalThis, 'document');
  Object.defineProperty(globalThis, 'location', {value: page, configurable: true});
  Object.defineProperty(globalThis, 'document', {value: document, configurable: true});
  const {DocumentBootReady} = await componentModule(new URL('../src/shell/DocumentBootReady.tsx', import.meta.url), {
    react: h.react, './boot-handover': {handoverDocumentBoot},
  });
  const stage = Object.fromEntries(['ServicesAuthStage', 'ServicesHeading', 'ServicesLead', 'ServicesNotice', 'ServicesActions', 'ServicesStateIcon'].map(name => [name, name]));
  const pages = await componentModule(new URL('../src/services/LinkPages.tsx', import.meta.url), {
    react: h.react, './i18n': {servicesI18n}, './link-effects': {verifyEmail, resetPassword},
    '../kit/accounts/ServicesAuth': stage, '../kit/controls': {Button: 'Button'}, '../kit/accounts': {AuthPasswordField: 'Password'},
  });
  let Root!: () => unknown;
  const routes = new Map<string, {component: () => unknown}>();
  const root = {useRouteContext: () => ({account, bootstrap, invalidContinuation: entry.invalidContinuation}), addChildren() {return root;}};
  await componentModule(new URL('../src/services/ServicesApp.tsx', import.meta.url), {
    react: h.react, '../shell/DocumentBootReady': {DocumentBootReady}, './i18n': {servicesI18n}, './LinkPages': pages,
    '../kit/accounts/ServicesAuth': stage, './AuthForm': {ServicesAuthForm: () => h.react.createElement(DocumentBootReady, {}, h.react.createElement('SignInForm'))}, './DevicePage': {DevicePage: 'DevicePage'},
    '../kit/states': {StateView: 'StartupError'},
    '@tanstack/react-router': {
      createRootRouteWithContext: () => (options: {component: () => unknown}) => {Root = options.component; return root;},
      createRoute: (route: {path: string; component: () => unknown}) => {routes.set(route.path, route); return {...route, useSearch: () => ({tab: 'devices'})};},
      Outlet: () => h.react.createElement(routes.get(location.pathname)!.component),
    },
  });
  return {bootstrap, service, store, requests, page, boot, render: () => h.render(Root), dispose() {
    h.dispose(); bootstrap.dispose();
    if (previousLocation) Object.defineProperty(globalThis, 'location', previousLocation); else Reflect.deleteProperty(globalThis, 'location');
    if (previousDocument) Object.defineProperty(globalThis, 'document', previousDocument); else Reflect.deleteProperty(globalThis, 'document');
  }};
}

test('an anonymous mailed verification link boots, leaves the splash and shows the drawn other-device result', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const f = await opening('/verify-email?token=' + token, false);
  try {
    f.render(); assert.equal(f.boot.hasAttribute('data-leaving'), false);
    await f.bootstrap.start();
    f.render();
    for (let i = 0; i < 12; i++) await Promise.resolve();
    const text = textOf(f.render());
    assert.match(text, /Email confirmed/);
    assert.match(text, /Email confirmed\. Go back to the device where you started\./);
    assert.doesNotMatch(text, /Continue to Portico|Sign in/);
    assert.equal(f.service.getSnapshot().phase, 'signedOut');
    assert.deepEqual(f.requests, ['registration', '/v1/email-links/confirm']);
    assert.equal(f.page.search, ''); assert.equal(f.page.hash, '');
    t.mock.timers.tick(320); assert.equal(f.boot.removed, true);
  } finally {f.dispose();}
});

test('an account restore failure reveals a retryable page instead of leaving the document splash', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const f = await opening('/', false), read = f.store.read;
  f.store.read = async () => {throw new Error('protected storage unavailable');};
  try {
    await f.bootstrap.start();
    const shown = tree(f.render());
    assert.equal(f.bootstrap.getSnapshot().phase, 'error');
    const error = shown.find(node => node.type === 'StartupError');
    assert.ok(error);
    t.mock.timers.tick(320); assert.equal(f.boot.removed, true);
    f.store.read = read;
    error.props.primary.onPress();
    await f.bootstrap.start();
    assert.equal(f.bootstrap.getSnapshot().phase, 'ready');
    assert.ok(tree(f.render()).some(node => node.type === 'SignInForm'));
  } finally {f.dispose();}
});

test('a boot refresh 401 resolves to signed out and reveals every public account destination', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  for (const [path, expected] of [
    ['/verify-email#token=' + token, 'Email confirmed'], ['/reset?token=reset_token', 'Choose a new password'],
    ['/reset-password?token=reset_token', 'Choose a new password'], ['/', 'SignInForm'], ['/account', 'SignInForm'], ['/device', 'DevicePage'],
  ]) {
    const f = await opening(path, true);
    try {
      await f.bootstrap.start();
      assert.equal(f.bootstrap.getSnapshot().phase, 'ready');
      assert.equal(f.service.getSnapshot().phase, 'signedOut');
      f.render(); for (let i = 0; i < 12; i++) await Promise.resolve();
      const shown = f.render();
      if (expected.endsWith('Form') || expected === 'DevicePage') assert.ok(tree(shown).some(node => node.type === expected), path);
      else assert.ok(textOf(shown).includes(expected), path + ': ' + textOf(shown));
      assert.equal(f.requests.filter(value => value === 'refresh:401').length, 1);
      assert.equal(f.boot.hasAttribute('data-leaving'), true, path);
      t.mock.timers.tick(320); assert.equal(f.boot.removed, true, path);
    } finally {f.dispose();}
  }
});
