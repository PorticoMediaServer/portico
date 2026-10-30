import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeShellNavigation, titleRoute, type NativeShellRoute} from '../src-next/shell/routes';
import type {ShellAuthority, ShellDestination} from '../src-next/shell/model';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';

const authority: ShellAuthority = {serverId: 's', accountId: 'a', profileId: 'p', epoch: 'e', viewerFence: 'v'};
const shell = (destination: ShellDestination): NativeShellRoute => ({kind: 'shell', destination});
const title = (id: string) => titleRoute({id, libraryId: 'l', kind: 'movie', title: id} as ContentEntry);
const tabs = (navigation: NativeShellNavigation) => navigation.tabStacks().stacks.map(stack => [stack.tab, stack.presentations.map(value => value.route.kind === 'shell' ? value.route.destination.kind : value.route.kind)]);
const touch = () => new NativeShellNavigation(authority, () => authority, undefined, {tabStacks: true});

test('each tab keeps its own stack, and selecting a tab brings it back as it was left (motion 2.11)', () => {
  const navigation = touch(), movie = title('m');
  navigation.open(movie, authority);
  navigation.selectTab({kind: 'search'}, authority, true);
  assert.deepEqual(tabs(navigation), [['home', ['home', 'title']], ['search', ['search']]]);
  assert.equal(navigation.tabStacks().active, 'search');
  // Parked pages stay retained, so their modules keep state while another tab shows.
  assert.ok(navigation.retainedRoutes().includes(movie));
  assert.ok(navigation.presentationStack().some(value => value.route === movie));
  navigation.selectTab({kind: 'home'}, authority, true);
  assert.strictEqual(navigation.getSnapshot().route, movie);
  assert.equal(navigation.getIntent(), 'restore');
  assert.deepEqual(navigation.getSnapshot().destination, {kind: 'home'});
});

test('a programmatic open of another tab starts that tab at its first screen', () => {
  const navigation = touch();
  navigation.selectTab({kind: 'saved'}, authority, true);
  navigation.open(title('s'), authority);
  navigation.selectTab({kind: 'home'}, authority, true);
  navigation.open(shell({kind: 'saved'}), authority);
  assert.deepEqual(tabs(navigation), [['home', ['home']], ['saved', ['saved']]]);
});

test('re-selecting the tab pops to its first screen; at the root there is nothing to pop', () => {
  const navigation = touch();
  navigation.open(title('a'), authority);
  navigation.open(title('b'), authority);
  assert.equal(navigation.popToRoot(authority), true);
  assert.deepEqual(navigation.getSnapshot().route, shell({kind: 'home'}));
  assert.equal(navigation.popToRoot(authority), false);
});

test('Back pops within the tab, then returns from another tab\'s root to Home, then yields to the system', () => {
  const navigation = touch(), movie = title('m');
  navigation.open(movie, authority);
  navigation.selectTab({kind: 'search'}, authority, true);
  navigation.open(title('s'), authority);
  assert.equal(navigation.back(authority), true);
  assert.deepEqual(navigation.getSnapshot().route, shell({kind: 'search'}));
  assert.equal(navigation.back(authority), true);
  assert.strictEqual(navigation.getSnapshot().route, movie);
  assert.equal(navigation.back(authority), true);
  assert.equal(navigation.back(authority), false);
});

test('switching library at the tab root replaces it in place; Libraries then a library pushes', () => {
  const navigation = touch();
  navigation.selectTab({kind: 'library', libraryId: 'a'}, authority);
  navigation.open(shell({kind: 'library', libraryId: 'b'}), authority);
  assert.deepEqual(navigation.tabStacks().stacks.at(-1)!.presentations.map(value => value.destination), [{kind: 'library', libraryId: 'b'}]);
  // Going up to Libraries from a first-screen library replaces it too.
  navigation.open(shell({kind: 'libraries'}), authority);
  assert.equal(navigation.tabStacks().stacks.at(-1)!.presentations.length, 1);
  navigation.open(shell({kind: 'library', libraryId: 'c'}), authority);
  navigation.open(title('t'), authority);
  assert.equal(navigation.tabStacks().stacks.at(-1)!.presentations.length, 3);
  // Returning to a page already lower in this stack pops to it instead of stacking it twice.
  navigation.open(shell({kind: 'libraries'}), authority);
  assert.deepEqual(navigation.tabStacks().stacks.at(-1)!.presentations.map(value => value.destination), [{kind: 'libraries'}]);
  assert.equal(navigation.getIntent(), 'restore');
});

test('Settings is a modal over the tab and does not park with it', () => {
  const navigation = touch();
  navigation.open(shell({kind: 'settings'}), authority);
  navigation.open({kind: 'server-settings'}, authority);
  navigation.selectTab({kind: 'search'}, authority, true);
  navigation.selectTab({kind: 'home'}, authority, true);
  assert.deepEqual(tabs(navigation), [['home', ['home']], ['search', ['search']]]);
});

test('a native pop commits only the exact top it captured', () => {
  const navigation = touch();
  navigation.open(title('a'), authority);
  const snapshot = navigation.getSnapshot(), occurrence = navigation.presentationStack().at(-1)!.occurrence;
  navigation.selectTab({kind: 'search'}, authority, true);
  assert.equal(navigation.commitNativePop(snapshot, occurrence), false);
  navigation.selectTab({kind: 'home'}, authority, true);
  assert.equal(navigation.commitNativePop(navigation.getSnapshot(), occurrence), true);
  assert.deepEqual(navigation.getSnapshot().route, shell({kind: 'home'}));
});

test('without tab stacks (television) history stays one linear list', () => {
  const navigation = new NativeShellNavigation(authority, () => authority), movie = title('m');
  navigation.open(movie, authority);
  navigation.open(shell({kind: 'search'}), authority);
  assert.deepEqual(navigation.retainedRoutes().map(route => route.kind), ['shell', 'title']);
  assert.deepEqual(tabs(navigation), [['home', ['home', 'title', 'search']]]);
  assert.equal(navigation.back(authority), true);
  assert.strictEqual(navigation.getSnapshot().route, movie);
  // selectTab is a plain open when untabbed.
  navigation.selectTab({kind: 'saved'}, authority, true);
  assert.equal(navigation.presentationStack().length, 3);
});
