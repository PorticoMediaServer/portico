// /app/account-return for a return this browser did not start: Forgot PIN's pop-up hands it to the
// page that opened it and closes; an app's sign-in browser goes on to the app. Neither completes a
// sign-in here. Stand-ins only.
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule, hooks} from './helpers/component-harness.mjs';

async function page(handed: boolean) {
  const host = hooks(), effects: (() => void)[] = [], assigned: string[] = [], completed: unknown[] = [];
  const say = (key: string) => key;
  const kit = Object.fromEntries(['ServicesActions', 'ServicesAuthStage', 'ServicesDivider', 'ServicesHeading', 'ServicesLabel', 'ServicesLead', 'ServicesNotice', 'ServicesProviderButton', 'ServicesProviderContext', 'ServicesQuiet', 'ServicesTerms'].map(name => [name, name]));
  const module = await componentModule(new URL('../src/services/AccountProviders.tsx', import.meta.url), {
    react: {...host.react, useEffect: (run: () => void) => {effects.push(run);}, default: {...host.react.default, useEffect: (run: () => void) => {effects.push(run);}}},
    '../kit/accounts/AuthStage': {AuthLinks: 'AuthLinks'}, '../kit/accounts/ServicesAuth': kit, '../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../platform/hosted-navigation': {goTo: () => {}}, '../platform/pin-provider-popup': {returnToOpener: () => handed},
    './i18n': {servicesI18n: () => ({t: say})}, './account-session': {account: {completeProvider: async (...args: unknown[]) => {completed.push(args);}}},
    './services-api': {ServicesError: class extends Error {}, thisDevice: () => ({})}, './account-problem': {problemField: () => undefined, problemMessage: () => 'problem'},
    './AccountTwoStep': {AccountTwoStep: 'AccountTwoStep'}, './AccountSignIn': {signInPath: (next: string) => next}, './services.module.css': {default: {}},
  });
  Object.assign(globalThis, {location: {origin: 'https://web.getportico.tv', assign: (url: string) => {assigned.push(url);}},
    sessionStorage: {getItem: () => null, setItem() {}, removeItem() {}}, window: {close() {}}});
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children)] : [];
  const text = (node: any) => [node.props.children].flat().join('');
  const unwrap = (node: any): any => typeof node?.type === 'function' ? unwrap(node.type(node.props)) : node;
  const draw = () => all(host.render(() => unwrap(module.AccountProviderReturn({arrival: {id: 'tx_1', code: 'c'.repeat(43), failed: false}}))));
  return {draw, text, assigned, completed, effects};
}

test('in Forgot PIN’s pop-up the return goes back to the page that opened it, and the window says it can close', async () => {
  const p = await page(true);
  const shown = p.draw();
  assert.ok(shown.some(node => node.type === 'ServicesLead' && p.text(node) === 'next.account.provider.returnedClose'));
  assert.deepEqual([p.assigned, p.completed], [[], []], 'nothing completed or followed here');
});

test('in an app’s sign-in browser the return goes on to the app, by its own button, and nothing is completed here', async () => {
  const p = await page(false);
  const shown = p.draw();
  const go = shown.find(node => node.type === 'Button');
  assert.equal(p.text(go), 'next.account.provider.continueInApp');
  assert.deepEqual(p.assigned, [], 'only on press');
  go.props.onClick();
  assert.equal(p.assigned.length, 1);
  assert.match(p.assigned[0]!, /^portico:\/\/account-return\?transactionId=tx_1&code=/);
  assert.deepEqual(p.completed, []);
});
