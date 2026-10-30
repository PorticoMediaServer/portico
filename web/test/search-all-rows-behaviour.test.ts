import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, tree, textOf} from './helpers/interaction-harness.mjs';
import {displayedSearchEntries} from '../src/screens/search/selection-window.ts';

test('actual Search shelf ranges register later visible cards, admit keyboard focus, and keep Select all within that displayed window', async context => {
  const track = {}, replacementTrack = {}, root = {clientWidth: 1312};
  const h = createRenderer({element: root});
  context.after(() => h.dispose());
  let currentTrack = track, reobserve: (() => void) | undefined;
  const part = {querySelector: () => ({parentElement: currentTrack})};
  const nodes = new Map([[0, part]]), observers: {callback: (records: any[]) => void; disconnected: boolean; observed: unknown[]; unobserved: unknown[]}[] = [];
  const originals = ['ResizeObserver', 'IntersectionObserver', 'MutationObserver'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)] as const);
  Object.defineProperty(globalThis, 'ResizeObserver', {configurable: true, value: class {observe() {} disconnect() {}}});
  Object.defineProperty(globalThis, 'IntersectionObserver', {configurable: true, value: class {
    state: typeof observers[number];
    constructor(callback: (records: any[]) => void) {this.state = {callback, disconnected: false, observed: [], unobserved: []};observers.push(this.state);}
    observe(target: unknown) {this.state.observed.push(target);}
    unobserve(target: unknown) {this.state.unobserved.push(target);}
    disconnect() {this.state.disconnected = true;}
  }});
  Object.defineProperty(globalThis, 'MutationObserver', {configurable: true, value: class {
    constructor(callback: () => void) {reobserve = callback;}
    observe(target: any) {assert.equal(target.clientWidth, root.clientWidth);}
    disconnect() {reobserve = undefined;}
  }});
  context.after(() => {for (const [key, descriptor] of originals) {if (descriptor) Object.defineProperty(globalThis, key, descriptor);else Reflect.deleteProperty(globalThis, key);}});
  const app = await componentModule(new URL('../src/screens/search/AllResultsRow.tsx', import.meta.url), {
    react: {...h.react, useRef: (value: unknown) => h.react.useRef(value instanceof Map ? nodes : value)},
    '@core/presentation/search-model.ts': {searchGroupLabel: (id: string) => id}, '../../kit/controls': {Button: 'Button'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../shell/viewport': {useShellWidth: () => 1440}, '../browse/PagedShelf': {PagedShelf: 'PagedShelf'},
    '../detail/text': {t: (key: string, values?: {count?: number}) => values?.count === undefined ? key : `${key}:${values.count}`},
    '../detail/ScreenProblem': {ScreenProblem: 'ScreenProblem'}, './selection-window': {displayedSearchEntries}, './search.module.css': {default: {}},
  });
  const entries = Array.from({length: 140}, (_, index) => ({id: String(index), libraryId: 'library', kind: 'movie'}));
  let result = {id: 'movies', items: entries, nextCursor: 'next', totalCount: 200, totalCountExact: true}, excluded: typeof entries[number] | undefined;
  const selected: any[][] = [];let offered: readonly any[] = [];
  const selection = {canSelectGroup(actual: unknown, shown: readonly any[]) {assert.strictEqual(actual, result);offered = shown;return shown.length > 0;},
    selectGroup(actual: unknown, shown: any[]) {assert.strictEqual(actual, result);selected.push(shown);}};
  const binding = {results: {page: () => ({loading: false, error: null}), next: async () => {}}, service: {getSnapshot: () => ({query: {q: 'harbor'}})}, context: {effects: {reportError(error: unknown) {throw error;}}}};
  const render = () => tree(h.render(() => app.AllResultsRow({result, binding, selection, excluded, size: 1, onSeeAll() {}, renderEntry: (_entry: any, _index: number, visible: boolean) => visible})));
  const shelf = () => one(render(), 'PagedShelf').props;
  const admitted = (index: number, focused = false) => {const current = shelf();return current.renderItem(current.items[index], {index, focused});};
  render();assert.equal(admitted(90), false);
  shelf().onVisibleRange(90, 96);render();
  assert.equal(admitted(90), false, 'horizontal geometry alone does not admit a vertically offscreen shelf');
  observers.at(-1)!.callback([{target: track, isIntersecting: true}]);render();
  assert.equal(admitted(90), true);assert.equal(admitted(0), false);assert.equal(admitted(97), false);
  assert.deepEqual(offered, entries.slice(90, 97), 'Select all asks the existing capability for the actual displayed rows');
  const actions = all(tree(shelf().actions), 'Button'), choose = actions.find((node: any) => textOf(node) === 'next.selection.selectAll:7');
  assert.ok(choose);choose.props.onClick();assert.deepEqual(selected, [entries.slice(90, 97)]);
  assert.equal(admitted(120, true), true, 'actual Shelf focus promotes an off-window keyboard target');
  assert.equal(admitted(120, false), false, 'blur clears the focused fallback supplied by Shelf');
  shelf().onVisibleRange(120, 126);render();
  assert.equal(admitted(90), false);assert.equal(admitted(120), true);
  observers.at(-1)!.callback([{target: track, isIntersecting: false}]);render();
  assert.equal(admitted(120), false);assert.deepEqual(offered, []);
  currentTrack = replacementTrack;reobserve!();render();
  assert.deepEqual(observers.at(-1)!.unobserved, [track]);
  assert.deepEqual(observers.at(-1)!.observed, [track, replacementTrack]);
  observers.at(-1)!.callback([{target: track, isIntersecting: true}]);render();
  assert.equal(admitted(120), false, 'a detached track cannot revive the registration window');
  observers.at(-1)!.callback([{target: replacementTrack, isIntersecting: true}]);render();assert.equal(admitted(120), true);
  const top = entries[0], previous = observers.at(-1)!;
  result = {...result, items: [top], nextCursor: '', totalCount: 1};excluded = top;render();
  assert.equal(all(render(), 'PagedShelf').length, 0);assert.equal(previous.disconnected, true);
  excluded = undefined;render();render();
  assert.equal(all(render(), 'PagedShelf').length, 1, 'selection mode can reveal a row previously wholly excluded by the top hit');
  assert.notStrictEqual(observers.at(-1), previous);
  shelf().onVisibleRange(0, 0);observers.at(-1)!.callback([{target: replacementTrack, isIntersecting: true}]);render();
  assert.equal(admitted(0), true, 'the revealed row establishes fresh geometry and viewport observers');
});
