import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, textOf} from './helpers/interaction-harness.mjs';

// P2-66 (Justin, binding): during a move to Portico Accounts the server's sign-in says so, then
// offers the old sign-in with the server's own remaining days. A fresh Portico server shows neither.
async function render(props) {
  const h = createRenderer();
  const label = (key, values) => values ? `${key}:${JSON.stringify(values)}` : key;
  const mod = await componentModule(new URL('../src/kit/accounts/ServerSignIn.tsx', import.meta.url), {
    react: h.react, '@i18n': {defaultI18n: {t: label, locale: 'en-US'}},
    '../controls/Button': {Button: 'Button'}, '../controls/Fields': {TextField: 'TextField'},
    './AuthStage': {AuthError: 'AuthError', AuthLinks: 'AuthLinks', AuthPasswordField: 'AuthPasswordField', AuthStage: 'AuthStage'},
    '../Icon': {Icon: 'Icon'}, './sign-in-retry': {signInRetryClock: () => '', signInRetrySeconds: () => 0}, './accounts.module.css': {default: {}},
  });
  const nodes = tree(h.render(() => mod.PorticoServerSignIn({serverName: 'Contabo', onContinue() {}, onRecovery() {}, onOldPassword() {}, ...props})));
  h.dispose();
  return nodes.map(node => textOf(node)).join('\n');
}

test('a moved server states the move and offers the old sign-in with its remaining days', async () => {
  const text = await render({moveDays: 3});
  assert.match(text, /accounts\.move\.toPortico:\{"server":"Contabo"\}/);
  assert.match(text, /accounts\.move\.oldPasswordAction:\{"days":3\}/);
  assert.ok(text.indexOf('accounts.move.toPortico') < text.indexOf('accounts.move.oldPasswordAction'), 'the context comes first');
});

test('a fresh or fully reconnected Portico server shows neither', async () => {
  const text = await render({});
  assert.doesNotMatch(text, /accounts\.move\./);
});
