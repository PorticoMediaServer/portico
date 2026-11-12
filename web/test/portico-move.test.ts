// A member waiting on a server that switched to Portico Accounts moves with the owner's move code
// (lead ruling (b)): the server's own page checks the code and hands it on to web.getportico.tv,
// which moves the account to the Portico Account held there. Stand-ins for the server and for
// Portico Services; nothing calls the real Services.
import test from 'node:test';
import assert from 'node:assert/strict';
import {systemAnswer} from '../../packages/client-core/test/next-wire.ts';
import {HttpLocalApi} from '@core/index.ts';
import {routeBase64} from '@core/route-identity.ts';
import {parseServerSignIn, type ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {componentModule} from './helpers/component-harness.mjs';

test.beforeEach(() => setInstallationIdentity({installationId: 'i'.repeat(43), name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

async function fixture() {
  const keys = await crypto.subtle.generateKey('Ed25519', true, ['sign', 'verify']) as CryptoKeyPair;
  const publicBytes = new Uint8Array(await crypto.subtle.exportKey('raw', keys.publicKey));
  const context = new TextEncoder().encode('portico.server.identity.v1\0'), input = new Uint8Array(context.length + publicBytes.length);
  input.set(context); input.set(publicBytes, context.length);
  const serverId = 'srv_' + routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', input)));
  const pin = {serverId, publicKey: routeBase64(publicBytes), fingerprint: routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256', publicBytes)))};
  const origin = 'https://den.example', accountId = 'acct_walk', challenge = 'challenge-from-the-den-server-00001';
  const calls: Array<{path: string; body?: any}> = [], admitted: Array<{family: string; hosted: unknown}> = [];
  let selected: unknown;
  const session = {accessToken: 'issued-access', refreshToken: 'issued-refresh-credential', sessionFamilyId: 'moved-family', tokenGeneration: '1', installationId: 'i'.repeat(43), deviceId: 'device',
    expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {authority: 'local', serverId, accountId: 'local-member', profileId: 'main', role: 'member'}};
  const connection = {pin, getSnapshot: () => ({phase: 'ready'}), dispose: () => {}};
  const api = new HttpLocalApi(origin, ''); api.getRouteConnection = () => connection as never; api.setAccessToken = () => {};
  api.request = (async (path, _method, body) => {
    calls.push({path, body});
    if (path === '/v1/system') return {...systemAnswer({id: serverId, name: 'Den', signIn: 'portico'}), move: 'to_portico'};
    if (path === '/v1/auth/move/check') {
      const held = body as {code?: string; username?: string; password?: string};
      if (held.code === 'K7QP-2MXD-9HRT') return {username: 'ada', next: 'portico_account'};
      // The rebuilt server answers a right old password with a one-use ticket (server-next MoveCheck.ticket).
      if (held.username === 'ada' && held.password === 'old password') return {username: 'ada', next: 'portico_account', ticket: 'ticketfromtheserverabcdefghijklmnop'};
      return Promise.reject(Object.assign(new Error('That code isn’t right.'), {status: 400, code: 'move_code_invalid'}));
    }
    // The server names whose ticket it is, without spending it (server-next POST /v1/auth/move/ticket).
    if (path === '/v1/auth/move/ticket') return (body as {ticket?: string}).ticket === 'ticketfromtheserverabcdefghijklmnop' ? {username: 'mallory', expiresAt: '2099-01-01T00:05:00Z'}
      : Promise.reject(Object.assign(new Error('That code isn’t right.'), {status: 401, code: 'move_code_invalid'}));
    if (path === '/v1/auth/portico/challenge') return {challenge, serverId, expiresAt: '2099-01-01T00:00:00Z'};
    if (path === '/v1/auth/move/complete') return {session, account: {id: 'local-member', username: 'ada', role: 'member'}, profiles: [{id: 'main', name: 'Ada', color: 'blue', isPrimary: true, pinRequired: false}]};
    throw new Error('Unexpected request: ' + path);
  }) as HttpLocalApi['request'];
  const note = {identity: {payload: btoa(JSON.stringify({kind: 'portico.identity.v1', accountId, serverId, challenge})).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, ''), signature: 'sig', keyId: 'k'}};
  const account = {origin: 'https://web.getportico.tv', accountId, isCurrent: () => true, observe: () => () => {},
    identity: async (forServer: string, forChallenge: string) => {calls.push({path: 'Services:identity'}); assert.equal(forServer, serverId); assert.equal(forChallenge, challenge); return note;}};
  const storage = {read: async () => undefined, change: async () => {}};
  const entry = new BrowserAccountEntry({store: {selected: async () => selected, read: async () => ({version: 1, revision: 3, servers: []}), connectionStorage: () => storage} as never,
    actions: {} as never, environment: () => ({storage}), restore: async () => undefined, inspect: async () => pin, connect: (async () => ({api, connection})) as never,
    remember: async (_api, issued, _environment, hosted) => {admitted.push({family: issued.sessionFamilyId, hosted}); selected = {signIn: 'portico', connection: {pin, session: issued}};},
    retire: async () => {},
  }, {inspect: async () => parseServerSignIn({...systemAnswer({id: serverId, name: 'Den', signIn: 'portico'}), move: 'to_portico'}), getSnapshot: () => ({origin}), cancel: () => {}, dispose: () => {}} as unknown as ServerSignInProbe);
  return {entry, account, calls, admitted, origin, serverId};
}

test('on the server\'s own page the owner\'s move code is checked by the server, then handed on to web.getportico.tv in the fragment', async () => {
  const f = await fixture(); await f.entry.inspect(f.origin);
  assert.equal(f.entry.getSnapshot().server?.move, 'to_portico', 'the server says a move is on');
  await f.entry.previewMoveCode('WRONG-CODE-0');
  assert.equal(f.entry.getSnapshot().phase, 'move-code'); assert.equal((f.entry.getSnapshot().error as {code?: string}).code, 'move_code_invalid');
  await f.entry.previewMoveCode('K7QP-2MXD-9HRT');
  assert.equal(f.entry.getSnapshot().phase, 'move-portico'); assert.equal(f.entry.getSnapshot().movePreview?.username, 'ada');
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /K7QP/, 'the code is never in the page\'s state');
  assert.equal(f.entry.porticoHandoff('https://web.getportico.tv', f.origin)?.url,
    `https://web.getportico.tv/servers?open=${f.serverId}#move=K7QP-2MXD-9HRT&address=https%3A%2F%2Fden.example`);
  assert.deepEqual(f.calls.map(call => call.path).filter(path => path.startsWith('/v1/auth/')), ['/v1/auth/move/check', '/v1/auth/move/check'], 'nothing is asked of Portico Services here');
  f.entry.dispose();
});

test('at web.getportico.tv the code moves the account to the Portico Account held there: the check, the challenge, the note and the move', async () => {
  const f = await fixture(); await f.entry.inspect(f.origin);
  await f.entry.previewMoveCode('K7QP-2MXD-9HRT');
  await f.entry.movePortico({code: 'K7QP-2MXD-9HRT'}, f.account);
  assert.deepEqual(f.calls.map(call => call.path).filter(path => path !== '/v1/system'), ['/v1/auth/move/check', '/v1/auth/portico/challenge', 'Services:identity', '/v1/auth/move/complete']);
  const complete = f.calls.find(call => call.path === '/v1/auth/move/complete')!.body;
  assert.equal(complete.code, 'K7QP-2MXD-9HRT'); assert.equal(complete.challenge, 'challenge-from-the-den-server-00001'); assert.ok(complete.identity && complete.device);
  assert.equal(complete.password, undefined, 'no password is asked for here');
  assert.deepEqual(f.admitted, [{family: 'moved-family', hosted: {origin: 'https://web.getportico.tv', accountId: 'acct_walk'}}], 'kept as a Portico Account server');
  assert.equal(f.entry.getSnapshot().memberMoveCompleted, true);
  f.entry.dispose();
});

test('a handed move code leaves the address bar at once and is carried as a move, not as an invitation', async () => {
  const stored = new Map<string, string>(), replaced: string[] = [];
  Object.assign(globalThis, {location: {hash: '#move=K7QP-2MXD-9HRT&address=https%3A%2F%2Fden.example', pathname: '/servers', search: '?open=srv_den'},
    history: {state: null, replaceState: (_: unknown, __: string, url: string) => {replaced.push(url);}},
    sessionStorage: {getItem: (k: string) => stored.get(k) ?? null, setItem: (k: string, v: string) => {stored.set(k, v);}, removeItem: (k: string) => {stored.delete(k);}}});
  const carry = await import('../src/platform/invitation-carry.ts');
  const start = await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {'../../platform/invitation-carry': carry});
  start.keepHandoffInvitation();
  assert.deepEqual(replaced, ['/servers?open=srv_den']);
  assert.deepEqual(carry.takeCarriedInvitation(), {code: 'K7QP-2MXD-9HRT', address: 'https://den.example', kind: 'move'});
});

test('P2-66 on the server\'s own page: the old sign-in proves the member there, and only the server\'s ticket goes on to web.getportico.tv', async () => {
  const f = await fixture(); await f.entry.inspect(f.origin);
  assert.equal(await f.entry.proveLocalMovePassword('ada', 'old password'), true);
  assert.equal(f.entry.getSnapshot().phase, 'move-portico'); assert.equal(f.entry.getSnapshot().movePreview?.username, 'ada');
  const url = f.entry.porticoHandoff('https://web.getportico.tv', f.origin)!.url;
  assert.doesNotMatch(new URL(url).pathname + new URL(url).search, /ticketfromtheserver/, 'only the fragment carries it: never the path or a query a server would receive');
  assert.equal(url, `https://web.getportico.tv/servers?open=${f.serverId}#ticket=ticketfromtheserverabcdefghijklmnop&address=https%3A%2F%2Fden.example`);
  assert.doesNotMatch(url, /old|password|ada/, 'neither the password nor the username leaves this page');
  assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()), /ticketfromtheserver/, 'the ticket is never in the page\'s state');
  f.entry.dispose();
});

test('at web.getportico.tv a handed ticket is named by the server, never the link, and finishes the move only when asked', async () => {
  const f = await fixture(); await f.entry.inspect(f.origin);
  await f.entry.previewMoveTicket('ticketfromtheserverabcdefghijklmnop');
  // The account named is the ticket's own (mallory), whoever followed the link.
  assert.equal(f.entry.getSnapshot().phase, 'move-portico'); assert.equal(f.entry.getSnapshot().movePreview?.username, 'mallory');
  assert.deepEqual(f.calls.map(call => call.path).filter(path => path !== '/v1/system'), ['/v1/auth/move/ticket'], 'naming it spends nothing and moves nothing');
  await f.entry.movePortico({ticket: 'ticketfromtheserverabcdefghijklmnop'}, f.account);
  assert.deepEqual(f.calls.map(call => call.path).filter(path => path !== '/v1/system'), ['/v1/auth/move/ticket', '/v1/auth/portico/challenge', 'Services:identity', '/v1/auth/move/complete']);
  assert.equal(f.calls.some(call => call.path.includes('ticketfromtheserver')), false, 'the ticket never rides in a request\'s address: only in its body');
  const complete = f.calls.find(call => call.path === '/v1/auth/move/complete')!.body;
  assert.equal(complete.ticket, 'ticketfromtheserverabcdefghijklmnop'); assert.equal(complete.password, undefined); assert.equal(complete.username, undefined);
  assert.equal(f.admitted.length, 1); f.entry.dispose();
});

test('a handed ticket is carried as a ticket, and something that is not one is dropped', async () => {
  const stored = new Map<string, string>(), replaced: string[] = [];
  const page = (hash: string) => Object.assign(globalThis, {location: {hash, pathname: '/servers', search: '?open=srv_den'},
    history: {state: null, replaceState: (_: unknown, __: string, url: string) => {replaced.push(url);}},
    sessionStorage: {getItem: (k: string) => stored.get(k) ?? null, setItem: (k: string, v: string) => {stored.set(k, v);}, removeItem: (k: string) => {stored.delete(k);}}});
  const carry = await import('../src/platform/invitation-carry.ts');
  const start = await componentModule(new URL('../src/screens/accounts/ChooserStart.tsx', import.meta.url), {'../../platform/invitation-carry': carry});
  page('#ticket=ticketfromtheserverabcdefghijklmnop&address=https%3A%2F%2Fden.example'); start.keepHandoffInvitation();
  assert.doesNotMatch(replaced.at(-1)!, /ticket/, 'the address left behind in history carries no ticket');
  assert.deepEqual(carry.takeCarriedInvitation(), {code: 'ticketfromtheserverabcdefghijklmnop', address: 'https://den.example', kind: 'ticket'});
  page('#ticket=<script>'); start.keepHandoffInvitation();
  assert.equal(carry.takeCarriedInvitation(), undefined); assert.equal(replaced.length, 2, 'the fragment leaves the address either way');
});

test('the confirm is the only way to move: it names the ticket\'s own account, draws without sending anything, Cancel spends nothing, and only pressing Move moves', async () => {
  const {hooks} = await import('./helpers/component-harness.mjs');
  const host = hooks(), moved: unknown[] = [], cancelled: string[] = [];
  const snapshot = {phase: 'move-portico', busy: false, server: {id: 'srv_den', name: 'Den', signIn: 'portico', move: 'to_portico'}, movePreview: {username: 'mallory', signIn: 'portico'}};
  const entry = {subscribe: () => () => {}, getSnapshot: () => snapshot, inspect: async () => {throw new Error('nothing is looked up on drawing');},
    previewMoveTicket: async () => {throw new Error('nothing is asked on drawing');}, movePortico: async (proof: unknown) => {moved.push(proof);}, cancel: () => {cancelled.push('cancel');}, dispose: () => {}};
  const label = (key: string, values?: Record<string, string>) => values ? `${key}:${JSON.stringify(values)}` : key;
  const {ChooserMove} = await componentModule(new URL('../src/screens/accounts/ChooserMove.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n: {t: label}},
    '../../platform/account-entry': {createBrowserAccountEntry: () => entry},
    '../../platform/portico-account-holder': {porticoAccountHolder: {access: () => ({accountId: 'acct_victim'})}},
    './sign-in-form': {AccountSignInForm: class {dispose() {}}},
  });
  const backs: string[] = [];
  const draw = () => host.render(() => ChooserMove({who: {id: 'acct_victim', username: 'victim'}, held: {ticket: 'ticketfromtheserverabcdefghijklmnop'}, onBack: () => {backs.push('back');}, onNotYou: () => {}}));
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children)] : [];
  const text = (node: any) => all(node).flatMap((item: any) => [item.props.children].flat()).filter((value: unknown) => typeof value === 'string').join('\n');
  const page = draw();
  assert.match(text(page), /accounts\.move\.porticoQuestion:\{"username":"mallory","server":"Den","account":"victim"\}/, 'the server\'s account, the server\'s name and the visitor\'s own account');
  assert.deepEqual(moved, [], 'drawing the page sends nothing');
  const button = (label: string) => all(page).find((node: any) => [node.props.children].flat().includes(label));
  button('action.cancel').props.onClick();
  assert.deepEqual(moved, [], 'Cancel spends nothing'); assert.deepEqual(backs, ['back']);
  button('accounts.move.porticoAction').props.onClick();
  assert.deepEqual(moved, [{ticket: 'ticketfromtheserverabcdefghijklmnop'}], 'only pressing Move moves');
});


test('a ticket cannot complete before its preview or after Cancel', async () => {
  const f = await fixture(); await f.entry.inspect(f.origin);
  const proof = {ticket: 'ticketfromtheserverabcdefghijklmnop'};
  await f.entry.movePortico(proof, f.account);
  assert.equal(f.calls.some(call => call.path === '/v1/auth/move/complete'), false);
  await f.entry.previewMoveTicket(proof.ticket);
  f.entry.cancel();
  await f.entry.movePortico(proof, f.account);
  assert.equal(f.calls.some(call => call.path === '/v1/auth/move/complete'), false);
  assert.equal(f.calls.some(call => call.path === 'Services:identity'), false);
  f.entry.dispose();
});

test('a move link naming a server that uses local sign-in says so, with the server\'s checked name, and asks it nothing about a move', async () => {
  const {hooks} = await import('./helpers/component-harness.mjs');
  const host = hooks(), asked: string[] = [], backs: string[] = [];
  let snapshot: Record<string, unknown> = {phase: 'welcome', busy: false};
  const entry = {subscribe: () => () => {}, getSnapshot: () => snapshot, cancel: () => {asked.push('cancel');}, dispose: () => {},
    inspect: async () => {asked.push('inspect'); snapshot = {phase: 'password', busy: false, server: {id: 'srv_den', name: 'Den', signIn: 'local', move: null}};},
    previewMoveCode: async () => {asked.push('previewMoveCode');}, previewMoveTicket: async () => {asked.push('previewMoveTicket');}, movePortico: async () => {asked.push('movePortico');}};
  const label = (key: string, values?: Record<string, string>) => values ? `${key}:${JSON.stringify(values)}` : key;
  const {ChooserMove} = await componentModule(new URL('../src/screens/accounts/ChooserMove.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n: {t: label}},
    '../../platform/account-entry': {createBrowserAccountEntry: () => entry},
    '../../platform/portico-account-holder': {porticoAccountHolder: {access: () => ({accountId: 'acct_visitor'})}},
    './sign-in-form': {AccountSignInForm: class {dispose() {}}},
  });
  const all = (node: any): any[] => Array.isArray(node) ? node.flatMap(all) : node?.props ? [node, ...all(node.props.children)] : [];
  const text = (node: any) => all(node).flatMap((item: any) => [item.props.children].flat()).filter((value: unknown) => typeof value === 'string').join('\n');
  // The refusal is its own small component; draw it in place.
  const unwrap = (node: any): any => node?.type?.name === 'MoveNotPortico' ? node.type(node.props) : node;
  const draw = () => host.render(() => unwrap(ChooserMove({who: {id: 'acct_visitor', username: 'visitor'}, held: {code: 'K7QP-2MXD'}, initialAddress: 'https://den.example', onBack: () => {backs.push('back');}, onNotYou: () => {}})));
  all(draw()).find((node: any) => node.type === 'form').props.onSubmit({preventDefault() {}});
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(asked, ['inspect'], 'the server is checked, and never asked about a move');
  const page = draw();
  assert.match(text(page), /accounts\.move\.notPortico:\{"server":"Den"\}/);
  assert.equal(all(page).some((node: any) => node.type === 'form'), false, 'no code form to fill in');
  all(page).find((node: any) => [node.props.children].flat().includes('action.back')).props.onClick();
  assert.deepEqual([asked, backs], [['inspect', 'cancel'], ['back']]);
});
