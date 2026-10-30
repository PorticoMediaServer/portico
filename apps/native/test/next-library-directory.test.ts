import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLibraryDirectory} from '../src-next/screens/library/directory.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';

const result = {items: [{id: 'real-movies', name: 'Movies', kind: 'movie', summary: {
  counts: [{kind: 'movie', count: 80, atLeast: false}], coverUrls: ['/art/poster'], latestArtwork: '/art/backdrop', firstScan: false, empty: false, emptyKnown: true,
}}]};
test('the directory gets counts and art from the one existing authorized summary producer', async () => {
  const paths: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string) => {paths.push(path); return result as T;}};
  const directory = new NativeLibraryDirectory({api, current: () => true});
  try {
    const loading = directory.load(); assert.equal(directory.load(), loading); await loading; await directory.load();
    assert.deepEqual(paths, ['/v1/libraries']); assert.equal(directory.getSnapshot().phase, 'ready');
    assert.deepEqual(directory.getSnapshot().libraries[0].summary.counts, result.items[0].summary.counts);
  } finally {directory.dispose();}
});
test('a late summary cannot restore a retired viewer or removed library directory', async () => {
  let current = true, finish!: (value: unknown) => void, signal: AbortSignal | undefined;
  const api: LibraryContentApi = {request: <T>(_path: string, _method?: string, _body?: unknown, nextSignal?: AbortSignal) => {
    signal = nextSignal; return new Promise<T>(resolve => {finish = value => resolve(value as T);});
  }};
  const directory = new NativeLibraryDirectory({api, current: () => current});
  const loading = directory.load(); current = false; const retired = directory.getSnapshot();
  assert.ok(signal?.aborted); assert.deepEqual(retired.libraries, []); finish(result); await loading;
  assert.equal(directory.getSnapshot(), retired);
});
