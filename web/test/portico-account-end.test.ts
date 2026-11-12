import {appLog} from '@core/app-log.ts';
// The servers this browser opened with its Portico Account end with that sign-in
// (`platform/portico-account-end.ts`), against the account pages' real session module
// (`services/account-session.ts`) and stand-ins for Portico Services and the saved servers.
import test from 'node:test';
import assert from 'node:assert/strict';
import {carryInvitation, takeCarriedInvitation} from '../src/platform/invitation-carry.ts';
import {componentModule} from './helpers/component-harness.mjs';

const ORIGIN = 'https://web.getportico.tv';
function browser() {
  const map = new Map<string, string>(), handlers: ((event: {key: string | null}) => void)[] = [];
  const storage = {getItem: (k: string) => map.get(k) ?? null, setItem: (k: string, v: string) => {map.set(k, v);}, removeItem: (k: string) => {map.delete(k);},
    get length() {return map.size;}, key: (i: number) => [...map.keys()][i] ?? null};
  const session = new Map<string, string>();
  Object.assign(globalThis, {sessionStorage: {getItem: (k: string) => session.get(k) ?? null, setItem: (k: string, v: string) => {session.set(k, v);}, removeItem: (k: string) => {session.delete(k);}}, localStorage: storage, location: {origin: ORIGIN},
    window: {addEventListener: (kind: string, handler: (event: {key: string | null}) => void) => {if (kind === 'storage') handlers.push(handler);}}});
  /** What another tab's write looks like here: the storage changes, then this tab hears of it. */
  const otherTab = (key: string, value: string | null) => {if (value === null) map.delete(key); else map.set(key, value); for (const handler of handlers) handler({key});};
  return {storage, otherTab};
}
const saved = (serverId: string, signIn: 'portico' | 'local', accountId?: string) =>
  ({signIn, connection: {pin: {serverId}, ...(accountId ? {hosted: {origin: ORIGIN, accountId}} : {})}});

async function app(accountId = 'acct_1') {
  const tab = browser(), ended: unknown[] = [], restored: string[] = [];
  let me = accountId;
  const servers = [saved('srv_den', 'portico', 'acct_1'), saved('srv_cabin', 'local'), saved('srv_old', 'portico', 'acct_0')];
  let held: any, readGate: Promise<void> | undefined, enterRead: (() => void) | undefined;
  const services = {signOut: async () => {held.forget(held.current());}, account: async () => ({id: me})};
  const session = await componentModule(new URL('../src/services/account-session.ts', import.meta.url), {
    '@core/portico-account.ts': {porticoAccount: (given: unknown) => {held = given; return services;}},
  });
  const holder = {subscribe: session.accountSession.subscribe, signedIn: session.accountSession.signedIn, signIns: session.accountSession.signIns,
    who: () => services.account()};
  const end = await componentModule(new URL('../src/platform/portico-account-end.ts', import.meta.url), {
    './portico-account-holder': {porticoAccountHolder: holder},
    './app-sessions': {browserAppSessionActions: () => ({endPorticoAccount: async (scope: {accountId: string}) => {
      ended.push(scope);
      for (let i = servers.length - 1; i >= 0; i--) if (servers[i].connection.hosted?.accountId === scope.accountId) servers.splice(i, 1);
      return [];
    }})},
    './server-connections': {browserAppSessions: () => ({read: async () => {const gate = readGate; readGate = undefined; if (gate) {enterRead?.(); await gate;} return {servers: [...servers]};}}),
      restoreBrowserConnection: async (expected: {id: string}) => {restored.push(expected.id); return undefined;}},
  });
  const settle = () => end.restoreAfterAccountEnd({id: 'probe'});
  return {...tab, holdNextRead: () => {
    let release!: () => void;
    const entered = new Promise<void>(resolve => {enterRead = resolve;});
    readGate = new Promise<void>(resolve => {release = resolve;});
    return {entered, release};
  }, held: () => held, services, servers, ended, settle, signInAs: (id: string) => {me = id;}};
}
const tokens = {accessToken: 'pat', expiresAt: new Date(Date.now() + 600_000).toISOString()};

test('Sign out on the account pages signs this browser out of the servers opened with the account; a local sign-in stays', async () => {
  const a = await app();
  a.held().began(tokens); await a.settle();
  assert.deepEqual(a.ended, [{origin: ORIGIN, accountId: 'acct_0'}], 'a sign-in ends the servers of an account signed in before it');
  appLog.write('error','app','error');
  await a.services.signOut(); await a.settle();
  assert.equal(appLog.snapshot(), '');
  assert.deepEqual(a.ended.at(-1), {origin: ORIGIN, accountId: 'acct_1'});
  assert.deepEqual(a.servers.map(entry => entry.connection.pin.serverId), ['srv_cabin']);
});

test('a sign-out in another tab, and a sign-in over another account there, end the servers here too', async () => {
  const a = await app();
  a.held().keep(tokens); await a.settle();
  const before = a.ended.length;
  a.signInAs('acct_2'); a.otherTab('portico.account.signedIn', 'another-sign-in'); await a.settle();
  assert.equal(a.held().current(), undefined, 'this tab no longer uses the other account\'s access token');
  assert.deepEqual(a.ended.slice(before).map(scope => (scope as {accountId: string}).accountId).sort(), ['acct_0', 'acct_1']);
  appLog.write('warn','app','warning');
  a.otherTab('portico.account.signedIn', null); await a.settle();
  assert.equal(appLog.snapshot(), '');
  assert.deepEqual(a.servers.map(entry => entry.connection.pin.serverId), ['srv_cabin']);
});

test('a saved server signed out on the account pages while no app tab was open is not reopened', async () => {
  const a = await app();
  // Nothing is held and no flag is set: the sign-in ended before this page loaded.
  await a.settle();
  assert.deepEqual(a.servers.map(entry => entry.connection.pin.serverId), ['srv_cabin']);
});


test('Not you can re-carry a move after sign-out even when an earlier account-end check is still queued', async () => {
  const a = await app(), gate = a.holdNextRead();
  a.held().began(tokens); await gate.entered;
  await a.services.signOut();
  carryInvitation('ticketfromtheserverabcdefghijklmnop', 'https://den.example', Date.now(), 'ticket');
  gate.release();
  // Drain the already queued work; requesting another account-end check would be a separate event.
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(takeCarriedInvitation(), {code: 'ticketfromtheserverabcdefghijklmnop', address: 'https://den.example', kind: 'ticket'});
});
