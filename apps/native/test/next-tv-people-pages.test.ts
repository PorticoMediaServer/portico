import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {PeoplePagesController, type PeoplePages} from '../src-next/screens/settings-live/server/people-pages';

// The TV People page reads the rebuilt server: the accounts (`GET /v1/admin/members`, with who is playing now) and the
// waiting invitations (`GET /v1/admin/invitations`), each in one read, GET only, through the TV server reader.
const authority = {serverId: 'server', accountId: 'account', profileId: 'primary', epoch: 'one', viewerFence: 'fence'};
const account = {canManage: true, account: {role: 'admin', primaryProfileId: 'primary', disabled: false}} as DirectSnapshot;
const member = (id: string, extra: Record<string, unknown> = {}) => ({id, username: id, role: 'member', disabled: false, createdAt: '2026-11-01T00:00:00Z', allLibraries: true, libraries: [], twoFactorEnabled: false, ...extra});
const invitation = (id: string, state = 'open') => ({id, role: 'member', state, allLibraries: true, libraries: [], createdAt: '2026-11-01T00:00:00Z', expiresAt: '2027-01-01T00:00:00Z'});
const lifetimeOf = () => new NativeScreenLifetime({authority: () => authority, current: () => true, subscribeAuthority: () => () => {}});
type Call = {path: string; method?: string};
/** A rebuilt server; `members` answers the accounts read and may hold it open. */
function server(members: (call: Call) => unknown = () => ({items: [member('amy')]})) {
  const calls: Call[] = [];
  const api = {request: async <T,>(path: string, method?: string): Promise<T> => {
    calls.push({path, method});
    if (path === '/v1/admin/members') return await members({path, method}) as T;
    if (path === '/v1/admin/sessions') return {items: []} as T;
    if (path === '/v1/admin/playback/stream-limits') return {} as T;
    if (path === '/v1/admin/invitations') return {items: [invitation('waiting'), invitation('used', 'accepted')]} as T;
    throw new Error(`unexpected ${path}`);
  }};
  return {api, calls};
}

test('the owner and an administrator see the accounts and the waiting invitations, each read once with GET', async () => {
  for (const role of ['owner', 'admin'] as const) {
    const lifetime = lifetimeOf(), s = server(() => ({items: [member('amy'), member('ben', {disabled: true})]}));
    let state!: PeoplePages;
    const pages = new PeoplePagesController({account: {...account, account: {...account.account, role}}, api: s.api}, lifetime, next => {state = next;});
    for (const section of ['members', 'invitations'] as const) {await pages.load(section); await pages.load(section);}
    assert.deepEqual(state.members.items.map(row => [row.username, row.disabled]), [['amy', false], ['ben', true]]);
    assert.deepEqual(state.invitations.items.map(row => row.id), ['waiting'], 'only invitations not yet used');
    assert.ok(s.calls.every(call => !call.method || call.method === 'GET'));
    assert.equal(s.calls.filter(call => call.path === '/v1/admin/members').length, 1, 'a loaded group is not read again');
    assert.ok(s.calls.every(call => !call.path.includes('/access/')), 'nothing asks the old server\'s routes');
    // The owner's stream limits are the owner's alone: an administrator's TV never asks for them.
    assert.equal(s.calls.some(call => call.path === '/v1/admin/playback/stream-limits'), role === 'owner');
    pages.dispose(); lifetime.dispose();
  }
});

test('a read in flight is shared, a failed one keeps its rows and can be tried again', async () => {
  const lifetime = lifetimeOf();
  let state!: PeoplePages, fail = true, release!: () => void;
  const gate = new Promise<void>(resolve => {release = resolve;});
  const s = server(async () => {await gate; if (fail) throw new Error('offline'); return {items: [member('amy')]};});
  const pages = new PeoplePagesController({account, api: s.api}, lifetime, next => {state = next;});
  const first = pages.load('members'), second = pages.load('members');
  release(); await first; await second;
  assert.equal(s.calls.filter(call => call.path === '/v1/admin/members').length, 1);
  assert.ok(state.members.error); assert.equal(state.members.loaded, false);
  fail = false; await pages.load('members');
  assert.deepEqual(state.members.items.map(row => row.username), ['amy']); assert.equal(state.members.error, undefined);
  pages.dispose(); lifetime.dispose();
});

test('a retired page or a replaced authority never publishes a late answer', async () => {
  let current = authority, notify = () => {};
  const owner = {authority: () => current, current: () => true, subscribeAuthority: (listener: () => void) => {notify = listener; return () => {};}};
  let release!: () => void;
  const gate = new Promise<void>(resolve => {release = resolve;});
  const oldLifetime = new NativeScreenLifetime(owner);
  let oldUpdates = 0;
  const old = new PeoplePagesController({account, api: server(async () => {await gate; return {items: [member('stale')]};}).api}, oldLifetime, () => {oldUpdates++;});
  const pending = old.load('members');
  current = {...authority, epoch: 'successor'}; notify();
  const nextLifetime = new NativeScreenLifetime(owner);
  let successor!: PeoplePages;
  const next = new PeoplePagesController({account, api: server(() => ({items: [member('successor')]})).api}, nextLifetime, state => {successor = state;});
  await next.load('members'); release(); await pending;
  assert.equal(oldUpdates, 1, 'only the pending mark before the authority moved');
  assert.deepEqual(successor.members.items.map(row => row.username), ['successor']);
  old.dispose(); next.dispose(); nextLifetime.dispose();
});

test('refresh retires the reads before it and starts both groups again; disposal is terminal', async () => {
  const lifetime = lifetimeOf();
  let state!: PeoplePages, reads = 0, releaseOld!: () => void;
  const gate = new Promise<void>(resolve => {releaseOld = resolve;});
  const s = server(async () => {reads++; if (reads === 1) {await gate; return {items: [member('stale')]};} return {items: [member(`fresh${reads}`)]};});
  const pages = new PeoplePagesController({account, api: s.api}, lifetime, next => {state = next;});
  const old = pages.load('members'), oldGeneration = pages.currentGeneration();
  pages.refresh();
  await new Promise<void>(resolve => setImmediate(resolve));
  releaseOld(); await old;
  assert.deepEqual(state.members.items.map(row => row.username), ['fresh2'], 'the earlier answer does not replace the refreshed one');
  assert.deepEqual(state.invitations.items.map(row => row.id), ['waiting']);
  await pages.load('members', '', oldGeneration); assert.equal(reads, 2, 'a load for an earlier opening does nothing');
  const count = s.calls.length; pages.dispose(); pages.refresh(); pages.start(); await pages.load('invitations');
  assert.equal(s.calls.length, count); lifetime.dispose();
});

test('an earlier opening\'s failure cannot replace the refreshed state', async () => {
  const lifetime = lifetimeOf();
  let state!: PeoplePages, reads = 0, rejectOld!: (error: Error) => void;
  const s = server(() => {reads++; return reads === 1 ? new Promise((_, reject) => {rejectOld = reject;}) : {items: [member('new')]};});
  const pages = new PeoplePagesController({account, api: s.api}, lifetime, next => {state = next;});
  const stale = pages.load('members'); pages.refresh();
  await new Promise<void>(resolve => setImmediate(resolve));
  rejectOld(new Error('old failure')); await stale;
  assert.deepEqual(state.members.items.map(row => row.username), ['new']); assert.equal(state.members.error, undefined);
  pages.dispose(); lifetime.dispose();
});
