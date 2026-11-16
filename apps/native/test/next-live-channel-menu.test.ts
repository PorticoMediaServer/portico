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
  let hidden = false;
  const calls: {path: string; method?: string; body?: any}[] = [];
  const owner = new NativeLiveOwnerController(lifetime, {async request<T>(path: string, method?: string, body?: any): Promise<T> {
    calls.push({path, method, body});
    if (method === 'PATCH') hidden = body.channels[0].hidden;
    return {items: []} as T;
  }}, () => 'operation');
  return {owner, lifetime, calls, hidden: () => hidden};
}

test('native owner Hide and Undo set the channel\'s own hidden on the rebuilt source, and a retired opening sends nothing', async () => {
  const f = visibilityOwner(), proof = {signal: f.lifetime.signal, isCurrent: () => true};
  const receipt = await f.owner.channelHidden('source', 'channel', true, proof);
  assert.deepEqual(f.calls[0], {path: '/v1/admin/live/sources/source/channels', method: 'PATCH', body: {channels: [{id: 'channel', hidden: true}]}});
  assert.equal(receipt.previousHidden, false); assert.equal(f.hidden(), true);
  await f.owner.channelHidden('source', 'channel', false, proof, receipt);
  assert.equal(f.hidden(), false);
  const count = f.calls.length;
  await assert.rejects(f.owner.channelHidden('source', 'channel', true, {...proof, isCurrent: () => false}));
  assert.equal(f.calls.length, count);
  f.owner.dispose();f.lifetime.dispose();
});

test('phone Edit channel names the Library Channel in Settings Live', () => {
  assert.deepEqual(nativeLibraryChannelEditorRoute('lc-one'), {kind: 'server-settings', page: 'live', channelId: 'lc-one'});
});

import {NativeLiveController} from '../src-next/screens/settings-live/live-controller';

test('source availability includes profile-hidden channels without asking for owner-hidden channels', async () => {
  const scope = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: 'session', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => scope, current: () => true, subscribeAuthority: () => () => {}});
  const requests: string[] = [];
  const controller = new NativeLiveController(lifetime, {async request<T>(path: string): Promise<T> {
    requests.push(path);
    return {items: [], sources: []} as T;
  }}, 'UTC');
  controller.select({includeServerHidden: true});
  await controller.loadSources();
  assert.equal(controller.getSnapshot().options.includeServerHidden, true);
  const query = new URL(requests[0]!, 'https://server').searchParams;
  assert.equal(new URL(requests[0]!, 'https://server').pathname, '/v1/live/channels');
  assert.equal(query.get('hidden'), 'true');
  assert.equal(query.has('includeServerHidden'), false);
  controller.dispose();lifetime.dispose();
});
