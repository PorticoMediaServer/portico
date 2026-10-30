import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tree, one} from '../helpers/interaction-harness.mjs';
import {parseBrowseCapabilities, browseCapabilitiesPath} from '../../../packages/client-core/src/browse.ts';
import {libraryTabs, readingTabModel} from '../../../packages/client-core/src/presentation/library-tabs.ts';
import {loadFacetPage} from '../../src/screens/read/facets.ts';
import {routePattern} from '../../../packages/contracts/src/routes.ts';

// Execute the actual LiveReadingLibrary function with mocked ports; the small
// hook harness models component wiring, not React scheduling or browser paint.
async function loadActual(h, navigations) {
  const file = new URL('../../src/screens/browse/Library.route.tsx', import.meta.url);
  const result = transformSync(file.pathname, await readFile(file, 'utf8'), {jsx: {runtime: 'classic'}});
  if (result.errors.length) throw new Error(JSON.stringify(result.errors));
  const modules = {
    react: h.react, '@i18n': {defaultI18n: {t: key => key}},
    '@core/browse.ts': {parseBrowseCapabilities, browseCapabilitiesPath},
    '@core/presentation/library-tabs.ts': {readingTabModel, libraryTabs},
    '../../shell/viewport': {useShellWidth: () => 1440}, './screen-preferences': {useScreenAppearance: () => ({libraryView: 'grid', showBackdrops: false})},
    '../read/useReadingMarkAll': {useReadingMarkAll: () => undefined},
    '../read': {useReadLibrarySummary: () => ({wash: undefined, counts: undefined, libraryCounts: []}), ReadLibraryEntry: 'ReadLibraryEntry', FacetPage: 'FacetPage'},
    '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'}, './Library': {LibraryIdentity: 'LibraryIdentity'},
    '../read/ReadPeriodicalEntry': {ReadPeriodicalEntry: 'ReadPeriodicalEntry'},
    '../../router': {router: {navigate: async value => {navigations.push(value);}}},
    '../../route-registry': {defineWebScreen: value => value}, './route-search': {parseLibraryScreenSearch: value => value},
  };
  const inert = new Proxy(function () {}, {get: (_, key) => key === 'then' ? undefined : inert, apply: () => undefined});
  const accessible = new Proxy(modules, {get: (target, key) => key in target ? target[key] : inert});
  const code = result.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g, (_, names, source) => {
    let bind = names.trim().replace(/\bas\b/g, ':');
    if (!bind.startsWith('{')) {const comma = bind.indexOf(','); if (comma < 0) return `const ${bind} = modules[${JSON.stringify(source)}].default;`; return `const ${bind.slice(0, comma)} = modules[${JSON.stringify(source)}].default; const ${bind.slice(comma + 1)} = modules[${JSON.stringify(source)}];`;}
    return `const ${bind} = modules[${JSON.stringify(source)}];`;
  }).replace(/export function LiveReadingLibrary/, 'function LiveReadingLibrary').replace(/export default /, 'const routeDefault = ');
  return new Function('modules', `const React = modules.react.default;\n${code}\nreturn LiveReadingLibrary;`)(accessible);
}
function deferred() {let resolve, reject; const promise = new Promise((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};}
function caps(id = 'library', kind = 'book', pivot = 'magazines') {
  return {library: {id, kind, name: 'Library', defaultView: 'discover'}, pivots: [
    {id: 'discover', labelKey: 'library.discover', entityKinds: [], defaultSort: [], supportedViews: [], browsable: false},
    {id: pivot, labelKey: `library.${pivot}`, entityKinds: [], defaultSort: [], supportedViews: [], browsable: false,
      facetSource: {endpoint: routePattern('get_reading_periodical_groups'), field: 'publicationType'}},
    {id: 'authors', labelKey: 'library.tab.authors', entityKinds: [], defaultSort: [], supportedViews: [], browsable: false,
      facetSource: {endpoint: routePattern('get_library_facets'), field: 'author'}},
  ], resolvedPivot: null, fields: [], sorts: [], quickFilters: [], queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65536, maximumSorts: 3, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1800000}};
}
async function fixture({strict = false, holdPassive = false} = {}) {
  const h = createRenderer(), calls = [], errors = [], passive = [], navigations = [];
  if (strict) {const effect = h.react.useLayoutEffect; h.react.useLayoutEffect = (run, deps) => effect(() => {const first = run(); first?.(); return run();}, deps);}
  // This harness flushes effects together. Holding passive setups explicitly models
  // the committed-layout-before-passive window, not React scheduling itself.
  if (holdPassive) {const effect = h.react.useEffect; h.react.useEffect = (run, deps) => effect(() => {passive.push(run);}, deps);}
  const Component = await loadActual(h, navigations);
  let active = true, library = {id: 'library', kind: 'book'}, search = {view: 'series', pivot: 'magazines'};
  const rawCalls = [];
  const api = {
    request(path, method, body, signal) {const call = {...deferred(), path, method, body, signal}; calls.push(call); return call.promise;},
    requestRaw(path, method, options) {const call = {...deferred(), path, method, options}; rawCalls.push(call); return call.promise;},
  };
  let captured = {context: {}}, context = {api, artwork: {}, identity: {libraries: []}, effects: {reportError: error => errors.push(error)}, isCurrent: () => active};
  let life = makeLife();
  function makeLife() {return {current: () => active, api};}
  const render = () => tree(h.render(() => Component({captured, library, search, onMarked: () => {}, context, life})));
  const settle = () => {let result; for (let i = 0; i < 3; i++) result = render(); return result;};
  return {calls, rawCalls, errors, navigations, settle, retry: () => one(settle(), 'StateView').props.primary.onPress,
    replace: (id = 'library', pivot = 'magazines') => {library = {id, kind: 'book'}; search = {view: 'series', pivot}; life = makeLife(); settle();},
    replaceContext: () => {captured = {context: {}}; context = {...context}; life = makeLife(); settle();},
    openSeries: entityId => {search = {...search, entityId}; return settle();},
    authors: () => {search = {view: 'authors'}; return settle();},
    discover: () => {search = {view: 'discover'}; life = makeLife(); return settle();},
    retire: () => {active = false;}, passive};
}

test('capability failure offers one retry, then mounts the exact periodical', async () => {
  const f = await fixture(); assert.ok(f.settle().some(node => node.type === 'Skeleton')); assert.equal(f.calls.length, 1);
  f.calls[0].reject(new Error('offline')); await Promise.resolve(); await Promise.resolve();
  assert.ok(f.settle().some(node => node.type === 'LibraryIdentity')); const retry = f.retry(); retry(); retry(); f.settle();
  assert.equal(f.calls.length, 2); assert.equal(f.calls[0].signal.aborted, true);
  f.calls[1].resolve(caps()); await Promise.resolve(); await Promise.resolve();
  const periodical = one(f.settle(), 'ReadPeriodicalEntry'); assert.equal(periodical.props.libraryId, 'library'); assert.equal(periodical.props.kind, 'magazine');
});

test('invalid or mismatched capabilities are recoverable, stale replies never replace a new opening', async () => {
  for (const bad of [null, caps('other'), caps('library', 'comic')]) {
    const f = await fixture(); f.settle(); f.calls[0].resolve(bad); await Promise.resolve(); await Promise.resolve();
    assert.ok(f.retry());
  }
  const f = await fixture(); f.settle(); const first = f.calls[0]; f.replace('new-library', 'newspapers');
  assert.equal(first.signal.aborted, true); first.resolve(caps()); await Promise.resolve(); await Promise.resolve();
  f.calls[1].resolve(caps('new-library', 'book', 'newspapers')); await Promise.resolve(); await Promise.resolve();
  const current = one(f.settle(), 'ReadPeriodicalEntry'); assert.equal(current.props.libraryId, 'new-library'); assert.equal(current.props.kind, 'newspaper');
});

test('retired lifetime refuses late failure and success', async () => {
  for (const fail of [false, true]) {
    const f = await fixture(); f.settle(); f.retire();
    if (fail) f.calls[0].reject(new Error('late')); else f.calls[0].resolve(caps());
    await Promise.resolve(); await Promise.resolve(); assert.deepEqual(f.errors, []);
    assert.equal(f.settle().some(node => node.type === 'ReadPeriodicalEntry'), false);
  }
});


test('effect replay withdraws original request before admitting the replacement', async () => {
  const f = await fixture({strict: true}); f.settle(); assert.equal(f.calls.length, 2); assert.equal(f.calls[0].signal.aborted, true);
  f.calls[0].reject(new Error('retired')); f.calls[1].resolve(caps()); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(f.errors, []); assert.equal(one(f.settle(), 'ReadPeriodicalEntry').props.kind, 'magazine');
});

test('a stale rejected request cannot replace an already ready successor', async () => {
  const f = await fixture(); f.settle(); const old = f.calls[0]; f.replace('new-library', 'newspapers');
  f.calls[1].resolve(caps('new-library', 'book', 'newspapers')); await Promise.resolve(); await Promise.resolve();
  old.reject(new Error('late')); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(f.errors, []); assert.equal(one(f.settle(), 'ReadPeriodicalEntry').props.libraryId, 'new-library');
});


test('committed replacement fences saved retry before passive effects and ignores a late result', async () => {
  const f = await fixture({holdPassive: true}); f.settle();
  f.calls[0].reject(new Error('offline')); await Promise.resolve(); await Promise.resolve();
  const retry = f.retry(); f.replace('successor', 'newspapers');
  const successor = f.calls[1]; retry(); assert.equal(f.calls.length, 2); assert.equal(successor.signal.aborted, false);
  successor.resolve(caps('successor', 'book', 'newspapers')); await Promise.resolve(); await Promise.resolve();
  assert.equal(one(f.settle(), 'ReadPeriodicalEntry').props.libraryId, 'successor');
  const g = await fixture({holdPassive: true}); g.settle(); const old = g.calls[0]; g.replaceContext();
  assert.equal(old.signal.aborted, true); old.resolve(caps()); await Promise.resolve(); await Promise.resolve();
  assert.equal(g.settle().some(node => node.type === 'ReadPeriodicalEntry'), false);
  g.calls[1].resolve(caps()); await Promise.resolve(); await Promise.resolve();
  assert.equal(one(g.settle(), 'ReadPeriodicalEntry').props.libraryId, 'library');
});

test('abort reentry cannot let an old retry clear or replace the successor', async () => {
  const f = await fixture({holdPassive: true}); f.settle(); f.calls[0].reject(new Error('offline'));
  await Promise.resolve(); await Promise.resolve(); const retry = f.retry();
  f.calls[0].signal.addEventListener('abort', () => f.replace('successor', 'newspapers'), {once: true});
  retry(); assert.equal(f.calls.length, 2); assert.equal(f.calls[1].signal.aborted, false);
  f.calls[1].resolve(caps('successor', 'book', 'newspapers')); await Promise.resolve(); await Promise.resolve();
  assert.equal(one(f.settle(), 'ReadPeriodicalEntry').props.libraryId, 'successor');
});

test('leaving a periodical opening preserves the nonperiodical fallback', async () => {
  const f = await fixture(); f.settle(); const old = f.calls[0];
  const next = f.discover(); assert.equal(old.signal.aborted, true);
  assert.equal(one(next, 'ReadLibraryEntry').props.view, 'discover');
  old.resolve(caps()); await Promise.resolve(); await Promise.resolve();
  assert.equal(one(f.settle(), 'ReadLibraryEntry').props.view, 'discover');
});


test('periodical drilldown keeps its series and tab actions use the published pivots', async () => {
  const f = await fixture(); f.settle(); f.calls[0].resolve(caps());
  await Promise.resolve(); await Promise.resolve();
  const nodes = f.openSeries('series-opaque');
  const periodical = one(nodes, 'ReadPeriodicalEntry');
  assert.equal(periodical.props.libraryId, 'library');
  assert.equal(periodical.props.kind, 'magazine');
  assert.equal(periodical.props.seriesId, 'series-opaque');
  const header = one(nodes, 'LibraryIdentity');
  header.props.onPivot('magazines');
  assert.deepEqual(f.navigations, [{to: '/library/$libraryId', params: {libraryId: 'library'}, search: {view: 'series', pivot: 'magazines'}}]);
  f.retire(); header.props.onPivot('authors');
  assert.equal(f.navigations.length, 1, 'a retired screen cannot navigate');
});

test('the sibling facet receives the raw API port and reads its wire envelope', async () => {
  const f = await fixture(); f.settle(); f.calls[0].resolve(caps());
  await Promise.resolve(); await Promise.resolve();
  const facet = one(f.authors(), 'FacetPage');
  const signal = new AbortController().signal;
  const work = loadFacetPage(facet.props.request, {libraryId: facet.props.libraryId, field: facet.props.field, pivot: facet.props.pivot}, signal);
  assert.equal(f.calls.length, 1, 'facet reads use the raw port, not the body-only capability port');
  assert.equal(f.rawCalls.length, 1);
  const call = f.rawCalls[0];
  assert.match(call.path, /\/v1\/libraries\/library\/facets\?/);
  assert.equal(call.method, 'GET');
  assert.equal(call.options.signal.aborted, false);
  assert.ok(call.options.signal instanceof AbortSignal);
  const body = {variant: 'page', field: 'author', total: 1, totalIsLowerBound: false, values: [{value: 'Ursula K. Le Guin', label: 'Ursula K. Le Guin', count: 2, countIsLowerBound: true}]};
  call.resolve({status: 200, headers: {}, body});
  const page = await work;
  assert.deepEqual(page.values, body.values);
  assert.equal(page.total, 1);
  assert.equal(facet.props.request.isCurrent(), true);
  f.retire();
  await assert.rejects(loadFacetPage(facet.props.request, {libraryId: 'library', field: 'author', pivot: 'books'}, signal), {name: 'AbortError'});
  assert.equal(f.rawCalls.length, 1, 'a retired route cannot start another raw read');
});
