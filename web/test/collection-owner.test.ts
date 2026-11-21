import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

// desktop-detail f8: an owner's card on a collection's page renames it, adds films and puts them in order, through
// PATCH /v1/admin/collections/{id}; the page then reads the collection again in its own order.
const {editCollection} = await import('../src/screens/detail/CollectionOwner.tsx');

test('each edit is one PATCH of the collection with only what changed', async () => {
  const sent: unknown[] = [];
  const api = {async request(path: string, method?: string, body?: unknown) {sent.push([method, path, body]); return undefined as never;}};
  await editCollection(api, 'itm_1', {title: 'Dune'});
  await editCollection(api, 'itm_1', {add: ['itm_2']});
  await editCollection(api, 'itm_1', {order: ['itm_3', 'itm_2']});
  assert.deepEqual(sent, [['PATCH', '/v1/admin/collections/itm_1', {title: 'Dune'}], ['PATCH', '/v1/admin/collections/itm_1', {add: ['itm_2']}],
    ['PATCH', '/v1/admin/collections/itm_1', {order: ['itm_3', 'itm_2']}]]);
});

test('the card is the local owner\'s only, and Change order swaps a film\'s Play for its up and down', () => {
  const page = readFileSync(new URL('../src/screens/detail/Collection.tsx', import.meta.url), 'utf8');
  assert.match(page, /owner = viewer\?\.role === 'owner' && viewer\.authority === 'local' && context\.identity\?\.capabilities\?\.manageServer === true/);
  assert.match(page, /\{owner && <OwnerCard title=\{t\('next\.collection\.ownerTitle'\)\}/);
  assert.match(page, /action=\{orderIds \? <span className=\{styles\.order\}>/);
});
