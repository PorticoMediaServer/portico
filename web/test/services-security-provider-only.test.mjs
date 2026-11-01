import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

// The Portico Account's own Security page (web.getportico.tv), same rule as server Settings (Justin,
// 1 Nov): a Google- or Apple-only account never sees Set a password; Add a password is behind More
// sign-in options. A password account keeps Change.
const google = {id: 'link-google', provider: 'google', revision: '1'};
const security = values => ({accountId: 'acct-1', familyId: 'fam-1', contactAddress: 'owner@example.test', privateEmail: false, hasPassword: false, mfaEnabled: false, recoveryCodesRemaining: 0, providers: [google], ...values});

async function panel(state) {
  const h = createRenderer(), saved = globalThis.location;
  globalThis.location = {search: '', origin: 'https://web.getportico.tv'};
  const names = new Proxy({}, {get: (_, name) => String(name)});
  const mod = await componentModule(new URL('../src/services/SecurityPanel.tsx', import.meta.url), {
    react: h.react, './i18n': {servicesI18n: () => ({t: (key, values) => values ? `${key}:${JSON.stringify(values)}` : key})},
    '../kit/accounts/ServicesWorkspace': names, '../kit/accounts/ServicesAuth': names, '../kit/accounts': names,
    '../kit/controls': {Button: 'Button', TextField: 'TextField'}, '../kit/overlays': {Dialog: 'Dialog'},
    './services.module.css': {default: {}}, './AccountExportRow': {AccountExportRow: 'AccountExportRow'},
    '../platform/account-security-policy': {accountSecurityBrowserAvailable: () => true},
    '../platform/account-security-context': {useOriginalAccountSecurity: () => undefined},
    './ProviderSecurityStartDialog': {ProviderSecurityStartDialog: 'ProviderSecurityStartDialog'},
    '../screens/settings/account-profile/ProviderPasswordFields': {ProviderPasswordFields: 'ProviderPasswordFields'},
  });
  const account = {client: {options: async () => ({credentialProviderReauthentication: 'account-settings-v1', providers: [{id: 'google', enabled: true}, {id: 'apple', enabled: true}]})}};
  const api = {security: async () => state};
  const render = () => tree(h.render(() => mod.SecurityPanel({account, api, identity: {accountId: 'acct-1', familyId: 'fam-1'}, username: 'owner', onDeleted() {}, onSecurityEnded() {}})));
  let nodes; for (let i = 0; i < 30; i++) {nodes = render(); await new Promise(resolve => setTimeout(resolve, 0));}
  nodes = render();
  const text = node => [node?.props?.children].flat().filter(value => typeof value === 'string').join('');
  const rows = () => nodes.filter(node => node.type === 'ServicesWorkspaceRow');
  return {rows, text, nodes: () => nodes, refresh: async () => {for (let i = 0; i < 4; i++) {nodes = render(); await new Promise(resolve => setTimeout(resolve, 0));} nodes = render();},
    buttons: () => nodes.filter(node => node.type === 'Button'), close: () => {h.dispose(); globalThis.location = saved;}};
}

test('a Google-only account: Password reads "Signs in with Google" with no button; More sign-in options reveals Add a password', async () => {
  const f = await panel(security());
  try {
    const password = f.rows().find(row => row.props.title === 'web.password');
    assert.equal(password.props.help, 'web.hostedSecurity.signsInWith:{"provider":"accounts.provider.google"}');
    assert.equal(tree(password).some(node => node.type === 'Button'), false, 'no button on the Password row');
    assert.equal(f.buttons().some(button => f.text(button) === 'account.setPassword'), false, 'never Set a password');
    const more = f.buttons().find(button => f.text(button) === 'web.hostedSecurity.moreSignInOptions');
    assert.ok(more); assert.equal(more.props.variant, 'text');
    more.props.onClick(); await f.refresh();
    const revealed = f.rows().find(row => row.props.title === 'web.hostedSecurity.moreSignInOptions');
    assert.ok(revealed, 'the Add a password row');
    assert.ok(tree(revealed).some(node => node.type === 'Button' && f.text(node) === 'web.hostedSecurity.addPassword'));
  } finally {f.close();}
});

test('a password account keeps Change and has no More sign-in options', async () => {
  const f = await panel(security({hasPassword: true}));
  try {
    const password = f.rows().find(row => row.props.title === 'web.password');
    assert.equal(password.props.help, 'web.hostedSecurity.passwordHelp');
    assert.ok(tree(password).some(node => node.type === 'Button' && f.text(node) === 'web.hostedSecurity.change'));
    assert.equal(f.buttons().some(button => f.text(button) === 'web.hostedSecurity.moreSignInOptions'), false);
  } finally {f.close();}
});
