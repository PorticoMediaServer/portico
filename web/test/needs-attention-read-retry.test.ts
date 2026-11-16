/**
 * Unmatched items on the rebuilt server (definition area 4, rules 12 to 17): the list reads
 * GET /v1/admin/libraries/{id}/unmatched, a failed read is retried at its own page, a list from
 * an api that was replaced never shows, and the owner's choices go to the rebuilt routes: Use
 * this match and Keep as is (PUT …/match), Look it up again (POST …/metadata/refresh).
 * The actual NeedsAttention component runs; only the transport is stubbed.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tick, tree} from './helpers/interaction-harness.mjs';

const entry = (id: string, title: string, kind = 'movie', matches: unknown[] = []) => ({itemId: id, kind, title, reason: 'no_close_match', matches});
const page = (items: unknown[], nextCursor = '') => ({items, ...(nextCursor ? {nextCursor} : {})});
const cursorOf = (path: string) => new URLSearchParams(path.split('?')[1] ?? '').get('cursor') ?? '';

async function mount() {
  const h = createRenderer(), lists: string[] = [], acts: {path: string; method: string; body: unknown}[] = [];
  let respond: (path: string) => Promise<unknown> = async () => page([]);
  const transport = () => ({
    request: (path: string, method = 'GET', body?: unknown) => {
      if (method !== 'GET') { acts.push({path, method, body}); return Promise.resolve(path.endsWith('/refresh') ? {matched: false} : {itemId: 'x'}); }
      lists.push(path);
      return respond(path);
    },
  });
  const api = transport();
  const app = await componentModule(new URL('../src/screens/metadata/NeedsAttention.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key, has: () => false}},
    '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/overlays': {Dialog: 'Dialog'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/accounts/ServicesAuth': {ServicesNotice: 'ServicesNotice'},
    './metadata.module.css': {default: {}},
  });
  const show = (target = api) => {
    const paint = () => tree(h.render(() => app.NeedsAttention({libraryId: 'lib', libraryName: 'Movies', api: target})));
    paint();
    return paint();
  };
  return {acts, api, lists, reply: (fn: (path: string) => Promise<unknown>) => {respond = fn;}, show, transport};
}

const words = (node: any): string => typeof node === 'string' ? node : Array.isArray(node) ? node.map(words).join('') : node?.props ? words(node.props.children) : '';
const press = (rendered: any[], type: string, label: string) => {
  const found = all(rendered, type).find(node => words(node).includes(label));
  assert.ok(found, `${label} is offered`);
  found.props.onClick({currentTarget: {}});
};
const articles = (rendered: any[]) => all(rendered, 'article');

test('the list reads the rebuilt route, and a failed read is retried at the same page', async () => {
  const {acts, lists, reply, show} = await mount();
  reply(async () => { throw new Error('transport down'); });
  show(); await tick();
  let v = show();
  assert.equal(articles(v).length, 0);
  reply(async () => page([entry('e1', 'T2')]));
  press(v, 'Button', 'next.browse.retry');
  await tick();
  v = show();
  assert.ok(lists.every(path => path.startsWith('/v1/admin/libraries/lib/unmatched?')), lists.join());
  assert.equal(cursorOf(lists[1]), '');
  assert.equal(articles(v).length, 1);
  assert.equal(acts.length, 0, 'a list Retry changes nothing');
});

test('a failed later page keeps the rows already shown and retries that page', async () => {
  const {lists, reply, show} = await mount();
  reply(async path => {
    if (cursorOf(path) === '') return page([entry('e1', 'T2')], 'next-1');
    throw new Error('later page down');
  });
  show(); await tick();
  let v = show();
  press(v, 'Button', 'web.hostedAccount.older');
  await tick();
  v = show();
  assert.equal(articles(v).length, 1, 'the rows already shown stay');
  press(v, 'Button', 'next.browse.retry');
  await tick();
  assert.equal(cursorOf(lists.at(-1)!), 'next-1');
});

test('a list from a replaced api never shows, and its old Retry does nothing', async () => {
  const {lists, reply, show, transport} = await mount();
  reply(async () => { throw new Error('down'); });
  show(); await tick();
  const old = show();
  let resolveOld!: (value: unknown) => void;
  reply(() => new Promise(resolve => { resolveOld = resolve; }));
  const next = transport();
  show(next);
  reply(async () => page([entry('e2', 'Monsters')]));
  const before = lists.length;
  press(old, 'Button', 'next.browse.retry');
  await tick();
  assert.equal(lists.length, before, 'the old Retry starts no read');
  resolveOld(page([entry('e3', 'Up')]));
  await tick();
  const v = show(next);
  assert.ok(!JSON.stringify(articles(v)).includes('T2'));
});

test('Use this match, Keep as is and Look it up again go to the rebuilt routes', async () => {
  const {acts, reply, show} = await mount();
  reply(async () => page([
    entry('show1', 'One Piece', 'show', [{provider: 'tmdb', id: '37854', title: 'One Piece', year: 1999}]),
    entry('film1', 'Naruto Shippuden', 'movie')]));
  show(); await tick();
  let v = show();
  const [onePiece, naruto] = articles(v);
  // A show's match carries the episode order the owner picks.
  const order = all(tree(onePiece), 'select')[0];
  order.props.onChange({currentTarget: {value: 'dvd'}});
  v = show();
  press(tree(articles(v)[0]), 'Button', 'metadata.attention.useMatch');
  await tick();
  assert.deepEqual(acts.at(-1), {path: '/v1/admin/items/show1/match', method: 'PUT', body: {provider: 'tmdb', id: '37854', episodeOrder: 'dvd'}});
  v = show();
  press(tree(articles(v)[1]), 'button', 'metadata.attention.keepAsIs');
  await tick();
  assert.deepEqual(acts.at(-1), {path: '/v1/admin/items/film1/match', method: 'PUT', body: {keepAsIs: true}});
  v = show();
  press(tree(articles(v)[1]), 'button', 'server.attention.lookUpAgain');
  await tick();
  assert.deepEqual(acts.at(-1), {path: '/v1/admin/items/film1/metadata/refresh', method: 'POST', body: undefined});
  assert.ok(naruto);
});
