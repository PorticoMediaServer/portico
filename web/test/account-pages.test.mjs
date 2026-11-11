import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tick, tree, textOf} from './helpers/interaction-harness.mjs';

// The Portico Account pages at web.getportico.tv against a stand-in for Portico Services. Each
// test drives a page's real code: what it sends, what it keeps and where it goes next.

const memory = () => { const m = new Map(); return {getItem: k => m.get(k) ?? null, setItem: (k, v) => m.set(k, String(v)), removeItem: k => m.delete(k), clear: () => m.clear(), dump: () => JSON.stringify([...m])}; };
globalThis.localStorage = memory();
globalThis.sessionStorage = memory();
const assigned = [];
globalThis.location = {pathname: '/', search: '', hash: '', assign: url => assigned.push(url)};
globalThis.history = {state: null, replaceState: (_s, _t, url) => { globalThis.location.hash = ''; globalThis.location.pathname = url; }};

/** Portico Services, as far as one test needs it: `routes['POST /v1/accounts'] = body => [status, answer]`. */
const calls = [];
let routes = {};
globalThis.fetch = async (path, init) => {
  const body = init.body ? JSON.parse(init.body) : undefined;
  calls.push({route: `${init.method} ${path}`, body, headers: init.headers});
  const handler = routes[`${init.method} ${path}`];
  const [status, answer] = handler ? handler(body) : [404, {error: {code: 'not_found'}}];
  return {ok: status < 300, status, headers: {get: () => null}, text: async () => answer === undefined ? '' : JSON.stringify(answer)};
};
const refusal = (code, fields) => ({error: {code, message: 'words from Services', fields}});
const tokens = {accessToken: 'pat_test', refreshToken: 'prt_test_refresh_token_long', expiresAt: new Date(Date.now() + 3600_000).toISOString()};

const api = await import('../src/services/services-api.ts');
const session = await import('../src/services/account-session.ts');
const problems = await import('../src/services/account-problem.ts');
const signUpMemory = await import('../src/services/account-sign-up-memory.ts');
const {enUS} = await import('../../packages/i18n/src/catalog/en-US.ts');

/** The pages, wired to each other, with the kit drawn as plain named elements. */
async function pages() {
  const h = createRenderer();
  const went = [];
  const named = new Proxy({}, {get: (_, key) => String(key)});
  const common = {
    react: h.react, '../kit/accounts': named, '../kit/accounts/ServicesAuth': named, '../kit/controls': named,
    '../platform/hosted-navigation': {goTo: to => went.push(to)},
    './i18n': {servicesI18n: () => ({t: (id, values) => values ? `${id} ${JSON.stringify(values)}` : id, date: value => value})},
    './services-api': api, './account-session': session, './account-problem': problems, './account-sign-up-memory': signUpMemory,
    './AccountProviders': {ProviderButtons: () => null}, './services.module.css': {default: named},
  };
  const twoStep = await componentModule(new URL('../src/services/AccountTwoStep.tsx', import.meta.url), common);
  const signIn = await componentModule(new URL('../src/services/AccountSignIn.tsx', import.meta.url), {...common, './AccountTwoStep': twoStep});
  const signUp = await componentModule(new URL('../src/services/AccountSignUp.tsx', import.meta.url), {...common, './AccountSignIn': signIn});
  const reset = await componentModule(new URL('../src/services/AccountReset.tsx', import.meta.url), {...common, './AccountSignIn': signIn, './AccountSignUp': signUp});
  const approve = await componentModule(new URL('../src/services/ApproveServer.tsx', import.meta.url), {...common, './AccountSignIn': signIn, './AccountSignUp': signUp});
  const tv = await componentModule(new URL('../src/services/LinkTV.tsx', import.meta.url), {...common, './AccountSignIn': signIn, './AccountSignUp': signUp});
  return {h, went, signIn, signUp, reset, approve, tv};
}
const nodes = rendered => tree(rendered);
const field = (rendered, name) => nodes(rendered).find(node => node.props?.name === name);
const type = (rendered, name, value) => field(rendered, name).props.onChange({currentTarget: {value}});
const submit = async rendered => { await nodes(rendered).find(node => node.type === 'form').props.onSubmit({preventDefault() {}}); };
const press = async (rendered, label) => {
  const found = nodes(rendered).find(node => node.props?.onClick && textOf(node.props.children) === label);
  assert.ok(found, `no control "${label}"`);
  await found.props.onClick();
};
const text = rendered => nodes(rendered).map(node => [textOf(node.props?.children), ...(node.props?.items ?? []).map(item => `${textOf(item.label)} ${textOf(item.value)}`)].join(' ')).join('\n');
const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
const fresh = () => { calls.length = 0; routes = {}; assigned.length = 0; session.accountSession.forget(); localStorage.clear(); sessionStorage.clear(); signUpMemory.forgetSignUp(); };

test('sign-up, then the code confirms the account this tab signed up and signs it in', async () => {
  fresh();
  const {h, went, signUp} = await pages();
  routes['POST /v1/accounts'] = () => [202, {status: 'confirm_email', email: 'ada@example.test', accountId: 'acct_own'}];
  let page = h.render(() => h.react.createElement(signUp.AccountSignUp, {next: '/account'}));
  type(page, 'email', 'ada@example.test'); type(page, 'username', 'ada'); type(page, 'new-password', 'long enough password');
  page = h.render(() => h.react.createElement(signUp.AccountSignUp, {next: '/account'}));
  await submit(page); await tick();
  assert.equal(calls[0].route, 'POST /v1/accounts');
  assert.ok(calls[0].headers['Idempotency-Key'], 'sign-up makes something, so it carries an Idempotency-Key');
  assert.match(went.at(-1), /^\/sign-up\/confirm/);

  routes['POST /v1/accounts/confirm'] = body => body.accountId === 'acct_own' && body.code === 'ABCD-EFGH' ? [200, {status: 'confirmed', tokens}] : [422, refusal('code_wrong')];
  let confirm = h.render(() => h.react.createElement(signUp.AccountConfirm, {next: '/account'}));
  assert.match(text(confirm), /ada@example\.test/);
  type(confirm, 'one-time-code', 'abcd efgh');
  confirm = h.render(() => h.react.createElement(signUp.AccountConfirm, {next: '/account'}));
  await submit(confirm); await tick();
  assert.equal(calls.at(-1).body.accountId, 'acct_own', 'the confirm page names the account its own sign-up was answered with');
  assert.equal(session.accountSession.signedIn(), true);
  assert.equal(went.at(-1), '/account');
  h.dispose();
});

test('a confirm page this tab did not sign up from sends nothing and asks the person to sign in', async () => {
  fresh();
  const {h, signUp} = await pages();
  globalThis.location.search = '?accountId=acct_from_a_link';
  const page = h.render(() => h.react.createElement(signUp.AccountConfirm, {next: '/account'}));
  globalThis.location.search = '';
  assert.match(text(page), /next\.account\.confirm\.lost/);
  assert.equal(calls.length, 0);
  h.dispose();
});

test('a right password for an unconfirmed account goes to "check your email" for the account that sign-in named', async () => {
  fresh();
  const {h, went, signIn} = await pages();
  routes['POST /v1/sessions'] = () => [403, refusal('confirm_email', {accountId: 'acct_waiting'})];
  let page = h.render(() => h.react.createElement(signIn.AccountSignIn, {next: '/approve/apr_1'}));
  type(page, 'username', 'ada@example.test'); type(page, 'password', 'long enough password');
  page = h.render(() => h.react.createElement(signIn.AccountSignIn, {next: '/approve/apr_1'}));
  await submit(page); await tick();
  assert.equal(signUpMemory.signUpInProgress().accountId, 'acct_waiting');
  assert.match(went.at(-1), /^\/sign-up\/confirm\?next=%2Fapprove%2Fapr_1$/);
  h.dispose();
});

test('sign-in with two-step: a wrong code is refused in words, then the right one signs in', async () => {
  fresh();
  const {h, went, signIn} = await pages();
  routes['POST /v1/sessions'] = () => [200, {twoStep: {challengeId: 'tsc_1'}}];
  const draw = () => h.render(() => h.react.createElement(signIn.AccountSignIn, {next: '/account'}));
  let page = draw();
  type(page, 'username', 'ada'); type(page, 'password', 'long enough password');
  page = draw(); await submit(page); await tick();
  page = draw();
  assert.match(text(page), /next\.account\.twoStep\.title/);
  routes['POST /v1/sessions/two-step'] = body => body.code === '123456' ? [200, {tokens}] : [403, refusal('two_step_wrong')];
  type(page, 'one-time-code', '000000'); page = draw(); await submit(page); await tick(); page = draw();
  assert.match(text(page), /next\.account\.problem\.twoStepWrong/);
  assert.equal(session.accountSession.signedIn(), false);
  type(page, 'one-time-code', '123456'); page = draw(); await submit(page); await tick();
  assert.deepEqual(calls.at(-1).body, {challengeId: 'tsc_1', code: '123456'});
  assert.equal(session.accountSession.signedIn(), true);
  assert.equal(went.at(-1), '/account');
  h.dispose();
});

test('reset: the code and a new password set it, every sign-in here ends, and a connected Google is named', async () => {
  fresh();
  session.accountSession.keep(tokens);
  const {h, reset} = await pages();
  routes['POST /v1/password-reset'] = () => [202, {status: 'sent_if_known'}];
  routes['POST /v1/password-reset/confirm'] = body => body.email === 'ada@example.test' && body.code === 'ABCD-EFGH' ? [200, {status: 'password_set', providers: ['google']}] : [422, refusal('code_wrong')];
  const draw = () => h.render(() => h.react.createElement(reset.AccountReset, {next: '/account'}));
  let page = draw();
  type(page, 'email', 'ada@example.test'); page = draw(); await submit(page); await tick(); page = draw();
  type(page, 'one-time-code', 'abcdefgh'); type(page, 'new-password', 'a new long password'); page = draw();
  await submit(page); await tick(); page = draw();
  assert.match(text(page), /next\.account\.reset\.doneTitle/);
  assert.match(text(page), /next\.account\.reset\.providers/);
  assert.equal(session.accountSession.signedIn(), false);
  h.dispose();
});

test('approving a server by link sends the browser back to the server with the approval', async () => {
  fresh();
  session.accountSession.keep(tokens);
  const {h, approve} = await pages();
  routes['GET /v1/approvals/apr_1'] = () => [200, {id: 'apr_1', state: 'waiting', serverId: 'srv_abc', serverName: 'Living room', purpose: 'setup'}];
  routes['GET /v1/account'] = () => [200, {username: 'ada', displayName: 'Ada', email: 'ada@example.test'}];
  routes['POST /v1/approvals/apr_1/decision'] = body => body.approve ? [200, {state: 'approved', returnUrl: 'https://192.168.1.20:32400/setup/return', approval: 'signed'}] : [200, {state: 'declined', returnUrl: 'x'}];
  const draw = () => h.render(() => h.react.createElement(approve.ApproveServer, {id: 'apr_1'}));
  draw(); await tick(); await tick();
  let page = draw();
  assert.match(text(page), /srv_abc/, 'the server id is shown to compare');
  assert.equal(calls.filter(call => call.route.endsWith('/decision')).length, 0, 'nothing is decided on page load');
  await press(page, 'next.account.approve.approve'); await settle();
  assert.deepEqual(assigned, ['https://192.168.1.20:32400/setup/return#approval=signed']);
  h.dispose();
});

test('a refused approval says why in words and does not offer Approve again', async () => {
  fresh();
  session.accountSession.keep(tokens);
  const {h, approve} = await pages();
  routes['GET /v1/approvals/apr_2'] = () => [200, {id: 'apr_2', state: 'waiting', serverId: 'srv_def', serverName: 'Cabin', purpose: 'setup'}];
  routes['GET /v1/account'] = () => [200, {username: 'ada', displayName: '', email: 'ada@example.test'}];
  routes['POST /v1/approvals/apr_2/decision'] = body => body.approve ? [409, refusal('too_many_servers')] : [200, {state: 'declined', returnUrl: 'https://192.168.1.20:32400/setup/return'}];
  const draw = () => h.render(() => h.react.createElement(approve.ApproveServer, {id: 'apr_2'}));
  draw(); await tick(); await tick();
  await press(draw(), 'next.account.approve.approve'); await settle();
  const page = draw();
  assert.match(text(page), /next\.account\.problem\.tooManyServers/);
  assert.ok(!nodes(page).some(node => textOf(node.props?.children) === 'next.account.approve.approve'));
  assert.equal(assigned.length, 0);
  await press(page, 'next.account.approve.decline'); await settle();
  assert.match(text(draw()), /next\.account\.approve\.declinedTitle/);
  h.dispose();
});

test("a TV's code leaves the address at once, is never stored, and is looked up but not approved without a tap", async () => {
  fresh();
  session.accountSession.keep(tokens);
  globalThis.location.pathname = '/tv'; globalThis.location.hash = '#code=rhcb-pw3v';
  const {h, tv} = await pages();
  routes['POST /v1/tv-codes/lookup'] = body => body.code === 'RHCB-PW3V' ? [200, {id: 'tvc_1', device: {name: 'Living\nroom‮', kind: 'Apple TV'}}] : [410, refusal('tv_code_gone')];
  routes['GET /v1/account'] = () => [200, {username: 'ada'}];
  const draw = () => h.render(() => h.react.createElement(tv.LinkTV));
  draw();
  assert.equal(globalThis.location.hash, '');
  draw(); await tick(); await tick(); await tick();
  const page = draw();
  assert.match(text(page), /RHCB-PW3V/);
  assert.match(text(page), /next\.account\.tv\.fromLink/);
  assert.match(text(page), /"name":"Livingroom"/, 'the name keeps nothing unprintable');
  assert.ok(!localStorage.dump().includes('RHCB') && !sessionStorage.dump().includes('RHCB'));
  assert.ok(calls.every(call => !call.route.includes('RHCB')), 'the code travels only in the lookup body');
  assert.equal(calls.filter(call => call.route.includes('/decision')).length, 0);
  h.dispose();
});

test('every refusal these pages can meet has its own sentence, and every sentence exists', () => {
  const codes = ['unreachable', 'busy', 'slow_down', 'wait', 'signed_out', 'credentials', 'username_taken', 'email_unavailable', 'code_wrong',
    'two_step_wrong', 'challenge_gone', 'approval_gone', 'already_answered', 'server_owned', 'too_many_servers', 'server_not_linked', 'confirm_email',
    'password_wrong', 'sign_in_again', 'no_password', 'email_limit', 'email_in_use', 'owns_server', 'last_way_in', 'two_step_on',
    'two_step_not_started', 'provider_in_use', 'provider_unavailable', 'not_owner', 'owner_cannot_leave'];
  for (const code of codes) {
    const id = problems.problemMessage(new api.ServicesError(400, code), 'account');
    assert.notEqual(id, 'next.account.problem.internal', `${code} has no sentence`);
    assert.ok(enUS[id], `${id} is not in the catalog`);
  }
  for (const field of ['email', 'username', 'displayName', 'password', 'newPassword', 'newEmail'])
    assert.notEqual(problems.problemMessage(new api.ServicesError(422, 'invalid', {[field]: 'x'}), 'signUp'), 'next.account.problem.internal');
});

test('a sign-in that has run out is renewed before the call, and one ended elsewhere is forgotten', async () => {
  fresh();
  session.accountSession.keep({...tokens, expiresAt: new Date(Date.now() - 1000).toISOString()});
  routes['POST /v1/sessions/refresh'] = () => [200, {...tokens, accessToken: 'pat_renewed'}];
  routes['GET /v1/account'] = () => [200, {username: 'ada'}];
  await session.personCall('GET', '/v1/account');
  assert.equal(calls.at(-1).headers.Authorization, 'Bearer pat_renewed');
  routes['GET /v1/account'] = () => [401, refusal('signed_out')];
  routes['POST /v1/sessions/refresh'] = () => [401, refusal('signed_out')];
  await assert.rejects(session.personCall('GET', '/v1/account'));
  assert.equal(session.accountSession.signedIn(), false);
});

test('after Google or Apple, the one-use code leaves the address at once and is finished only with this tab\'s secret', async () => {
  fresh();
  const {h, went} = await pages();
  const named = new Proxy({}, {get: (_, key) => String(key)});
  const providers = await componentModule(new URL('../src/services/AccountProviders.tsx', import.meta.url), {
    react: h.react, '../kit/accounts': named, '../kit/accounts/ServicesAuth': named, '../kit/controls': named,
    '../platform/hosted-navigation': {goTo: to => went.push(to)}, './i18n': {servicesI18n: () => ({t: id => id})},
    './services-api': api, './account-session': session, './account-problem': problems, './AccountTwoStep': {AccountTwoStep: () => null},
    './AccountSignIn': {signInPath: next => `/sign-in?next=${next}`}, './services.module.css': {default: named},
  });
  globalThis.location.pathname = '/app/account-return'; globalThis.location.hash = '#tx=sso_1&code=one-use';
  const arrival = providers.captureProviderReturn(globalThis.location, globalThis.history);
  assert.equal(globalThis.location.hash, '');
  assert.deepEqual(arrival, {id: 'sso_1', code: 'one-use', failed: false});
  sessionStorage.setItem('portico.account.provider.sso_1', JSON.stringify({secret: 'kept-by-this-tab', next: '/approve/apr_1', provider: 'google', connect: false}));
  routes['POST /v1/sessions/provider/complete'] = body => body.secret === 'kept-by-this-tab' && body.code === 'one-use' ? [200, {tokens}] : [410, refusal('sign_in_gone')];
  h.render(() => h.react.createElement(providers.AccountProviderReturn, {arrival}));
  for (let i = 0; i < 6; i++) await tick();
  assert.equal(session.accountSession.signedIn(), true);
  assert.equal(went.at(-1), '/approve/apr_1');
  assert.equal(sessionStorage.getItem('portico.account.provider.sso_1'), null, 'the secret is used once');
  h.dispose();
});
