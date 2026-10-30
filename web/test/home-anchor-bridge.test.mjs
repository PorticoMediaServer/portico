import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree} from './helpers/interaction-harness.mjs';

// FX177: the blocker's component/hook harness shape on this repo's own helpers.
// Mounts the real HomePage, measures destination, card scroll and guarded anchor
// consumption. P2-66: with servers, Home hands over to the app's own chooser at
// /servers on this same address ('Directory' below), never to a server.

const server = id => ({id, status: 'quiet', ownedByMe: false, name: id, presence: {lastSeenAt: null}, role: 'member'});

async function mount({hash = '', pathname = '/', search = '', servers = [server('one')], session = {account: {id: 'account', username: 'person'}, familyId: 'family'}} = {}) {
  const saved = {location: globalThis.location, history: globalThis.history, document: globalThis.document,
    addEventListener: globalThis.addEventListener, removeEventListener: globalThis.removeEventListener, matchMedia: globalThis.matchMedia};
  const handlers = new Set(), replaces = [], scrolls = [], cleanups = new Set(), opens = [];
  const targets = new Map();
  const target = id => {
    if (!targets.has(id)) targets.set(id, {id, getClientRects: () => [{}], scrollIntoView: options => scrolls.push({id, ...options})});
    return targets.get(id);
  };
  let location = {hash, pathname, search, href: 'https://web.getportico.tv' + pathname + search + hash, replace: url => opens.push(url)};
  globalThis.location = location;
  globalThis.history = {state: {kept: true, continuation: 'original'}, replaceState(state, _title, url) {
    replaces.push({state, url});
    const [rest, fragment] = url.split('#');
    const [path, query] = rest.split('?');
    location = {...location, href: url, pathname: path, search: query ? '?' + query : '', hash: fragment ? '#' + fragment : ''};
    globalThis.location = location;
  }};
  globalThis.document = {getElementById: id => ['join', 'setup', 'setup-details'].includes(id) ? target(id) : null};
  globalThis.matchMedia = () => ({matches: false});
  globalThis.addEventListener = (type, fn) => {if (type === 'hashchange') handlers.add(fn);};
  globalThis.removeEventListener = (type, fn) => {if (type === 'hashchange') handlers.delete(fn);};

  const h = createRenderer();
  // Record the live cleanup each effect actually returns, and let a test unmount by
  // invoking them (the shared helper is not edited).
  const react = {...h.react, useLayoutEffect: (run, deps) => h.react.useLayoutEffect(() => {
    const cleanup = run();
    if (typeof cleanup === 'function') cleanups.add(cleanup);
    return () => {cleanups.delete(cleanup); if (typeof cleanup === 'function') cleanup();};
  }, deps)};
  let currentSession = session;
  const account = {service: {getSnapshot: () => ({session: currentSession})}};
  const api = {servers: async () => ({items: servers})};
  const m = await componentModule(new URL('../src/services/HomePage.tsx', import.meta.url), {
    react,
    '../shell/DocumentBootReady': {DocumentBootReady: ({children}) => children},
    './session': {
      accountIdentity: value => ({accountId: value?.account?.id, familyId: value?.familyId}),
      sameAccount: (a, b) => Boolean(a && b && a.accountId === b.accountId && a.familyId === b.familyId),
    },
    './workspace-api': {workspaceApi: () => api}, './i18n': {servicesI18n: () => ({t: x => x})},
    '../kit/accounts/ServicesAuth': {ServicesAuthStage: 'Stage'}, '../kit/accounts/ServicesWorkspace': {ServicesStart: 'Start', ServicesHeader: 'Header'},
    './DirectoryPage': {ServicesLocalServerPage: 'Local'},
    // No app is registered here, so goTo is an ordinary page load (platform/hosted-navigation).
    '../platform/hosted-navigation': {goTo: (to, options) => options?.replace ? globalThis.location.replace(to) : globalThis.location.assign(to)}, './use-membership-ending': {useMembershipEnding: () => ({visible: false})},
  });
  // The original prop never changes: a successor session exists only in the service
  // snapshot, so a stale-callback vector cannot create a rightful successor Home.
  const render = () => tree(h.render(() => m.ServicesHomePage({account, session})));
  const settle = async () => {let value; for (let i = 0; i < 6; i++) {value = render(); await Promise.resolve();} return value;};
  const dispatchHash = async () => {for (const fn of [...handlers]) fn(); return settle();};
  const replaceAccount = next => {currentSession = next;};
  return {
    value: await settle(), settle, dispatchHash, handlers, replaces, scrolls, cleanups, opens, replaceAccount, render,
    setHash: hash => {location = {...location, hash, href: location.pathname + location.search + hash}; globalThis.location = location;},
    has: type => type === 'Directory' ? opens.includes('/servers') : render().some(node => node.type === type),
    restore: () => Object.assign(globalThis, saved),
  };
}

test('cold supported anchors reach Start and scroll the authored card', async () => {
  for (const [anchor, section] of [['#join', 'join'], ['#setup', 'setup']]) {
    const f = await mount({hash: anchor});
    try {
      assert.equal(f.has('Start'), true, anchor);
      assert.equal(f.has('Directory'), false, anchor);
      assert.equal(f.replaces.length, 1, anchor);
      assert.deepEqual(f.replaces[0].state, {kept: true, continuation: 'original'});
      assert.equal(f.replaces[0].url, '/', anchor);
      assert.deepEqual(f.scrolls.at(-1), {id: section, block: 'start', behavior: 'smooth'}, anchor);
    } finally { f.restore(); }
  }
});

test('query and history state survive the single consumption', async () => {
  const f = await mount({hash: '#join', search: '?capability=abc&x=1'});
  try {
    assert.deepEqual(f.replaces[0], {state: {kept: true, continuation: 'original'}, url: '/?capability=abc&x=1'});
  } finally { f.restore(); }
});

test('multiple servers, plain root, and zero servers keep their own destinations', async () => {
  const many = await mount({servers: [server('one'), server('two')], hash: '#join'});
  try { assert.equal(many.has('Start'), true); } finally { many.restore(); }
  const root = await mount({servers: [server('one'), server('two')]});
  try { assert.equal(root.has('Directory'), true); assert.equal(root.replaces.length, 0); } finally { root.restore(); }
  const empty = await mount({servers: []});
  try { assert.equal(empty.has('Start'), true); } finally { empty.restore(); }
});

test('same-document join then setup then a repeat join each scroll and keep the Start draft', async () => {
  const f = await mount({hash: '#join'});
  try {
    // The Start stub is what the real Start renders; its retained invitation draft is
    // the proof of no remount (a remount would reset it).
    const stub = () => f.render().find(node => node.type === 'Start');
    stub().props.onInvitationChange('retained draft');
    f.setHash('#setup');
    await f.dispatchHash();
    assert.deepEqual(f.scrolls.at(-1), {id: 'setup', block: 'start', behavior: 'smooth'});
    f.setHash('#join');
    await f.dispatchHash();
    assert.deepEqual(f.scrolls.at(-1), {id: 'join', block: 'start', behavior: 'smooth'});
    assert.equal(f.replaces.length, 3);              // one consumption per navigation
    assert.equal(stub().props.invitation, 'retained draft');
  } finally { f.restore(); }
});

test('invalid fragments never become intent or rewrite the URL', async () => {
  for (const anchor of ['#security', '#join/', '#JOIN', '#setup?capability=1', '#account']) {
    const f = await mount({hash: anchor});
    try { assert.equal(f.has('Directory'), true, anchor); assert.equal(f.replaces.length, 0, anchor); assert.equal(f.scrolls.length, 0, anchor); } finally { f.restore(); }
  }
});

test('a changed account in the service snapshot leaves the retained callback inert', async () => {
  const f = await mount();                            // plain root: a Directory, no intent
  try {
    assert.equal(f.has('Directory'), true);
    const held = [...f.handlers];
    assert.equal(held.length, 1);                     // the original Home's own handler
    const before = {replaces: f.replaces.length, scrolls: f.scrolls.length};
    f.replaceAccount({account: {id: 'other', username: 'other'}, familyId: 'other-family'});
    f.setHash('#setup');
    for (const fn of held) fn();                      // the retained original callback
    await f.settle();
    assert.equal(f.replaces.length, before.replaces);
    assert.equal(f.scrolls.length, before.scrolls);
    assert.equal(f.has('Directory'), true);
  } finally { f.restore(); }
});

test('a changed pathname refuses the anchor and cleanup removes the live handler', async () => {
  const f = await mount({hash: '#join'});
  try {
    const held = [...f.handlers];
    const before = {replaces: f.replaces.length, scrolls: f.scrolls.length};
    globalThis.location = {...globalThis.location, pathname: '/account', href: '/account#setup', hash: '#setup'};
    for (const fn of held) fn();
    await f.settle();
    assert.equal(f.replaces.length, before.replaces);
    assert.equal(f.scrolls.length, before.scrolls);

    const old = [...f.handlers];
    for (const cleanup of [...f.cleanups]) cleanup();
    assert.equal(f.handlers.size, 0);
    globalThis.location = {...globalThis.location, pathname: '/', href: '/#join', hash: '#join'};
    for (const fn of old) fn();                       // the captured old callback
    await f.settle();
    assert.equal(f.replaces.length, before.replaces);
    assert.equal(f.scrolls.length, before.scrolls);
  } finally { f.restore(); }
});
