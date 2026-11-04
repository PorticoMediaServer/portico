import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tree} from '../helpers/interaction-harness.mjs';
import {parseBrowseCapabilities, browseCapabilitiesPath} from '../../../packages/client-core/src/browse.ts';
import {libraryTabs, readingTabModel} from '../../../packages/client-core/src/presentation/library-tabs.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';

// #687 review: on a reading library's grid pivot BoundLibrary's CommittedLibrary owns the owner's scan offer, so the
// reading screen around it must not open a second one (each offer is one uncached get_admin_jobs read).
// Runs the actual LiveReadingLibrary with mocked ports, as periodical-capabilities.test.mjs does.
async function loadActual(h, reads) {
  const file = new URL('../../src/screens/browse/Library.route.tsx', import.meta.url);
  const result = transformSync(file.pathname, await readFile(file, 'utf8'), {jsx: {runtime: 'classic'}});
  if (result.errors.length) throw new Error(JSON.stringify(result.errors));
  class LibraryManagementService {
    constructor({api}) {this.api = api;}
    selectLibrary(libraryId) {reads.push(routePath('get_admin_jobs') + '?libraryId=' + libraryId); return new Promise(() => {});}
    getSnapshot() {return {selected: {phase: 'loading'}};}
    dispose() {}
  }
  const modules = {
    react: h.react, '@i18n': {defaultI18n: {t: key => key}},
    '@core/browse.ts': {parseBrowseCapabilities, browseCapabilitiesPath},
    '@core/presentation/library-tabs.ts': {readingTabModel, libraryTabs},
    '@core/library-management.ts': {LibraryManagementService},
    '../../shell/viewport': {useShellWidth: () => 1440}, './screen-preferences': {useScreenAppearance: () => ({libraryView: 'grid', showBackdrops: false})},
    '../read/useReadingMarkAll': {useReadingMarkAll: () => undefined},
    '../read': {useReadLibrarySummary: () => ({wash: undefined, counts: undefined, libraryCounts: []}), ReadLibraryEntry: 'ReadLibraryEntry', FacetPage: 'FacetPage'},
    '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'}, './Library': {LibraryIdentity: 'LibraryIdentity'},
    '../read/ReadPeriodicalEntry': {ReadPeriodicalEntry: 'ReadPeriodicalEntry'},
    '../pins/PinEditor': {usePinEditor: () => ({open: () => {}, element: null})},
    '../../router': {router: {navigate: async () => {}}},
    '../../route-registry': {defineWebScreen: value => value}, './route-search': {parseLibraryScreenSearch: value => value},
  };
  const inert = new Proxy(function () {}, {get: (_, key) => key === 'then' ? undefined : inert, apply: () => undefined});
  const accessible = new Proxy(modules, {get: (target, key) => key in target ? target[key] : inert});
  const code = result.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g, (_, names, source) => {
    let bind = names.trim().replace(/\bas\b/g, ':');
    if (!bind.startsWith('{')) {const comma = bind.indexOf(','); if (comma < 0) return `const ${bind} = modules[${JSON.stringify(source)}].default;`; return `const ${bind.slice(0, comma)} = modules[${JSON.stringify(source)}].default; const ${bind.slice(comma + 1).trim()} = modules[${JSON.stringify(source)}];`;}
    return `const ${bind} = modules[${JSON.stringify(source)}];`;
  }).replace(/export function LiveReadingLibrary/, 'function LiveReadingLibrary').replace(/export default /, 'const routeDefault = ');
  return new Function('modules', `const React = modules.react.default;\n${code}\nreturn {LiveReadingLibrary, BoundLibrary};`)(accessible);
}

async function mount(search) {
  const h = createRenderer(), reads = [];
  const {LiveReadingLibrary, BoundLibrary} = await loadActual(h, reads);
  const api = {request: () => new Promise(() => {}), requestRaw: () => new Promise(() => {})};
  const context = {api, artwork: {}, effects: {reportError: () => {}}, isCurrent: () => true, queries: {}, scope: {serverId: 'server', viewerId: 'viewer'},
    identity: {libraries: [], viewer: {role: 'owner', authority: 'local'}, capabilities: {manageServer: true}}};
  const props = {captured: {context: {}}, library: {id: 'library', kind: 'book'}, search, onMarked: () => {}, context, life: {current: () => true, api}};
  let rendered; for (let i = 0; i < 3; i++) rendered = tree(h.render(() => LiveReadingLibrary(props)));
  return {reads, rendered, BoundLibrary};
}

test('a reading library\'s Discover offers the owner Scan through exactly one get_admin_jobs read', async () => {
  const {reads} = await mount({view: 'discover'});
  assert.deepEqual(reads, [routePath('get_admin_jobs') + '?libraryId=library']);
});

test('on the grid pivot the reading screen makes no get_admin_jobs read of its own; CommittedLibrary\'s is the one', async () => {
  for (const search of [{view: 'browse'}, {view: 'discover', query: 'q'}]) {
    const {reads} = await mount(search);
    assert.deepEqual(reads, [], JSON.stringify(search));
  }
  // The grid path's single offer: CommittedLibrary calls the same hook, enabled.
  const route = await readFile(new URL('../../src/screens/browse/Library.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /const scan = useLibraryScan\(context, library, resources\.life\);/);
  assert.match(route, /const scan = useLibraryScan\(context, library, life, !\(search\.view === 'browse' \|\| search\.query\)\);/);
  assert.equal(route.match(/useLibraryScan\(context, library, /g).length, 2);
});
