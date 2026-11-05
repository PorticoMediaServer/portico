import test from 'node:test';
import assert from 'node:assert/strict';
import {itemEntryRoute, openContentLink} from '../../apps/native/src-next/screens/share/share-open.ts';
import {createShellFactOwner} from '../src/shell/facts.ts';
import {harness} from './shell-facts-fixture.ts';

const deferred = () => {
  let resolve!: (value: unknown) => void;
  const promise = new Promise<unknown>(done => {resolve = done;});
  return {promise, resolve};
};
const signal = new AbortController().signal;

test('share parsing retains its original precedence over a competing failure', async () => {
  const read = deferred(), competing = new Error('competing');
  const item = itemEntryRoute('item', () => read.promise, signal);
  read.resolve(null);
  const other = Promise.resolve().then(() => {throw competing;});
  await assert.rejects(Promise.all([item, other]), error => error !== competing && /couldn't read/.test((error as Error).message));
});

for (const body of [null, {id: 'item', kind: 'movie', title: 'Movie', libraryId: 'lib', available: true, durationMs: 42000, progressMs: 0}]) {
  test(`content link retains its original claim ordering: ${body ? 'valid' : 'malformed'}`, async () => {
    const read = deferred(), events: string[] = [];
    let newer = false;
    const pending = openContentLink({action: 'item', itemId: 'item'}, {read: () => read.promise, signal, online: true, downloads: true,
      take: () => !newer, selectHome: () => {events.push('home');}, open: () => {events.push('title');}, play: async () => {}});
    read.resolve(body);
    queueMicrotask(() => queueMicrotask(() => {newer = true;}));
    assert.equal(await pending, body ? 'opened' : 'no-access');
    assert.deepEqual(events, body ? ['home', 'title'] : []);
  });
}

test('directory privacy fence retains retirement before a queued flight abort', async () => {
  const f = harness(), read = deferred(), abort = new AbortController();
  const interests = new Map<string, {read: (context: {signal: AbortSignal}) => Promise<{kind: string}>}>();
  let retired = 0;
  const session = {sessionFamilyId: 'family', viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}};
  const hub = {interest(o: {name: string; read: (context: {signal: AbortSignal}) => Promise<{kind: string}>}) {
    interests.set(o.name, o);return {invalidate() {}, dispose() {}, setRetained() {}};
  }, settled: async () => {}};
  const owner = createShellFactOwner({api: {request: () => read.promise}, session: () => session, hub, initial: f.initial, witnesses: f.witnesses,
    isCurrent: () => true, retire: () => {retired++;}, preferences: {}, auth: {}} as unknown as Parameters<typeof createShellFactOwner>[0]);
  try {
    const pending = interests.get('shell-directory')!.read({signal: abort.signal});
    read.resolve({items: [], viewerFence: 'directory', privacyFence: 'changed'});
    queueMicrotask(() => abort.abort());
    assert.equal((await pending).kind, 'superseded');
    assert.equal(retired, 1);
  } finally {owner.dispose();f.dispose();}
});
