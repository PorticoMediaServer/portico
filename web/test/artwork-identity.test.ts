import test from 'node:test';
import assert from 'node:assert/strict';
import {createArtworkRuntime} from '../src/kit/artwork/source.ts';
import {artworkIdentity, sameArtworkIdentity} from '../src/kit/artwork/identity.ts';

const runtime = () => createArtworkRuntime({scopeKey: 'scope', load: async () => ({src: '/blob', width: 1, height: 1}), classify: () => ({retryable: false})});

test('a Home card keeps its picture identity when its row only moves it in paint order', () => {
  const art = runtime();
  // Home derives paint order from the card's position (rowIndex * 1000 + index), so a row that
  // gains, loses or gains a place above it re-hands the same picture a new order.
  const before = art.source({url: '/v1/items/one/art/poster?v=1', cacheKey: 'one'}, {order: 2000, group: 'new_on_server'});
  const after = art.source({url: '/v1/items/one/art/poster?v=1', cacheKey: 'one'}, {order: 3000, group: 'new_on_server'});
  assert.equal(before.key, after.key, 'the same resource key');
  assert.notEqual(before.request?.order, after.request?.order, 'the request really moved');
  assert.ok(sameArtworkIdentity(artworkIdentity(before, true), artworkIdentity(after, true)), 'a moved card owns the same picture');
});

test('a different picture, scheduler, retention owner or visibility is a different identity', () => {
  const art = runtime();
  const one = art.source({url: '/one?v=1', cacheKey: 'one'}, {order: 0});
  assert.ok(!sameArtworkIdentity(undefined, artworkIdentity(one, true)), 'nothing adopts an unowned identity');
  assert.ok(!sameArtworkIdentity(artworkIdentity(one, true), artworkIdentity(art.source({url: '/two?v=1', cacheKey: 'two'}, {order: 0}), true)), 'a new URL is a new picture');
  assert.ok(!sameArtworkIdentity(artworkIdentity(one, true), artworkIdentity(one, false)), 'hiding retires the lease');
  // Custom sources still retire when their own retain callback is replaced.
  const custom = {scheduler: one.scheduler, key: one.key, retain: () => () => {}};
  assert.ok(!sameArtworkIdentity(artworkIdentity(one, true), artworkIdentity(custom, true)), 'a custom retention owner is part of the identity');
  const other = runtime();
  assert.ok(!sameArtworkIdentity(artworkIdentity(one, true), artworkIdentity(other.source({url: '/one?v=1', cacheKey: 'one'}, {order: 0}), true)), 'another runtime authority owns nothing here');
  art.dispose(); other.dispose();
});

test('the runtime retention owner is shared, so ordinary source wrappers never lose a picture', () => {
  const art = runtime();
  const first = art.source({url: '/one?v=1', cacheKey: 'one'});
  const second = art.source({url: '/one?v=1', cacheKey: 'one'}, {order: 4, priority: 'low', group: 'row'});
  assert.equal(artworkIdentity(first, true).retention, artworkIdentity(second, true).retention);
  assert.ok(sameArtworkIdentity(artworkIdentity(first, true), artworkIdentity(second, true)));
  art.dispose();
});
