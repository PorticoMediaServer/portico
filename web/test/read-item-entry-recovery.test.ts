/** Prepared offline: actual ReadItemEntry component callbacks through the published
 * web/test componentModule + createRenderer. Models callback composition, not native
 * React timing or paint. UNRUN. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, tree, textOf} from './helpers/interaction-harness.mjs';
import * as versionChoice from '../src/screens/read/read-version-choice.ts';

const epub = {sourceId: 'epub-source', format: 'epub' as const, size: 900, inventoryAvailable: true};
const pdf = {sourceId: 'pdf-source', format: 'pdf' as const, size: 1200, inventoryAvailable: true};

// ---------------------------------------------------------------------------
// Actual ReadItemEntry interaction regression. The published component harness
// runs the screen's own hooks/effects and expands its real element callbacks; it
// models callback composition, not native React timing or paint.
// ---------------------------------------------------------------------------

const Menu = (props: any) => props.open ? {type: 'Menu', props} : null;
const MenuGroup = (props: any) => ({type: 'MenuGroup', props});
const ReadBookHero = (props: any) => ({type: 'ReadBookHero',
  props: {...props, children: [props.children, props.actions, props.under].filter(Boolean)}});
const i18n = {t: (key: string) => key, bytes: (value: number) => `${value} B`, number: (value: number) => String(value),
  duration: (value: number) => `${value}s`, percent: (value: number) => `${value}%`};

function makeSession(sourceId: string) {
  const format = sourceId === pdf.sourceId ? 'pdf' : 'epub';
  return {manifest: {sourceId, revision: 'r1', format, size: 1000, canSaveFile: false, contents: [],
    metadata: {title: `Book ${sourceId}`, author: 'Author', coverUrl: '/cover.jpg', language: 'en'}},
    progress: async () => ({progress: 0})};
}

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {resolve = res; reject = rej;});
  return {promise, resolve, reject};
}

async function mountRead(options: {canGoBack?: boolean; phone?: boolean; open?: (sourceId: string) => Promise<any>} = {}) {
  const h = createRenderer(), calls: string[] = [], openCalls: string[] = [];
  let current = true, canGoBack = options.canGoBack !== false, phone = options.phone !== false;
  const location = (key: string) => ({href: '/item/book', pathname: '/item/book', search: {}, hash: '', state: {__TSR_key: key}});
  const router = {state: {location: location('key-1')},
    history: {canGoBack: () => canGoBack, back: () => calls.push('history.back')},
    navigate: async () => {}, subscribe: () => () => {}};
  let context: any = {
    isCurrent: () => current,
    effects: {back: () => calls.push('back'), openHome: () => calls.push('home'), reportError: (error: unknown) => calls.push(`error:${String(error)}`)},
    artwork: {foreground: (path: string) => path},
    session: () => ({viewer: {authority: 'local', role: 'owner', serverId: 's', accountId: 'a', profileId: 'p'}}),
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
  };
  const captured: any = {context, binding: {home: {effects: {reportError: context.effects.reportError}}, scopeKey: 'scope'}};
  let screenContext = context;
  let eligibility = {itemId: 'book', versions: [epub, pdf], chosenSourceId: 'epub-source'};
  const bindingsFactory = () => ({
    client: {},
    authority: {isCurrent: () => true, subscribe: () => () => {}},
    open: async ({sourceId}: {sourceId: string}) => {openCalls.push(sourceId); return options.open ? options.open(sourceId) : makeSession(sourceId);},
    close: async () => {}, dispose: async () => {},
  });
  const app = await componentModule(new URL('../src/screens/read/ReadItemEntry.tsx', import.meta.url), {
    react: h.react,
    '@tanstack/react-router': {useLocation: () => router.state.location},
    '@i18n': {defaultI18n: i18n},
    '@design': {generatedCoverTint: () => ({first: '#111', second: '#222'})},
    '@core/index.ts': {ApiError: class ApiError extends Error {}},
    '@core/reading-reread.ts': {rereadReadingSource: async () => {}},
    '@core/metadata-repair.ts': {validateRepair: () => true},
    '../../../../packages/contracts/src/routes.ts': {encodedRoutePath: (path: string) => path},
    '@portico/reader-core': {createReadingPublication: async () => ({contentsTarget: (index: number) => ({index}), dispose() {}})},
    './contents-handoff': {stageContentsTarget: () => {}, retireContentsTarget: () => {}},
    '../../kit/reader/ReaderPanel': {ReaderContents: 'ReaderContents'},
    './save-file': {saveReadingFile: async () => {}},
    '../../route-registry-view': {useProtectedWebScreenContext: () => captured},
    '../../router': {router},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/controls': {Button: 'Button', ResumeButton: 'ResumeButton'},
    '../../kit/overlays': {Menu, MenuGroup, MenuItem: 'MenuItem'},
    '../../kit/overlays/shared': {usePhone: () => phone},
    '../detail/context': {runScreenCommand: (_context: any, command: () => void) => command()},
    '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'},
    '../../kit/reader/ReadBookHero': {ReadBookFacts: 'ReadBookFacts', ReadBookHero, ReadBookSections: 'ReadBookSections'},
    '../../kit/shell': {ServerMark: 'ServerMark'},
    '../browse/screen-context': {useLibraryScreenContext: () => screenContext},
    './ReadMetadata': {ReadMetadata: 'ReadMetadata'},
    './bindings': {createReadBindings: bindingsFactory},
    './navigation': {readDestination: (itemId: string) => `/read/${itemId}`},
    './read-version-choice': versionChoice,
    './read-item.module.css': {default: {}},
    './useReadingFavorite': {useReadingFavorite: () => undefined},
    './ReadSavedAction': {ReadSavedAction: 'ReadSavedAction'},
    './ReadMarkAction': {ReadMarkAction: 'ReadMarkAction'},
  });
  const flat = () => tree(h.render(() => app.ReadItemEntry({itemId: 'book', eligibility})));
  const settle = async (rounds = 12) => {for (let i = 0; i < rounds; i++) {await Promise.resolve(); flat();}};
  flat();
  return {
    flat, settle, calls, openCalls, router, context, captured,
    setLocation(key: string) {router.state.location = location(key);},
    setEligibility(next: any) {eligibility = next;},
    setContext(next: any) {context = next; captured.context = next; screenContext = next;},
    retire() {current = false;},
    setGoBack(value: boolean) {canGoBack = value;},
  };
}

const backButton = (f: any) => all(f.flat(), 'Button').find((node: any) => node.props['aria-label'] === 'next.states.back');
const versionButton = (f: any) => all(f.flat(), 'Button').find((node: any) => node.props['aria-label'] === 'next.reader.version');
const versionItem = (f: any, format: string) => all(f.flat(), 'MenuItem').find((node: any) => textOf(node) === format);

test('actual ReadItemEntry keeps one phone Back through loading, failure, ready and direct entry', async () => {
  const loading = await mountRead({open: () => new Promise(() => {})});
  await loading.settle(2);
  backButton(loading).props.onClick();
  assert.ok(loading.calls.includes('back'), 'loading Back reaches the captured back effect');

  const failure = await mountRead({open: async sourceId => {if (sourceId === epub.sourceId) throw new Error('rejected'); return makeSession(sourceId);}});
  await failure.settle();
  assert.equal(failure.calls.includes('back'), false);
  backButton(failure).props.onClick();
  assert.ok(failure.calls.includes('back'), 'failure Back reaches the captured back effect');

  const ready = await mountRead({open: async sourceId => makeSession(sourceId)});
  await ready.settle();
  backButton(ready).props.onClick();
  assert.ok(ready.calls.includes('back'), 'ready Back reaches the captured back effect');

  const direct = await mountRead({canGoBack: false, open: async sourceId => makeSession(sourceId)});
  await direct.settle();
  backButton(direct).props.onClick();
  assert.ok(direct.calls.includes('home'), 'direct entry uses the captured Home effect');
  assert.equal(direct.calls.includes('back'), false);
});

test('actual ReadItemEntry Back is inert after the same-book entry replacement, new Back works', async () => {
  const f = await mountRead({open: async sourceId => makeSession(sourceId)});
  await f.settle();
  const oldBack = backButton(f);
  const before = f.calls.length;
  f.setLocation('key-2'); f.flat(); await f.settle();
  oldBack.props.onClick();
  assert.equal(f.calls.length, before, 'old Back did nothing after the new entry');
  backButton(f).props.onClick();
  assert.ok(f.calls.slice(before).includes('back'), 'the new entry Back works');
});

test('actual ReadItemEntry failure chooser selects B and only B publishes', async () => {
  const f = await mountRead({open: async sourceId => {if (sourceId === epub.sourceId) throw new Error('default rejected'); return makeSession(sourceId);}});
  await f.settle();
  versionButton(f).props.onClick(); f.flat();
  versionItem(f, 'PDF').props.onSelect(); f.flat(); await f.settle();
  assert.deepEqual(f.openCalls, [epub.sourceId, pdf.sourceId]);
  const hero = one(f.flat(), 'ReadBookHero');
  assert.ok(String(hero.props.title).includes(pdf.sourceId), 'the ready hero is B');
  assert.equal(String(hero.props.title).includes(epub.sourceId), false, 'A never published');
});

test('actual ReadItemEntry ignores a stale A response after B opens', async () => {
  const a = deferred<any>();
  const f = await mountRead({open: sourceId => sourceId === epub.sourceId ? a.promise : Promise.resolve(makeSession(sourceId))});
  await f.settle(3);
  assert.equal(versionButton(f), undefined, 'a pending read has no inline version control');
  all(f.flat(), 'Button').find(node => node.props['aria-label'] === 'next.controls.more')!.props.onClick(); f.flat();
  versionItem(f, 'PDF').props.onSelect(); f.flat(); await f.settle();
  assert.deepEqual(f.openCalls, [epub.sourceId, pdf.sourceId]);
  a.resolve(makeSession(epub.sourceId)); await f.settle();
  const hero = one(f.flat(), 'ReadBookHero');
  assert.ok(String(hero.props.title).includes(pdf.sourceId), 'stale A publication cannot replace B');
});

test('actual ReadItemEntry refuses an old menu after entry, list and context replacement', async () => {
  const f = await mountRead({open: async sourceId => {if (sourceId === epub.sourceId) throw new Error('default rejected'); return makeSession(sourceId);}});
  await f.settle();
  versionButton(f).props.onClick(); f.flat();
  const oldEntryPdf = versionItem(f, 'PDF');
  f.setLocation('key-2'); f.flat(); await f.settle();
  oldEntryPdf.props.onSelect(); f.flat();
  assert.equal(f.openCalls.includes(pdf.sourceId), false, 'old entry menu cannot open B');
  const oldListPdf = versionItem(f, 'PDF');
  f.setEligibility({itemId: 'book', versions: [epub, pdf], chosenSourceId: 'epub-source'}); f.flat();
  oldListPdf.props.onSelect(); f.flat();
  assert.equal(f.openCalls.includes(pdf.sourceId), false, 'old list menu cannot open B');
  const oldContextPdf = versionItem(f, 'PDF');
  f.setContext({...f.context, effects: {...f.context.effects, back: () => f.calls.push('back2'), openHome: () => f.calls.push('home2')}});
  f.flat(); await f.settle();
  oldContextPdf.props.onSelect(); f.flat();
  assert.equal(f.openCalls.includes(pdf.sourceId), false, 'old context menu cannot open B');
  versionItem(f, 'PDF').props.onSelect(); f.flat(); await f.settle();
  assert.ok(f.openCalls.includes(pdf.sourceId), 'the current entry/list/context menu opens B');
});

test('actual ReadItemEntry refuses a source write when an abort listener replaces the entry', async t => {
  const realAbort = globalThis.AbortController, controllers: AbortController[] = [];
  class RecordingAbort extends realAbort {constructor() {super(); controllers.push(this);}}
  globalThis.AbortController = RecordingAbort as any;
  t.after(() => {globalThis.AbortController = realAbort;});
  const f = await mountRead({open: async sourceId => {if (sourceId === epub.sourceId) throw new Error('default rejected'); return makeSession(sourceId);}});
  await f.settle();
  assert.ok(controllers.length >= 1);
  controllers[0]!.signal.addEventListener('abort', () => f.setLocation('key-abort'));
  versionButton(f).props.onClick(); f.flat();
  versionItem(f, 'PDF').props.onSelect(); f.flat();
  assert.equal(f.openCalls.includes(pdf.sourceId), false, 'the post-retire route witness refused the write');
});

test('actual ReadItemEntry refuses the old write when an abort listener starts a successor', async t => {
  const realAbort = globalThis.AbortController, controllers: AbortController[] = [];
  class RecordingAbort extends realAbort {constructor() {super(); controllers.push(this);}}
  globalThis.AbortController = RecordingAbort as any;
  t.after(() => {globalThis.AbortController = realAbort;});
  const f = await mountRead({open: async sourceId => {if (sourceId === epub.sourceId) throw new Error('default rejected'); return makeSession(sourceId);}});
  await f.settle();
  const successorContext = {...f.context};
  controllers[0]!.signal.addEventListener('abort', () => {f.setContext(successorContext); f.flat();});
  versionButton(f).props.onClick(); f.flat();
  versionItem(f, 'PDF').props.onSelect(); f.flat();
  assert.equal(f.openCalls.includes(pdf.sourceId), false, 'the successor attempt blocked the old source write');
  assert.ok(f.openCalls.filter(source => source === epub.sourceId).length >= 2, 'a fresh attempt opened for the successor lifetime');
});
