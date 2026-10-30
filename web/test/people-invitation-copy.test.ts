import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, createElement, isValidElement, type ReactNode} from 'react';
import {transformTsx} from './transform-tsx.mjs';

// Run the real table/card effects and callbacks with deterministic hook slots.
// Leaf kit surfaces stand in for DOM; this is lifecycle, not device evidence.
const bridge = {current: undefined as Frame | undefined};
(globalThis as any).__invitationCopyHooks = bridge;
const react = import.meta.resolve('react');
const hooks = 'data:text/javascript,' + encodeURIComponent(`export * from ${JSON.stringify(react)};
const b=globalThis.__invitationCopyHooks;
${['useState','useRef','useMemo','useCallback','useEffect','useLayoutEffect'].map(name => `export const ${name}=(...args)=>b.current.${name}(...args);`).join('')}`);
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button='button';");
  if (url.endsWith('/kit/settings/index.ts')) return module("export const SettingsGroup='group',SettingsPageFailure='failure',SettingsTable='table';");
  if (url.endsWith('/kit/overlays/index.ts')) return module(`export const useRetained=value=>{const r=globalThis.__invitationCopyHooks.current.useRef(undefined);if(value!=null)r.current=value;return r.current;};`);
  if (url.endsWith('/kit/overlays/Dialog.tsx')) return module("export const Dialog='dialog';");
  if (url.endsWith('/PeopleInvitations.tsx') || url.endsWith('/PeopleInvitationCard.tsx')) return module(transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify(hooks)}`));
  return next(url, context);
}});
const {PeopleInvitations} = await import('../src/screens/settings/PeopleInvitations.tsx');
// Warm the real local encoder so each test's readiness needs only promise turns.
await import('qrcode');
test.after(() => {loader.deregister();delete (globalThis as any).__invitationCopyHooks;});

const changed = (a?: readonly unknown[], b?: readonly unknown[]) => !a || !b || a.length !== b.length || a.some((value, i) => !Object.is(value, b[i]));
class Frame {
  slots: any[] = [];cursor = 0;pending: {slot: any; run: () => any; old?: () => void}[] = [];
  constructor(readonly tree: Tree) {}
  useRef(value: any) {return this.slots[this.cursor++] ??= {current: value};}
  useState(initial: any) {const i = this.cursor++;this.slots[i] ??= {value: typeof initial === 'function' ? initial() : initial};return [this.slots[i].value, (next: any) => {
    const value = typeof next === 'function' ? next(this.slots[i].value) : next;
    if (!Object.is(value, this.slots[i].value)) {this.slots[i].value = value;this.tree.dirty = true;}
  }];}
  useMemo(make: () => any, deps?: readonly unknown[]) {const i = this.cursor++;if (!this.slots[i] || changed(this.slots[i].deps, deps)) this.slots[i] = {value: make(), deps};return this.slots[i].value;}
  useCallback(fn: any, deps?: readonly unknown[]) {return this.useMemo(() => fn, deps);}
  useEffect(run: () => any, deps?: readonly unknown[]) {const i = this.cursor++, before = this.slots[i];if (!before || changed(before.deps, deps)) {
    const slot = {deps, cleanup: undefined};this.slots[i] = slot;this.pending.push({slot, run, old: before?.cleanup});
  }}
  useLayoutEffect(run: () => any, deps?: readonly unknown[]) {this.useEffect(run, deps);}
  unmount() {for (const slot of this.slots) slot?.cleanup?.();}
}
class Tree {
  frames = new Map<string, Frame>();leaves: any[] = [];dirty = false;
  constructor(public props: any) {}
  render() {
    for (let attempt = 0; attempt < 12; attempt++) {
      this.dirty = false;this.leaves = [];const seen = new Set<string>(), jobs: Frame['pending'] = [];
      const visit = (node: ReactNode, path: string) => Children.forEach(node, (child, i) => {
        if (!isValidElement<any>(child)) return;
        const at = path + '/' + (child.key ?? i);
        if (typeof child.type === 'function') {
          let frame = this.frames.get(at);if (!frame) {frame = new Frame(this);this.frames.set(at, frame);}
          seen.add(at);frame.cursor = 0;frame.pending = [];bridge.current = frame;
          visit((child.type as any)(child.props), at);jobs.push(...frame.pending);
        } else {
          this.leaves.push(child);visit(child.props.children, at);
          if (child.type === 'table') for (const row of child.props.rows) visit(child.props.rowActions(row), at + '/row-' + row.id);
          if (child.type === 'dialog') visit(child.props.footer, at + '/footer');
        }
      });
      visit(createElement(PeopleInvitations, this.props), 'root');
      for (const [path, frame] of this.frames) if (!seen.has(path)) {frame.unmount();this.frames.delete(path);}
      jobs.forEach(job => job.old?.());jobs.forEach(job => {job.slot.cleanup = job.run();});
      if (!this.dirty) return;
    }
    throw new Error('invitation effects did not settle');
  }
  buttons(icon?: string) {return this.leaves.filter(node => node.type === 'button' && (icon === undefined || node.props.icon === icon));}
  table() {return this.leaves.find(node => node.type === 'table')!;}
  dialog() {return this.leaves.find(node => node.type === 'dialog');}
  unmount() {for (const frame of this.frames.values()) frame.unmount();this.frames.clear();}
}
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
const settle = async () => {for (let i = 0; i < 20; i++) await Promise.resolve();};
const start = Date.parse('2026-10-28T12:00:00Z');
function fixture(t: import('node:test').TestContext) {
  t.mock.timers.enable({apis: ['Date', 'setTimeout'], now: start});
  let authorized = true, gesture = false;
  const writes: string[] = [], links: any[] = [];
  const row = {id: 'invite', revision: 7, state: 'pending', role: 'member', allowedLibraries: [], createdAt: new Date(start).toISOString(), expiresAt: new Date(start + 60_000).toISOString()};
  const invitations = {read: async () => ({items: [row], matchedCount: 1, totalCount: 1}), link: (selected: any, life: any) => {
    const result = deferred<string>();links.push({selected, life, ...result});return result.promise;
  }};
  const tree = new Tree({invitations, serverName: 'My server', libraries: [], authority: {isCurrent: () => authorized, onAuthorityChanged() {}}, version: 0, openHome() {}, reportError(error: unknown) {throw error;}});
  for (const [key, value] of Object.entries({document: {}, navigator: {clipboard: {writeText(text: string) {assert.equal(gesture, true);writes.push(text);return Promise.resolve();}}, share() {return Promise.resolve();}}})) {
    const before = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, {configurable: true, value});
    t.after(() => {if (before) Object.defineProperty(globalThis, key, before);else delete (globalThis as any)[key];});
  }
  t.after(() => tree.unmount());
  const click = (button: any) => {gesture = true;try {button.props.onClick({currentTarget: {id: 'opener'}});} finally {gesture = false;}tree.render();};
  const load = async () => {tree.render();t.mock.timers.tick(0);await settle();tree.render();};
  const open = () => {click(tree.buttons().find(button => button.props.size === 'xs'));assert.ok(tree.dialog());};
  return {tree, row, links, writes, click, load, open, retire: () => {authorized = false;}};
}

test('table prepares the exact row without copying; ready card Copy uses a fresh gesture and keeps Save picture/Share', async t => {
  const f = fixture(t);await f.load();f.open();
  assert.equal(f.links.length, 1);assert.equal(f.links[0].selected.revision, 7);assert.deepEqual(f.writes, []);
  assert.equal(f.tree.buttons('i-copy').length, 0, 'no copy of an unprepared value');
  t.mock.timers.tick(10_000);f.links[0].resolve('https://example.test/join#ready');await settle();f.tree.render();
  assert.deepEqual(f.writes, [], 'network completion cannot use the old gesture');
  assert.equal(f.tree.buttons('i-download').length, 1);assert.equal(f.tree.buttons('i-share').length, 1);
  f.click(f.tree.buttons('i-copy')[0]);assert.deepEqual(f.writes, ['https://example.test/join#ready']);
  assert.equal(f.links.length, 1, 'ready Copy does not fetch another capability');
  await settle();f.tree.render();assert.equal(f.tree.buttons('i-check').length, 1);
});

test('close/reopen drops the old opening and ignores its delayed link or retained actions', async t => {
  const f = fixture(t);await f.load();f.open();const a = f.links[0], closeA = f.tree.buttons().find(button => button.props.variant === 'primary');
  f.click(closeA);assert.equal(a.life.signal.aborted, true);assert.equal(a.life.isCurrent(), false);
  f.open();const b = f.links[1];f.click(closeA);assert.ok(f.tree.dialog(), 'old Done cannot close B');
  a.resolve('retired');await settle();f.tree.render();assert.equal(f.tree.buttons('i-copy').length, 0);
  b.resolve('current');await settle();f.tree.render();const held = f.tree.buttons('i-copy')[0];
  f.tree.props = {...f.tree.props, version: 1};await f.load();f.click(held);
  assert.equal(b.life.signal.aborted, true);assert.deepEqual(f.writes, []);
});

test('query/source/revision replacement and authority retirement cannot copy an earlier card', async t => {
  const f = fixture(t);await f.load();f.open();f.links[0].resolve('original');await settle();f.tree.render();
  const held = f.tree.buttons('i-copy')[0];
  f.tree.table().props.onQueryChange({search: 'new', filters: {status: 'all'}, sort: {by: 'sent', direction: 'desc'}});f.tree.render();f.click(held);
  assert.equal(f.links[0].life.signal.aborted, true);assert.deepEqual(f.writes, []);
  f.row.revision = 8;f.tree.props = {...f.tree.props, invitations: {...f.tree.props.invitations}};await f.load();
  t.mock.timers.tick(200);await settle();f.tree.render();f.open();assert.equal(f.links[1].selected.revision, 8);
  f.links[1].resolve('second');await settle();f.tree.render();
  const next = f.tree.buttons('i-copy')[0];f.retire();f.click(next);assert.deepEqual(f.writes, []);
  assert.equal(f.links[1].life.isCurrent(), false);
});

test('expiry retires a ready capability and prevents delayed preparation or retained Copy', async t => {
  const f = fixture(t);await f.load();f.open();f.links[0].resolve('ready');await settle();f.tree.render();const held = f.tree.buttons('i-copy')[0];
  t.mock.timers.tick(60_000);f.tree.render();f.click(held);
  assert.equal(f.links[0].life.signal.aborted, true);assert.equal(f.links[0].life.isCurrent(), false);
  assert.equal(f.tree.buttons('i-copy').length, 0);assert.deepEqual(f.writes, []);
  f.click(f.tree.buttons().find(button => button.props.variant === 'primary'));f.open();
  assert.equal(f.links.length, 1, 'expired opening cannot fetch or recover a link');
});

test('expiry during a slow link request rejects its late value without preparing another one', async t => {
  const f = fixture(t);await f.load();f.open();const pending = f.links[0];
  t.mock.timers.tick(60_000);pending.resolve('too late');await settle();f.tree.render();
  assert.equal(pending.life.signal.aborted, true);assert.equal(pending.life.isCurrent(), false);
  assert.equal(f.tree.buttons('i-copy').length, 0);assert.deepEqual(f.writes, []);assert.equal(f.links.length, 1);
});
