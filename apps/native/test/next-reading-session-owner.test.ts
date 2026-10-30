import test from 'node:test';
import assert from 'node:assert/strict';
import {createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';

test('one original session survives guest eviction and closes once on owner retirement', async () => {
  const signal = new AbortController(); let opens = 0, closes = 0, releases = 0;
  const manifest = {itemId: 'original', sourceId: 'source', revision: 'a'.repeat(64)};
  const progress = {progress: null};
  const session = {manifest, signal: signal.signal, progress: async () => progress,
    close: async () => {closes++;}, headPDF: async () => ({}), readRange: async () => new Uint8Array(),
    readAsset: async () => new Uint8Array(), page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {},
    acquire: async () => {opens++; return {session: session as any, release: () => {releases++;}};}});
  const [first, second] = await Promise.all([owner.ensure(), owner.ensure()]);
  assert.equal(opens, 1); assert.equal(first.session, second.session); assert.equal(first.manifest, manifest);
  const guest = owner.attach(); guest.release(); assert.equal(guest.current(), false); assert.equal(closes, 0);
  const restored = owner.attach(); assert.equal(restored.session.manifest, manifest); assert.equal(opens, 1);
  restored.release(); await owner.dispose(); await owner.dispose();
  assert.equal(closes, 1); assert.equal(releases, 1); assert.equal(owner.current(), false);
});

test('a retired guest cannot publish a late progress response to its successor', async () => {
  const signal = new AbortController(); let finish!: (value: {progress: null}) => void, calls = 0;
  const initial = {progress: null};
  const session = {manifest: {itemId: 'item', sourceId: 'source', revision: 'a'.repeat(64)}, signal: signal.signal,
    progress: () => ++calls === 1 ? Promise.resolve(initial) : new Promise<{progress: null}>(resolve => {finish = resolve;}),
    close: async () => {}, headPDF: async () => ({}), readRange: async () => new Uint8Array(),
    readAsset: async () => new Uint8Array(), page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {}, acquire: async () => ({session: session as any, release() {}})});
  await owner.ensure(); const old = owner.attach(), pending = old.session.progress(); old.release();
  const fresh = owner.attach(); finish({progress: null}); await assert.rejects(pending);
  assert.equal(fresh.current(), true); assert.equal(owner.snapshot().confirmedProgress, initial);
  fresh.release(); await owner.dispose();
});

test('retirement tears down the guest before closing its original session', async () => {
  const events: string[] = [], signal = new AbortController();
  const session = {manifest: {itemId: 'item', sourceId: 'source', revision: 'a'.repeat(64)}, signal: signal.signal,
    progress: async () => ({progress: null}), close: async () => {events.push('close');},
    headPDF: async () => ({}), readRange: async () => new Uint8Array(), readAsset: async () => new Uint8Array(),
    page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {},
    acquire: async () => ({session: session as any, release: () => {events.push('release');}})});
  await owner.ensure(); const guest = owner.attach();
  guest.onRetire(() => {events.push('guest'); guest.release();});
  await owner.dispose(); assert.deepEqual(events, ['guest', 'close', 'release']);
});

test('late acquisition after original retirement is closed and never accepted', async () => {
  const signal = new AbortController(); let finish!: (value: any) => void, closed = 0, released = 0;
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {},
    acquire: () => new Promise(resolve => {finish = resolve;})});
  const pending = owner.ensure(); await Promise.resolve(); await owner.dispose();
  finish({session: {signal: signal.signal, close: async () => {closed++;}}, release: () => {released++;}});
  await assert.rejects(pending); assert.equal(closed, 1); assert.equal(released, 1); assert.equal(owner.snapshot().phase, 'retired');
});

for (const reason of ['dispose', 'session-abort'] as const)
test(`pending initial progress followed by ${reason} closes and releases once`, async () => {
  const signal = new AbortController(); let finish!: (value: {progress: null}) => void, closes = 0, releases = 0;
  const session = {manifest: {itemId: 'item', sourceId: 'source', revision: 'a'.repeat(64)}, signal: signal.signal,
    progress: () => new Promise<{progress: null}>(resolve => {finish = resolve;}), close: async () => {closes++;},
    headPDF: async () => ({}), readRange: async () => new Uint8Array(), readAsset: async () => new Uint8Array(),
    page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {},
    acquire: async () => ({session: session as any, release: () => {releases++;}})});
  const pending = owner.ensure(); await Promise.resolve(); await Promise.resolve();
  if (reason === 'dispose') await owner.dispose(); else {signal.abort(); await owner.dispose();}
  finish({progress: null}); await assert.rejects(pending); await Promise.resolve();
  assert.equal(closes, 1); assert.equal(releases, 1); assert.equal(owner.snapshot().phase, 'retired');
  assert.equal(owner.snapshot().confirmedProgress, undefined);
});

test('late refused acquisition releases once even when original close ignores abort', async () => {
  const signal = new AbortController(); let finish!: (value: any) => void, closes = 0, releases = 0;
  const owner = createReadingSessionOwner({current: () => true, subscribe: () => () => {}, acquire: () => new Promise(resolve => {finish = resolve;})});
  const pending = owner.ensure(); await Promise.resolve(); await owner.dispose();
  finish({session: {signal: signal.signal, close: () => {closes++; return new Promise(() => {});}}, release: () => {releases++;}});
  await assert.rejects(pending); assert.equal(closes, 1); assert.equal(releases, 1);
  await owner.dispose(); assert.equal(releases, 1);
});

test('revocation while evicted cannot reopen the original occurrence', async () => {
  let live = true, opens = 0, closes = 0; const listeners = new Set<() => void>(), signal = new AbortController();
  const session = {manifest: {itemId: 'item', sourceId: 'source', revision: 'a'.repeat(64)}, signal: signal.signal,
    progress: async () => ({progress: null}), close: async () => {closes++;},
    headPDF: async () => ({}), readRange: async () => new Uint8Array(), readAsset: async () => new Uint8Array(),
    page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const owner = createReadingSessionOwner({current: () => live,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    acquire: async () => {opens++; return {session: session as any, release() {}};}});
  await owner.ensure(); const guest = owner.attach(); guest.release();
  live = false; for (const listener of [...listeners]) listener(); await owner.dispose();
  live = true; assert.equal(owner.current(), false); assert.throws(() => owner.attach());
  await assert.rejects(async () => owner.ensure()); assert.equal(opens, 1); assert.equal(closes, 1);
});
