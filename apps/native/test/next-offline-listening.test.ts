import test from 'node:test';
import assert from 'node:assert/strict';
import {createNativeOfflineListeningReadPort, nativeOfflineListeningData} from '../src-next/platform/playback/listening-controls';

test('offline audiobook controls read cached chapter pages and rate changes, then retire with their owner', async () => {
  const life = new AbortController(), listeners = new Set<() => void>();
  let current = true, rate = 1;
  const chapters = Array.from({length: 101}, (_, i) => ({index: i, title: `Part ${i + 1}`,
    startSeconds: i * 10, endSeconds: (i + 1) * 10}));
  const fence = {kind: 'local', playback: {kind: 'local', scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'}, run: 1},
    sessionId: 'local:copy:1', generation: 1, itemId: 'book', entryId: 'copy', playId: 'play', artifact: {sha256: 'a'.repeat(64), bytes: 100}} as const;
  const reader = createNativeOfflineListeningReadPort({
    lifetime: {signal: life.signal, isCurrent: () => true}, current: () => current,
    facts: () => ({rate: {selected: rate, supported: [1, 1.5, 2]}, preferences: {phase: 'ready', values: null}}),
    data: nativeOfflineListeningData(fence, 'library', 1010, chapters),
    subscribe: [listener => {listeners.add(listener); return () => {listeners.delete(listener);};}],
  });
  await reader.readChapters();
  let snapshot = reader.getSnapshot(); assert.equal(snapshot.phase, 'current');
  if (snapshot.phase !== 'current') throw new Error('reader retired');
  assert.equal(snapshot.chapters.data?.chapters.length, 100);
  assert.equal(snapshot.chapters.data?.chapters[0]?.index, 1, 'cached chapters draw with the same numbering as the online player');
  assert.equal(snapshot.chapters.data?.nextCursor, '100');
  await reader.readNextChapterPage();
  snapshot = reader.getSnapshot(); assert.equal(snapshot.phase, 'current');
  if (snapshot.phase !== 'current') throw new Error('reader retired');
  assert.equal(snapshot.chapters.data?.chapters[0]?.startSeconds, 1000);
  assert.equal(snapshot.chapters.pagination.canPrevious, true);
  assert.equal(snapshot.chapters.data?.nextCursor, '');
  rate = 1.5; for (const listener of listeners) listener();
  snapshot = reader.getSnapshot(); assert.equal(snapshot.phase === 'current' && snapshot.rate.selected, 1.5);
  await reader.readPreviousChapterPage();
  current = false; for (const listener of [...listeners]) listener();
  assert.equal(reader.current(), false); assert.equal(reader.getSnapshot().phase, 'retired');
  assert.equal(listeners.size, 0); await assert.rejects(reader.readChapters(), /retired/);
  life.abort(); reader.dispose();
});
