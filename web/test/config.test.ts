import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, mkdtempSync, mkdirSync, writeFileSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {runInNewContext} from 'node:vm';
import {aliases, assetNames, browserCheck, browserFloor, familyImports, kitFamilies} from '../scripts/config.mjs';
import {enUS} from '../../packages/i18n/src/catalog/en-US.ts';
import {routeFadeTiming} from '../src/platform/route-motion.ts';
import {gallerySearch} from '../src/platform/gallery-search.ts';
import {lintNextGlossary} from '../scripts/lint-next-glossary.mjs';
const read = (file: string) => readFileSync(new URL(file, import.meta.url), 'utf8');
test('build pins the existing browser floor, eight-character hashes and same-origin assets', () => {
  assert.deepEqual(browserFloor, ['chrome111', 'edge111', 'firefox113', 'safari16.4', 'ios16.4']);
  for (const value of Object.values(assetNames)) assert.match(value, /\[name\]-\[hash:8\]/);
  const config = read('../vite.config.ts'); assert.match(config, /assetsInlineLimit: 0/); assert.match(config, /port: 5174, strictPort: true/);
  assert.match(config, /127\.0\.0\.1:32500/); assert.match(config, /target: browserFloor, cssTarget: browserFloor/);
  assert.equal(JSON.parse(read('../package.json')).dependencies['hls.js'], 'file:../third_party/hls.js/hls.js-1.7.2-portico.1.tgz');
  const paths = JSON.parse(read('../tsconfig.json')).compilerOptions.paths;
  for (const [alias, target] of Object.entries(aliases)) assert.equal(new URL(paths[alias === '@' || alias === '@core' ? alias + '/*' : alias][0].replace(/\/\*$/, ''), new URL('../', import.meta.url)).pathname.replace(/\/$/, ''), target);
});
test('HTML guard precedes React and preserves the per-document nonce marker', () => {
  const html = read('../index.html'); assert.ok(html.indexOf('/browser-check.js') < html.indexOf('/src/main.tsx'));
  assert.match(html, /content="__PORTICO_STYLE_NONCE__"/); assert.match(read('../src/app-main.tsx'), /__webpack_nonce__/); assert.match(read('../src/services/main.tsx'), /__webpack_nonce__/);
  // The approved first paint needs a stylesheet in the document. It uses the
  // same per-response CSP nonce; style attributes and inline scripts stay forbidden.
  assert.doesNotMatch(html, /\sstyle=|<script> /);
  const inlineStyles = [...html.matchAll(/<style\b([^>]*)>/g)];
  assert.equal(inlineStyles.length, 1);
  assert.equal(inlineStyles[0][1].trim(), 'nonce="__PORTICO_STYLE_NONCE__"');
});
test('browser guard substitutes catalogue words and stops unsupported engines before app bootstrap', () => {
  let source = ''; browserCheck(enUS).generateBundle.call({emitFile: (file: {source: string}) => { source = file.source; }});
  assert.doesNotMatch(source, /__PORTICO_UNSUPPORTED/); assert.ok(source.includes(enUS['web.browser.unsupportedTitle']));
  assert.doesNotMatch(source.replace(/\/\*[\s\S]*?\*\//g, ''), /=>|\blet\b|\bconst\b|`/);
  const old = {window: {} as {__porticoUnsupportedBrowser?: boolean}, document: {readyState: 'loading', addEventListener() {}}}; runInNewContext(source, old); assert.equal(old.window.__porticoUnsupportedBrowser, true);
  const modern = {window: {}, CSS: {supports: () => true}, ResizeObserver: class {}, structuredClone, document: {createElement: () => ({noModule: false})}};
  runInNewContext(source, modern); assert.deepEqual(modern.window, {});
});
test('gallery discovery creates sorted on-demand public loaders and no production registrations', t => {
  const root = mkdtempSync(join(tmpdir(), 'portico-gallery-')); t.after(() => rmSync(root, {recursive: true, force: true}));
  const dir = join(root, 'src/dev/families'); mkdirSync(dir, {recursive: true});
  for (const name of ['states.tsx', 'controls.tsx', 'notes.md']) writeFileSync(join(dir, name), '');
  const source = familyImports(root);
  assert.ok(source.indexOf('["controls","/src/dev/families/controls.tsx"]') < source.indexOf('["states","/src/dev/families/states.tsx"]'));
  assert.match(source, /import\(\/\* @vite-ignore \*\/ url\)/);
  assert.doesNotMatch(source, /import\(["']\/src\/dev\/families\//);
  assert.doesNotMatch(source, /notes\.md/);
  const plugin = kitFamilies(); plugin.configResolved({root, command: 'build'});
  assert.match(plugin.load('\0virtual:portico-kit-families'), /^export default \[\];export async function loadGalleryInputs\(\)\{throw/);
  assert.match(read('../src/router.tsx'), /import\.meta\.env\.DEV\s*\?/);
});
test('capture query dimensions are bounded integral CSS pixels with safe identifiers', () => {
  assert.deepEqual(gallerySearch({family: 'controls', case: 'primary', capture: 'true', width: '390', height: '844'}), {family: 'controls', case: 'primary', capture: true, width: 390, height: 844});
  const bad = gallerySearch({family: '../../web', width: 'NaN', height: 1.5}); assert.equal(bad.family, undefined); assert.equal(bad.width, undefined); assert.equal(bad.height, undefined);
});
test('next catalogue glossary rejects nested family terms with no legacy exceptions', () => {
  assert.deepEqual(lintNextGlossary({'next.controls.save': 'My List'}), []);
  assert.ok(lintNextGlossary({'next.controls.bad': 'Favourites'}).length > 0);
  assert.ok(lintNextGlossary({'next.shell.brand': 'Portico Cloud'}).length > 0);
});

test('browser-floor route fallback keeps the short reduced-motion opacity fade', () => {
  assert.equal(routeFadeTiming(false).duration, 180);
  assert.equal(routeFadeTiming(true).duration, 160);
});
