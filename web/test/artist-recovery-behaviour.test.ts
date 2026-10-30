/** Artist recovery uses actual component hooks and the original opening, with controlled services. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, tree} from './helpers/interaction-harness.mjs';
import {screenLifetime, runScreenCommand} from '../src/screens/detail/context.ts';

/** Load the WHOLE actual route module; expose its private component only to this fixture. */
async function actualRouteBack(context: any, router: any, libraryId: string) {
  const h = createRenderer(), react = {...h.react, useEffect() {}, useLayoutEffect() {}};
  react.default = react;
  const file = new URL('../src/screens/browse/Library.route.tsx', import.meta.url);
  const transformed = transformSync(file.pathname, await readFile(file, 'utf8'), {jsx: {runtime: 'classic'}});
  if (transformed.errors.length) throw new Error(JSON.stringify(transformed.errors));
  const inert: any = new Proxy(function () {}, {get: (_target, key) => key === 'then' ? undefined : inert, apply: () => undefined});
  const modules: any = new Proxy({react, '../../router': {router}, '@i18n': {defaultI18n: {t: (key: string) => key, region: {formatLocale: 'en'}}},
    '../detail/Artist': {ArtistScreen: 'ArtistScreen'}, './reading-library': {isReadingLibrary: () => false}, '../read': {useReadLibrarySummary: () => ({libraryCounts: []})}},
    {get: (target: any, key: string) => key in target ? target[key] : inert});
  const code = transformed.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g, (_match: string, names: string, source: string) => {
    const binding = names.trim().replace(/\bas\b/g, ':');
    if (binding.startsWith('{')) return `const ${binding} = modules[${JSON.stringify(source)}];`;
    const comma = binding.indexOf(',');
    return comma < 0 ? `const ${binding} = modules[${JSON.stringify(source)}].default;` : `const ${binding.slice(0, comma)} = modules[${JSON.stringify(source)}].default; const ${binding.slice(comma + 1)} = modules[${JSON.stringify(source)}];`;
  }).replace(/export default [\s\S]*?;\s*$/, '');
  const {CommittedLibrary} = new Function('modules', 'const React = modules.react.default;\n' + code + '\nreturn {CommittedLibrary};')(modules);
  const controller = {subscribe: () => () => {}, getSnapshot: () => ({queryRevision: 1}), canSaveView: () => false};
  const resources = {controller, life: {current: () => true, api: {}, signal: () => new AbortController().signal}};
  const frame = {context, appearance: {libraryView: 'grid'}, width: 1200, size: 'medium', tabs: [], view: 'artist'};
  const node = h.render(() => CommittedLibrary({captured: {context: {}, binding: {}}, library: {id: libraryId, kind: 'music', name: 'Music'}, search: {entityId: 'one'}, frame, resources, href: router.state.location.href}));
  const artist = all(tree(node), 'ArtistScreen')[0];
  assert.ok(artist, 'the actual CommittedLibrary supplies ArtistScreen');
  return artist.props.onBack as () => void;
}

test('Artist Back and retry keep their committed entry and never adopt successor services', async () => {
  const h = createRenderer(), services: any[] = [], backs: string[] = [], reports: unknown[] = [];
  const react = {...h.react};
  react.useLayoutEffect = (effect: () => any, deps: any[]) => h.react.useLayoutEffect(() => {let cleanup = effect(); cleanup?.(); cleanup = effect(); return () => cleanup?.();}, deps);
  let location: any = {href: '/library?artist=one'}, contextCurrent = true;
  const navigations: any[] = [];
  const router = {state: {location}, navigate: (target: any) => {navigations.push(target); return Promise.resolve();}};
  class Service {
    snapshot: any = {phase: 'loading', projection: null, appended: [], error: null};
    cancelled = 0; disposed = 0; selections: any[] = []; pages: string[] = []; resolve!: () => void; reject!: (error: unknown) => void;
    constructor() {services.push(this);}
    subscribe = () => () => {};
    getSnapshot = () => this.snapshot;
    select = (selector: any) => {this.selections.push(selector); return new Promise<void>((resolve, reject) => {this.resolve = resolve; this.reject = reject;});};
    next = async (section: string) => {this.pages.push(section); this.snapshot = {...this.snapshot, phase: 'loading'};};
    cancel = () => {this.cancelled++;}; dispose = () => {this.disposed++;};
  }
  const t = (key: string) => key;
  const identity = await componentModule(new URL('../src/screens/detail/context.ts', import.meta.url), {react: h.react});
  const problem = await componentModule(new URL('../src/screens/detail/ScreenProblem.tsx', import.meta.url), {
    react: h.react, './context': identity, '@core/presentation/errors.ts': {presentError: (error: any) => ({category: error?.code === 'not-found' ? 'not-found' : 'failed', action: 'retry'})},
    '@core/presentation/service-text.ts': {serviceI18n: () => ({})}, '../../kit/states': {StateLayout: 'StateLayout'}, './text': {t},
  });
  const app = await componentModule(new URL('../src/screens/detail/Artist.tsx', import.meta.url), {
    react, '@tanstack/react-router': {useLocation: () => location}, '../../router': {router},
    '@i18n': {defaultI18n: {region: {formatLocale: 'en'}}}, './text': {t},
    '@core/library-content.ts': {LibraryContentService: Service, readListeningSimilar: () => Promise.resolve({entries: []})},
    '@core/freshness-hub.ts': {dependsOn: {libraries: 'libraries'}}, '../../platform/freshness': {useFreshDocument() {}},
    '../../kit/controls': {Button: 'Button'}, '../../kit/artwork': {AmbientArt: 'AmbientArt', CircleArt: 'CircleArt'},
    '../../kit/layout': {Lockup: 'Lockup'}, '../../kit/title/TitleParts': {TitleFactsLine: 'TitleFactsLine', TitleProse: 'TitleProse'},
    '../../kit/browse': {TrackTable: 'TrackTable'}, '../../kit/overlays': {Menu: 'Menu'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../shell/viewport': {useShellWidth: () => 1440}, './context': {...identity, screenLifetime, runScreenCommand},
    '../detail/context': identity,
    './ScreenProblem': {ScreenProblem: problem.ScreenProblem}, './Detail': {TitleRow: 'TitleRow'}, './model': {entrySubject: () => ({})},
    './MusicEntryMenu': {MusicEntryMenu: 'MusicEntryMenu'}, './MusicLoading': {MusicDetailLoading: 'MusicDetailLoading'}, './artist.module.css': {default: {}},
  });
  const context: any = {scope: {serverId: 's', viewerId: 'v'}, isCurrent: () => contextCurrent, api: {}, artwork: {ambient: () => undefined, foreground: () => undefined}, effects: {reportError: (e: unknown) => reports.push(e), back: () => backs.push('one')}};
  let artistId = 'one', libraryId = 'lib';
  let routeBack = await actualRouteBack(context, router, libraryId);
  const render = () => tree(h.render(() => app.ArtistScreen({context, artistId, libraryId, onBack: routeBack})));
  const back = (view: any[]) => all(view, 'Button').find((node: any) => node.props['aria-label'] === 'next.states.back');
  const settle = async () => {await Promise.resolve(); await Promise.resolve(); return render();};
  let view = render(); assert.equal(back(view).props.disabled, true); view = render(); back(view).props.onClick(); assert.deepEqual(backs, ['one']);
  assert.equal(services[0].disposed, 1); const first = services[1]; assert.deepEqual(first.selections[0], {libraryId: 'lib', view: 'artist', entityId: 'one'});
  first.snapshot = {phase: 'error', projection: null, appended: [], error: {code: 'temporary'}}; first.reject(first.snapshot.error); view = await settle();
  const failure = all(view, 'StateLayout')[0]; assert.equal(failure.props.title, 'next.states.pageFailed');
  const oldBack = back(view).props.onClick, retry = failure.props.primary.onPress;
  const failedSnapshot = first.snapshot; first.snapshot = {phase: 'ready', projection: {}, appended: [], error: null}; retry(); assert.equal(services.length, 2); first.snapshot = failedSnapshot;
  retry(); retry(); oldBack(); view = render(); view = render(); assert.equal(services.length, 4); assert.equal(first.cancelled, 1); assert.equal(first.disposed, 1);
  assert.deepEqual(services[3].selections[0], {libraryId: 'lib', view: 'artist', entityId: 'one'}); back(view).props.onClick(); assert.deepEqual(backs, ['one', 'one']);
  const second = services[3]; second.snapshot = {phase: 'error', projection: null, appended: [], error: {code: 'not-found'}}; second.reject(second.snapshot.error); view = await settle();
  assert.equal(all(view, 'StateLayout')[0].props.title, 'next.states.notFound'); const staleBack = back(view).props.onClick;
  location = {href: location.href}; router.state.location = location; view = render(); staleBack(); view = render();
  assert.equal(services.length, 6); assert.deepEqual(backs, ['one', 'one']);
  contextCurrent = false; back(view).props.onClick(); assert.equal(backs.length, 2);
  contextCurrent = true;
  const beforeReentry = back(view).props.onClick;
  context.isCurrent = () => {context.isCurrent = () => true; location = {href: location.href}; router.state.location = location; return true;};
  beforeReentry(); assert.equal(backs.length, 2, 'context reentry cannot spend the retired Back');
  const retired = services[5]; view = render(); view = render();
  retired.reject(new Error('late retired failure')); view = await settle();
  assert.equal(reports.length, 2, 'a retired service cannot report into its successor');
  const oldArtistBack = back(view).props.onClick; artistId = 'two'; view = render(); oldArtistBack(); view = render(); assert.equal(backs.length, 2);
  const oldLibraryBack = back(view).props.onClick; libraryId = 'other'; view = render(); oldLibraryBack(); view = render(); assert.equal(backs.length, 2);
  const oldFallback = back(view).props.onClick; context.effects.back = undefined; routeBack = await actualRouteBack(context, router, libraryId);
  location = {href: location.href}; router.state.location = location; view = render(); oldFallback(); view = render(); assert.equal(navigations.length, 0);
  back(view).props.onClick(); assert.deepEqual(navigations[0], {to: '/library/$libraryId', params: {libraryId: 'other'}, search: {}});
  const staleFallback = back(view).props.onClick; location = {href: location.href}; router.state.location = location; view = render(); staleFallback(); assert.equal(navigations.length, 1);
  view = render();
  const refresh = services.at(-1); refresh.snapshot = {phase: 'refresh-required', projection: null, appended: [], error: {code: 'temporary'}}; refresh.resolve(); view = await settle();
  assert.equal(all(view, 'StateLayout')[0].props.title, 'next.states.pageFailed'); back(view).props.onClick();
  all(view, 'StateLayout')[0].props.primary.onPress(); view = render(); view = render();
  const mismatch = services.at(-1); mismatch.snapshot = {phase: 'ready', projection: {entity: {id: 'wrong', kind: 'artist'}, scope: {libraryId, view: 'artist', entityId: artistId}, revision: {catalog: 1, viewer: 1}}, appended: [], error: null}; mismatch.resolve(); view = await settle();
  assert.equal(all(view, 'StateLayout')[0].props.title, 'next.states.pageFailed');
  all(view, 'StateLayout')[0].props.primary.onPress(); view = render(); view = render();
  const wrongKind = services.at(-1); wrongKind.snapshot = {phase: 'ready', projection: {entity: {id: artistId, kind: 'album', title: 'Wrong kind'}, scope: {libraryId, view: 'artist', entityId: artistId}, revision: {catalog: 1, viewer: 1}}, appended: [], error: null}; wrongKind.resolve(); view = await settle();
  assert.equal(all(view, 'StateLayout')[0].props.title, 'next.states.pageFailed');
  assert.equal(all(view, 'span').some((node: any) => node.props.children?.includes?.('Wrong kind')), false);
  all(view, 'StateLayout')[0].props.primary.onPress(); view = render(); view = render();
  const late = services.at(-1), reportCount = reports.length; location = {href: location.href}; router.state.location = location; view = render(); view = render();
  late.snapshot = {phase: 'ready', projection: {entity: {id: artistId, kind: 'artist'}, scope: {libraryId, view: 'artist', entityId: artistId}, revision: {catalog: 1, viewer: 1}}, appended: [], error: null}; late.resolve(); view = await settle();
  assert.equal(reports.length, reportCount, 'retired success cannot report or publish to successor');
  assert.equal(all(view, 'Lockup').length, 0, 'late original artist cannot render in successor');
  const fresh = services.at(-1); fresh.snapshot = {phase: 'ready', projection: {entity: {id: artistId, kind: 'artist', title: 'Real artist'}, scope: {libraryId, view: 'artist', entityId: artistId}, revision: {catalog: 1, viewer: 1}, sections: []}, appended: [], error: null}; fresh.resolve(); view = await settle();
  assert.equal(all(view, 'Lockup')[0].props.title, 'Real artist'); assert.ok(back(view));
  assert.ok(all(view, 'span').some((node: any) => node.props.children?.includes?.('Real artist')), 'bar shows the admitted artist title');
  const album = {id: 'release', kind: 'album', libraryId, title: 'Release'}, albums = {id: 'items', entries: [album], nextCursor: 'next-release', totalCount: 2};
  fresh.snapshot = {...fresh.snapshot, projection: {...fresh.snapshot.projection, entity: {...fresh.snapshot.projection.entity, overview: 'Stored biography', overviewSource: 'Provider'}, sections: [albums]}, appended: [albums]};
  view = render();
  assert.equal(all(view, 'TitleProse')[0].props.markup, 'Stored biography');
  const row = all(view, 'TitleRow')[0];
  assert.equal(row.props.pagination.cursor, 'next-release');
  assert.equal(row.props.pagination.loading, false);
  row.props.pagination.onMore(); view = render();
  assert.deepEqual(fresh.pages, ['items']);
  assert.equal(all(view, 'Lockup')[0].props.title, 'Real artist', 'the page stays mounted while the serialized source fetches a shelf page');
  assert.equal(all(view, 'TitleRow')[0].props.pagination.loading, true);
  const final = {...albums, entries: [...albums.entries, {...album, id: 'second-release'}], nextCursor: ''};
  fresh.snapshot = {...fresh.snapshot, phase: 'ready', projection: {...fresh.snapshot.projection, sections: [final]}, appended: [final]};
  view = render();
  assert.equal(all(view, 'TitleRow')[0].props.entries.length, 2);
  assert.equal(all(view, 'TitleRow')[0].props.pagination.cursor, '');
  row.props.pagination.onMore(); await Promise.resolve();
  assert.deepEqual(fresh.pages, ['items'], 'a retained callback cannot advance a replacement projection');
});
