import test from 'node:test';
import assert from 'node:assert/strict';
import {browseServerFilesystem} from '../src/library-management.ts';

// A folder's name may end in a space on Linux. The explorer opens a folder by the path the
// server listed, so that path must reach the server as it is: only a trailing slash goes.
test('a folder the server listed whose name ends in a space is opened by that name', async () => {
  const asked: string[] = [];
  const api = {async request<T>(path: string) { asked.push(new URL(path, 'http://x').searchParams.get('path') ?? ''); return {path: '/srv/Films ', folders: []} as T; }};
  await browseServerFilesystem(api as never, {path: '/srv/Films '});
  await browseServerFilesystem(api as never, {path: '/srv/Films /'});
  assert.deepEqual(asked, ['/srv/Films ', '/srv/Films ']);
});
