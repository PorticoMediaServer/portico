import test from 'node:test';
import assert from 'node:assert/strict';
import {createSelectionEditor} from '../src/screens/browse/selection-editor.ts';
import {LibraryDeleteProducer} from '../src/screens/browse/selection-delete.ts';
import {HttpLocalApi} from '../../packages/client-core/src/index.ts';
import {screenLifetime} from '../src/screens/detail/context.ts';
import {rebuiltMetadata} from '../../packages/client-core/test/fixtures/rebuilt-metadata.ts';

const tick = () => new Promise(resolve => setImmediate(resolve));
const draft = {fields: {contentRating: 'G'}, lists: {tags: {add: [' X '], remove: []}, genres: {add: ['Drama'], remove: []}}, lockEdited: false};

test('web explicit preview reads each title\'s document through the core HTTP and sign-in renewal policy', async () => {
  for (const renew of [false, true]) {
    const wire: {url: string; method?: string; headers: Record<string, string>}[] = [];
    const identities = ['second /é', 'first'].map(id => ({kind: 'movie', id, libraryId: 'library', capabilities: {edit: true}}));
    const picks = identities.map(item => ({key: item.id, item}));
    const operation = {target: {mode: 'explicit', context: {}, items: picks, displayItems: picks}} as never;
    const http = new HttpLocalApi('https://wire.example', 'preview-token', async (url, init) => {
      wire.push({url: String(url), method: init?.method, headers: Object.fromEntries(new Headers(init?.headers))});
      if (renew && wire.length === 1) return new Response(JSON.stringify({error: {code: 'unauthorized', message: 'Expired'}}), {status: 401});
      const id = decodeURIComponent(String(url).split('/')[6]!);
      return new Response(JSON.stringify({itemId: id, kind: 'movie', revision: '3', fields: {title: {value: id, source: 'file', locked: false}, contentRating: {value: 'G', source: 'file', locked: false}},
        lists: {genres: {values: [], source: 'file', locked: false}}, match: {state: 'found', locked: false, ids: {}}}), {headers: {'Content-Type': 'application/json'}});
    });
    if (renew) http.setAuthRecovery({token: async token => token, recover: async () => 'renewed-token'});
    const life = screenLifetime({api: http, isCurrent: () => true} as never);
    const editor = createSelectionEditor({api: {request: life.api.request}, events: {on: () => () => {}},
      requestId: async () => {throw new Error('preview must not mint an operation ID');}, isCurrent: life.current, serverId: 'server'});
    try {
      const result = await editor.preview(operation);
      assert.deepEqual(result.targets.map(target => [target.id, target.expectedRevision]), identities.map(item => [item.id, '3']));
      // Read a few at a time, so in no fixed order; a renewed sign-in reads the refused title again.
      assert.deepEqual([...new Set(wire.map(call => `${call.method} ${call.url}`))].sort(), [
        'GET https://wire.example/v1/admin/items/first/metadata', 'GET https://wire.example/v1/admin/items/second%20%2F%C3%A9/metadata']);
      assert.equal(wire.length, renew ? 3 : 2);
      if (renew) assert.ok(wire.some(call => call.headers.authorization === 'Bearer renewed-token'));
    } finally {life.dispose();}
  }
});
function editing(count: number, options: {loseTail?: boolean; retireOnSecondId?: boolean} = {}) {
  const events: string[] = [], writes: any[] = []; let ids = 0, live = true;
  const docs = Object.fromEntries(Array.from({length: count}, (_, index) => [`m${index}`, {title: `M${index}`}]));
  const server = rebuiltMetadata(docs);
  const picks = Object.keys(docs).map(id => ({key: id, item: {kind: 'movie', id, libraryId: 'library', capabilities: {edit: true}}}));
  const operation = {target: {mode: 'explicit', context: {}, items: picks, displayItems: picks}} as never;
  const api = {async request(path: string, method?: string, body?: any) {
    if (path === '/v1/jobs') {writes.push(body); events.push(`write-${writes.length}`); if (options.loseTail && writes.length === 2) throw new Error('lost tail response');}
    return server.answer(path, method, body);
  }};
  const editor = createSelectionEditor({api: api as never, events: {on: () => () => {}}, requestId: async () => {
    ids++; events.push(`id-${ids}`); if (options.retireOnSecondId && ids === 2) live = false; return `operation-${ids}`;
  }, isCurrent: () => live, serverId: 'server'});
  return {editor, operation, writes, events, ids: () => ids};
}

test('web bulk saves 200 titles a job, and a lost answer is sent again as the same job', async () => {
  const f = editing(201, {loseTail: true}), preview = await f.editor.preview(f.operation);
  const first = await Promise.allSettled([f.editor.save(f.operation, preview, draft), f.editor.save(f.operation, preview, draft)]);
  assert.ok(first.every(result => result.status === 'rejected'));
  assert.deepEqual(f.events, ['id-1', 'id-2', 'write-1', 'write-2']);
  assert.deepEqual(f.writes.map(job => job.target.items.length), [200, 1]);
  assert.deepEqual(f.writes[0].params, {contentRating: 'G', add: {labels: ['X'], genres: ['Drama']}, lock: false});
  const receipts = await f.editor.save(f.operation, preview, draft);
  assert.deepEqual(receipts.map(receipt => receipt.updated), [200, 1]);
  assert.equal(f.ids(), 2); assert.equal(f.writes.length, 3); assert.deepEqual(f.writes[2], f.writes[1]);
});

test('web bulk retirement during ID allocation sends no body', async () => {
  const f = editing(201, {retireOnSecondId: true}), preview = await f.editor.preview(f.operation);
  await assert.rejects(f.editor.save(f.operation, preview, draft), /cancelled/);
  assert.equal(f.ids(), 2); assert.equal(f.writes.length, 0);
});

// Several titles: the rebuilt server's selection routes (definition 03 rules 63 to 69).
function deletion(options: {removed?: number} = {}) {
  const targets = ['first', 'second'].map(itemId => ({itemId, libraryId: 'library', kind: 'movie' as const, title: itemId === 'first' ? 'First' : 'Second'}));
  const writes: any[] = [], reads: string[] = [];
  const producer = new LibraryDeleteProducer();
  const ports = {serverId: 'server', viewerId: 'viewer', isCurrent: () => true, requestId: async () => 'unused',
    api: {async request(path: string, method?: string, body?: any) {
      if (path === '/v1/admin/items/deletion') return {count: 2, titles: 2, files: 2, bytes: 4096, listEntries: 3, otherPeople: 0, alsoRemoves: []};
      if (method === 'GET') {reads.push(path); if (path.endsWith('/first')) throw Object.assign(new Error('gone'), {code: 'not_found'}); return {};}
      assert.equal(path, '/v1/admin/items/delete'); writes.push(body);
      if (writes.length === 1) throw new Error('unknown deletion outcome');
      return {removed: options.removed ?? 2, filesDeleted: 2, bytes: 4096, kept: options.removed === 1 ? [{path: '/films/Second.mkv', reason: 'The file changed since Portico last read it.'}] : [], alsoRemoved: []};
    }}};
  producer.open(targets, ports as never);
  return {producer, writes, reads};
}

test('several titles are confirmed by typing the count; a lost answer is tried again for the same titles', async () => {
  const f = deletion();
  try {
    await tick(); assert.equal(f.producer.getSnapshot().phase, 'ready');
    assert.deepEqual([f.producer.getSnapshot().preview?.confirmation, f.producer.getSnapshot().preview?.listEntries], ['2', 3]);
    f.producer.setConfirmation(' 2 '); await f.producer.confirm(); assert.equal(f.writes.length, 0);
    f.producer.setConfirmation('2'); await f.producer.confirm();
    assert.equal(f.producer.getSnapshot().phase, 'uncertain'); assert.deepEqual(f.writes, [{ids: ['first', 'second']}]);
    await f.producer.retry(); assert.equal(f.producer.getSnapshot().phase, 'result');
    assert.deepEqual(f.writes, [{ids: ['first', 'second']}, {ids: ['first', 'second']}]);
  } finally {f.producer.close();}
});

test('when the server kept a title, each is looked for: the one still there is named as kept, with the file and why (rule 68)', async () => {
  const f = deletion({removed: 1});
  try {
    await tick(); f.producer.setConfirmation('2'); await f.producer.confirm(); await f.producer.retry();
    const result = f.producer.getSnapshot().result!;
    assert.deepEqual([result.removed, result.failed], [1, 1]);
    assert.deepEqual(result.receipts.map(receipt => [receipt.title, receipt.removed]), [['First', true], ['Second', false]]);
    assert.deepEqual(result.kept, [{path: '/films/Second.mkv', reason: 'The file changed since Portico last read it.'}]);
    assert.deepEqual(f.reads, ['/v1/items/first', '/v1/items/second']);
  } finally {f.producer.close();}
});
