import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {readingSelectionAuthority} from '../src/screens/read/reading-selection-authority.ts';

function nodes(node: any): any[] {return Array.isArray(node) ? node.flatMap(nodes) : node && typeof node === 'object' ? [node, ...nodes(node.props?.children)] : [];}
async function fixture() {
  const hook = hooks(), opened: any[] = [];
  const modules = {react: hook.react, '@i18n': {defaultI18n: {t: (key: string) => key}}, '../../kit/controls': {Button: 'Button'}, '../../kit/artwork': {CoverMosaic: 'Mosaic'}, '../../kit/selection': {CollectionPicker: 'Picker'},
    '@core/presentation/errors.ts': {presentError: () => ({body: 'Unavailable'})}, './reading-selection.module.css': {default: {}},
    './reading-selection-authority': {readingSelectionAuthority},
    '../detail/context': {screenLifetime: (context: any) => {const life = new AbortController(); return {current: () => !life.signal.aborted && context.isCurrent(), api: {}, signal: () => life.signal, dispose: () => life.abort()};}},
    '@core/reading-collection.ts': {ReadingCollection: class {
      constructor(readonly options: any, readonly items: any[]) {opened.push(this);}
      load = async () => {}; dispose() {}
    }},
  };
  return {hook, opened, ...await componentModule(new URL('../src/screens/read/ReadSelectionBar.tsx', import.meta.url), modules)};
}

test('web Reading draws Collection beside Edit and opens exactly the scoped selected publications', async () => {
  const f = await fixture(), context = {isCurrent: () => true, identity: {viewer: {authority: 'local', role: 'owner'}}, scope: {serverId: 's', viewerId: 'v'}, effects: {reportError: (error: unknown) => {throw error;}}};
  const item = {id: 'one', libraryId: 'books', title: 'One'}, props = {context, selection: new Map([['one', item]]), kind: 'ebook', current: () => true, onExit() {}, onEdit() {}};
  const view = f.hook.render(() => f.ReadSelectionBar(props));
  const button = nodes(view).find(node => node.type === 'Button' && node.props.icon === 'i-layers');
  assert.ok(button); assert.equal(button.props.disabled, false); button.props.onClick({currentTarget: {}});
  assert.equal(f.opened.length, 1); assert.deepEqual(f.opened[0].items, [{...item, kind: 'ebook'}]);
  assert.equal(f.opened[0].options.isCurrent(), true);
  f.hook.render(() => f.ReadSelectionBar({...props, selection: new Map(props.selection)}));
  assert.equal(f.opened[0].options.isCurrent(), false);
});

test('retained web Collection presses cannot open a successor selection; member Edit stays absent', async () => {
  const f = await fixture(), context = {isCurrent: () => true, identity: {viewer: {authority: 'local', role: 'member'}}};
  const props = {context, selection: new Map([['one', {id: 'one', libraryId: 'books', title: 'One'}]]), kind: 'ebook', current: () => true, onExit() {}, onEdit() {throw new Error('member edit');}};
  const view = f.hook.render(() => f.ReadSelectionBar(props));
  assert.equal(nodes(view).filter(node => node.props?.icon === 'i-edit').length, 0);
  const old = nodes(view).find(node => node.props?.icon === 'i-layers');
  f.hook.render(() => f.ReadSelectionBar({...props, selection: new Map()})); old.props.onClick({currentTarget: {}});
  assert.equal(f.opened.length, 0);
});

test('web picker executes the rendered choice and presents the real partial receipt', async () => {
  const f = await fixture(), chosen: any[] = [], retried: any[] = [];
  const state = {phase: 'result', rows: [{id: 'c', name: 'Shelf', count: 10, canAdd: true, artwork: []}], query: '', next: false, previous: false,
    destination: {id: 'c', name: 'Shelf', entryCount: 11}, receipt: {added: 1, alreadyPresent: 0, failed: 1, unknown: 0, pending: false, complete: true, failures: [{itemId: 'two', code: 'not_found'}]}};
  const controller = {items: [{id: 'one', title: 'First'}, {id: 'two', title: 'Second'}], getSnapshot: () => state, subscribe: () => () => {},
    choose: async (...args: any[]) => {chosen.push(args);}, retry: async () => {retried.push(true);}};
  const view = f.hook.render(() => f.ReadCollectionPicker({controller, context: {isCurrent: () => true, effects: {reportError: (error: unknown) => {throw error;}}}, anchor: {current: null}, onClose() {}}));
  assert.equal(view.type, 'Picker'); assert.equal(view.props.result.inserted, 1); assert.equal(view.props.result.failed, 1);
  assert.deepEqual(view.props.result.failures, [{id: 'two', title: 'Second', reason: 'Unavailable'}]);
  view.props.onAdd('c'); await Promise.resolve(); assert.deepEqual(chosen, [[state, 'c']]);
  view.props.onRetry(['two']); await Promise.resolve(); assert.equal(retried.length, 1);
});

test('members keep Reading Select and their own collection creation; only the owner edits details', () => {
  // Justin: members retain Select and add the drawn bulk Collection; no owner mark, no metadata editor.
  assert.deepEqual(readingSelectionAuthority({authority: 'local', role: 'member'}), {select: true, create: true, edit: false});
  assert.deepEqual(readingSelectionAuthority({authority: 'local', role: 'owner'}), {select: true, create: true, edit: true});
  assert.deepEqual(readingSelectionAuthority({authority: 'server', role: 'owner'}), {select: false, create: false, edit: false});
  assert.deepEqual(readingSelectionAuthority(undefined), {select: false, create: false, edit: false});
});

test('a member bar opens the picker with personal creation and no Edit action', async () => {
  const f = await fixture(), context = {isCurrent: () => true, identity: {viewer: {authority: 'local', role: 'member'}}, scope: {serverId: 's', viewerId: 'v'}, effects: {reportError: (error: unknown) => {throw error;}}};
  const item = {id: 'one', libraryId: 'books', title: 'One'}, props = {context, selection: new Map([['one', item]]), kind: 'ebook', current: () => true, onExit() {}, onEdit() {throw new Error('member edit');}};
  const view = f.hook.render(() => f.ReadSelectionBar(props));
  assert.equal(nodes(view).filter(node => node.props?.icon === 'i-edit').length, 0);
  const collection = nodes(view).find(node => node.props?.icon === 'i-layers');
  collection.props.onClick({currentTarget: {}});
  assert.equal(f.opened.length, 1); assert.equal(f.opened[0].options.canCreate, true);
  assert.deepEqual(f.opened[0].items, [{...item, kind: 'ebook'}]);
});
