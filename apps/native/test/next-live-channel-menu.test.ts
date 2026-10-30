import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeLibraryChannelEditorRoute, nativeChannelMenuEntries} from '../src-next/screens/settings-live/guide-presentation';

const t = (id: string) => id;

test('the guide channel menu offers Favorites to everyone and Hide only to the owner', () => {
  assert.deepEqual(nativeChannelMenuEntries({favorite: false}, false, false, t as never).map(entry => [entry.id, entry.label, entry.icon]),
    [['favorite', 'channels.addFavorite', 'i-star']]);
  assert.deepEqual(nativeChannelMenuEntries({favorite: true}, false, true, t as never).map(entry => [entry.id, entry.label, entry.icon]),
    [['favorite', 'channels.removeFavorite', 'i-star-f'], ['hidden', 'channels.hide', 'i-eyeoff']]);
  // A channel the owner hid (drawn only while hidden channels are shown) is shown again.
  assert.deepEqual(nativeChannelMenuEntries({favorite: false}, true, true, t as never).map(entry => [entry.id, entry.label]),
    [['favorite', 'channels.addFavorite'], ['hidden', 'channels.unhide']]);
});


test('Library Channels offer Edit only on the phone and never offer Hide', () => {
  assert.deepEqual(nativeChannelMenuEntries({favorite: false, kind: 'library'}, false, true, t as never).map(entry => entry.id), ['favorite', 'edit']);
  assert.deepEqual(nativeChannelMenuEntries({favorite: false, kind: 'library'}, false, true, t as never, true).map(entry => entry.id), ['favorite']);
  assert.deepEqual(nativeChannelMenuEntries({favorite: false, kind: 'library'}, false, false, t as never).map(entry => entry.id), ['favorite']);
});

import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';

function visibilityOwner() {
  const scope = {serverId: 'server', accountId: 'owner', profileId: 'owner', epoch: 'session', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => scope, current: () => true, subscribeAuthority: () => () => {}});
  let revision = 2, hidden = false;
  const calls: {path: string; method?: string; body?: any}[] = [];
  const row = () => ({sourceId: 'source', generation: 'g', revision, nextCursor: '', items: [{channelId: 'channel', name: 'One', sourceNumber: '1', number: '51', overrideNumber: '51', group: '', guideChannelId: 'epg', hidden, position: 0, overridden: true}]});
  const owner = new NativeLiveOwnerController(lifetime, {async request<T>(path: string, method?: string, body?: any): Promise<T> {
    calls.push({path, method, body});
    if (method === 'PUT') {assert.equal(body.expectedRevision, revision);hidden = body.overrides[0].hidden;revision++;}
    return {protocolVersion: '1.0', serverId: 'server', result: row()} as T;
  }}, () => 'operation');
  return {owner, lifetime, calls, changeRevision: () => revision++, hidden: () => hidden};
}

test('native owner Hide and Undo preserve the map and use the committed revision', async () => {
  const f = visibilityOwner(), proof = {signal: f.lifetime.signal, isCurrent: () => true};
  const receipt = await f.owner.channelHidden('source', 'channel', true, proof);
  assert.match(f.calls[0]!.path, /channel-map\?channelId=channel&limit=1$/);
  assert.deepEqual(f.calls[1]!.body.overrides, [{channelId: 'channel', number: '51', guideChannelId: 'epg', hidden: true}]);
  assert.deepEqual(receipt, {revision: 3, hidden: true, previousHidden: false});
  await f.owner.channelHidden('source', 'channel', false, proof, receipt);
  assert.equal(f.hidden(), false);
  f.owner.dispose();f.lifetime.dispose();
});

test('native retained Undo refuses a subsequent owner edit and a retired opening', async () => {
  const f = visibilityOwner(), proof = {signal: f.lifetime.signal, isCurrent: () => true};
  const receipt = await f.owner.channelHidden('source', 'channel', true, proof);
  f.changeRevision();
  await assert.rejects(f.owner.channelHidden('source', 'channel', false, proof, receipt), {message: 'channel_map_changed'});
  const count = f.calls.length;
  await assert.rejects(f.owner.channelHidden('source', 'channel', false, {...proof, isCurrent: () => false}, receipt), {name: 'AbortError'});
  assert.equal(f.calls.length, count);
  assert.equal(f.hidden(), true);
  f.owner.dispose();f.lifetime.dispose();
});

test('phone Edit channel names the Library Channel in Settings Live', () => {
  assert.deepEqual(nativeLibraryChannelEditorRoute('lc-one'), {kind: 'server-settings', page: 'live', channelId: 'lc-one'});
});

import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';

test('native Show hidden channels asks for server visibility without enabling profile-hidden rows', async () => {
  const scope = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'session', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => scope, current: () => true, subscribeAuthority: () => () => {}});
  const requests: string[] = [];
  const controller = new NativeLiveController(lifetime, {async request<T>(path: string): Promise<T> {
    requests.push(path);
    return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'fence', revision: 'r1', sources: []} as T;
  }}, 'UTC');
  controller.select({includeServerHidden: true});
  await controller.loadSources();
  assert.equal(controller.getSnapshot().options.includeServerHidden, true);
  const query = new URL(requests[0]!, 'https://server').searchParams;
  assert.equal(query.get('includeServerHidden'), 'true');
  assert.equal(query.has('includeHidden'), false);
  controller.dispose();lifetime.dispose();
});
