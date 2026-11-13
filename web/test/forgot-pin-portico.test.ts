// Forgot PIN on a server that uses Portico Accounts, on the web: the Portico Account's password is
// typed here and checked by Portico Services (owner's rule, 12 Nov). Stand-ins for the controller
// (client-core, #3075) and for the account; nothing calls the real Services.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule, hooks} from './helpers/component-harness.mjs';

/** The wrapper (`platform/profile-recovery.ts`) around a stand-in controller in its Portico step. */
async function wrapper(options: {hostedWeb: boolean; who?: () => Promise<unknown>}) {
  const calls: unknown[][] = [], listeners = new Set<() => void>();
  class Controller {
    private readonly state = Object.freeze({phase: 'portico'});
    getSnapshot = () => this.state;
    subscribe = (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);};
    async porticoPassword(...args: unknown[]) {calls.push(['porticoPassword', ...args]);}
    dispose() {}
  }
  const account = {serverPINIdentity: async () => {calls.push(['Services:identity']); return {identity: {}};}};
  const module = await componentModule(new URL('../src/platform/profile-recovery.ts', import.meta.url), {
    '@core/profile-recovery.ts': {ProfileRecoveryController: Controller},
    '../services/account-session': {account, accountSession: {signIns: () => 1, signedIn: () => true}},
    './portico-account-holder': {porticoAccountHolder: {who: options.who ?? (async () => {calls.push(['who']); return {id: 'acct', username: 'ada', hasPassword: true, twoStep: true};})}},
    './environment': {currentWebEnvironment: () => ({hostedWeb: options.hostedWeb})},
  });
  const flow = new module.BrowserProfileRecovery({baseUrl: 'https://den.example'}, {}, 'alex', 'portico', 'family', () => true);
  return {flow, calls, module};
}

test('at a server\'s own address the Portico step says where to go, and the account client is never used', async () => {
  const w = await wrapper({hostedWeb: false, who: async () => {throw new Error('the account was read at a server\'s own address');}});
  await w.flow.preparePortico();
  assert.deepEqual(w.flow.getSnapshot().portico, {phase: 'elsewhere'});
  await w.flow.porticoPassword('secret', '');
  assert.deepEqual(w.calls, [], 'nothing is sent anywhere');
  await assert.rejects(w.module.browserPorticoPINProof(), {code: 'portico_account_elsewhere'}, 'refused outright off web.getportico.tv');
});

test('a failed Portico Account lookup stays in the flow\'s own state, and a good one gives its facts and its proof', async () => {
  const failing = await wrapper({hostedWeb: true, who: async () => {throw Object.assign(new Error('signed out'), {code: 'signed_out'});}});
  await failing.flow.preparePortico();
  const state = failing.flow.getSnapshot();
  assert.equal(state.phase, 'portico'); assert.equal(state.portico.phase, 'failed'); assert.equal(state.portico.error.code, 'signed_out');
  assert.equal(failing.flow.getSnapshot(), state, 'one stable snapshot until something changes');

  const good = await wrapper({hostedWeb: true});
  await good.flow.preparePortico();
  assert.deepEqual(good.flow.getSnapshot().portico, {phase: 'ready', account: {username: 'ada', hasPassword: true, twoStep: true}});
  await good.flow.porticoPassword('secret', '123456');
  const sent = good.calls.find(call => call[0] === 'porticoPassword')!;
  assert.deepEqual(sent.slice(1, 3), ['secret', '123456']); assert.equal((sent[3] as {accountId: string}).accountId, 'acct', 'with the account this page holds');
});

/** The dialog (`kit/accounts/ProfilePINRecovery.tsx`) in its Portico step. */
async function dialog(portico: unknown, error?: unknown) {
  const host = hooks(), actions: unknown[][] = [];
  const {ProfilePINRecovery} = await componentModule(new URL('../src/kit/accounts/ProfilePINRecovery.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n: {t: (key: string, values?: unknown) => values ? `${key}:${JSON.stringify(values)}` : key}},
    '../overlays/Dialog': {Dialog: 'Dialog'}, '../controls/Button': {Button: 'Button'},
    './AuthStage': {AuthCodeBoxes: 'CodeBoxes', AuthError: 'AuthError', AuthPasswordField: 'PasswordField'}, './accounts.module.css': {default: {}},
  });
  const record = (name: string) => (...args: unknown[]) => {actions.push([name, ...args]);};
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children), ...all(node.props.headerAction)] : [];
  const draw = () => all(host.render(() => ProfilePINRecovery({profileName: 'Alex', username: 'demo', phase: 'portico', portico, error,
    onPassword: record('password'), onPorticoPassword: record('porticoPassword'), onPorticoRetry: record('retry'), onPorticoElsewhere: record('elsewhere'),
    onSetPassword: record('setPassword'), onCode: record('code'), onPIN: record('pin'), onClose: record('close'), onOpen: record('open')})));
  const text = (nodes: any[]) => nodes.flatMap(node => [node.props.children].flat()).filter(value => typeof value === 'string');
  return {draw, actions, text};
}

test('the Portico step: elsewhere hands on to web.getportico.tv; an account with no password is told to set one', async () => {
  const elsewhere = await dialog({phase: 'elsewhere'});
  let page = elsewhere.draw();
  assert.ok(elsewhere.text(page).includes('accounts.profiles.porticoElsewhere'));
  page.find(node => node.type === 'Button' && [node.props.children].flat().includes('accounts.signIn.handoff')).props.onClick();
  assert.deepEqual(elsewhere.actions, [['elsewhere']]);
  assert.equal(page.some(node => node.type === 'PasswordField'), false, 'no password is asked where it cannot be checked');

  for (const [account, error] of [[{username: 'ada', hasPassword: false, twoStep: false}, undefined], [{username: 'ada', hasPassword: true, twoStep: false}, {code: 'account_password_required'}]] as const) {
    const unset = await dialog({phase: 'ready', account}, error);
    page = unset.draw();
    assert.ok(unset.text(page).includes('accounts.profiles.porticoNeedsPassword'), 'Set a password for your Portico Account first.');
    page.find(node => node.type === 'Button').props.onClick();
    assert.deepEqual(unset.actions, [['setPassword']]);
  }
});

test('the Portico step asks for the two-step code with the password when it is on, and Services\' line replaces frame 18\'s footer', async () => {
  const d = await dialog({phase: 'ready', account: {username: 'ada', hasPassword: true, twoStep: true}});
  let page = d.draw();
  assert.equal(page.find(node => node.type === 'PasswordField').props.label, 'accounts.profiles.porticoPassword:{"username":"ada"}');
  assert.ok(d.text(page).includes('accounts.profiles.porticoChecked'));
  assert.equal(d.text(page).includes('accounts.profiles.porticoInstead'), false);
  page.find(node => node.type === 'PasswordField').props.onChange({currentTarget: {value: 'secret'}}); page = d.draw();
  const submit = () => page.find(node => node.type === 'Button' && node.props.type === 'submit');
  assert.equal(submit().props.disabled, true, 'not without the code: an empty code would count as a wrong one');
  page.find(node => node.type === 'CodeBoxes').props.onChange('123456'); page = d.draw();
  assert.equal(submit().props.disabled, false);
  page.find(node => node.type === 'form').props.onSubmit({preventDefault() {}});
  assert.deepEqual(d.actions, [['porticoPassword', 'secret', '123456']]);
});

test('a wrong password or two-step code says so, not that the profile lacks access', async () => {
  const {presentError} = await import('@core/presentation/errors.ts');
  const {defaultI18n} = await import('@i18n');
  for (const [code, id] of [['password_wrong', 'next.account.problem.passwordWrong'], ['two_step_wrong', 'next.account.problem.twoStepWrong']])
    assert.equal(presentError(Object.assign(new Error('refused'), {code, status: 403}), 'sign-in', {operation: 'action', i18n: defaultI18n}).messageId, id, code);
});
