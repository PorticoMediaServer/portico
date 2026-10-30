import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children)] : [];

async function fixture(count: number) {
  const host = hooks(), effects: (() => void)[] = [], cleanups = new Set<{cleanup?: () => void}>();
  const effect = (run: () => void | (() => void), deps: readonly unknown[]) => {
    const slot = host.react.useRef(undefined);
    if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {
      const before = slot.current, next = {deps, cleanup: undefined as (() => void) | undefined};
      slot.current = next; cleanups.add(next);
      effects.push(() => {before?.cleanup?.(); next.cleanup = run() || undefined;});
    }
  };
  Object.assign(host.react, {useEffect: effect, useLayoutEffect: effect});
  Object.assign(host.react.default, {useEffect: effect, useLayoutEffect: effect});
  const ids = Array.from({length: count}, (_, index) => `item-${index + 1}`), libraryIds = ['library'];
  const titles = new Map<string, {id: string; title: string}>(), calls: {ids: string[]; signal: AbortSignal}[] = [];
  let active = true, selectionCount = 0, pause: (() => Promise<void>) | undefined;
  const source = {
    select: async () => {selectionCount++;},
    // This is the existing source's public bounded-page contract. It deliberately
    // ignores IDs after the first twenty, so passing a visible prefix cannot pass.
    labels: async (page: readonly string[], signal: AbortSignal) => {
      calls.push({ids: [...page], signal}); const pending = pause; pause = undefined; await pending?.();
      if (!signal.aborted) for (const id of page.slice(0, 20)) titles.set(id, {id, title: `Title ${id}`});
    },
    title: (id: string) => titles.get(id), cancelSearch() {},
  };
  const isCurrent = () => active;
  const {NativeChannelPins} = await screenModule(new URL('../src-next/screens/settings-live/server/channels/ChannelPins.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../../../../kit/controls': {Button: 'Button', Chip: 'Chip', TextField: 'TextField'},
    '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => ({colors: {text: 'white', record: 'red', textSecondary: 'grey'}})},
  });
  let tree: any;
  const render = () => {tree = host.render(() => NativeChannelPins({source, libraryIds, ids, locked: false, isCurrent, t: (id: string) => id, onChange() {}})); for (const run of effects.splice(0)) run(); return tree;};
  const settle = async () => {await new Promise(resolve => setImmediate(resolve)); return render();};
  const more = () => {const button = nodes(tree).find(node => node.type === 'Button' && node.props.children.includes('action.showMore')); assert.ok(button); button.props.onPress(); render();};
  render(); await settle();
  return {ids, calls, more, render, settle, tree: () => tree, selections: () => selectionCount,
    retire: () => {active = false;},
    pauseNext: () => {let finish!: () => void; pause = () => new Promise<void>(resolve => {finish = resolve;}); return () => finish();},
    dispose: () => {active = false; for (const value of cleanups) value.cleanup?.();},
  };
}

test('Pinned titles load only revealed pages and Show more resolves labels beyond the first twenty', async () => {
  const f = await fixture(45);
  try {
    assert.deepEqual(f.calls.map(call => call.ids), [f.ids.slice(0, 20)]);
    assert.equal(nodes(f.tree()).filter(node => node.type === 'Chip').length, 20);
    f.more(); await f.settle();
    assert.equal(nodes(f.tree()).filter(node => node.type === 'Chip').length, 40);
    assert.deepEqual(f.calls.at(-1)?.ids, f.ids.slice(20, 40));
    f.more(); await f.settle();
    const chips = nodes(f.tree()).filter(node => node.type === 'Chip');
    assert.equal(chips.length, 45);
    assert.deepEqual(chips.map(chip => chip.props.children[0]), f.ids.map(id => `Title ${id}`));
    assert.deepEqual(f.calls.at(-1)?.ids, f.ids.slice(40));
    assert.ok(f.calls.every(call => call.ids.length <= 20));
    assert.equal(nodes(f.tree()).some(node => node.type === 'Button' && node.props.children.includes('action.showMore')), false);
  } finally {f.dispose();}
});

test('A retired pin editor stops before the next label page after an in-flight page completes', async () => {
  const f = await fixture(45);
  try {
    const finish = f.pauseNext(); f.more(); await f.settle();
    assert.equal(f.calls.length, 2); f.retire(); finish(); await f.settle();
    assert.equal(f.calls.length, 2, 'the second visible page is never requested from a retired opening');
  } finally {f.dispose();}
});

test('Unmount aborts an in-flight label page and prevents another page from starting', async () => {
  const f = await fixture(45);
  const finish = f.pauseNext(); f.more(); await f.settle();
  assert.equal(f.calls.length, 2); const pending = f.calls.at(-1)!;
  f.dispose(); assert.equal(pending.signal.aborted, true); finish(); await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.length, 2);
});
