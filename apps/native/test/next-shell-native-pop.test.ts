import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeShellNavigation, titleRoute, type NativeRouteSnapshot} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';

const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'family', viewerFence: 'viewer'};
const title = titleRoute({id: 'same-item', kind: 'movie', title: 'Actual title', libraryId: 'movies'});
const top = (nav: NativeShellNavigation) => nav.presentationStack().at(-1)!;

// Source vectors only; no native stack, gesture progress or platform Back is simulated.
test('presentationStack is stable, readonly and contains the actual root and current descriptors', () => {
  const nav = new NativeShellNavigation(authority, () => authority), root = nav.getSnapshot(), stack = nav.presentationStack();
  assert.equal(stack.length, 1);assert.equal(stack[0].route, root.route);assert.equal(stack[0].destination, root.destination);
  assert.ok(stack[0].occurrence);assert.ok(Object.isFrozen(stack));assert.ok(Object.isFrozen(stack[0]));
  assert.equal(nav.presentationStack(), stack);assert.equal(nav.getSnapshot(), root);
  assert.equal(nav.commitNativePop(root, stack[0].occurrence), false);
  nav.dispose();assert.deepEqual(nav.presentationStack(), []);
});

test('opening the identical route object twice creates distinct occurrences and Back restores the old occurrence', () => {
  const nav = new NativeShellNavigation(authority, () => authority), root = top(nav);
  nav.open(title, authority);const first = top(nav);nav.open(title, authority);const second = top(nav);
  assert.equal(first.route, title);assert.equal(second.route, title);assert.notEqual(first.occurrence, second.occurrence);
  assert.equal(new Set(nav.presentationStack().map(value => value.occurrence)).size, 3);
  nav.back(authority);assert.equal(top(nav).occurrence, first.occurrence);assert.equal(top(nav).route, first.route);
  nav.open(title, authority);assert.notEqual(top(nav).occurrence, first.occurrence);assert.notEqual(top(nav).occurrence, second.occurrence);
  nav.back(authority);nav.back(authority);assert.equal(top(nav).occurrence, root.occurrence);nav.dispose();
});

test('native completion spends exactly the captured snapshot and occurrence once', () => {
  const nav = new NativeShellNavigation(authority, () => authority);nav.open({kind: 'servers'}, authority);const prior = top(nav);
  nav.open({kind: 'account'}, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;let notices = 0;
  nav.subscribe(() => notices++);
  assert.equal(nav.commitNativePop(expected, occurrence), true);assert.equal(top(nav).occurrence, prior.occurrence);
  assert.equal(nav.getSnapshot().route, prior.route);assert.equal(nav.getSnapshot().revision, expected.revision + 1);assert.equal(nav.getIntent(), 'restore');
  const after = nav.getSnapshot(), stack = nav.presentationStack();
  assert.equal(nav.commitNativePop(expected, occurrence), false);assert.equal(nav.getSnapshot(), after);assert.equal(nav.presentationStack(), stack);assert.equal(notices, 1);nav.dispose();
});

test('cancellation/read-only projection makes no history write and stale or cloned snapshots never fall back to latest', () => {
  const nav = new NativeShellNavigation(authority, () => authority);nav.open(title, authority);
  const expected = nav.getSnapshot(), occurrence = top(nav).occurrence, stack = nav.presentationStack();
  nav.presentationStack();nav.getSnapshot();assert.equal(nav.presentationStack(), stack);assert.equal(nav.getSnapshot(), expected);
  assert.equal(nav.commitNativePop({...expected}, occurrence), false);assert.equal(nav.commitNativePop(expected, 'another-occurrence'), false);
  nav.open(title, authority);const successor = nav.getSnapshot(), successorStack = nav.presentationStack();
  assert.equal(nav.commitNativePop(expected, occurrence), false);assert.equal(nav.getSnapshot(), successor);assert.equal(nav.presentationStack(), successorStack);nav.dispose();
});

test('Back revalidates the prior descriptor against its original destination before changing history', () => {
  let allowed = true, checked: unknown;
  const nav = new NativeShellNavigation(authority, () => authority, (route, origin) => {checked = {route, origin};return allowed;});
  nav.open({kind: 'shell', destination: {kind: 'library', libraryId: 'movies'}}, authority);nav.open(title, authority);const prior = top(nav);
  nav.open({kind: 'shell', destination: {kind: 'saved'}}, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence, stack = nav.presentationStack();
  allowed = false;assert.equal(nav.commitNativePop(expected, occurrence), false);
  assert.deepEqual(checked, {route: prior.route, origin: prior.destination});assert.equal(nav.getSnapshot(), expected);assert.equal(nav.presentationStack(), stack);
  allowed = true;assert.equal(nav.commitNativePop(expected, occurrence), true);assert.equal(nav.getSnapshot().destination, prior.destination);nav.dispose();
});

test('server/account/profile/family/viewer replacement or disposal cannot expose or pop the original private stack', () => {
  for (const field of ['serverId', 'accountId', 'profileId', 'epoch', 'viewerFence'] as const) {
    let current = authority;const nav = new NativeShellNavigation(authority, () => current);nav.open(title, authority);
    const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;current = {...authority, [field]: 'successor'};
    assert.deepEqual(nav.presentationStack(), []);assert.equal(nav.commitNativePop(expected, occurrence), false);assert.equal(nav.getSnapshot(), expected);nav.dispose();
  }
  const nav = new NativeShellNavigation(authority, () => authority);nav.open(title, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;
  nav.dispose();assert.equal(nav.commitNativePop(expected, occurrence), false);assert.deepEqual(nav.presentationStack(), []);
});

test('admission reentry that opens a successor prevents the old native pop from changing it', () => {
  let reenter = false;let nav: NativeShellNavigation;
  nav = new NativeShellNavigation(authority, () => authority, route => {
    if (reenter && route === title) {reenter = false;nav.open({kind: 'notifications'}, authority);}return true;
  });
  nav.open(title, authority);nav.open({kind: 'account'}, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;
  reenter = true;assert.equal(nav.commitNativePop(expected, occurrence), false);
  assert.equal(nav.getSnapshot().route.kind, 'notifications');assert.equal(nav.presentationStack().length, 4);nav.dispose();
});

test('admission reentrant duplicate completion cannot consume a second history entry', () => {
  let armed = false, nested: boolean | undefined, expected!: NativeRouteSnapshot, occurrence = '';let nav: NativeShellNavigation;
  nav = new NativeShellNavigation(authority, () => authority, () => {if (armed) {armed = false;nested = nav.commitNativePop(expected, occurrence);}return true;});
  nav.open(title, authority);nav.open({kind: 'account'}, authority);expected = nav.getSnapshot();occurrence = top(nav).occurrence;
  armed = true;assert.equal(nav.commitNativePop(expected, occurrence), true);assert.equal(nested, false);assert.equal(nav.presentationStack().length, 2);assert.equal(top(nav).route, title);nav.dispose();
});

test('authority retirement in a supplied callback refuses pop without publishing a retired route', () => {
  let armed = false, calls = 0;let nav: NativeShellNavigation;
  nav = new NativeShellNavigation(authority, () => {if (armed && ++calls === 2) nav.dispose();return authority;});
  nav.open(title, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;armed = true;
  assert.equal(nav.commitNativePop(expected, occurrence), false);assert.equal(nav.getSnapshot(), expected);assert.deepEqual(nav.presentationStack(), []);
});

test('a throwing admission releases the pop claim without a write or a latest retry', () => {
  let fail = false;const nav = new NativeShellNavigation(authority, () => authority, () => {if (fail) throw new Error('refused');return true;});
  nav.open(title, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence, stack = nav.presentationStack();
  fail = true;assert.equal(nav.commitNativePop(expected, occurrence), false);assert.equal(nav.getSnapshot(), expected);assert.equal(nav.presentationStack(), stack);
  fail = false;assert.equal(nav.commitNativePop(expected, occurrence), true);nav.dispose();
});

test('notification failure after commit cannot report that the captured pop made no write', () => {
  const nav = new NativeShellNavigation(authority, () => authority), root = top(nav);nav.open(title, authority);
  const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;const off = nav.subscribe(() => {throw new Error('subscriber');});
  assert.equal(nav.commitNativePop(expected, occurrence), true);assert.equal(top(nav).occurrence, root.occurrence);
  assert.equal(nav.commitNativePop(expected, occurrence), false);off();nav.dispose();
});

test('top and channel replacement keep the opening while a new navigation instance has a distinct root', () => {
  const nav = new NativeShellNavigation(authority, () => authority), root = top(nav);
  nav.top({kind: 'home'}, authority);assert.equal(top(nav).occurrence, root.occurrence);
  nav.open({kind: 'channel', sourceId: 'source', channelId: 'one'}, authority);const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;
  nav.replaceChannel(expected, {kind: 'channel', sourceId: 'source', channelId: 'two'}, authority);assert.equal(top(nav).occurrence, occurrence);
  assert.equal(nav.commitNativePop(expected, occurrence), false);
  const next = new NativeShellNavigation(authority, () => authority);assert.notEqual(top(next).occurrence, root.occurrence);nav.dispose();next.dispose();
});

test('an open admission that reenters cannot append an obsolete occurrence over its successor', () => {
  let armed = false;let nav: NativeShellNavigation;
  nav = new NativeShellNavigation(authority, () => authority, route => {if (armed && route === title) {armed = false;nav.open({kind: 'notifications'}, authority);}return true;});
  armed = true;assert.equal(nav.open(title, authority), false);assert.equal(nav.getSnapshot().route.kind, 'notifications');assert.equal(nav.presentationStack().length, 2);nav.dispose();
});

test('mutating a caller authority object cannot rewrite the original navigation authority', () => {
  const supplied = {...authority}, nav = new NativeShellNavigation(supplied, () => supplied);nav.open(title, supplied);
  const expected = nav.getSnapshot(), occurrence = top(nav).occurrence;assert.ok(Object.isFrozen(expected.authority));
  supplied.profileId = 'successor';assert.equal(expected.authority.profileId, authority.profileId);
  assert.deepEqual(nav.presentationStack(), []);assert.equal(nav.commitNativePop(expected, occurrence), false);nav.dispose();
});

test('restoring an occurrence does not make its older snapshot a fresh pop receipt', () => {
  const nav = new NativeShellNavigation(authority, () => authority);nav.open({kind: 'servers'}, authority);
  const old = nav.getSnapshot(), occurrence = top(nav).occurrence;nav.open({kind: 'account'}, authority);nav.back(authority);
  const restored = nav.getSnapshot();assert.equal(top(nav).occurrence, occurrence);assert.notEqual(restored, old);
  assert.equal(nav.commitNativePop(old, occurrence), false);assert.equal(nav.getSnapshot(), restored);
  assert.equal(nav.commitNativePop(restored, occurrence), true);nav.dispose();
});
