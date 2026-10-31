import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import type {SetupController} from '@core/setup-controller.ts';
import type {SetupView} from '@core/setup-machine.ts';

const t = defaultI18n.t;
const escape = (text: string) => text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#x27;');

async function screen() {
  const {registerHooks} = await import('node:module');
  const {transformTsx} = await import('./transform-tsx.mjs');
  registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  const {createElement} = await import('react');
  const {renderToStaticMarkup} = await import('react-dom/server');
  const {SetupScreen} = await import('../src/screens/setup/SetupScreen.tsx');
  return (view: SetupView, busy = false) => {
    const controller = {getSnapshot: () => view, busy: () => busy, subscribe: () => () => {}} as unknown as SetupController;
    return renderToStaticMarkup(createElement(SetupScreen, {controller, serverName: () => 'Den', hostname: 'den.local'}));
  };
}
const has = (markup: string, text: string, label: string) => assert.ok(markup.includes(escape(text)), `${label}: missing "${text}"`);
const lacks = (markup: string, text: string, label: string) => assert.ok(!markup.includes('>' + escape(text) + '<'), `${label}: unexpected "${text}"`);

test('each drawn setup state shows its own catalogue words and only the actions drawn for it', async () => {
  const render = await screen();
  const back = t('action.back'), again = t('action.tryAgain'), go = t('accounts.signIn.handoff');
  // 1d-1: while it starts.
  has(render({kind: 'connecting'}), t('web.setup.preparing'), '1d-1');
  // 1: the form, Portico Account first.
  const form = render({kind: 'form', mode: 'choose', name: ''});
  for (const text of [t('web.setup.welcome'), go, t('web.setup.recommended'), t('web.setup.tabTrip')]) has(form, text, '1');
  lacks(form, back, '1 has no Back');
  // 1c: local sign-in asks for the owner.
  const local = render({kind: 'form', mode: 'local', name: 'Den'});
  for (const text of [t('web.setup.ownerTitle'), t('web.setup.createOwner'), t('web.setup.plainHttp')]) has(local, text, '1c');
  // Continue running: the ordinary Back stays available (the server decides a race with the approval).
  const starting = render({kind: 'starting', mode: 'hosted', name: 'Den', navigate: true, step: 'approval'}, true);
  has(starting, back, 'starting');
  // 1d-2: setup can't start from this address; Continue stays off.
  const address = render({kind: 'refused', reason: 'address_not_allowed'});
  has(address, t('web.setup.notOnNetwork'), '1d-2');
  assert.match(address, /disabled=""/, '1d-2 Continue is off');
  // 1d-3: this server can't reach Portico Services: Try again and Back, nothing towards local sign-in.
  const services = render({kind: 'failed', error: {code: 'claim_unavailable', retryable: true}, retryable: true, name: 'Den', hold: 'unapproved',
    retry: {to: {kind: 'starting', mode: 'hosted', name: 'Den', navigate: true, step: 'approval'}, effects: [{kind: 'request-approval', navigate: true}]}});
  for (const text of [t('web.setup.servicesUnavailableBody'), again, back]) has(services, text, '1d-3');
  lacks(services, t('web.setup.localSignIn'), '1d-3 offers no local sign-in');
  // Another browser holds setup.
  has(render({kind: 'busy_elsewhere', polls: 0}), t('web.setup.inProgressNoTime'), 'busy');
  // An attempt that was declined.
  has(render({kind: 'ended', outcome: 'denied', name: 'Den'}), t('web.claim.denied', {server: 'Den'}), 'denied');
  // Approved but the server stopped trying: the honest wording and Try again.
  const exhausted = render({kind: 'exhausted', polls: 0});
  has(exhausted, t('web.claim.finishingSlow'), 'exhausted');
  has(exhausted, again, 'exhausted');
  // 3: the first library.
  const library = render({kind: 'library'});
  for (const text of [t('web.setup.youOwn', {server: 'Den'}), t('web.setup.firstLibrary'), t('web.setup.addLibraryScan'), t('web.setup.skip')]) has(library, text, '3');
});
