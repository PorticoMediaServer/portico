// Signing in with a Portico Account at a server's own HTTPS page (option 2): the server page's start
// and return steps, and web.getportico.tv's return page. Stand-ins only: nothing calls a live server
// or the live Portico Services.
import test from 'node:test';
import assert from 'node:assert/strict';
import {systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {parseServerSignIn, type ServerSignInProbe} from '@core/sign-in.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {presentError} from '@core/presentation/errors.ts';
import {defaultI18n} from '@i18n';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import {dispatchPorticoReturn} from '../src/platform/portico-return-dispatch.ts';
import {accountReturnFinish, accountReturnStart, httpsOrigin, keepAccountReturn, readReturnFragment, takeAccountReturn} from '../src/platform/portico-account-return.ts';
import {listedReturn, readServerReturnAsk} from '../src/services/AccountServerReturn.tsx';

const origin = 'https://66-94-102-39.ptc-lik6kb74q4cwxmcuravq.portico.direct:32510', serverId = 'srv_' + 'A'.repeat(43), hosted = 'https://web.getportico.tv';
// Shaped like a real server's challenge, which has dots in it.
const challenge = 'c'.repeat(40) + '.' + 'd'.repeat(40) + '.e_f-g', note = {payload: btoa(JSON.stringify({kind: 'portico.identity.v1', accountId: 'acct', serverId, challenge})).replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_'), signature: 's', keyId: 'k'};
const tab = new Map<string, string>();
Object.assign(globalThis, {sessionStorage: {getItem: (k: string) => tab.get(k) ?? null, setItem: (k: string, v: string) => {tab.set(k, v);}, removeItem: (k: string) => {tab.delete(k);}}});
test.beforeEach(() => {tab.clear(); setInstallationIdentity({installationId: 'i'.repeat(43), name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'});});
test.after(() => setInstallationIdentity(undefined));

test('the tab\'s record is read once, whether or not it is used, and is gone after ten minutes', () => {
  const state = keepAccountReturn({challenge, serverId, origin, hosted}, 1_000_000);
  assert.match(state, /^[A-Za-z0-9_-]{43}$/);
  assert.deepEqual(takeAccountReturn(1_000_000 + 1000), {state, challenge, serverId, origin, hosted, at: 1_000_000});
  assert.equal(takeAccountReturn(1_000_000 + 2000), undefined, 'read once');
  keepAccountReturn({challenge, serverId, origin, hosted}, 1_000_000);
  assert.equal(takeAccountReturn(1_000_000 + 11 * 60_000), undefined, 'too old');
  assert.equal(tab.size, 0, 'and deleted all the same');
  assert.notEqual(keepAccountReturn({challenge, serverId, origin, hosted}), keepAccountReturn({challenge, serverId, origin, hosted}), 'a fresh random state each time');
});

test('the way out and back: the challenge goes in the query, the note only ever in the fragment of the server\'s own return page', () => {
  const out = new URL(accountReturnStart(hosted, {serverId, challenge, to: origin, state: 's'.repeat(43)}));
  assert.equal(out.origin + out.pathname, hosted + '/servers/return');
  assert.deepEqual(Object.fromEntries(out.searchParams), {server: serverId, challenge, to: origin, state: 's'.repeat(43)});
  const back = new URL(accountReturnFinish(origin, {identity: note, state: 's'.repeat(43)}));
  assert.equal(back.origin + back.pathname + back.search, origin + '/auth/portico-return', 'nothing in the path or query');
  assert.deepEqual(readReturnFragment(back.hash), {identity: note, state: 's'.repeat(43)});
  for (const bad of ['', '#note=x&state=' + 's'.repeat(43), back.hash + '&extra=1', back.hash + '&state=' + 't'.repeat(43), back.hash.replace(/state=[^&]+/, 'state=short')])
    assert.equal(readReturnFragment(bad), undefined, bad.slice(0, 40));
});

test('a note and a setup or owner continuation can\'t be taken for each other', () => {
  const noteFragment = new URL(accountReturnFinish(origin, {identity: note, state: 's'.repeat(43)})).hash;
  const captured = {href: origin + '/auth/portico-return', fragment: noteFragment};
  for (const pending of [{kind: 'setup', transactionId: 't'.repeat(20)}, {kind: 'owner', transactionId: 't'.repeat(20)}, {kind: 'registered'}] as const)
    assert.throws(() => dispatchPorticoReturn(captured, pending), {code: 'invalid_return'}, `a note is not a ${pending.kind} return`);
  for (const fragment of ['#transaction=' + 't'.repeat(20), '#code=x&state=' + 's'.repeat(43) + '&transaction=srt_' + 't'.repeat(20)])
    assert.equal(readReturnFragment(fragment), undefined, `${fragment.slice(0, 14)} is not a note`);
});

test('the return page reads only a whole, well-formed ask, and only an HTTPS origin for `to`', () => {
  const ask = (to: string, extra = '') => readServerReturnAsk(`?server=${serverId}&challenge=${challenge}&to=${encodeURIComponent(to)}&state=${'s'.repeat(43)}${extra}`);
  assert.deepEqual(ask(origin), {serverId, challenge, to: origin, state: 's'.repeat(43)});
  for (const to of ['http://192.168.1.20:32500', origin + '/sign-in', 'https://user:pw@den.example', 'https://den.example:443', 'javascript:alert(1)']) assert.equal(ask(to), undefined, to);
  assert.equal(ask(origin, '&server=' + serverId), undefined, 'one of each');
  assert.equal(httpsOrigin('https://den.example'), 'https://den.example');
});

test('the name and address shown come from the signed list; a foreign, look-alike or other-port address is refused', () => {
  const ask = readServerReturnAsk(`?server=${serverId}&challenge=${challenge}&to=${encodeURIComponent(origin)}&state=${'s'.repeat(43)}`)!;
  const servers = [{serverId, name: 'Portico Lanes', addresses: ['https://home.example', origin]}];
  assert.deepEqual(listedReturn(servers, ask), {name: 'Portico Lanes', address: new URL(origin).host});
  for (const to of ['https://evil.example', origin.replace('.portico.direct', '.portico.direct.evil.example'), origin.replace(':32510', ':32511'), origin.replace('https://66', 'https://67')])
    assert.deepEqual(listedReturn(servers, {...ask, to}), {refused: 'address_unknown', name: 'Portico Lanes'}, to);
  assert.deepEqual(listedReturn(servers, {...ask, serverId: 'srv_' + 'B'.repeat(43)}), {refused: 'not_listed'});
  assert.deepEqual(listedReturn([{serverId, name: 'Den', addresses: ['https://den.example:443']}], {...ask, to: 'https://den.example'}), {name: 'Den', address: 'den.example'}, 'the default port as browsers write it');
});

test('the server names its secure address on /v1/system only as an HTTPS origin, and only for Portico Accounts', () => {
  const base = {id: serverId, name: 'Den', setupRequired: false, apiMajor: 1, capabilities: [], signInMethods: ['portico']};
  assert.equal(parseServerSignIn({...base, porticoOrigin: origin}).secureAddress, origin);
  for (const value of [undefined, 'http://den.example', origin + '/x', 'not a url']) assert.equal(parseServerSignIn({...base, porticoOrigin: value}).secureAddress, undefined, String(value));
  assert.equal(parseServerSignIn({...base, signInMethods: ['password'], porticoOrigin: origin}).secureAddress, undefined);
});

/** The server page's entry against a stand-in server at `origin`. */
function entryFixture() {
  let raw: AppSessionRecord | undefined;
  const calls: string[] = [], sent: unknown[] = [];
  const store = new AppSessionStore({read: async () => raw, change: async (update, beforeCommit) => {const next = update(raw); beforeCommit?.(); raw = next;}});
  const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
  const session = (): LocalSession => ({accessToken: 'private-access', refreshToken: 'private-refresh-family', sessionFamilyId: 'family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
    expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'member', profileId: 'main', role: 'account'}});
  const snapshot = () => ({authority: 'local', serverId, canManage: false, twoStepSetupRequired: false, capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'member', username: 'lanes-member', hostedAccountId: 'acct', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Lanes Member', isPrimary: true, color: 'mint', pinRequired: true}, {id: 'kids', name: 'Kids', isPrimary: false, color: 'gold', pinRequired: false}]});
  const system = {id: serverId, name: 'Portico Lanes', version: '1', signIn: 'portico', move: null, setupRequired: false, hostedConfigured: true, hostedAttached: true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const connection = {pin, logicalOrigin: origin, getSnapshot: () => ({phase: 'ready'}), getMemory: () => ({paired: [origin]}), setPersistence: () => {}, dispose: () => {}};
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => connection as never;
  api.request = (async (path: string, _method?: string, body?: unknown) => {
    calls.push(path);
    if (path === '/v1/system') return systemAnswer(system);
    if (path === '/v1/auth/portico/challenge') return {challenge, serverId, expiresAt: '2099-01-01T00:00:00Z'};
    if (path === '/v1/auth/portico/exchange') {sent.push(body); return {...snapshot(), session: session()};}
    throw new Error('Unexpected request ' + path);
  }) as HttpLocalApi['request'];
  const probe = {inspect: async () => system, getSnapshot: () => ({origin, error: undefined}), cancel() {}, dispose() {}} as unknown as ServerSignInProbe;
  const entry = new BrowserAccountEntry({store, actions: {} as never, environment: () => ({storage: store.connectionStorage(serverId)}), restore: async () => undefined,
    inspect: (async () => pin) as never, connect: (async () => ({api, connection})) as never, remember: rememberNativeSession, retire: async () => {}} as never, probe);
  return {entry, calls, sent, store};
}
const returned = (state: string) => ({href: origin + '/auth/portico-return', fragment: new URL(accountReturnFinish(origin, {identity: note, state})).hash});

test('Continue at the server\'s own HTTPS page asks its server for a challenge, keeps it in this tab, and names this address', async () => {
  const f = entryFixture();
  await f.entry.inspect(origin);
  const url = new URL((await f.entry.startPorticoReturn(hosted, origin))!);
  assert.deepEqual(f.calls.filter(path => path.includes('portico')), ['/v1/auth/portico/challenge']);
  const record = takeAccountReturn()!;
  assert.deepEqual({server: url.searchParams.get('server'), to: url.searchParams.get('to'), state: url.searchParams.get('state'), challenge: url.searchParams.get('challenge')},
    {server: serverId, to: origin, state: record.state, challenge: record.challenge});
  assert.equal(await f.entry.startPorticoReturn(hosted, 'http://192.168.1.20:32500'), undefined, 'never from plain HTTP');
  assert.equal(tab.size, 0, 'and nothing kept for it');
  f.entry.dispose();
});

test('the note comes back to the tab that asked: spent once with the challenge kept here, and signed in', async () => {
  const f = entryFixture();
  const state = keepAccountReturn({challenge, serverId, origin, hosted});
  await f.entry.finishPorticoReturn(returned(state));
  assert.deepEqual(f.sent, [{challenge, identity: note, device: (f.sent[0] as {device: unknown}).device}]);
  assert.equal(f.entry.getSnapshot().phase, 'profiles', 'on to Who\'s watching');
  assert.deepEqual((await f.store.selected())?.connection.hosted, {origin: hosted, accountId: 'acct'}, 'remembered under the Portico Account the note names');
  assert.equal((await f.store.selected())?.connection.session.sessionFamilyId, 'family');
  f.entry.dispose();
});

test('a note for another tab, a replayed note, a mismatched state or another address is refused before the server sees it', async () => {
  const cases: [string, () => ReturnType<typeof returned>][] = [
    ['no record in this tab (another tab, or pushed by a link)', () => returned('s'.repeat(43))],
    ['a state that does not match', () => {keepAccountReturn({challenge, serverId, origin, hosted}); return returned('s'.repeat(43));}],
    ['a record started at another address', () => returned(keepAccountReturn({challenge, serverId, origin: 'https://den.example', hosted}))],
    ['a record for another server', () => returned(keepAccountReturn({challenge, serverId: 'srv_' + 'B'.repeat(43), origin, hosted}))],
  ];
  for (const [name, arrive] of cases) {
    const f = entryFixture();
    await f.entry.finishPorticoReturn(arrive());
    assert.deepEqual(f.sent, [], name);
    assert.equal((f.entry.getSnapshot().error as {code?: string})?.code, 'return_not_this_tab', name);
    assert.equal(presentError(f.entry.getSnapshot().error, 'sign-in', {operation: 'action', i18n: defaultI18n}).messageId, 'accounts.signIn.returnNotThisTab', `${name}: said in its own sentence`);
    assert.equal(f.entry.getSnapshot().phase, 'portico', `${name}: Continue is offered again`);
    assert.equal(tab.size, 0, `${name}: nothing left behind`);
    f.entry.dispose();
  }
  const f = entryFixture(), state = keepAccountReturn({challenge, serverId, origin, hosted});
  await f.entry.finishPorticoReturn(returned(state));
  await f.entry.finishPorticoReturn(returned(state));
  assert.equal(f.sent.length, 1, 'a replayed note is never sent a second time');
  f.entry.dispose();
});

test('a setup or owner continuation arriving while a sign-in is kept goes to its own path and leaves the record alone', async () => {
  const f = entryFixture(), state = keepAccountReturn({challenge, serverId, origin, hosted});
  await f.entry.finishPorticoReturn({href: origin + '/auth/portico-return', fragment: '#transaction=' + 't'.repeat(20)});
  assert.deepEqual(f.sent, []);
  assert.equal(takeAccountReturn()?.state, state, 'the kept sign-in was not read');
  f.entry.dispose();
});

const tree = (node: any): any[] => Array.isArray(node) ? node.flatMap(tree) : node?.props ? [node, ...tree(node.props.children)] : [];
const words = (nodes: any[]) => nodes.flatMap(node => [node.props.children].flat()).filter(value => typeof value === 'string');
const say = (key: string, values?: unknown) => values ? `${key}:${JSON.stringify(values)}` : key;

test('the server\'s page: HTTPS comes straight back; plain HTTP explains, with its secure address when the server names one', async () => {
  const {componentModule, hooks} = await import('./helpers/component-harness.mjs');
  const host = hooks();
  const {PorticoServerSignIn} = await componentModule(new URL('../src/kit/accounts/ServerSignIn.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n: {t: say}}, '../controls/Button': {Button: 'Button'}, '../controls/Fields': {TextField: 'TextField'},
    './AuthStage': {AuthError: 'AuthError', AuthLinks: 'AuthLinks', AuthPasswordField: 'AuthPasswordField', AuthStage: 'AuthStage'}, '../Icon': {Icon: 'Icon'},
    './sign-in-retry': {signInRetryClock: () => 0, signInRetrySeconds: () => 0}, './accounts.module.css': {default: {}},
  });
  const pressed: string[] = [];
  const draw = (props: object) => tree(host.render(() => PorticoServerSignIn({serverName: 'Portico Lanes', onContinue: () => pressed.push('continue'), ...props})));
  const buttons = (page: any[]) => page.filter(node => node.type === 'Button').map(node => [[node.props.children].flat().join(''), node.props.variant ?? 'default']);
  let page = draw({});
  assert.ok(words(page).includes('accounts.signIn.handoffReturnBody:{"server":"Portico Lanes"}'));
  assert.deepEqual(buttons(page), [['accounts.signIn.handoff', 'primary']]);
  page = draw({plainHttp: true});
  assert.ok(words(page).includes('accounts.signIn.handoffPlainHttp:{"server":"Portico Lanes"}'));
  assert.deepEqual(buttons(page), [['accounts.signIn.handoffHosted', 'primary']], 'no server address: only web.getportico.tv');
  page = draw({plainHttp: true, onSecure: () => pressed.push('secure')});
  assert.deepEqual(buttons(page), [['accounts.signIn.openSecure:{"server":"Portico Lanes"}', 'primary'], ['accounts.signIn.handoffHosted', 'default']]);
  page.find(node => node.type === 'Button').props.onClick();
  assert.deepEqual(pressed, ['secure']);
});

test('web.getportico.tv\'s return page names the server and address from the signed list, and sends nothing until Continue', async () => {
  const {componentModule, hooks} = await import('./helpers/component-harness.mjs');
  const host = hooks(), effects: (() => void)[] = [], asked: unknown[][] = [], went: string[] = [], replaced: string[] = [];
  const services = {account: async () => ({id: 'acct', username: 'lanes-member'}), serverList: async () => ({list: {}}),
    serverReturnIdentity: async (...args: unknown[]) => {asked.push(args); return {identity: note};}, signOut: async () => {}};
  const module = await componentModule(new URL('../src/services/AccountServerReturn.tsx', import.meta.url), {
    react: {...host.react, useEffect: (run: () => void) => {effects.push(run);}},
    '@core/portico-server-list.ts': {openServerList: async () => ({servers: [{serverId, name: 'Portico Lanes', addresses: [origin]}]})}, '@core/route-identity.ts': {webRouteCrypto: {}},
    '../kit/accounts/AuthStage': {AuthLinks: 'AuthLinks'}, '../kit/controls': {Button: 'Button'},
    '../kit/accounts/ServicesAuth': Object.fromEntries(['ServicesActions', 'ServicesAuthStage', 'ServicesFacts', 'ServicesHeading', 'ServicesLead', 'ServicesNotice', 'ServicesStateIcon'].map(name => [name, name])),
    '../platform/hosted-navigation': {goTo: (to: string) => {went.push(to);}}, '../platform/portico-account-return': await import('../src/platform/portico-account-return.ts'),
    './i18n': {servicesI18n: () => ({t: say})}, './account-session': {account: services, accountSession: {subscribe: () => () => {}, signedIn: () => true}},
    './services-api': {ServicesError: class extends Error {}}, './account-problem': {problemMessage: () => 'problem'}, './AccountSignIn': {AccountSignIn: 'AccountSignIn'},
    './AccountSignUp': {useAction: () => ({busy: false, error: undefined, run: (work: () => Promise<void>) => () => work()})},
  });
  Object.assign(globalThis, {location: {pathname: '/servers/return', search: `?server=${serverId}&challenge=${challenge}&to=${encodeURIComponent(origin)}&state=${'s'.repeat(43)}`,
    replace: (url: string) => {replaced.push(url);}}});
  // Draws each component the page returns (the page, its confirm, then the card) in one hooks pass.
  const unwrap = (node: any): any => typeof node?.type === 'function' ? unwrap(node.type(node.props)) : node;
  const draw = () => tree(host.render(() => unwrap(module.AccountServerReturn())));
  draw(); for (const run of effects.splice(0)) run(); await new Promise(resolve => setTimeout(resolve, 0));
  const page = draw();
  const heading = page.find(node => node.type === 'ServicesHeading');
  assert.deepEqual([heading.props.children[0], [heading.props.children[1].props.children].flat().join('')], ['next.account.serverReturn.title:{"server":"Portico Lanes","address":"', new URL(origin).host + '"}']);
  assert.deepEqual(page.find(node => node.type === 'ServicesFacts').props.items.map((item: {value: unknown}) => item.value), ['Portico Lanes', 'lanes-member']);
  assert.deepEqual([asked, replaced], [[], []], 'arriving by the link sends nothing');
  const button = (label: string) => page.find(node => node.type === 'Button' && [node.props.children].flat().includes(label));
  button('next.account.serverReturn.cancel').props.onClick();
  assert.deepEqual([asked, went], [[], ['/servers']], 'Cancel spends nothing');
  await button('next.account.serverReturn.continue').props.onClick();
  assert.deepEqual(asked, [[serverId, challenge, origin]]);
  assert.deepEqual(replaced, [accountReturnFinish(origin, {identity: note, state: 's'.repeat(43)})]);
});
