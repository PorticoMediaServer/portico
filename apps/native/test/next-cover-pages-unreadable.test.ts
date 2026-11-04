import test from 'node:test';
import assert from 'node:assert/strict';
import {loadNativeCoverPages} from '../src-next/screens/read/read-cover-pages';

// RD-2 (#699): the owner's "choose cover from pages" picker. A page the server rejects as
// content (422 reading_member_unreadable) is a plain tile that can't be chosen; the rest load.
const png = Uint8Array.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
const opening = {itemId: 'item', sourceId: 'source', revision: 'rev', pageCount: 3, pageNames: ['p1.png', 'p2.png', 'p3.png']};
function client(fail: (index: number) => unknown) {
  const calls = {closed: 0, pages: [] as number[]};
  const session = {manifest: {itemId: 'item', sourceId: 'source', format: 'cbz', revision: 'rev', pageNames: opening.pageNames},
    async page(index: number) {calls.pages.push(index); const error = fail(index); if (error) throw error; return {index, name: opening.pageNames[index]!, mime: 'image/png', bytes: png};},
    async close() {calls.closed++;}};
  return {calls, client: {open: async () => session} as never};
}
const rejected = Object.assign(new Error('reading_member_unreadable'), {name: 'ReadingError', code: 'reading_member_unreadable', status: 422});

test('RD-2 (#699): an unreadable page is a plain tile that can\'t be chosen, and the picker still loads the other pages', async () => {
  const {client: reading, calls} = client(index => index === 1 ? rejected : undefined);
  const pages = await loadNativeCoverPages({client: reading, opening, signal: new AbortController().signal, isCurrent: () => true});
  assert.deepEqual(pages.map(page => [page.index, 'unreadable' in page]), [[0, false], [1, true], [2, false]]);
  assert.equal('uri' in pages[1]!, false, 'no preview to choose');
  assert.match((pages[0] as {uri: string}).uri, /^data:image\/png;base64,/);
  assert.deepEqual(calls.pages, [0, 1, 2]); assert.equal(calls.closed, 1);
});

test('RD-2 (#699): every other page failure still fails the picker, and the session is closed', async () => {
  const other = Object.assign(new Error('reading_source_changed'), {code: 'reading_source_changed', status: 409});
  const {client: reading, calls} = client(index => index === 1 ? other : undefined);
  await assert.rejects(loadNativeCoverPages({client: reading, opening, signal: new AbortController().signal, isCurrent: () => true}), other);
  assert.equal(calls.closed, 1);
});
