/**
 * FX-240 / BLK-WEB-015: the Unmatched page can recover a failed list read at its own cursor.
 *
 * The actual NeedsAttention component runs against the real metadata-attention service; only the
 * transport is stubbed. Covers initial failure/Retry, a later page retry that keeps accepted rows,
 * coalesced double clicks, a replacement api retiring late old responses, and the separation from
 * the per-title retry mutation.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tick, tree} from './helpers/interaction-harness.mjs';

const entry = (id: string, title: string) => ({entityId: id, kind: 'item', itemKind: 'movie', parentId: '', actions: ['retry'], title, revision: 1, reason: 'unmatched', confidence: 0, candidates: []});
const page = (entries: any[], nextCursor = '', libraryId = 'lib') => ({libraryId, total: entries.length, entries, nextCursor});
const cursorOf = (path: string) => new URLSearchParams(path.split('?')[1] ?? '').get('cursor') ?? '';

async function mount(libraryId = 'lib') {
  const h = createRenderer(), lists: string[] = [], acts: any[] = [];
  let respond: (path: string) => Promise<unknown> = async () => page([], '', libraryId);
  const transport = (requests: string[], actions: any[]) => ({
    request: (path: string, method?: string, body?: unknown) => {
      if (method === 'POST') { actions.push({path, body}); return Promise.resolve(undefined); }
      requests.push(path);
      return respond(path);
    },
  });
  const api = transport(lists, acts);
  const app = await componentModule(new URL('../src/screens/metadata/NeedsAttention.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key, has: () => false}},
    '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/overlays': {Dialog: 'Dialog'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/accounts/ServicesAuth': {HostedError: 'HostedError'},
    '../../kit/artwork': {Artwork: 'Artwork', GeneratedCover: 'GeneratedCover'},
    './metadata.module.css': {default: {}},
    '@design': {generatedCoverTint: () => ''},
  });
  const show = (target = api, id = libraryId) => {
    const paint = () => tree(h.render(() => app.NeedsAttention({libraryId: id, libraryName: 'Movies', api: target})));
    paint();
    return paint();
  };
  const paintOnce = (target = api, id = libraryId) => tree(h.render(() => app.NeedsAttention({libraryId: id, libraryName: 'Movies', api: target})));
  return {acts, api, app, lists, h, paintOnce, reply: (fn: (path: string) => Promise<unknown>) => {respond = fn;}, show, transport};
}

const labelOf = (node: any) => Array.isArray(node.props.children) ? node.props.children.join('') : node.props.children;
const button = (rendered: any[], label: string) => all(rendered, 'Button').find(node => labelOf(node) === label);
const articles = (rendered: any[]) => all(rendered, 'article');

test('a failed initial read shows Retry and recovers at the same cursor', async () => {
  const {acts, lists, reply, show} = await mount();
  reply(async () => { throw new Error('transport down'); });
  let v = show();
  await tick();
  v = show();
  assert.equal(articles(v).length, 0);
  const retry = button(v, 'next.browse.retry');
  assert.ok(retry, 'a read failure offers Retry');
  reply(async () => page([entry('e1', 'T2')]));
  retry.props.onClick();
  await tick();
  v = show();
  assert.equal(cursorOf(lists[1]), '', 'Retry repeats the exact failed cursor (page one)');
  assert.equal(articles(v).length, 1, 'the recovered page renders');
  assert.equal(acts.length, 0, 'a list Retry never sends a mutation');
});

test('a failed later page keeps accepted rows and retries its offered cursor', async () => {
  const {lists, reply, show} = await mount();
  reply(async path => {
    const cursor = cursorOf(path);
    if (cursor === '') return page([entry('e1', 'T2')], 'next-1');
    if (cursor === 'next-1') throw new Error('later page down');
    return page([entry('e2', 'Monsters')], '');
  });
  let v = show();
  await tick();
  v = show();
  assert.equal(articles(v).length, 1);
  button(v, 'web.hostedAccount.older').props.onClick();
  await tick();
  v = show();
  assert.equal(articles(v).length, 1, 'accepted rows stay on a later-page failure');
  assert.ok(button(v, 'next.browse.retry'), 'later-page failure offers Retry');
  button(v, 'next.browse.retry').props.onClick();
  await tick();
  v = show();
  assert.equal(cursorOf(lists.at(-1)!), 'next-1', 'Retry asks for the offered later cursor, not page one');
  assert.equal(articles(v).length, 1);
});

test('double Retry coalesces into one read', async () => {
  const {lists, reply, show} = await mount();
  reply(async () => { throw new Error('down'); });
  let v = show();
  await tick();
  v = show();
  const before = lists.length;
  reply(async () => page([entry('e1', 'T2')]));
  const retry = button(v, 'next.browse.retry');
  retry.props.onClick();
  retry.props.onClick();
  await tick();
  assert.equal(lists.length - before, 1, 'the second click joins the in-flight retry');
});

test('a replacement api retires the late old read', async () => {
  const {lists, reply, show, transport} = await mount();
  let resolveOld!: (value: unknown) => void;
  reply(() => new Promise(resolve => { resolveOld = resolve; }));
  let v = show();
  const next = transport(lists, []);
  reply(async () => page([entry('e2', 'Monsters')]));
  v = show(next);
  await tick();
  resolveOld(page([entry('e1', 'T2')]));
  await tick();
  v = show(next);
  const titles = articles(v).map(node => JSON.stringify(node));
  assert.equal(articles(v).length, 1);
  assert.ok(titles.some(value => value.includes('Monsters')), 'only the current api/library response publishes');
  assert.ok(!titles.some(value => value.includes('T2')), 'the retired response never reaches the page');
});

test('a successful empty list still draws its empty state without Retry', async () => {
  const {reply, show} = await mount();
  reply(async () => page([]));
  let v = show();
  await tick();
  v = show();
  assert.equal(articles(v).length, 0);
  assert.equal(button(v, 'next.browse.retry'), undefined);
  assert.ok(all(v, 'div').some(node => JSON.stringify(node).includes('metadata.attention.empty')));
});

test('the title retry stays a mutation; the list Retry sends no mutation', async () => {
  const {acts, reply, show} = await mount();
  reply(async () => page([entry('e1', 'T2')]));
  let v = show();
  await tick();
  v = show();
  assert.equal(button(v, 'next.browse.retry'), undefined, 'a loaded list has no read Retry');
  const titleRetry = all(v, 'button').find(node => (Array.isArray(node.props.children) ? node.props.children : [node.props.children]).includes('server.attention.lookUpAgain'));
  assert.ok(titleRetry, 'the row keeps its own retry control');
  titleRetry.props.onClick();
  await tick();
  assert.deepEqual(acts.map(value => value.body.action), ['retry'], 'the title Retry is the existing mutation');
});

test('a saved old Retry after the successor succeeds never reaches it', async () => {
  const {lists, reply, show, transport} = await mount();
  reply(async () => { throw new Error('down'); });
  let v = show();
  await tick();
  v = show();
  const oldRetry = button(v, 'next.browse.retry');
  assert.ok(oldRetry);
  const next = transport(lists, []);
  reply(async () => page([entry('e2', 'Monsters')]));
  v = show(next);
  await tick();
  v = show(next);
  assert.equal(articles(v).length, 1);
  const before = lists.length;
  oldRetry.props.onClick();
  await tick();
  v = show(next);
  assert.equal(lists.length, before, 'the retired admission cannot start a read');
  const shown = JSON.stringify(articles(v));
  assert.ok(shown.includes('Monsters'));
  assert.ok(!shown.includes('T2'));
});

test('replacement retires the old admission before its Retry can act', async () => {
  const {lists, reply, show, transport} = await mount();
  reply(async () => { throw new Error('down'); });
  let v = show();
  await tick();
  v = show();
  const oldRetry = button(v, 'next.browse.retry');
  const next = transport(lists, []);
  let resolveNew!: (value: unknown) => void;
  reply(() => new Promise(resolve => { resolveNew = resolve; }));
  v = show(next);
  const before = lists.length;
  oldRetry.props.onClick();
  await tick();
  assert.equal(lists.length, before, 'the retired callback is refused while the successor read is still pending');
  resolveNew(page([entry('e3', 'Up')]));
  await tick();
  v = show(next);
  assert.equal(articles(v).length, 1);
  assert.ok(JSON.stringify(articles(v)).includes('Up'));
  const after = lists.length;
  oldRetry.props.onClick();
  await tick();
  assert.equal(lists.length, after, 'replaying the retired callback after settlement is still refused');
});

test('a failed read suspends the settling poll until Retry', async () => {
  const {lists, reply, show} = await mount();
  reply(async path => {
    const cursor = cursorOf(path);
    if (cursor === '') return page([{...entry('p1', 'Waiting title'), reason: 'pending'}], 'page-2');
    throw new Error('later page down');
  });
  let v = show();
  await tick();
  v = show();
  button(v, 'web.hostedAccount.older').props.onClick();
  await tick();
  v = show();
  assert.ok(button(v, 'next.browse.retry'), 'the failed later read waits for Retry');
  const before = lists.length;
  await new Promise(resolve => setTimeout(resolve, 2200));
  assert.equal(lists.length, before, 'the settling poll does not reload the accepted page while the failure waits');
});

test('a replacement render withholds the predecessor rows before layout commits', async () => {
  const {lists, paintOnce, reply, show, transport} = await mount();
  reply(async () => page([entry('e1', 'T2')]));
  let v = show();
  await tick();
  v = show();
  assert.equal(articles(v).length, 1);
  const next = transport(lists, []);
  reply(async () => page([entry('e2', 'Monsters')]));
  const first = paintOnce(next);
  assert.equal(articles(first).length, 0, 'the replacement render may not draw the old rows');
  assert.equal(button(first, 'next.browse.retry'), undefined, 'nor the old read error');
});

test('a queued pre-cleanup settling tick cannot erase the failed cursor', async () => {
  const {lists, reply, show} = await mount();
  const timers: Array<() => void> = [];
  const original = globalThis.setInterval;
  (globalThis as any).setInterval = (fn: () => void) => {timers.push(fn); return 0 as any;};
  try {
    reply(async path => cursorOf(path) === '' ? page([{...entry('e1', 'Waiting'), reason: 'pending'}], 'n1') : Promise.reject(new Error('later down')));
    let v = show();
    await tick();
    v = show();
    button(v, 'web.hostedAccount.older')!.props.onClick();
    await tick();
    v = show();
    assert.ok(button(v, 'next.browse.retry'), 'the later page failure waits');
    const before = lists.length;
    timers.at(-1)!();
    await tick();
    assert.equal(lists.length, before, 'the queued tick belongs to the retired read generation');
  } finally {(globalThis as any).setInterval = original;}
});

test('a saved Retry from a recovered failure cannot start another page-one read', async () => {
  const {lists, reply, show} = await mount();
  reply(async () => {throw new Error('down');});
  let v = show();
  await tick();
  v = show();
  const saved = button(v, 'next.browse.retry')!;
  reply(async () => page([entry('e1', 'T2')]));
  saved.props.onClick();
  await tick();
  v = show();
  assert.equal(articles(v).length, 1);
  const after = lists.length;
  saved.props.onClick();
  await tick();
  assert.equal(lists.length, after, 'the recovered failure no longer retries a page-one read');
});

test('double paging applies one history change and one request', async () => {
  const {lists, reply, show} = await mount();
  let release!: (value: any) => void;
  reply(async path => cursorOf(path) === '' ? page([entry('e1', 'T2')], 'n1') : new Promise(resolve => {release = resolve;}));
  let v = show();
  await tick();
  v = show();
  const before = lists.length;
  button(v, 'web.hostedAccount.older')!.props.onClick();
  button(v, 'web.hostedAccount.older')!.props.onClick();
  await tick();
  assert.equal(lists.length - before, 1, 'the second saved paging callback joins the flight');
  release(page([entry('e2', 'Monsters')], ''));
  await tick();
  v = show();
  const after = lists.length;
  button(v, 'web.hostedAccount.newer')!.props.onClick();
  await tick();
  assert.equal(lists.length - after, 1, 'history moves back once');
});
