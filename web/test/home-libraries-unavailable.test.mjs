import assert from 'node:assert/strict';
import test from 'node:test';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tree, all, one} from './helpers/interaction-harness.mjs';

// P2-53 walk-stopper: a library directory that did not answer is not "no libraries". Home still
// renders, says so in one quiet line with Try again, and never shows an empty-library state.
async function homeRoute(h, identity, retries) {
  const context = {isCurrent: () => true, session: () => ({viewer: {role: identity.role}}), facts: {identity}, retryLibraries: () => retries.push(1)};
  const captured = {context, binding: {home: {}}};
  const react = {...h.react, useEffect() {}};
  react.default = react;
  const file = new URL('../src/screens/home/Home.route.tsx', import.meta.url);
  const transformed = transformSync(file.pathname, await readFile(file, 'utf8'), {jsx: {runtime: 'classic'}});
  if (transformed.errors.length) throw new Error(JSON.stringify(transformed.errors));
  const inert = new Proxy(function () {}, {get: (_target, key) => key === 'then' ? undefined : inert, apply: () => undefined});
  const modules = new Proxy({react, '@i18n': {defaultI18n: {t: key => key}}, '../../route-registry-view': {useProtectedWebScreenContext: () => captured},
    '../browse/reading-library': {isReadingLibrary: () => false}, './Home': {HomeScreen: 'HomeScreen'}, './first-library-state': {FirstLibraryState: 'FirstLibraryState'},
    './member-empty-state': {MemberEmptyState: 'MemberEmptyState'}, '../../kit/overlays': {Banner: 'Banner'}, '../browse/music-discover-navigation': {MusicDiscoverMore: 'MusicDiscoverMore'}},
    {get: (target, key) => key in target ? target[key] : inert});
  const code = transformed.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g, (_match, names, source) => {
    const binding = names.trim().replace(/\bas\b/g, ':');
    if (binding.startsWith('{')) return `const ${binding} = modules[${JSON.stringify(source)}];`;
    return `const ${binding} = modules[${JSON.stringify(source)}].default;`;
  }).replace(/export default [\s\S]*?;\s*$/, '');
  const {HomeRouteScreen} = new Function('modules', 'const React = modules.react.default;\n' + code + '\nreturn {HomeRouteScreen};')(modules);
  return () => tree(h.render(() => HomeRouteScreen()));
}
const base = {libraries: [], liveAvailable: false, primaryProfile: true, capabilities: {manageServer: true}};

test('libraries that did not answer: Home renders with one quiet line and Try again, never an empty state', async () => {
  for (const role of ['owner', 'member']) {
    const h = createRenderer(), retries = [];
    try {
      const draw = await homeRoute(h, {...base, role, librariesUnavailable: true}, retries);
      const ui = draw();
      assert.equal(all(ui, 'FirstLibraryState').length + all(ui, 'MemberEmptyState').length, 0, `${role}: no empty state`);
      const home = one(ui, 'HomeScreen');
      assert.ok(home.props.notice, `${role}: the notice rides on Home`);
      const banner = tree(home.props.notice).find(node => node.type === 'Banner') ?? home.props.notice;
      banner.props.action.onAction();
      assert.equal(retries.length, 1, `${role}: Try again reads the directory again`);
    } finally {h.dispose();}
  }
});

test('a directory that answered empty still shows the empty states', async () => {
  const h = createRenderer();
  try {
    const owner = await homeRoute(h, {...base, role: 'owner'}, []);
    assert.equal(all(owner(), 'FirstLibraryState').length, 1);
  } finally {h.dispose();}
});
