import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import type {MobileChooserAttempt, MobileChooserProps, MobileChooserRow} from '../src/screens/accounts/Servers.route';
import {AppSessionStore, type AppSessionRecord, type AppServerSession} from '@core/app-sessions.ts';
import {BrowserAppSessionActions} from '../src/platform/app-sessions.ts';

registerHooks({load(url, context, next) {
  // The route shell owns navigation; this unit render never mounts the app router.
  if (url.endsWith('/src/router.tsx')) return {format: 'module', source: 'export const router = {navigate: async () => {}};', shortCircuit: true};
  if (url.endsWith('/Servers.route.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.svg')) return {format: 'module', source: 'export default "/wordmark.svg";', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {MobileServerChooser, AttemptLock, waitingStep, mobileChooserRows, chooserFailureState, resumeAfterProfiles} = await import('../src/screens/accounts/Servers.route');

const seenAt = new Date(2026, 9, 12, 12, 0, 0).toISOString();
const rows: readonly MobileChooserRow[] = [
  {id: 'portico-demo', label: 'Portico demo', signIn: 'portico', selected: false, status: {kind: 'online'}, owner: true},
  {id: 'the-cottage', label: 'The cottage', signIn: 'portico', selected: false, status: {kind: 'online'}},
  {id: 'dads-server', label: 'Dad’s server', signIn: 'portico', selected: false, status: {kind: 'seen', at: seenAt}},
  {id: 'lake-house', label: 'Lake house', signIn: 'portico', selected: false, status: {kind: 'never'}},
];
const noop = () => {};
const frameSeen = (iso: string) => new Intl.DateTimeFormat('en-GB', {day: 'numeric', month: 'short', timeZone: 'UTC'}).format(new Date(iso));
function props(overrides: Partial<MobileChooserProps> = {}): MobileChooserProps {
  return {rows, initial: 'N', formatSeen: frameSeen, onChoose: noop, onRefresh: noop, onLocal: noop, onDifferentAccount: noop, onCheckNow: noop, onOther: noop, ...overrides};
}
function render(overrides: Partial<MobileChooserProps> = {}) {
  return renderToStaticMarkup(createElement(MobileServerChooser, props(overrides)));
}
function attempt(value: MobileChooserAttempt) {return render({attempt: value});}

test('frame 6a renders the authored list from the saved rows, with no invented current marker or credential form', () => {
  const html = render();
  assert.match(html, /<h1>Choose a server<\/h1>/);
  assert.match(html, /Choose the server to watch from\./);
  assert.equal((html.match(/data-row=/g) ?? []).length, 4);
  assert.equal((html.match(/>Online</g) ?? []).length, 2);
  assert.match(html, /Last seen 12 Oct/);
  assert.match(html, /Never seen/);
  assert.equal((html.match(/>Owner</g) ?? []).length, 1);
  assert.match(html, /Refresh servers/);
  assert.match(html, /Use a different Portico Account/);
  assert.match(html, /Use a local server/);
  assert.doesNotMatch(html, /Current server|<input|<form|<a\b/);
  assert.match(html, /data-mweb-server-chooser/);
});

test('a saved row without account presence keeps its name and invents neither presence nor a sign-in-kind substitute', () => {
  const html = render({rows: [{id: 'local', label: 'Studio', signIn: 'local', selected: false}], onDifferentAccount: undefined});
  assert.match(html, /Studio/);
  assert.doesNotMatch(html, /Signed in with a local account|Signed in with your Portico Account|Online|Last seen|Never seen|Owner|Use a different Portico Account/);
});

test('frame 6b is the real waiting state with a countdown ring and the two recovery actions', () => {
  const html = attempt({kind: 'waiting', row: rows[1]!, seconds: 20, total: 20});
  assert.match(html, /Waiting for The cottage/);
  assert.match(html, /Portico will connect automatically when it’s back\./);
  assert.match(html, /Checking again in 20 seconds/);
  assert.match(html, /data-progress="1"/);
  assert.match(html, /Check now/);
  assert.match(html, /Choose another server/);
  assert.doesNotMatch(html, /Refresh servers|Owner|Choose a server/);
});

test('frame 6c says exactly what was last seen and offers Try again, never a password form', () => {
  const html = attempt({kind: 'unreachable', row: rows[2]!});
  assert.match(html, /Portico can’t reach Dad’s server/);
  assert.match(html, /It was last online 12 Oct\./);
  assert.match(html, /Try again/);
  assert.match(html, /Choose another server/);
  assert.doesNotMatch(html, /<input|<form|Password/);
});

test('the unreachable sentence falls back to the saved server name when the runtime has no presence clock', () => {
  const html = attempt({kind: 'unreachable', row: {id: 'cottage', label: 'The cottage', signIn: 'portico', selected: false}});
  assert.match(html, /Portico can’t reach The cottage\. Check that it’s on and connected, then try again\./);
  assert.doesNotMatch(html, /12 Oct|Never seen/);
});

test('opening, loading and list-failed are real transient states, not a second fake chooser', () => {
  const opening = attempt({kind: 'opening', row: rows[1]!});
  assert.match(opening, /Opening The cottage…/);
  assert.match(opening, /Choose another server/);
  assert.doesNotMatch(opening, /Refresh servers|Owner/);
  assert.match(attempt({kind: 'loading'}), /Loading your servers/);
  const failed = render({attempt: {kind: 'loadFailed'}, notice: 'Your saved servers couldn’t be read.'});
  assert.match(failed, /Your servers couldn’t be loaded/);
  assert.match(failed, /Your saved servers couldn’t be read\./);
  assert.match(failed, /Try again/);
});

test('genuine account-directory facts supply the drawn presence and owner; status/ownership unknowns grant nothing', () => {
  const choices = [{id: 'a', label: 'Saved A', signIn: 'portico', selected: false}, {id: 'b', label: 'Saved B', signIn: 'portico', selected: false}, {id: 'c', label: 'Saved C', signIn: 'local', selected: false}] as const;
  const listed = mobileChooserRows(choices, [
    {id: 'a', name: 'Portico demo', status: 'active', role: 'owner', presence: {lastSeenAt: null}, ownedByMe: true},
    {id: 'b', name: 'The cottage', status: 'quiet', role: 'member', presence: {lastSeenAt: seenAt}, ownedByMe: false},
    {id: 'c', name: 'Lake house', status: 'dormant', role: 'admin', presence: {lastSeenAt: null}, ownedByMe: false},
  ] as const);
  assert.deepEqual(listed[0], {...choices[0], label: 'Portico demo', status: {kind: 'online'}, owner: true});
  assert.deepEqual(listed[1], {...choices[1], label: 'The cottage', status: {kind: 'seen', at: seenAt}});
  assert.deepEqual(listed[2], {...choices[2], label: 'Lake house', status: {kind: 'never'}, administrator: true});
  assert.equal(listed[1]!.owner, undefined); assert.equal(listed[2]!.owner, undefined); assert.equal(listed[1]!.administrator, undefined);
});

test('a saved server absent from the account directory keeps only its saved facts', () => {
  const choices = [{id: 'x', label: 'Studio', signIn: 'local', selected: false}] as const;
  assert.deepEqual(mobileChooserRows(choices, []), choices);
  assert.deepEqual(mobileChooserRows(choices, [{id: 'y', name: 'Elsewhere', status: 'active', role: 'owner', presence: {lastSeenAt: null}, ownedByMe: true}]), choices);
});

test('a cancelled attempt cannot let its late settle clear a successor or leave the chooser locked', () => {
  const lock = new AttemptLock();
  const first = lock.begin();
  assert.equal(lock.running, true);
  lock.cancel();
  assert.equal(lock.running, false);
  const second = lock.begin();
  assert.equal(lock.settle(first), false);
  assert.equal(lock.running, true);
  assert.equal(lock.settle(second), true);
  assert.equal(lock.running, false);
  const third = lock.begin(); lock.cancel();
  assert.equal(lock.settle(third), false); assert.equal(lock.running, false);
});

test('the waiting countdown holds hidden, re-arms a positive countdown on visibility, and retries only at zero', () => {
  const waiting = {kind: 'waiting', row: rows[1]!, seconds: 20, total: 20} as const;
  assert.deepEqual(waitingStep(waiting, 'timer', false), {kind: 'hold'});
  assert.deepEqual(waitingStep(waiting, 'timer', true), {kind: 'tick', seconds: 19});
  assert.deepEqual(waitingStep({...waiting, seconds: 0}, 'timer', false), {kind: 'hold'});
  assert.deepEqual(waitingStep({...waiting, seconds: 0}, 'timer', true), {kind: 'retry'});
  assert.deepEqual(waitingStep(waiting, 'visibility', true), {kind: 'rearm'});
  assert.deepEqual(waitingStep({...waiting, seconds: 0}, 'visibility', true), {kind: 'retry'});
  assert.deepEqual(waitingStep(undefined, 'visibility', true), {kind: 'hold'});
  assert.deepEqual(waitingStep({kind: 'unreachable', row: rows[2]!}, 'visibility', true), {kind: 'hold'});
});

test('an admin directory fact renders the drawn badge with the Administrator catalogue word', () => {
  const html = render({rows: [{id: 'admin-server', label: 'Studio', signIn: 'portico', selected: false, administrator: true}], onDifferentAccount: undefined});
  assert.match(html, /Administrator/);
  assert.doesNotMatch(html, /Owner|Signed in with a local account/);
});

function sessionEntry(id: string): AppServerSession {
  return {signIn: 'local', connection: {version: '1', selectionId: id.padEnd(32, '_'), logicalOrigin: 'https://' + id + '.test',
    pin: {serverId: id, publicKey: 'p', fingerprint: 'f'}, routes: {paired: ['https://' + id + '.test']},
    session: {sessionFamilyId: 'family-' + id, tokenGeneration: '1', accessToken: 'credential-' + id, expiresAt: '2099-01-01T00:00:00Z',
      authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {serverId: id, accountId: 'membership-' + id, profileId: 'profile-' + id, authority: 'local', role: 'member'}}}};
}

test('a cancelled durable selection cannot commit over its successor or advance the fence', async () => {
  let raw: AppSessionRecord = {version: 1, revision: 1, selectedServerId: 'local', servers: [sessionEntry('local'), sessionEntry('first'), sessionEntry('second')]};
  let unlock!: () => void, entered!: () => void, first = true, current = true;
  const waiting = new Promise<void>(resolve => { unlock = resolve; }), started = new Promise<void>(resolve => { entered = resolve; });
  let tail = Promise.resolve();
  const store = new AppSessionStore({read: async () => structuredClone(raw), change: (update, beforeCommit) => {
    const result = tail.then(async () => { const next = update(raw); if (first) { first = false; entered(); await waiting; } beforeCommit?.(); raw = next; });
    tail = result.catch(() => {});
    return result;
  }});
  const actions = new BrowserAppSessionActions(store, () => { throw new Error('No network expected.'); }, async () => { throw new Error('No revocation expected.'); });
  const cancelled = assert.rejects(actions.switchServer('first', () => current), {code: 'cancelled'});
  await started; current = false;
  const successor = actions.switchServer('second');
  assert.equal((await store.read()).selectedServerId, 'local');
  unlock(); await cancelled; await successor;
  assert.equal((await store.read()).selectedServerId, 'second');
  assert.equal((await store.read()).revision, 2, 'only the successor advanced the admission fence');
  actions.dispose();
});

test('real permission and identity failures map to the drawn refused and unconfirmed states', () => {
  assert.equal(chooserFailureState({status: 403, code: 'forbidden'}), 'refused');
  assert.equal(chooserFailureState({code: 'access_refused'}), 'refused');
  assert.equal(chooserFailureState({code: 'identity_mismatch'}), 'unreachable');
  assert.equal(chooserFailureState({code: 'wrong_server'}), 'unreachable');
  assert.equal(chooserFailureState(new TypeError('network')), 'waiting');
  assert.equal(chooserFailureState({code: 'probe_timeout'}), 'waiting');
  assert.equal(chooserFailureState({status: 500, retryable: true}), 'waiting');
  assert.equal(chooserFailureState({status: 400, code: 'invalid_system'}), 'unreachable');
  assert.equal(chooserFailureState(undefined), 'unreachable');
});

test('frame 4c refused state says what happened and offers only real recovery controls', () => {
  const html = attempt({kind: 'refused', row: rows[1]!});
  assert.match(html, /You no longer have access to The cottage/);
  assert.match(html, /The owner removed you or ended your invitation\. They can invite you again\./);
  assert.match(html, /Choose another server/);
  assert.match(html, /Refresh servers/);
  assert.doesNotMatch(html, /<input|<form|Password|Check now|Try again/);
});

test('frame 5c unconfirmed state says nothing was sent and offers Try again plus another server', () => {
  const html = attempt({kind: 'unconfirmed', row: rows[1]!});
  assert.match(html, /Portico couldn’t confirm this is The cottage/);
  assert.match(html, /Nothing was sent to it\. Try again, or ask its owner to check the server\./);
  assert.match(html, /Try again/);
  assert.match(html, /Choose another server/);
  assert.doesNotMatch(html, /<input|<form|Password|Refresh servers/);
});

test('a restored refusal is preserved before anonymous inspection and credential UI', async () => {
  let inspected = 0, selected = 0;
  const entry = {getSnapshot: () => ({error: {status: 403, code: 'access_refused'}}), inspect: async () => { inspected++; }};
  const plan = await resumeAfterProfiles(entry, async () => { selected++; return undefined; }, 'server', 'https://server.test', () => true);
  assert.equal(plan, 'refused');
  assert.equal(inspected, 0, 'anonymous inspect must not overwrite a terminal refusal');
  assert.equal(selected, 0, 'the saved selection is not reread after a preserved refusal');
});

test('only a clean no-snapshot profile read falls back to inspect; a snapshot enters directly', async () => {
  let inspected = 0;
  const selected = async () => ({connection: {pin: {serverId: 'server'}}});
  const inspectedPlan = await resumeAfterProfiles({getSnapshot: () => ({}), inspect: async () => { inspected++; }}, selected, 'server', 'https://server.test', () => true);
  assert.equal(inspectedPlan, 'inspected'); assert.equal(inspected, 1);
  const entryPlan = await resumeAfterProfiles({getSnapshot: () => ({snapshot: {}}), inspect: async () => { inspected++; }}, selected, 'server', 'https://server.test', () => true);
  assert.equal(entryPlan, 'entry'); assert.equal(inspected, 1);
});

test('post-auth identity failures stay generic and never use the pre-credential unconfirmed copy', async () => {
  let inspected = 0;
  const entry = {getSnapshot: () => ({error: {code: 'identity_mismatch'}}), inspect: async () => { inspected++; }};
  const plan = await resumeAfterProfiles(entry, async () => undefined, 'server', 'https://server.test', () => true);
  assert.equal(plan, 'error');
  assert.equal(inspected, 0);
  assert.equal(chooserFailureState({code: 'identity_mismatch'}), 'unreachable');
});
