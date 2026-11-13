import test from 'node:test';
import assert from 'node:assert/strict';
import {SearchOnNowController} from './on-now.ts';

const serverId = 'server';
const channel = id => ({id, kind: 'source', name: `Channel ${id}`, number: id,
  favorite: id === 'ch1', hidden: false, logo: '/v1/art/logo'});
const page = (count = 0) => ({items: Array.from({length: count}, (_, n) => ({channel: channel('ch' + (n + 1)),
  now: {id: 'programme-' + (n + 1), title: 'World Tonight', episodeTitle: 'Homecoming',
    startsAt: new Date(Date.now() - 60_000).toISOString(), endsAt: new Date(Date.now() + 3600_000).toISOString()}})), nextCursor: count ? 'not-followed' : undefined});

function setup(handler) {
  let current = true, requests = [];
  const parent = new AbortController();
  const controller = new SearchOnNowController({api: {request: async (path, method, body, signal) => {
    assert.equal(method, 'GET');assert.equal(body, undefined);requests.push(path);
    return await handler(path, signal);
  }}, serverId, timezone: 'America/Halifax', isCurrent: () => current, signal: parent.signal, limit: 4});
  return {controller, parent, requests: () => requests, retire: () => {current = false;}};
}

test('an empty permitted On now page leaves the row absent with one read', async () => {
  const owner = setup(async () => page());
  try {
    assert.equal(owner.controller.getSnapshot().phase, 'idle');
    await owner.controller.load();
    const state = owner.controller.getSnapshot();
    assert.equal(state.phase, 'ready');assert.deepEqual(state.cards, []);assert.deepEqual(state.sourceNames, []);
    assert.equal(owner.requests().length, 1);
    assert.deepEqual(owner.requests(), ['/v1/live/now']);
  } finally {owner.controller.dispose();}
});

test('one rebuilt On now read supplies four cards in server order and never follows its continuation', async () => {
  const owner = setup(async path => {
    assert.equal(path, '/v1/live/now');
    return page(50);
  });
  try {
    await owner.controller.load();
    const state = owner.controller.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.cards.map(card => card.channel.id), ['ch1', 'ch2', 'ch3', 'ch4']);
    assert.deepEqual(state.sourceNames, [], 'the existing screen fallback shows the known source count');
    assert.equal(state.cards[0].channel.sourceId, '', 'a global channel must not become a fabricated provider-source filter');
    assert.equal(state.cards[0].channel.logoUrl, '/v1/art/logo');
    assert.equal(state.cards[0].programmes[0].title, 'World Tonight');
    assert.equal(state.cards[0].programmes[0].subtitle, 'Homecoming');
    assert.equal(owner.requests().length, 1);
  } finally {owner.controller.dispose();}
});

test('a refused Live TV read hides the section without exposing another source', async () => {
  const owner = setup(async () => {throw Object.assign(new Error('not permitted'), {status: 403});});
  try {
    await assert.rejects(owner.controller.load(), {message: 'not permitted'});
    const state = owner.controller.getSnapshot();
    assert.equal(state.phase, 'hidden');assert.deepEqual(state.cards, []);
    assert.equal(owner.requests().length, 1);
  } finally {owner.controller.dispose();}
});

test('retiring the captured authority refuses a late guide answer and clears private cards', async () => {
  let resolve;
  const pending = new Promise(done => {resolve = done;});
  const owner = setup(async () => pending);
  try {
    const loading = owner.controller.load();
    owner.retire();
    resolve(page());
    await assert.rejects(loading, {name: 'AbortError'});
    assert.deepEqual(owner.controller.getSnapshot().cards, []);
    assert.equal(owner.controller.getSnapshot().phase, 'idle');
  } finally {owner.controller.dispose();}
});
