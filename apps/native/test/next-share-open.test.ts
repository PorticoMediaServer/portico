import assert from 'node:assert/strict';
import test from 'node:test';
import {openSharedLink, shareRoute, type SharedLinkDeps} from '../src-next/screens/share/share-open.ts';
import {sharedRouteToWebUrl} from '../src-next/screens/share/share-link.ts';

// O-162 native half: a shared web.getportico.tv/s/<serverId>/… link opens the right screen over
// that server's Home, switches server, waits for sign-in, or says plainly why it can't open.

const signal = new AbortController().signal;
const item = (kind: string) => async () => ({id: 'abc', kind, title: 'Slow Horses', libraryId: 'tv', available: true, durationMs: 3_000_000, progressMs: 0});
const target = (destination: string, params: Record<string, string> = {}) => ({serverId: 'srv', destination, params}) as never;

test('each shared destination maps to the matching native screen; screens the phone has no page for open Home', async () => {
  const none = async () => {throw new Error('no read expected');};
  assert.equal(await shareRoute(target('home'), none, signal), undefined);
  assert.equal(await shareRoute(target('player'), none, signal), undefined);
  assert.deepEqual(await shareRoute(target('library', {libraryId: 'lib-1'}), none, signal), {kind: 'shell', destination: {kind: 'library', libraryId: 'lib-1'}});
  assert.deepEqual(await shareRoute(target('libraries'), none, signal), {kind: 'shell', destination: {kind: 'libraries'}});
  assert.deepEqual(await shareRoute(target('search', {q: 'lamb'}), none, signal), {kind: 'shell', destination: {kind: 'search', query: 'lamb'}});
  assert.deepEqual(await shareRoute(target('person', {personId: 'p9'}), none, signal), {kind: 'person', personId: 'p9'});
  assert.deepEqual(await shareRoute(target('live'), none, signal), {kind: 'shell', destination: {kind: 'live'}});
  assert.deepEqual(await shareRoute(target('saved'), none, signal), {kind: 'shell', destination: {kind: 'saved'}});
  assert.deepEqual(await shareRoute(target('notifications'), none, signal), {kind: 'notifications'});
  const show = await shareRoute(target('item', {itemId: 'abc'}), item('show'), signal) as {kind: string; entry: {id: string}};
  assert.equal(show.kind, 'title'); assert.equal(show.entry.id, 'abc', 'an item is read from its server before its page opens');
  assert.equal((await shareRoute(target('item', {itemId: 'abc'}), item('album'), signal) as {kind: string}).kind, 'music');
});

function device(state: Partial<SharedLinkDeps> = {}) {
  const calls: string[] = [];
  let taken = 0, newer = false;
  const deps: SharedLinkDeps = {currentServerId: 'srv', savedServerIds: ['srv'], online: true, read: item('movie'), signal,
    take: () => {if (newer) return false; taken++; return true;}, selectHome: () => calls.push('home'), open: route => calls.push(`open:${route.kind}`),
    switchServer: async serverId => {calls.push(`switch:${serverId}`);}, ...state};
  return {deps, calls, taken: () => taken, supersede: () => {newer = true;}};
}
const url = (raw: string) => sharedRouteToWebUrl(raw)!;

test('cold start and warm open: the destination opens over this server\'s Home, so Back lands on Home', async () => {
  for (const raw of ['portico://shared/srv/item/abc', 'portico://shared/srv/person/p1']) {
    const {deps, calls, taken} = device();
    assert.equal(await openSharedLink(url(raw), deps), 'opened');
    assert.deepEqual(calls.slice(0, 1), ['home'], 'Home first, then the destination on top');
    assert.equal(calls.length, 2); assert.equal(taken(), 1);
  }
  const {deps, calls} = device();
  assert.equal(await openSharedLink(url('portico://shared/srv'), deps), 'opened');
  assert.deepEqual(calls, ['home'], 'a link to the server itself is its Home');
});

test('signed out: the link waits, then opens once sign-in and a profile give a ready shell', async () => {
  const signedOut = device({currentServerId: undefined, savedServerIds: []});
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), signedOut.deps), 'waiting');
  assert.equal(signedOut.taken(), 0, 'nothing is claimed: the same link is still pending');
  const signedIn = device();
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), signedIn.deps), 'opened');
});

test('another server this device is signed in to: switch, then the restarted shell opens it', async () => {
  const onA = device({currentServerId: 'srv-a', savedServerIds: ['srv-a', 'srv']});
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), onA.deps), 'switching');
  assert.deepEqual(onA.calls, ['switch:srv']);
  assert.equal(onA.taken(), 0, 'the link stays pending across the switch');
  const onB = device({currentServerId: 'srv', savedServerIds: ['srv-a', 'srv']});
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), onB.deps), 'opened');
});

test('no access, offline and unknown links say so plainly; a stale consumer never acts', async () => {
  const stranger = device({currentServerId: 'srv-a', savedServerIds: ['srv-a']});
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), stranger.deps), 'no-access');
  const offline = device({currentServerId: 'srv-a', savedServerIds: ['srv-a', 'srv'], online: false});
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), offline.deps), 'offline');
  const unknown = device();
  assert.equal(await openSharedLink(url('portico://shared/srv/account'), unknown.deps), 'no-access', 'a non-shareable screen is never opened');
  const replaced = device(); replaced.supersede();
  assert.equal(await openSharedLink(url('portico://shared/srv/item/abc'), replaced.deps), 'superseded');
  assert.deepEqual(replaced.calls, [], 'a newer link replaced it: nothing moves');
});
